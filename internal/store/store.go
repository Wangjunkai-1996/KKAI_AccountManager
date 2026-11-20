// Package store persists OpenAI login credentials and a redacted attempt history.
// Credentials and tokens are encrypted at rest with a per-installation AES-GCM key.
package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrAccountNotFound         = errors.New("account not found")
	ErrAccountBusy             = errors.New("account login already in progress")
	ErrAttemptNotFound         = errors.New("login attempt not found")
	ErrSub2ImportNotFound      = errors.New("sub2 import not found")
	ErrOAuthCredentialsMissing = errors.New("账号没有可导入的 OAuth token")
)

// Credentials are the sensitive values needed to log in again. They are never
// returned by ListAccounts or serialized by the history endpoint.
type Credentials struct {
	Email      string
	Password   string
	TOTPSecret string
	Proxy      string
	// ExistingAccountID makes BeginAttempt require this identity, preventing a
	// stale history login from recreating a deleted account. Zero allows creation.
	ExistingAccountID int64
	// SingleAttempt records explicit one-shot intent before the browser starts.
	SingleAttempt bool
}

// Result contains values returned by a successful OAuth login.
type Result struct {
	AccessToken      string
	RefreshToken     string
	ChatGPTAccountID string
	OrganizationID   string
	PlanType         string
	ExpiresAt        int64
	ExpiresIn        int
}

// Failure is the redacted login error persisted in history.
type Failure struct {
	Stage         string
	Code          string
	HTTPStatus    int
	Retryable     bool
	Message       string
	AccountStatus string
}

// Account contains safe account metadata. It never contains passwords, TOTP,
// access tokens, refresh tokens, or proxy credentials.
type Account struct {
	CredentialVersion    int64     `json:"credential_version"`
	ID                   int64     `json:"id"`
	Email                string    `json:"email"`
	Status               string    `json:"status"`
	Sub2Policy           string    `json:"sub2_policy"`
	ChatGPTAccountID     string    `json:"chatgpt_account_id,omitempty"`
	OrganizationID       string    `json:"organization_id,omitempty"`
	PlanType             string    `json:"plan_type,omitempty"`
	ExpiresAt            int64     `json:"expires_at,omitempty"`
	LastErrorCode        string    `json:"last_error_code,omitempty"`
	LastError            string    `json:"last_error,omitempty"`
	LastHTTPStatus       int       `json:"last_http_status,omitempty"`
	AttemptCount         int       `json:"attempt_count"`
	RecoveryCount        int       `json:"recovery_count"`
	RecoveryAttemptCount int       `json:"recovery_attempt_count"`
	LastAttemptAt        time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt        time.Time `json:"last_success_at,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// Attempt is a login attempt's safe metadata. Release must be called by the
// request owner after FinishAttempt, including on cancellation.
type Attempt struct {
	ID        int64
	AccountID int64
	Email     string
	StartedAt time.Time
	Release   func()
}

// AttemptRecord is a safe historical login event. It contains no credentials
// or token values.
type AttemptRecord struct {
	ID            int64     `json:"id"`
	AccountID     int64     `json:"account_id"`
	Email         string    `json:"email"`
	Status        string    `json:"status"`
	Stage         string    `json:"stage,omitempty"`
	Code          string    `json:"code,omitempty"`
	Message       string    `json:"message,omitempty"`
	HTTPStatus    int       `json:"http_status,omitempty"`
	Retryable     bool      `json:"retryable"`
	SingleAttempt bool      `json:"single_attempt"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at,omitempty"`
	DurationMS    int64     `json:"duration_ms"`
}

// Sub2Import describes a persisted AUTH -> Sub2API import task. It never
// exposes the encrypted payload or OAuth tokens to the browser.
type Sub2Import struct {
	ID                 int64     `json:"id"`
	DestinationKey     string    `json:"-"`
	AccountID          int64     `json:"account_id"`
	AccountEmail       string    `json:"email,omitempty"`
	CredentialVersion  int64     `json:"-"`
	OperationID        string    `json:"operation_id"`
	IdempotencyKey     string    `json:"-"`
	State              string    `json:"state"`
	CreateAcknowledged bool      `json:"create_acknowledged"`
	Sub2AccountID      int64     `json:"sub2_account_id,omitempty"`
	LastError          string    `json:"last_error,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type Store struct {
	db   *sql.DB
	aead cipher.AEAD

	activeMu sync.Mutex
	active   map[string]struct{}
}

// Open opens (or creates) the SQLite database and its separate 0600 key file.
// An empty keyPath uses dbPath + ".key".
func Open(dbPath, keyPath string) (*Store, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, errors.New("database path is required")
	}
	if keyPath == "" {
		keyPath = dbPath + ".key"
	}
	if dbPath != ":memory:" && !strings.HasPrefix(dbPath, "file:") {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	key, err := loadKey(keyPath)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create encryption cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create encryption mode: %w", err)
	}
	dsn := dbPath
	if dbPath == ":memory:" {
		dsn = "file:openai-login-memory?mode=memory&cache=shared"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// A single writer connection avoids SQLITE_BUSY while login workers finish
	// attempts. Browser work remains concurrent; only tiny metadata writes queue.
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA busy_timeout = 5000; PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure sqlite: %w", err)
	}
	s := &Store{db: db, aead: aead, active: make(map[string]struct{})}
	if err = s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.migrateAccountChecks(); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.migrateAccountRecoveryTasks(); err != nil {
		db.Close()
		return nil, err
	}
	if err = migrateAccountRecoveryGroupDecisions(s); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.migrateAccountDeliveries(); err != nil {
		db.Close()
		return nil, err
	}
	if dbPath != ":memory:" && !strings.HasPrefix(dbPath, "file:") {
		if err := protectSQLiteFiles(dbPath); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err = s.verifyKey(); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.retireLifecycleDeliveries(); err != nil {
		db.Close()
		return nil, fmt.Errorf("retire legacy lifecycle deliveries: %w", err)
	}
	if err = s.convergeTerminalCredentials(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.convergeDiscardedAttempts(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.RecoverInterrupted(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.RecoverSub2Imports(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.RecoverAccountChecks(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.RecoverAccountRecoveryTasks(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func protectSQLiteFiles(dbPath string) error {
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if err := os.Chmod(path, 0600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("protect sqlite file %s: %w", path, err)
		}
	}
	return nil
}

func loadKey(path string) ([]byte, error) {
	if path == ":memory:" {
		path = filepath.Join(os.TempDir(), "openai-login-test.key")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create key directory: %w", err)
	}
	if key, err := os.ReadFile(path); err == nil {
		if len(key) != 32 {
			return nil, fmt.Errorf("encryption key %s has invalid length", path)
		}
		if err := os.Chmod(path, 0600); err != nil {
			return nil, fmt.Errorf("protect encryption key: %w", err)
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read encryption key: %w", err)
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate encryption key: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return loadKey(path)
		}
		return nil, fmt.Errorf("create encryption key: %w", err)
	}
	if _, err = f.Write(key); err != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("write encryption key: %w", err)
	}
	if err = f.Close(); err != nil {
		return nil, fmt.Errorf("close encryption key: %w", err)
	}
	return key, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS accounts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  email TEXT NOT NULL UNIQUE,
  password_cipher BLOB NOT NULL,
  totp_cipher BLOB,
  proxy_cipher BLOB,
  access_token_cipher BLOB,
  refresh_token_cipher BLOB,
  chatgpt_account_id TEXT,
  organization_id TEXT,
  plan_type TEXT,
  expires_at INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'new',
	sub2_policy TEXT NOT NULL DEFAULT 'unknown',
  last_error_code TEXT,
  last_error TEXT,
  last_http_status INTEGER NOT NULL DEFAULT 0,
  attempt_count INTEGER NOT NULL DEFAULT 0,
  last_success_at INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS login_attempts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  status TEXT NOT NULL,
  stage TEXT,
  error_code TEXT,
  error_message TEXT,
  http_status INTEGER NOT NULL DEFAULT 0,
  retryable INTEGER NOT NULL DEFAULT 0,
  single_attempt INTEGER NOT NULL DEFAULT 0,
	plan_type TEXT,
	chatgpt_account_id TEXT,
	organization_id TEXT,
	observation_source TEXT,
  started_at INTEGER NOT NULL,
  finished_at INTEGER NOT NULL DEFAULT 0,
  duration_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS login_attempts_account_started ON login_attempts(account_id, started_at DESC);
CREATE TABLE IF NOT EXISTS account_lifecycle_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id INTEGER,
  email TEXT NOT NULL,
  source TEXT NOT NULL,
  status TEXT NOT NULL,
  reason TEXT,
  code TEXT,
  http_status INTEGER NOT NULL DEFAULT 0,
  dedupe_key TEXT NOT NULL UNIQUE,
  observed_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS account_lifecycle_events_account_observed ON account_lifecycle_events(account_id, observed_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS account_lifecycle_events_email_observed ON account_lifecycle_events(email, observed_at DESC, id DESC);
CREATE TABLE IF NOT EXISTS lifecycle_request_bindings (
  request_key TEXT NOT NULL,
  binding_id INTEGER NOT NULL,
  account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  sub2_id INTEGER NOT NULL,
  destination TEXT NOT NULL,
  operation TEXT NOT NULL,
  binding_version INTEGER NOT NULL,
  source_version INTEGER NOT NULL,
  PRIMARY KEY(request_key,binding_id)
);
CREATE TABLE IF NOT EXISTS store_meta (
  key TEXT PRIMARY KEY,
  value_cipher BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS sub2_imports (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  destination_key TEXT NOT NULL,
  account_id INTEGER NOT NULL,
  credential_version INTEGER NOT NULL DEFAULT 0,
  operation_id TEXT NOT NULL UNIQUE,
  idempotency_key TEXT NOT NULL UNIQUE,
  payload_cipher BLOB NOT NULL,
  state TEXT NOT NULL,
  create_acknowledged INTEGER NOT NULL DEFAULT 0,
  sub2_account_id INTEGER,
  last_error TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE(destination_key, account_id)
);
CREATE INDEX IF NOT EXISTS sub2_imports_state_updated ON sub2_imports(state, updated_at);
`)
	if err != nil {
		return fmt.Errorf("migrate sqlite: %w", err)
	}
	// Older databases predate the credential version guard. Keep their
	// existing tasks usable while binding them to the latest known successful
	// login before the worker is allowed to send them.
	rows, err := s.db.Query(`PRAGMA table_info(sub2_imports)`)
	if err != nil {
		return fmt.Errorf("inspect sub2 imports migration: %w", err)
	}
	hasCredentialVersion := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return fmt.Errorf("read sub2 imports migration: %w", err)
		}
		if name == "credential_version" {
			hasCredentialVersion = true
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close sub2 imports migration: %w", err)
	}
	if !hasCredentialVersion {
		if _, err := s.db.Exec(`ALTER TABLE sub2_imports ADD COLUMN credential_version INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add sub2 import credential version: %w", err)
		}
	}
	var hasSingleAttempt int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('login_attempts') WHERE name='single_attempt'`).Scan(&hasSingleAttempt); err != nil {
		return fmt.Errorf("inspect login attempt intent: %w", err)
	}
	if hasSingleAttempt == 0 {
		if _, err := s.db.Exec(`ALTER TABLE login_attempts ADD COLUMN single_attempt INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add login attempt intent: %w", err)
		}
	}
	for _, column := range []struct {
		name string
		ddl  string
	}{
		{name: "sub2_policy", ddl: "ALTER TABLE accounts ADD COLUMN sub2_policy TEXT NOT NULL DEFAULT 'unknown'"},
		{name: "plan_type", ddl: "ALTER TABLE login_attempts ADD COLUMN plan_type TEXT"},
		{name: "chatgpt_account_id", ddl: "ALTER TABLE login_attempts ADD COLUMN chatgpt_account_id TEXT"},
		{name: "organization_id", ddl: "ALTER TABLE login_attempts ADD COLUMN organization_id TEXT"},
		{name: "observation_source", ddl: "ALTER TABLE login_attempts ADD COLUMN observation_source TEXT"},
	} {
		var exists int
		table := "accounts"
		if column.name != "sub2_policy" {
			table = "login_attempts"
		}
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, column.name).Scan(&exists); err != nil {
			return fmt.Errorf("inspect %s.%s migration: %w", table, column.name, err)
		}
		if exists == 0 {
			if _, err := s.db.Exec(column.ddl); err != nil {
				return fmt.Errorf("add %s.%s: %w", table, column.name, err)
			}
		}
	}
	_, err = s.db.Exec(`UPDATE login_attempts AS la SET
		plan_type=a.plan_type,chatgpt_account_id=a.chatgpt_account_id,organization_id=a.organization_id,observation_source='legacy_oauth_login'
		FROM accounts a WHERE la.account_id=a.id AND la.status='success' AND la.observation_source IS NULL
		AND trim(COALESCE(la.plan_type,''))='' AND COALESCE(la.stage,'')<>'token_refresh'
		AND trim(COALESCE(a.plan_type,''))<>'' AND la.finished_at=a.last_success_at AND a.last_success_at>0
		AND la.id=(SELECT MAX(id) FROM login_attempts WHERE account_id=a.id AND status='success')
		AND NOT EXISTS (SELECT 1 FROM login_attempts WHERE account_id=a.id AND observation_source IS NOT NULL)`)
	return err
}

func (s *Store) verifyKey() error {
	const marker = "openai-login-store-key-v1"
	var encrypted []byte
	err := s.db.QueryRow(`SELECT value_cipher FROM store_meta WHERE key='encryption_check'`).Scan(&encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		value, sealErr := s.seal(marker)
		if sealErr != nil {
			return sealErr
		}
		if _, err = s.db.Exec(`INSERT INTO store_meta(key,value_cipher) VALUES('encryption_check',?)`, value); err != nil {
			return fmt.Errorf("save encryption key check: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read encryption key check: %w", err)
	}
	value, err := s.open(encrypted)
	if err != nil || value != marker {
		return errors.New("database encryption key does not match the existing database")
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Ping verifies that the backing database is still reachable. It is used by
// the readiness endpoint; liveness does not depend on this check.
func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("store is not initialized")
	}
	return s.db.PingContext(ctx)
}

// RecoverInterrupted makes crashes visible in account history instead of
// leaving an account permanently in the running state.
func (s *Store) RecoverInterrupted(ctx context.Context) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `UPDATE login_attempts SET status='interrupted', finished_at=?, duration_ms=CASE WHEN started_at > 0 THEN MAX(0, (? - started_at) * 1000) ELSE 0 END WHERE status='running'`, now, now)
	if err != nil {
		return fmt.Errorf("recover interrupted attempts: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `UPDATE accounts SET status=CASE WHEN status='running' THEN 'interrupted' ELSE status END, updated_at=? WHERE status='running'`, now)
	return err
}

// RecoverSub2Imports converts in-flight tasks to unknown after a process
// restart. Unknown means the POST result was not observed; the worker must
// reconcile by marker before it is ever allowed to send another POST.
func (s *Store) RecoverSub2Imports(ctx context.Context) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `UPDATE sub2_imports SET state='unknown',updated_at=? WHERE state IN ('sending','confirming')`, now)
	if err != nil {
		return fmt.Errorf("recover sub2 imports: %w", err)
	}
	return nil
}

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func (s *Store) seal(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, []byte(value), nil), nil
}

func (s *Store) open(value []byte) (string, error) {
	if len(value) == 0 {
		return "", nil
	}
	n := s.aead.NonceSize()
	if len(value) < n {
		return "", errors.New("encrypted value is truncated")
	}
	plain, err := s.aead.Open(nil, value[:n], value[n:], nil)
	if err != nil {
		return "", errors.New("encrypted value authentication failed")
	}
	return string(plain), nil
}

// UpsertCredentials stores credentials while retaining the account's status
// and historical result when the same email is entered again.
func (s *Store) UpsertCredentials(ctx context.Context, credentials Credentials) (Account, error) {
	credentials.Email = normalizeEmail(credentials.Email)
	if credentials.Email == "" || credentials.Password == "" {
		return Account{}, errors.New("email and password are required")
	}
	password, err := s.seal(credentials.Password)
	if err != nil {
		return Account{}, err
	}
	totp, err := s.seal(credentials.TOTPSecret)
	if err != nil {
		return Account{}, err
	}
	proxy, err := s.seal(credentials.Proxy)
	if err != nil {
		return Account{}, err
	}
	now := time.Now().Unix()
	result, err := s.db.ExecContext(ctx, `INSERT INTO accounts(email,password_cipher,totp_cipher,proxy_cipher,created_at,updated_at) VALUES(?,?,?,?,?,?)
ON CONFLICT(email) DO UPDATE SET password_cipher=excluded.password_cipher, totp_cipher=excluded.totp_cipher, proxy_cipher=excluded.proxy_cipher, updated_at=excluded.updated_at
WHERE accounts.status NOT IN ('deleted','deactivated','deleted_or_deactivated','discarded') AND accounts.sub2_policy NOT IN ('official_unavailable','discarded')`, credentials.Email, password, totp, proxy, now, now)
	if err != nil {
		return Account{}, fmt.Errorf("save account: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return Account{}, err
	} else if count == 0 {
		account, err := s.GetAccount(ctx, credentials.Email)
		if err != nil {
			return Account{}, err
		}
		return Account{}, account.terminalError()
	}
	return s.GetAccount(ctx, credentials.Email)
}

// BeginAttempt atomically replaces an account's credentials and starts its
// login attempt. Acquiring the per-email lock before the upsert closes the
// delete/reinsert race between a finishing old login and a new request.
func (s *Store) BeginAttempt(ctx context.Context, credentials Credentials) (Account, Attempt, error) {
	credentials.Email = normalizeEmail(credentials.Email)
	if credentials.Email == "" || credentials.Password == "" {
		return Account{}, Attempt{}, errors.New("email and password are required")
	}
	s.activeMu.Lock()
	if _, exists := s.active[credentials.Email]; exists {
		s.activeMu.Unlock()
		return Account{}, Attempt{}, ErrAccountBusy
	}
	s.active[credentials.Email] = struct{}{}
	s.activeMu.Unlock()
	release := sync.OnceFunc(func() {
		s.activeMu.Lock()
		delete(s.active, credentials.Email)
		s.activeMu.Unlock()
	})
	return s.beginAttemptLocked(ctx, credentials, release)
}

// beginAttemptLocked is shared with recovery, whose lease already owns the
// per-email lock for the entire remote recovery operation.
func (s *Store) beginAttemptLocked(ctx context.Context, credentials Credentials, release func()) (Account, Attempt, error) {
	password, err := s.seal(credentials.Password)
	if err != nil {
		release()
		return Account{}, Attempt{}, err
	}
	totp, err := s.seal(credentials.TOTPSecret)
	if err != nil {
		release()
		return Account{}, Attempt{}, err
	}
	proxy, err := s.seal(credentials.Proxy)
	if err != nil {
		release()
		return Account{}, Attempt{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		release()
		return Account{}, Attempt{}, err
	}
	defer tx.Rollback()
	var existingID int64
	var existingStatus, existingPolicy string
	existingErr := tx.QueryRowContext(ctx, `SELECT id,status,COALESCE(sub2_policy,'unknown') FROM accounts WHERE email=?`, credentials.Email).Scan(&existingID, &existingStatus, &existingPolicy)
	if existingErr == nil {
		if credentials.ExistingAccountID > 0 && existingID != credentials.ExistingAccountID {
			release()
			return Account{}, Attempt{}, ErrAccountNotFound
		}
		if err := (Account{Status: existingStatus, Sub2Policy: existingPolicy}).terminalError(); err != nil {
			release()
			return Account{}, Attempt{}, err
		}
	} else if !errors.Is(existingErr, sql.ErrNoRows) {
		release()
		return Account{}, Attempt{}, existingErr
	} else if credentials.ExistingAccountID > 0 {
		release()
		return Account{}, Attempt{}, ErrAccountNotFound
	}
	now := time.Now().Unix()
	if _, err = tx.ExecContext(ctx, `INSERT INTO accounts(email,password_cipher,totp_cipher,proxy_cipher,created_at,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(email) DO UPDATE SET password_cipher=excluded.password_cipher,totp_cipher=excluded.totp_cipher,proxy_cipher=excluded.proxy_cipher,updated_at=excluded.updated_at`, credentials.Email, password, totp, proxy, now, now); err != nil {
		release()
		return Account{}, Attempt{}, err
	}
	var accountID, attemptID int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE email=?`, credentials.Email).Scan(&accountID); err != nil {
		release()
		return Account{}, Attempt{}, err
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO login_attempts(account_id,status,started_at,single_attempt) VALUES(?,'running',?,?) RETURNING id`, accountID, now, boolInt(credentials.SingleAttempt)).Scan(&attemptID); err != nil {
		release()
		return Account{}, Attempt{}, err
	}
	if err = consumeCredentialRepairRecoveryTx(ctx, tx, accountID, attemptID); err != nil {
		release()
		return Account{}, Attempt{}, err
	}
	if err = freezeLifecycleBindingsTx(ctx, tx, fmt.Sprintf("login_attempt:%d", attemptID), accountID); err != nil {
		release()
		return Account{}, Attempt{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE accounts SET status='running',attempt_count=attempt_count+1,updated_at=? WHERE id=?`, now, accountID); err != nil {
		release()
		return Account{}, Attempt{}, err
	}
	if err = tx.Commit(); err != nil {
		release()
		return Account{}, Attempt{}, err
	}
	account, err := s.GetAccount(context.Background(), credentials.Email)
	if err != nil {
		release()
		return Account{}, Attempt{}, err
	}
	return account, Attempt{ID: attemptID, AccountID: accountID, Email: credentials.Email, StartedAt: time.Unix(now, 0), Release: release}, nil
}

func (s *Store) GetCredentials(ctx context.Context, email string) (Credentials, error) {
	return s.getCredentials(ctx, `WHERE email=?`, normalizeEmail(email))
}

func (s *Store) GetCredentialsByID(ctx context.Context, id int64) (Credentials, error) {
	return s.getCredentials(ctx, `WHERE id=?`, id)
}

// GetOAuthResultByID reads only the OAuth material written by a successful
// login. Passwords, TOTP secrets and login proxies are deliberately excluded.
func (s *Store) GetOAuthResultByID(ctx context.Context, id int64) (string, Result, error) {
	var email string
	var access, refresh []byte
	var result Result
	var status, policy string
	err := s.db.QueryRowContext(ctx, `SELECT email,status,COALESCE(sub2_policy,'unknown'),access_token_cipher,refresh_token_cipher,COALESCE(chatgpt_account_id,''),COALESCE(organization_id,''),COALESCE(plan_type,''),expires_at FROM accounts WHERE id=?`, id).Scan(&email, &status, &policy, &access, &refresh, &result.ChatGPTAccountID, &result.OrganizationID, &result.PlanType, &result.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", Result{}, ErrAccountNotFound
	}
	if err != nil {
		return "", Result{}, err
	}
	if err := (Account{Status: status, Sub2Policy: policy}).terminalError(); err != nil {
		return email, Result{}, err
	}
	var openErr error
	if result.AccessToken, openErr = s.open(access); openErr != nil {
		return "", Result{}, openErr
	}
	if result.RefreshToken, openErr = s.open(refresh); openErr != nil {
		return "", Result{}, openErr
	}
	if result.AccessToken == "" || result.RefreshToken == "" {
		return email, result, ErrOAuthCredentialsMissing
	}
	return email, result, nil
}

// UpdateOAuthResultByID replaces the encrypted OAuth tokens after a refresh.
// Empty identity fields from a refresh response preserve the existing values.
func (s *Store) UpdateOAuthResultByID(ctx context.Context, id int64, result Result) error {
	account, err := s.GetAccountByID(ctx, id)
	if err != nil {
		return err
	}
	if err := account.terminalError(); err != nil {
		return err
	}
	lease, err := s.AcquireAccountRecovery(ctx, id)
	if err != nil {
		return err
	}
	defer lease.Release()
	_, err = lease.UpdateOAuthResult(ctx, 0, result)
	return err
}

func scanSub2Import(row scanner) (Sub2Import, error) {
	var item Sub2Import
	var created, updated int64
	var acknowledged, sub2ID sql.NullInt64
	if err := row.Scan(&item.ID, &item.DestinationKey, &item.AccountID, &item.AccountEmail, &item.CredentialVersion, &item.OperationID, &item.IdempotencyKey, &item.State, &acknowledged, &sub2ID, &item.LastError, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Sub2Import{}, ErrSub2ImportNotFound
		}
		return Sub2Import{}, err
	}
	item.CreateAcknowledged = acknowledged.Valid && acknowledged.Int64 != 0
	if sub2ID.Valid {
		item.Sub2AccountID = sub2ID.Int64
	}
	item.CreatedAt, item.UpdatedAt = unixTime(created), unixTime(updated)
	return item, nil
}

const sub2ImportSelect = `SELECT i.id,i.destination_key,i.account_id,COALESCE(a.email,''),i.credential_version,i.operation_id,i.idempotency_key,i.state,i.create_acknowledged,i.sub2_account_id,COALESCE(i.last_error,''),i.created_at,i.updated_at FROM sub2_imports i LEFT JOIN accounts a ON a.id=i.account_id`

// CreateOrGetSub2Import creates one durable task per destination/account.
// The bool reports whether a new row was inserted.
func (s *Store) CreateOrGetSub2Import(ctx context.Context, destinationKey string, accountID int64, operationID, idempotencyKey string, payload []byte) (Sub2Import, bool, error) {
	if strings.TrimSpace(destinationKey) == "" || accountID <= 0 || strings.TrimSpace(operationID) == "" || strings.TrimSpace(idempotencyKey) == "" || len(payload) == 0 {
		return Sub2Import{}, false, errors.New("invalid sub2 import task")
	}
	account, err := s.GetAccountByID(ctx, accountID)
	if err != nil {
		return Sub2Import{}, false, err
	}
	if err := account.Sub2EligibilityError(); err != nil {
		return Sub2Import{}, false, err
	}
	if !IsSub2PaidPlan(account.PlanType) {
		return Sub2Import{}, false, ErrSub2PlanIneligible
	}
	missing, err := s.Sub2AccountMissing(ctx, destinationKey, accountID)
	if err != nil {
		return Sub2Import{}, false, err
	}
	if missing {
		return Sub2Import{}, false, ErrSub2AccountMissing
	}
	ciphertext, err := s.seal(string(payload))
	if err != nil {
		return Sub2Import{}, false, err
	}
	now := time.Now().Unix()
	// The account can be deleted after the caller builds its OAuth snapshot.
	// Check existence in the insert itself so a stale request cannot orphan a task.
	result, err := s.db.ExecContext(ctx, `INSERT INTO sub2_imports(destination_key,account_id,credential_version,operation_id,idempotency_key,payload_cipher,state,created_at,updated_at) SELECT ?,id,COALESCE((SELECT MAX(la.id) FROM login_attempts la WHERE la.account_id=accounts.id AND la.status='success'),0),?,?,?,'queued',?,? FROM accounts WHERE id=? AND status<>'discarded' AND sub2_policy<>'discarded' ON CONFLICT(destination_key,account_id) DO NOTHING`, destinationKey, operationID, idempotencyKey, ciphertext, now, now, accountID)
	if err != nil {
		return Sub2Import{}, false, fmt.Errorf("create sub2 import: %w", err)
	}
	inserted, _ := result.RowsAffected()
	item, err := s.GetSub2Import(ctx, destinationKey, accountID)
	if errors.Is(err, ErrSub2ImportNotFound) {
		return Sub2Import{}, false, ErrAccountNotFound
	}
	return item, inserted > 0, err
}

func (s *Store) GetSub2Import(ctx context.Context, destinationKey string, accountID int64) (Sub2Import, error) {
	return scanSub2Import(s.db.QueryRowContext(ctx, sub2ImportSelect+` WHERE i.destination_key=? AND i.account_id=?`, destinationKey, accountID))
}

func (s *Store) GetSub2ImportByID(ctx context.Context, id int64) (Sub2Import, error) {
	return scanSub2Import(s.db.QueryRowContext(ctx, sub2ImportSelect+` WHERE i.id=?`, id))
}

func (s *Store) ListSub2ImportsForRemoteID(ctx context.Context, destination string, remoteID int64) ([]Sub2Import, error) {
	rows, err := s.db.QueryContext(ctx, sub2ImportSelect+` WHERE i.destination_key=? AND i.sub2_account_id=?`, destination, remoteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var bindings []Sub2Import
	for rows.Next() {
		binding, err := scanSub2Import(rows)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

func (s *Store) ListSub2ImportStatuses(ctx context.Context) ([]Sub2Import, error) {
	rows, err := s.db.QueryContext(ctx, sub2ImportSelect+` ORDER BY i.updated_at DESC,i.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Sub2Import, 0)
	for rows.Next() {
		item, err := scanSub2Import(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListQueuedSub2Imports(ctx context.Context, limit int) ([]Sub2Import, error) {
	return s.ListQueuedSub2ImportsForDestination(ctx, "", limit)
}

func (s *Store) ListQueuedSub2ImportsForDestination(ctx context.Context, destination string, limit int) ([]Sub2Import, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	destination = strings.TrimSpace(destination)
	rows, err := s.db.QueryContext(ctx, sub2ImportSelect+` WHERE i.state='queued' AND (?='' OR i.destination_key=?) ORDER BY i.created_at ASC,i.id ASC LIMIT ?`, destination, destination, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Sub2Import, 0)
	for rows.Next() {
		item, err := scanSub2Import(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListPendingSub2Cleanup(ctx context.Context, destination string, limit int) ([]Sub2Import, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	destination = strings.TrimSpace(destination)
	// Keep a transient cleanup failure from monopolizing the importer. Manual
	// conflicts use a separate state and are intentionally excluded.
	due := time.Now().Add(-30 * time.Second).Unix()
	rows, err := s.db.QueryContext(ctx, sub2ImportSelect+` WHERE i.state='cleanup_pending' AND i.updated_at<=? AND (?='' OR i.destination_key=?) ORDER BY i.updated_at ASC,i.id ASC LIMIT ?`, due, destination, destination, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Sub2Import, 0)
	for rows.Next() {
		item, err := scanSub2Import(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const sub2ImportNotDiscarded = ` AND NOT EXISTS (SELECT 1 FROM accounts a WHERE a.id=sub2_imports.account_id AND (a.status='discarded' OR a.sub2_policy='discarded'))`

func (s *Store) GetSub2ImportPayload(ctx context.Context, id int64) ([]byte, error) {
	var ciphertext []byte
	if err := s.db.QueryRowContext(ctx, `SELECT payload_cipher FROM sub2_imports WHERE id=?`+sub2ImportNotDiscarded, id).Scan(&ciphertext); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSub2ImportNotFound
	} else if err != nil {
		return nil, err
	}
	plain, err := s.open(ciphertext)
	if err != nil {
		return nil, err
	}
	return []byte(plain), nil
}

func (s *Store) MarkSub2ImportSending(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE sub2_imports SET state='sending',updated_at=? WHERE id=? AND state='queued'`+sub2ImportNotDiscarded, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return errors.New("sub2 import is not queued")
	}
	return nil
}

func (s *Store) MarkSub2ImportSendingForVersion(ctx context.Context, id, credentialVersion int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE sub2_imports SET state='sending',updated_at=? WHERE id=? AND state='queued' AND credential_version=?`+sub2ImportNotDiscarded, time.Now().Unix(), id, credentialVersion)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return errors.New("sub2 import is not queued for the current credentials")
	}
	return nil
}

func (s *Store) UpdateSub2Import(ctx context.Context, id int64, state string, acknowledged bool, sub2AccountID int64, lastError string) error {
	return s.updateSub2Import(ctx, id, nil, state, acknowledged, sub2AccountID, lastError)
}

// Worker results carry the operation observed before making a remote request.
func (s *Store) UpdateSub2ImportForOperation(ctx context.Context, binding Sub2Import, state string, acknowledged bool, sub2AccountID int64, lastError string) error {
	return s.updateSub2Import(ctx, binding.ID, &binding.OperationID, state, acknowledged, sub2AccountID, lastError)
}

func (s *Store) updateSub2Import(ctx context.Context, id int64, operation *string, state string, acknowledged bool, sub2AccountID int64, lastError string) error {
	state = strings.TrimSpace(state)
	if state == "" || state == Sub2RemoteMissing {
		return errors.New("invalid sub2 import state")
	}
	query := `UPDATE sub2_imports SET state=?,create_acknowledged=?,sub2_account_id=CASE WHEN ? > 0 THEN ? ELSE sub2_account_id END,last_error=?,updated_at=? WHERE id=? AND state<>'remote_missing'` + sub2ImportNotDiscarded
	args := []any{state, boolInt(acknowledged), sub2AccountID, sub2AccountID, strings.TrimSpace(lastError), time.Now().Unix(), id}
	if operation != nil {
		query += " AND operation_id=?"
		args = append(args, *operation)
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrAccountRecoveryStale
	}
	return nil
}

func (s *Store) MarkSub2ImportSendingForOperation(ctx context.Context, binding Sub2Import) error {
	result, err := s.db.ExecContext(ctx, `UPDATE sub2_imports SET state='sending',updated_at=? WHERE id=? AND operation_id=? AND credential_version=? AND state='queued'`+sub2ImportNotDiscarded, time.Now().Unix(), binding.ID, binding.OperationID, binding.CredentialVersion)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrAccountRecoveryStale
	}
	return nil
}

// MarkSub2ImportCleanupPending records the credential version that produced
// the deletion observation. A later successful login can then supersede the
// retry without allowing an obsolete cleanup to delete the new account.
func (s *Store) MarkSub2ImportCleanupPending(ctx context.Context, id, credentialVersion, sub2AccountID int64, lastError string) error {
	if id <= 0 || credentialVersion < 0 || sub2AccountID <= 0 {
		return errors.New("invalid Sub2 cleanup task")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE sub2_imports SET state='cleanup_pending',credential_version=?,create_acknowledged=1,sub2_account_id=?,last_error=?,updated_at=? WHERE id=? AND state IN ('imported','cleanup_pending')`+sub2ImportNotDiscarded, credentialVersion, sub2AccountID, strings.TrimSpace(lastError), time.Now().Unix(), id)
	return err
}

func (s *Store) RequeueSub2Import(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sub2_imports SET state='queued',last_error='',updated_at=? WHERE id=? AND state='failed'`+sub2ImportNotDiscarded, time.Now().Unix(), id)
	return err
}

// RefreshSub2ImportPayload replaces the encrypted OAuth snapshot before a
// definitive failure is retried. The operation and idempotency keys remain
// unchanged so a retry cannot create a second source marker.
func (s *Store) RefreshSub2ImportPayload(ctx context.Context, id int64, payload []byte) error {
	if len(payload) == 0 {
		return errors.New("sub2 import payload is required")
	}
	ciphertext, err := s.seal(string(payload))
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE sub2_imports SET payload_cipher=?,state='queued',last_error='',updated_at=? WHERE id=? AND state='failed'`+sub2ImportNotDiscarded, ciphertext, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return errors.New("sub2 import is not failed")
	}
	return nil
}

func (s *Store) RefreshSub2ImportPayloadForVersion(ctx context.Context, id, credentialVersion int64, requireVersion bool, payload []byte) error {
	if len(payload) == 0 {
		return errors.New("sub2 import payload is required")
	}
	ciphertext, err := s.seal(string(payload))
	if err != nil {
		return err
	}
	query := `UPDATE sub2_imports SET payload_cipher=?,credential_version=?,state='queued',last_error='',updated_at=? WHERE id=? AND state IN ('failed','queued')` + sub2ImportNotDiscarded
	args := []any{ciphertext, credentialVersion, time.Now().Unix(), id}
	if requireVersion {
		query += ` AND credential_version<>?`
		args = append(args, credentialVersion)
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return errors.New("sub2 import is not failed")
	}
	return nil
}

func (s *Store) getCredentials(ctx context.Context, where string, arg interface{}) (Credentials, error) {
	var account Account
	var password, totp, proxy []byte
	err := s.db.QueryRowContext(ctx, `SELECT email,status,sub2_policy,password_cipher,totp_cipher,proxy_cipher FROM accounts `+where, arg).Scan(&account.Email, &account.Status, &account.Sub2Policy, &password, &totp, &proxy)
	if errors.Is(err, sql.ErrNoRows) {
		return Credentials{}, ErrAccountNotFound
	}
	if err != nil {
		return Credentials{}, err
	}
	if err := account.terminalError(); err != nil {
		return Credentials{}, err
	}
	p, err := s.open(password)
	if err != nil {
		return Credentials{}, err
	}
	t, err := s.open(totp)
	if err != nil {
		return Credentials{}, err
	}
	pr, err := s.open(proxy)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{Email: account.Email, Password: p, TOTPSecret: t, Proxy: pr}, nil
}

func (s *Store) GetAccount(ctx context.Context, email string) (Account, error) {
	row := s.db.QueryRowContext(ctx, accountSelect+` WHERE email=?`, normalizeEmail(email))
	return scanAccount(row)
}

func (s *Store) GetAccountByID(ctx context.Context, id int64) (Account, error) {
	row := s.db.QueryRowContext(ctx, accountSelect+` WHERE id=?`, id)
	return scanAccount(row)
}

const accountSelect = `SELECT id,email,status,COALESCE(sub2_policy,'unknown'),COALESCE(chatgpt_account_id,''),COALESCE(organization_id,''),COALESCE(plan_type,''),expires_at,COALESCE(last_error_code,''),COALESCE(last_error,''),last_http_status,attempt_count,(SELECT COALESCE(MAX(started_at),0) FROM login_attempts la WHERE la.account_id=accounts.id),last_success_at,created_at,updated_at,COALESCE((SELECT COUNT(*) FROM account_recovery_tasks rt WHERE rt.account_id=accounts.id AND rt.purpose='recovery'),0),COALESCE((SELECT COUNT(*) FROM account_recovery_tasks rt WHERE rt.account_id=accounts.id AND rt.purpose='recovery' AND rt.state='completed'),0),(SELECT COALESCE(MAX(la.id),0) FROM login_attempts la WHERE la.account_id=accounts.id AND la.status='success') FROM accounts`

type scanner interface{ Scan(...interface{}) error }

func scanAccount(row scanner) (Account, error) {
	var a Account
	var lastAttempt, lastSuccess, created, updated int64
	if err := row.Scan(&a.ID, &a.Email, &a.Status, &a.Sub2Policy, &a.ChatGPTAccountID, &a.OrganizationID, &a.PlanType, &a.ExpiresAt, &a.LastErrorCode, &a.LastError, &a.LastHTTPStatus, &a.AttemptCount, &lastAttempt, &lastSuccess, &created, &updated, &a.RecoveryAttemptCount, &a.RecoveryCount, &a.CredentialVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Account{}, ErrAccountNotFound
		}
		return Account{}, err
	}
	a.LastAttemptAt = unixTime(lastAttempt)
	a.LastSuccessAt = unixTime(lastSuccess)
	a.CreatedAt = unixTime(created)
	a.UpdatedAt = unixTime(updated)
	return a, nil
}

func unixTime(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(value, 0)
}

func (s *Store) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, accountSelect+` ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := make([]Account, 0)
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

// ListAttempts returns the newest historical events for one account. A limit
// of zero uses the bounded default; this prevents an unbounded history query.
func (s *Store) ListAttempts(ctx context.Context, email string, limit int) ([]AttemptRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `SELECT la.id,la.account_id,a.email,la.status,COALESCE(la.stage,''),COALESCE(la.error_code,''),COALESCE(la.error_message,''),la.http_status,la.retryable,la.started_at,la.finished_at,la.duration_ms,la.single_attempt FROM login_attempts la JOIN accounts a ON a.id=la.account_id WHERE a.email=? ORDER BY la.started_at DESC,la.id DESC LIMIT ?`, normalizeEmail(email), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AttemptRecord, 0)
	for rows.Next() {
		var record AttemptRecord
		var started, finished int64
		var retryable, singleAttempt int
		if err := rows.Scan(&record.ID, &record.AccountID, &record.Email, &record.Status, &record.Stage, &record.Code, &record.Message, &record.HTTPStatus, &retryable, &started, &finished, &record.DurationMS, &singleAttempt); err != nil {
			return nil, err
		}
		record.Retryable = retryable != 0
		record.SingleAttempt = singleAttempt != 0
		record.StartedAt, record.FinishedAt = unixTime(started), unixTime(finished)
		result = append(result, record)
	}
	return result, rows.Err()
}

// StartAttempt acquires a process-local per-email lock and records running
// state. A second tab for the same email receives ErrAccountBusy.
func (s *Store) StartAttempt(ctx context.Context, email string) (Attempt, error) {
	email = normalizeEmail(email)
	if email == "" {
		return Attempt{}, errors.New("email is required")
	}
	s.activeMu.Lock()
	if _, exists := s.active[email]; exists {
		s.activeMu.Unlock()
		return Attempt{}, ErrAccountBusy
	}
	s.active[email] = struct{}{}
	s.activeMu.Unlock()
	release := func() {
		s.activeMu.Lock()
		delete(s.active, email)
		s.activeMu.Unlock()
	}
	var id, accountID int64
	var status, policy string
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		release()
		return Attempt{}, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT id,status,COALESCE(sub2_policy,'unknown') FROM accounts WHERE email=?`, email).Scan(&accountID, &status, &policy)
	if errors.Is(err, sql.ErrNoRows) {
		release()
		return Attempt{}, ErrAccountNotFound
	}
	if err != nil {
		release()
		return Attempt{}, err
	}
	if err := (Account{Status: status, Sub2Policy: policy}).terminalError(); err != nil {
		release()
		return Attempt{}, err
	}
	now := time.Now().Unix()
	if err = tx.QueryRowContext(ctx, `INSERT INTO login_attempts(account_id,status,started_at) VALUES(?,'running',?) RETURNING id`, accountID, now).Scan(&id); err != nil {
		release()
		return Attempt{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE accounts SET status='running', attempt_count=attempt_count+1, updated_at=? WHERE id=?`, now, accountID); err != nil {
		release()
		return Attempt{}, err
	}
	if err = consumeCredentialRepairRecoveryTx(ctx, tx, accountID, id); err != nil {
		release()
		return Attempt{}, err
	}
	if err = freezeLifecycleBindingsTx(ctx, tx, fmt.Sprintf("login_attempt:%d", id), accountID); err != nil {
		release()
		return Attempt{}, err
	}
	if err = tx.Commit(); err != nil {
		release()
		return Attempt{}, err
	}
	return Attempt{ID: id, AccountID: accountID, Email: email, StartedAt: time.Unix(now, 0), Release: release}, nil
}

// FinishAttempt records the result and updates account metadata atomically.
// Officially deleted accounts remain as a local tombstone. The verified Sub2
// cleanup later clears their credentials while retaining the safe table row.
func (s *Store) FinishAttempt(ctx context.Context, id int64, success bool, result *Result, failure *Failure) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.finishAttemptTx(ctx, tx, id, success, result, failure); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) finishAttemptTx(ctx context.Context, tx *sql.Tx, id int64, success bool, result *Result, failure *Failure) error {
	var accountID, started int64
	var email, status, accountStatus, policy string
	err := tx.QueryRowContext(ctx, `SELECT la.account_id,a.email,la.started_at,la.status,a.status,COALESCE(a.sub2_policy,'unknown') FROM login_attempts la JOIN accounts a ON a.id=la.account_id WHERE la.id=?`, id).Scan(&accountID, &email, &started, &status, &accountStatus, &policy)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAttemptNotFound
	} else if err != nil {
		return err
	}
	if err := (Account{Status: accountStatus, Sub2Policy: policy}).terminalError(); err != nil {
		return err
	}
	if status != "running" {
		return errors.New("login attempt is no longer running")
	}
	now := time.Now().Unix()
	duration := int64(0)
	if started > 0 && now >= started {
		duration = (now - started) * 1000
	}
	if success {
		if result == nil {
			return errors.New("successful attempt requires result")
		}
		access, e := s.seal(result.AccessToken)
		if e != nil {
			return e
		}
		refresh, e := s.seal(result.RefreshToken)
		if e != nil {
			return e
		}
		nextPolicy := nextSub2Policy(result.PlanType, policy)
		if _, err = tx.ExecContext(ctx, `UPDATE accounts SET status='active',sub2_policy=?,access_token_cipher=?,refresh_token_cipher=?,chatgpt_account_id=?,organization_id=?,plan_type=?,expires_at=?,last_error_code='',last_error='',last_http_status=0,last_success_at=?,updated_at=? WHERE id=?`, nextPolicy, access, refresh, result.ChatGPTAccountID, result.OrganizationID, result.PlanType, result.ExpiresAt, now, now, accountID); err != nil {
			return err
		}
		if nextPolicy == Sub2PolicyFreeLocal {
			if err := cancelSub2ForFreeTx(ctx, tx, accountID, id, fmt.Sprintf("login_attempt:%d", id), result.PlanType); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE login_attempts SET status='success',plan_type=?,chatgpt_account_id=?,organization_id=?,observation_source='oauth_login',finished_at=?,duration_ms=? WHERE id=?`, result.PlanType, result.ChatGPTAccountID, result.OrganizationID, now, duration, id); err != nil {
			return err
		}
	} else {
		if failure == nil {
			failure = &Failure{Code: "login_failed", Message: "login failed"}
		}
		status := lifecycleStatusFromFailure(failure.AccountStatus, failure.Code)
		if status == "" {
			status = "failed"
		}
		policyValue := policy
		source := "official"
		if isOfficialLifecycleStatus(status) {
			policyValue = Sub2PolicyOfficialUnavailable
		} else if !failure.Retryable && isDiscardedLoginFailure(failure.Code, failure.Stage, failure.HTTPStatus) {
			var superseded bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM login_attempts WHERE account_id=? AND status='success' AND id>?)`, accountID, id).Scan(&superseded); err != nil {
				return err
			}
			if superseded {
				return ErrAccountRecoveryVersionChanged
			}
			status, policyValue, source = "discarded", Sub2PolicyDiscarded, "login_discarded"
		}
		if _, err = tx.ExecContext(ctx, `UPDATE accounts SET status=?,sub2_policy=?,last_error_code=?,last_error=?,last_http_status=?,updated_at=? WHERE id=?`, status, policyValue, failure.Code, failure.Message, failure.HTTPStatus, now, accountID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE login_attempts SET status='failed',stage=?,error_code=?,error_message=?,http_status=?,retryable=?,finished_at=?,duration_ms=? WHERE id=?`, failure.Stage, failure.Code, failure.Message, failure.HTTPStatus, boolInt(failure.Retryable), now, duration, id); err != nil {
			return err
		}
		if IsTerminalLifecycleStatus(status) {
			if err := recordLifecycleEventTx(ctx, tx, LifecycleEvent{
				AccountID:  accountID,
				Email:      email,
				Source:     source,
				Status:     status,
				Reason:     failure.Message,
				Code:       failure.Code,
				HTTPStatus: failure.HTTPStatus,
				DedupeKey:  fmt.Sprintf("login_attempt:%d", id),
				ObservedAt: time.Unix(now, 0),
			}); err != nil {
				return err
			}
			if err := authorizeFrozenCleanupTx(ctx, tx, fmt.Sprintf("login_attempt:%d", id)); err != nil {
				return err
			}
			if err := finalizeTerminalUnavailableTx(ctx, tx, accountID, failure.Message, id, policyValue); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM lifecycle_request_bindings WHERE request_key=?`, fmt.Sprintf("login_attempt:%d", id))
	return err
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) DeleteIfDeleted(ctx context.Context, email, status string) (bool, error) {
	if strings.ToLower(strings.TrimSpace(status)) != "deleted" {
		return false, nil
	}
	account, err := s.GetAccount(ctx, email)
	if errors.Is(err, ErrAccountNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	err = s.DeleteAccountByID(ctx, account.ID)
	if errors.Is(err, ErrAccountNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) DeleteAccount(ctx context.Context, email string) error {
	_, err := s.DeleteIfDeleted(ctx, email, "deleted")
	return err
}

// DeleteAccountByID removes only the selected account identity. Holding the
// login lock through commit prevents a new attempt from racing the busy check;
// import creation and retries serialize with this transaction in SQLite.
func (s *Store) DeleteAccountByID(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Acquire the DB connection first: BeginAttempt may release its login lock
	// while unwinding a transaction, so waiting for DB under activeMu can deadlock.
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	var account Account
	err = tx.QueryRowContext(ctx, `SELECT email,status,sub2_policy FROM accounts WHERE id=?`, id).Scan(&account.Email, &account.Status, &account.Sub2Policy)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAccountNotFound
	} else if err != nil {
		return err
	}
	if err := account.terminalError(); err != nil {
		return err
	}
	if _, active := s.active[account.Email]; active || account.Status == "running" {
		return ErrAccountBusy
	}
	var importPending bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sub2_imports WHERE account_id=? AND state NOT IN ('imported','failed','remote_missing'))`, id).Scan(&importPending); err != nil {
		return err
	}
	if importPending {
		return ErrAccountBusy
	}
	var recoveryPending bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_recovery_tasks WHERE account_id=? AND state IN ('queued','validating','disabling_schedule','logging_in','login_succeeded','identity_verified','refreshing_credentials','applying_credentials','credentials_applied','enabling_schedule'))`, id).Scan(&recoveryPending); err != nil {
		return err
	}
	if recoveryPending {
		return ErrAccountBusy
	}
	// These are local snapshots only. Deleting AUTH history never deletes the
	// imported account in Sub2. Checks retain their anonymous batch evidence via FK.
	if _, err = tx.ExecContext(ctx, `DELETE FROM sub2_imports WHERE account_id=?`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM accounts WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
