package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func (s *sub2RecoveryService) handleRecheckRetry(w http.ResponseWriter, r *http.Request, taskID int64) {
	if r.Method != http.MethodPost {
		recoveryAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不支持")
		return
	}
	if !s.configured() || s.paused.Load() || s.ctx.Err() != nil {
		recoveryAPIError(w, http.StatusServiceUnavailable, "recovery_unavailable", "恢复服务暂不可用")
		return
	}
	task, err := s.store.GetAccountRecoveryTaskByIDForDestination(r.Context(), taskID, s.sub2.destinationKey)
	if errors.Is(err, store.ErrAccountRecoveryNotFound) {
		recoveryAPIError(w, http.StatusNotFound, "recovery_not_found", "恢复任务不存在")
		return
	}
	if err != nil {
		recoveryAPIError(w, http.StatusInternalServerError, "recovery_read_failed", "读取恢复任务失败")
		return
	}
	if err := s.validateRecheckOwnership(r.Context(), task, nil); err != nil {
		recoveryAPIError(w, http.StatusConflict, "recheck_not_resumable", safeRecoveryMessage(err))
		return
	}
	resumed, err := s.store.RetryAccountRecoveryRecheck(r.Context(), taskID, s.sub2.destinationKey)
	if err != nil {
		recoveryAPIError(w, http.StatusInternalServerError, "recheck_retry_failed", "复检提交失败，请刷新后重试")
		return
	}
	if !resumed {
		recoveryAPIError(w, http.StatusConflict, "recheck_not_resumable", "复检状态已变化，请刷新后重试")
		return
	}
	s.notify()
	respondJSONStatus(w, http.StatusAccepted, map[string]any{"success": true, "task_id": taskID})
}

// The existing recovery worker runs one persisted follow-up when its login
// queue is empty. Closing the page or restarting does not discard the probe.
func (s *sub2RecoveryService) processNextRecheck() bool {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	check, err := s.store.ClaimAccountRecoveryRecheckForDestination(ctx, time.Now(), s.sub2.destinationKey)
	cancel()
	if err != nil {
		if !errors.Is(err, store.ErrQueueDeferred) || !retryStorageRead(s.ctx, err) {
			s.pauseRecovery(0, err)
		}
		return false
	}
	if check == nil {
		return false
	}
	s.processRecheck(*check)
	return true
}

func (s *sub2RecoveryService) finishRecheck(check store.AccountRecoveryRecheck, state, message string, next *time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.FinishAccountRecoveryRecheck(ctx, check.TaskID, state, message, next); err != nil {
		s.pauseRecovery(check.TaskID, err)
	}
}

func (s *sub2RecoveryService) recheckFailure(check store.AccountRecoveryRecheck, err error) {
	failure := classifyRecoveryError(err)
	if failure.Code == "manual_pause" || failure.Code == "version_changed" || failure.Code == "sub2_account_missing" {
		s.finishRecheck(check, "canceled", failure.Error(), nil)
	} else if failure.RequiresAction {
		s.finishRecheck(check, "requires_action", failure.Error(), nil)
	} else {
		next := time.Now().UTC().Add(recoveryRetryDelay(failure, check.RetryCount))
		s.finishRecheck(check, "pending", failure.Error(), &next)
	}
}

func (s *sub2RecoveryService) processRecheck(check store.AccountRecoveryRecheck) {
	ctx, cancel := context.WithTimeout(s.ctx, recoveryProbeTimeout+30*time.Second)
	defer cancel()
	lease, err := s.store.AcquireAccountRecovery(ctx, check.AccountID)
	if err != nil {
		if errors.Is(err, store.ErrAccountBusy) {
			err = &recoveryOperationError{Code: "account_busy"}
		}
		s.recheckFailure(check, err)
		return
	}
	defer lease.Release()
	task, err := s.store.GetAccountRecoveryTaskByID(ctx, check.TaskID)
	if err != nil {
		s.recheckFailure(check, err)
		return
	}
	var probe func() error
	if err := s.validateRecheckOwnership(ctx, task, &probe); err != nil {
		s.recheckFailure(check, err)
		return
	}
	if probe != nil {
		err = probe()
	} else {
		err = s.verifySub2AccountProbe(ctx, task.Sub2AccountID)
	}
	// The admin test uses Sub2's current token and egress. Re-read ownership and
	// operator state after it, because its remote request can outlive a pause.
	if ownershipErr := s.validateRecheckOwnership(ctx, task, nil); ownershipErr != nil {
		s.recheckFailure(check, ownershipErr)
		return
	}
	if err == nil {
		s.finishRecheck(check, "passed", "", nil)
		return
	}
	failure := classifyRecoveryError(err)
	if !failure.CredentialInvalid {
		s.recheckFailure(check, failure)
		return
	}
	if !s.autoRecovery.Load() {
		next := time.Now().UTC().Add(5 * time.Minute)
		s.finishRecheck(check, "pending", "Sub2 凭据已失效；自动恢复关闭，启用后继续处理", &next)
		return
	}
	if _, err := s.store.QueueAccountRecoveryFromRecheck(ctx, task.ID); err != nil {
		if errors.Is(err, store.ErrAccountBusy) || errors.Is(err, store.ErrAccountRecoveryVersionChanged) {
			err = &recoveryOperationError{Code: "version_changed"}
		}
		s.recheckFailure(check, err)
		return
	}
	s.notify()
}

func (s *sub2RecoveryService) validateRecheckOwnership(ctx context.Context, task store.AccountRecoveryTask, probe *func() error) error {
	if failure, err := s.automationFailure(ctx, task.AccountID); err != nil {
		return err
	} else if failure != nil {
		return failure
	}
	latest, err := s.store.GetLatestAccountRecoveryTaskForDestination(ctx, task.AccountID, s.sub2.destinationKey)
	if err != nil {
		return err
	}
	if latest.ID != task.ID || latest.State != store.RecoveryCompleted || !task.OriginalSchedulable {
		return &recoveryOperationError{Code: "version_changed", RequiresAction: true}
	}
	if err := s.store.ValidateAccountRecoveryVersion(ctx, task.ID); err != nil {
		if errors.Is(err, store.ErrAccountRecoveryVersionChanged) {
			return &recoveryOperationError{Code: "version_changed", RequiresAction: true}
		}
		return err
	}
	account, err := s.store.GetAccountByID(ctx, task.AccountID)
	if err != nil {
		return err
	}
	binding, err := s.sub2.store.GetSub2Import(ctx, s.sub2.destinationKey, task.AccountID)
	if err != nil && !errors.Is(err, store.ErrSub2ImportNotFound) {
		return err
	}
	if err != nil || binding.State != "imported" || binding.Sub2AccountID != task.Sub2AccountID {
		return &recoveryOperationError{Code: "identity_changed", RequiresAction: true}
	}
	detail, err := s.sub2Account(withSub2StatusFresh(ctx), task.Sub2AccountID)
	if err != nil {
		return err
	}
	if verifySub2RecoveryIdentity(detail, task.Sub2AccountID, task.AccountID, account, binding) != nil {
		return &recoveryOperationError{Code: "identity_changed", RequiresAction: true}
	}
	if err := s.verifyRecheckCheckpoint(ctx, task, detail, binding, probe); err != nil {
		return err
	}
	scheduled, ok := boolField(detail, "schedulable")
	if !ok {
		return &recoveryOperationError{Code: "checkpoint_unconfirmed", RequiresAction: true}
	}
	status, _ := detail["status"].(string)
	if recoveryAccountDisabled(detail) || strings.EqualFold(status, "active") && !scheduled {
		return &recoveryOperationError{Code: "manual_pause", RequiresAction: true}
	}
	return nil
}

func (s *sub2RecoveryService) verifyRecheckCheckpoint(ctx context.Context, task store.AccountRecoveryTask, detail map[string]any, binding store.Sub2Import, probe *func() error) error {
	changed := &recoveryOperationError{Code: "identity_changed", RequiresAction: true}
	if task.Purpose == "delivery" && task.DeliveryID > 0 {
		delivery, err := s.store.GetAccountDelivery(ctx, task.DeliveryID)
		if err != nil {
			return err
		}
		if delivery.OriginalBindingID > 0 {
			extra, _ := detail["extra"].(map[string]any)
			_, hasMarker := extra["kkai_auth_import"]
			if !delivery.MatchesOriginalBinding(binding) || delivery.RecoveryTaskID != task.ID ||
				delivery.CredentialVersion != task.ResultCredentialAttemptID || task.ResultCredentialAttemptID <= 0 ||
				delivery.State == "canceled" || hasMarker && !matchesImportMarker(detail, binding) {
				return changed
			}
			operation := fmt.Sprintf("auth-delivery-%d-%d", delivery.ID, delivery.CredentialVersion)
			path := fmt.Sprintf("/admin/accounts/%d/lifecycle-oauth/%s", task.Sub2AccountID, operation)
			var receipt lifecycleOAuthReceipt
			if err := s.sub2JSONBody(ctx, http.MethodGet, path, nil, &receipt); err != nil {
				return err
			}
			stamp, _ := detail["updated_at"].(string)
			revision, err := time.Parse(time.RFC3339Nano, stamp)
			if err != nil || !receipt.AccountRevision.Equal(revision) || receipt.ID != task.Sub2AccountID ||
				receipt.OperationID != operation || receipt.SourceAccountID != task.AccountID ||
				receipt.ImportOperationID != binding.OperationID || receipt.CredentialRevision != delivery.CredentialVersion ||
				receipt.Phase != "enable" || !receipt.MayEnable || !receipt.Schedulable {
				return changed
			}
			if probe != nil {
				credentials, _ := detail["credentials"].(map[string]any)
				workspace, _ := credentials["chatgpt_account_id"].(string)
				if workspace == "" {
					return changed
				}
				*probe = func() error {
					return s.sub2JSONBody(ctx, http.MethodPost, strings.TrimSuffix(path, "/"+operation), map[string]any{
						"phase": "probe", "expected_updated_at": revision.UTC(), "expected_email": task.AccountEmail,
						"expected_workspace": workspace, "source_account_id": task.AccountID,
						"import_operation_id": binding.OperationID, "operation_id": operation,
						"credential_revision": delivery.CredentialVersion,
					}, nil)
				}
			}
			return nil
		}
	}
	if !recoveryMarkerMatches(detail, task) {
		return changed
	}
	return nil
}

func (s *sub2RecoveryService) delayedRecheckRetryOwned(ctx context.Context, task store.AccountRecoveryTask, detail map[string]any) bool {
	if task.RetryAction != "relogin" && task.RetryAction != "resume" {
		return false
	}
	parent, err := s.store.GetAccountRecoveryRecheckParent(ctx, task.ID)
	if err != nil || parent.State != store.RecoveryCompleted || parent.AccountID != task.AccountID || parent.Sub2AccountID != task.Sub2AccountID || parent.ResultCredentialAttemptID != task.SourceCredentialAttemptID {
		return false
	}
	binding, err := s.store.GetSub2Import(ctx, s.sub2.destinationKey, task.AccountID)
	if err != nil || binding.State != "imported" || binding.Sub2AccountID != task.Sub2AccountID || s.verifyRecheckCheckpoint(ctx, parent, detail, binding, nil) != nil {
		return false
	}
	scheduled, ok := boolField(detail, "schedulable")
	status, _ := detail["status"].(string)
	if !ok || recoveryAccountDisabled(detail) {
		return false
	}
	// A saved phase after disabling confirms that this worker owns the pause;
	// validating/disabling alone cannot distinguish an operator's intervention.
	pauseConfirmed := task.FailureStage == store.RecoveryLoggingIn || task.FailureStage == store.RecoveryLoginSucceeded ||
		task.FailureStage == store.RecoveryIdentityVerified || task.FailureStage == store.RecoveryApplyingCredentials
	beforeApply := task.FailureStage == "delayed_recheck" || task.FailureStage == store.RecoveryValidating ||
		task.FailureStage == store.RecoveryDisablingSchedule || pauseConfirmed
	if !beforeApply {
		return false
	}
	return scheduled || strings.EqualFold(status, "error") || pauseConfirmed
}
