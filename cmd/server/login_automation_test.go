package main

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func TestLoginAutomationDeterministicFailures(t *testing.T) {
	for _, code := range []string{"email_verification_required", "phone_verification_required", "password_rejected", "totp_rejected", "verification_code_rejected", "credentials_rejected"} {
		t.Run(code, func(t *testing.T) {
			f := newRecoveryFixture(t, true)
			ctx := context.Background()
			credentials, err := f.store.GetCredentialsByID(ctx, f.account.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, attempt, err := f.store.BeginAttempt(ctx, credentials)
			if err != nil {
				t.Fatal(err)
			}
			stage := "mfa"
			switch code {
			case "email_verification_required":
				stage = "email"
			case "password_rejected", "credentials_rejected":
				stage = "password"
			}
			err = f.store.FinishAttempt(ctx, attempt.ID, false, nil, &store.Failure{Stage: stage, Code: code, HTTPStatus: 400, Message: "specific safe failure"})
			attempt.Release()
			if err != nil {
				t.Fatal(err)
			}
			switch code {
			case "email_verification_required", "password_rejected", "credentials_rejected":
				account, err := f.store.GetAccountByID(ctx, f.account.ID)
				if err != nil || !account.IsTerminalUnavailable() || account.IsOfficialUnavailable() {
					t.Fatalf("expected discarded account: %s, %v", account.Status, err)
				}
				allowed, err := f.service.automaticRecoveryAllowed(ctx, f.account.ID)
				if err != nil || allowed || f.loginCalls != 0 {
					t.Fatalf("discarded account can retry: %t, %v", allowed, err)
				}
				return
			}
			if _, err = f.db.Exec(`UPDATE account_recovery_tasks SET state='completed',retry_action='',next_retry_at=NULL,manual_action='' WHERE id=?`, f.task.ID); err != nil {
				t.Fatal(err)
			}
			allowed, err := f.service.automaticRecoveryAllowed(ctx, f.account.ID)
			if err != nil || allowed {
				t.Fatal("completed old task bypassed deterministic login stop")
			}
			if _, err = f.db.Exec(`UPDATE account_recovery_tasks SET state='unknown',retry_action='relogin',next_retry_at=?,manual_action='' WHERE id=?`, time.Now().Add(-time.Minute).UnixMilli(), f.task.ID); err != nil {
				t.Fatal(err)
			}
			f.service.autoRecovery.Store(true)
			f.service.retryDueRecoveries()
			got := f.state(t)
			if !got.RequiresAction || got.ErrorCode != code || got.NextRetryAt != nil || got.RetryAction != "manual" || f.loginCalls != 0 {
				t.Fatalf("old retry did not stop: %+v", got)
			}
			failure := classifyRecoveryLoginInfo(login.LoginErrorInfo{Code: code, HTTPStatus: 400}, 0)
			if failure.Code != code || !failure.RequiresAction || failure.HTTPStatus != 400 {
				t.Fatal("specific error lost to generic 4xx")
			}
			if _, err = f.db.Exec(`DELETE FROM account_recovery_tasks WHERE id=?`, f.task.ID); err != nil {
				t.Fatal(err)
			}
			allowed, err = f.service.automaticRecoveryAllowed(ctx, f.account.ID)
			if err != nil || allowed {
				t.Fatal("absence of recovery task bypassed login stop")
			}
		})
	}
}

func persistDeterministicLoginStop(t *testing.T, f *recoveryFixture, code string) {
	t.Helper()
	ctx := context.Background()
	credentials, err := f.store.GetCredentialsByID(ctx, f.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := f.store.BeginAttempt(ctx, credentials)
	if err != nil {
		t.Fatal(err)
	}
	err = f.store.FinishAttempt(ctx, attempt.ID, false, nil, &store.Failure{Stage: "password", Code: code, HTTPStatus: 400, Message: "确定的登录错误"})
	attempt.Release()
	if err != nil {
		t.Fatal(err)
	}
	f.service.recordRecoveryFailure(f.task, store.RecoveryLoggingIn, &recoveryOperationError{Code: code, RequiresAction: true})
	f.schedule = false
}

func TestCredentialRepairDeterministicFailureAllowsOneSavedEdit(t *testing.T) {
	for _, succeeds := range []bool{true, false} {
		name := "rejects_again"
		if succeeds {
			name = "succeeds"
		}
		t.Run(name, func(t *testing.T) {
			f := newRecoveryFixture(t, true)
			ctx := context.Background()
			persistDeterministicLoginStop(t, f, login.LoginErrorTOTPRejected)
			password := "corrected-password"
			if _, err := f.store.PatchAccountCredentials(ctx, f.account.ID, store.CredentialPatch{Password: &password}, true); err != nil {
				t.Fatal(err)
			}
			if !f.service.processNextCredentialRepair() {
				t.Fatal("saved edit not claimed")
			}
			pending, err := f.store.GetCredentialRepair(ctx, f.account.ID)
			if err != nil || pending.State != "recovery_queued" {
				t.Fatalf("handoff missing: %+v %v", pending, err)
			}
			f.service.loginWithProxies = func(_ context.Context, email, gotPassword, _, _, _ string) (*login.LoginResult, error) {
				f.loginCalls++
				if gotPassword != password {
					t.Fatal("old password retried")
				}
				if !succeeds {
					return nil, &recoveryOperationError{Code: login.LoginErrorTOTPRejected, RequiresAction: true}
				}
				return &login.LoginResult{Email: email, AccessToken: "new-at", RefreshToken: "new-rt", ChatGPTAccountID: "workspace", PlanType: "plus"}, nil
			}
			f.service.process(f.state(t))
			task := f.state(t)
			repair, err := f.store.GetCredentialRepair(ctx, f.account.ID)
			if err != nil || repair.State != "completed" || repair.AttemptID == 0 || f.loginCalls != 1 {
				t.Fatalf("saved edit not consumed once: %+v task=%+v calls=%d err=%v", repair, task, f.loginCalls, err)
			}
			if succeeds && (task.State != store.RecoveryCompleted || !f.schedule) {
				t.Fatalf("corrected credentials did not recover: %+v", task)
			}
			if !succeeds && (task.RetryAction != "manual" || task.NextRetryAt != nil || task.ErrorCode != login.LoginErrorTOTPRejected) {
				t.Fatalf("rejected edit did not stop: %+v", task)
			}
			if err := f.store.RecoverAccountRecoveryTasks(ctx); err != nil {
				t.Fatal(err)
			}
			f.service.autoRecovery.Store(true)
			f.service.retryDueRecoveries()
			f.service.processNextCredentialRepair()
			if f.loginCalls != 1 {
				t.Fatal("restart or scan replayed saved edit")
			}
		})
	}
}

func TestLoginAutomationNewSuccessAllowsNewUnauthorizedRecovery(t *testing.T) {
	f := newRecoveryFixture(t, true)
	ctx := context.Background()
	persistDeterministicLoginStop(t, f, login.LoginErrorTOTPRejected)
	credentials, err := f.store.GetCredentialsByID(ctx, f.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := f.store.BeginAttempt(ctx, credentials)
	if err != nil {
		t.Fatal(err)
	}
	err = f.store.FinishAttempt(ctx, attempt.ID, true, &store.Result{AccessToken: "manual-at", RefreshToken: "manual-rt", ChatGPTAccountID: "workspace", PlanType: "plus"}, nil)
	attempt.Release()
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := f.service.automaticRecoveryAllowed(ctx, f.account.ID)
	if err != nil || !allowed {
		t.Fatalf("new success still blocked by old TOTP error: %v", err)
	}
	if _, _, err := f.store.CreateAccountCheckBatch(ctx, store.AccountCheckInput{RequestKey: "new-credential-401", AccountIDs: []int64{f.account.ID}, Concurrency: 1, ProxyMode: "direct"}); err != nil {
		t.Fatal(err)
	}
	work, err := f.store.ClaimAccountCheck(ctx)
	if err != nil || work == nil {
		t.Fatalf("new check missing: %v", err)
	}
	status := 401
	if _, err := f.store.FinishAccountCheck(ctx, work.Check.ID, store.AccountCheckResult{Outcome: "unauthorized", ErrorCode: "unauthorized", HTTPStatus: &status}); err != nil {
		t.Fatal(err)
	}
	f.service.autoRecovery.Store(true)
	task, created, err := f.service.enqueue(ctx, f.account.ID, true)
	if err != nil || !created || task.ID == f.task.ID || task.SourceCredentialAttemptID != attempt.ID {
		t.Fatalf("new 401 did not create current recovery: %+v created=%v err=%v", task, created, err)
	}
	f.service.process(task)
	got, err := f.store.GetAccountRecoveryTaskByID(ctx, task.ID)
	if err != nil || got.State != store.RecoveryCompleted || f.loginCalls != 1 {
		t.Fatalf("new 401 did not recover: %+v calls=%d err=%v", got, f.loginCalls, err)
	}
}

func TestLoginAutomationNewSuccessPreservesOtherManualStops(t *testing.T) {
	for _, code := range []string{"identity_changed", "group_changed", "manual_pause", "sub2_account_missing", "account_unavailable"} {
		t.Run(code, func(t *testing.T) {
			f := newRecoveryFixture(t, true)
			ctx := context.Background()
			f.service.recordRecoveryFailure(f.task, store.RecoveryValidating, &recoveryOperationError{Code: code, RequiresAction: true})
			credentials, err := f.store.GetCredentialsByID(ctx, f.account.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, attempt, err := f.store.BeginAttempt(ctx, credentials)
			if err != nil {
				t.Fatal(err)
			}
			err = f.store.FinishAttempt(ctx, attempt.ID, true, &store.Result{AccessToken: "new-at", RefreshToken: "new-rt", ChatGPTAccountID: "workspace"}, nil)
			attempt.Release()
			if err != nil {
				t.Fatal(err)
			}
			if allowed, err := f.service.automaticRecoveryAllowed(ctx, f.account.ID); err != nil || allowed {
				t.Fatalf("new login bypassed %s: allowed=%v err=%v", code, allowed, err)
			}
		})
	}
}
