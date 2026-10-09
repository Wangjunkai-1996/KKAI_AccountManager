package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrAccountDeliveryNotFound       = errors.New("account delivery not found")
	ErrAccountDeliverySettled        = errors.New("account delivery is already settled")
	ErrAccountDeliveryRequiresAction = errors.New("account delivery requires manual action")
)

// AccountDelivery tracks a login's handoff without exposing its credentials or
// counting first delivery as an account recovery.
type AccountDelivery struct {
	Options           DeliveryOptions `json:"delivery_options"`
	ID                int64           `json:"id"`
	AccountID         int64           `json:"account_id"`
	CredentialVersion int64           `json:"-"`
	DestinationKey    string          `json:"-"`
	RecoveryTaskID    int64           `json:"recovery_task_id,omitempty"`
	State             string          `json:"state"`
	LastError         string          `json:"last_error,omitempty"`
	NextRetryAt       *time.Time      `json:"next_retry_at,omitempty"`
	ManualAction      string          `json:"manual_action,omitempty"`
	RequiresAction    bool            `json:"requires_action"`
	RetryCount        int             `json:"retry_count"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

func (s *Store) migrateAccountDeliveries() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS account_deliveries (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 credential_version INTEGER NOT NULL,
 destination_key TEXT NOT NULL,
 recovery_task_id INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL,
 last_error TEXT NOT NULL DEFAULT '',
 next_retry_at INTEGER,
 manual_action TEXT NOT NULL DEFAULT '',
 retry_count INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 UNIQUE(destination_key,account_id,credential_version)
);
CREATE INDEX IF NOT EXISTS account_delivery_history ON account_deliveries(account_id,id DESC);`)
	if err != nil {
		return err
	}
	return s.migrateDeliveryOptions()
}

const accountDeliverySelect = `SELECT id,account_id,credential_version,destination_key,recovery_task_id,state,last_error,next_retry_at,manual_action,retry_count,created_at,updated_at,options_json FROM account_deliveries`

func scanAccountDelivery(row scanner) (AccountDelivery, error) {
	var delivery AccountDelivery
	var options string
	var next sql.NullInt64
	var created, updated int64
	if err := row.Scan(&delivery.ID, &delivery.AccountID, &delivery.CredentialVersion, &delivery.DestinationKey, &delivery.RecoveryTaskID, &delivery.State, &delivery.LastError, &next, &delivery.ManualAction, &delivery.RetryCount, &created, &updated, &options); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AccountDelivery{}, ErrAccountDeliveryNotFound
		}
		return AccountDelivery{}, err
	}
	if err := json.Unmarshal([]byte(options), &delivery.Options); err != nil {
		return delivery, err
	}
	if next.Valid {
		at := time.UnixMilli(next.Int64)
		delivery.NextRetryAt = &at
	}
	delivery.RequiresAction = delivery.State == "requires_action" || delivery.ManualAction != ""
	delivery.CreatedAt, delivery.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
	return delivery, nil
}

// FinishAttemptAndQueueDelivery commits the encrypted OAuth result and its
// delivery intent together. The caller retains the login attempt's account lock.
func (s *Store) FinishAttemptAndQueueDelivery(ctx context.Context, attemptID int64, result *Result, destination string, options ...DeliveryOptions) (AccountDelivery, error) {
	opt := DefaultDeliveryOptions()
	if len(options) > 0 {
		opt = options[0]
	}
	if err := opt.Validate(); err != nil {
		return AccountDelivery{}, err
	}
	raw, _ := json.Marshal(opt)
	destination = strings.TrimSpace(destination)
	if result == nil || strings.TrimSpace(result.AccessToken) == "" || strings.TrimSpace(result.RefreshToken) == "" || destination == "" {
		return AccountDelivery{}, errors.New("complete OAuth tokens and delivery destination are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountDelivery{}, err
	}
	defer tx.Rollback()
	if err := s.finishAttemptTx(ctx, tx, attemptID, true, result, nil); err != nil {
		return AccountDelivery{}, err
	}
	var accountID int64
	if err := tx.QueryRowContext(ctx, `SELECT account_id FROM login_attempts WHERE id=?`, attemptID).Scan(&accountID); err != nil {
		return AccountDelivery{}, err
	}
	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE account_deliveries SET state='canceled',last_error='已有更新的登录结果，旧交付已取消',next_retry_at=NULL,manual_action='',updated_at=? WHERE account_id=? AND recovery_task_id=0 AND state NOT IN ('completed','canceled')`, now, accountID); err != nil {
		return AccountDelivery{}, err
	}
	insert, err := tx.ExecContext(ctx, `INSERT INTO account_deliveries(account_id,credential_version,destination_key,state,created_at,updated_at,options_json) VALUES(?,?,?,'queued',?,?,?)`, accountID, attemptID, destination, now, now, string(raw))
	if err != nil {
		return AccountDelivery{}, err
	}
	id, err := insert.LastInsertId()
	if err != nil {
		return AccountDelivery{}, err
	}
	if err := tx.Commit(); err != nil {
		return AccountDelivery{}, err
	}
	return s.GetAccountDelivery(ctx, id)
}

func (s *Store) GetAccountDelivery(ctx context.Context, id int64) (AccountDelivery, error) {
	return scanAccountDelivery(s.db.QueryRowContext(ctx, accountDeliverySelect+` WHERE id=?`, id))
}

func (s *Store) ListLatestAccountDeliveries(ctx context.Context) ([]AccountDelivery, error) {
	return s.listAccountDeliveries(ctx, accountDeliverySelect+` WHERE id IN (SELECT MAX(id) FROM account_deliveries GROUP BY account_id) ORDER BY id DESC`)
}

func (s *Store) ListDueAccountDeliveries(ctx context.Context, now time.Time, limit int) ([]AccountDelivery, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.listAccountDeliveries(ctx, accountDeliverySelect+` WHERE id IN (SELECT MAX(id) FROM account_deliveries GROUP BY account_id) AND recovery_task_id=0 AND state IN ('queued','checking','importing','retry_wait') AND manual_action='' AND (next_retry_at IS NULL OR next_retry_at<=?) ORDER BY COALESCE(next_retry_at,created_at),id LIMIT ?`, now.UnixMilli(), limit)
}

func (s *Store) listAccountDeliveries(ctx context.Context, query string, args ...any) ([]AccountDelivery, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	deliveries := make([]AccountDelivery, 0)
	for rows.Next() {
		delivery, err := scanAccountDelivery(rows)
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

// UpdateAccountDelivery preserves an established handoff when recoveryTaskID
// is zero. A nonzero ID must belong to this delivery and cannot replace a link.
func (s *Store) UpdateAccountDelivery(ctx context.Context, id int64, state, message, manualAction string, nextRetryAt *time.Time, recoveryTaskID int64) (AccountDelivery, error) {
	switch state {
	case "queued", "checking", "importing", "verifying", "completed", "retry_wait", "requires_action", "canceled":
	default:
		return AccountDelivery{}, errors.New("invalid account delivery state")
	}
	manualAction = strings.TrimSpace(manualAction)
	if recoveryTaskID < 0 {
		return AccountDelivery{}, errors.New("invalid delivery recovery task")
	}
	var next any
	if nextRetryAt != nil {
		if nextRetryAt.UnixMilli() <= 0 || manualAction != "" || state == "requires_action" || state == "completed" || state == "canceled" {
			return AccountDelivery{}, errors.New("invalid account delivery retry deadline")
		}
		next = nextRetryAt.UnixMilli()
	}
	if state == "completed" || state == "canceled" {
		manualAction = ""
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountDelivery{}, err
	}
	defer tx.Rollback()
	delivery, err := scanAccountDelivery(tx.QueryRowContext(ctx, accountDeliverySelect+` WHERE id=?`, id))
	if err != nil {
		return delivery, err
	}
	if delivery.State == "completed" || delivery.State == "canceled" {
		if delivery.State == state {
			return delivery, nil
		}
		return delivery, ErrAccountDeliverySettled
	}
	if recoveryTaskID > 0 {
		var matches bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_recovery_tasks WHERE id=? AND delivery_id=? AND account_id=? AND purpose='delivery')`, recoveryTaskID, id, delivery.AccountID).Scan(&matches); err != nil {
			return delivery, err
		}
		if !matches || (delivery.RecoveryTaskID != 0 && delivery.RecoveryTaskID != recoveryTaskID) {
			return delivery, errors.New("delivery recovery task identity mismatch")
		}
	} else {
		recoveryTaskID = delivery.RecoveryTaskID
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_deliveries SET state=?,last_error=?,manual_action=?,next_retry_at=?,recovery_task_id=?,retry_count=retry_count+?,updated_at=? WHERE id=?`, state, strings.TrimSpace(message), manualAction, next, recoveryTaskID, boolInt(state == "retry_wait"), time.Now().UnixMilli(), id); err != nil {
		return delivery, err
	}
	if err := tx.Commit(); err != nil {
		return delivery, err
	}
	return s.GetAccountDelivery(ctx, id)
}
