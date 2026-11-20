package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const Sub2RemoteMissing = "remote_missing"
const Sub2MissingMessage = "Sub2 账号已删除或关联不存在，自动恢复已停止"

var ErrSub2AccountMissing = errors.New(Sub2MissingMessage)

// Sub2AccountMissing reads the binding first; an explicit new operation
// supersedes old lifecycle evidence. Historical lookup is never UI-truncated.
func (s *Store) Sub2AccountMissing(ctx context.Context, destination string, accountID int64) (bool, error) {
	return sub2AccountMissing(ctx, s.db, destination, accountID)
}

type sub2Queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func sub2AccountMissing(ctx context.Context, q sub2Queryer, destination string, accountID int64) (bool, error) {
	var state string
	err := q.QueryRowContext(ctx, "SELECT state FROM sub2_imports WHERE destination_key=? AND account_id=?", destination, accountID).Scan(&state)
	if err == nil {
		return state == Sub2RemoteMissing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var missing bool
	prefix := "sub2:" + destination + ":"
	err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_lifecycle_events
 WHERE account_id=? AND source='sub2' AND status='deleted' AND code='not_found' AND http_status=404
 AND substr(dedupe_key,1,?)=? AND substr(dedupe_key,-10)=':not_found')`, accountID, len(prefix), prefix).Scan(&missing)
	return missing, err
}

// MarkSub2AccountMissing atomically retires only work owned by this exact
// destination/binding. Mutating callers hold the account lease. The operation fence
// makes late 404s harmless after an explicit rebind.
func (s *Store) MarkSub2AccountMissing(ctx context.Context, binding Sub2Import) error {
	return s.markSub2AccountMissing(ctx, binding, false)
}

func (s *Store) MarkRetiredSub2AccountMissing(ctx context.Context, binding Sub2Import) error {
	return s.markSub2AccountMissing(ctx, binding, true)
}

func (s *Store) markSub2AccountMissing(ctx context.Context, binding Sub2Import, retired bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := `UPDATE sub2_imports SET state='remote_missing',payload_cipher=X'',last_error=?,updated_at=?
 WHERE id=? AND destination_key=? AND account_id=? AND sub2_account_id=? AND operation_id=?
 AND state IN ('imported','cleanup_pending','remote_missing')`
	args := []any{Sub2MissingMessage, time.Now().Unix(), binding.ID, binding.DestinationKey, binding.AccountID, binding.Sub2AccountID, binding.OperationID}
	if retired {
		query += " AND credential_version=? AND state=?"
		args = append(args, binding.CredentialVersion, binding.State)
	}
	result, err := tx.ExecContext(ctx, query, args...)
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
	var email string
	if err := tx.QueryRowContext(ctx, "SELECT email FROM accounts WHERE id=?", binding.AccountID).Scan(&email); err != nil {
		return err
	}
	if err := recordLifecycleEventTx(ctx, tx, LifecycleEvent{
		AccountID: binding.AccountID, Email: email, Source: "sub2", Status: "deleted",
		Reason: Sub2MissingMessage, Code: "not_found", HTTPStatus: 404,
		DedupeKey: fmt.Sprintf("sub2:%s:%d:not_found", binding.DestinationKey, binding.Sub2AccountID),
	}); err != nil {
		return err
	}
	if err := retireSub2WorkTx(ctx, tx, binding); err != nil {
		return err
	}
	return tx.Commit()
}

func retireSub2WorkTx(ctx context.Context, tx *sql.Tx, binding Sub2Import) error {
	now := time.Now().UnixMilli()
	var soleBinding bool
	if err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM sub2_imports WHERE account_id=? AND destination_key<>? AND state='imported')`, binding.AccountID, binding.DestinationKey).Scan(&soleBinding); err != nil {
		return err
	}
	// Settle individual checks without stopping unrelated accounts in a batch.
	if _, err := tx.ExecContext(ctx, `UPDATE account_checks SET state=CASE WHEN state='running' AND request_attempted=0 THEN 'skipped' ELSE 'canceled' END,
 skip_reason='sub2_account_missing',finished_at=?,error_code='sub2_account_missing',error_message=?
 WHERE account_id=? AND state IN ('queued','running') AND batch_id IN (SELECT id FROM account_check_batches WHERE destination_key=? OR (destination_key='' AND ?))`,
		now, Sub2MissingMessage, binding.AccountID, binding.DestinationKey, soleBinding); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_check_batches SET state=CASE WHEN state='stopping' THEN 'stopped' ELSE 'completed' END,finished_at=?,proxy_cipher=NULL
 WHERE state IN ('active','stopping') AND NOT EXISTS(SELECT 1 FROM account_checks c WHERE c.batch_id=account_check_batches.id AND c.state IN ('queued','running'))`, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_deliveries SET state='canceled',last_error=?,manual_action='',next_retry_at=NULL,updated_at=?
 WHERE account_id=? AND destination_key=? AND state NOT IN ('completed','canceled')`, Sub2MissingMessage, now, binding.AccountID, binding.DestinationKey); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_recovery_rechecks SET state='canceled',last_error=?,manual_action='',next_check_at=NULL,updated_at=?
 WHERE state NOT IN ('passed','canceled') AND task_id IN (SELECT id FROM account_recovery_tasks WHERE account_id=? AND (destination_key=? OR (destination_key='' AND sub2_account_id=? AND ?)))`,
		Sub2MissingMessage, now, binding.AccountID, binding.DestinationKey, binding.Sub2AccountID, soleBinding); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_recovery_tasks SET state='canceled',error_code='sub2_account_missing',last_error=?,retry_action='manual',manual_action=?,next_retry_at=NULL,updated_at=?
 WHERE account_id=? AND (destination_key=? OR (destination_key='' AND sub2_account_id=? AND ?)) AND state NOT IN ('completed','canceled')`, Sub2MissingMessage, Sub2MissingMessage, now, binding.AccountID, binding.DestinationKey, binding.Sub2AccountID, soleBinding); err != nil {
		return err
	}
	// Repair is account-scoped; do not cancel another destination's work.
	if _, err := tx.ExecContext(ctx, `UPDATE account_credential_repairs SET state='canceled',last_error=?,manual_action='',next_retry_at=NULL,updated_at=?
 WHERE account_id=? AND state IN ('queued','checking','retry_wait','recovery_queued') AND NOT EXISTS
 (SELECT 1 FROM sub2_imports WHERE account_id=? AND destination_key<>? AND state='imported')`,
		Sub2MissingMessage, now, binding.AccountID, binding.AccountID, binding.DestinationKey); err != nil {
		return err
	}
	return nil
}

// AuthorizeRemoteMissingReimport replaces the operation only for an explicit
// reimport request, and retires old work in the same transaction. A normal
// retry, credential save, or successful login must never call this method.
func (s *Store) AuthorizeRemoteMissingReimport(ctx context.Context, binding Sub2Import, version int64, operation, key string, payload []byte, options DeliveryOptions) (Sub2Import, error) {
	if binding.State != Sub2RemoteMissing || version <= 0 || strings.TrimSpace(operation) == "" || operation == binding.OperationID || strings.TrimSpace(key) == "" || len(payload) == 0 {
		return Sub2Import{}, ErrSub2AccountMissing
	}
	if err := options.ValidateForImport(); err != nil {
		return Sub2Import{}, err
	}
	rawOptions, err := json.Marshal(options)
	if err != nil {
		return Sub2Import{}, err
	}
	cipher, err := s.seal(string(payload))
	if err != nil {
		return Sub2Import{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Sub2Import{}, err
	}
	defer tx.Rollback()
	if err := ensureSub2EligibleTx(ctx, tx, binding.AccountID); err != nil {
		return Sub2Import{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE sub2_imports SET state='queued',credential_version=?,operation_id=?,idempotency_key=?,payload_cipher=?,sub2_account_id=NULL,create_acknowledged=0,last_error='',updated_at=?
 WHERE id=? AND destination_key=? AND account_id=? AND sub2_account_id=? AND operation_id=? AND state='remote_missing'
 AND EXISTS(SELECT 1 FROM accounts a WHERE a.id=? AND a.status='active')
 AND ?=COALESCE((SELECT MAX(id) FROM login_attempts WHERE account_id=? AND status='success'),0)`,
		version, operation, key, cipher, time.Now().Unix(), binding.ID, binding.DestinationKey, binding.AccountID, binding.Sub2AccountID, binding.OperationID, binding.AccountID, version, binding.AccountID)
	if err != nil {
		return Sub2Import{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return Sub2Import{}, err
	}
	if n != 1 {
		return Sub2Import{}, ErrAccountRecoveryStale
	}
	if err := retireSub2WorkTx(ctx, tx, binding); err != nil {
		return Sub2Import{}, err
	}
	// Detach only the retired handoff of the intent being replaced. Its
	// recovery record and diagnostic history stay intact, while the existing
	// unique delivery_id checkpoint can belong to the new operation.
	if _, err := tx.ExecContext(ctx, `UPDATE account_recovery_tasks SET delivery_id=0 WHERE state IN ('completed','canceled') AND delivery_id IN
 (SELECT id FROM account_deliveries WHERE account_id=? AND destination_key=? AND credential_version=?)`, binding.AccountID, binding.DestinationKey, version); err != nil {
		return Sub2Import{}, err
	}
	// Replacing the intent is explicit and operation-fenced; older workers and
	// their canceled recovery tasks cannot mutate or reattach to this intent.
	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_deliveries(account_id,credential_version,destination_key,state,created_at,updated_at,options_json,allow_create,import_operation_id)
 VALUES(?,?,?,'queued',?,?,?,1,?) ON CONFLICT(destination_key,account_id,credential_version) DO UPDATE SET
 state='queued',recovery_task_id=0,last_error='',next_retry_at=NULL,manual_action='',retry_count=0,updated_at=excluded.updated_at,
 options_json=excluded.options_json,allow_create=1,import_operation_id=excluded.import_operation_id,original_binding_id=0,original_remote_id=0,original_binding_version=0,bound_handoff_state=''`, binding.AccountID, version, binding.DestinationKey, now, now, string(rawOptions), operation); err != nil {
		return Sub2Import{}, err
	}
	if err := tx.Commit(); err != nil {
		return Sub2Import{}, err
	}
	return s.GetSub2ImportByID(ctx, binding.ID)
}

// Official deletion clears imported OAuth secrets while retaining only the
// display name in the existing encrypted payload and keeping the tombstone.
func (s *Store) clearMissingImportSecretsTx(ctx context.Context, tx *sql.Tx, accountID int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,payload_cipher FROM sub2_imports WHERE account_id=? AND state='remote_missing'`, accountID)
	if err != nil {
		return err
	}
	type snapshot struct {
		id     int64
		cipher []byte
	}
	var saved []snapshot
	for rows.Next() {
		var item snapshot
		if err := rows.Scan(&item.id, &item.cipher); err != nil {
			rows.Close()
			return err
		}
		saved = append(saved, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range saved {
		var safe struct {
			Data struct {
				Accounts []struct {
					Name string `json:"name"`
				} `json:"accounts"`
			} `json:"data"`
		}
		if plain, err := s.open(item.cipher); err == nil {
			_ = json.Unmarshal([]byte(plain), &safe)
		}
		raw, err := json.Marshal(safe)
		if err != nil {
			return err
		}
		cipher, err := s.seal(string(raw))
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE sub2_imports SET payload_cipher=? WHERE id=? AND state='remote_missing'`, cipher, item.id); err != nil {
			return err
		}
	}
	return nil
}
