package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type DeliveryOptions struct {
	GroupIDs    []int64 `json:"group_ids"`
	Priority    int     `json:"priority"`
	Concurrency int     `json:"concurrency"`
	NamePrefix  string  `json:"name_prefix,omitempty"`
}

func DefaultDeliveryOptions() DeliveryOptions {
	return DeliveryOptions{GroupIDs: []int64{}, Priority: 1, Concurrency: 3}
}
func (o *DeliveryOptions) Validate() error {
	o.NamePrefix = strings.TrimSpace(o.NamePrefix)
	if utf8.RuneCountInString(o.NamePrefix) > 32 || strings.ContainsFunc(o.NamePrefix, unicode.IsControl) {
		return errors.New("账号前缀最多 32 个字符，不能包含控制字符")
	}
	if o.Priority < 0 || int64(o.Priority) > 2147483647 || o.Concurrency < 1 || o.Concurrency > 10000 || len(o.GroupIDs) > 100 {
		return errors.New("优先级应为非负整数，并发应为 1–10000，分组最多 100 个")
	}
	seen := map[int64]bool{}
	ids := make([]int64, 0, len(o.GroupIDs))
	for _, id := range o.GroupIDs {
		if id <= 0 {
			return errors.New("分组 ID 必须为正整数")
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	o.GroupIDs = ids
	return nil
}
func (s *Store) migrateDeliveryOptions() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS sub2_delivery_defaults(destination_key TEXT PRIMARY KEY,options_json TEXT NOT NULL)`)
	if err != nil {
		return err
	}
	rows, err := s.db.Query(`PRAGMA table_info(account_deliveries)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, nn, pk int
		var name, kind string
		var def any
		if err = rows.Scan(&cid, &name, &kind, &nn, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		found = found || name == "options_json"
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !found {
		_, err = s.db.Exec(`ALTER TABLE account_deliveries ADD COLUMN options_json TEXT NOT NULL DEFAULT '{"group_ids":[],"priority":1,"concurrency":3}'`)
	}
	return err
}
func (s *Store) GetDeliveryDefaults(ctx context.Context, destination string) (DeliveryOptions, error) {
	var raw string
	o := DefaultDeliveryOptions()
	err := s.db.QueryRowContext(ctx, `SELECT options_json FROM sub2_delivery_defaults WHERE destination_key=?`, destination).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return o, nil
	}
	if err != nil {
		return o, err
	}
	if err = json.Unmarshal([]byte(raw), &o); err != nil {
		return o, err
	}
	err = o.Validate()
	return o, err
}
func (s *Store) SaveDeliveryDefaults(ctx context.Context, destination string, o DeliveryOptions) error {
	if err := o.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO sub2_delivery_defaults(destination_key,options_json) VALUES(?,?) ON CONFLICT(destination_key) DO UPDATE SET options_json=excluded.options_json`, destination, string(raw))
	return err
}
func (s *Store) WakeAccountDelivery(ctx context.Context, accountID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE account_deliveries SET state='queued',next_retry_at=NULL,manual_action='',last_error='' WHERE account_id=? AND id=(SELECT MAX(id) FROM account_deliveries WHERE account_id=?) AND recovery_task_id=0 AND state IN ('retry_wait','requires_action')`, accountID, accountID)
	return err
}

func (s *Store) HasEarlierCompletedDelivery(ctx context.Context, accountID, deliveryID int64) (bool, error) {
	var found bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_deliveries d JOIN account_recovery_tasks t ON t.id=d.recovery_task_id WHERE d.account_id=? AND d.id<? AND t.state='completed')`, accountID, deliveryID).Scan(&found)
	return found, err
}

// QueueAccountDelivery accepts manual import of an already saved OAuth result.
// Callers retain the per-account lease until the durable intent is recorded.
func (s *Store) QueueAccountDelivery(ctx context.Context, accountID int64, destination string, options DeliveryOptions) (AccountDelivery, error) {
	if err := options.Validate(); err != nil {
		return AccountDelivery{}, err
	}
	version, err := s.GetAccountCredentialVersion(ctx, accountID)
	if err != nil {
		return AccountDelivery{}, err
	}
	if version <= 0 {
		return AccountDelivery{}, errors.New("账号尚无成功登录凭据")
	}
	raw, err := json.Marshal(options)
	if err != nil {
		return AccountDelivery{}, err
	}
	now := time.Now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountDelivery{}, err
	}
	defer tx.Rollback()
	existing, lookupErr := scanAccountDelivery(tx.QueryRowContext(ctx, accountDeliverySelect+` WHERE destination_key=? AND account_id=? AND credential_version=?`, destination, accountID, version))
	if lookupErr == nil {
		if existing.State == "canceled" {
			return existing, ErrAccountDeliverySettled
		}
		if existing.RecoveryTaskID > 0 {
			// The delivery row stays verifying after handoff; its recovery task
			// carries the current action/cancellation state shown in the UI.
			var state, action, manual string
			if err := tx.QueryRowContext(ctx, `SELECT state,retry_action,manual_action FROM account_recovery_tasks WHERE id=?`, existing.RecoveryTaskID).Scan(&state, &action, &manual); err != nil {
				return existing, err
			}
			if state == RecoveryCanceled {
				return existing, ErrAccountDeliverySettled
			}
			if existing.State == "requires_action" || action == "manual" || manual != "" {
				return existing, ErrAccountDeliveryRequiresAction
			}
			return existing, nil
		}
		if existing.State == "requires_action" {
			// A requires_action delivery can be corrected in place only while no
			// import intent exists. Once an import row exists, its payload and
			// idempotency key may already have been sent to Sub2; replacing the
			// delivery options could make a retry claim a different remote intent.
			var importCount int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sub2_imports WHERE destination_key=? AND account_id=?`, destination, accountID).Scan(&importCount); err != nil {
				return AccountDelivery{}, err
			}
			if importCount > 0 {
				return existing, ErrAccountDeliveryRequiresAction
			}
			if _, err := tx.ExecContext(ctx, `UPDATE account_deliveries SET options_json=?,state='queued',last_error='',next_retry_at=NULL,manual_action='',retry_count=0,updated_at=? WHERE id=? AND state='requires_action' AND recovery_task_id=0`, string(raw), now, existing.ID); err != nil {
				return AccountDelivery{}, err
			}
			if err := tx.Commit(); err != nil {
				return AccountDelivery{}, err
			}
			return s.GetAccountDelivery(ctx, existing.ID)
		}
		return existing, nil
	}
	if !errors.Is(lookupErr, ErrAccountDeliveryNotFound) {
		return AccountDelivery{}, lookupErr
	}
	// Keep the unique-key race idempotent: another request may have inserted
	// the same intent after our lookup but before this insert.
	if _, err = tx.ExecContext(ctx, `INSERT INTO account_deliveries(account_id,credential_version,destination_key,state,created_at,updated_at,options_json) VALUES(?,?,?,'queued',?,?,?) ON CONFLICT(destination_key,account_id,credential_version) DO NOTHING`, accountID, version, destination, now, now, string(raw)); err != nil {
		return AccountDelivery{}, err
	}
	if err := tx.Commit(); err != nil {
		return AccountDelivery{}, err
	}
	return scanAccountDelivery(s.db.QueryRowContext(ctx, accountDeliverySelect+` WHERE destination_key=? AND account_id=? AND credential_version=?`, destination, accountID, version))
}
