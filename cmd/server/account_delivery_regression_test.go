package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func TestAccountDeliveryDeletedGroupRetry(t *testing.T) {
	history, accountID := testOAuthStore(t)
	ctx := context.Background()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/admin/accounts":
			fmt.Fprint(w, `{"code":0,"data":{"items":[],"total":0,"pages":1}}`)
		case "/admin/groups/all":
			fmt.Fprint(w, `{"code":0,"data":[{"id":8,"name":"replacement","platform":"openai","status":"active"}]}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer api.Close()
	importer := &sub2ImportService{store: history, baseURL: api.URL, adminAPIKey: "fixture", destinationKey: "fixture", client: api.Client()}
	recovery := newSub2RecoveryService(history, nil, importer)
	defer recovery.Stop()
	delivery := newAccountDeliveryService(history, importer, recovery)
	defer delivery.Stop()
	task, err := history.QueueAccountDelivery(ctx, accountID, "fixture", store.DeliveryOptions{GroupIDs: []int64{7}, Priority: 1, Concurrency: 3})
	if err != nil {
		t.Fatal(err)
	}
	delivery.runOnce()
	task, err = history.GetAccountDelivery(ctx, task.ID)
	if err != nil || !task.RequiresAction {
		t.Fatalf("missing initial group failure: %+v %v", task, err)
	}
	response := httptest.NewRecorder()
	importer.handleImport(response, httptest.NewRequest(http.MethodPost, "/api/sub2/import", strings.NewReader(fmt.Sprintf(`{"account_ids":[%d],"delivery_options":{"group_ids":[8],"priority":1,"concurrency":3}}`, accountID))))
	if response.Code != http.StatusOK {
		t.Fatalf("resubmit rejected: %d %s", response.Code, response.Body.String())
	}
	after, err := history.GetAccountDelivery(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	due, err := history.ListDueAccountDeliveries(ctx, time.Now().Add(time.Hour), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) == 0 || after.Options.GroupIDs[0] != 8 {
		t.Fatalf("accepted corrected group but task is still stuck: state=%s groups=%v due=%d response=%s", after.State, after.Options.GroupIDs, len(due), response.Body.String())
	}
}

func TestAccountDeliveryPauseStopsBatch(t *testing.T) {
	f, delivery, first := newDeliveryFixture(t)
	ctx := context.Background()
	account, err := f.store.UpsertCredentials(ctx, store.Credentials{Email: "second@example.test", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := f.store.StartAttempt(ctx, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.FinishAttemptAndQueueDelivery(ctx, attempt.ID, &store.Result{AccessToken: "second-at", RefreshToken: "second-rt", ChatGPTAccountID: "second-workspace"}, "fixture")
	attempt.Release()
	if err != nil {
		t.Fatal(err)
	}
	posts := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			posts++
			fmt.Fprint(w, `{"code":0,"data":{"account_created":1,"account_failed":0}}`)
			return
		}
		fmt.Fprint(w, `{"code":0,"data":{"items":[],"total":0,"pages":1}}`)
	}))
	defer api.Close()
	f.service.sub2.baseURL, f.service.sub2.client = api.URL, api.Client()
	_, err = f.db.Exec(fmt.Sprintf(`CREATE TRIGGER review_delivery_first_failure BEFORE UPDATE OF state ON account_deliveries WHEN OLD.id=%d BEGIN SELECT RAISE(ABORT,'storage failure'); END`, first.ID))
	if err != nil {
		t.Fatal(err)
	}
	delivery.runOnce()
	if !f.service.paused.Load() {
		t.Fatal("fixture failed to pause")
	}
	if posts != 0 {
		t.Fatalf("storage circuit breaker paused but batch still created %d remote accounts", posts)
	}
}

func TestSub2RecoveryWrappedSSE401Relogin(t *testing.T) {
	f := newRecoveryFixture(t, true)
	// Exact shape emitted by Sub2 TestEvent plus sendErrorAndEnd in its
	// OpenAI account test path when the upstream returns HTTP 401.
	f.probeBody = "data: {\"type\":\"error\",\"error\":\"API returned 401: {\\\"error\\\":{\\\"code\\\":\\\"token_revoked\\\"}}\"}\n\n"
	f.service.process(f.task)
	got := f.state(t)
	if got.RetryAction != "relogin" || got.ErrorCode != "credential_invalid" {
		t.Fatalf("upstream 401 does not invalidate saved OAuth checkpoint: action=%s code=%s version=%d loginCalls=%d", got.RetryAction, got.ErrorCode, got.ResultCredentialAttemptID, f.loginCalls)
	}
}
