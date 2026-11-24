package main

import (
	"context"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

// Read the latest persisted login outcome even when no recovery task exists.
// A stale completed/retry_wait task cannot authorize repeating a definitive
// rejection. A later successful login naturally supersedes this evidence.
func (s *sub2RecoveryService) loginAutomationFailure(ctx context.Context, accountID int64) (*recoveryOperationError, error) {
	account, err := s.store.GetAccountByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account.Sub2Policy == store.Sub2PolicyDiscarded || account.Status == "discarded" {
		return &recoveryOperationError{Code: "account_discarded"}, nil
	}
	if account.IsOfficialUnavailable() {
		return &recoveryOperationError{Code: "account_unavailable", RequiresAction: true}, nil
	}
	if account.Status != "failed" && account.Status != "interrupted" {
		return nil, nil
	}
	attempts, err := s.store.ListAttempts(ctx, account.Email, 1)
	if err != nil {
		return nil, err
	}
	if len(attempts) == 0 || (attempts[0].Status != "failed" && attempts[0].Status != "interrupted") {
		return nil, nil
	}
	attempt := attempts[0]
	info := login.NormalizeErrorInfo(login.LoginErrorInfo{Stage: attempt.Stage, Code: attempt.Code, HTTPStatus: attempt.HTTPStatus, Retryable: attempt.Retryable})
	failure := classifyRecoveryLoginInfo(info, 0)
	if attempt.SingleAttempt && attempt.Status == "interrupted" {
		failure.Code = "interrupted"
	}
	// Explicit intent is independent of the error's normal retry policy, so
	// unknown failures also stop while old default records retain their policy.
	if login.IsDeterministicFailure(info) || attempt.SingleAttempt {
		failure.RequiresAction = true
		return failure, nil
	}
	return nil, nil
}

// Shared checkpoint for queued work, pre-login and pre-write decisions.
func (s *sub2RecoveryService) automationFailure(ctx context.Context, accountID int64) (*recoveryOperationError, error) {
	missing, err := s.store.Sub2AccountMissing(ctx, s.sub2.destinationKey, accountID)
	if err != nil {
		return nil, err
	}
	if missing {
		return &recoveryOperationError{Code: "sub2_account_missing", RequiresAction: true}, nil
	}
	return s.loginAutomationFailure(ctx, accountID)
}

// Explicit credential edits may retry their bound recovery once. Missing,
// deactivated and unrelated identity or operator-state failures stay blocked.
func (s *sub2RecoveryService) recoveryAutomationFailure(ctx context.Context, accountID, taskID int64) (*recoveryOperationError, error) {
	failure, err := s.automationFailure(ctx, accountID)
	if err != nil || failure == nil || !manualLoginRetryAllowed(failure.Code) {
		return failure, err
	}
	task, err := s.store.GetAccountRecoveryTaskByIDForDestination(ctx, taskID, s.sub2.destinationKey)
	if err != nil {
		return nil, err
	}
	if task.AccountID == accountID && task.ManualAttempt() && task.State != store.RecoveryFailed && task.State != store.RecoveryUnknown && task.State != store.RecoveryCompleted && task.State != store.RecoveryCanceled {
		return nil, s.store.ValidateAccountRecoveryVersion(ctx, taskID)
	}
	if !login.IsDeterministicFailure(login.LoginErrorInfo{Code: failure.Code}) {
		return failure, nil
	}
	allowed, err := s.store.CredentialRepairAuthorizesRecovery(ctx, accountID, taskID)
	if err != nil {
		return nil, err
	}
	if allowed {
		return nil, nil
	}
	return failure, nil
}

func manualLoginRetryAllowed(code string) bool {
	if login.IsDeterministicFailure(login.LoginErrorInfo{Code: code}) {
		return true
	}
	switch code {
	case "auth_page_timeout_exhausted", "auth_page_timeout", "timeout", "network_error", "challenge", "login_failed", "interrupted", "process_interrupted", "rate_limited", "upstream_error", "browser_runtime_permission":
		return true
	}
	return false
}

// A strictly newer login supersedes credential-specific observations.
// An unconfirmed plan also requires a paid observation for that new version.
func (s *sub2RecoveryService) successfulLoginSupersedesFailure(ctx context.Context, task store.AccountRecoveryTask) (bool, error) {
	if task.RetryAction != "manual" || (task.State != store.RecoveryFailed && task.State != store.RecoveryUnknown) {
		return false, nil
	}
	switch task.FailureStage {
	case store.RecoveryValidating:
		if task.ErrorCode != "reauthorize" && task.ErrorCode != "verify_remote_refresh" {
			return false, nil
		}
	case store.RecoveryLoggingIn:
		if !login.IsDeterministicFailure(login.LoginErrorInfo{Code: task.ErrorCode}) && task.ErrorCode != "auth_page_timeout_exhausted" {
			return false, nil
		}
	case store.RecoveryLoginSucceeded, store.RecoveryRefreshingCredentials:
		if task.ErrorCode != store.RecoveryPlanUnconfirmed {
			return false, nil
		}
	default:
		return false, nil
	}
	account, err := s.store.GetAccountByID(ctx, task.AccountID)
	if err != nil || account.CredentialVersion <= task.SourceCredentialAttemptID || account.CredentialVersion <= task.ResultCredentialAttemptID {
		return false, err
	}
	if task.FailureStage == store.RecoveryLoggingIn || task.FailureStage == store.RecoveryValidating {
		return true, nil
	}
	if !account.AllowsSub2() {
		return false, nil
	}
	plan, err := s.store.ObservedCredentialPlan(ctx, task.AccountID, account.CredentialVersion)
	return store.IsSub2PaidPlan(plan), err
}
