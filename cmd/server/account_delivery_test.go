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
	if _, err := f.db.Exec("UPDATE account_deliveries SET original_binding_id=0,original_remote_id=0,original_binding_version=0 WHERE id=?", delivery.ID); err != nil {
		t.Fatal(err)
	}
	delivery, err = f.store.GetAccountDelivery(ctx, delivery.ID)
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

func TestAccountDeliveryBoundHistoryReloginReachesSub2(t *testing.T) {
	for _, scenario := range []string{"auth_error", "operator_pause", "linked_without_marker", "official_deleted", "stale_receipt", "ordinary_401", "ordinary_502", "recheck_binding_changed", "recheck_parent"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRecoveryFixture(t, false)
			ctx := context.Background()
			manualPause := scenario == "operator_pause"
			importOperation := "operation"
			extra := map[string]any{"kkai_auth_import": map[string]any{"source_account_id": f.account.ID, "operation_id": importOperation}}
			if scenario == "linked_without_marker" {
				importOperation = "linked-fixture"
				extra = map[string]any{}
				if _, err := f.db.Exec("UPDATE sub2_imports SET operation_id=? WHERE account_id=?", importOperation, f.account.ID); err != nil {
					t.Fatal(err)
				}
			}
			if manualPause {
				f.detailStatus, f.detailError = "active", ""
			} else {
				f.detailError = "Token revoked (401): fixture"
			}
			revision := time.Now().UTC()
			var receipt lifecycleOAuthReceipt
			endpoint := "/admin/accounts/901/lifecycle-oauth"
			var phases []string
			reconciled := false
			mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/admin/accounts/901" && r.Method == http.MethodGet {
					f.methods = append(f.methods, r.Method+" "+r.URL.Path)
					writeRecoveryEnvelope(w, map[string]any{
						"id": 901, "platform": "openai", "type": "oauth", "status": f.detailStatus,
						"error_message": f.detailError, "schedulable": f.schedule, "updated_at": revision,
						"credentials": map[string]any{"email": f.account.Email, "chatgpt_account_id": "workspace"},
						"extra":       extra,
					})
					return
				}
				if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, endpoint+"/") {
					if receipt.Phase == "" {
						writeRecoveryError(w, http.StatusNotFound, "LIFECYCLE_RECEIPT_MISSING", "Operation receipt not found")
					} else if r.URL.Path == endpoint+"/"+receipt.OperationID {
						reconciled = true
						current := receipt
						if scenario == "stale_receipt" && f.testCalls > 0 {
							current.CredentialRevision++
						}
						writeRecoveryEnvelope(w, current)
					} else {
						t.Errorf("wrong operation receipt path: %s", r.URL.Path)
						w.WriteHeader(http.StatusConflict)
					}
					return
				}
				var body map[string]any
				if r.Method != http.MethodPost || r.URL.Path != endpoint || json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				operation, _ := body["operation_id"].(string)
				version, _ := body["credential_revision"].(float64)
				phase, _ := body["phase"].(string)
				if receipt.Phase == "enable" && phase == "probe" &&
					body["expected_updated_at"] == revision.Format(time.RFC3339Nano) &&
					operation == receipt.OperationID && int64(version) == receipt.CredentialRevision {
					f.testCalls++
					writeRecoveryEnvelope(w, receipt)
					return
				}
				expected := []string{"pause", "apply", "probe", "enable"}
				if len(phases) >= len(expected) || phase != expected[len(phases)] ||
					body["expected_updated_at"] != revision.Format(time.RFC3339Nano) ||
					body["import_operation_id"] != importOperation || body["source_account_id"] != float64(f.account.ID) ||
					body["expected_email"] != f.account.Email || body["expected_workspace"] != "workspace" ||
					operation == "" || version <= 0 || receipt.Phase != "" && (operation != receipt.OperationID || int64(version) != receipt.CredentialRevision) {
					t.Errorf("incorrect lifecycle preconditions for %s", phase)
					w.WriteHeader(http.StatusConflict)
					return
				}
				phases = append(phases, phase)
				switch phase {
				case "pause":
					receipt.MayEnable = body["explicit_enable"] == true
				case "apply":
					f.applyCalls++
					f.applyBody = body
				case "probe":
					f.testCalls++
					if scenario == "official_deleted" || scenario == "stale_receipt" || scenario == "ordinary_401" || scenario == "ordinary_502" {
						metadata := map[string]string{}
						if scenario != "ordinary_502" {
							metadata["upstream_status"] = "401"
						}
						if scenario == "official_deleted" || scenario == "stale_receipt" {
							metadata["upstream_code"] = "account_deleted"
						}
						w.WriteHeader(http.StatusBadGateway)
						_ = json.NewEncoder(w).Encode(map[string]any{"reason": "LIFECYCLE_PROBE_FAILED", "metadata": metadata})
						return
					}
					f.detailStatus = "active"
				case "enable":
					f.schedule = true
				}
				if phase != "probe" {
					revision = revision.Add(time.Second)
				}
				receipt.ID, receipt.OperationID, receipt.SourceAccountID = 901, operation, f.account.ID
				receipt.ImportOperationID, receipt.CredentialRevision = importOperation, int64(version)
				receipt.Phase, receipt.AccountRevision, receipt.Schedulable = phase, revision, f.schedule
				if phase == "apply" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				writeRecoveryEnvelope(w, receipt)
			}))
			defer mock.Close()
			f.service.sub2.baseURL, f.service.sub2.client = mock.URL, mock.Client()
			if _, err := f.store.UpdateAccountRecoveryTask(ctx, f.task.ID, store.RecoveryCanceled, "fixture setup"); err != nil {
				t.Fatal(err)
			}
			oldHistory, oldImporter := loginHistory, sub2Importer
			loginHistory, sub2Importer = f.store, f.service.sub2
			t.Cleanup(func() { loginHistory, sub2Importer = oldHistory, oldImporter })
			result, err := runWithHistoryAccount(ctx, f.account.ID, LoginRequest{Email: f.account.Email, Password: "password"}, func(context.Context) (*login.LoginResult, error) {
				return &login.LoginResult{Email: f.account.Email, AccessToken: "relogin-at", RefreshToken: "relogin-rt", ChatGPTAccountID: "workspace", PlanType: "plus", ExpiresAt: 2100000000}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			delivery, ok := result.Delivery.(store.AccountDelivery)
			if !ok || delivery.AllowCreate || delivery.OriginalBindingID == 0 {
				t.Fatalf("missing bound update-only delivery: %#v", result.Delivery)
			}
			service := newAccountDeliveryService(f.store, f.service.sub2, f.service)
			defer service.Stop()
			service.runOnce()
			current, err := f.store.GetAccountDelivery(ctx, delivery.ID)
			if err != nil || current.RecoveryTaskID == 0 {
				t.Fatalf("handoff missing: %+v, %v", current, err)
			}
			task, err := f.store.ClaimAccountRecoveryTask(ctx, current.RecoveryTaskID)
			if err != nil {
				t.Fatal(err)
			}
			f.service.process(task)
			f.task = task
			finished := f.state(t)
			if scenario == "official_deleted" || scenario == "stale_receipt" || scenario == "ordinary_401" || scenario == "ordinary_502" {
				account, err := f.store.GetAccountByID(ctx, f.account.ID)
				if err != nil {
					t.Fatal(err)
				}
				binding, err := f.store.GetSub2Import(ctx, "fixture", account.ID)
				if err != nil {
					t.Fatal(err)
				}
				official := scenario == "official_deleted"
				if account.IsOfficialUnavailable() != official || (binding.State == "cleanup_pending") != official ||
					official && binding.CredentialVersion != delivery.CredentialVersion || f.schedule || f.testCalls != 1 || len(phases) != 3 {
					t.Fatalf("unexpected probe outcome: official=%v binding_state=%s phases=%v task=%s", account.IsOfficialUnavailable(), binding.State, phases, finished.State)
				}
				return
			}
			credentials, _ := f.applyBody["credentials"].(map[string]any)
			if finished.State != store.RecoveryCompleted || credentials["refresh_token"] != "relogin-rt" ||
				f.schedule == manualPause || f.applyCalls != 1 || f.testCalls != 1 || f.loginCalls != 0 || !reconciled {
				t.Fatalf("update failed: state=%s code=%s schedule=%v events=%v", finished.State, finished.ErrorCode, f.schedule, f.events)
			}
			if !manualPause && f.methods[len(f.methods)-1] != "GET /admin/accounts/901" {
				t.Fatalf("missing scheduling readback: %v", f.methods)
			}
			if scenario == "recheck_parent" {
				forceDueRecheck(t, f)
				if _, err := f.store.ClaimAccountRecoveryRecheckForDestination(ctx, time.Now(), "fixture"); err != nil {
					t.Fatal(err)
				}
				child, err := f.store.QueueAccountRecoveryFromRecheck(ctx, task.ID)
				if err != nil {
					t.Fatal(err)
				}
				detail, err := f.service.sub2Account(ctx, task.Sub2AccountID)
				if err != nil || !f.service.delayedRecheckRetryOwned(ctx, child, detail) {
					t.Fatalf("parent receipt was not accepted: %v", err)
				}
				receipt.CredentialRevision++
				if f.service.delayedRecheckRetryOwned(ctx, child, detail) {
					t.Fatal("child accepted a changed parent credential revision")
				}
			}
			if scenario == "auth_error" || scenario == "recheck_binding_changed" {
				forceDueRecheck(t, f)
				if !f.service.processNextRecheck() {
					t.Fatal("first lifecycle recheck was not processed")
				}
				if check := currentRecheck(t, f); check.Round != 2 || check.State != "pending" {
					t.Fatalf("first lifecycle recheck: %+v", check)
				}
				if scenario == "recheck_binding_changed" {
					if _, err := f.db.Exec("UPDATE sub2_imports SET operation_id='replacement' WHERE account_id=?", f.account.ID); err != nil {
						t.Fatal(err)
					}
					f.schedule = false
				}
				forceDueRecheck(t, f)
				if !f.service.processNextRecheck() {
					t.Fatal("second lifecycle recheck was not processed")
				}
				check := currentRecheck(t, f)
				if scenario == "auth_error" {
					if check.State != "passed" || f.testCalls != 3 || len(phases) != 4 || !f.schedule {
						t.Fatalf("lifecycle rechecks failed: state=%s probes=%d phases=%v schedule=%v", check.State, f.testCalls, phases, f.schedule)
					}
				} else if check.State != "requires_action" || f.testCalls != 2 || len(phases) != 4 || f.schedule {
					t.Fatalf("changed binding accepted: state=%s probes=%d phases=%v schedule=%v", check.State, f.testCalls, phases, f.schedule)
				}
			}
		})
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
