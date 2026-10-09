package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
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
	if err := s.FinishAttempt(ctx, attempt.ID, true, &store.Result{AccessToken: "at", RefreshToken: "rt", ChatGPTAccountID: "chatgpt-1", ExpiresAt: 123}, nil); err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	account, err := s.GetAccount(ctx, "sub2@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return s, account.ID
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
	task, err := service.ensureTask(context.Background(), accountID)
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
	if !regexp.MustCompile(`^AUTH_[0-9]{8}_sub2@example\.com$`).MatchString(importedName) {
		t.Fatalf("unexpected display name %q", importedName)
	}
}

func TestListSub2AccountStatusesReadsDetailAndDistinguishesMissing(t *testing.T) {
	history, accountID := testOAuthStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/admin/accounts/42":
			_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"id":42,"status":"active","schedulable":true,"error_message":""}}`))
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
	missing := statuses[accountID+1]
	if missing.Exists || missing.Unknown || missing.Stale || missing.Error == "" {
		t.Fatalf("missing status = %+v", missing)
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

func TestDeleteDeletedAccountVerifiesAndDeletesRemoteBinding(t *testing.T) {
	history, accountID := testOAuthStore(t)
	ctx := context.Background()
	task, _, err := history.CreateOrGetSub2Import(ctx, "test", accountID, "operation", "idempotency", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if err := history.UpdateSub2Import(ctx, task.ID, "imported", true, 42, ""); err != nil {
		t.Fatal(err)
	}
	var deleted atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case http.MethodGet + " /api/v1/admin/accounts/42":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"id": 42, "platform": "openai", "type": "oauth",
				"credentials": map[string]any{"email": "sub2@example.com", "chatgpt_account_id": "chatgpt-1"},
				"extra":       map[string]any{"kkai_auth_import": map[string]any{"source_account_id": accountID}},
			}})
		case http.MethodDelete + " /api/v1/admin/accounts/42":
			deleted.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"message": "deleted"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := &sub2ImportService{store: history, baseURL: server.URL + "/api/v1", adminAPIKey: "secret", destinationKey: "test", client: server.Client()}
	if err := service.deleteDeletedAccount(ctx, accountID); err != nil {
		t.Fatal(err)
	}
	if deleted.Load() != 1 {
		t.Fatalf("delete requests = %d, want 1", deleted.Load())
	}
	if _, err := history.GetSub2Import(ctx, "test", accountID); !errors.Is(err, store.ErrSub2ImportNotFound) {
		t.Fatalf("deleted remote binding retained: %v", err)
	}
}

func TestDeleteDeletedAccountSkipsChangedBinding(t *testing.T) {
	history, accountID := testOAuthStore(t)
	ctx := context.Background()
	task, _, err := history.CreateOrGetSub2Import(ctx, "test", accountID, "operation", "idempotency", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if err := history.UpdateSub2Import(ctx, task.ID, "imported", true, 42, ""); err != nil {
		t.Fatal(err)
	}
	var deleted atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case http.MethodGet + " /api/v1/admin/accounts/42":
			if err := history.UpdateSub2Import(ctx, task.ID, "unknown", true, 0, "changed during verification"); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"id": 42, "platform": "openai", "type": "oauth",
				"credentials": map[string]any{"email": "sub2@example.com", "chatgpt_account_id": "chatgpt-1"},
				"extra":       map[string]any{"kkai_auth_import": map[string]any{"source_account_id": accountID}},
			}})
		case http.MethodDelete + " /api/v1/admin/accounts/42":
			deleted.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"message": "deleted"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := &sub2ImportService{store: history, baseURL: server.URL + "/api/v1", adminAPIKey: "secret", destinationKey: "test", client: server.Client()}
	if err := service.deleteDeletedAccount(ctx, accountID); err == nil {
		t.Fatal("changed binding deletion unexpectedly succeeded")
	}
	if deleted.Load() != 0 {
		t.Fatalf("delete requests = %d, want 0", deleted.Load())
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
