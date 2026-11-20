package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// convergeDiscardedAttempts applies the retirement policy to evidence saved
// by older releases. It never starts a login or contacts a remote service.
func (s *Store) convergeDiscardedAttempts(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT a.id,a.email,f.id,f.error_code,COALESCE(f.stage,''),f.http_status,f.finished_at
		FROM accounts a JOIN login_attempts f ON f.id=(SELECT MAX(id) FROM login_attempts WHERE account_id=a.id)
		WHERE a.status='failed' AND a.sub2_policy NOT IN ('discarded','official_unavailable')
		AND f.status='failed' AND f.retryable=0 AND f.started_at>0 AND f.finished_at>=f.started_at
		AND a.updated_at=f.finished_at AND a.last_error_code=f.error_code AND a.last_success_at<=f.started_at
		AND NOT EXISTS (SELECT 1 FROM login_attempts ok WHERE ok.account_id=a.id
			AND ok.status='success' AND ok.finished_at>f.started_at)
		AND NOT EXISTS (SELECT 1 FROM account_credential_repairs r WHERE r.account_id=a.id
			AND r.updated_at/1000>=f.started_at AND r.attempt_id<>f.id)
		AND NOT EXISTS (SELECT 1 FROM account_lifecycle_events e WHERE e.dedupe_key='login_attempt:'||f.id)`)
	if err != nil {
		return err
	}
	var events []LifecycleEvent
	for rows.Next() {
		var event LifecycleEvent
		var attempt, finished int64
		var stage string
		if err := rows.Scan(&event.AccountID, &event.Email, &attempt, &event.Code, &stage, &event.HTTPStatus, &finished); err != nil {
			rows.Close()
			return err
		}
		if !isDiscardedLoginFailure(event.Code, stage, event.HTTPStatus) {
			continue
		}
		event.Source, event.Status = "login_discarded", Sub2PolicyDiscarded
		event.DedupeKey = fmt.Sprintf("login_attempt:%d", attempt)
		event.ObservedAt = time.Unix(finished, 0)
		event.Reason = "登录凭据被拒绝，账号已废弃"
		switch strings.ToLower(strings.TrimSpace(event.Code)) {
		case "email_verification_required", "email_verification_needed":
			event.Reason = "登录要求邮箱验证，账号已废弃"
		}
		events = append(events, event)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, event := range events {
		if err := recordLifecycleEventTx(ctx, tx, event); err != nil {
			return err
		}
		// Older releases did not freeze the binding at login. Only reconstruct
		// it when an independent prior recovery identifies the same destination,
		// remote ID and successful version, and the binding predates the attempt.
		if _, err := tx.ExecContext(ctx, `INSERT INTO lifecycle_request_bindings
			(request_key,binding_id,account_id,sub2_id,destination,operation,binding_version,source_version)
			SELECT ?,i.id,i.account_id,i.sub2_account_id,i.destination_key,i.operation_id,i.credential_version,ok.id
			FROM sub2_imports i JOIN login_attempts f ON f.account_id=i.account_id AND 'login_attempt:'||f.id=?
			JOIN login_attempts ok ON ok.id=(SELECT MAX(id) FROM login_attempts WHERE account_id=i.account_id AND status='success')
			WHERE i.account_id=? AND i.state='imported' AND i.create_acknowledged=1 AND i.sub2_account_id>0
			AND i.operation_id<>'' AND i.destination_key<>'' AND i.created_at>0
			AND i.updated_at>=i.created_at AND i.updated_at<f.started_at AND ok.id<f.id
			AND EXISTS (SELECT 1 FROM account_recovery_tasks t WHERE t.account_id=i.account_id
				AND t.destination_key=i.destination_key AND t.sub2_account_id=i.sub2_account_id
				AND t.source_credential_attempt_id=ok.id AND t.created_at>0
				AND t.created_at/1000 BETWEEN i.updated_at AND f.started_at)
			ON CONFLICT(request_key,binding_id) DO NOTHING`, event.DedupeKey, event.DedupeKey, event.AccountID); err != nil {
			return err
		}
		if err := authorizeFrozenCleanupTx(ctx, tx, event.DedupeKey); err != nil {
			return err
		}
		if err := finalizeTerminalUnavailableTx(ctx, tx, event.AccountID, event.Reason, 0, Sub2PolicyDiscarded); err != nil {
			return err
		}
	}
	return tx.Commit()
}
