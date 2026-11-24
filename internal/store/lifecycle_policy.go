package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/accountstate"
)

const (
	Sub2PolicyDiscarded           = "discarded"
	Sub2PolicyEligible            = "eligible"
	Sub2PolicyFreeLocal           = "free_local"
	Sub2PolicyOfficialUnavailable = "official_unavailable"
	Sub2PolicyUnknown             = "unknown"
)

var (
	ErrAccountDiscarded           = errors.New("account is discarded")
	ErrAccountOfficialUnavailable = errors.New("account is officially unavailable")
	ErrSub2PlanIneligible         = errors.New("account is not eligible for Sub2")
)

func normalizeSub2Policy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case Sub2PolicyEligible, Sub2PolicyFreeLocal, Sub2PolicyOfficialUnavailable, Sub2PolicyDiscarded:
		return strings.ToLower(strings.TrimSpace(policy))
	default:
		return Sub2PolicyUnknown
	}
}

func (a Account) IsOfficialUnavailable() bool {
	return normalizeSub2Policy(a.Sub2Policy) == Sub2PolicyOfficialUnavailable || isOfficialLifecycleStatus(a.Status)
}

func IsTerminalLifecycleStatus(status string) bool {
	return isOfficialLifecycleStatus(status) || strings.EqualFold(strings.TrimSpace(status), Sub2PolicyDiscarded)
}

func (a Account) IsTerminalUnavailable() bool {
	return a.terminalError() != nil
}

func (a Account) terminalError() error {
	if a.IsOfficialUnavailable() {
		return ErrAccountOfficialUnavailable
	}
	if normalizeSub2Policy(a.Sub2Policy) == Sub2PolicyDiscarded || IsTerminalLifecycleStatus(a.Status) {
		return ErrAccountDiscarded
	}
	return nil
}

func (a Account) IsFreeLocal() bool {
	return normalizeSub2Policy(a.Sub2Policy) == Sub2PolicyFreeLocal
}

func (a Account) AllowsSub2() bool {
	return !a.IsTerminalUnavailable() && normalizeSub2Policy(a.Sub2Policy) == Sub2PolicyEligible
}

func (a Account) Sub2EligibilityError() error {
	if err := a.terminalError(); err != nil {
		return err
	}
	if !a.AllowsSub2() {
		return ErrSub2PlanIneligible
	}
	return nil
}

func IsOfficialLifecycleStatus(status string) bool {
	return isOfficialLifecycleStatus(status)
}

func lifecycleStatusFromFailure(status, code string) string {
	if isOfficialLifecycleStatus(status) {
		return strings.ToLower(strings.TrimSpace(status))
	}
	if isOfficialLifecycleStatus(code) {
		return strings.ToLower(strings.TrimSpace(code))
	}
	return accountstate.OfficialStatus(code)
}

func normalizePlanType(plan string) string {
	return strings.ToLower(strings.TrimSpace(plan))
}

func isDiscardedLoginCode(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "password_rejected", "invalid_password", "incorrect_password", "wrong_password",
		"credentials_rejected", "invalid_credentials", "incorrect_credentials", "invalid_username_or_password",
		"email_verification_required", "email_verification_needed":
		return true
	}
	return false
}

func isDiscardedLoginFailure(code, stage string, httpStatus int) bool {
	// A transient response or a proxy challenge cannot authorize retirement.
	if httpStatus == 407 || httpStatus == 408 || httpStatus == 425 || httpStatus == 429 || httpStatus >= 500 {
		return false
	}
	stage = strings.ToLower(strings.TrimSpace(stage))
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "email_verification_required", "email_verification_needed":
		return stage == "email"
	case "credentials_rejected", "invalid_credentials", "incorrect_credentials", "invalid_username_or_password":
		return stage == "password" || stage == "email"
	default:
		return stage == "password" && isDiscardedLoginCode(code)
	}
}

func isFreePlan(plan string) bool {
	return normalizePlanType(plan) == "free"
}

// R1 removal evidence is deliberately narrower than the general paid-plan
// eligibility list. A plain pro observation never authorizes paid-to-free
// remote cleanup.
func isR1PaidPlan(plan string) bool {
	switch normalizePlanType(plan) {
	case "plus", "team", "business", "self_serve_business", "self_serve_business_prolite", "business_pro", "business_prolite":
		return true
	default:
		return false
	}
}

func isSub2PaidPlan(plan string) bool {
	if isR1PaidPlan(plan) {
		return true
	}
	switch normalizePlanType(plan) {
	case "pro", "enterprise":
		return true
	default:
		return false
	}
}

// IsSub2PaidPlan reports whether the current OAuth observation is a supported
// paid plan that may be written to the Sub2 pool.
func IsSub2PaidPlan(plan string) bool {
	return isSub2PaidPlan(plan)
}

func IsFreePlan(plan string) bool {
	return isFreePlan(plan)
}

func hasHistoricalR1PaidObservationTx(ctx context.Context, tx *sql.Tx, accountID, beforeAttemptID int64) (bool, error) {
	query := `SELECT lower(trim(COALESCE(plan_type,''))) FROM login_attempts WHERE account_id=? AND status='success' AND trim(COALESCE(plan_type,''))<>''`
	args := []any{accountID}
	if beforeAttemptID > 0 {
		query += ` AND id<?`
		args = append(args, beforeAttemptID)
	}
	query += ` ORDER BY id DESC LIMIT 1`
	var plan string
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&plan); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return isR1PaidPlan(plan), nil
}

func nextSub2Policy(plan, current string) string {
	if strings.TrimSpace(plan) == "" {
		if normalizeSub2Policy(current) == Sub2PolicyFreeLocal {
			return Sub2PolicyFreeLocal
		}
		return Sub2PolicyUnknown
	}
	if isSub2PaidPlan(plan) {
		return Sub2PolicyEligible
	}
	if isFreePlan(plan) || normalizeSub2Policy(current) == Sub2PolicyFreeLocal {
		return Sub2PolicyFreeLocal
	}
	return Sub2PolicyUnknown
}

func cancelSub2ForFreeTx(ctx context.Context, tx *sql.Tx, accountID, credentialVersion int64, request string, plan string) error {
	const message = "当前账号仅保留在 AUTH 本地"
	now := time.Now()
	if _, err := tx.ExecContext(ctx, `UPDATE account_recovery_tasks SET state='canceled',last_error=?,retry_action='',manual_action='',next_retry_at=NULL,updated_at=? WHERE account_id=? AND state NOT IN ('completed','canceled')`, message, now.UnixMilli(), accountID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_recovery_rechecks SET state='canceled',last_error=?,manual_action='',next_check_at=NULL,updated_at=? WHERE task_id IN (SELECT id FROM account_recovery_tasks WHERE account_id=?) AND state NOT IN ('passed','canceled')`, message, now.UnixMilli(), accountID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_deliveries SET state='canceled',last_error=?,manual_action='',next_retry_at=NULL,updated_at=? WHERE account_id=? AND state NOT IN ('completed','canceled')`, message, time.Now().UnixMilli(), accountID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sub2_imports SET state='failed',payload_cipher=X'',last_error=?,updated_at=? WHERE account_id=? AND state IN ('queued','failed')`, message, now.Unix(), accountID); err != nil {
		return err
	}
	if request == "" || !isFreePlan(plan) {
		return nil
	}
	paid, err := hasHistoricalR1PaidObservationTx(ctx, tx, accountID, credentialVersion)
	if err != nil || !paid {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE sub2_imports AS i SET state='cleanup_pending',credential_version=?,last_error=?,updated_at=?
		WHERE i.account_id=? AND i.create_acknowledged=1 AND i.state='imported'
		AND EXISTS (SELECT 1 FROM lifecycle_request_bindings b WHERE b.request_key=?
			AND b.binding_id=i.id AND b.account_id=i.account_id AND b.sub2_id=i.sub2_account_id
			AND b.destination=i.destination_key AND b.operation=i.operation_id AND b.binding_version=i.credential_version
			AND b.binding_version>0 AND b.source_version>0
			AND EXISTS (SELECT 1 FROM login_attempts original WHERE original.id=b.binding_version AND original.account_id=b.account_id AND original.status='success' AND original.id<=b.source_version)
			AND b.source_version=(SELECT MAX(id) FROM login_attempts WHERE account_id=i.account_id AND status='success' AND id<?))`,
		credentialVersion, message, now.Unix(), accountID, request, credentialVersion)
	return err
}

func (s *Store) convergeTerminalCredentials(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT a.id FROM accounts a
		WHERE (length(a.password_cipher)>0 OR a.totp_cipher IS NOT NULL OR a.proxy_cipher IS NOT NULL
			OR a.access_token_cipher IS NOT NULL OR a.refresh_token_cipher IS NOT NULL OR a.expires_at<>0)
		AND (a.sub2_policy=? OR EXISTS (
			SELECT 1 FROM account_lifecycle_events e
			WHERE e.account_id=a.id AND e.email=a.email AND e.source='official'
			AND e.status=CASE lower(trim(e.code))
				WHEN 'deleted' THEN 'deleted' WHEN 'account_deleted' THEN 'deleted' WHEN 'user_deleted' THEN 'deleted'
				WHEN 'deactivated' THEN 'deactivated' WHEN 'account_deactivated' THEN 'deactivated' WHEN 'user_deactivated' THEN 'deactivated'
				WHEN 'account_disabled' THEN 'deactivated' WHEN 'user_disabled' THEN 'deactivated'
				WHEN 'payment_required' THEN 'deactivated' WHEN 'subscription_expired' THEN 'deactivated' WHEN 'deactivated_workspace' THEN 'deactivated'
				WHEN 'deleted_or_deactivated' THEN 'deleted_or_deactivated' END
			AND (EXISTS (
				SELECT 1 FROM login_attempts failed
				WHERE failed.account_id=a.id AND e.dedupe_key='login_attempt:'||failed.id
				AND failed.status='failed' AND lower(trim(failed.error_code))=lower(trim(e.code))
				AND failed.finished_at=e.observed_at AND failed.finished_at>0
				AND NOT EXISTS (
					SELECT 1 FROM login_attempts newer WHERE newer.account_id=a.id AND newer.status='success'
					AND (newer.id>failed.id OR newer.finished_at>failed.finished_at)
				)
			) OR EXISTS (
				SELECT 1 FROM account_checks c JOIN login_attempts credential ON credential.id=c.credential_attempt_id
				WHERE c.account_id=a.id AND e.dedupe_key='account_check:'||c.id
				AND c.state='finished' AND c.request_attempted=1 AND c.finished_at/1000=e.observed_at
				AND lower(trim(e.code)) IN (lower(trim(c.error_code)),lower(trim(c.stream_error_code)))
				AND credential.account_id=a.id AND credential.status='success'
				AND credential.id=(SELECT MAX(id) FROM login_attempts WHERE account_id=a.id AND status='success')
			))
		))`, Sub2PolicyOfficialUnavailable)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := finalizeOfficialUnavailableTx(ctx, tx, id, "", 0); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ensureSub2EligibleTx(ctx context.Context, tx *sql.Tx, accountID int64) error {
	var status, policy string
	if err := tx.QueryRowContext(ctx, `SELECT status,COALESCE(sub2_policy,'unknown') FROM accounts WHERE id=?`, accountID).Scan(&status, &policy); errors.Is(err, sql.ErrNoRows) {
		return ErrAccountNotFound
	} else if err != nil {
		return err
	}
	account := Account{Status: status, Sub2Policy: normalizeSub2Policy(policy)}
	return account.Sub2EligibilityError()
}

func ensureSub2Eligible(ctx context.Context, s *Store, accountID int64) error {
	account, err := s.GetAccountByID(ctx, accountID)
	if err != nil {
		return err
	}
	return account.Sub2EligibilityError()
}

func freezeLifecycleBindingsTx(ctx context.Context, tx *sql.Tx, request string, accountID int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO lifecycle_request_bindings
		(request_key,binding_id,account_id,sub2_id,destination,operation,binding_version,source_version)
		SELECT ?,id,account_id,sub2_account_id,destination_key,operation_id,credential_version,
		(SELECT COALESCE(MAX(id),0) FROM login_attempts WHERE account_id=i.account_id AND status='success')
		FROM sub2_imports i WHERE account_id=? AND state='imported' AND create_acknowledged=1
		AND sub2_account_id>0 AND operation_id<>''`, request, accountID)
	return err
}

func authorizeFrozenCleanupTx(ctx context.Context, tx *sql.Tx, request string) error {
	const matched = ` FROM lifecycle_request_bindings b JOIN sub2_imports i
		ON i.id=b.binding_id AND i.account_id=b.account_id AND i.destination_key=b.destination
		AND i.sub2_account_id=b.sub2_id AND i.operation_id=b.operation AND i.credential_version=b.binding_version
		AND i.state='imported' AND i.create_acknowledged=1
		JOIN account_lifecycle_events e ON e.account_id=b.account_id AND e.dedupe_key=b.request_key
		WHERE b.request_key=? AND b.source_version>0
		AND (e.status<>'discarded' OR (b.binding_version>0 AND EXISTS (
			SELECT 1 FROM login_attempts original WHERE original.id=b.binding_version AND original.account_id=b.account_id
			AND original.status='success' AND original.id<=b.source_version)))
		AND b.source_version=
		(SELECT MAX(id) FROM login_attempts WHERE account_id=b.account_id AND status='success')`
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_lifecycle_events
		(account_id,email,source,status,reason,code,http_status,dedupe_key,observed_at)
		SELECT e.account_id,e.email,'cleanup_authorization',e.status,e.reason,e.code,e.http_status,
	(CASE WHEN e.status='discarded' THEN 'sub2-discarded:' ELSE 'sub2-official:' END)
	|| i.id || ':' || b.sub2_id || ':' || b.source_version || ':' || b.operation,e.observed_at`+
		matched+` ON CONFLICT(dedupe_key) DO NOTHING`, request); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE sub2_imports SET credential_version=b.source_version,state='cleanup_pending'
		FROM (SELECT i.id,b.source_version`+matched+`) b WHERE sub2_imports.id=b.id`, request)
	return err
}

func finalizeOfficialUnavailableTx(ctx context.Context, tx *sql.Tx, accountID int64, message string, currentAttemptID int64) error {
	return finalizeTerminalUnavailableTx(ctx, tx, accountID, message, currentAttemptID, Sub2PolicyOfficialUnavailable)
}

func finalizeTerminalUnavailableTx(ctx context.Context, tx *sql.Tx, accountID int64, message string, currentAttemptID int64, policy string) error {
	if accountID <= 0 {
		return ErrAccountNotFound
	}
	if strings.TrimSpace(message) == "" {
		message = "账号已被官方删除或停用"
		if policy == Sub2PolicyDiscarded {
			message = "账号已退役"
		}
	}
	stage, code := "official", "account_official_unavailable"
	if policy == Sub2PolicyDiscarded {
		stage, code = "discarded", "account_discarded"
	}
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE login_attempts SET status='failed',stage=?,error_code=?,error_message=?,finished_at=?,duration_ms=CASE WHEN started_at>0 THEN MAX(0,(?-started_at)*1000) ELSE 0 END WHERE account_id=? AND status='running' AND id<>?`, stage, code, message, now, now, accountID, currentAttemptID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_recovery_tasks SET state='canceled',last_error=?,retry_action='',next_retry_at=NULL,manual_action='',updated_at=? WHERE account_id=? AND state NOT IN ('completed','canceled')`, message, now*1000, accountID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_recovery_rechecks SET state='canceled',last_error=?,manual_action='',next_check_at=NULL,updated_at=? WHERE task_id IN (SELECT id FROM account_recovery_tasks WHERE account_id=?) AND state NOT IN ('passed','canceled')`, message, now*1000, accountID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_deliveries SET state='canceled',last_error=?,manual_action='',next_retry_at=NULL,updated_at=? WHERE account_id=? AND state NOT IN ('completed','canceled')`, message, now*1000, accountID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_credential_repairs SET state='canceled',last_error=?,manual_action='',next_retry_at=NULL,updated_at=? WHERE account_id=?`, message, now*1000, accountID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_checks SET state=CASE WHEN state='running' AND request_attempted=0 THEN 'skipped' ELSE 'canceled' END,skip_reason=?,error_code=?,error_message=?,finished_at=? WHERE account_id=? AND state IN ('queued','running')`, code, code, message, now*1000, accountID); err != nil {
		return err
	}
	if err := clearSub2ImportSecretsTx(ctx, tx, accountID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_check_batches SET state=CASE WHEN state='stopping' THEN 'stopped' ELSE 'completed' END,finished_at=?,proxy_cipher=NULL
		WHERE state IN ('active','stopping') AND id IN (SELECT batch_id FROM account_checks WHERE account_id=?)
		AND NOT EXISTS (SELECT 1 FROM account_checks WHERE batch_id=account_check_batches.id AND state IN ('queued','running'))`, now*1000, accountID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM lifecycle_request_bindings WHERE account_id=?`, accountID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE sub2_imports SET state=CASE WHEN state IN ('remote_missing','imported','cleanup_pending') THEN state ELSE 'failed' END,last_error=?,updated_at=? WHERE account_id=?`, message, now, accountID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET status=CASE WHEN ?='discarded' THEN 'discarded' WHEN status IN ('deleted','deactivated','deleted_or_deactivated') THEN status ELSE 'deleted_or_deactivated' END,sub2_policy=?,password_cipher=X'',totp_cipher=NULL,proxy_cipher=NULL,access_token_cipher=NULL,refresh_token_cipher=NULL,expires_at=0,updated_at=? WHERE id=?`, policy, policy, now, accountID)
	return err
}

func markOfficialUnavailableTx(ctx context.Context, tx *sql.Tx, accountID, credentialVersion int64, binding Sub2Import, event LifecycleEvent) error {
	var email, status, policy string
	if err := tx.QueryRowContext(ctx, `SELECT email,status,COALESCE(sub2_policy,'unknown') FROM accounts WHERE id=?`, accountID).Scan(&email, &status, &policy); errors.Is(err, sql.ErrNoRows) {
		return ErrAccountNotFound
	} else if err != nil {
		return err
	}
	var currentVersion int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM login_attempts WHERE account_id=? AND status='success'`, accountID).Scan(&currentVersion); err != nil {
		return err
	}
	if credentialVersion <= 0 || currentVersion != credentialVersion {
		return ErrAccountRecoveryVersionChanged
	}
	if binding.AccountID != 0 || binding.Sub2AccountID != 0 || binding.OperationID != "" || binding.DestinationKey != "" {
		if binding.AccountID != accountID || binding.Sub2AccountID <= 0 || strings.TrimSpace(binding.DestinationKey) == "" || strings.TrimSpace(binding.OperationID) == "" {
			return ErrAccountRecoveryStale
		}
		var matches bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sub2_imports WHERE id=? AND account_id=? AND destination_key=? AND sub2_account_id=? AND operation_id=? AND credential_version=? AND state IN ('imported','cleanup_pending'))`, binding.ID, accountID, binding.DestinationKey, binding.Sub2AccountID, binding.OperationID, binding.CredentialVersion).Scan(&matches); err != nil {
			return err
		}
		if !matches {
			return ErrAccountRecoveryStale
		}
	}
	event.AccountID = accountID
	event.Email = email
	event.Status = strings.ToLower(strings.TrimSpace(event.Status))
	if !isOfficialLifecycleStatus(event.Status) {
		return errors.New("official lifecycle status is required")
	}
	if event.Source != "official" || lifecycleStatusFromFailure("", event.Code) == "" {
		return errors.New("verified official account error is required")
	}
	if strings.TrimSpace(event.DedupeKey) == "" {
		return errors.New("lifecycle event dedupe key is required")
	}
	if err := recordLifecycleEventTx(ctx, tx, event); err != nil {
		return err
	}
	if binding.ID > 0 && event.DedupeKey == binding.OfficialCleanupEvidenceKey(credentialVersion) {
		if _, err := tx.ExecContext(ctx, `UPDATE sub2_imports SET credential_version=?,state='cleanup_pending' WHERE id=? AND credential_version=? AND state IN ('imported','cleanup_pending')`, credentialVersion, binding.ID, binding.CredentialVersion); err != nil {
			return err
		}
	}
	return finalizeOfficialUnavailableTx(ctx, tx, accountID, event.Reason, 0)
}

// MarkOfficialUnavailable commits trusted official terminal evidence only when
// the successful credential version and exact Sub2 binding still match.
func (s *Store) MarkOfficialUnavailable(ctx context.Context, accountID, credentialVersion int64, binding Sub2Import, event LifecycleEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := markOfficialUnavailableTx(ctx, tx, accountID, credentialVersion, binding, event); err != nil {
		return err
	}
	return tx.Commit()
}

// FinalizeOfficialUnavailableAccount is idempotent and preserves attempts and
// lifecycle evidence while removing all sensitive credentials and live work.
func (s *Store) FinalizeOfficialUnavailableAccount(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrAccountNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status, policy string
	if err := tx.QueryRowContext(ctx, `SELECT status,COALESCE(sub2_policy,'unknown') FROM accounts WHERE id=?`, id).Scan(&status, &policy); errors.Is(err, sql.ErrNoRows) {
		return ErrAccountNotFound
	} else if err != nil {
		return err
	}
	if !isOfficialLifecycleStatus(status) && normalizeSub2Policy(policy) != Sub2PolicyOfficialUnavailable {
		return nil
	}
	if err := finalizeOfficialUnavailableTx(ctx, tx, id, "账号已被官方删除或停用", 0); err != nil {
		return err
	}
	return tx.Commit()
}

// FinalizeDeletedAccount remains for existing callers.
func (s *Store) FinalizeDeletedAccount(ctx context.Context, id int64) error {
	return s.FinalizeOfficialUnavailableAccount(ctx, id)
}

func clearSub2ImportSecretsTx(ctx context.Context, tx *sql.Tx, accountID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE sub2_imports SET payload_cipher=X'' WHERE account_id=?`, accountID)
	return err
}
