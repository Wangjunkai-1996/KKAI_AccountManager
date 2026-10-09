package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/probe"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func accountCheckFixture(t *testing.T, count int) (*store.Store, []int64) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "accounts.db"), filepath.Join(dir, "accounts.key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ids := make([]int64, 0, count)
	ctx := context.Background()
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("fixture-%d", i)
		a, err := s.UpsertCredentials(ctx, store.Credentials{Email: name + "@example.test", Password: "fixture-password"})
		if err != nil {
			t.Fatal(err)
		}
		attempt, err := s.StartAttempt(ctx, a.Email)
		if err != nil {
			t.Fatal(err)
		}
		err = s.FinishAttempt(ctx, attempt.ID, true, &store.Result{AccessToken: "fixture-at", RefreshToken: "fixture-rt", ChatGPTAccountID: name}, nil)
		attempt.Release()
		if err != nil {
			t.Fatal(err)
		}
		imported, _, err := s.CreateOrGetSub2Import(ctx, "fixture", a.ID, name, name, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateSub2Import(ctx, imported.ID, "imported", true, 100+a.ID, ""); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
	}
	return s, ids
}

func callCheckAPI(handler http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	corsMiddleware(handler)(w, r)
	return w
}

func waitCheckBatch(t *testing.T, s *store.Store, id string) (store.AccountCheckBatch, []store.AccountCheck) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		b, items, err := s.GetAccountCheckBatch(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if b.State == "completed" || b.State == "stopped" {
			return b, items
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("batch did not reach terminal state")
	return store.AccountCheckBatch{}, nil
}

func TestAccountCheckAPIValidationIdempotency(t *testing.T) {
	history, ids := accountCheckFixture(t, 1)
	s := newAccountCheckService(history, "http://private-user:private-password@127.0.0.1:1234", "")
	defer s.Stop()
	body := fmt.Sprintf(`{"request_key":"fixture-request","account_ids":[%d],"concurrency":2,"proxy_mode":"default"}`, ids[0])
	w := callCheckAPI(s.handleCollection, "POST", "/api/account-checks", body)
	if w.Code != 202 || strings.Contains(w.Body.String(), "private-") || strings.Contains(w.Body.String(), "fixture-at") {
		t.Fatalf("create response = %d %s", w.Code, w.Body.String())
	}
	var result struct {
		BatchID string `json:"batch_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.BatchID == "" {
		t.Fatal("missing batch id")
	}
	// A retry must return the original batch even if current routing changed.
	s.proxy, s.upstreamProxy = "", "http://127.0.0.1:1235"
	w = callCheckAPI(s.handleCollection, "POST", "/api/account-checks", body)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"reused":true`) {
		t.Fatalf("replay = %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		body string
		want int
	}{
		{strings.Replace(body, `"concurrency":2`, `"concurrency":1`, 1), 409},
		{strings.TrimSuffix(body, "}") + `,"upstream_url":"http://127.0.0.1"}`, 400},
		{body + ` {}`, 400},
		{strings.Repeat(" ", 33<<10) + body, 413},
	} {
		if got := callCheckAPI(s.handleCollection, "POST", "/api/account-checks", tc.body); got.Code != tc.want {
			t.Fatalf("invalid request status=%d want=%d", got.Code, tc.want)
		}
	}
	w = callCheckAPI(s.handleAction, "POST", "/api/account-checks/"+result.BatchID+"/cancel", `{}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"stopped"`) {
		t.Fatalf("cancel=%d %s", w.Code, w.Body.String())
	}
	// Explicit direct ignores the unsupported default proxy chain.
	direct := strings.Replace(strings.Replace(body, "fixture-request", "fixture-direct", 1), `"default"`, `"direct"`, 1)
	if got := callCheckAPI(s.handleCollection, "POST", "/api/account-checks", direct); got.Code != 202 {
		t.Fatalf("direct=%d %s", got.Code, got.Body.String())
	}
}

func TestAccountCheckAPIEligibilityAndCanceledRequest(t *testing.T) {
	history, _ := accountCheckFixture(t, 0)
	s := newAccountCheckService(history, "", "")
	defer s.Stop()
	w := callCheckAPI(s.handleCollection, "POST", "/api/account-checks", `{"request_key":"missing","account_ids":[999],"concurrency":1,"proxy_mode":"direct"}`)
	if w.Code != 422 {
		t.Fatalf("eligibility=%d %s", w.Code, w.Body.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "/api/account-checks", nil).WithContext(ctx)
	w = httptest.NewRecorder()
	s.respondError(w, r, sql.ErrTxDone)
	if s.paused.Load() || w.Code != 408 {
		t.Fatal("browser cancellation paused background workers")
	}
	r = httptest.NewRequest("POST", "/api/account-checks", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://other.example")
	w = httptest.NewRecorder()
	corsMiddleware(s.handleCollection)(w, r)
	if w.Code != 403 {
		t.Fatal("foreign origin accepted")
	}
}

func TestAccountCheckWorkerConcurrent(t *testing.T) {
	for _, concurrency := range []int{1, 2} {
		t.Run(fmt.Sprint(concurrency), func(t *testing.T) {
			history, ids := accountCheckFixture(t, 3)
			s := newAccountCheckService(history, "", "")
			entered := make(chan struct{}, 3)
			release := make(chan struct{})
			var active, peak atomic.Int32
			s.runProbe = func(ctx context.Context, route, token, account string) probe.Result {
				current := active.Add(1)
				defer active.Add(-1)
				for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
				}
				entered <- struct{}{}
				select {
				case <-ctx.Done():
					return probe.Result{Outcome: "canceled"}
				case <-release:
				}
				return probe.Result{Outcome: "ok", HTTPStatus: 200, RequestAttempted: true}
			}
			b, _, err := history.CreateAccountCheckBatch(context.Background(), store.AccountCheckInput{RequestKey: "workers", AccountIDs: ids, Concurrency: concurrency, ProxyMode: "direct"})
			if err != nil {
				t.Fatal(err)
			}
			s.Start()
			defer s.Stop()
			for i := 0; i < concurrency; i++ {
				select {
				case <-entered:
				case <-time.After(2 * time.Second):
					t.Fatal("configured workers did not overlap")
				}
			}
			select {
			case <-entered:
				t.Fatal("exceeded concurrency")
			case <-time.After(40 * time.Millisecond):
			}
			close(release)
			batch, items := waitCheckBatch(t, history, b.ID)
			if peak.Load() != int32(concurrency) || batch.Counts.OK != 3 || len(items) != 3 {
				t.Fatalf("peak=%d counts=%+v", peak.Load(), batch.Counts)
			}
		})
	}
}

func TestAccountCheckWorkerCancelLateSuccess(t *testing.T) {
	history, ids := accountCheckFixture(t, 3)
	s := newAccountCheckService(history, "", "")
	entered := make(chan struct{}, 2)
	s.runProbe = func(ctx context.Context, _, _, _ string) probe.Result {
		entered <- struct{}{}
		<-ctx.Done()
		return probe.Result{Outcome: "ok", HTTPStatus: 200, RequestAttempted: true}
	}
	b, _, err := history.CreateAccountCheckBatch(context.Background(), store.AccountCheckInput{RequestKey: "cancel-workers", AccountIDs: ids, Concurrency: 2, ProxyMode: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Stop()
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("worker not started")
		}
	}
	w := callCheckAPI(s.handleAction, "POST", "/api/account-checks/"+b.ID+"/cancel", `{}`)
	if w.Code != 200 {
		t.Fatalf("cancel=%d", w.Code)
	}
	batch, items := waitCheckBatch(t, history, b.ID)
	if batch.Counts.Canceled != 3 || batch.Counts.OK != 0 {
		t.Fatalf("late success leaked: %+v", batch.Counts)
	}
	for _, item := range items {
		if item.Outcome != "" {
			t.Fatalf("canceled result promoted: %s", item.Outcome)
		}
	}
}

func TestAccountCheckHistorySafeDTO(t *testing.T) {
	history, ids := accountCheckFixture(t, 1)
	old := loginHistory
	loginHistory = history
	defer func() { loginHistory = old }()
	w := callCheckAPI(handleHistory(), "GET", "/api/history", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"checks_available":true`) || !strings.Contains(w.Body.String(), `"eligible":true`) {
		t.Fatalf("history=%d %s", w.Code, w.Body.String())
	}
	for _, secret := range []string{"fixture-at", "fixture-rt", "fixture-password"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("credential in history DTO")
		}
	}
	w = callCheckAPI(handleHistoryDelete(), "GET", fmt.Sprintf("/api/history/%d", ids[0]), "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"checks":[]`) {
		t.Fatalf("detail=%d %s", w.Code, w.Body.String())
	}
}

func TestAccountCheckWorkerFinalRequestEvidence(t *testing.T) {
	history, ids := accountCheckFixture(t, 1)
	s := newAccountCheckService(history, "", "")
	// The client can discover expiry after the worker's initial snapshot.
	s.runProbe = func(context.Context, string, string, string) probe.Result {
		return probe.Result{Outcome: "access_token_expired", FailureStage: "precheck", RequestAttempted: false}
	}
	b, _, err := history.CreateAccountCheckBatch(context.Background(), store.AccountCheckInput{RequestKey: "local-precheck", AccountIDs: ids, Concurrency: 1, ProxyMode: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Stop()
	_, items := waitCheckBatch(t, history, b.ID)
	if len(items) != 1 || items[0].RequestAttempted == nil || *items[0].RequestAttempted || items[0].HTTPStatus != nil {
		t.Fatal("local precheck falsely reported an upstream request")
	}
}

func TestAccountCheckDeletedRemovesSub2Account(t *testing.T) {
	history, ids := accountCheckFixture(t, 1)
	var deleted atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case http.MethodGet + " /api/v1/admin/accounts/101":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"id": 101, "platform": "openai", "type": "oauth",
				"credentials": map[string]any{"email": "fixture-0@example.test", "chatgpt_account_id": "fixture-0"},
				"extra":       map[string]any{"kkai_auth_import": map[string]any{"source_account_id": ids[0]}},
			}})
		case http.MethodDelete + " /api/v1/admin/accounts/101":
			deleted.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"message": "deleted"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	oldImporter := sub2Importer
	sub2Importer = &sub2ImportService{store: history, baseURL: server.URL + "/api/v1", adminAPIKey: "secret", destinationKey: "fixture", client: server.Client()}
	defer func() { sub2Importer = oldImporter }()

	checker := newAccountCheckService(history, "", "")
	checker.runProbe = func(context.Context, string, string, string) probe.Result {
		return probe.Result{Outcome: "account_deleted", HTTPStatus: http.StatusForbidden, RequestAttempted: true}
	}
	batch, _, err := history.CreateAccountCheckBatch(context.Background(), store.AccountCheckInput{RequestKey: "deleted-sub2", AccountIDs: ids, Concurrency: 1, ProxyMode: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	checker.Start()
	defer checker.Stop()
	_, items := waitCheckBatch(t, history, batch.ID)
	if len(items) != 1 || items[0].Outcome != "account_deleted" {
		t.Fatalf("check result = %+v", items)
	}
	deadline := time.Now().Add(2 * time.Second)
	for deleted.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if deleted.Load() != 1 {
		t.Fatalf("delete requests = %d, want 1", deleted.Load())
	}
}

func TestAccountCheckCanceledLateDeletedDoesNotRemoveSub2Account(t *testing.T) {
	history, ids := accountCheckFixture(t, 1)
	var deleted atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted.Add(1)
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	oldImporter, oldRecovery := sub2Importer, accountRecoveryService
	sub2Importer = &sub2ImportService{store: history, baseURL: server.URL + "/api/v1", adminAPIKey: "secret", destinationKey: "fixture", client: server.Client()}
	accountRecoveryService = nil
	defer func() {
		sub2Importer = oldImporter
		accountRecoveryService = oldRecovery
	}()

	batch, _, err := history.CreateAccountCheckBatch(context.Background(), store.AccountCheckInput{
		RequestKey: "deleted-cancel-race", AccountIDs: ids, Concurrency: 1, ProxyMode: "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	work, err := history.ClaimAccountCheck(context.Background())
	if err != nil || work == nil {
		t.Fatalf("claim check = %#v, %v", work, err)
	}
	if _, err := history.CancelAccountCheckBatch(context.Background(), batch.ID); err != nil {
		t.Fatal(err)
	}
	checker := newAccountCheckService(history, "", "")
	status := http.StatusForbidden
	attempted := true
	checker.finish(work.Check.ID, ids[0], store.AccountCheckResult{
		Outcome: "account_deleted", HTTPStatus: &status, RequestAttempted: &attempted,
	})
	if deleted.Load() != 0 {
		t.Fatalf("late canceled result issued %d remote deletes", deleted.Load())
	}
	_, checks, err := history.GetAccountCheckBatch(context.Background(), batch.ID)
	if err != nil || len(checks) != 1 {
		t.Fatalf("read canceled check = %v, checks=%+v", err, checks)
	}
	if checks[0].State != "canceled" || checks[0].Outcome != "" {
		t.Fatalf("late canceled result persisted as %+v", checks[0])
	}
}
