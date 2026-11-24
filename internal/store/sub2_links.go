package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) HasOfficialSub2CleanupEvidence(ctx context.Context, binding Sub2Import) (bool, error) {
	var authorized bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM sub2_imports i JOIN account_lifecycle_events e ON e.account_id=i.account_id
		WHERE i.id=? AND i.account_id=? AND i.destination_key=? AND i.sub2_account_id=? AND i.operation_id=?
		AND i.credential_version=? AND i.state=? AND i.create_acknowledged=1
		AND i.credential_version=(SELECT MAX(id) FROM login_attempts WHERE account_id=i.account_id AND status='success')
		AND e.source IN ('official','cleanup_authorization') AND (lower(trim(e.code)) IN
		('deleted','account_deleted','user_deleted','deactivated','account_deactivated','user_deactivated','account_disabled','user_disabled','payment_required','subscription_expired','deactivated_workspace','deleted_or_deactivated')
			OR (e.source='cleanup_authorization' AND e.status='deleted_or_deactivated'))
		AND e.dedupe_key='sub2-official:' || i.id || ':' || i.sub2_account_id || ':' || i.credential_version || ':' || i.operation_id
		AND e.observed_at >= (SELECT finished_at FROM login_attempts WHERE id=i.credential_version)
	)`, binding.ID, binding.AccountID, binding.DestinationKey, binding.Sub2AccountID, binding.OperationID, binding.CredentialVersion, binding.State).Scan(&authorized)
	return authorized, err
}

func (s *Store) HasDiscardedSub2CleanupEvidence(ctx context.Context, binding Sub2Import) (bool, error) {
	var code string
	err := s.db.QueryRowContext(ctx, `SELECT e.code FROM sub2_imports i
		JOIN accounts a ON a.id=i.account_id
		JOIN account_lifecycle_events e ON e.account_id=i.account_id
		WHERE i.id=? AND i.account_id=? AND i.destination_key=? AND i.sub2_account_id=?
		AND i.operation_id=? AND i.credential_version=? AND i.state='cleanup_pending' AND i.create_acknowledged=1
		AND a.status='discarded' AND a.sub2_policy='discarded'
		AND i.credential_version=(SELECT MAX(id) FROM login_attempts WHERE account_id=i.account_id AND status='success')
		AND e.source='cleanup_authorization' AND e.status='discarded'
		AND e.dedupe_key='sub2-discarded:'||i.id||':'||i.sub2_account_id||':'||i.credential_version||':'||i.operation_id`,
		binding.ID, binding.AccountID, binding.DestinationKey, binding.Sub2AccountID, binding.OperationID, binding.CredentialVersion).Scan(&code)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && isDiscardedLoginCode(code), err
}

func (s *Store) HasFreeSub2CleanupEvidence(ctx context.Context, binding Sub2Import) (bool, error) {
	var currentPlan, priorPlan string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(la.plan_type,''), COALESCE((SELECT paid.plan_type FROM login_attempts paid
		WHERE paid.account_id=a.id AND paid.status='success' AND paid.id<i.credential_version
		AND trim(COALESCE(paid.plan_type,''))<>'' ORDER BY paid.id DESC LIMIT 1),'')
		FROM accounts a
		JOIN sub2_imports i ON i.account_id=a.id
		JOIN login_attempts la ON la.account_id=a.id AND la.id=i.credential_version
		WHERE i.id=? AND i.account_id=? AND i.destination_key=? AND i.sub2_account_id=?
		AND i.operation_id=? AND i.credential_version=? AND i.state='cleanup_pending'
		AND a.sub2_policy='free_local' AND i.create_acknowledged=1 AND la.status='success'
		AND NOT EXISTS(SELECT 1 FROM login_attempts newer WHERE newer.account_id=a.id AND newer.status='success'
			AND newer.id>la.id AND lower(trim(COALESCE(newer.plan_type,''))) NOT IN ('','free'))`,
		binding.ID, binding.AccountID, binding.DestinationKey, binding.Sub2AccountID, binding.OperationID, binding.CredentialVersion).Scan(&currentPlan, &priorPlan)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && isFreePlan(currentPlan) && isR1PaidPlan(priorPlan), err
}

func (s *Store) Sub2CleanupWorkspace(ctx context.Context, binding Sub2Import, free bool) (string, error) {
	var workspace string
	query := `SELECT COALESCE(chatgpt_account_id,'') FROM login_attempts WHERE account_id=? AND status='success' AND id=?`
	if free {
		query = `SELECT COALESCE(chatgpt_account_id,'') FROM login_attempts WHERE account_id=? AND status='success'
			AND id<? AND trim(COALESCE(plan_type,''))<>'' ORDER BY id DESC LIMIT 1`
	}
	err := s.db.QueryRowContext(ctx, query, binding.AccountID, binding.CredentialVersion).Scan(&workspace)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return workspace, err
}

func (binding Sub2Import) OfficialCleanupEvidenceKey(version int64) string {
	return fmt.Sprintf("sub2-official:%d:%d:%d:%s", binding.ID, binding.Sub2AccountID, version, binding.OperationID)
}

func (s *Store) DeferFrozenSub2Cleanup(ctx context.Context, binding Sub2Import, message string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sub2_imports SET last_error=?,updated_at=?
		WHERE id=? AND destination_key=? AND account_id=? AND sub2_account_id=? AND operation_id=?
		AND credential_version=? AND state='cleanup_pending'`,
		message, time.Now().Unix(), binding.ID, binding.DestinationKey, binding.AccountID, binding.Sub2AccountID, binding.OperationID, binding.CredentialVersion)
	return err
}

func (s *Store) DeleteFrozenSub2ImportBinding(ctx context.Context, binding Sub2Import) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM sub2_imports WHERE id=? AND destination_key=? AND account_id=?
		AND sub2_account_id=? AND operation_id=? AND credential_version=? AND state=?`,
		binding.ID, binding.DestinationKey, binding.AccountID, binding.Sub2AccountID, binding.OperationID, binding.CredentialVersion, binding.State)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return ErrAccountRecoveryStale
	}
	return err
}

// LinkSub2Account records a verified existing remote account in a terminal
// state. It must never enter the import queue or replace an in-flight import.
func (s *Store) LinkSub2Account(ctx context.Context, destinationKey string, accountID, sub2AccountID int64, operationID string) (Sub2Import, error) {
	if strings.TrimSpace(destinationKey) == "" || accountID <= 0 || sub2AccountID <= 0 || !strings.HasPrefix(operationID, "linked-") {
		return Sub2Import{}, errors.New("invalid sub2 account binding")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Sub2Import{}, err
	}
	defer tx.Rollback()
	var account Account
	if err := tx.QueryRowContext(ctx, `SELECT status,sub2_policy FROM accounts WHERE id=?`, accountID).Scan(&account.Status, &account.Sub2Policy); errors.Is(err, sql.ErrNoRows) {
		return Sub2Import{}, ErrAccountNotFound
	} else if err != nil {
		return Sub2Import{}, err
	}
	if err := account.terminalError(); err != nil {
		return Sub2Import{}, err
	}
	missing, err := sub2AccountMissing(ctx, tx, destinationKey, accountID)
	if err != nil {
		return Sub2Import{}, err
	}
	if missing {
		return Sub2Import{}, ErrSub2AccountMissing
	}
	payload, err := s.seal(`{}`)
	if err != nil {
		return Sub2Import{}, err
	}
	now := time.Now().Unix()
	_, err = tx.ExecContext(ctx, `INSERT INTO sub2_imports(destination_key,account_id,credential_version,operation_id,idempotency_key,payload_cipher,state,create_acknowledged,sub2_account_id,created_at,updated_at)
		SELECT ?,id,COALESCE((SELECT MAX(la.id) FROM login_attempts la WHERE la.account_id=accounts.id AND la.status='success'),0),?,?,?,'imported',1,?,?,? FROM accounts WHERE id=?
		ON CONFLICT(destination_key,account_id) DO NOTHING`, destinationKey, operationID, operationID, payload, sub2AccountID, now, now, accountID)
	if err != nil {
		return Sub2Import{}, fmt.Errorf("link sub2 account: %w", err)
	}
	item, err := scanSub2Import(tx.QueryRowContext(ctx, sub2ImportSelect+` WHERE i.destination_key=? AND i.account_id=?`, destinationKey, accountID))
	if err != nil {
		return Sub2Import{}, err
	}
	return item, tx.Commit()
}

// DeleteSub2ImportBinding removes a confirmed remote binding after the remote
// account has been deleted. The identity conditions keep a concurrent rebind
// from being removed accidentally.
func (s *Store) DeleteSub2ImportBinding(ctx context.Context, destinationKey string, accountID, sub2AccountID int64, operationID string) error {
	if strings.TrimSpace(destinationKey) == "" || accountID <= 0 || sub2AccountID <= 0 {
		return errors.New("invalid sub2 account binding")
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM sub2_imports WHERE destination_key=? AND account_id=? AND sub2_account_id=? AND operation_id=? AND state IN ('imported','cleanup_pending')", destinationKey, accountID, sub2AccountID, operationID)
	return err
}
