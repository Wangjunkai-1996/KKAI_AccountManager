package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func TestCredentialRepairUsesNewCredentialsAndQueuesDelivery(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.service.process(f.task)
	password := "corrected-password"
	if _, err := f.store.PatchAccountCredentials(context.Background(), f.account.ID, store.CredentialPatch{Password: &password}, true); err != nil {
		t.Fatal(err)
	}
	f.service.loginWithProxies = func(_ context.Context, email, gotPassword, _, _, _ string) (*login.LoginResult, error) {
		f.loginCalls++
		if gotPassword != password {
			t.Fatal("worker did not use saved corrected password")
		}
		return &login.LoginResult{Email: email, AccessToken: "repaired-at", RefreshToken: "repaired-rt", ChatGPTAccountID: "workspace"}, nil
	}
	if !f.service.processNextCredentialRepair() {
		t.Fatal("durable edit not processed")
	}
	repair, err := f.store.GetCredentialRepair(context.Background(), f.account.ID)
	deliveries, deliveryErr := f.store.ListLatestAccountDeliveries(context.Background())
	if err != nil || repair.State != "completed" || deliveryErr != nil || len(deliveries) != 1 || deliveries[0].State != "queued" || deliveries[0].CredentialVersion != repair.AttemptID || f.loginCalls != 2 {
		t.Fatalf("repair did not persist successful delivery handoff: %+v, deliveries=%+v errs=%v/%v", repair, deliveries, err, deliveryErr)
	}
	// Simulate a crash after login/delivery commit but before marking the edit
	// complete: the exact successful attempt must not log in twice.
	if _, err := f.db.Exec(`UPDATE account_credential_repairs SET state='queued',next_retry_at=0 WHERE account_id=?`, f.account.ID); err != nil {
		t.Fatal(err)
	}
	f.service.processNextCredentialRepair()
	if f.loginCalls != 2 {
		t.Fatal("restart repeated successful repair login")
	}
}

func TestCredentialRepairResumesFailedRecoveryWithNewPassword(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.service.loginWithProxies = func(context.Context, string, string, string, string, string) (*login.LoginResult, error) {
		return nil, &recoveryOperationError{Code: "login_required", RequiresAction: true}
	}
	f.service.process(f.task)
	password := "corrected-password"
	if _, err := f.store.PatchAccountCredentials(context.Background(), f.account.ID, store.CredentialPatch{Password: &password}, true); err != nil {
		t.Fatal(err)
	}
	f.service.processNextCredentialRepair()
	queued := f.state(t)
	if queued.State != store.RecoveryQueued || queued.RetryAction != "relogin" {
		t.Fatalf("fixed credentials did not resume original task: %+v", queued)
	}
	f.service.loginWithProxies = func(_ context.Context, email, gotPassword, _, _, _ string) (*login.LoginResult, error) {
		if gotPassword != password {
			t.Fatal("recovery used old password")
		}
		return &login.LoginResult{Email: email, AccessToken: "repaired-at", RefreshToken: "repaired-rt", ChatGPTAccountID: "workspace"}, nil
	}
	f.service.process(queued)
	if f.state(t).State != store.RecoveryCompleted || !f.schedule {
		t.Fatalf("fixed task did not automatically finish: %+v", f.state(t))
	}
}

func TestCredentialRepairHonorsLatestVersionAndPause(t *testing.T) {
	for _, action := range []string{"new_login", "pause", "busy"} {
		t.Run(action, func(t *testing.T) {
			f := newRecoveryFixture(t, true)
			if action != "busy" {
				f.service.process(f.task)
			}
			password := "corrected-password"
			_, err := f.store.PatchAccountCredentials(context.Background(), f.account.ID, store.CredentialPatch{Password: &password}, true)
			if action == "busy" {
				if err != store.ErrAccountBusy {
					t.Fatalf("queued recovery permitted edit: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if action == "new_login" {
				if err := f.store.UpdateOAuthResultByID(context.Background(), f.account.ID, store.Result{AccessToken: "newer-at", RefreshToken: "newer-rt"}); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := f.store.UpdateAccountRecoveryTask(context.Background(), f.task.ID, store.RecoveryCanceled, "paused"); err != nil {
					t.Fatal(err)
				}
			}
			f.service.processNextCredentialRepair()
			repair, _ := f.store.GetCredentialRepair(context.Background(), f.account.ID)
			if repair.State != "canceled" || f.loginCalls != 1 {
				t.Fatalf("repair overrode version/pause: %+v login=%d", repair, f.loginCalls)
			}
		})
	}
}

func TestHistoryCredentialPatchValidationAndSecrets(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.service.process(f.task)
	oldHistory, oldRecovery := loginHistory, accountRecoveryService
	loginHistory, accountRecoveryService = f.store, f.service
	t.Cleanup(func() { loginHistory, accountRecoveryService = oldHistory, oldRecovery })
	for _, body := range []string{`{}`, `{"password":""}`, `{"email":"other@example.test","password":"valid-password"}`, `{"totp_secret":"","clear_totp":true}`, `{"proxy":"ftp://unsafe"}`} {
		r := httptest.NewRequest(http.MethodPatch, "/api/history/1/credentials", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handleHistoryDelete()(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid edit accepted: %s => %d", body, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodPatch, "/api/history/1/credentials", strings.NewReader(`{"password":" secret-password ","clear_totp":true,"proxy":"direct"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleHistoryDelete()(w, r)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "secret-password") || !strings.Contains(w.Body.String(), `"queued"`) {
		t.Fatalf("safe patch response: %d %s", w.Code, w.Body.String())
	}
	credentials, _ := f.store.GetCredentialsByID(context.Background(), f.account.ID)
	if credentials.Password != " secret-password " || credentials.TOTPSecret != "" || credentials.Proxy != "direct" {
		t.Fatal("patch changed password whitespace or failed explicit clear")
	}
	check, _ := f.store.ClaimCredentialRepair(context.Background(), time.Now())
	if check == nil {
		t.Fatal("request returned without durable continuation")
	}
}

func TestCredentialRepairTemporaryLoginFailureRetriesWithoutManualAlert(t *testing.T) {
	f := newRecoveryFixture(t, true)
	// A first login failure has no existing recovery or delivery to resume.
	if _, err := f.db.Exec(`DELETE FROM account_recovery_tasks WHERE id=?`, f.task.ID); err != nil {
		t.Fatal(err)
	}
	password := "corrected-password"
	if _, err := f.store.PatchAccountCredentials(context.Background(), f.account.ID, store.CredentialPatch{Password: &password}, true); err != nil {
		t.Fatal(err)
	}
	f.service.loginWithProxies = func(context.Context, string, string, string, string, string) (*login.LoginResult, error) {
		return nil, &recoveryOperationError{Code: "rate_limited", HTTPStatus: 429, RetryAfterSeconds: 600}
	}
	f.service.processNextCredentialRepair()
	repair, err := f.store.GetCredentialRepair(context.Background(), f.account.ID)
	if err != nil || repair.State != "retry_wait" || repair.NextRetryAt == nil || time.Until(*repair.NextRetryAt) < 9*time.Minute || repair.RequiresAction {
		t.Fatalf("temporary login incorrectly required a person: %+v, %v", repair, err)
	}
	if _, err := f.db.Exec(`UPDATE account_credential_repairs SET next_retry_at=0 WHERE account_id=?`, f.account.ID); err != nil {
		t.Fatal(err)
	}
	f.service.loginWithProxies = func(_ context.Context, email, gotPassword, _, _, _ string) (*login.LoginResult, error) {
		if gotPassword != password {
			t.Fatal("retry lost corrected password")
		}
		return &login.LoginResult{Email: email, AccessToken: "corrected-at", RefreshToken: "corrected-rt", ChatGPTAccountID: "workspace"}, nil
	}
	f.service.processNextCredentialRepair()
	repair, _ = f.store.GetCredentialRepair(context.Background(), f.account.ID)
	if repair.State != "completed" {
		t.Fatalf("retry did not finish: %+v", repair)
	}
}

func TestCredentialRepairBindingReadTimeoutDoesNotRequireManualAction(t *testing.T) {
	f := newRecoveryFixture(t, true)
	if _, err := f.store.RecordAccountRecoveryFailure(context.Background(), f.task.ID, store.RecoveryFailure{State: store.RecoveryFailed, Stage: store.RecoveryLoggingIn, Code: "login_required", RetryAction: "manual", ManualAction: "更新资料"}); err != nil {
		t.Fatal(err)
	}
	password := "corrected-password"
	if _, err := f.store.PatchAccountCredentials(context.Background(), f.account.ID, store.CredentialPatch{Password: &password}, true); err != nil {
		t.Fatal(err)
	}
	repair, err := f.store.ClaimCredentialRepair(context.Background(), time.Now())
	if err != nil || repair == nil {
		t.Fatalf("claim: %+v %v", repair, err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	f.service.resumeCredentialRepairRecovery(ctx, *repair, f.state(t), f.account)
	updated, _ := f.store.GetCredentialRepair(context.Background(), f.account.ID)
	if updated.State != "retry_wait" || updated.RequiresAction || updated.NextRetryAt == nil || strings.Contains(updated.LastError, "身份") {
		t.Fatalf("temporary binding read became identity conflict: %+v", updated)
	}
}
