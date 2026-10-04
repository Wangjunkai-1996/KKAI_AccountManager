package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

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
}
