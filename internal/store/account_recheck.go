package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const accountRecoveryFirstRecheckDelay = 30 * time.Second

// AccountRecoveryRecheck tracks the two follow-up probes without changing the
// successful recovery record or counting a probe as another recovery.
type AccountRecoveryRecheck struct {
	TaskID         int64      `json:"task_id"`
	AccountID      int64      `json:"account_id"`
	State          string     `json:"state"`
	Round          int        `json:"round"`
	NextCheckAt    *time.Time `json:"next_check_at,omitempty"`
	RetryCount     int        `json:"retry_count"`
	LastError      string     `json:"last_error,omitempty"`
	ManualAction   string     `json:"manual_action,omitempty"`
	RequiresAction bool       `json:"requires_action"`
	RecoveryTaskID int64      `json:"recovery_task_id,omitempty"`
	CompletedAt    time.Time  `json:"completed_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (s *Store) migrateAccountRecoveryRechecks() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS account_recovery_rechecks (
 task_id INTEGER PRIMARY KEY REFERENCES account_recovery_tasks(id) ON DELETE CASCADE,
 state TEXT NOT NULL DEFAULT 'pending',
 round INTEGER NOT NULL DEFAULT 1,
 next_check_at INTEGER,
 retry_count INTEGER NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL DEFAULT '',
 manual_action TEXT NOT NULL DEFAULT '',
 recovery_task_id INTEGER NOT NULL DEFAULT 0,
 completed_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS account_recheck_due ON account_recovery_rechecks(state,next_check_at);`)
	if err != nil {
		return err
	}
	// Only shorten untouched legacy plans; preserve retries and their backoff.
	_, err = s.db.Exec(`UPDATE account_recovery_rechecks SET next_check_at=completed_at+?,updated_at=?
 WHERE state='pending' AND round=1 AND retry_count=0 AND last_error='' AND manual_action=''
 AND next_check_at=completed_at+300000 AND task_id IN (
 SELECT t.id FROM account_recovery_tasks t WHERE t.state='completed'
 AND t.id=(SELECT MAX(id) FROM account_recovery_tasks WHERE account_id=t.account_id)
 )`, accountRecoveryFirstRecheckDelay.Milliseconds(), time.Now().UnixMilli())
	return err
}

const accountRecheckSelect = `SELECT r.task_id,t.account_id,r.state,r.round,r.next_check_at,r.retry_count,r.last_error,r.manual_action,r.recovery_task_id,r.completed_at,r.updated_at FROM account_recovery_rechecks r JOIN account_recovery_tasks t ON t.id=r.task_id`

func scanAccountRecoveryRecheck(row scanner) (AccountRecoveryRecheck, error) {
	var result AccountRecoveryRecheck
	var due sql.NullInt64
	var completed, updated int64
	err := row.Scan(&result.TaskID, &result.AccountID, &result.State, &result.Round, &due, &result.RetryCount, &result.LastError, &result.ManualAction, &result.RecoveryTaskID, &completed, &updated)
	if err != nil {
		return result, err
	}
	if due.Valid {
		at := time.UnixMilli(due.Int64)
		result.NextCheckAt = &at
	}
	result.RequiresAction = result.State == "requires_action"
	result.CompletedAt, result.UpdatedAt = time.UnixMilli(completed), time.UnixMilli(updated)
	return result, nil
}

func (s *Store) ListAccountRecoveryRechecks(ctx context.Context) (map[int64]AccountRecoveryRecheck, error) {
	rows, err := s.db.QueryContext(ctx, accountRecheckSelect+` WHERE t.id=(SELECT MAX(id) FROM account_recovery_tasks WHERE account_id=t.account_id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]AccountRecoveryRecheck)
	for rows.Next() {
		item, err := scanAccountRecoveryRecheck(rows)
		if err != nil {
			return nil, err
		}
		out[item.AccountID] = item
	}
	return out, rows.Err()
}

// RetryAccountRecoveryRecheck is called after account/configuration details
// were corrected. The worker still revalidates versions, binding and pauses.
func (s *Store) RetryAccountRecoveryRecheck(ctx context.Context, accountID int64) (bool, error) {
	now := time.Now().UnixMilli()
	result, err := s.db.ExecContext(ctx, `UPDATE account_recovery_rechecks SET state='pending',next_check_at=?,manual_action='',last_error='',retry_count=0,updated_at=? WHERE state='requires_action' AND task_id=(SELECT id FROM account_recovery_tasks WHERE account_id=? ORDER BY id DESC LIMIT 1)`, now, now, accountID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (s *Store) ClaimAccountRecoveryRecheck(ctx context.Context, now time.Time) (*AccountRecoveryRecheck, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	recheck, err := scanAccountRecoveryRecheck(tx.QueryRowContext(ctx, accountRecheckSelect+` WHERE r.state='pending' AND r.next_check_at<=? AND t.state='completed' AND t.id=(SELECT MAX(id) FROM account_recovery_tasks WHERE account_id=t.account_id) ORDER BY r.next_check_at LIMIT 1`, now.UnixMilli()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_recovery_rechecks SET state='checking',updated_at=? WHERE task_id=?`, now.UnixMilli(), recheck.TaskID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	recheck.State, recheck.UpdatedAt = "checking", now
	return &recheck, nil
}

// FinishAccountRecoveryRecheck keeps retry failures separate from the original
// successful operation. Round two is anchored to completion, not round one's
// retry time, so a delayed first probe cannot push it out indefinitely.
func (s *Store) FinishAccountRecoveryRecheck(ctx context.Context, taskID int64, state, message string, next *time.Time) error {
	switch state {
	case "passed", "pending", "requires_action", "canceled":
	default:
		return errors.New("invalid recovery recheck state")
	}
	var due any
	if next != nil {
		due = next.UnixMilli()
	}
	if state == "pending" && next == nil {
		return errors.New("recheck retry requires a deadline")
	}
	manual := ""
	if state == "requires_action" {
		manual = message
	}
	_, err := s.db.ExecContext(ctx, `UPDATE account_recovery_rechecks SET
 state=CASE WHEN ?='passed' AND round=1 THEN 'pending' ELSE ? END,
 next_check_at=CASE WHEN ?='passed' AND round=1 THEN completed_at+1800000 ELSE ? END,
 round=CASE WHEN ?='passed' AND round=1 THEN 2 ELSE round END,
 retry_count=CASE WHEN ?='pending' THEN retry_count+1 ELSE 0 END,
 last_error=?,manual_action=?,updated_at=? WHERE task_id=? AND state='checking'`, state, state, state, due, state, state, message, manual, time.Now().UnixMilli(), taskID)
	return err
}

// QueueAccountRecoveryFromRecheck turns a persisted explicit credential failure
// into one new login task. The prior recovery remains completed, and a crash
// cannot enqueue a second login for the same probe.
func (s *Store) QueueAccountRecoveryFromRecheck(ctx context.Context, parentID int64) (AccountRecoveryTask, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountRecoveryTask{}, err
	}
	defer tx.Rollback()
	parent, err := scanAccountRecoveryTask(tx.QueryRowContext(ctx, accountRecoverySelect+` WHERE t.id=?`, parentID))
	if err != nil {
		return AccountRecoveryTask{}, err
	}
	check, err := scanAccountRecoveryRecheck(tx.QueryRowContext(ctx, accountRecheckSelect+` WHERE r.task_id=?`, parentID))
	if err != nil {
		return AccountRecoveryTask{}, err
	}
	if check.RecoveryTaskID > 0 {
		return scanAccountRecoveryTask(tx.QueryRowContext(ctx, accountRecoverySelect+` WHERE t.id=?`, check.RecoveryTaskID))
	}
	if parent.State != RecoveryCompleted || check.State != "checking" || parent.ResultCredentialAttemptID <= 0 || !parent.OriginalSchedulable {
		return AccountRecoveryTask{}, ErrAccountRecoveryNotResumable
	}
	if err := validateRecoveryVersionTx(ctx, tx, parent.ID, parent.AccountID); err != nil {
		return AccountRecoveryTask{}, err
	}
	var latest int64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(id) FROM account_recovery_tasks WHERE account_id=?`, parent.AccountID).Scan(&latest); err != nil {
		return AccountRecoveryTask{}, err
	}
	if latest != parent.ID {
		return AccountRecoveryTask{}, ErrAccountBusy
	}
	now := time.Now().UnixMilli()
	result, err := tx.ExecContext(ctx, `INSERT INTO account_recovery_tasks(account_id,source_credential_attempt_id,sub2_account_id,original_schedulable,state,failure_stage,error_code,retry_action,created_at,updated_at) VALUES (?,?,?,1,'queued','delayed_recheck','credential_invalid','relogin',?,?)`, parent.AccountID, parent.ResultCredentialAttemptID, parent.Sub2AccountID, now, now)
	if err != nil {
		return AccountRecoveryTask{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return AccountRecoveryTask{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_recovery_rechecks SET state='recovery_queued',next_check_at=NULL,last_error='Sub2 实际凭据已失效，已自动重新登录',manual_action='',recovery_task_id=?,updated_at=? WHERE task_id=?`, id, now, parent.ID); err != nil {
		return AccountRecoveryTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return AccountRecoveryTask{}, err
	}
	return s.GetAccountRecoveryTaskByID(ctx, id)
}

// GetAccountRecoveryRecheckParent supplies the durable ownership proof when a
// delayed probe queued a fresh recovery before Sub2 changed its status to error.
func (s *Store) GetAccountRecoveryRecheckParent(ctx context.Context, taskID int64) (AccountRecoveryTask, error) {
	return scanAccountRecoveryTask(s.db.QueryRowContext(ctx, accountRecoverySelect+` WHERE t.id=(SELECT task_id FROM account_recovery_rechecks WHERE recovery_task_id=? AND state='recovery_queued')`, taskID))
}
