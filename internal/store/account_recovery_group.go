package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// RecoveryGroupDecision is the operator's explicit choice after a delivery
// discovers a different group assignment on the existing Sub2 account.
// Pending decisions reserve the task while the remote account is rechecked;
// only confirmed decisions allow the recovery worker to resume.
type RecoveryGroupDecision struct {
	TaskID          int64     `json:"task_id"`
	Action          string    `json:"action"`
	State           string    `json:"state"`
	CurrentGroupIDs []int64   `json:"current_group_ids"`
	TargetGroupIDs  []int64   `json:"target_group_ids"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func migrateAccountRecoveryGroupDecisions(s *Store) error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS account_recovery_group_decisions (
 task_id INTEGER PRIMARY KEY REFERENCES account_recovery_tasks(id) ON DELETE CASCADE,
 action TEXT NOT NULL CHECK(action IN ('keep_current','use_target')),
 state TEXT NOT NULL CHECK(state IN ('pending','confirmed')),
 current_group_ids_json TEXT NOT NULL,
 target_group_ids_json TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS account_recovery_group_decisions_state ON account_recovery_group_decisions(state,updated_at);`)
	if err != nil {
		return fmt.Errorf("migrate recovery group decisions: %w", err)
	}
	return nil
}

func normalizeRecoveryGroups(ids []int64) []int64 {
	result := append([]int64(nil), ids...)
	slices.Sort(result)
	result = slices.Compact(result)
	return result
}

func marshalRecoveryGroups(ids []int64) (string, error) {
	raw, err := json.Marshal(normalizeRecoveryGroups(ids))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func unmarshalRecoveryGroups(raw string) ([]int64, error) {
	var ids []int64
	if strings.TrimSpace(raw) == "" {
		return []int64{}, nil
	}
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, err
	}
	return normalizeRecoveryGroups(ids), nil
}

func scanRecoveryGroupDecision(row scanner) (RecoveryGroupDecision, error) {
	var result RecoveryGroupDecision
	var currentRaw, targetRaw string
	var created, updated int64
	if err := row.Scan(&result.TaskID, &result.Action, &result.State, &currentRaw, &targetRaw, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RecoveryGroupDecision{}, ErrAccountRecoveryNotFound
		}
		return RecoveryGroupDecision{}, err
	}
	var err error
	if result.CurrentGroupIDs, err = unmarshalRecoveryGroups(currentRaw); err != nil {
		return RecoveryGroupDecision{}, err
	}
	if result.TargetGroupIDs, err = unmarshalRecoveryGroups(targetRaw); err != nil {
		return RecoveryGroupDecision{}, err
	}
	result.CreatedAt, result.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
	return result, nil
}

func (s *Store) GetAccountRecoveryGroupDecision(ctx context.Context, taskID int64) (RecoveryGroupDecision, error) {
	if taskID <= 0 {
		return RecoveryGroupDecision{}, ErrAccountRecoveryNotFound
	}
	return scanRecoveryGroupDecision(s.db.QueryRowContext(ctx, `SELECT task_id,action,state,current_group_ids_json,target_group_ids_json,created_at,updated_at FROM account_recovery_group_decisions WHERE task_id=?`, taskID))
}

// BeginAccountRecoveryGroupDecision reserves one explicit action after the
// task and its diagnostic snapshot have been checked. It never changes the
// remote account and can therefore be safely repeated after a lost response.
func (s *Store) BeginAccountRecoveryGroupDecision(ctx context.Context, taskID int64, action string, current, target []int64, expectedUpdatedAt int64) (RecoveryGroupDecision, error) {
	if action == "apply_target" {
		action = "use_target"
	}
	if action != "keep_current" && action != "use_target" {
		return RecoveryGroupDecision{}, errors.New("invalid recovery group action")
	}
	currentJSON, err := marshalRecoveryGroups(current)
	if err != nil {
		return RecoveryGroupDecision{}, err
	}
	targetJSON, err := marshalRecoveryGroups(target)
	if err != nil {
		return RecoveryGroupDecision{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RecoveryGroupDecision{}, err
	}
	defer tx.Rollback()
	task, err := scanAccountRecoveryTask(tx.QueryRowContext(ctx, accountRecoverySelect+` WHERE t.id=?`, taskID))
	if err != nil {
		return RecoveryGroupDecision{}, err
	}
	if task.State != RecoveryFailed && task.State != RecoveryUnknown || task.ErrorCode != "group_conflict" || task.Diagnostic == nil {
		return RecoveryGroupDecision{}, errors.New("recovery group conflict is no longer actionable")
	}
	if expectedUpdatedAt > 0 && task.UpdatedAt.UnixMilli() != expectedUpdatedAt {
		return RecoveryGroupDecision{}, ErrAccountRecoveryStale
	}
	if !slices.Equal(normalizeRecoveryGroups(task.Diagnostic.CurrentGroupIDs), normalizeRecoveryGroups(current)) ||
		!slices.Equal(normalizeRecoveryGroups(task.Diagnostic.TargetGroupIDs), normalizeRecoveryGroups(target)) {
		return RecoveryGroupDecision{}, ErrAccountRecoveryStale
	}
	now := time.Now().UnixMilli()
	_, err = tx.ExecContext(ctx, `INSERT INTO account_recovery_group_decisions(task_id,action,state,current_group_ids_json,target_group_ids_json,created_at,updated_at) VALUES(?,?, 'pending',?,?,?,?) ON CONFLICT(task_id) DO UPDATE SET action=excluded.action,state=CASE WHEN account_recovery_group_decisions.action=excluded.action AND account_recovery_group_decisions.current_group_ids_json=excluded.current_group_ids_json AND account_recovery_group_decisions.target_group_ids_json=excluded.target_group_ids_json THEN account_recovery_group_decisions.state ELSE 'pending' END,current_group_ids_json=excluded.current_group_ids_json,target_group_ids_json=excluded.target_group_ids_json,updated_at=excluded.updated_at`, taskID, action, currentJSON, targetJSON, now, now)
	if err != nil {
		return RecoveryGroupDecision{}, err
	}
	if err = tx.Commit(); err != nil {
		return RecoveryGroupDecision{}, err
	}
	return s.GetAccountRecoveryGroupDecision(ctx, taskID)
}

// ConfirmAccountRecoveryGroupDecision atomically records the verified choice
// and requeues the saved recovery checkpoint. The task's current diagnostic
// and timestamp form a CAS fence against a newer failure or operator retry.
func (s *Store) ConfirmAccountRecoveryGroupDecision(ctx context.Context, taskID int64, action string, current, target []int64, expectedUpdatedAt int64) (AccountRecoveryTask, error) {
	if action == "apply_target" {
		action = "use_target"
	}
	currentJSON, err := marshalRecoveryGroups(current)
	if err != nil {
		return AccountRecoveryTask{}, err
	}
	targetJSON, err := marshalRecoveryGroups(target)
	if err != nil {
		return AccountRecoveryTask{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountRecoveryTask{}, err
	}
	defer tx.Rollback()
	task, err := scanAccountRecoveryTask(tx.QueryRowContext(ctx, accountRecoverySelect+` WHERE t.id=?`, taskID))
	if err != nil {
		return AccountRecoveryTask{}, err
	}
	if task.State != RecoveryFailed && task.State != RecoveryUnknown || task.ErrorCode != "group_conflict" || task.Diagnostic == nil {
		return AccountRecoveryTask{}, errors.New("recovery group conflict is no longer actionable")
	}
	if expectedUpdatedAt > 0 && task.UpdatedAt.UnixMilli() != expectedUpdatedAt {
		return AccountRecoveryTask{}, ErrAccountRecoveryStale
	}
	if !slices.Equal(normalizeRecoveryGroups(task.Diagnostic.CurrentGroupIDs), normalizeRecoveryGroups(current)) ||
		!slices.Equal(normalizeRecoveryGroups(task.Diagnostic.TargetGroupIDs), normalizeRecoveryGroups(target)) {
		return AccountRecoveryTask{}, ErrAccountRecoveryStale
	}
	var savedAction, state, savedCurrent, savedTarget string
	if err := tx.QueryRowContext(ctx, `SELECT action,state,current_group_ids_json,target_group_ids_json FROM account_recovery_group_decisions WHERE task_id=?`, taskID).Scan(&savedAction, &state, &savedCurrent, &savedTarget); err != nil {
		return AccountRecoveryTask{}, err
	}
	if savedAction != action || state != "pending" && state != "confirmed" || savedCurrent != currentJSON || savedTarget != targetJSON {
		return AccountRecoveryTask{}, ErrAccountRecoveryStale
	}
	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE account_recovery_group_decisions SET state='confirmed',updated_at=? WHERE task_id=?`, now, taskID); err != nil {
		return AccountRecoveryTask{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_recovery_tasks SET state='queued',last_error='',retry_action='resume',next_retry_at=NULL,manual_action='',updated_at=? WHERE id=? AND state IN ('failed','unknown')`, now, taskID); err != nil {
		return AccountRecoveryTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return AccountRecoveryTask{}, err
	}
	return s.GetAccountRecoveryTaskByID(ctx, taskID)
}
