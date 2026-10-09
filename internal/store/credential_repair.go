package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type CredentialPatch struct {
	Password, TOTPSecret, Proxy *string
}

type CredentialRepair struct {
	AccountID      int64      `json:"account_id"`
	Revision       int64      `json:"-"`
	SourceVersion  int64      `json:"-"`
	AttemptID      int64      `json:"-"`
	State          string     `json:"state"`
	RetryCount     int        `json:"retry_count"`
	NextRetryAt    *time.Time `json:"next_retry_at,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	ManualAction   string     `json:"manual_action,omitempty"`
	RequiresAction bool       `json:"requires_action"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (s *Store) migrateCredentialRepairs() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS account_credential_repairs (
 account_id INTEGER PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL DEFAULT 1,
 source_version INTEGER NOT NULL DEFAULT 0,
 attempt_id INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL,
 retry_count INTEGER NOT NULL DEFAULT 0,
 next_retry_at INTEGER,
 last_error TEXT NOT NULL DEFAULT '',
 manual_action TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL
);`)
	return err
}

const credentialRepairSelect = `SELECT account_id,revision,source_version,attempt_id,state,retry_count,next_retry_at,last_error,manual_action,updated_at FROM account_credential_repairs`

func scanCredentialRepair(row scanner) (CredentialRepair, error) {
	var repair CredentialRepair
	var due sql.NullInt64
	var updated int64
	err := row.Scan(&repair.AccountID, &repair.Revision, &repair.SourceVersion, &repair.AttemptID, &repair.State, &repair.RetryCount, &due, &repair.LastError, &repair.ManualAction, &updated)
	if err != nil {
		return repair, err
	}
	if due.Valid {
		at := time.UnixMilli(due.Int64)
		repair.NextRetryAt = &at
	}
	repair.RequiresAction, repair.UpdatedAt = repair.State == "requires_action", time.UnixMilli(updated)
	return repair, nil
}

// PatchAccountCredentials commits encrypted edits and the durable continuation
// together. It never changes identity or stored OAuth credentials.
func (s *Store) PatchAccountCredentials(ctx context.Context, id int64, patch CredentialPatch, autoContinue bool) (CredentialRepair, error) {
	if patch.Password == nil && patch.TOTPSecret == nil && patch.Proxy == nil || patch.Password != nil && *patch.Password == "" {
		return CredentialRepair{}, errors.New("credential changes are required")
	}
	lease, err := s.AcquireAccountRecovery(ctx, id)
	if err != nil {
		return CredentialRepair{}, err
	}
	defer lease.Release()
	values := make([][]byte, 3)
	for i, raw := range []*string{patch.Password, patch.TOTPSecret, patch.Proxy} {
		if raw != nil {
			values[i], err = s.seal(*raw)
			if err != nil {
				return CredentialRepair{}, err
			}
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CredentialRepair{}, err
	}
	defer tx.Rollback()
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_recovery_tasks WHERE account_id=? AND state IN ('queued','validating','disabling_schedule','logging_in','login_succeeded','identity_verified','refreshing_credentials','applying_credentials','credentials_applied','enabling_schedule'))`, id).Scan(&active); err != nil {
		return CredentialRepair{}, err
	}
	if active {
		return CredentialRepair{}, ErrAccountBusy
	}
	now := time.Now()
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET password_cipher=CASE WHEN ? THEN ? ELSE password_cipher END,totp_cipher=CASE WHEN ? THEN ? ELSE totp_cipher END,proxy_cipher=CASE WHEN ? THEN ? ELSE proxy_cipher END,updated_at=? WHERE id=?`, patch.Password != nil, values[0], patch.TOTPSecret != nil, values[1], patch.Proxy != nil, values[2], now.Unix(), id); err != nil {
		return CredentialRepair{}, err
	}
	state := "queued"
	if !autoContinue {
		state = "canceled"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_credential_repairs(account_id,source_version,state,next_retry_at,updated_at) VALUES(?,COALESCE((SELECT MAX(id) FROM login_attempts WHERE account_id=? AND status='success'),0),?,?,?) ON CONFLICT(account_id) DO UPDATE SET revision=account_credential_repairs.revision+1,source_version=excluded.source_version,attempt_id=0,state=excluded.state,retry_count=0,next_retry_at=excluded.next_retry_at,last_error='',manual_action='',updated_at=excluded.updated_at`, id, id, state, now.UnixMilli(), now.UnixMilli()); err != nil {
		return CredentialRepair{}, err
	}
	if err := tx.Commit(); err != nil {
		return CredentialRepair{}, err
	}
	return s.GetCredentialRepair(ctx, id)
}

func (s *Store) GetCredentialRepair(ctx context.Context, accountID int64) (CredentialRepair, error) {
	return scanCredentialRepair(s.db.QueryRowContext(ctx, credentialRepairSelect+` WHERE account_id=?`, accountID))
}

func (s *Store) ListCredentialRepairs(ctx context.Context) (map[int64]CredentialRepair, error) {
	rows, err := s.db.QueryContext(ctx, credentialRepairSelect)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	all := make(map[int64]CredentialRepair)
	for rows.Next() {
		repair, err := scanCredentialRepair(rows)
		if err != nil {
			return nil, err
		}
		all[repair.AccountID] = repair
	}
	return all, rows.Err()
}

func (s *Store) ClaimCredentialRepair(ctx context.Context, now time.Time) (*CredentialRepair, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	repair, err := scanCredentialRepair(tx.QueryRowContext(ctx, credentialRepairSelect+` WHERE state IN ('queued','retry_wait') AND next_retry_at<=? ORDER BY next_retry_at,account_id LIMIT 1`, now.UnixMilli()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_credential_repairs SET state='checking',updated_at=? WHERE account_id=?`, now.UnixMilli(), repair.AccountID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	repair.State, repair.UpdatedAt = "checking", now
	return &repair, nil
}

func (s *Store) SetCredentialRepairAttempt(ctx context.Context, repair CredentialRepair, attemptID int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE account_credential_repairs SET attempt_id=?,updated_at=? WHERE account_id=? AND revision=? AND state='checking' AND EXISTS(SELECT 1 FROM login_attempts WHERE id=? AND account_id=?)`, attemptID, time.Now().UnixMilli(), repair.AccountID, repair.Revision, attemptID, repair.AccountID)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count == 0 {
		return ErrAccountBusy
	}
	return nil
}

func (s *Store) FinishCredentialRepair(ctx context.Context, repair CredentialRepair, state, message string, next *time.Time) error {
	switch state {
	case "completed", "canceled", "retry_wait", "requires_action":
	default:
		return errors.New("invalid credential repair state")
	}
	var due any
	if state == "retry_wait" {
		if next == nil {
			return errors.New("repair retry requires a deadline")
		}
		due = next.UnixMilli()
	}
	manual := ""
	if state == "requires_action" {
		manual = message
	}
	_, err := s.db.ExecContext(ctx, `UPDATE account_credential_repairs SET state=?,next_retry_at=?,last_error=?,manual_action=?,retry_count=retry_count+?,updated_at=? WHERE account_id=? AND revision=? AND state='checking'`, state, due, message, manual, boolInt(state == "retry_wait"), time.Now().UnixMilli(), repair.AccountID, repair.Revision)
	return err
}

// RetryAccountRecoveryForCredentialRepair consumes one saved edit together
// with its recovery handoff. A restart must not replay that edit after the
// recovery has independently failed again or received a Retry-After deadline.
func (s *Store) RetryAccountRecoveryForCredentialRepair(ctx context.Context, repair CredentialRepair, taskID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := scanCredentialRepair(tx.QueryRowContext(ctx, credentialRepairSelect+` WHERE account_id=?`, repair.AccountID))
	if err != nil {
		return err
	}
	if current.Revision != repair.Revision || current.State != "checking" {
		return ErrAccountBusy
	}
	task, err := retryAccountRecoveryTaskTx(ctx, tx, taskID, true)
	if err != nil {
		return err
	}
	if task.AccountID != repair.AccountID {
		return ErrAccountNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_credential_repairs SET state='completed',next_retry_at=NULL,manual_action='',last_error='新资料已保存，已继续重新登录和恢复',updated_at=? WHERE account_id=? AND revision=?`, time.Now().UnixMilli(), repair.AccountID, repair.Revision); err != nil {
		return err
	}
	return tx.Commit()
}
