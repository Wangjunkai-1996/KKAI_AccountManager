package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func manualRecoveryRequest(task store.AccountRecoveryTask) sub2RecoveryRequest {
	id, count := task.ID, task.RetryCount
	return sub2RecoveryRequest{AccountID: task.AccountID, ManualAttempt: true, ExpectedTaskID: &id, ExpectedRetryCount: &count}
}

func persistManualLoginStop(t *testing.T, f *recoveryFixture, code string) {
	t.Helper()
	persistDeterministicLoginStop(t, f, code)
	if code == "auth_page_timeout_exhausted" {
		// Page-budget exhaustion belongs to recovery; login history keeps its
		// real retryable timeout, as production does.
		if _, err := f.db.Exec(`UPDATE login_attempts SET error_code='timeout',retryable=1 WHERE account_id=? AND status='failed'`, f.account.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestManualRecoveryOneAttemptAfterLoginStop(t *testing.T) {
	for _, code := range []string{"auth_page_timeout_exhausted", login.LoginErrorEmailVerificationRequired, login.LoginErrorPhoneVerificationRequired, login.LoginErrorPasswordRejected, login.LoginErrorTOTPRejected} {
		t.Run(code, func(t *testing.T) {
			f := newRecoveryFixture(t, true)
			ctx := context.Background()
			persistManualLoginStop(t, f, code)
			f.service.autoRecovery.Store(true)
			if allowed, err := f.service.automaticRecoveryAllowed(ctx, f.account.ID); err != nil || allowed {
				t.Fatalf("automatic pause lost: allowed=%v err=%v", allowed, err)
			}
			req := manualRecoveryRequest(f.state(t))
			body, _ := json.Marshal(req)
			request := httptest.NewRequest(http.MethodPost, "/api/account-recovery", strings.NewReader(string(body)))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			f.service.handleCollection(response, request)
			if code == login.LoginErrorPasswordRejected {
				if response.Code != http.StatusGone || !strings.Contains(response.Body.String(), "account_discarded") || f.loginCalls != 0 {
					t.Fatalf("discarded account response=%d %s", response.Code, response.Body.String())
				}
				return
			}
			if response.Code != http.StatusOK {
				t.Fatalf("manual endpoint rejected stopped login: %d %s", response.Code, response.Body.String())
			}
			task := f.state(t)
			if task.ID != *req.ExpectedTaskID || !task.ManualAttempt() {
				t.Fatalf("manual intent not persisted: %+v", task)
			}
			if duplicate, _, err := f.service.enqueueWithIntent(ctx, f.account.ID, true, req); err != nil || duplicate.ID != task.ID {
				t.Fatalf("active duplicate not reused: %v", err)
			}
			f.service.loginWithProxies = func(context.Context, string, string, string, string, string) (*login.LoginResult, error) {
				f.loginCalls++
				return nil, &recoveryOperationError{Code: login.LoginErrorTOTPRejected, RequiresAction: true}
			}
			f.service.process(task)
			got := f.state(t)
			if f.loginCalls != 1 || f.applyCalls != 0 || got.RetryAction != "manual" || got.NextRetryAt != nil || got.ErrorCode != login.LoginErrorTOTPRejected {
				t.Fatalf("manual rejection did not stop once: %+v calls=%d", got, f.loginCalls)
			}
			if _, _, err := f.service.enqueueWithIntent(ctx, f.account.ID, true, req); err == nil {
				t.Fatalf("old HTTP snapshot reauthorized: %v", err)
			}
			if _, err := f.store.RetryManualAccountRecoveryTask(ctx, task, true); !errors.Is(err, store.ErrAccountRecoveryStale) {
				t.Fatalf("stale store snapshot reauthorized: %v", err)
			}
			if err := f.store.RecoverAccountRecoveryTasks(ctx); err != nil {
				t.Fatal(err)
			}
			f.service.retryDueRecoveries()
			if f.state(t).State != store.RecoveryFailed || f.loginCalls != 1 {
				t.Fatal("background replayed consumed manual intent")
			}
		})
	}
}

func TestManualRecoveryWithoutPriorTaskRequiresAuthorization(t *testing.T) {
	f := newMonitorFixture(t)
	ctx := context.Background()
	finishRecoveryCheck(t, f.store, f.db, f.account.ID, "manual-check", "unauthorized", "", "", nil)
	request := manualRecoveryRequest(store.AccountRecoveryTask{AccountID: f.account.ID})
	_, created, err := f.service.enqueueWithIntent(ctx, f.account.ID, true, request)
	if err == nil || created || classifyRecoveryError(err).Code != "reauthorize" {
		t.Fatalf("manual endpoint accepted an AUTH-only result: created=%v err=%v", created, err)
	}
	assertCheckAction(t, f, "reauthorize")
}

func TestManualRecoveryRestartAndTransientFailureStop(t *testing.T) {
	for _, restart := range []bool{false, true} {
		f := newRecoveryFixture(t, true)
		ctx := context.Background()
		persistManualLoginStop(t, f, "auth_page_timeout_exhausted")
		request := manualRecoveryRequest(f.state(t))
		task, _, err := f.service.enqueueWithIntent(ctx, f.account.ID, true, request)
		if err != nil {
			t.Fatal(err)
		}
		if restart {
			if _, err := f.store.ClaimAccountRecoveryTask(ctx, task.ID); err != nil {
				t.Fatal(err)
			}
			if err := f.store.RecoverAccountRecoveryTasks(ctx); err != nil {
				t.Fatal(err)
			}
		} else {
			f.service.loginWithProxies = func(context.Context, string, string, string, string, string) (*login.LoginResult, error) {
				f.loginCalls++
				return nil, &recoveryOperationError{Code: "network_error"}
			}
			f.service.process(task)
		}
		f.service.autoRecovery.Store(true)
		f.service.retryDueRecoveries()
		got := f.state(t)
		if got.RetryAction != "manual" || got.NextRetryAt != nil || got.State == store.RecoveryQueued {
			t.Fatalf("single attempt became automatic after restart=%v: %+v", restart, got)
		}
	}
}

func TestManualRecoveryPreservesSafetyGatesAndCheckpoint(t *testing.T) {
	for _, gate := range []string{"sub2_missing", "deleted", "identity", "manual_pause", "checkpoint"} {
		t.Run(gate, func(t *testing.T) {
			f := newRecoveryFixture(t, true)
			ctx := context.Background()
			if gate == "checkpoint" {
				f.enableFailsOnce = true
				f.service.process(f.task)
			} else {
				persistDeterministicLoginStop(t, f, login.LoginErrorPasswordRejected)
			}
			switch gate {
			case "sub2_missing":
				binding, err := f.store.GetSub2Import(ctx, "fixture", f.account.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.store.MarkSub2AccountMissing(ctx, binding); err != nil {
					t.Fatal(err)
				}
			case "deleted":
				if _, err := f.db.Exec(`UPDATE accounts SET status='deleted' WHERE id=?`, f.account.ID); err != nil {
					t.Fatal(err)
				}
			case "identity":
				f.detailEmail = "another@example.test"
			case "manual_pause":
				f.detailStatus, f.schedule = "active", false
				if _, err := f.db.Exec(`UPDATE account_recovery_tasks SET failure_stage='validating' WHERE id=?`, f.task.ID); err != nil {
					t.Fatal(err)
				}
			}
			task, _, err := f.service.enqueueWithIntent(ctx, f.account.ID, true, manualRecoveryRequest(f.state(t)))
			if gate != "checkpoint" {
				if err == nil || f.loginCalls != 0 || f.applyCalls != 0 {
					t.Fatalf("manual intent bypassed %s: %v", gate, err)
				}
				return
			}
			if err != nil || task.RetryAction != "manual_resume" {
				t.Fatalf("checkpoint not reused: %+v %v", task, err)
			}
			f.service.process(task)
			if got := f.state(t); got.State != store.RecoveryCompleted || f.loginCalls != 1 || f.applyCalls != 1 {
				t.Fatalf("checkpoint replayed login/write: %+v login=%d apply=%d", got, f.loginCalls, f.applyCalls)
			}
		})
	}
}

func TestLegacyManualRecoveryCheckOnlyReportsAction(t *testing.T) {
	f := newMonitorFixture(t)
	ctx := context.Background()
	batch, _, err := f.store.CreateAccountCheckBatch(ctx, store.AccountCheckInput{
		RequestKey: "recovery-manual-once-fixture", AccountIDs: []int64{f.account.ID},
		DestinationKey: "fixture", Concurrency: 1, ProxyMode: "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	work, err := f.store.ClaimAccountCheck(ctx)
	if err != nil || work == nil || work.Batch.ID != batch.ID {
		t.Fatalf("legacy work=%+v err=%v", work, err)
	}
	f.service.checker.process(work)
	assertCheckAction(t, f, "reauthorize")
}

func TestManualHistoryFailureStopsAutomaticRecovery(t *testing.T) {
	for _, failure := range []struct {
		err        error
		code       string
		canRecover bool
	}{{login.ErrAuthConnectionReset, login.LoginErrorConnectionReset, true}, {errors.Join(login.ErrUnexpectedAuthPage, login.ErrAuthPageTimeout), login.LoginErrorUnexpectedPage, true}, {errors.New("unknown test failure"), login.LoginErrorUnknown, false}, {errors.New("OAuth test failure"), login.LoginErrorOAuth, false}} {
		for _, existingRetry := range []bool{false, true} {
			f := newRecoveryFixture(t, true)
			ctx := context.Background()
			previousHistory := loginHistory
			loginHistory = f.store
			t.Cleanup(func() { loginHistory = previousHistory })
			if existingRetry {
				if _, err := f.store.RecordAccountRecoveryFailure(ctx, f.task.ID, store.RecoveryFailure{State: store.RecoveryUnknown, Stage: store.RecoveryLoggingIn, Code: "network_error", RetryAction: "relogin", NextRetryAt: timePointer(time.Now().Add(-time.Minute))}); err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.db.Exec(`DELETE FROM account_recovery_tasks`); err != nil {
				t.Fatal(err)
			}
			_, err := runWithHistoryAccount(login.WithSingleAttempt(ctx), f.account.ID, LoginRequest{Email: f.account.Email, Password: "password"}, func(context.Context) (*login.LoginResult, error) {
				return nil, failure.err
			})
			if err == nil {
				t.Fatal("expected explicit login failure")
			}
			attempts, err := f.store.ListAttempts(ctx, f.account.Email, 1)
			if err != nil || len(attempts) != 1 || !attempts[0].SingleAttempt || attempts[0].Code != failure.code {
				t.Fatalf("manual failure not persisted with original reason: %+v %v", attempts, err)
			}
			if allowed, err := f.service.automaticRecoveryAllowed(ctx, f.account.ID); err != nil || allowed {
				t.Fatalf("manual login failure allowed auto scan: %v", err)
			}
			f.service.autoRecovery.Store(true)
			f.service.retryDueRecoveries()
			if existingRetry && (f.state(t).RetryAction != "manual" || f.state(t).NextRetryAt != nil) {
				t.Fatalf("old background retry survived manual failure: %+v", f.state(t))
			}
			if f.loginCalls != 0 {
				t.Fatal("manual history failure started another login")
			}
			if !failure.canRecover {
				// Unclassified failures require another explicit AUTH login;
				// they never gain permission to bypass recovery safety checks.
				_, err := runWithHistoryAccount(login.WithSingleAttempt(ctx), f.account.ID, LoginRequest{Email: f.account.Email, Password: "password"}, func(context.Context) (*login.LoginResult, error) {
					return &login.LoginResult{Email: f.account.Email, AccessToken: "manual-at", RefreshToken: "manual-rt"}, nil
				})
				if err != nil {
					t.Fatalf("explicit AUTH login stayed blocked after unknown error: %v", err)
				}
				continue
			}
			snapshot := store.AccountRecoveryTask{AccountID: f.account.ID}
			if existingRetry {
				snapshot = f.state(t)
			}
			task, _, err := f.service.enqueueWithIntent(ctx, f.account.ID, true, manualRecoveryRequest(snapshot))
			if !existingRetry {
				if err == nil || classifyRecoveryError(err).Code != "reauthorize" {
					t.Fatalf("local-only auth failure err=%v", err)
				}
				assertCheckAction(t, f, "reauthorize")
				continue
			}
			if err != nil {
				t.Fatalf("next explicit attempt remained blocked: %v", err)
			}
			f.service.process(task)
			got, err := f.store.GetAccountRecoveryTaskByID(ctx, task.ID)
			if err != nil || got.State != store.RecoveryCompleted || f.loginCalls != 1 {
				t.Fatalf("next explicit attempt did not complete: %+v %v", got, err)
			}
		}
	}
}

func TestManualHistoryInterruptedIntentSurvives(t *testing.T) {
	for _, single := range []bool{false, true} {
		f := newRecoveryFixture(t, true)
		ctx := context.Background()
		credentials, err := f.store.GetCredentialsByID(ctx, f.account.ID)
		if err != nil {
			t.Fatal(err)
		}
		credentials.SingleAttempt = single
		_, attempt, err := f.store.BeginAttempt(ctx, credentials)
		if err != nil {
			t.Fatal(err)
		}
		attempt.Release()
		if err := f.store.RecoverInterrupted(ctx); err != nil {
			t.Fatal(err)
		}
		attempts, err := f.store.ListAttempts(ctx, f.account.Email, 1)
		if err != nil || len(attempts) != 1 || attempts[0].Status != "interrupted" || attempts[0].SingleAttempt != single {
			t.Fatalf("interruption lost intent: %+v %v", attempts, err)
		}
		failure, err := f.service.loginAutomationFailure(ctx, f.account.ID)
		if err != nil || (failure != nil) != single || single && (!failure.RequiresAction || failure.Code != "interrupted") {
			t.Fatalf("wrong interruption policy single=%v failure=%v err=%v", single, failure, err)
		}
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func TestManualLoginSuccessSupersedesOnlyLoginStage(t *testing.T) {
	for _, tc := range []struct {
		code, stage string
		allowed     bool
	}{
		{"auth_page_timeout_exhausted", store.RecoveryLoggingIn, true},
		{login.LoginErrorPasswordRejected, store.RecoveryLoggingIn, true},
		{"auth_page_timeout_exhausted", store.RecoveryValidating, false},
		{"reauthorize", store.RecoveryValidating, true},
		{"verify_remote_refresh", store.RecoveryValidating, true},
		{"network_error", store.RecoveryApplyingCredentials, false},
		{"identity_changed", store.RecoveryLoggingIn, false},
	} {
		t.Run(tc.code+"_"+tc.stage, func(t *testing.T) {
			f := newRecoveryFixture(t, true)
			ctx := context.Background()
			f.service.recordRecoveryFailure(f.task, tc.stage, &recoveryOperationError{Code: tc.code, RequiresAction: true})
			stopped := f.state(t)
			if ok, err := f.service.successfulLoginSupersedesFailure(ctx, stopped); err != nil || ok {
				t.Fatalf("same credential version superseded failure: %v", err)
			}
			credentials, err := f.store.GetCredentialsByID(ctx, f.account.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, attempt, err := f.store.BeginAttempt(ctx, credentials)
			if err != nil {
				t.Fatal(err)
			}
			err = f.store.FinishAttempt(ctx, attempt.ID, true, &store.Result{AccessToken: "manual-at", RefreshToken: "manual-rt", ChatGPTAccountID: "workspace"}, nil)
			attempt.Release()
			if err != nil {
				t.Fatal(err)
			}
			if ok, err := f.service.successfulLoginSupersedesFailure(ctx, stopped); err != nil || ok != tc.allowed {
				t.Fatalf("wrong supersession allowed=%v want=%v err=%v", ok, tc.allowed, err)
			}
		})
	}
}

func TestPaidManualLoginSupersedesUnconfirmedPlan(t *testing.T) {
	f := newRecoveryFixture(t, true)
	ctx := context.Background()
	if _, err := f.db.Exec(`UPDATE account_recovery_tasks SET state='unknown',failure_stage=?,error_code=?,retry_action='manual',manual_action='需要核实套餐' WHERE id=?`, store.RecoveryLoginSucceeded, store.RecoveryPlanUnconfirmed, f.task.ID); err != nil {
		t.Fatal(err)
	}
	stopped, err := f.store.GetAccountRecoveryTaskByID(ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if superseded, err := f.service.successfulLoginSupersedesFailure(ctx, stopped); err != nil || superseded {
		t.Fatalf("same credential version superseded unconfirmed plan: %v, %v", superseded, err)
	}
	credentials, err := f.store.GetCredentialsByID(ctx, f.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := f.store.BeginAttempt(ctx, credentials)
	if err != nil {
		t.Fatal(err)
	}
	err = f.store.FinishAttempt(ctx, attempt.ID, true, &store.Result{AccessToken: "manual-paid-at", RefreshToken: "manual-paid-rt", ChatGPTAccountID: "workspace", PlanType: "plus"}, nil)
	attempt.Release()
	if err != nil {
		t.Fatal(err)
	}
	if superseded, err := f.service.successfulLoginSupersedesFailure(ctx, stopped); err != nil || !superseded {
		t.Fatalf("paid manual login did not supersede unconfirmed plan: %v, %v", superseded, err)
	}
	if err := f.store.UpdateOAuthResultByID(ctx, f.account.ID, store.Result{AccessToken: "refresh-at", RefreshToken: "refresh-rt"}); err != nil {
		t.Fatal(err)
	}
	if superseded, err := f.service.successfulLoginSupersedesFailure(ctx, stopped); err != nil || superseded {
		t.Fatalf("empty refresh inherited paid evidence: %v, %v", superseded, err)
	}
	if _, err := f.db.Exec(`UPDATE accounts SET sub2_policy='unknown',plan_type='' WHERE id=?`, f.account.ID); err != nil {
		t.Fatal(err)
	}
	if superseded, err := f.service.successfulLoginSupersedesFailure(ctx, stopped); err != nil || superseded {
		t.Fatalf("unknown plan superseded unconfirmed plan: %v, %v", superseded, err)
	}
}
