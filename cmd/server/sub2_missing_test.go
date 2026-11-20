package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func TestSub2MissingTrustsOnlyAccountDetailEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name, path, contentType, body string
		want                          bool
	}{
		{"numeric", "/admin/accounts/42", "application/json", `{"code":404,"message":"account not found"}`, true},
		{"legacy", "/admin/accounts/42", "application/json; charset=utf-8", `{"code":1,"message":"not found"}`, true},
		{"empty", "/admin/accounts/42", "application/json", "", false},
		{"html", "/admin/accounts/42", "text/html", "<h1>404</h1>", false},
		{"proxy json", "/admin/accounts/42", "application/json", `{"error":"not found"}`, false},
		{"false", "/admin/accounts/42", "application/json", `{"error":false}`, false},
		{"empty error", "/admin/accounts/42", "application/json", `{"error":""}`, false},
		{"wrong code", "/admin/accounts/42", "application/json", `{"code":502,"message":"not found"}`, false},
		{"wrong message", "/admin/accounts/42", "application/json", `{"code":404,"message":"proxy route not found"}`, false},
		{"stats", "/admin/accounts/42/stats", "application/json", `{"code":404,"message":"not found"}`, false},
		{"list", "/admin/accounts", "application/json", `{"code":404,"message":"not found"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := trustedSub2NotFound(tc.path, tc.contentType, []byte(tc.body)); got != tc.want {
				t.Fatalf("trusted=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestSub2MissingRefreshAndWorkersNeverReuseDeletedRemote(t *testing.T) {
	history, id := testOAuthStore(t)
	ctx := context.Background()
	binding, err := history.LinkSub2Account(ctx, "test", id, 42, "linked-original")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"code":404,"message":"account not found"}`))
	}))
	defer remote.Close()
	service := &sub2ImportService{store: history, baseURL: remote.URL, adminAPIKey: "key", destinationKey: "test", client: remote.Client()}
	account, _ := history.GetAccountByID(ctx, id)
	first := service.sub2AccountStatus(ctx, 42, time.Now(), account)
	if first.AssociationStatus != "missing" || first.Unknown {
		t.Fatalf("first=%+v", first)
	}
	for i := 0; i < 2; i++ {
		_, statuses, err := service.syncAccountStatuses(ctx)
		if err != nil || statuses[id].AssociationStatus != "missing" {
			t.Fatalf("refresh=%+v err=%v", statuses, err)
		}
		if _, err = service.ensureDeliveryTask(ctx, id, true); !errors.Is(err, store.ErrSub2AccountMissing) {
			t.Fatalf("auto create=%v", err)
		}
		if err = service.processQueued(ctx, binding); !errors.Is(err, store.ErrSub2AccountMissing) {
			t.Fatalf("old queue=%v", err)
		}
		if err = service.reconcile(ctx, binding.ID); !errors.Is(err, store.ErrSub2AccountMissing) {
			t.Fatalf("old reconcile=%v", err)
		}
		if remoteID, err := service.recentAccountCostRemoteID(ctx, id); err != nil || remoteID != 0 {
			t.Fatalf("cost id=%d err=%v", remoteID, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("missing binding made %d requests", calls.Load())
	}
	if _, err = history.GetCredentialsByID(ctx, id); err != nil {
		t.Fatalf("credentials removed: %v", err)
	}
}

func TestSub2MissingLate404CannotRetireNewOperation(t *testing.T) {
	history, id := testOAuthStore(t)
	ctx := context.Background()
	binding, err := history.LinkSub2Account(ctx, "test", id, 42, "linked-original")
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"code":404,"message":"account not found"}`))
	}))
	defer remote.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	service := &sub2ImportService{store: history, baseURL: remote.URL, adminAPIKey: "key", destinationKey: "test", client: remote.Client()}
	done := make(chan sub2AccountStatus, 1)
	go func() { done <- service.sub2AccountStatus(ctx, 42, time.Now()) }()
	<-started
	if err = history.MarkSub2AccountMissing(ctx, binding); err != nil {
		t.Fatal(err)
	}
	binding, _ = history.GetSub2ImportByID(ctx, binding.ID)
	version, _ := history.GetAccountCredentialVersion(ctx, id)
	options := store.DefaultDeliveryOptions()
	options.Merchant = "merchant"
	options.Deadline = "later"
	fresh, err := history.AuthorizeRemoteMissingReimport(ctx, binding, version, "new-operation", "new-key", []byte(`{}`), options)
	if err != nil {
		t.Fatal(err)
	}
	if err = history.UpdateSub2ImportForOperation(ctx, fresh, "imported", true, 42, ""); err != nil {
		t.Fatal(err)
	}
	close(release)
	status := <-done
	if !status.Unknown {
		t.Fatalf("late response became current deletion: %+v", status)
	}
	current, err := history.GetSub2ImportByID(ctx, binding.ID)
	if err != nil || current.State != "imported" || current.OperationID != "new-operation" {
		t.Fatalf("new operation retired: %+v %v", current, err)
	}
}

func TestSub2MissingUnboundUpdateIntentNeverCreates(t *testing.T) {
	history, id := testOAuthStore(t)
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer remote.Close()
	service := &sub2ImportService{store: history, baseURL: remote.URL, adminAPIKey: "key", destinationKey: "test", client: remote.Client()}
	if _, err := service.ensureDeliveryTask(context.Background(), id, false); !errors.Is(err, store.ErrSub2AccountMissing) {
		t.Fatalf("update fallback=%v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("unbound update contacted remote %d times", calls.Load())
	}
}

func TestSub2MissingReimportEndpointRequiresExplicitIntentAndFreshLogin(t *testing.T) {
	history, accountID := testOAuthStore(t)
	ctx := context.Background()
	binding, err := history.LinkSub2Account(ctx, "test", accountID, 42, "linked-original")
	if err != nil {
		t.Fatal(err)
	}
	if err := history.MarkSub2AccountMissing(ctx, binding); err != nil {
		t.Fatal(err)
	}
	service := &sub2ImportService{
		store: history, baseURL: "http://127.0.0.1:1", adminAPIKey: "fixture", destinationKey: "test",
		client: http.DefaultClient,
	}
	options := `{"group_ids":[],"priority":1,"concurrency":3,"merchant":"测试号商","deadline":"2026-12-31"}`
	ordinary := fmt.Sprintf(`{"account_ids":[%d],"delivery_options":%s}`, accountID, options)
	response := httptest.NewRecorder()
	service.handleImport(response, httptest.NewRequest(http.MethodPost, "/api/sub2/import", strings.NewReader(ordinary)))
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatalf("ordinary import revived missing account: %d %s", response.Code, response.Body.String())
	}
	if deliveries, err := history.ListLatestAccountDeliveries(ctx, "test"); err != nil || len(deliveries) != 0 {
		t.Fatalf("ordinary import created delivery: %+v %v", deliveries, err)
	}
	reimport := fmt.Sprintf(`{"account_ids":[%d],"reimport":true,"delivery_options":%s}`, accountID, options)
	response = httptest.NewRecorder()
	service.handleImport(response, httptest.NewRequest(http.MethodPost, "/api/sub2/import", strings.NewReader(reimport)))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"reimport_credential_version_required"`) {
		t.Fatalf("unversioned reimport accepted: %d %s", response.Code, response.Body.String())
	}
	version, err := history.GetAccountCredentialVersion(ctx, accountID)
	if err != nil || version <= 0 {
		t.Fatalf("credential version=%d err=%v", version, err)
	}
	unbound := fmt.Sprintf(`{"account_ids":[%d],"reimport":true,"expected_credential_version":%d,"delivery_options":%s}`, accountID, version, options)
	response = httptest.NewRecorder()
	service.handleImport(response, httptest.NewRequest(http.MethodPost, "/api/sub2/import", strings.NewReader(unbound)))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"reimport_binding_required"`) {
		t.Fatalf("unbound reimport accepted: %d %s", response.Code, response.Body.String())
	}
	reimport = fmt.Sprintf(`{"account_ids":[%d],"reimport":true,"expected_credential_version":%d,"expected_import_operation_id":%q,"delivery_options":%s}`, accountID, version, binding.OperationID, options)
	stale := fmt.Sprintf(`{"account_ids":[%d],"reimport":true,"expected_credential_version":%d,"expected_import_operation_id":%q,"delivery_options":%s}`, accountID, version+1, binding.OperationID, options)
	response = httptest.NewRecorder()
	service.handleImport(response, httptest.NewRequest(http.MethodPost, "/api/sub2/import", strings.NewReader(stale)))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"reimport_credential_version_stale"`) {
		t.Fatalf("stale reimport accepted: %d %s", response.Code, response.Body.String())
	}
	blocked, _, err := history.CreateOrGetAccountRecoveryTask(ctx, accountID, 0, 42, true, "test")
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	service.handleImport(response, httptest.NewRequest(http.MethodPost, "/api/sub2/import", strings.NewReader(reimport)))
	if response.Code == http.StatusOK || strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatalf("reimport bypassed active recovery: %d %s", response.Code, response.Body.String())
	}
	if _, err := history.UpdateAccountRecoveryTask(ctx, blocked.ID, store.RecoveryCanceled, "fixture"); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	service.handleImport(response, httptest.NewRequest(http.MethodPost, "/api/sub2/import", strings.NewReader(reimport)))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatalf("explicit reimport rejected: %d %s", response.Code, response.Body.String())
	}
	deliveries, err := history.ListLatestAccountDeliveries(ctx, "test")
	if err != nil || len(deliveries) != 1 || deliveries[0].State != "queued" || !deliveries[0].AllowCreate {
		t.Fatalf("reimport delivery=%+v err=%v", deliveries, err)
	}
	response = httptest.NewRecorder()
	service.handleImport(response, httptest.NewRequest(http.MethodPost, "/api/sub2/import", strings.NewReader(reimport)))
	if response.Code == http.StatusOK || strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatalf("replayed reimport was accepted: %d %s", response.Code, response.Body.String())
	}
	after, err := history.ListLatestAccountDeliveries(ctx, "test")
	if err != nil || len(after) != 1 || after[0].ImportOperationID != deliveries[0].ImportOperationID {
		t.Fatalf("replay changed delivery intent: %+v %v", after, err)
	}
	// Even if the new remote account later goes missing, the old request is
	// still bound to the first missing operation and cannot recreate it again.
	current, err := history.GetSub2Import(ctx, "test", accountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := history.UpdateSub2ImportForOperation(ctx, current, "imported", true, 43, ""); err != nil {
		t.Fatal(err)
	}
	current, err = history.GetSub2Import(ctx, "test", accountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := history.MarkSub2AccountMissing(ctx, current); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	service.handleImport(response, httptest.NewRequest(http.MethodPost, "/api/sub2/import", strings.NewReader(reimport)))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"reimport_credential_version_stale"`) {
		t.Fatalf("old operation revived later missing binding: %d %s", response.Code, response.Body.String())
	}
	afterBinding, err := history.GetSub2Import(ctx, "test", accountID)
	if err != nil || afterBinding.State != store.Sub2RemoteMissing || afterBinding.OperationID != current.OperationID {
		t.Fatalf("old request changed current missing binding: %+v %v", afterBinding, err)
	}
}

func TestSub2AccountDisplayNameSourcesAndFallbackRefresh(t *testing.T) {
	history, id := testOAuthStore(t)
	ctx := context.Background()
	account, _ := history.GetAccountByID(ctx, id)
	service := &sub2ImportService{store: history, destinationKey: "test"}
	var snapshot sub2AccountStatus
	service.attachDisplayName(ctx, account, store.Sub2Import{}, &snapshot)
	if snapshot.Name != account.Email || snapshot.NameSource != "email_fallback" {
		t.Fatalf("fallback=%+v", snapshot)
	}
	options := store.DefaultDeliveryOptions()
	options.Merchant = "new prefix"
	options.Deadline = "later"
	if _, err := history.QueueAccountDelivery(ctx, id, "test", options); err != nil {
		t.Fatal(err)
	}
	snapshot = sub2AccountStatus{}
	service.attachDisplayName(ctx, account, store.Sub2Import{}, &snapshot)
	if snapshot.NameSource != "local_delivery" || !strings.HasPrefix(snapshot.Name, "new prefix | later | ") {
		t.Fatalf("fallback cache hid new prefix=%+v", snapshot)
	}
	payload := fmt.Sprintf(`{"data":{"accounts":[{"name":%q}]}}`, "saved prefix | account")
	binding, _, err := history.CreateOrGetSub2Import(ctx, "test", id, "operation", "key", []byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	snapshot = sub2AccountStatus{}
	service.attachDisplayName(ctx, account, binding, &snapshot)
	if snapshot.NameSource != "historical_import" || snapshot.Name != "saved prefix | account" || !snapshot.NameStale {
		t.Fatalf("import name=%+v", snapshot)
	}
	snapshot = sub2AccountStatus{Name: "current remote name"}
	service.attachDisplayName(ctx, account, binding, &snapshot)
	if snapshot.NameSource != "current_remote" || snapshot.NameStale {
		t.Fatalf("current name=%+v", snapshot)
	}
	snapshot = sub2AccountStatus{Unknown: true, Stale: true}
	service.attachDisplayName(ctx, account, binding, &snapshot)
	if snapshot.NameSource != "cached_remote" || snapshot.Name != "current remote name" || !snapshot.NameStale {
		t.Fatalf("cached name=%+v", snapshot)
	}
}

func TestAccountDeliveryStatusIncludesRecoveryCredentialVersions(t *testing.T) {
	f, service, delivery := newDeliveryFixture(t)
	ctx := context.Background()
	service.runOnce()
	current, err := f.store.GetAccountDelivery(ctx, delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.store.GetAccountRecoveryTaskByID(ctx, current.RecoveryTaskID)
	if err != nil {
		t.Fatal(err)
	}
	statuses, err := listDeliveryStatuses(ctx, f.store, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	projected := statuses[delivery.AccountID]
	if projected.SourceCredentialAttemptID != task.SourceCredentialAttemptID || projected.ResultCredentialAttemptID != task.ResultCredentialAttemptID || projected.SourceCredentialAttemptID <= 0 {
		t.Fatalf("version projection=%+v task=%+v", projected, task)
	}
}

func TestSub2MissingOfficialDeletionStillClearsCredentials(t *testing.T) {
	history, id := testOAuthStore(t)
	ctx := context.Background()
	binding, _, err := history.CreateOrGetSub2Import(ctx, "test", id, "original-operation", "original-key", []byte(`{"data":{"accounts":[{"name":"retained prefix","credentials":{"access_token":"import-secret-at","refresh_token":"import-secret-rt"}}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = history.UpdateSub2Import(ctx, binding.ID, "imported", true, 42, ""); err != nil {
		t.Fatal(err)
	}
	binding, _ = history.GetSub2ImportByID(ctx, binding.ID)
	if err = history.MarkSub2AccountMissing(ctx, binding); err != nil {
		t.Fatal(err)
	}
	account, _ := history.GetAccountByID(ctx, id)
	attempt, err := history.StartAttempt(ctx, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	if err = history.FinishAttempt(ctx, attempt.ID, false, nil, &store.Failure{AccountStatus: "deleted", Code: "account_deleted"}); err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("confirmed missing issued remote request")
		w.WriteHeader(500)
	}))
	defer remote.Close()
	service := &sub2ImportService{store: history, baseURL: remote.URL, adminAPIKey: "key", destinationKey: "test", client: remote.Client()}
	if err = service.deleteDeletedAccount(ctx, id, binding.CredentialVersion); err != nil {
		t.Fatal(err)
	}
	credentials, err := history.GetCredentialsByID(ctx, id)
	if !errors.Is(err, store.ErrAccountOfficialUnavailable) || credentials.Password != "" || credentials.TOTPSecret != "" {
		t.Fatalf("official deletion retained secrets: %v", err)
	}
	current, err := history.GetSub2Import(ctx, "test", id)
	if err != nil || current.State != store.Sub2RemoteMissing || current.ID != binding.ID || current.Sub2AccountID != 42 || current.OperationID != binding.OperationID {
		t.Fatalf("tombstone removed: %+v %v", current, err)
	}
	cleaned, err := history.GetSub2ImportPayload(ctx, binding.ID)
	if err != nil || len(cleaned) != 0 {
		t.Fatalf("import secrets not cleared safely: %v", err)
	}
	if err = service.reconcile(ctx, binding.ID); !errors.Is(err, store.ErrSub2AccountMissing) {
		t.Fatalf("deleted reconcile=%v", err)
	}
}

func TestSub2MissingUntrustedCleanup404KeepsBindingWithSecretsCleared(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			history, id := testOAuthStore(t)
			ctx := context.Background()
			binding, err := history.LinkSub2Account(ctx, "test", id, 42, "linked-original")
			if err != nil {
				t.Fatal(err)
			}
			account, _ := history.GetAccountByID(ctx, id)
			attempt, err := history.StartAttempt(ctx, account.Email)
			if err != nil {
				t.Fatal(err)
			}
			if err = history.FinishAttempt(ctx, attempt.ID, false, nil, &store.Failure{Code: "account_deleted", AccountStatus: "deleted"}); err != nil {
				t.Fatal(err)
			}
			attempt.Release()
			version, _ := history.GetAccountCredentialVersion(ctx, id)
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == method {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(404)
					_, _ = w.Write([]byte(`{"error":false}`))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"code":0,"data":{"id":42,"platform":"openai","type":"oauth","schedulable":true,"updated_at":"2026-11-20T00:00:00Z","credentials":{"email":%q,"chatgpt_account_id":%q}}}`, account.Email, account.ChatGPTAccountID)
			}))
			defer remote.Close()
			service := &sub2ImportService{store: history, baseURL: remote.URL, adminAPIKey: "key", destinationKey: "test", client: remote.Client()}
			err = service.deleteDeletedAccount(ctx, id, version)
			var operation *recoveryOperationError
			wantCode := "protocol_error"
			if method == http.MethodPost {
				wantCode = "account_missing"
			}
			if !errors.As(err, &operation) || operation.Code != wantCode {
				t.Fatalf("untrusted404 classification=%v", err)
			}
			current, err := history.GetSub2ImportByID(ctx, binding.ID)
			if err != nil || current.State != "cleanup_pending" {
				t.Fatalf("untrusted cleanup removed binding: %+v %v", current, err)
			}
			credentials, err := history.GetCredentialsByID(ctx, id)
			if !errors.Is(err, store.ErrAccountOfficialUnavailable) || credentials.Password != "" {
				t.Fatalf("official deletion left credentials accessible: %v", err)
			}
			events, err := history.ListLifecycleEventsForAccount(ctx, id, 20)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range events {
				if event.Source == "sub2" {
					t.Fatal("untrusted cleanup recorded deletion")
				}
			}
		})
	}
}
