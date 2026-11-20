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
	SourceCredentialAttemptID int64               `json:"source_credential_attempt_id,omitempty"`
	ResultCredentialAttemptID int64               `json:"result_credential_attempt_id,omitempty"`
	OriginalBindingID         int64               `json:"-"`
	OriginalRemoteID          int64               `json:"-"`
	OriginalBindingVersion    int64               `json:"-"`
	ImportOperationID         string              `json:"-"`
	BlockedReason             string              `json:"blocked_reason,omitempty"` // Runtime status; never persisted.
	Options                   DeliveryOptions     `json:"delivery_options"`
	ID                        int64               `json:"id"`
	AccountID                 int64               `json:"account_id"`
	AllowCreate               bool                `json:"-"`
	CredentialVersion         int64               `json:"-"`
	DestinationKey            string              `json:"-"`
	RecoveryTaskID            int64               `json:"recovery_task_id,omitempty"`
	State                     string              `json:"state"`
	LastError                 string              `json:"last_error,omitempty"`
	FailureStage              string              `json:"failure_stage,omitempty"`
	ErrorCode                 string              `json:"error_code,omitempty"`
	HTTPStatus                int                 `json:"http_status,omitempty"`
	Diagnostic                *RecoveryDiagnostic `json:"diagnostic,omitempty"`
	NextRetryAt               *time.Time          `json:"next_retry_at,omitempty"`
	ManualAction              string              `json:"manual_action,omitempty"`
	RequiresAction            bool                `json:"requires_action"`
	RetryCount                int                 `json:"retry_count"`
	CreatedAt                 time.Time           `json:"created_at"`
	UpdatedAt                 time.Time           `json:"updated_at"`
}

// MatchesOriginalBinding checks the exact target captured at login. Older
// deliveries retain their existing operation check until a fresh login.
func (d AccountDelivery) MatchesOriginalBinding(b Sub2Import) bool {
	if b.AccountID != d.AccountID || b.DestinationKey != d.DestinationKey || b.State != "imported" || !b.CreateAcknowledged || b.Sub2AccountID <= 0 {
		return false
	}
	if d.ImportOperationID != "" && b.OperationID != d.ImportOperationID {
		return false
	}
	return d.OriginalBindingID == 0 || (d.OriginalBindingID == b.ID && d.OriginalRemoteID == b.Sub2AccountID && d.OriginalBindingVersion == b.CredentialVersion)
}

func (s *Store) migrateAccountDeliveries() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS account_deliveries (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 credential_version INTEGER NOT NULL,
 destination_key TEXT NOT NULL,
 recovery_task_id INTEGER NOT NULL DEFAULT 0,
 allow_create INTEGER NOT NULL DEFAULT 0,
 import_operation_id TEXT NOT NULL DEFAULT '',
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
	_, err = s.db.Exec(`UPDATE account_recovery_tasks SET destination_key=(SELECT d.destination_key FROM account_deliveries d WHERE d.id=account_recovery_tasks.delivery_id) WHERE delivery_id>0 AND EXISTS (SELECT 1 FROM account_deliveries d WHERE d.id=account_recovery_tasks.delivery_id)`)
	if err != nil {
		return err
	}
	rows, err := s.db.Query(`PRAGMA table_info(account_deliveries)`)
	if err != nil {
		return err
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
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
	for _, column := range []string{"allow_create", "import_operation_id", "original_binding_id", "original_remote_id", "original_binding_version"} {
		if columns[column] {
			continue
		}
		declaration := " INTEGER NOT NULL DEFAULT 0"
		if column == "import_operation_id" {
			declaration = " TEXT NOT NULL DEFAULT ''"
		}
		if _, err := s.db.Exec("ALTER TABLE account_deliveries ADD COLUMN " + column + declaration); err != nil {
			return err
		}
	}
	return s.migrateDeliveryOptions()
}

const accountDeliverySelect = `SELECT id,account_id,credential_version,destination_key,recovery_task_id,state,last_error,next_retry_at,manual_action,retry_count,created_at,updated_at,options_json,allow_create,import_operation_id,original_binding_id,original_remote_id,original_binding_version FROM account_deliveries`

func scanAccountDelivery(row scanner) (AccountDelivery, error) {
	var delivery AccountDelivery
	var options string
	var next sql.NullInt64
	var created, updated int64
	if err := row.Scan(&delivery.ID, &delivery.AccountID, &delivery.CredentialVersion, &delivery.DestinationKey, &delivery.RecoveryTaskID, &delivery.State, &delivery.LastError, &next, &delivery.ManualAction, &delivery.RetryCount, &created, &updated, &options, &delivery.AllowCreate, &delivery.ImportOperationID, &delivery.OriginalBindingID, &delivery.OriginalRemoteID, &delivery.OriginalBindingVersion); err != nil {
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
	delivery.SourceCredentialAttemptID = delivery.CredentialVersion
	delivery.RequiresAction = delivery.State == "requires_action" || delivery.ManualAction != ""
	delivery.CreatedAt, delivery.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
	return delivery, nil
}

// FinishAttemptAndQueueDelivery commits the encrypted OAuth result and its
// delivery intent together. The caller retains the login attempt's account lock.
func (s *Store) FinishAttemptAndQueueDelivery(ctx context.Context, attemptID int64, result *Result, destination string, options ...DeliveryOptions) (AccountDelivery, error) {
	return s.finishAttemptAndQueueDelivery(ctx, attemptID, result, destination, true, false, options...)
}

// FinishAttemptAndQueueDeliveryUpdate preserves repair's update-only intent.
func (s *Store) FinishAttemptAndQueueDeliveryUpdate(ctx context.Context, attemptID int64, result *Result, destination string, options ...DeliveryOptions) (AccountDelivery, error) {
	return s.finishAttemptAndQueueDelivery(ctx, attemptID, result, destination, false, false, options...)
}

// FinishBoundRelogin saves a history login and updates its initial binding.
// An unbound local-only login has no delivery row.
func (s *Store) FinishBoundRelogin(ctx context.Context, attemptID int64, result *Result, destination string) (AccountDelivery, error) {
	return s.finishAttemptAndQueueDelivery(ctx, attemptID, result, destination, false, true)
}

func (s *Store) finishAttemptAndQueueDelivery(ctx context.Context, attemptID int64, result *Result, destination string, allowCreate, onlyIfBound bool, options ...DeliveryOptions) (AccountDelivery, error) {
	opt := DefaultDeliveryOptions()
	if len(options) > 0 {
		opt = options[0]
	}
	if err := opt.Validate(); err != nil {
		return AccountDelivery{}, err
	}
	raw, _ := json.Marshal(opt)
	destination = strings.TrimSpace(destination)
	if result == nil || destination == "" {
		return AccountDelivery{}, errors.New("complete OAuth tokens and delivery destination are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountDelivery{}, err
	}
	defer tx.Rollback()
	var bindingID, remoteID, bindingVersion int64
	var operationID string
	var bindingChanged bool
	// Compare the start-of-request snapshot before finishAttemptTx consumes it.
	err = tx.QueryRowContext(ctx, `SELECT b.binding_id,b.sub2_id,b.binding_version,b.operation,
    NOT EXISTS(SELECT 1 FROM sub2_imports i WHERE i.id=b.binding_id AND i.account_id=b.account_id
     AND i.destination_key=b.destination AND i.sub2_account_id=b.sub2_id AND i.operation_id=b.operation
     AND i.credential_version=b.binding_version AND i.state='imported' AND i.create_acknowledged=1)
   FROM lifecycle_request_bindings b WHERE b.request_key='login_attempt:' || ? AND b.destination=?`, attemptID, destination).
		Scan(&bindingID, &remoteID, &bindingVersion, &operationID, &bindingChanged)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AccountDelivery{}, err
	}
	if err := s.finishAttemptTx(ctx, tx, attemptID, true, result, nil); err != nil {
		return AccountDelivery{}, err
	}
	if onlyIfBound && bindingID == 0 {
		return AccountDelivery{}, tx.Commit()
	}
	if strings.TrimSpace(result.AccessToken) == "" || strings.TrimSpace(result.RefreshToken) == "" {
		return AccountDelivery{}, errors.New("complete OAuth tokens are required")
	}
	var accountID int64
	if err := tx.QueryRowContext(ctx, `SELECT account_id FROM login_attempts WHERE id=?`, attemptID).Scan(&accountID); err != nil {
		return AccountDelivery{}, err
	}
	account, err := scanAccount(tx.QueryRowContext(ctx, accountSelect+` WHERE id=?`, accountID))
	if err != nil {
		return AccountDelivery{}, err
	}
	if account.IsOfficialUnavailable() {
		if _, err := tx.ExecContext(ctx, `UPDATE account_deliveries SET state='canceled',last_error=?,manual_action='',next_retry_at=NULL,updated_at=? WHERE account_id=? AND destination_key=? AND state NOT IN ('completed','canceled')`, "账号已被官方删除或停用", time.Now().UnixMilli(), accountID, destination); err != nil {
			return AccountDelivery{}, err
		}
		if err := tx.Commit(); err != nil {
			return AccountDelivery{}, err
		}
		return AccountDelivery{AccountID: accountID, DestinationKey: destination, CredentialVersion: attemptID, State: "canceled", LastError: "账号已被官方删除或停用"}, nil
	}
	if bindingChanged || !account.AllowsSub2() || !IsSub2PaidPlan(result.PlanType) {
		message := "当前套餐未进入 Sub2 付费池"
		if account.IsFreeLocal() {
			message = "当前账号仅保留在 AUTH 本地"
		} else if bindingChanged {
			message = "登录期间 Sub2 绑定已变化，旧交付已取消"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE account_deliveries SET state='canceled',last_error=?,manual_action='',next_retry_at=NULL,updated_at=? WHERE account_id=? AND destination_key=? AND state NOT IN ('completed','canceled')`, message, time.Now().UnixMilli(), accountID, destination); err != nil {
			return AccountDelivery{}, err
		}
		if err := tx.Commit(); err != nil {
			return AccountDelivery{}, err
		}
		return AccountDelivery{AccountID: accountID, DestinationKey: destination, CredentialVersion: attemptID, State: "canceled", LastError: message}, nil
	}
	missing, err := sub2AccountMissing(ctx, tx, destination, accountID)
	if err != nil {
		return AccountDelivery{}, err
	}
	if missing {
		// OAuth success is still durable, but never releases the remote tombstone.
		if err := tx.Commit(); err != nil {
			return AccountDelivery{}, err
		}
		return AccountDelivery{AccountID: accountID, DestinationKey: destination, CredentialVersion: attemptID, State: "canceled", LastError: Sub2MissingMessage}, nil
	}
	var bound bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sub2_imports WHERE destination_key=? AND account_id=?) OR EXISTS(SELECT 1 FROM account_deliveries WHERE destination_key=? AND account_id=?)`, destination, accountID, destination, accountID).Scan(&bound); err != nil {
		return AccountDelivery{}, err
	}
	if bindingID == 0 {
		var appeared bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sub2_imports WHERE account_id=? AND destination_key=? AND state='imported')`, accountID, destination).Scan(&appeared); err != nil {
			return AccountDelivery{}, err
		}
		if appeared {
			if err := tx.Commit(); err != nil {
				return AccountDelivery{}, err
			}
			return AccountDelivery{AccountID: accountID, DestinationKey: destination, CredentialVersion: attemptID, State: "canceled", LastError: "登录期间 Sub2 绑定已变化，旧交付已取消"}, nil
		}
	}
	allowCreate = allowCreate && !bound
	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE account_deliveries SET state='canceled',last_error='已有更新的登录结果，旧交付已取消',next_retry_at=NULL,manual_action='',updated_at=? WHERE account_id=? AND destination_key=? AND recovery_task_id=0 AND state NOT IN ('completed','canceled')`, now, accountID, destination); err != nil {
		return AccountDelivery{}, err
	}
	insert, err := tx.ExecContext(ctx, `INSERT INTO account_deliveries(account_id,credential_version,destination_key,state,created_at,updated_at,options_json,allow_create,import_operation_id,original_binding_id,original_remote_id,original_binding_version) VALUES(?,?,?,'queued',?,?,?,?,?,?,?,?)`, accountID, attemptID, destination, now, now, string(raw), boolInt(allowCreate), operationID, bindingID, remoteID, bindingVersion)
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

// An empty destination retains the latest delivery across all destinations for
// history viewing when Sub2 is not configured.
func (s *Store) ListLatestAccountDeliveries(ctx context.Context, destinations ...string) ([]AccountDelivery, error) {
	destination := deliveryDestination(destinations)
	return s.listAccountDeliveries(ctx, accountDeliverySelect+` WHERE id IN (SELECT MAX(id) FROM account_deliveries WHERE (?='' OR destination_key=?) GROUP BY account_id) ORDER BY id DESC`, destination, destination)
}

func (s *Store) ListDueAccountDeliveries(ctx context.Context, now time.Time, limit int, destinations ...string) ([]AccountDelivery, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	destination := deliveryDestination(destinations)
	return s.listAccountDeliveries(ctx, accountDeliverySelect+` WHERE id IN (SELECT MAX(id) FROM account_deliveries WHERE (?='' OR destination_key=?) GROUP BY account_id) AND recovery_task_id=0 AND state IN ('queued','checking','importing','retry_wait') AND manual_action='' AND (next_retry_at IS NULL OR next_retry_at<=?) ORDER BY COALESCE(next_retry_at,created_at),id LIMIT ?`, destination, destination, now.UnixMilli(), limit)
}

func deliveryDestination(destinations []string) string {
	if len(destinations) == 0 {
		return ""
	}
	return strings.TrimSpace(destinations[0])
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
	return s.updateAccountDelivery(ctx, id, nil, state, message, manualAction, nextRetryAt, recoveryTaskID)
}

func (s *Store) UpdateAccountDeliveryForOperation(ctx context.Context, task AccountDelivery, state, message, manualAction string, nextRetryAt *time.Time, recoveryTaskID int64) (AccountDelivery, error) {
	return s.updateAccountDelivery(ctx, task.ID, &task.ImportOperationID, state, message, manualAction, nextRetryAt, recoveryTaskID)
}

func (s *Store) updateAccountDelivery(ctx context.Context, id int64, operation *string, state, message, manualAction string, nextRetryAt *time.Time, recoveryTaskID int64) (AccountDelivery, error) {
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
	if operation != nil && delivery.ImportOperationID != *operation {
		return delivery, ErrAccountDeliverySettled
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
