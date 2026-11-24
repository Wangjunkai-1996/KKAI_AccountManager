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

func newDeliveryFixture(t *testing.T) (*recoveryFixture, *accountDeliveryService, store.AccountDelivery) {
	t.Helper()
	// This fixture represents established deliveries created before binding snapshots.
	f, service, delivery := newBoundDeliveryFixture(t)
	if _, err := f.db.Exec("UPDATE account_deliveries SET original_binding_id=0,original_remote_id=0,original_binding_version=0,bound_handoff_state='' WHERE id=?", delivery.ID); err != nil {
		t.Fatal(err)
	}
	delivery, err := f.store.GetAccountDelivery(context.Background(), delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f, service, delivery
}

func newBoundDeliveryFixture(t *testing.T) (*recoveryFixture, *accountDeliveryService, store.AccountDelivery) {
	t.Helper()
	f := newRecoveryFixture(t, true)
	ctx := context.Background()
	if _, err := f.store.UpdateAccountRecoveryTask(ctx, f.task.ID, store.RecoveryCanceled, "fixture setup"); err != nil {
		t.Fatal(err)
	}
	attempt, err := f.store.StartAttempt(ctx, f.account.Email)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := f.store.FinishAttemptAndQueueDelivery(ctx, attempt.ID, &store.Result{
		AccessToken: "delivery-at", RefreshToken: "delivery-rt", ChatGPTAccountID: "new-delivery-workspace", PlanType: "plus", ExpiresAt: 2100000000,
	}, "fixture", store.DeliveryOptions{Priority: 1, Concurrency: 3, Merchant: "测试号商", Deadline: "2026-12-31"})
	attempt.Release()
	if err != nil {
		t.Fatal(err)
	}
	s := newAccountDeliveryService(f.store, f.service.sub2, f.service)
	t.Cleanup(s.Stop)
	f.service.autoRecovery.Store(false)
	return f, s, delivery
}

func TestAccountDeliveryExistingBindingUsesSavedLoginWithAutoRecoveryOff(t *testing.T) {
	f, service, delivery := newDeliveryFixture(t)
	ctx := context.Background()
	before, err := f.store.GetAccountByID(ctx, f.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	service.runOnce()
	delivery, err = f.store.GetAccountDelivery(ctx, delivery.ID)
	if err != nil || delivery.State != "verifying" || delivery.RecoveryTaskID <= 0 || f.service.autoRecovery.Load() {
		t.Fatalf("handoff with auto recovery off=%+v err=%v", delivery, err)
	}
	task, err := f.store.ClaimAccountRecoveryTask(ctx, delivery.RecoveryTaskID)
	if err != nil {
		t.Fatal(err)
	}
	f.service.process(task)
	task, err = f.store.GetAccountRecoveryTaskByID(ctx, task.ID)
	if err != nil || task.State != store.RecoveryCompleted || task.Purpose != "delivery" {
		t.Fatalf("delivery recovery=%+v err=%v", task, err)
	}
	f.mu.Lock()
	if f.loginCalls != 0 || f.refreshCalls != 0 || f.applyCalls != 1 || f.testCalls != 1 || !f.schedule {
		t.Fatalf("delivery login=%d refresh=%d apply=%d test=%d scheduled=%v", f.loginCalls, f.refreshCalls, f.applyCalls, f.testCalls, f.schedule)
	}
	for _, method := range f.methods {
		if method == "POST /admin/accounts/data" || method == "POST /admin/accounts" {
			t.Fatalf("delivery duplicated existing import: %v", f.methods)
		}
	}
	events := strings.Join(f.events, ",")
	f.mu.Unlock()
	if !strings.Contains(events, "apply,test,enable") {
		t.Fatalf("schedule enabled before real probe: %s", events)
	}
	after, err := f.store.GetAccountByID(ctx, f.account.ID)
	if err != nil || after.AttemptCount != before.AttemptCount || after.RecoveryCount != before.RecoveryCount || after.RecoveryAttemptCount != before.RecoveryAttemptCount {
		t.Fatalf("delivery counted as login/revival: before=%+v after=%+v err=%v", before, after, err)
	}
	statuses, err := listDeliveryStatuses(ctx, f.store)
	if err != nil || statuses[f.account.ID].State != "completed" {
		t.Fatalf("delivery completion not surfaced=%+v err=%v", statuses, err)
	}
}

func TestAccountDeliveryStaleVersionBlocksRemoteWrites(t *testing.T) {
	for _, scenario := range []string{"stale_before_handoff", "stale_after_handoff"} {
		t.Run(scenario, func(t *testing.T) {
			f, service, delivery := newDeliveryFixture(t)
			ctx := context.Background()
			if scenario == "stale_before_handoff" {
				if err := f.store.UpdateOAuthResultByID(ctx, f.account.ID, store.Result{PlanType: "plus", AccessToken: "newer-at", RefreshToken: "newer-rt"}); err != nil {
					t.Fatal(err)
				}
			}
			service.process(ctx, delivery)
			delivery, err := f.store.GetAccountDelivery(ctx, delivery.ID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "stale_after_handoff" {
				if delivery.RecoveryTaskID == 0 {
					t.Fatal("handoff did not create recovery task")
				}
				if err := f.store.UpdateOAuthResultByID(ctx, f.account.ID, store.Result{PlanType: "plus", AccessToken: "newer-at", RefreshToken: "newer-rt"}); err != nil {
					t.Fatal(err)
				}
				task, err := f.store.ClaimAccountRecoveryTask(ctx, delivery.RecoveryTaskID)
				if err != nil {
					t.Fatal(err)
				}
				f.service.process(task)
				statuses, err := listDeliveryStatuses(ctx, f.store)
				if err != nil || !statuses[f.account.ID].RequiresAction {
					t.Fatalf("stale handoff not surfaced=%+v err=%v", statuses, err)
				}
			} else if delivery.State != "canceled" || delivery.RecoveryTaskID != 0 {
				t.Fatalf("stale delivery not canceled=%+v", delivery)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.applyCalls != 0 || f.testCalls != 0 || f.loginCalls != 0 {
				t.Fatalf("blocked delivery did work: apply=%d test=%d login=%d", f.applyCalls, f.testCalls, f.loginCalls)
			}
		})
	}
}

func TestAccountDeliveryReconnectsCommittedHandoffAfterCrash(t *testing.T) {
	f, service, delivery := newDeliveryFixture(t)
	ctx := context.Background()
	task, _, err := f.store.CreateAccountDeliveryRecoveryTask(ctx, delivery.ID, delivery.AccountID, 901, delivery.CredentialVersion, true)
	if err != nil {
		t.Fatal(err)
	}
	// The handoff task committed but the delivery's link did not. No remote call
	// or duplicate task should be needed to repair this local checkpoint.
	service.process(ctx, delivery)
	reattached, err := f.store.GetAccountDelivery(ctx, delivery.ID)
	if err != nil || reattached.RecoveryTaskID != task.ID || reattached.State != "verifying" {
		t.Fatalf("handoff not reattached=%+v err=%v", reattached, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.methods) != 0 || f.loginCalls != 0 {
		t.Fatalf("reattach repeated external work: methods=%v login=%d", f.methods, f.loginCalls)
	}
}

func TestRunWithHistoryAutoDeliveryOptInAndHistoryStatus(t *testing.T) {
	history := newTestHistory(t)
	oldImporter := sub2Importer
	sub2Importer = &sub2ImportService{store: history, baseURL: "http://unused.invalid", adminAPIKey: "fixture-key", destinationKey: "fixture", client: &http.Client{Timeout: time.Second}}
	t.Cleanup(func() { sub2Importer = oldImporter })
	ctx := context.Background()
	request := LoginRequest{Email: "delivery-opt-in@example.test", Password: "fixture-password", AutoDeliver: true}
	result, err := runWithHistory(ctx, request, func(context.Context) (*login.LoginResult, error) {
		return &login.LoginResult{Email: request.Email, PlanType: "plus", AccessToken: "private-delivery-at", RefreshToken: "private-delivery-rt", ChatGPTAccountID: "workspace"}, nil
	})
	if err != nil || result == nil || result.AccountID == 0 {
		t.Fatalf("login delivery result=%+v err=%v", result, err)
	}
	delivery, ok := result.Delivery.(store.AccountDelivery)
	if !ok || delivery.State != "queued" || delivery.AccountID != result.AccountID {
		t.Fatalf("login did not return durable queued delivery: %#v", result.Delivery)
	}
	// Status is persisted independently of a live importer or browser tab.
	sub2Importer = nil
	response := httptest.NewRecorder()
	handleHistory()(response, httptest.NewRequest(http.MethodGet, "/api/history", nil))
	var payload historyListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || response.Code != http.StatusOK || payload.Deliveries[result.AccountID].ID != delivery.ID {
		t.Fatalf("history delivery=%d %s err=%v", response.Code, response.Body.String(), err)
	}
	if strings.Contains(response.Body.String(), "private-delivery") || strings.Contains(response.Body.String(), "fixture-password") {
		t.Fatal("history exposed delivery credentials")
	}
	request.Email, request.AutoDeliver = "login-only@example.test", false
	plain, err := runWithHistory(ctx, request, func(context.Context) (*login.LoginResult, error) {
		return &login.LoginResult{AccessToken: "plain-at", RefreshToken: "plain-rt"}, nil
	})
	if err != nil || plain == nil || plain.Delivery != nil {
		t.Fatalf("ordinary login compatibility=%+v err=%v", plain, err)
	}
	deliveries, err := history.ListLatestAccountDeliveries(ctx)
	if err != nil || len(deliveries) != 1 || deliveries[0].ID != delivery.ID {
		t.Fatalf("opt-out queued delivery=%+v err=%v", deliveries, err)
	}
	request.AutoDeliver = true
	called := false
	_, err = runWithHistory(ctx, request, func(context.Context) (*login.LoginResult, error) {
		called = true
		return nil, errors.New("should not run")
	})
	if err == nil || called {
		t.Fatal("unconfigured delivery started login")
	}
}

func TestHistoryReloginDoesNotAdoptChangedBinding(t *testing.T) {
	for _, scenario := range []string{"unbound", "appeared", "replaced", "unknown_plan"} {
		t.Run(scenario, func(t *testing.T) {
			history := newTestHistory(t)
			oldImporter := sub2Importer
			sub2Importer = &sub2ImportService{store: history, baseURL: "http://unused.invalid", adminAPIKey: "fixture", destinationKey: "fixture"}
			t.Cleanup(func() { sub2Importer = oldImporter })
			ctx := context.Background()
			const email = "history-binding@example.test"
			req := LoginRequest{Email: email, Password: "pw"}
			old, err := runWithHistory(ctx, req, func(context.Context) (*login.LoginResult, error) {
				return &login.LoginResult{Email: email, AccessToken: "old-at", RefreshToken: "old-rt", PlanType: "plus"}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			bind := func() store.Sub2Import {
				b, _, err := history.CreateOrGetSub2Import(ctx, "fixture", old.AccountID, "operation", "key", []byte("{}"))
				if err != nil {
					t.Fatal(err)
				}
				if err := history.UpdateSub2Import(ctx, b.ID, "imported", true, 42, ""); err != nil {
					t.Fatal(err)
				}
				return b
			}
			var binding store.Sub2Import
			if scenario == "replaced" || scenario == "unknown_plan" {
				binding = bind()
			}
			req.AutoDeliver = scenario == "appeared"
			got, err := runWithHistoryAccount(ctx, old.AccountID, req, func(context.Context) (*login.LoginResult, error) {
				plan := "plus"
				switch scenario {
				case "appeared":
					bind()
				case "replaced":
					if err := history.UpdateSub2Import(ctx, binding.ID, "imported", true, 43, ""); err != nil {
						t.Fatal(err)
					}
				case "unknown_plan":
					plan = ""
				}
				return &login.LoginResult{Email: email, AccessToken: "new-at", RefreshToken: "new-rt", PlanType: plan}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if status, ok := got.Delivery.(store.AccountDelivery); ok && (status.ID != 0 || status.State != "canceled") {
				t.Fatalf("unexpected delivery %v", status)
			}
			due, err := history.ListLatestAccountDeliveries(ctx, "fixture")
			if err != nil || len(due) != 0 {
				t.Fatalf("unexpected delivery count %d, err %v", len(due), err)
			}
			_, saved, err := history.GetOAuthResultByID(ctx, old.AccountID)
			if err != nil || saved.RefreshToken != "new-rt" {
				t.Fatalf("local result missing, err %v", err)
			}
		})
	}
}

func TestHistoryReloginBoundSavesOnlyToAUTH(t *testing.T) {
	f := newRecoveryFixture(t, true)
	ctx := context.Background()
	if _, err := f.store.UpdateAccountRecoveryTask(ctx, f.task.ID, store.RecoveryCanceled, "fixture setup"); err != nil {
		t.Fatal(err)
	}
	oldHistory, oldImporter := loginHistory, sub2Importer
	loginHistory, sub2Importer = f.store, f.service.sub2
	t.Cleanup(func() { loginHistory, sub2Importer = oldHistory, oldImporter })
	req := LoginRequest{Email: f.account.Email, Password: "password"}
	loginOnce := func(context.Context) (*login.LoginResult, error) {
		return &login.LoginResult{Email: f.account.Email, AccessToken: "new-at", RefreshToken: "new-rt", ChatGPTAccountID: "workspace", PlanType: "plus", ExpiresAt: 2100000000}, nil
	}
	result, err := runWithHistoryAccount(ctx, f.account.ID, req, loginOnce)
	if err != nil || result.Delivery != nil {
		t.Fatalf("ordinary history login returned a delivery or failed: %v", err)
	}
	_, saved, err := f.store.GetOAuthResultByID(ctx, f.account.ID)
	if err != nil || saved.RefreshToken != "new-rt" {
		t.Fatalf("local credentials missing: %v", err)
	}
	service := newAccountDeliveryService(f.store, f.service.sub2, f.service)
	defer service.Stop()
	service.runOnce()
	deliveries, err := f.store.ListLatestAccountDeliveries(ctx)
	if err != nil || len(deliveries) != 0 {
		t.Fatalf("ordinary history login queued delivery: %d, %v", len(deliveries), err)
	}
	latest, err := f.store.GetLatestAccountRecoveryTaskForDestination(ctx, f.account.ID, "fixture")
	if err != nil || latest.ID != f.task.ID || latest.State != store.RecoveryCanceled || len(f.methods) != 0 {
		t.Fatalf("ordinary history login started remote work: methods=%v, err=%v", f.methods, err)
	}

	req.AutoDeliver = true
	binding, err := f.store.GetSub2Import(ctx, "fixture", f.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	version, err := f.store.GetAccountCredentialVersion(ctx, f.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	req.expectedBinding = &store.HistoryBindingExpectation{Destination: "fixture", OperationID: binding.OperationID, RemoteID: binding.Sub2AccountID, CredentialVersion: version}
	result, err = runWithHistoryAccount(ctx, f.account.ID, req, loginOnce)
	if err != nil {
		t.Fatal(err)
	}
	delivery, ok := result.Delivery.(store.AccountDelivery)
	if !ok || delivery.State != "queued" || delivery.OriginalBindingID == 0 || delivery.AllowCreate {
		t.Fatal("explicit delivery did not capture the original binding")
	}
	_, err = runWithHistoryAccount(ctx, f.account.ID, req, func(context.Context) (*login.LoginResult, error) {
		t.Fatal("a duplicate request started OAuth")
		return nil, nil
	})
	if !errors.Is(err, store.ErrHistoryBindingChanged) {
		t.Fatalf("duplicate bound delivery returned %v", err)
	}
	service.runOnce()
	delivery, err = f.store.GetAccountDelivery(ctx, delivery.ID)
	if err != nil || delivery.RecoveryTaskID == 0 {
		t.Fatalf("explicit delivery did not reach recovery: %v", err)
	}
	task, err := f.store.ClaimAccountRecoveryTask(ctx, delivery.RecoveryTaskID)
	if err != nil {
		t.Fatal(err)
	}
	f.service.process(task)
	task, err = f.store.GetAccountRecoveryTaskByID(ctx, task.ID)
	if err != nil || task.State != store.RecoveryCompleted || f.applyCalls != 1 || f.testCalls != 1 || !f.schedule {
		t.Fatalf("explicit delivery state=%s apply=%d probes=%d scheduled=%v err=%v", task.State, f.applyCalls, f.testCalls, f.schedule, err)
	}
}

func TestHistoryDeliveryRejectsStaleBindingBeforeLogin(t *testing.T) {
	f := newRecoveryFixture(t, true)
	ctx := context.Background()
	oldHistory, oldImporter := loginHistory, sub2Importer
	loginHistory, sub2Importer = f.store, f.service.sub2
	t.Cleanup(func() { loginHistory, sub2Importer = oldHistory, oldImporter })
	account, err := f.store.GetAccountByID(ctx, f.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := f.store.GetSub2Import(ctx, "fixture", account.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range []string{"operation", "remote_id", "destination", "removed"} {
		expected := &store.HistoryBindingExpectation{Destination: "fixture", OperationID: binding.OperationID, RemoteID: binding.Sub2AccountID, CredentialVersion: account.CredentialVersion}
		switch changed {
		case "operation":
			expected.OperationID += "-stale"
		case "remote_id":
			expected.RemoteID++
		case "destination":
			expected.Destination = "other"
		case "removed":
			if _, err := f.db.Exec(`DELETE FROM sub2_imports WHERE id=?`, binding.ID); err != nil {
				t.Fatal(err)
			}
		}
		req := LoginRequest{Email: account.Email, Password: "fixture", AutoDeliver: true, expectedBinding: expected}
		_, err := runWithHistoryAccount(ctx, account.ID, req, func(context.Context) (*login.LoginResult, error) {
			t.Fatalf("%s started a login", changed)
			return nil, nil
		})
		current, readErr := f.store.GetAccountByID(ctx, account.ID)
		if !errors.Is(err, store.ErrHistoryBindingChanged) || readErr != nil || current.AttemptCount != account.AttemptCount {
			t.Fatalf("%s: login err=%v, attempts=%d, read err=%v", changed, err, current.AttemptCount, readErr)
		}
	}
	if len(f.methods) != 0 {
		t.Fatalf("stale requests reached Sub2: %v", f.methods)
	}
}

func TestAccountDeliveryFrozenBindingStopsBeforeRemoteWrites(t *testing.T) {
	for _, scenario := range []string{"manual_pause", "replaced_operation"} {
		t.Run(scenario, func(t *testing.T) {
			f, service, delivery := newBoundDeliveryFixture(t)
			ctx := context.Background()
			want := "identity_changed"
			if scenario == "manual_pause" {
				f.schedule, f.detailStatus, f.detailError = false, "active", ""
				want = "manual_pause"
			} else {
				// Replacing the remote import marker alone must invalidate the
				// old binding, even if the email, source ID and remote ID agree.
				f.detailOperation = "replacement"
			}
			service.runOnce()
			current, err := f.store.GetAccountDelivery(ctx, delivery.ID)
			if err != nil {
				t.Fatal(err)
			}
			task, err := f.store.ClaimAccountRecoveryTask(ctx, current.RecoveryTaskID)
			if err != nil {
				t.Fatal(err)
			}
			f.service.process(task)
			got, err := f.store.GetAccountRecoveryTaskByID(ctx, task.ID)
			if err != nil || got.ErrorCode != want || len(f.events) != 0 {
				t.Fatalf("code=%s events=%v err=%v", got.ErrorCode, f.events, err)
			}
		})
	}
}

func TestAccountDeliveryFrozenBindingDoesNotReplayUnconfirmedApply(t *testing.T) {
	f, service, delivery := newBoundDeliveryFixture(t)
	ctx := context.Background()
	f.detailStatus, f.detailError, f.applyFailsOnce = "active", "", true
	service.runOnce()
	delivery, err := f.store.GetAccountDelivery(ctx, delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.task, err = f.store.ClaimAccountRecoveryTask(ctx, delivery.RecoveryTaskID)
	if err != nil {
		t.Fatal(err)
	}
	f.service.process(f.task)
	recoveryRetryDue(t, f)
	f.service.retryDueRecoveries()
	task := f.state(t)
	if task.ErrorCode != "checkpoint_unconfirmed" || !task.RequiresAction || task.NextRetryAt != nil ||
		f.applyCalls != 1 || f.testCalls != 0 || f.schedule {
		t.Fatalf("unconfirmed apply was not stopped: state=%s code=%s apply=%d probes=%d scheduled=%v", task.State, task.ErrorCode, f.applyCalls, f.testCalls, f.schedule)
	}
}

func TestAccountDeliveryResumesInterruptedApply(t *testing.T) {
	f, service, delivery := newDeliveryFixture(t)
	f.detailStatus, f.detailError, f.applyFailsOnce = "active", "", true
	service.runOnce()
	delivery, err := f.store.GetAccountDelivery(context.Background(), delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.task, err = f.store.ClaimAccountRecoveryTask(context.Background(), delivery.RecoveryTaskID)
	if err != nil {
		t.Fatal(err)
	}
	f.service.process(f.task)
	if got := f.state(t); got.RetryAction != "resume" || got.NextRetryAt == nil || f.schedule || f.loginCalls != 0 {
		t.Fatalf("apply failure=%+v", got)
	}
	recoveryRetryDue(t, f)
	f.service.retryDueRecoveries()
	runQueuedRecovery(t, f)
	if got := f.state(t); got.State != store.RecoveryCompleted || f.loginCalls != 0 || !f.schedule {
		t.Fatalf("delivery stalled=%+v", got)
	}
}

func TestAccountDeliveryStorageFailureStopsBeforeRemoteWork(t *testing.T) {
	f, service, delivery := newDeliveryFixture(t)
	if _, err := f.db.Exec(`CREATE TRIGGER delivery_state_failure BEFORE UPDATE OF state ON account_deliveries BEGIN SELECT RAISE(ABORT,'fixture storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	service.process(context.Background(), delivery)
	if !f.service.paused.Load() || len(f.methods) != 0 {
		t.Fatalf("work after storage failure: paused=%v methods=%v", f.service.paused.Load(), f.methods)
	}
}

func TestSub2RecoveryAdoptsLegacyCheckpoint(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.service.autoRecovery.Store(true)
	f.probeStatus = http.StatusServiceUnavailable
	f.service.process(f.task)
	if _, err := f.db.Exec(`UPDATE account_recovery_tasks SET retry_action='',next_retry_at=NULL,failure_stage='',error_code='' WHERE id=?`, f.task.ID); err != nil {
		t.Fatal(err)
	}
	f.probeStatus = 0
	f.service.retryDueRecoveries()
	if got := f.state(t); got.RetryAction != "resume" || got.NextRetryAt == nil || got.RequiresAction {
		t.Fatalf("legacy checkpoint not adopted=%+v", got)
	}
	recoveryRetryDue(t, f)
	f.service.retryDueRecoveries()
	runQueuedRecovery(t, f)
	if got := f.state(t); got.State != store.RecoveryCompleted || f.loginCalls != 1 || f.applyCalls != 1 {
		t.Fatalf("legacy checkpoint repeated work=%+v", got)
	}
}

func TestAccountDeliveryImportUncertainOutcomeOnlyReconciles(t *testing.T) {
	history, accountID := testOAuthStore(t)
	ctx := context.Background()
	account, err := history.GetAccountByID(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := history.StartAttempt(ctx, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := history.FinishAttemptAndQueueDelivery(ctx, attempt.ID, &store.Result{PlanType: "plus", AccessToken: "delivery-at", RefreshToken: "delivery-rt", ChatGPTAccountID: "workspace"}, "fixture", store.DeliveryOptions{Priority: 1, Concurrency: 3, Merchant: "测试号商", Deadline: "2026-12-31"})
	attempt.Release()
	if err != nil {
		t.Fatal(err)
	}
	posts, gets := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		gets++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"items":[],"total":0,"pages":1}}`))
	}))
	defer server.Close()
	importer := &sub2ImportService{store: history, baseURL: server.URL, adminAPIKey: "fixture", destinationKey: "fixture", client: server.Client()}
	recovery := newSub2RecoveryService(history, nil, importer)
	defer recovery.Stop()
	service := newAccountDeliveryService(history, importer, recovery)
	defer service.Stop()
	service.process(ctx, delivery)
	first, err := history.GetAccountDelivery(ctx, delivery.ID)
	if err != nil || first.State != "retry_wait" || first.RequiresAction || first.NextRetryAt == nil {
		t.Fatalf("uncertain result=%+v err=%v", first, err)
	}
	service.process(ctx, first)
	if posts != 1 || gets < 2 {
		t.Fatalf("uncertain import replayed: posts=%d gets=%d", posts, gets)
	}
	binding, err := history.GetSub2Import(ctx, "fixture", accountID)
	if err != nil || binding.State != "unknown" {
		t.Fatalf("uncertainty lost=%+v err=%v", binding, err)
	}
}

func TestAccountDeliveryManagementErrorClassification(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			history, _ := testOAuthStore(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "700")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("private-upstream-secret"))
			}))
			defer server.Close()
			importer := &sub2ImportService{store: history, baseURL: server.URL, adminAPIKey: "fixture", destinationKey: "fixture", client: server.Client()}
			_, err := importer.listOAuthAccounts(context.Background())
			failure := classifyRecoveryError(err)
			if failure.HTTPStatus != status || failure.RequiresAction != (status == 401) || failure.RetryAfterSeconds != 700 || strings.Contains(failure.Error(), "secret") {
				t.Fatalf("classification=%+v", failure)
			}
		})
	}
}

func TestSub2RecoveryRechecksLegacyIdentityRules(t *testing.T) {
	f := newRecoveryFixture(t, false)
	f.service.autoRecovery.Store(true)
	if _, err := f.db.Exec(`UPDATE accounts SET status='failed',last_error_code='identity_mismatch' WHERE id=?`, f.account.ID); err != nil {
		t.Fatal(err)
	}
	// Historical tasks predate structured retry actions and failure stages.
	if _, err := f.db.Exec(`UPDATE account_recovery_tasks SET retry_action='',failure_stage='',error_code='' WHERE id=?`, f.task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.UpdateAccountRecoveryTask(context.Background(), f.task.ID, store.RecoveryUnknown, "old workspace check"); err != nil {
		t.Fatal(err)
	}
	f.service.retryDueRecoveries()
	if got := f.state(t); got.RetryAction != "relogin" || got.RequiresAction || got.NextRetryAt == nil {
		t.Fatalf("obsolete identity rule retained=%+v", got)
	}
	recoveryRetryDue(t, f)
	f.service.retryDueRecoveries()
	runQueuedRecovery(t, f)
	if got := f.state(t); got.State != store.RecoveryCompleted || f.loginCalls != 1 || !f.schedule {
		t.Fatalf("legacy identity retry failed=%+v", got)
	}
}
