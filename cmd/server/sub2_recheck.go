package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

// The existing recovery worker runs one persisted follow-up when its login
// queue is empty. Closing the page or restarting does not discard the probe.
func (s *sub2RecoveryService) processNextRecheck() bool {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	check, err := s.store.ClaimAccountRecoveryRecheckForDestination(ctx, time.Now(), s.sub2.destinationKey)
	cancel()
	if err != nil {
		s.pauseRecovery(0)
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
		s.pauseRecovery(check.TaskID)
	}
}

func (s *sub2RecoveryService) recheckFailure(check store.AccountRecoveryRecheck, err error) {
	failure := classifyRecoveryError(err)
	if failure.Code == "manual_pause" || failure.Code == "version_changed" {
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
	if err := s.validateRecheckOwnership(ctx, task); err != nil {
		s.recheckFailure(check, err)
		return
	}
	err = s.verifySub2AccountProbe(ctx, task.Sub2AccountID)
	// The admin test uses Sub2's current token and egress. Re-read ownership and
	// operator state after it, because its remote request can outlive a pause.
	if ownershipErr := s.validateRecheckOwnership(ctx, task); ownershipErr != nil {
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

func (s *sub2RecoveryService) validateRecheckOwnership(ctx context.Context, task store.AccountRecoveryTask) error {
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
	detail, err := s.sub2Account(ctx, task.Sub2AccountID)
	if err != nil {
		return err
	}
	if verifySub2RecoveryIdentity(detail, task.Sub2AccountID, task.AccountID, account, binding) != nil || !recoveryMarkerMatches(detail, task) {
		return &recoveryOperationError{Code: "identity_changed", RequiresAction: true}
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

func (s *sub2RecoveryService) delayedRecheckRetryOwned(ctx context.Context, task store.AccountRecoveryTask, detail map[string]any) bool {
	if task.RetryAction != "relogin" && task.RetryAction != "resume" {
		return false
	}
	parent, err := s.store.GetAccountRecoveryRecheckParent(ctx, task.ID)
	if err != nil || parent.AccountID != task.AccountID || parent.Sub2AccountID != task.Sub2AccountID || parent.ResultCredentialAttemptID != task.SourceCredentialAttemptID || !recoveryMarkerMatches(detail, parent) {
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
