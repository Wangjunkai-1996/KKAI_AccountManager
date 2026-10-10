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
	ErrAccountRecoveryStale          = errors.New("account recovery failure result is stale")
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
	ID                        int64      `json:"id"`
	AccountID                 int64      `json:"account_id"`
	AccountEmail              string     `json:"email,omitempty"`
	CheckID                   int64      `json:"check_id,omitempty"`
	SourceCredentialAttemptID int64      `json:"source_credential_attempt_id"`
	ResultCredentialAttemptID int64      `json:"result_credential_attempt_id,omitempty"`
	Resumable                 bool       `json:"resumable"`
	Sub2AccountID             int64      `json:"sub2_account_id"`
	DestinationKey            string     `json:"-"`
	OriginalSchedulable       bool       `json:"original_schedulable"`
	Purpose                   string     `json:"purpose"`
	DeliveryID                int64      `json:"delivery_id,omitempty"`
	State                     string     `json:"state"`
	LastError                 string     `json:"last_error,omitempty"`
	FailureStage              string     `json:"failure_stage,omitempty"`
	ErrorCode                 string     `json:"error_code,omitempty"`
	RetryAction               string     `json:"retry_action,omitempty"`
	NextRetryAt               *time.Time `json:"next_retry_at,omitempty"`
	RetryCount                int        `json:"retry_count"`
	ManualAction              string     `json:"manual_action,omitempty"`
	RequiresAction            bool       `json:"requires_action"`
	CreatedAt                 time.Time  `json:"created_at"`
	UpdatedAt                 time.Time  `json:"updated_at"`
}

// RecoveryFailure contains only the caller's redacted classification.
// RetryCount counts failed rounds of this task, including resumed rounds.
type RecoveryFailure struct {
	State        string
	Stage        string
	Code         string
	Message      string
	RetryAction  string
	NextRetryAt  *time.Time
	ManualAction string
	// ExpectedState enables a snapshot fence. RetryCount distinguishes failed
	// rounds even when their state and millisecond timestamp are identical.
	ExpectedState      string
	ExpectedUpdatedAt  int64
	ExpectedRetryCount int
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
 purpose TEXT NOT NULL DEFAULT 'recovery',
 delivery_id INTEGER NOT NULL DEFAULT 0,
 destination_key TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL,
 last_error TEXT NOT NULL DEFAULT '',
 failure_stage TEXT NOT NULL DEFAULT '',
 error_code TEXT NOT NULL DEFAULT '',
 retry_action TEXT NOT NULL DEFAULT '',
 next_retry_at INTEGER,
 retry_count INTEGER NOT NULL DEFAULT 0,
 manual_action TEXT NOT NULL DEFAULT '',
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
	for _, column := range []struct{ name, definition string }{
		{"source_credential_attempt_id", "INTEGER NOT NULL DEFAULT 0"},
		{"result_credential_attempt_id", "INTEGER NOT NULL DEFAULT 0"},
		{"purpose", "TEXT NOT NULL DEFAULT 'recovery'"},
		{"delivery_id", "INTEGER NOT NULL DEFAULT 0"},
		{"failure_stage", "TEXT NOT NULL DEFAULT ''"},
		{"error_code", "TEXT NOT NULL DEFAULT ''"},
		{"retry_action", "TEXT NOT NULL DEFAULT ''"},
		{"next_retry_at", "INTEGER"},
		{"retry_count", "INTEGER NOT NULL DEFAULT 0"},
		{"manual_action", "TEXT NOT NULL DEFAULT ''"},
		{"destination_key", "TEXT NOT NULL DEFAULT ''"},
	} {
		if !columns[column.name] {
			if _, err := s.db.Exec(`ALTER TABLE account_recovery_tasks ADD COLUMN ` + column.name + ` ` + column.definition); err != nil {
				return err
			}
		}
	}
	// Recover ownership for rows created before destination-aware recovery.
	// Delivery rows are exact; ordinary tasks use a sole imported binding.
	if _, err := s.db.Exec(`UPDATE account_recovery_tasks SET destination_key=COALESCE((SELECT MIN(i.destination_key) FROM sub2_imports i WHERE i.account_id=account_recovery_tasks.account_id AND i.sub2_account_id=account_recovery_tasks.sub2_account_id AND i.state='imported' HAVING COUNT(DISTINCT i.destination_key)=1),'') WHERE destination_key='' AND delivery_id=0`); err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS account_recovery_delivery ON account_recovery_tasks(delivery_id) WHERE delivery_id>0`)
	if err != nil {
		return err
	}
	if err := s.migrateAccountRecoveryRechecks(); err != nil {
		return err
	}
	return s.migrateCredentialRepairs()
}

// RecoverAccountRecoveryTasks marks in-flight work as unknown after a process
// restart. The due retry must reconcile the remote state before repeating any
// mutation; a persisted checkpoint avoids another login where possible.
func (s *Store) RecoverAccountRecoveryTasks(ctx context.Context) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `UPDATE account_recovery_tasks SET failure_stage=state,state='unknown',last_error='任务在进程重启时中断，将核对状态后继续',error_code='process_interrupted',retry_action=CASE WHEN result_credential_attempt_id>0 THEN 'resume' ELSE 'relogin' END,next_retry_at=?,retry_count=retry_count+1,manual_action='',updated_at=? WHERE state IN ('validating','disabling_schedule','logging_in','login_succeeded','identity_verified','refreshing_credentials','applying_credentials','credentials_applied','enabling_schedule')`, now, now)
	if err != nil {
		return fmt.Errorf("recover account recovery tasks: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `UPDATE account_recovery_rechecks SET state='pending',next_check_at=?,updated_at=? WHERE state='checking'`, now, now)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE account_credential_repairs SET state='retry_wait',next_retry_at=?,updated_at=? WHERE state='checking'`, now, now)
	return err
}

const accountRecoverySelect = `SELECT t.id,t.account_id,COALESCE(a.email,''),t.check_id,t.source_credential_attempt_id,t.result_credential_attempt_id,t.sub2_account_id,t.original_schedulable,t.purpose,t.delivery_id,t.destination_key,t.state,t.last_error,t.failure_stage,t.error_code,t.retry_action,t.next_retry_at,t.retry_count,t.manual_action,t.created_at,t.updated_at,COALESCE((SELECT MAX(id) FROM login_attempts WHERE account_id=t.account_id AND status='success'),0) FROM account_recovery_tasks t LEFT JOIN accounts a ON a.id=t.account_id`

func recoveryDestination(destinations []string) string {
	if len(destinations) == 0 {
		return ""
	}
	return strings.TrimSpace(destinations[0])
}

func scanAccountRecoveryTask(row scanner) (AccountRecoveryTask, error) {
	var task AccountRecoveryTask
	var original, created, updated, currentVersion int64
	var nextRetry sql.NullInt64
	if err := row.Scan(&task.ID, &task.AccountID, &task.AccountEmail, &task.CheckID, &task.SourceCredentialAttemptID, &task.ResultCredentialAttemptID, &task.Sub2AccountID, &original, &task.Purpose, &task.DeliveryID, &task.DestinationKey, &task.State, &task.LastError, &task.FailureStage, &task.ErrorCode, &task.RetryAction, &nextRetry, &task.RetryCount, &task.ManualAction, &created, &updated, &currentVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AccountRecoveryTask{}, ErrAccountRecoveryNotFound
		}
		return AccountRecoveryTask{}, err
	}
	task.OriginalSchedulable = original != 0
	task.Resumable = task.ResultCredentialAttemptID > 0 && task.ResultCredentialAttemptID == currentVersion && task.RetryAction != "relogin" && (task.State == RecoveryFailed || task.State == RecoveryUnknown)
	task.RequiresAction = task.RetryAction == "manual" || task.ManualAction != ""
	if nextRetry.Valid {
		next := time.UnixMilli(nextRetry.Int64)
		task.NextRetryAt = &next
	}
	task.CreatedAt = time.UnixMilli(created)
	task.UpdatedAt = time.UnixMilli(updated)
	return task, nil
}

// CreateOrGetAccountRecoveryTask creates one active recovery task per account.
// When an active task already exists, it is returned with created=false.
func (s *Store) CreateOrGetAccountRecoveryTask(ctx context.Context, accountID, checkID, sub2AccountID int64, originalSchedulable bool, destinations ...string) (AccountRecoveryTask, bool, error) {
	if accountID <= 0 || sub2AccountID <= 0 {
		return AccountRecoveryTask{}, false, errors.New("account and sub2 account are required")
	}
	destination := recoveryDestination(destinations)
	now := time.Now().UnixMilli()
	result, err := s.db.ExecContext(ctx, `INSERT INTO account_recovery_tasks(account_id,check_id,source_credential_attempt_id,sub2_account_id,original_schedulable,destination_key,state,created_at,updated_at) SELECT ?,?,COALESCE((SELECT credential_attempt_id FROM account_checks WHERE id=? AND account_id=accounts.id),(SELECT MAX(id) FROM login_attempts WHERE account_id=accounts.id AND status='success'),0),?,?,?,?,?,? FROM accounts WHERE id=? ON CONFLICT DO NOTHING`, accountID, checkID, checkID, sub2AccountID, boolInt(originalSchedulable), destination, RecoveryQueued, now, now, accountID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			task, found, getErr := s.getActiveAccountRecoveryTask(ctx, accountID, destination)
			if getErr == nil && !found && destination != "" {
				if _, globalFound, globalErr := s.getActiveAccountRecoveryTask(ctx, accountID); globalErr != nil {
					return AccountRecoveryTask{}, false, globalErr
				} else if globalFound {
					return AccountRecoveryTask{}, false, ErrAccountBusy
				}
			}
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
		task, found, getErr := s.getActiveAccountRecoveryTask(ctx, accountID, destination)
		if getErr == nil && !found && destination != "" {
			if _, globalFound, globalErr := s.getActiveAccountRecoveryTask(ctx, accountID); globalErr != nil {
				return AccountRecoveryTask{}, false, globalErr
			} else if globalFound {
				return AccountRecoveryTask{}, false, ErrAccountBusy
			}
		}
		return task, false, getErr
	}
	id, err := result.LastInsertId()
	if err != nil {
		return AccountRecoveryTask{}, false, err
	}
	task, err := s.GetAccountRecoveryTaskByID(ctx, id)
	return task, true, err
}

// CreateAccountDeliveryRecoveryTask hands an existing successful login to the
// recovery worker without another login. DeliveryID survives checkpoint changes
// so a crash between creating this task and recording its ID cannot duplicate it.
func (s *Store) CreateAccountDeliveryRecoveryTask(ctx context.Context, deliveryID, accountID, sub2ID, credentialVersion int64, originalSchedulable bool) (AccountRecoveryTask, bool, error) {
	if deliveryID <= 0 || accountID <= 0 || sub2ID <= 0 || credentialVersion <= 0 {
		return AccountRecoveryTask{}, false, errors.New("delivery, account, Sub2 account and credential version are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountRecoveryTask{}, false, err
	}
	defer tx.Rollback()
	prior, err := scanAccountRecoveryTask(tx.QueryRowContext(ctx, accountRecoverySelect+` WHERE t.delivery_id=?`, deliveryID))
	if err == nil {
		if prior.AccountID != accountID || prior.Sub2AccountID != sub2ID || prior.Purpose != "delivery" {
			return AccountRecoveryTask{}, false, errors.New("delivery task identity mismatch")
		}
		return prior, false, nil
	}
	if !errors.Is(err, ErrAccountRecoveryNotFound) {
		return AccountRecoveryTask{}, false, err
	}
	var current int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT MAX(id) FROM login_attempts WHERE account_id=accounts.id AND status='success'),0) FROM accounts WHERE id=?`, accountID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrAccountNotFound
		}
		return AccountRecoveryTask{}, false, err
	}
	if current != credentialVersion {
		return AccountRecoveryTask{}, false, ErrAccountRecoveryVersionChanged
	}
	var destination string
	if err := tx.QueryRowContext(ctx, `SELECT destination_key FROM account_deliveries WHERE id=? AND account_id=?`, deliveryID, accountID).Scan(&destination); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Older callers created delivery recovery rows before the delivery
			// table carried its destination. Keep the legacy path readable.
			destination = ""
		} else {
			return AccountRecoveryTask{}, false, err
		}
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_recovery_tasks WHERE account_id=? AND state IN ('queued','validating','disabling_schedule','logging_in','login_succeeded','identity_verified','refreshing_credentials','applying_credentials','credentials_applied','enabling_schedule'))`, accountID).Scan(&active); err != nil {
		return AccountRecoveryTask{}, false, err
	}
	if active {
		return AccountRecoveryTask{}, false, ErrAccountBusy
	}
	now := time.Now().UnixMilli()
	result, err := tx.ExecContext(ctx, `INSERT INTO account_recovery_tasks(account_id,source_credential_attempt_id,result_credential_attempt_id,sub2_account_id,original_schedulable,purpose,delivery_id,destination_key,state,created_at,updated_at) VALUES(?,?,?,?,?,'delivery',?,?, 'queued',?,?)`, accountID, credentialVersion, credentialVersion, sub2ID, boolInt(originalSchedulable), deliveryID, destination, now, now)
	if err != nil {
		return AccountRecoveryTask{}, false, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return AccountRecoveryTask{}, false, err
	}
	if err := tx.Commit(); err != nil {
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

func (s *Store) GetAccountRecoveryTaskByIDForDestination(ctx context.Context, id int64, destination string) (AccountRecoveryTask, error) {
	if id <= 0 {
		return AccountRecoveryTask{}, ErrAccountRecoveryNotFound
	}
	if strings.TrimSpace(destination) == "" {
		return s.GetAccountRecoveryTaskByID(ctx, id)
	}
	return scanAccountRecoveryTask(s.db.QueryRowContext(ctx, accountRecoverySelect+` WHERE t.id=? AND (t.destination_key=? OR (t.destination_key='' AND (SELECT COUNT(DISTINCT legacy_dest.destination_key) FROM sub2_imports legacy_dest WHERE legacy_dest.account_id=t.account_id AND legacy_dest.sub2_account_id=t.sub2_account_id AND legacy_dest.state='imported')=1 AND EXISTS (SELECT 1 FROM sub2_imports i WHERE i.destination_key=? AND i.account_id=t.account_id AND i.sub2_account_id=t.sub2_account_id AND i.state='imported')))`, id, destination, destination))
}

func (s *Store) GetActiveAccountRecoveryTask(ctx context.Context, accountID int64) (AccountRecoveryTask, bool, error) {
	return s.getActiveAccountRecoveryTask(ctx, accountID)
}

// getActiveAccountRecoveryTask optionally limits the result to the Sub2
// destination that owns the persisted binding. Recovery tasks predate the
// destination-aware schema, so the binding is the durable ownership link.
func (s *Store) GetActiveAccountRecoveryTaskForDestination(ctx context.Context, accountID int64, destination string) (AccountRecoveryTask, bool, error) {
	return s.getActiveAccountRecoveryTask(ctx, accountID, destination)
}

func (s *Store) getActiveAccountRecoveryTask(ctx context.Context, accountID int64, destinations ...string) (AccountRecoveryTask, bool, error) {
	if accountID <= 0 {
		return AccountRecoveryTask{}, false, ErrAccountRecoveryNotFound
	}
	query := accountRecoverySelect + ` WHERE t.account_id=? AND t.state IN ('queued','validating','disabling_schedule','logging_in','login_succeeded','identity_verified','refreshing_credentials','applying_credentials','credentials_applied','enabling_schedule')`
	args := []any{accountID}
	if destination := recoveryDestination(destinations); destination != "" {
		query += ` AND (t.destination_key=? OR (t.destination_key='' AND (SELECT COUNT(DISTINCT legacy_dest.destination_key) FROM sub2_imports legacy_dest WHERE legacy_dest.account_id=t.account_id AND legacy_dest.sub2_account_id=t.sub2_account_id AND legacy_dest.state='imported')=1 AND EXISTS (SELECT 1 FROM sub2_imports i WHERE i.destination_key=? AND i.account_id=t.account_id AND i.sub2_account_id=t.sub2_account_id AND i.state='imported')))`
		args = append(args, destination, destination)
	}
	query += ` ORDER BY t.id DESC LIMIT 1`
	task, err := scanAccountRecoveryTask(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, ErrAccountRecoveryNotFound) {
		return AccountRecoveryTask{}, false, nil
	}
	return task, err == nil, err
}

// GetLatestAccountRecoveryTask returns the most recent task, including a
// terminal task. It is used by the status endpoint after a worker completes.
func (s *Store) GetLatestAccountRecoveryTask(ctx context.Context, accountID int64) (AccountRecoveryTask, error) {
	return s.getLatestAccountRecoveryTask(ctx, accountID)
}

func (s *Store) GetLatestAccountRecoveryTaskForDestination(ctx context.Context, accountID int64, destination string) (AccountRecoveryTask, error) {
	return s.getLatestAccountRecoveryTask(ctx, accountID, destination)
}

func (s *Store) getLatestAccountRecoveryTask(ctx context.Context, accountID int64, destinations ...string) (AccountRecoveryTask, error) {
	if accountID <= 0 {
		return AccountRecoveryTask{}, ErrAccountRecoveryNotFound
	}
	query := accountRecoverySelect + ` WHERE t.account_id=?`
	args := []any{accountID}
	if destination := recoveryDestination(destinations); destination != "" {
		query += ` AND (t.destination_key=? OR (t.destination_key='' AND (SELECT COUNT(DISTINCT legacy_dest.destination_key) FROM sub2_imports legacy_dest WHERE legacy_dest.account_id=t.account_id AND legacy_dest.sub2_account_id=t.sub2_account_id AND legacy_dest.state='imported')=1 AND EXISTS (SELECT 1 FROM sub2_imports i WHERE i.destination_key=? AND i.account_id=t.account_id AND i.sub2_account_id=t.sub2_account_id AND i.state='imported')))`
		args = append(args, destination, destination)
	}
	query += ` ORDER BY t.id DESC LIMIT 1`
	return scanAccountRecoveryTask(s.db.QueryRowContext(ctx, query, args...))
}

// ClaimAccountRecoveryTask atomically claims a queued task for validation.
// The worker records logging_in only after it has durably disabled the remote
// schedule; this prevents a restart immediately after claim from pretending
// that it owns an operator's pause.
func (s *Store) ClaimAccountRecoveryTask(ctx context.Context, id int64) (AccountRecoveryTask, error) {
	if id <= 0 {
		return AccountRecoveryTask{}, ErrAccountRecoveryNotFound
	}
	result, err := s.db.ExecContext(ctx, `UPDATE account_recovery_tasks SET state='validating',updated_at=? WHERE id=? AND state='queued'`, time.Now().UnixMilli(), id)
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountRecoveryTask{}, err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	result, err := tx.ExecContext(ctx, `UPDATE account_recovery_tasks SET state=?,last_error=?,next_retry_at=NULL,manual_action='',failure_stage=CASE WHEN ? THEN '' ELSE failure_stage END,error_code=CASE WHEN ? THEN '' ELSE error_code END,retry_action=CASE WHEN ? THEN '' ELSE retry_action END,updated_at=? WHERE id=?`, state, strings.TrimSpace(lastError), state == RecoveryCompleted, state == RecoveryCompleted, state == RecoveryCompleted, now, id)
	if err != nil {
		return AccountRecoveryTask{}, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return AccountRecoveryTask{}, err
	} else if n == 0 {
		return AccountRecoveryTask{}, ErrAccountRecoveryNotFound
	}
	if state == RecoveryCompleted {
		if _, err := tx.ExecContext(ctx, `INSERT INTO account_recovery_rechecks(task_id,next_check_at,completed_at,updated_at) SELECT id,?,?,? FROM account_recovery_tasks WHERE id=? AND original_schedulable=1 AND result_credential_attempt_id>0 ON CONFLICT(task_id) DO NOTHING`, now+accountRecoveryFirstRecheckDelay.Milliseconds(), now, now, id); err != nil {
			return AccountRecoveryTask{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AccountRecoveryTask{}, err
	}
	return s.GetAccountRecoveryTaskByID(ctx, id)
}

// RecordAccountRecoveryFailure persists the decision and its retry deadline in
// one write. The worker supplies an already redacted message and classification.
func (s *Store) RecordAccountRecoveryFailure(ctx context.Context, id int64, failure RecoveryFailure) (AccountRecoveryTask, error) {
	if id <= 0 {
		return AccountRecoveryTask{}, ErrAccountRecoveryNotFound
	}
	if failure.State != RecoveryFailed && failure.State != RecoveryUnknown && failure.State != RecoveryCanceled {
		return AccountRecoveryTask{}, errors.New("invalid recovery failure state")
	}
	switch failure.RetryAction {
	case "", "resume", "relogin", "manual":
	default:
		return AccountRecoveryTask{}, errors.New("invalid recovery retry action")
	}
	var next any
	if failure.NextRetryAt != nil {
		if (failure.RetryAction != "resume" && failure.RetryAction != "relogin") || failure.State == RecoveryCanceled || strings.TrimSpace(failure.ManualAction) != "" || failure.NextRetryAt.UnixMilli() <= 0 {
			return AccountRecoveryTask{}, errors.New("invalid recovery retry deadline")
		}
		next = failure.NextRetryAt.UnixMilli()
	}
	query := `UPDATE account_recovery_tasks SET state=?,last_error=?,failure_stage=?,error_code=?,retry_action=?,next_retry_at=?,retry_count=retry_count+1,manual_action=?,updated_at=? WHERE id=? AND state NOT IN ('completed','canceled')`
	args := []any{failure.State, strings.TrimSpace(failure.Message), strings.TrimSpace(failure.Stage), strings.TrimSpace(failure.Code), failure.RetryAction, next, strings.TrimSpace(failure.ManualAction), time.Now().UnixMilli(), id}
	if strings.TrimSpace(failure.ExpectedState) != "" {
		query += ` AND state=? AND retry_count=?`
		args = append(args, strings.TrimSpace(failure.ExpectedState), failure.ExpectedRetryCount)
	}
	if failure.ExpectedUpdatedAt > 0 {
		query += ` AND updated_at=?`
		args = append(args, failure.ExpectedUpdatedAt)
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return AccountRecoveryTask{}, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return AccountRecoveryTask{}, err
	} else if n == 0 {
		task, err := s.GetAccountRecoveryTaskByID(ctx, id)
		if err != nil {
			return task, err
		}
		if failure.ExpectedState != "" || failure.ExpectedUpdatedAt > 0 {
			return task, ErrAccountRecoveryStale
		}
		return task, ErrAccountRecoveryNotResumable
	}
	return s.GetAccountRecoveryTaskByID(ctx, id)
}

// ListDueAccountRecoveryTasks never selects superseded tasks or tasks needing
// a person. Requeue still checks versions and exclusivity in its transaction.
func (s *Store) ListDueAccountRecoveryTasks(ctx context.Context, now time.Time, limit int) ([]AccountRecoveryTask, error) {
	return s.listDueAccountRecoveryTasks(ctx, now, limit)
}

func (s *Store) ListDueAccountRecoveryTasksForDestination(ctx context.Context, now time.Time, limit int, destination string) ([]AccountRecoveryTask, error) {
	return s.listDueAccountRecoveryTasks(ctx, now, limit, destination)
}

func (s *Store) listDueAccountRecoveryTasks(ctx context.Context, now time.Time, limit int, destinations ...string) ([]AccountRecoveryTask, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := accountRecoverySelect + ` WHERE t.state IN ('failed','unknown') AND t.retry_action IN ('resume','relogin') AND t.manual_action='' AND t.next_retry_at<=? AND t.id=(SELECT MAX(latest.id) FROM account_recovery_tasks latest WHERE latest.account_id=t.account_id`
	args := []any{now.UnixMilli()}
	if destination := recoveryDestination(destinations); destination != "" {
		query += ` AND (latest.destination_key=? OR (latest.destination_key='' AND (SELECT COUNT(DISTINCT legacy_dest.destination_key) FROM sub2_imports legacy_dest WHERE legacy_dest.account_id=latest.account_id AND legacy_dest.sub2_account_id=latest.sub2_account_id AND legacy_dest.state='imported')=1 AND EXISTS (SELECT 1 FROM sub2_imports i WHERE i.destination_key=? AND i.account_id=latest.account_id AND i.sub2_account_id=latest.sub2_account_id AND i.state='imported')))`
		args = append(args, destination, destination)
	}
	query += `) AND NOT EXISTS(SELECT 1 FROM account_recovery_tasks active WHERE active.account_id=t.account_id AND active.state IN ('queued','validating','disabling_schedule','logging_in','login_succeeded','identity_verified','refreshing_credentials','applying_credentials','credentials_applied','enabling_schedule')) ORDER BY t.next_retry_at,t.id LIMIT ?`
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

func (s *Store) ListAccountRecoveryTasks(ctx context.Context, state string, limit int) ([]AccountRecoveryTask, error) {
	return s.listAccountRecoveryTasks(ctx, state, limit)
}

func (s *Store) ListAccountRecoveryTasksForDestination(ctx context.Context, state string, limit int, destination string) ([]AccountRecoveryTask, error) {
	return s.listAccountRecoveryTasks(ctx, state, limit, destination)
}

func (s *Store) listAccountRecoveryTasks(ctx context.Context, state string, limit int, destinations ...string) ([]AccountRecoveryTask, error) {
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
	if destination := recoveryDestination(destinations); destination != "" {
		if strings.Contains(query, " WHERE ") {
			query += ` AND (t.destination_key=? OR (t.destination_key='' AND (SELECT COUNT(DISTINCT legacy_dest.destination_key) FROM sub2_imports legacy_dest WHERE legacy_dest.account_id=t.account_id AND legacy_dest.sub2_account_id=t.sub2_account_id AND legacy_dest.state='imported')=1 AND EXISTS (SELECT 1 FROM sub2_imports i WHERE i.destination_key=? AND i.account_id=t.account_id AND i.sub2_account_id=t.sub2_account_id AND i.state='imported')))`
		} else {
			query += ` WHERE (t.destination_key=? OR (t.destination_key='' AND (SELECT COUNT(DISTINCT legacy_dest.destination_key) FROM sub2_imports legacy_dest WHERE legacy_dest.account_id=t.account_id AND legacy_dest.sub2_account_id=t.sub2_account_id AND legacy_dest.state='imported')=1 AND EXISTS (SELECT 1 FROM sub2_imports i WHERE i.destination_key=? AND i.account_id=t.account_id AND i.sub2_account_id=t.sub2_account_id AND i.state='imported')))`
		}
		args = append(args, destination, destination)
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
	return s.listLatestAccountRecoveryTasks(ctx)
}

func (s *Store) ListLatestAccountRecoveryTasksForDestination(ctx context.Context, destination string) ([]AccountRecoveryTask, error) {
	return s.listLatestAccountRecoveryTasks(ctx, destination)
}

func (s *Store) listLatestAccountRecoveryTasks(ctx context.Context, destinations ...string) ([]AccountRecoveryTask, error) {
	query := accountRecoverySelect + ` WHERE t.id IN (SELECT MAX(id) FROM account_recovery_tasks latest WHERE latest.account_id=t.account_id`
	args := make([]any, 0, 1)
	if destination := recoveryDestination(destinations); destination != "" {
		query += ` AND (latest.destination_key=? OR (latest.destination_key='' AND (SELECT COUNT(DISTINCT legacy_dest.destination_key) FROM sub2_imports legacy_dest WHERE legacy_dest.account_id=latest.account_id AND legacy_dest.sub2_account_id=latest.sub2_account_id AND legacy_dest.state='imported')=1 AND EXISTS (SELECT 1 FROM sub2_imports i WHERE i.destination_key=? AND i.account_id=latest.account_id AND i.sub2_account_id=latest.sub2_account_id AND i.state='imported')))`
		args = append(args, destination, destination)
	}
	query += `) ORDER BY t.id DESC`
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

// ListAccountRecoveryHistory returns the newest recovery tasks for one account.
// The extra row lets callers indicate that older history exists without
// exposing an unbounded list to the browser.
func (s *Store) ListAccountRecoveryHistory(ctx context.Context, accountID int64, limit int, destinations ...string) ([]AccountRecoveryTask, bool, error) {
	if accountID <= 0 {
		return nil, false, ErrAccountRecoveryNotFound
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	query := accountRecoverySelect + ` WHERE t.account_id=? AND t.purpose!='delivery'`
	args := []any{accountID}
	if destination := recoveryDestination(destinations); destination != "" {
		query += ` AND (t.destination_key=? OR (t.destination_key='' AND (SELECT COUNT(DISTINCT legacy_dest.destination_key) FROM sub2_imports legacy_dest WHERE legacy_dest.account_id=t.account_id AND legacy_dest.sub2_account_id=t.sub2_account_id AND legacy_dest.state='imported')=1 AND EXISTS (SELECT 1 FROM sub2_imports i WHERE i.destination_key=? AND i.account_id=t.account_id AND i.sub2_account_id=t.sub2_account_id AND i.state='imported')))`
		args = append(args, destination, destination)
	}
	query += ` ORDER BY t.id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	tasks := make([]AccountRecoveryTask, 0, limit)
	for rows.Next() {
		task, err := scanAccountRecoveryTask(rows)
		if err != nil {
			return nil, false, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(tasks) > limit
	if truncated {
		tasks = tasks[:limit]
	}
	return tasks, truncated, nil
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
	var observedMissing bool
	err := tx.QueryRowContext(ctx, `SELECT t.account_id,t.source_credential_attempt_id,t.result_credential_attempt_id,COALESCE((SELECT MAX(id) FROM login_attempts WHERE account_id=t.account_id AND status='success'),0),EXISTS(SELECT 1 FROM account_checks c WHERE c.id=t.check_id AND c.account_id=t.account_id AND c.state='finished' AND c.outcome='credential_missing' AND c.credential_attempt_id=0) FROM account_recovery_tasks t WHERE t.id=?`, taskID).Scan(&owner, &source, &result, &current, &observedMissing)
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
	// A verified missing-token observation can start the first successful
	// login. Version zero is still rejected for tasks without that observation,
	// and any intervening successful login invalidates the task as usual.
	if expected != current || (expected == 0 && !observedMissing) {
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
	return s.RetryAccountRecoveryTask(ctx, taskID, false)
}

// RetryAccountRecoveryTask requeues one latest task, preserving its failure
// count. A relogin discards only this task's current checkpoint, never a newer
// successful login. Callers must prove that relogin is appropriate and recheck
// the binding and remote state before mutations.
func (s *Store) RetryAccountRecoveryTask(ctx context.Context, taskID int64, relogin bool) (AccountRecoveryTask, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountRecoveryTask{}, err
	}
	defer tx.Rollback()
	task, err := retryAccountRecoveryTaskTx(ctx, tx, taskID, relogin)
	if err != nil {
		return task, err
	}
	if err := tx.Commit(); err != nil {
		return task, err
	}
	return s.GetAccountRecoveryTaskByID(ctx, taskID)
}

func retryAccountRecoveryTaskTx(ctx context.Context, tx *sql.Tx, taskID int64, relogin bool) (AccountRecoveryTask, error) {
	task, err := scanAccountRecoveryTask(tx.QueryRowContext(ctx, accountRecoverySelect+` WHERE t.id=?`, taskID))
	if err != nil {
		return task, err
	}
	if task.State != RecoveryFailed && task.State != RecoveryUnknown {
		return task, ErrAccountRecoveryNotResumable
	}
	if !relogin && task.ResultCredentialAttemptID == 0 {
		return task, ErrAccountRecoveryNotResumable
	}
	if err := validateRecoveryVersionTx(ctx, tx, taskID, task.AccountID); err != nil {
		return task, err
	}
	if !relogin && !task.Resumable {
		return task, ErrAccountRecoveryNotResumable
	}
	var newer, active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_recovery_tasks WHERE account_id=? AND id>?),EXISTS(SELECT 1 FROM account_recovery_tasks WHERE account_id=? AND state IN ('queued','validating','disabling_schedule','logging_in','login_succeeded','identity_verified','refreshing_credentials','applying_credentials','credentials_applied','enabling_schedule'))`, task.AccountID, task.ID, task.AccountID).Scan(&newer, &active); err != nil {
		return task, err
	}
	if newer || active {
		return task, ErrAccountBusy
	}
	action := "resume"
	if relogin {
		action = "relogin"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_recovery_tasks SET state='queued',last_error='',retry_action=?,next_retry_at=NULL,manual_action='',source_credential_attempt_id=CASE WHEN ? AND result_credential_attempt_id>0 THEN result_credential_attempt_id ELSE source_credential_attempt_id END,result_credential_attempt_id=CASE WHEN ? THEN 0 ELSE result_credential_attempt_id END,updated_at=? WHERE id=?`, action, relogin, relogin, time.Now().UnixMilli(), task.ID); err != nil {
		return task, err
	}
	return task, nil
}
