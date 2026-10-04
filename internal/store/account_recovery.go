package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrAccountRecoveryNotFound       = errors.New("account recovery task not found")
	ErrAccountRecoveryNotQueued      = errors.New("account recovery task is not queued")
	ErrAccountRecoveryVersionChanged = errors.New("account credentials changed since recovery started")
	ErrAccountRecoveryNotResumable   = errors.New("account recovery has no reusable credential checkpoint")
)

const (
	RecoveryQueued                = "queued"
	RecoveryValidating            = "validating"
	RecoveryDisablingSchedule     = "disabling_schedule"
	RecoveryLoggingIn             = "logging_in"
	RecoveryLoginSucceeded        = "login_succeeded"
	RecoveryIdentityVerified      = "identity_verified"
	RecoveryRefreshingCredentials = "refreshing_credentials"
	RecoveryApplyingCredentials   = "applying_credentials"
	RecoveryCredentialsApplied    = "credentials_applied"
	RecoveryEnablingSchedule      = "enabling_schedule"
	RecoveryCompleted             = "completed"
	RecoveryFailed                = "failed"
	RecoveryUnknown               = "unknown"
	RecoveryCanceled              = "canceled"
)

// AccountRecoveryTask is the durable state for one AUTH account's Sub2
// recovery. It contains identifiers and redacted errors only.
type AccountRecoveryTask struct {
	ID                        int64     `json:"id"`
	AccountID                 int64     `json:"account_id"`
	AccountEmail              string    `json:"email,omitempty"`
	CheckID                   int64     `json:"check_id,omitempty"`
	SourceCredentialAttemptID int64     `json:"source_credential_attempt_id"`
	ResultCredentialAttemptID int64     `json:"result_credential_attempt_id,omitempty"`
	Resumable                 bool      `json:"resumable"`
	Sub2AccountID             int64     `json:"sub2_account_id"`
	OriginalSchedulable       bool      `json:"original_schedulable"`
	State                     string    `json:"state"`
	LastError                 string    `json:"last_error,omitempty"`
	CreatedAt                 time.Time `json:"created_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

func (s *Store) migrateAccountRecoveryTasks() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS account_recovery_tasks (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 check_id INTEGER NOT NULL DEFAULT 0,
 source_credential_attempt_id INTEGER NOT NULL DEFAULT 0,
 result_credential_attempt_id INTEGER NOT NULL DEFAULT 0,
 sub2_account_id INTEGER NOT NULL,
 original_schedulable INTEGER NOT NULL DEFAULT 0 CHECK(original_schedulable IN (0,1)),
 state TEXT NOT NULL,
 last_error TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS account_recovery_account_history ON account_recovery_tasks(account_id, id DESC);
CREATE INDEX IF NOT EXISTS account_recovery_state_updated ON account_recovery_tasks(state, updated_at, id);
CREATE UNIQUE INDEX IF NOT EXISTS account_recovery_one_active ON account_recovery_tasks(account_id)
 WHERE state IN ('queued','validating','disabling_schedule','logging_in','login_succeeded','identity_verified','refreshing_credentials','applying_credentials','credentials_applied','enabling_schedule');
`)
	if err != nil {
		return fmt.Errorf("migrate account recovery tasks: %w", err)
	}
	rows, err := s.db.Query(`PRAGMA table_info(account_recovery_tasks)`)
	if err != nil {
		return err
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, column := range []string{"source_credential_attempt_id", "result_credential_attempt_id"} {
		if !columns[column] {
			if _, err := s.db.Exec(`ALTER TABLE account_recovery_tasks ADD COLUMN ` + column + ` INTEGER NOT NULL DEFAULT 0`); err != nil {
				return err
			}
		}
	}
	return nil
}

// RecoverAccountRecoveryTasks marks in-flight work as unknown after a process
// restart. Unknown means the external Sub2 write result needs reconciliation;
// the caller must not blindly repeat it.
func (s *Store) RecoverAccountRecoveryTasks(ctx context.Context) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `UPDATE account_recovery_tasks SET state='unknown',last_error=CASE WHEN last_error='' THEN '任务在进程重启时中断' ELSE last_error END,updated_at=? WHERE state IN ('validating','disabling_schedule','logging_in','login_succeeded','identity_verified','refreshing_credentials','applying_credentials','credentials_applied','enabling_schedule')`, now)
	if err != nil {
		return fmt.Errorf("recover account recovery tasks: %w", err)
	}
	return nil
}

const accountRecoverySelect = `SELECT t.id,t.account_id,COALESCE(a.email,''),t.check_id,t.source_credential_attempt_id,t.result_credential_attempt_id,t.sub2_account_id,t.original_schedulable,t.state,t.last_error,t.created_at,t.updated_at,COALESCE((SELECT MAX(id) FROM login_attempts WHERE account_id=t.account_id AND status='success'),0) FROM account_recovery_tasks t LEFT JOIN accounts a ON a.id=t.account_id`

func scanAccountRecoveryTask(row scanner) (AccountRecoveryTask, error) {
	var task AccountRecoveryTask
	var original, created, updated, currentVersion int64
	if err := row.Scan(&task.ID, &task.AccountID, &task.AccountEmail, &task.CheckID, &task.SourceCredentialAttemptID, &task.ResultCredentialAttemptID, &task.Sub2AccountID, &original, &task.State, &task.LastError, &created, &updated, &currentVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AccountRecoveryTask{}, ErrAccountRecoveryNotFound
		}
		return AccountRecoveryTask{}, err
	}
	task.OriginalSchedulable = original != 0
	task.Resumable = task.ResultCredentialAttemptID > 0 && task.ResultCredentialAttemptID == currentVersion && (task.State == RecoveryFailed || task.State == RecoveryUnknown)
	task.CreatedAt = time.UnixMilli(created)
	task.UpdatedAt = time.UnixMilli(updated)
	return task, nil
}

// CreateOrGetAccountRecoveryTask creates one active recovery task per account.
// When an active task already exists, it is returned with created=false.
func (s *Store) CreateOrGetAccountRecoveryTask(ctx context.Context, accountID, checkID, sub2AccountID int64, originalSchedulable bool) (AccountRecoveryTask, bool, error) {
	if accountID <= 0 || sub2AccountID <= 0 {
		return AccountRecoveryTask{}, false, errors.New("account and sub2 account are required")
	}
	now := time.Now().UnixMilli()
	result, err := s.db.ExecContext(ctx, `INSERT INTO account_recovery_tasks(account_id,check_id,source_credential_attempt_id,sub2_account_id,original_schedulable,state,created_at,updated_at) SELECT ?,?,COALESCE((SELECT credential_attempt_id FROM account_checks WHERE id=? AND account_id=accounts.id),(SELECT MAX(id) FROM login_attempts WHERE account_id=accounts.id AND status='success'),0),?,?,?,?,? FROM accounts WHERE id=? ON CONFLICT DO NOTHING`, accountID, checkID, checkID, sub2AccountID, boolInt(originalSchedulable), RecoveryQueued, now, now, accountID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			task, _, getErr := s.GetActiveAccountRecoveryTask(ctx, accountID)
			return task, false, getErr
		}
		return AccountRecoveryTask{}, false, fmt.Errorf("create account recovery task: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return AccountRecoveryTask{}, false, err
	}
	if inserted == 0 {
		var exists bool
		if err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=?)`, accountID).Scan(&exists); err != nil {
			return AccountRecoveryTask{}, false, err
		}
		if !exists {
			return AccountRecoveryTask{}, false, ErrAccountNotFound
		}
		task, _, getErr := s.GetActiveAccountRecoveryTask(ctx, accountID)
		return task, false, getErr
	}
	id, err := result.LastInsertId()
	if err != nil {
		return AccountRecoveryTask{}, false, err
	}
	task, err := s.GetAccountRecoveryTaskByID(ctx, id)
	return task, true, err
}

func (s *Store) GetAccountRecoveryTaskByID(ctx context.Context, id int64) (AccountRecoveryTask, error) {
	if id <= 0 {
		return AccountRecoveryTask{}, ErrAccountRecoveryNotFound
	}
	return scanAccountRecoveryTask(s.db.QueryRowContext(ctx, accountRecoverySelect+` WHERE t.id=?`, id))
}

func (s *Store) GetActiveAccountRecoveryTask(ctx context.Context, accountID int64) (AccountRecoveryTask, bool, error) {
	if accountID <= 0 {
		return AccountRecoveryTask{}, false, ErrAccountRecoveryNotFound
	}
	task, err := scanAccountRecoveryTask(s.db.QueryRowContext(ctx, accountRecoverySelect+` WHERE t.account_id=? AND t.state IN ('queued','validating','disabling_schedule','logging_in','login_succeeded','identity_verified','refreshing_credentials','applying_credentials','credentials_applied','enabling_schedule') ORDER BY t.id DESC LIMIT 1`, accountID))
	if errors.Is(err, ErrAccountRecoveryNotFound) {
		return AccountRecoveryTask{}, false, nil
	}
	return task, err == nil, err
}

// GetLatestAccountRecoveryTask returns the most recent task, including a
// terminal task. It is used by the status endpoint after a worker completes.
func (s *Store) GetLatestAccountRecoveryTask(ctx context.Context, accountID int64) (AccountRecoveryTask, error) {
	if accountID <= 0 {
		return AccountRecoveryTask{}, ErrAccountRecoveryNotFound
	}
	return scanAccountRecoveryTask(s.db.QueryRowContext(ctx, accountRecoverySelect+` WHERE t.account_id=? ORDER BY t.id DESC LIMIT 1`, accountID))
}

// ClaimAccountRecoveryTask atomically moves a queued task to logging_in.
func (s *Store) ClaimAccountRecoveryTask(ctx context.Context, id int64) (AccountRecoveryTask, error) {
	if id <= 0 {
		return AccountRecoveryTask{}, ErrAccountRecoveryNotFound
	}
	result, err := s.db.ExecContext(ctx, `UPDATE account_recovery_tasks SET state='logging_in',updated_at=? WHERE id=? AND state='queued'`, time.Now().UnixMilli(), id)
	if err != nil {
		return AccountRecoveryTask{}, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return AccountRecoveryTask{}, err
	} else if n == 0 {
		task, getErr := s.GetAccountRecoveryTaskByID(ctx, id)
		if getErr != nil {
			return AccountRecoveryTask{}, getErr
		}
		return task, ErrAccountRecoveryNotQueued
	}
	return s.GetAccountRecoveryTaskByID(ctx, id)
}

// UpdateAccountRecoveryTask records a state transition and a redacted error.
func (s *Store) UpdateAccountRecoveryTask(ctx context.Context, id int64, state, lastError string) (AccountRecoveryTask, error) {
	if id <= 0 {
		return AccountRecoveryTask{}, ErrAccountRecoveryNotFound
	}
	state = strings.TrimSpace(state)
	if !validRecoveryState(state) {
		return AccountRecoveryTask{}, errors.New("invalid account recovery task state")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE account_recovery_tasks SET state=?,last_error=?,updated_at=? WHERE id=?`, state, strings.TrimSpace(lastError), time.Now().UnixMilli(), id)
	if err != nil {
		return AccountRecoveryTask{}, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return AccountRecoveryTask{}, err
	} else if n == 0 {
		return AccountRecoveryTask{}, ErrAccountRecoveryNotFound
	}
	return s.GetAccountRecoveryTaskByID(ctx, id)
}

func (s *Store) ListAccountRecoveryTasks(ctx context.Context, state string, limit int) ([]AccountRecoveryTask, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := accountRecoverySelect
	args := make([]any, 0, 2)
	if state = strings.TrimSpace(state); state != "" {
		if !validRecoveryState(state) {
			return nil, errors.New("invalid account recovery task state")
		}
		query += ` WHERE t.state=?`
		args = append(args, state)
	}
	if state == RecoveryQueued {
		query += ` ORDER BY t.id ASC LIMIT ?`
	} else {
		query += ` ORDER BY t.updated_at DESC,t.id DESC LIMIT ?`
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]AccountRecoveryTask, 0)
	for rows.Next() {
		task, err := scanAccountRecoveryTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// ListLatestAccountRecoveryTasks supplies the account history's status map.
// A global task limit would hide older accounts behind one frequently retried
// account, so select exactly one latest task for every account.
func (s *Store) ListLatestAccountRecoveryTasks(ctx context.Context) ([]AccountRecoveryTask, error) {
	rows, err := s.db.QueryContext(ctx, accountRecoverySelect+` WHERE t.id IN (SELECT MAX(id) FROM account_recovery_tasks GROUP BY account_id) ORDER BY t.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]AccountRecoveryTask, 0)
	for rows.Next() {
		task, err := scanAccountRecoveryTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// AccountRecoveryNeedsLogin prevents an explicit retry from reusing a refresh
// token whose previous consumption was not checkpointed. A new check does not
// make that same credential version safe to consume again.
func (s *Store) AccountRecoveryNeedsLogin(ctx context.Context, taskID int64) (bool, error) {
	var needsLogin bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_recovery_tasks previous WHERE previous.account_id=t.account_id AND previous.source_credential_attempt_id=t.source_credential_attempt_id AND previous.id<t.id AND previous.state IN ('failed','unknown') AND previous.result_credential_attempt_id=0) FROM account_recovery_tasks t WHERE t.id=?`, taskID).Scan(&needsLogin)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrAccountRecoveryNotFound
	}
	return needsLogin, err
}

func validRecoveryState(state string) bool {
	switch state {
	case RecoveryQueued, RecoveryValidating, RecoveryDisablingSchedule, RecoveryLoggingIn, RecoveryLoginSucceeded, RecoveryIdentityVerified, RecoveryRefreshingCredentials, RecoveryApplyingCredentials, RecoveryCredentialsApplied, RecoveryEnablingSchedule, RecoveryCompleted, RecoveryFailed, RecoveryUnknown, RecoveryCanceled:
		return true
	default:
		return false
	}
}

// AccountRecoveryLease holds the same account lock as ordinary logins across
// the remote recovery steps. Its methods never release that lock early.
type AccountRecoveryLease struct {
	store     *Store
	accountID int64
	email     string
	mu        sync.Mutex
	released  bool
}

func (s *Store) AcquireAccountRecovery(ctx context.Context, accountID int64) (*AccountRecoveryLease, error) {
	account, err := s.GetAccountByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account.Status == "running" {
		return nil, ErrAccountBusy
	}
	s.activeMu.Lock()
	if _, active := s.active[account.Email]; active {
		s.activeMu.Unlock()
		return nil, ErrAccountBusy
	}
	s.active[account.Email] = struct{}{}
	s.activeMu.Unlock()
	lease := &AccountRecoveryLease{store: s, accountID: accountID, email: account.Email}
	// A deletion may have committed between reading the email and taking the lock.
	if account, err := s.GetAccountByID(ctx, accountID); err != nil {
		lease.Release()
		return nil, err
	} else if account.Status == "running" {
		lease.Release()
		return nil, ErrAccountBusy
	}
	return lease, nil
}

func (l *AccountRecoveryLease) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.released {
		l.store.activeMu.Lock()
		delete(l.store.active, l.email)
		l.store.activeMu.Unlock()
		l.released = true
	}
}

func (l *AccountRecoveryLease) BeginAttempt(ctx context.Context, credentials Credentials) (Account, Attempt, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released || normalizeEmail(credentials.Email) != l.email || credentials.Password == "" {
		return Account{}, Attempt{}, ErrAccountBusy
	}
	credentials.Email, credentials.ExistingAccountID = l.email, l.accountID
	return l.store.beginAttemptLocked(ctx, credentials, func() {})
}

// FinishAttempt commits the login result and recovery checkpoint together;
// after a restart a saved credential can be reused without consuming its RT.
func (l *AccountRecoveryLease) FinishAttempt(ctx context.Context, taskID, attemptID int64, success bool, result *Result, failure *Failure) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return ErrAccountBusy
	}
	tx, err := l.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var accountID int64
	if err := tx.QueryRowContext(ctx, `SELECT account_id FROM login_attempts WHERE id=?`, attemptID).Scan(&accountID); err != nil {
		return err
	}
	if accountID != l.accountID {
		return ErrAccountNotFound
	}
	if success {
		if result == nil || strings.TrimSpace(result.AccessToken) == "" || strings.TrimSpace(result.RefreshToken) == "" {
			return errors.New("complete OAuth tokens are required")
		}
		if err := validateRecoveryVersionTx(ctx, tx, taskID, l.accountID); err != nil {
			return err
		}
	}
	if err := l.store.finishAttemptTx(ctx, tx, attemptID, success, result, failure); err != nil {
		return err
	}
	if success {
		if err := checkpointRecoveryTx(ctx, tx, taskID, l.accountID, attemptID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (l *AccountRecoveryLease) UpdateOAuthResult(ctx context.Context, taskID int64, result Result) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return 0, ErrAccountBusy
	}
	if strings.TrimSpace(result.AccessToken) == "" || strings.TrimSpace(result.RefreshToken) == "" {
		return 0, errors.New("complete OAuth tokens are required")
	}
	access, err := l.store.seal(result.AccessToken)
	if err != nil {
		return 0, err
	}
	refresh, err := l.store.seal(result.RefreshToken)
	if err != nil {
		return 0, err
	}
	tx, err := l.store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if taskID > 0 {
		if err := validateRecoveryVersionTx(ctx, tx, taskID, l.accountID); err != nil {
			return 0, err
		}
	}
	now := time.Now().Unix()
	exec, err := tx.ExecContext(ctx, `UPDATE accounts SET access_token_cipher=?,refresh_token_cipher=?,chatgpt_account_id=CASE WHEN ?<>'' THEN ? ELSE chatgpt_account_id END,organization_id=CASE WHEN ?<>'' THEN ? ELSE organization_id END,plan_type=CASE WHEN ?<>'' THEN ? ELSE plan_type END,expires_at=CASE WHEN ?>0 THEN ? ELSE expires_at END,status='active',attempt_count=attempt_count+1,last_error_code='',last_error='',last_http_status=0,last_success_at=?,updated_at=? WHERE id=?`, access, refresh, result.ChatGPTAccountID, result.ChatGPTAccountID, result.OrganizationID, result.OrganizationID, result.PlanType, result.PlanType, result.ExpiresAt, result.ExpiresAt, now, now, l.accountID)
	if err != nil {
		return 0, err
	}
	if n, err := exec.RowsAffected(); err != nil {
		return 0, err
	} else if n != 1 {
		return 0, ErrAccountNotFound
	}
	var version int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO login_attempts(account_id,status,stage,started_at,finished_at) VALUES(?,'success','token_refresh',?,?) RETURNING id`, l.accountID, now, now).Scan(&version); err != nil {
		return 0, err
	}
	if taskID > 0 {
		if err := checkpointRecoveryTx(ctx, tx, taskID, l.accountID, version); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return version, nil
}

func checkpointRecoveryTx(ctx context.Context, tx *sql.Tx, taskID, accountID, version int64) error {
	result, err := tx.ExecContext(ctx, `UPDATE account_recovery_tasks SET result_credential_attempt_id=?,state='login_succeeded',last_error='',updated_at=? WHERE id=? AND account_id=? AND result_credential_attempt_id=0 AND state IN ('logging_in','refreshing_credentials')`, version, time.Now().UnixMilli(), taskID, accountID)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrAccountRecoveryNotResumable
	}
	return nil
}

func (s *Store) GetAccountCredentialVersion(ctx context.Context, accountID int64) (int64, error) {
	var version int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT MAX(id) FROM login_attempts WHERE account_id=accounts.id AND status='success'),0) FROM accounts WHERE id=?`, accountID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrAccountNotFound
	}
	return version, err
}

func validateRecoveryVersionTx(ctx context.Context, tx *sql.Tx, taskID, accountID int64) error {
	var source, result, current, owner int64
	err := tx.QueryRowContext(ctx, `SELECT t.account_id,t.source_credential_attempt_id,t.result_credential_attempt_id,COALESCE((SELECT MAX(id) FROM login_attempts WHERE account_id=t.account_id AND status='success'),0) FROM account_recovery_tasks t WHERE t.id=?`, taskID).Scan(&owner, &source, &result, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAccountRecoveryNotFound
	}
	if err != nil {
		return err
	}
	if accountID > 0 && owner != accountID {
		return ErrAccountNotFound
	}
	expected := source
	if result > 0 {
		expected = result
	}
	if expected <= 0 || expected != current {
		return ErrAccountRecoveryVersionChanged
	}
	return nil
}

// ValidateAccountRecoveryVersion must be called while holding the account's
// recovery lease before each remote mutation. A fresh normal login invalidates
// both queued tasks and saved checkpoints from an older recovery.
func (s *Store) ValidateAccountRecoveryVersion(ctx context.Context, taskID int64) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return validateRecoveryVersionTx(ctx, tx, taskID, 0)
}

func (s *Store) ResumeAccountRecoveryTask(ctx context.Context, taskID int64) (AccountRecoveryTask, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountRecoveryTask{}, err
	}
	defer tx.Rollback()
	task, err := scanAccountRecoveryTask(tx.QueryRowContext(ctx, accountRecoverySelect+` WHERE t.id=?`, taskID))
	if err != nil {
		return task, err
	}
	if !task.Resumable {
		if task.ResultCredentialAttemptID > 0 {
			if err := validateRecoveryVersionTx(ctx, tx, taskID, task.AccountID); err != nil {
				return task, err
			}
		}
		return task, ErrAccountRecoveryNotResumable
	}
	var newer, active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_recovery_tasks WHERE account_id=? AND id>?),EXISTS(SELECT 1 FROM account_recovery_tasks WHERE account_id=? AND state IN ('queued','validating','disabling_schedule','logging_in','login_succeeded','identity_verified','refreshing_credentials','applying_credentials','credentials_applied','enabling_schedule'))`, task.AccountID, task.ID, task.AccountID).Scan(&newer, &active); err != nil {
		return task, err
	}
	if newer || active {
		return task, ErrAccountBusy
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_recovery_tasks SET state='queued',last_error='',updated_at=? WHERE id=?`, time.Now().UnixMilli(), task.ID); err != nil {
		return task, err
	}
	if err := tx.Commit(); err != nil {
		return task, err
	}
	return s.GetAccountRecoveryTaskByID(ctx, task.ID)
}
