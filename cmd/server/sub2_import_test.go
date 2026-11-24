package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func testOAuthStore(t *testing.T) (*store.Store, int64) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "accounts.db"), filepath.Join(t.TempDir(), "accounts.key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if _, err := s.UpsertCredentials(ctx, store.Credentials{Email: "sub2@example.com", Password: "password"}); err != nil {
		t.Fatal(err)
	}
	attempt, err := s.StartAttempt(ctx, "sub2@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(ctx, attempt.ID, true, &store.Result{AccessToken: "at", RefreshToken: "rt", ChatGPTAccountID: "chatgpt-1", PlanType: "plus", ExpiresAt: 123}, nil); err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	account, err := s.GetAccount(ctx, "sub2@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return s, account.ID
}

func finishFreeCleanupLogin(t *testing.T, history *store.Store) int64 {
	t.Helper()
	ctx := context.Background()
	attempt, err := history.StartAttempt(ctx, "sub2@example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Release()
	if err := history.FinishAttempt(ctx, attempt.ID, true, &store.Result{AccessToken: "free-at", RefreshToken: "free-rt", PlanType: "free", ChatGPTAccountID: "free-workspace"}, nil); err != nil {
		t.Fatal(err)
	}
	return attempt.ID
}

func TestOfficialSub2CleanupFence(t *testing.T) {
	for _, scenario := range []string{"confirmed", "payment_required", "subscription_expired", "deactivated_workspace", "official_page", "discarded", "free", "has_shadow", "already_missing", "unsupported", "delete_404_target_present", "untrusted_target_404", "no_evidence", "unbound_evidence", "stale_version", "wrong_operation", "wrong_workspace", "pause_unconfirmed", "changed_revision", "lost_delete_response", "lost_pause_response"} {
		t.Run(scenario, func(t *testing.T) {
			history, accountID := testOAuthStore(t)
			ctx := context.Background()
			if err := history.UpdateOAuthResultByID(ctx, accountID, store.Result{AccessToken: "paid-at", RefreshToken: "paid-rt", PlanType: "plus", ChatGPTAccountID: "paid-workspace"}); err != nil {
				t.Fatal(err)
			}
			binding, err := history.LinkSub2Account(ctx, "test", accountID, 42, "linked-fixture")
			if err != nil {
				t.Fatal(err)
			}
			version := binding.CredentialVersion
			if scenario == "free" {
				version = finishFreeCleanupLogin(t, history)
			} else if scenario == "official_page" {
				attempt, err := history.StartAttempt(ctx, "sub2@example.com")
				if err != nil {
					t.Fatal(err)
				}
				err = history.FinishAttempt(ctx, attempt.ID, false, nil, &store.Failure{Code: "account_unavailable", AccountStatus: "deleted_or_deactivated"})
				attempt.Release()
				if err != nil {
					t.Fatal(err)
				}
			} else if scenario == "discarded" {
				attempt, err := history.StartAttempt(ctx, "sub2@example.com")
				if err != nil {
					t.Fatal(err)
				}
				err = history.FinishAttempt(ctx, attempt.ID, false, nil, &store.Failure{Stage: "password", Code: "password_rejected"})
				attempt.Release()
				if err != nil {
					t.Fatal(err)
				}
				account, err := history.GetAccountByID(ctx, accountID)
				if err != nil || !account.IsTerminalUnavailable() || account.IsOfficialUnavailable() {
					t.Fatalf("expected local retirement, got %s: %v", account.Status, err)
				}
				if _, err := history.GetCredentialsByID(ctx, accountID); !errors.Is(err, store.ErrAccountDiscarded) {
					t.Fatalf("discarded credential read: %v", err)
				}
				if _, _, err := history.BeginAttempt(ctx, store.Credentials{Email: account.Email, Password: "fixture", ExistingAccountID: accountID}); !errors.Is(err, store.ErrAccountDiscarded) {
					t.Fatalf("discarded relogin: %v", err)
				}
			} else if scenario != "no_evidence" {
				key := binding.OfficialCleanupEvidenceKey(version)
				if scenario == "unbound_evidence" {
					key = "fixture"
				}
				event := store.LifecycleEvent{Source: "official", Status: "deleted", Code: "account_deleted", DedupeKey: key}
				if scenario == "payment_required" || scenario == "subscription_expired" || scenario == "deactivated_workspace" {
					event.Status, event.Code, event.HTTPStatus = "deactivated", scenario, http.StatusPaymentRequired
				}
				if err := history.MarkOfficialUnavailable(ctx, accountID, version, binding, event); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "stale_version" {
				version++
			}
			var paused, deleted atomic.Bool
			deleted.Store(scenario == "already_missing")
			var writes atomic.Int32
			const beforePause = "2026-11-20T00:00:00.123456Z"
			const afterPause = "2026-11-20T00:00:01.987654Z"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "GET /admin/accounts/42":
					if deleted.Load() {
						if scenario == "untrusted_target_404" {
							http.NotFound(w, r)
							return
						}
						w.WriteHeader(http.StatusNotFound)
						_, _ = w.Write([]byte(`{"code":404,"message":"account not found"}`))
						return
					}
					stamp := beforePause
					if paused.Load() {
						stamp = afterPause
						if scenario == "changed_revision" {
							stamp = "2026-11-20T00:00:02Z"
						}
					}
					operation := binding.OperationID
					if scenario == "wrong_operation" {
						operation = "different-operation"
					}
					workspace := "paid-workspace"
					if scenario == "wrong_workspace" {
						workspace = "other-workspace"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
						"id": 42, "platform": "openai", "type": "oauth", "schedulable": !paused.Load(), "updated_at": stamp,
						"credentials": map[string]any{"email": "sub2@example.com", "chatgpt_account_id": workspace},
						"extra":       map[string]any{"kkai_auth_import": map[string]any{"source_account_id": accountID, "operation_id": operation}},
					}})
				case "GET /admin/accounts":
					ids := []map[string]int{{"id": 42}}
					if scenario == "has_shadow" {
						ids = append(ids, map[string]int{"id": 51, "parent_account_id": 42})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"items": ids, "total": len(ids)}})
				case "POST /admin/accounts/42/schedulable":
					writes.Add(1)
					var request map[string]any
					if json.NewDecoder(r.Body).Decode(&request) != nil || request["schedulable"] != false {
						t.Error("expected a pause request")
					}
					if scenario == "unsupported" {
						http.NotFound(w, r)
						return
					}
					paused.Store(scenario != "pause_unconfirmed")
					if scenario == "lost_pause_response" {
						panic(http.ErrAbortHandler)
					}
					_, _ = w.Write([]byte(`{"code":0,"data":{"id":42,"schedulable":false,"updated_at":"` + afterPause + `"}}`))
				case "DELETE /admin/accounts/42":
					writes.Add(1)
					if !paused.Load() {
						t.Error("delete before verified pause")
					}
					if scenario == "delete_404_target_present" {
						w.WriteHeader(http.StatusNotFound)
						_, _ = w.Write([]byte(`{"code":404,"message":"account not found"}`))
						return
					}
					deleted.Store(true)
					if scenario == "lost_delete_response" {
						panic(http.ErrAbortHandler)
					}
					_, _ = w.Write([]byte(`{"code":0}`))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			service := &sub2ImportService{store: history, baseURL: server.URL, adminAPIKey: "fixture", destinationKey: "test", client: server.Client()}
			if scenario == "discarded" {
				if _, err := service.ensureTask(ctx, accountID); !errors.Is(err, store.ErrAccountDiscarded) {
					t.Fatalf("discarded import: %v", err)
				}
			}
			err = service.deleteDeletedAccount(ctx, accountID, version)
			if scenario == "lost_pause_response" {
				if err == nil || deleted.Load() || !paused.Load() {
					t.Fatalf("ambiguous pause advanced to delete: %v", err)
				}
				err = service.deleteDeletedAccount(ctx, accountID, version)
			}
			switch scenario {
			case "no_evidence", "unbound_evidence", "stale_version", "wrong_operation", "wrong_workspace", "already_missing":
				if writes.Load() != 0 {
					t.Fatalf("issued %d writes without ownership evidence", writes.Load())
				}
			}
			wantDelete := scenario == "confirmed" || scenario == "official_page" || scenario == "payment_required" || scenario == "subscription_expired" || scenario == "deactivated_workspace" || scenario == "discarded" || scenario == "free" || scenario == "already_missing" || scenario == "lost_delete_response" || scenario == "lost_pause_response"
			if deleted.Load() != (wantDelete || scenario == "untrusted_target_404") || (err == nil) != wantDelete {
				t.Fatalf("deleted = %t, err = %v, want delete = %t", deleted.Load(), err, wantDelete)
			}
			retained, err := history.GetSub2Import(ctx, "test", accountID)
			if err != nil || retained.ID != binding.ID || retained.Sub2AccountID != binding.Sub2AccountID || (retained.State == store.Sub2RemoteMissing) != wantDelete {
				t.Fatalf("unexpected retained binding: %+v, %v", retained, err)
			}
			switch scenario {
			case "unsupported", "delete_404_target_present", "untrusted_target_404", "pause_unconfirmed", "changed_revision":
				if retained.State != "cleanup_pending" {
					t.Fatalf("unconfirmed retirement left automatic queue: %s", retained.State)
				}
			}
			if scenario == "free" {
				_, saved, err := history.GetOAuthResultByID(ctx, accountID)
				if err != nil || saved.RefreshToken != "free-rt" {
					t.Fatal("Free credential was not retained")
				}
			}
		})
	}
}

func TestFreeCleanupRequiresLatestR1PaidObservation(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		paid string
		want bool
	}{
		{name: "plus_to_free", paid: "plus", want: true},
		{name: "team_to_free", paid: "team", want: true},
		{name: "pro_to_free", paid: "pro", want: false},
		{name: "paid_then_unknown_then_free", paid: "", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			history, accountID := testOAuthStore(t)
			if tc.paid != "" {
				if err := history.UpdateOAuthResultByID(ctx, accountID, store.Result{AccessToken: "paid-at", RefreshToken: "paid-rt", PlanType: tc.paid}); err != nil {
					t.Fatal(err)
				}
			}
			binding, err := history.LinkSub2Account(ctx, "test", accountID, 42, "linked-free-fixture")
			if err != nil {
				t.Fatal(err)
			}
			if tc.paid == "" {
				if err := history.UpdateOAuthResultByID(ctx, accountID, store.Result{AccessToken: "unknown-at", RefreshToken: "unknown-rt", PlanType: "pro"}); err != nil {
					t.Fatal(err)
				}
			}
			finishFreeCleanupLogin(t, history)
			binding, err = history.GetSub2Import(ctx, "test", accountID)
			if err != nil {
				t.Fatal(err)
			}
			got, err := history.HasFreeSub2CleanupEvidence(ctx, binding)
			if err != nil || got != tc.want {
				t.Fatalf("evidence=%t err=%v want=%t binding=%+v", got, err, tc.want, binding)
			}
		})
	}
}

func TestSub2ImportServiceStartStop(t *testing.T) {
	history, _ := testOAuthStore(t)
	service := &sub2ImportService{store: history}
	service.Start()
	done := make(chan struct{})
	go func() {
		service.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sub2 importer did not stop promptly")
	}
	// Stop is intentionally idempotent so shutdown paths can be composed.
	service.Stop()
}

func TestAccountDeactivationConfirmedRecognizesOfficialCodesOnly(t *testing.T) {
	for _, code := range []string{"account_deleted", "user_deleted", "account_deactivated", "user_deactivated", "account_disabled", "user_disabled", "payment_required", "subscription_expired", "deactivated_workspace"} {
		if !accountDeactivationConfirmed(code) || !accountDeactivationConfirmed(strings.ToUpper(code)) {
			t.Fatalf("official code %q was not recognized", code)
		}
	}
	for _, code := range []string{"deleted_or_deactivated", "account_unavailable", "access_denied", ""} {
		if accountDeactivationConfirmed(code) {
			t.Fatalf("ambiguous/non-official code %q was recognized", code)
		}
	}
}

func TestSub2ImportMissingPlanReleasesQueueAndHoldsDelivery(t *testing.T) {
	for _, tc := range []struct{ name, observed, cached, policy, reason string }{
		{"missing_plan", "", "plus", "eligible", store.RecoveryPlanUnconfirmedMessage},
		{"revoked_policy", "plus", "plus", "unknown", "当前账号未获准进入 Sub2 付费池，请核实套餐及账号策略"},
		{"unconfirmed_account_plan", "plus", "", "eligible", "当前账号未获准进入 Sub2 付费池，请核实套餐及账号策略"},
		{"free", "free", "free", "free_local", "当前账号仅保留在 AUTH 本地"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			path := filepath.Join(dir, "accounts.db")
			history, err := store.Open(path, filepath.Join(dir, "accounts.key"))
			if err != nil {
				t.Fatal(err)
			}
			defer history.Close()
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			firstAccount, firstAttempt, err := history.BeginAttempt(ctx, store.Credentials{Email: "first@example.test", Password: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			if err := history.FinishAttempt(ctx, firstAttempt.ID, true, &store.Result{PlanType: "plus"}, nil); err != nil {
				t.Fatal(err)
			}
			firstAttempt.Release()
			firstID := firstAccount.ID
			delivery, err := history.QueueAccountDelivery(ctx, firstID, "fixture", store.DefaultDeliveryOptions())
			if err != nil {
				t.Fatal(err)
			}
			first, _, err := history.CreateOrGetSub2Import(ctx, "fixture", firstID, "head", "head-key", []byte(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			secondAccount, attempt, err := history.BeginAttempt(ctx, store.Credentials{Email: "second@example.test", Password: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			if err := history.FinishAttempt(ctx, attempt.ID, true, &store.Result{PlanType: "plus"}, nil); err != nil {
				t.Fatal(err)
			}
			attempt.Release()
			second, _, err := history.CreateOrGetSub2Import(ctx, "fixture", secondAccount.ID, "next", "next-key", []byte(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE accounts SET plan_type=?,sub2_policy=? WHERE id=?`, tc.cached, tc.policy, firstID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE login_attempts SET plan_type=?, observation_source='token_refresh' WHERE id=?`, tc.observed, firstAttempt.ID); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unconfirmed import issued %s %s", r.Method, r.URL.Path)
			}))
			defer server.Close()
			importer := &sub2ImportService{store: history, baseURL: server.URL, adminAPIKey: "fixture", destinationKey: "fixture", client: server.Client()}
			importer.runOnce(ctx)
			held, err := history.GetSub2ImportByID(ctx, first.ID)
			if err != nil || held.State != "failed" || held.LastError != tc.reason {
				t.Fatalf("first task=%+v err=%v", held, err)
			}
			queue, err := history.ListQueuedSub2ImportsForDestination(ctx, "fixture", 1)
			if err != nil || len(queue) != 1 || queue[0].ID != second.ID {
				t.Fatalf("next task=%+v err=%v", queue, err)
			}
			service := newAccountDeliveryService(history, importer, nil)
			service.process(ctx, delivery)
			heldDelivery, err := history.GetAccountDelivery(ctx, delivery.ID)
			if err != nil || heldDelivery.State != "requires_action" || heldDelivery.ManualAction != tc.reason || heldDelivery.NextRetryAt != nil {
				t.Fatalf("delivery=%+v err=%v", heldDelivery, err)
			}
			held, err = history.GetSub2ImportByID(ctx, first.ID)
			if err != nil || held.State != "failed" {
				t.Fatalf("delivery requeued import: %+v err=%v", held, err)
			}
			stale := second
			stale.OperationID = "earlier-operation"
			if err := history.FailQueuedSub2Import(ctx, stale, attempt.ID, tc.reason); !errors.Is(err, store.ErrAccountRecoveryStale) {
				t.Fatalf("earlier operation changed task: %v", err)
			}
			if err := history.MarkSub2ImportSendingForOperation(ctx, second); err != nil {
				t.Fatal(err)
			}
			if err := history.FailQueuedSub2Import(ctx, second, attempt.ID, tc.reason); !errors.Is(err, store.ErrAccountRecoveryStale) {
				t.Fatalf("late worker changed in-flight task: %v", err)
			}
		})
	}
}

func TestSub2ImportPostsAndReconcilesUnGroupedAccount(t *testing.T) {
	history, accountID := testOAuthStore(t)
	var importedName string
	var operationID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "secret" {
			t.Error("missing admin API key")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case http.MethodPost + " /api/v1/admin/accounts/data":
			var body sub2DataImportRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if !body.SkipDefaultGroupBind || len(body.Data.Accounts) != 1 || body.Data.Accounts[0].Concurrency != 3 {
				t.Fatalf("unexpected import request: %+v", body)
			}
			if mode := body.Data.Accounts[0].Extra["codex_fingerprint_mode"]; mode != "full" {
				t.Fatalf("new account fingerprint mode = %v, want full", mode)
			}
			if _, exists := body.Data.Accounts[0].Extra["codex_fingerprint_seed"]; exists {
				t.Fatal("fingerprint seed must be generated by Sub2")
			}
			importedName = body.Data.Accounts[0].Name
			operationID = body.Data.Accounts[0].Extra["kkai_auth_import"].(map[string]any)["operation_id"].(string)
			_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"account_created":1,"account_failed":0}}`))
		case http.MethodGet + " /api/v1/admin/accounts":
			response := map[string]any{"code": 0, "message": "success", "data": map[string]any{"items": []any{map[string]any{
				"id": 42, "name": importedName, "group_ids": []any{}, "extra": map[string]any{"kkai_auth_import": map[string]any{"operation_id": operationID, "source_account_id": accountID}},
			}}}}
			_ = json.NewEncoder(w).Encode(response)
		case http.MethodGet + " /api/v1/admin/accounts/42":
			response := map[string]any{"code": 0, "message": "success", "data": map[string]any{
				"id": 42, "group_ids": []any{}, "extra": map[string]any{"kkai_auth_import": map[string]any{"operation_id": operationID, "source_account_id": accountID}},
			}}
			_ = json.NewEncoder(w).Encode(response)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service := &sub2ImportService{
		store: history, baseURL: server.URL + "/api/v1", adminAPIKey: "secret", destinationKey: "test", client: server.Client(),
	}
	task, err := service.ensureTask(context.Background(), accountID, store.DeliveryOptions{Priority: 1, Concurrency: 3, Merchant: "测试号商", Deadline: "2026-12-31"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.processQueued(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	got, err := history.GetSub2ImportByID(context.Background(), task.ID)
	if err != nil || got.State != "imported" || got.Sub2AccountID != 42 || importedName == "" {
		t.Fatalf("import task = %+v err=%v", got, err)
	}
	if importedName != "测试号商 | 2026-12-31 | sub2@example.com" {
		t.Fatalf("unexpected display name %q", importedName)
	}
}

func TestSub2ImportRefreshesQueuedCredentialsBeforePost(t *testing.T) {
	history, accountID := testOAuthStore(t)
	ctx := context.Background()
	var posted string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		posted = string(body)
		http.Error(w, "temporary failure", http.StatusBadGateway)
	}))
	defer server.Close()
	service := &sub2ImportService{store: history, baseURL: server.URL + "/api/v1", adminAPIKey: "secret", destinationKey: "test", client: server.Client()}
	options := store.DeliveryOptions{Priority: 1, Concurrency: 3, Merchant: "测试号商", Deadline: "2026-12-31"}
	payload, operationID, idempotencyKey, err := service.buildPayload(accountID, options)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := history.CreateOrGetSub2Import(ctx, "test", accountID, operationID, idempotencyKey, payload)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := history.StartAttempt(ctx, "sub2@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := history.FinishAttempt(ctx, attempt.ID, true, &store.Result{AccessToken: "new-at", RefreshToken: "new-rt", ChatGPTAccountID: "chatgpt-1", PlanType: "plus"}, nil); err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	if err := service.processQueued(ctx, task); err == nil {
		t.Fatal("temporary Sub2 failure unexpectedly succeeded")
	}
	if strings.Contains(posted, "\"access_token\":\"at\"") || strings.Contains(posted, "\"refresh_token\":\"rt\"") {
		t.Fatalf("stale OAuth credentials were posted: %s", posted)
	}
	if !strings.Contains(posted, "\"access_token\":\"new-at\"") || !strings.Contains(posted, "\"refresh_token\":\"new-rt\"") {
		t.Fatalf("current OAuth credentials were not posted: %s", posted)
	}
}

func TestListSub2AccountStatusesReadsDetailAndDistinguishesMissing(t *testing.T) {
	history, accountID := testOAuthStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/admin/accounts/42":
			_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"id":42,"status":"active","schedulable":true,"error_message":"","extra":{"codex_5h_used_percent":42,"codex_5h_reset_at":"2026-10-29T10:00:00Z","codex_5h_reset_after_seconds":3600,"codex_5h_window_minutes":300,"codex_7d_used_percent":68,"codex_7d_reset_at":"2026-11-04T10:00:00Z","codex_7d_window_minutes":10080,"codex_usage_updated_at":"2026-10-29T09:00:00Z","codex_credits_snapshot":{"credits":{"balance":"12","has_credits":true,"unlimited":false}}}}}`))
		case "/api/v1/admin/accounts/404":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":1,"message":"not found"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service := &sub2ImportService{
		store: history, baseURL: server.URL + "/api/v1", adminAPIKey: "secret", destinationKey: "test", client: server.Client(),
	}
	statuses := service.listSub2AccountStatuses(context.Background(), []store.Sub2Import{
		{DestinationKey: "test", AccountID: accountID, Sub2AccountID: 42, State: "imported"},
		{DestinationKey: "test", AccountID: accountID + 1, Sub2AccountID: 404, State: "imported"},
	})
	active := statuses[accountID]
	if !active.Exists || active.Unknown || active.Stale || active.Status != "active" || active.Schedulable == nil || !*active.Schedulable {
		t.Fatalf("active status = %+v", active)
	}
	if active.Quota == nil || active.Quota.FiveHour == nil || active.Quota.FiveHour.UsedPercent == nil || *active.Quota.FiveHour.UsedPercent != 42 || active.Quota.SevenDay == nil || active.Quota.Credits == nil || active.Quota.Credits.Balance != "12" {
		t.Fatalf("quota snapshot = %+v", active.Quota)
	}
	missing := statuses[accountID+1]
	if missing.Exists || missing.Unknown || missing.Stale || missing.Error == "" {
		t.Fatalf("missing status = %+v", missing)
	}
}

func TestSub2NotFoundRecordsOneLifecycleEventAndTransientFailureDoesNot(t *testing.T) {
	history, accountID := testOAuthStore(t)
	ctx := context.Background()
	if _, err := history.UpsertCredentials(ctx, store.Credentials{Email: "sub2-transient@example.com", Password: "password"}); err != nil {
		t.Fatal(err)
	}
	secondAttempt, err := history.StartAttempt(ctx, "sub2-transient@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := history.FinishAttempt(ctx, secondAttempt.ID, true, &store.Result{AccessToken: "at-2", RefreshToken: "rt-2", ChatGPTAccountID: "chatgpt-2", ExpiresAt: 123}, nil); err != nil {
		t.Fatal(err)
	}
	secondAttempt.Release()
	secondAccount, err := history.GetAccount(ctx, "sub2-transient@example.com")
	if err != nil {
		t.Fatal(err)
	}
	missingTask, _, err := history.CreateOrGetSub2Import(ctx, "test", accountID, "missing-operation", "missing-idempotency", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if err := history.UpdateSub2Import(ctx, missingTask.ID, "imported", true, 404, ""); err != nil {
		t.Fatal(err)
	}
	transientTask, _, err := history.CreateOrGetSub2Import(ctx, "test", secondAccount.ID, "transient-operation", "transient-idempotency", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if err := history.UpdateSub2Import(ctx, transientTask.ID, "imported", true, 503, ""); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/404") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":404,"message":"account not found"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/503") {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	service := &sub2ImportService{store: history, baseURL: server.URL + "/api/v1", adminAPIKey: "secret", destinationKey: "test", client: server.Client()}
	imports := []store.Sub2Import{
		{DestinationKey: "test", AccountID: accountID, Sub2AccountID: 404, State: "imported"},
		{DestinationKey: "test", AccountID: secondAccount.ID, Sub2AccountID: 503, State: "imported"},
	}
	first := service.listSub2AccountStatuses(ctx, imports)
	if first[accountID].Exists || first[accountID].Unknown || first[accountID].Stale || first[accountID].Error == "" {
		t.Fatalf("404 status = %+v", first[accountID])
	}
	// A second poll must be idempotent, while a transport/5xx failure must not
	// manufacture deletion evidence.
	service.listSub2AccountStatuses(ctx, imports[:1])
	events, err := history.ListLifecycleEventsForAccount(ctx, accountID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Source != "sub2" || events[0].Status != "deleted" || events[0].HTTPStatus != http.StatusNotFound {
		t.Fatalf("lifecycle events = %+v", events)
	}
	transient := service.listSub2AccountStatuses(ctx, []store.Sub2Import{imports[1]})[secondAccount.ID]
	if transient.Exists || !transient.Unknown || !transient.Stale || transient.Error == "" {
		t.Fatalf("503 status = %+v", transient)
	}
	events, err = history.ListLifecycleEventsForAccount(ctx, accountID, 20)
	if err != nil || len(events) != 1 {
		t.Fatalf("transient status added lifecycle evidence: events=%+v err=%v", events, err)
	}
}

func TestMergeSub2QuotaKeepsFallbackFields(t *testing.T) {
	credits := &sub2Credits{Balance: "12"}
	base := &sub2Quota{FiveHour: &sub2UsageWindow{UsedPercent: floatPtr(10)}, Credits: credits, Source: "detail"}
	fresh := &sub2Quota{FiveHour: &sub2UsageWindow{UsedPercent: floatPtr(20)}, Source: "usage", UpdatedAt: "now"}
	merged := mergeSub2Quota(base, fresh)
	if merged == nil || merged.FiveHour == nil || *merged.FiveHour.UsedPercent != 20 || merged.Credits != credits || merged.Source != "usage" || merged.UpdatedAt == nil {
		t.Fatalf("merged quota = %+v", merged)
	}
}

func floatPtr(value float64) *float64 { return &value }

func TestSub2AccountStatusCachesBriefly(t *testing.T) {
	history, _ := testOAuthStore(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"id":42,"status":"active","schedulable":true}}`))
	}))
	defer server.Close()
	service := &sub2ImportService{store: history, baseURL: server.URL + "/api/v1", adminAPIKey: "secret", destinationKey: "test", client: server.Client()}
	if got := service.sub2AccountStatus(withSub2StatusCache(context.Background()), 42, time.Now().UTC()); !got.Exists || got.Unknown {
		t.Fatalf("first status = %+v", got)
	}
	if got := service.sub2AccountStatus(withSub2StatusCache(context.Background()), 42, time.Now().UTC()); !got.Exists || got.Unknown {
		t.Fatalf("cached status = %+v", got)
	}
	if requests.Load() != 1 {
		t.Fatalf("detail requests = %d, want 1 within cache TTL", requests.Load())
	}
}

func TestSub2QuotaFromUsageMapsWindowsAndUsageMetadata(t *testing.T) {
	snapshot := sub2QuotaFromUsage(map[string]any{
		"source":     "cached",
		"updated_at": "2026-10-29T12:00:00Z",
		"five_hour":  map[string]any{"utilization": 42.5, "resets_at": "2026-10-29T14:00:00Z", "remaining_seconds": 7200, "used_requests": 17, "limit_requests": 100},
		"seven_day":  map[string]any{"utilization": 9.25, "resets_at": "2026-11-01T00:00:00Z"},
	})
	if snapshot == nil || snapshot.Source != "cached" || snapshot.UpdatedAt == nil || snapshot.FiveHour == nil || snapshot.SevenDay == nil {
		t.Fatalf("usage snapshot = %+v", snapshot)
	}
	if snapshot.FiveHour.UsedPercent == nil || *snapshot.FiveHour.UsedPercent != 42.5 || snapshot.FiveHour.UsedRequests == nil || *snapshot.FiveHour.UsedRequests != 17 {
		t.Fatalf("five-hour window = %+v", snapshot.FiveHour)
	}
}

func TestSub2AccountUsageBatchSeparatesPerAccountErrors(t *testing.T) {
	history, _ := testOAuthStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/admin/accounts/usage/batch" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			AccountIDs []int64 `json:"account_ids"`
			Force      bool    `json:"force"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.AccountIDs) != 2 || body.Force {
			t.Fatalf("unexpected usage request: %+v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"usage":  map[string]any{"42": map[string]any{"source": "cached", "five_hour": map[string]any{"utilization": 20}}},
			"errors": map[string]string{"43": "账号尚无额度快照"},
		}})
	}))
	defer server.Close()
	service := &sub2ImportService{store: history, baseURL: server.URL + "/api/v1", adminAPIKey: "secret", client: server.Client()}
	usage, perAccountErrors, err := service.sub2AccountUsageBatch(context.Background(), []int64{42, 43})
	if err != nil || usage[42] == nil || usage[42].FiveHour == nil || perAccountErrors[43] == "" {
		t.Fatalf("usage=%+v errors=%+v err=%v", usage, perAccountErrors, err)
	}
}

func TestAttachSub2UsageKeepsDetailCredits(t *testing.T) {
	history, _ := testOAuthStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/accounts/usage/batch" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"usage": map[string]any{"42": map[string]any{"source": "cached", "five_hour": map[string]any{"utilization": 20}}},
		}})
	}))
	defer server.Close()
	service := &sub2ImportService{store: history, baseURL: server.URL + "/api/v1", adminAPIKey: "secret", client: server.Client()}
	credits := &sub2Credits{Balance: "12"}
	statuses := map[int64]sub2AccountStatus{7: {Exists: true, Sub2AccountID: 42, Quota: &sub2Quota{Credits: credits, Source: "Sub2 extra"}}}
	service.attachSub2Usage(context.Background(), statuses)
	status := statuses[7]
	if status.Quota == nil || status.Quota.FiveHour == nil || status.Quota.Credits != credits || status.Quota.Credits.Balance != "12" {
		t.Fatalf("merged quota lost detail credits: %+v", status.Quota)
	}
}

func TestAttachSub2UsageDoesNotChangeVerifiedExistenceOnUsageFailure(t *testing.T) {
	history, _ := testOAuthStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	service := &sub2ImportService{store: history, baseURL: server.URL + "/api/v1", adminAPIKey: "secret", client: server.Client()}
	statuses := map[int64]sub2AccountStatus{7: {Exists: true, Sub2AccountID: 42}}
	service.attachSub2Usage(context.Background(), statuses)
	status := statuses[7]
	if !status.Exists || !status.UsageUnknown || !status.UsageStale || status.UsageError == "" {
		t.Fatalf("usage failure changed account existence or was not marked unknown: %+v", status)
	}
}

func TestListSub2AccountStatusesMarksTransportFailuresUnknown(t *testing.T) {
	history, accountID := testOAuthStore(t)
	service := &sub2ImportService{
		store: history, baseURL: "http://127.0.0.1:1/api/v1", adminAPIKey: "secret", destinationKey: "test",
		client: &http.Client{Timeout: 20 * time.Millisecond},
	}
	statuses := service.listSub2AccountStatuses(context.Background(), []store.Sub2Import{{DestinationKey: "test", AccountID: accountID, Sub2AccountID: 42, State: "imported"}})
	got := statuses[accountID]
	if got.Exists || !got.Unknown || !got.Stale || got.Error == "" {
		t.Fatalf("transport status = %+v", got)
	}
}

func officialCleanupFixture(t *testing.T) (*store.Store, store.Sub2Import) {
	t.Helper()
	history, accountID := testOAuthStore(t)
	ctx := context.Background()
	binding, err := history.LinkSub2Account(ctx, "test", accountID, 42, "linked-cleanup")
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := history.StartAttempt(ctx, "sub2@example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Release()
	if err := history.FinishAttempt(ctx, attempt.ID, false, nil, &store.Failure{Code: "account_deleted", AccountStatus: "deleted"}); err != nil {
		t.Fatal(err)
	}
	return history, binding
}

func TestDeleteDeletedAccountVerifiesAndDeletesRemoteBinding(t *testing.T) {
	testConditionalDelete(t, false, false)
}

func TestDeleteDeletedAccountRetriesTransientFailureAndFinalizesTombstone(t *testing.T) {
	testConditionalDelete(t, true, false)
}

func TestDeleteDeletedAccountHoldsAccountLeaseAcrossRemoteDelete(t *testing.T) {
	testConditionalDelete(t, false, true)
}

func testConditionalDelete(t *testing.T, transientFailure, checkLease bool) {
	t.Helper()
	history, binding := officialCleanupFixture(t)
	ctx := context.Background()
	var gets, deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /admin/accounts/42":
			if gets.Add(1) == 1 && transientFailure {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			if deletes.Load() != 0 {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"code":404,"reason":"ACCOUNT_NOT_FOUND","message":"account not found"}`)
				return
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"id":42,"platform":"openai","type":"oauth","schedulable":false,"updated_at":"2026-11-20T00:00:00Z","credentials":{"email":"sub2@example.com","chatgpt_account_id":"chatgpt-1"}}}`)
		case "GET /admin/accounts":
			_, _ = io.WriteString(w, `{"code":0,"data":{"items":[{"id":42}],"total":1}}`)
		case "DELETE /admin/accounts/42":
			if checkLease {
				lease, err := history.AcquireAccountRecovery(r.Context(), binding.AccountID)
				if err == nil {
					lease.Release()
				}
				if !errors.Is(err, store.ErrAccountBusy) {
					t.Errorf("account lease was available during remote delete: %v", err)
				}
			}
			deletes.Add(1)
			_, _ = io.WriteString(w, `{"code":0}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := &sub2ImportService{store: history, baseURL: server.URL, adminAPIKey: "fixture", destinationKey: "test", client: server.Client()}
	if transientFailure {
		err := service.deleteDeletedAccount(ctx, binding.AccountID, binding.CredentialVersion)
		pending, readErr := history.GetSub2ImportByID(ctx, binding.ID)
		if err == nil || readErr != nil || pending.State != "cleanup_pending" || deletes.Load() != 0 {
			t.Fatalf("transient read did not preserve pending cleanup: %+v, %v, %v", pending, err, readErr)
		}
	}
	if err := service.deleteDeletedAccount(ctx, binding.AccountID, binding.CredentialVersion); err != nil {
		t.Fatal(err)
	}
	retained, err := history.GetSub2ImportByID(ctx, binding.ID)
	if err != nil || retained.State != store.Sub2RemoteMissing || retained.Sub2AccountID != binding.Sub2AccountID ||
		retained.OperationID != binding.OperationID || deletes.Load() != 1 {
		t.Fatalf("unexpected retirement result: %+v, %v, deletes=%d", retained, err, deletes.Load())
	}
	account, err := history.GetAccountByID(ctx, binding.AccountID)
	if err != nil || !account.IsOfficialUnavailable() {
		t.Fatalf("official tombstone lost: %+v, %v", account, err)
	}
	if _, _, err := history.GetOAuthResultByID(ctx, binding.AccountID); !errors.Is(err, store.ErrAccountOfficialUnavailable) {
		t.Fatalf("official account OAuth read returned %v", err)
	}
}

func TestDeleteDeletedAccountKeepsBindingIfLifecycleWriteFails(t *testing.T) {
	f := newRecoveryFixture(t, false)
	ctx := context.Background()
	binding, err := f.store.GetSub2Import(ctx, "fixture", f.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`CREATE TRIGGER lifecycle_storage_failure BEFORE INSERT ON account_lifecycle_events BEGIN SELECT RAISE(ABORT,'storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := f.service.sub2.finishDeletedSub2Binding(ctx, f.account, binding, http.StatusNotFound); err == nil {
		t.Fatal("lifecycle storage failure was ignored")
	}
	retained, err := f.store.GetSub2Import(ctx, "fixture", f.account.ID)
	if err != nil || retained.Sub2AccountID != binding.Sub2AccountID || retained.State != binding.State {
		t.Fatalf("binding was lost after evidence write failed: %+v err=%v", retained, err)
	}
}

func TestPendingCleanupDoesNotDeleteAfterNewSuccessfulLogin(t *testing.T) {
	history, accountID := testOAuthStore(t)
	ctx := context.Background()
	binding, err := history.LinkSub2Account(ctx, "test", accountID, 42, "linked-free")
	if err != nil {
		t.Fatal(err)
	}
	version := finishFreeCleanupLogin(t, history)
	pending, err := history.GetSub2ImportByID(ctx, binding.ID)
	if err != nil || pending.State != "cleanup_pending" || pending.CredentialVersion != version {
		t.Fatalf("Free login did not queue cleanup: %+v, %v", pending, err)
	}
	if err := history.UpdateOAuthResultByID(ctx, accountID, store.Result{
		AccessToken: "paid-at", RefreshToken: "paid-rt", PlanType: "plus", ChatGPTAccountID: "chatgpt-1",
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("obsolete cleanup issued %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	service := &sub2ImportService{store: history, baseURL: server.URL, adminAPIKey: "fixture", destinationKey: "test", client: server.Client()}
	if err := service.deleteDeletedAccount(ctx, accountID, version); !errors.Is(err, store.ErrAccountRecoveryStale) && !errors.Is(err, store.ErrAccountRecoveryVersionChanged) {
		t.Fatalf("obsolete cleanup returned %v", err)
	}
	retained, err := history.GetSub2ImportByID(ctx, binding.ID)
	if err != nil || retained.State == store.Sub2RemoteMissing || retained.Sub2AccountID != binding.Sub2AccountID {
		t.Fatalf("original binding lost: %+v, %v", retained, err)
	}
}

func TestDeleteDeletedAccountSkipsChangedBinding(t *testing.T) {
	testCleanupIdentityConflict(t, true)
}

func TestDeleteDeletedAccountKeepsIdentityConflictPending(t *testing.T) {
	testCleanupIdentityConflict(t, false)
}

func testCleanupIdentityConflict(t *testing.T, changeBinding bool) {
	t.Helper()
	history, binding := officialCleanupFixture(t)
	ctx := context.Background()
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/admin/accounts/42" {
			t.Errorf("conflicting cleanup issued %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		reads.Add(1)
		workspace := "other-workspace"
		if changeBinding {
			workspace = "chatgpt-1"
			if err := history.UpdateSub2Import(r.Context(), binding.ID, "imported", true, 43, "changed"); err != nil {
				t.Error(err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"code":0,"data":{"id":42,"platform":"openai","type":"oauth","schedulable":false,"updated_at":"2026-11-20T00:00:00Z","credentials":{"email":"sub2@example.com","chatgpt_account_id":%q}}}`, workspace)
	}))
	defer server.Close()
	service := &sub2ImportService{store: history, baseURL: server.URL, adminAPIKey: "fixture", destinationKey: "test", client: server.Client()}
	if err := service.deleteDeletedAccount(ctx, binding.AccountID, binding.CredentialVersion); !errors.Is(err, store.ErrAccountRecoveryStale) || reads.Load() != 1 {
		t.Fatalf("expected conflict after one detail read: %v, reads=%d", err, reads.Load())
	}
	retained, err := history.GetSub2ImportByID(ctx, binding.ID)
	wantState, wantID := "cleanup_pending", binding.Sub2AccountID
	if changeBinding {
		wantState, wantID = "imported", 43
	}
	if err != nil || retained.State != wantState || retained.Sub2AccountID != wantID {
		t.Fatalf("unexpected binding after conflict: %+v, %v", retained, err)
	}
}

func TestDeleteDeletedAccountSkipsChangedCredentialVersion(t *testing.T) {
	history, binding := officialCleanupFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("stale cleanup issued %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	service := &sub2ImportService{store: history, baseURL: server.URL, adminAPIKey: "fixture", destinationKey: "test", client: server.Client()}
	if err := service.deleteDeletedAccount(context.Background(), binding.AccountID, binding.CredentialVersion+1); !errors.Is(err, store.ErrAccountRecoveryVersionChanged) {
		t.Fatalf("stale credential version returned %v", err)
	}
}

func TestListSub2AccountStatusesPropagatesRuntimeCooldowns(t *testing.T) {
	history, accountID := testOAuthStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		future := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano)
		past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
		_, _ = w.Write([]byte(`{"code":0,"data":{"id":42,"status":"active","schedulable":true,"error_message":"","rate_limited_at":"` + past + `","rate_limit_reset_at":"` + future + `","overload_until":null,"temp_unschedulable_until":null,"temp_unschedulable_reason":"","expires_at":null}}`))
	}))
	defer server.Close()
	service := &sub2ImportService{store: history, baseURL: server.URL + "/api/v1", adminAPIKey: "secret", destinationKey: "test", client: server.Client()}
	status := service.listSub2AccountStatuses(context.Background(), []store.Sub2Import{{DestinationKey: "test", AccountID: accountID, Sub2AccountID: 42, State: "imported"}})[accountID]
	if status.RateLimitedAt == nil || status.RateLimitResetAt == nil || status.EffectiveSchedulable == nil || *status.EffectiveSchedulable {
		t.Fatalf("future rate limit was not propagated/effective schedulability disabled: %+v", status)
	}
}

func TestRemoteStatusTimeAndEffectiveSchedulableExpiry(t *testing.T) {
	if parsed, ok := remoteStatusTime("2026-01-02T03:04:05.123Z"); !ok || parsed.Year() != 2026 {
		t.Fatalf("RFC3339 parse failed: %v %v", parsed, ok)
	}
	if parsed, ok := remoteStatusTime(float64(time.Now().UnixMilli())); !ok || parsed.IsZero() {
		t.Fatalf("millisecond parse failed: %v %v", parsed, ok)
	}
	if _, ok := remoteStatusTime("not-a-time"); ok {
		t.Fatal("malformed optional timestamp treated as valid")
	}
}

func TestSyncSub2AccountAssociation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		workspaces  []string
		association string
		wantLinked  bool
	}{
		{"unique_external", []string{"chatgpt-1"}, "linked", true},
		{"workspace_disambiguates", []string{"other", "chatgpt-1"}, "linked", true},
		{"ambiguous_on_next_page", []string{"chatgpt-1", "chatgpt-1"}, "ambiguous", false},
		{"wrong_workspace", []string{"other"}, "conflict", false},
		{"no_remote_account", nil, "unmatched", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			history, accountID := testOAuthStore(t)
			// A binding for another destination must not hide current discovery.
			if _, err := history.LinkSub2Account(context.Background(), "previous", accountID, 99, "linked-previous"); err != nil {
				t.Fatal(err)
			}
			remote := func(index int) map[string]any {
				return map[string]any{"id": index + 42, "name": "custom display name", "platform": "openai", "type": "oauth", "status": "error", "schedulable": false,
					"credentials": map[string]any{"email": " SUB2@EXAMPLE.COM ", "chatgpt_account_id": tc.workspaces[index]}}
			}
			mutations := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/api/v1/admin/accounts/usage/batch" {
					var body struct {
						AccountIDs []int64 `json:"account_ids"`
						Force      bool    `json:"force"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.AccountIDs) != 1 || body.Force {
						t.Errorf("unexpected read-only usage request: %+v, err=%v", body, err)
					}
					usage := make(map[string]any, len(body.AccountIDs))
					for _, id := range body.AccountIDs {
						usage[strconv.FormatInt(id, 10)] = map[string]any{"source": "cached", "five_hour": map[string]any{"utilization": 0}}
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"usage": usage}})
					return
				}
				if r.Method != http.MethodGet {
					mutations++
					t.Error("association mutated Sub2")
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/admin/accounts" {
					if r.URL.Query().Get("search") != "" || r.URL.Query().Get("platform") != "openai" || r.URL.Query().Get("type") != "oauth" {
						t.Error("identity discovery must cover all OpenAI OAuth names")
					}
					page, _ := strconv.Atoi(r.URL.Query().Get("page"))
					items := []map[string]any{}
					if page <= len(tc.workspaces) {
						items = append(items, remote(page-1))
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"items": items, "pages": len(tc.workspaces), "page": page, "total": len(tc.workspaces)}})
					return
				}
				id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/v1/admin/accounts/"))
				if id < 42 || id-42 >= len(tc.workspaces) {
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": remote(id - 42)})
			}))
			defer server.Close()
			svc := &sub2ImportService{store: history, baseURL: server.URL + "/api/v1", adminAPIKey: "secret", destinationKey: "current", client: server.Client()}
			imports, statuses, err := svc.syncAccountStatuses(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			snapshot, exists := statuses[accountID]
			if !exists || snapshot.AssociationStatus != tc.association || (len(imports) == 1) != tc.wantLinked {
				t.Fatalf("imports=%+v status=%+v", imports, snapshot)
			}
			if tc.wantLinked {
				if !snapshot.Exists || snapshot.Unknown || imports[0].DestinationKey != "current" || !strings.HasPrefix(imports[0].OperationID, "linked-") {
					t.Fatalf("invalid association %+v / %+v", imports, snapshot)
				}
				task, err := svc.ensureTask(context.Background(), accountID)
				if err != nil || task.ID != imports[0].ID || task.State != "imported" {
					t.Fatalf("import should reuse external association: %+v, %v", task, err)
				}
			} else if tc.association != "unmatched" {
				if _, err := svc.ensureTask(context.Background(), accountID); err == nil {
					t.Fatal("ambiguous or conflicting remote identity must block duplicate import")
				}
			}
			queued, err := history.ListQueuedSub2Imports(context.Background(), 100)
			if err != nil || len(queued) != 0 || mutations != 0 {
				t.Fatalf("association created import work: %+v, mutations=%d, %v", queued, mutations, err)
			}
		})
	}
}

func TestSyncSub2AccountStatusesFailureIncludesEveryAccount(t *testing.T) {
	history, accountID := testOAuthStore(t)
	second, err := history.UpsertCredentials(context.Background(), store.Credentials{Email: "other@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	service := &sub2ImportService{store: history, baseURL: "http://127.0.0.1:1/api/v1", adminAPIKey: "secret", destinationKey: "test", client: &http.Client{Timeout: 20 * time.Millisecond}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, statuses, err := service.syncAccountStatuses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{accountID, second.ID} {
		status, exists := statuses[id]
		if !exists || !status.Unknown || !status.Stale || status.Error == "" || status.Exists {
			t.Fatalf("failed status omitted or reported absent: %d, %+v", id, status)
		}
	}
}

func TestSyncSub2AccountStatusesAttachesBatchUsage(t *testing.T) {
	history, accountID := testOAuthStore(t)
	ctx := context.Background()
	task, _, err := history.CreateOrGetSub2Import(ctx, "test", accountID, "linked-operation", "idempotency", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if err := history.UpdateSub2Import(ctx, task.ID, "imported", true, 42, ""); err != nil {
		t.Fatal(err)
	}

	var usageRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case http.MethodGet + " /api/v1/admin/accounts/42":
			_, _ = w.Write([]byte(`{"code":0,"data":{"id":42,"platform":"openai","type":"oauth","status":"active","schedulable":true,"credentials":{"email":"sub2@example.com","chatgpt_account_id":"chatgpt-1"}}}`))
		case http.MethodPost + " /api/v1/admin/accounts/usage/batch":
			usageRequests.Add(1)
			var body struct {
				AccountIDs []int64 `json:"account_ids"`
				Force      bool    `json:"force"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body.AccountIDs) != 1 || body.AccountIDs[0] != 42 || body.Force {
				t.Fatalf("unexpected usage request: %+v", body)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"usage":{"42":{"source":"cached","updated_at":"2026-10-29T12:00:00Z","five_hour":{"utilization":77}}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service := &sub2ImportService{
		store: history, baseURL: server.URL + "/api/v1", adminAPIKey: "secret", destinationKey: "test", client: server.Client(),
	}
	imports, statuses, err := service.syncAccountStatuses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) != 1 || usageRequests.Load() != 1 {
		t.Fatalf("imports=%+v usage requests=%d", imports, usageRequests.Load())
	}
	status := statuses[accountID]
	if !status.Exists || status.Unknown || status.Stale || status.UsageUnknown || status.UsageStale {
		t.Fatalf("status = %+v", status)
	}
	if status.Quota == nil || status.Quota.FiveHour == nil || status.Quota.FiveHour.UsedPercent == nil || *status.Quota.FiveHour.UsedPercent != 77 {
		t.Fatalf("batch usage was not attached: %+v", status.Quota)
	}
}

func TestSub2ImportLinksExistingAccountBeforeCreatingTask(t *testing.T) {
	history, accountID := testOAuthStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("existing account must not be imported again")
		}
		detail := map[string]any{"id": 42, "platform": "openai", "type": "oauth", "credentials": map[string]any{"email": "sub2@example.com", "chatgpt_account_id": "chatgpt-1"}}
		var data any = detail
		if r.URL.Path == "/admin/accounts" {
			data = map[string]any{"items": []any{detail}, "total": 1, "pages": 1}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer server.Close()
	svc := &sub2ImportService{store: history, baseURL: server.URL, adminAPIKey: "secret", destinationKey: "test", client: server.Client()}
	task, err := svc.ensureTask(context.Background(), accountID)
	if err != nil || task.State != "imported" || task.Sub2AccountID != 42 || !strings.HasPrefix(task.OperationID, "linked-") {
		t.Fatalf("direct import did not associate: %+v, %v", task, err)
	}
	queued, err := history.ListQueuedSub2Imports(context.Background(), 100)
	if err != nil || len(queued) != 0 {
		t.Fatalf("existing account queued: %+v, %v", queued, err)
	}
}

func TestSyncSub2IncompletePaginationCannotConfirmAssociation(t *testing.T) {
	history, accountID := testOAuthStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"items":[{"id":42,"platform":"openai","type":"oauth","credentials":{"email":"sub2@example.com","chatgpt_account_id":"chatgpt-1"}}],"pages":2,"total":2}}`))
	}))
	defer server.Close()
	svc := &sub2ImportService{store: history, baseURL: server.URL, adminAPIKey: "secret", destinationKey: "test", client: server.Client()}
	imports, statuses, err := svc.syncAccountStatuses(context.Background())
	status := statuses[accountID]
	if err != nil || len(imports) != 0 || !status.Unknown || !status.Stale || status.AssociationStatus == "unmatched" || status.Exists {
		t.Fatalf("partial list falsely confirmed a binding/absence: %+v, %+v, %v", imports, status, err)
	}
}
