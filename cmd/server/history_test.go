package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func newTestHistory(t *testing.T) *store.Store {
	t.Helper()
	history, err := store.Open(t.TempDir()+"/accounts.db", t.TempDir()+"/accounts.key")
	if err != nil {
		t.Fatalf("open history: %v", err)
	}
	old := loginHistory
	loginHistory = history
	t.Cleanup(func() {
		loginHistory = old
		_ = history.Close()
	})
	return history
}

func TestRunWithHistoryPersistsSuccessAndFailure(t *testing.T) {
	history := newTestHistory(t)
	req := LoginRequest{Email: " USER@example.com ", Password: "password-123", TotpSecret: "JBSWY3DPEHPK3PXP", Proxy: "http://127.0.0.1:7890"}
	result, err := runWithHistory(context.Background(), req, func(context.Context) (*login.LoginResult, error) {
		return &login.LoginResult{Email: "user@example.com", AccessToken: "access", RefreshToken: "refresh", PlanType: "plus"}, nil
	})
	if err != nil || result == nil {
		t.Fatalf("success = %#v, err = %v", result, err)
	}
	accounts, err := history.ListAccounts(context.Background())
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts = %#v, err = %v", accounts, err)
	}
	if accounts[0].Email != "user@example.com" || accounts[0].Status != "active" || accounts[0].AttemptCount != 1 {
		t.Fatalf("account metadata = %#v", accounts[0])
	}
	if _, err := history.GetCredentials(context.Background(), "user@example.com"); err != nil {
		t.Fatalf("credentials were not saved: %v", err)
	}

	_, err = runWithHistory(context.Background(), req, func(context.Context) (*login.LoginResult, error) {
		return nil, errors.New("network timeout")
	})
	if err == nil {
		t.Fatal("expected failed login")
	}
	accounts, err = history.ListAccounts(context.Background())
	if err != nil || len(accounts) != 1 || accounts[0].Status != "failed" {
		t.Fatalf("failure metadata = %#v, err = %v", accounts, err)
	}
}

func TestRunWithHistoryRejectsSameEmailWhileActive(t *testing.T) {
	newTestHistory(t)
	req := LoginRequest{Email: "same@example.com", Password: "password-123", TotpSecret: "JBSWY3DPEHPK3PXP"}
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := runWithHistory(context.Background(), req, func(context.Context) (*login.LoginResult, error) {
			close(started)
			<-release
			return &login.LoginResult{AccessToken: "a", RefreshToken: "r"}, nil
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first login did not start")
	}
	if _, err := runWithHistory(context.Background(), req, func(context.Context) (*login.LoginResult, error) {
		t.Fatal("busy login runner was called")
		return nil, nil
	}); !errors.Is(err, store.ErrAccountBusy) {
		t.Fatalf("busy error = %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first login = %v", err)
	}
}

func TestHistoryListAndDeleteEndpoints(t *testing.T) {
	history := newTestHistory(t)
	account, err := history.UpsertCredentials(context.Background(), store.Credentials{Email: "history@example.com", Password: "password-123", TOTPSecret: "JBSWY3DPEHPK3PXP"})
	if err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRecorder()
	handleHistory()(get, httptest.NewRequest(http.MethodGet, "/api/history", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "history@example.com") || strings.Contains(get.Body.String(), "password-123") {
		t.Fatalf("history response = %d %q", get.Code, get.Body.String())
	}
	detail := httptest.NewRecorder()
	handleHistoryDelete()(detail, httptest.NewRequest(http.MethodGet, "/api/history/"+itoa(account.ID), nil))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"attempts":[]`) {
		t.Fatalf("history detail response = %d %q", detail.Code, detail.Body.String())
	}
	delete := httptest.NewRecorder()
	handleHistoryDelete()(delete, httptest.NewRequest(http.MethodDelete, "/api/history/"+itoa(account.ID), nil))
	if delete.Code != http.StatusOK {
		t.Fatalf("delete status = %d: %s", delete.Code, delete.Body.String())
	}
	if _, err := history.GetAccountByID(context.Background(), account.ID); !errors.Is(err, store.ErrAccountNotFound) {
		t.Fatalf("account still exists: %v", err)
	}
}

func TestHistoryLifecycleEventsAreProjected(t *testing.T) {
	history := newTestHistory(t)
	ctx := context.Background()
	account, err := history.UpsertCredentials(ctx, store.Credentials{Email: "lifecycle-api@example.test", Password: "password-123"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := history.RecordLifecycleEvent(ctx, store.LifecycleEvent{
		AccountID: account.ID, Email: account.Email, Source: "official", Status: "deactivated",
		Reason: "官方账号已停用", Code: "account_deactivated", HTTPStatus: http.StatusForbidden,
		DedupeKey: "history-api:lifecycle-api@example.test",
	}); err != nil {
		t.Fatal(err)
	}

	list := httptest.NewRecorder()
	handleHistory()(list, httptest.NewRequest(http.MethodGet, "/api/history", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"lifecycle_events"`) || !strings.Contains(list.Body.String(), "account_deactivated") {
		t.Fatalf("lifecycle list projection = %d %s", list.Code, list.Body.String())
	}

	detail := httptest.NewRecorder()
	handleHistoryDelete()(detail, httptest.NewRequest(http.MethodGet, "/api/history/"+itoa(account.ID), nil))
	body := detail.Body.String()
	if detail.Code != http.StatusOK || !strings.Contains(body, `"lifecycle_events_available":true`) || !strings.Contains(body, "lifecycle-api@example.test") || !strings.Contains(body, `"http_status":403`) {
		t.Fatalf("lifecycle detail projection = %d %s", detail.Code, body)
	}
}

func TestHistoryRecoveryCountsAndSafeTimeline(t *testing.T) {
	history := newTestHistory(t)
	ctx := context.Background()
	account, err := history.UpsertCredentials(ctx, store.Credentials{Email: "recovery-history@example.test", Password: "secret-fixture-password"})
	if err != nil {
		t.Fatal(err)
	}
	if err := history.UpdateOAuthResultByID(ctx, account.ID, store.Result{AccessToken: "at", RefreshToken: "rt", PlanType: "plus"}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{store.RecoveryCompleted, store.RecoveryUnknown} {
		task, _, err := history.CreateOrGetAccountRecoveryTask(ctx, account.ID, 0, 42, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = history.UpdateAccountRecoveryTask(ctx, task.ID, state, "safe recovery outcome"); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/api/history", "/api/history/" + itoa(account.ID)} {
		response := httptest.NewRecorder()
		if path == "/api/history" {
			handleHistory()(response, httptest.NewRequest(http.MethodGet, path, nil))
		} else {
			handleHistoryDelete()(response, httptest.NewRequest(http.MethodGet, path, nil))
		}
		body := response.Body.String()
		if response.Code != http.StatusOK || !strings.Contains(body, `"recovery_count":1`) || !strings.Contains(body, `"recovery_attempt_count":2`) {
			t.Fatalf("history counts %s: %d %s", path, response.Code, body)
		}
		if strings.Contains(body, "secret-fixture-password") || strings.Contains(body, "password_cipher") || strings.Contains(body, "access_token") {
			t.Fatalf("sensitive recovery response: %s", body)
		}
		if path != "/api/history" && (!strings.Contains(body, `"recovery_history_available":true`) || !strings.Contains(body, `"recovery_history_truncated":false`) || !strings.Contains(body, `"state":"unknown"`) || !strings.Contains(body, `"state":"completed"`)) {
			t.Fatalf("missing recovery timeline: %s", body)
		}
	}
}

func TestHistoryRecoveryFailureProjectionIsRedacted(t *testing.T) {
	history := newTestHistory(t)
	ctx := context.Background()
	account, err := history.UpsertCredentials(ctx, store.Credentials{Email: "recovery-failure-history@example.test", Password: "secret-fixture-password"})
	if err != nil {
		t.Fatal(err)
	}
	if err := history.UpdateOAuthResultByID(ctx, account.ID, store.Result{AccessToken: "at", RefreshToken: "rt", PlanType: "plus"}); err != nil {
		t.Fatal(err)
	}
	task, _, err := history.CreateOrGetAccountRecoveryTask(ctx, account.ID, 0, 42, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := history.RecordAccountRecoveryFailure(ctx, task.ID, store.RecoveryFailure{
		State: store.RecoveryUnknown, Stage: store.RecoveryValidating, Code: "upstream_error", Message: "上游服务暂不可用", HTTPStatus: 503, RetryAction: "resume",
	}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handleHistoryDelete()(response, httptest.NewRequest(http.MethodGet, "/api/history/"+itoa(account.ID), nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `"recovery_failures_available":true`) || !strings.Contains(body, `"failure_stage":"validating"`) || !strings.Contains(body, `"error_code":"upstream_error"`) || !strings.Contains(body, `"http_status":503`) {
		t.Fatalf("missing failure projection: %d %s", response.Code, body)
	}
	if strings.Contains(body, "secret-fixture-password") || strings.Contains(body, "upstream raw response") {
		t.Fatalf("unsafe failure projection: %s", body)
	}
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}

func TestHistoryDeleteRejectsActiveWork(t *testing.T) {
	history := newTestHistory(t)
	ctx := context.Background()
	account, attempt, err := history.BeginAttempt(ctx, store.Credentials{Email: "busy-delete@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Release()
	response := httptest.NewRecorder()
	handleHistoryDelete()(response, httptest.NewRequest(http.MethodDelete, "/api/history/"+itoa(account.ID), nil))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"account_busy"`) {
		t.Fatalf("busy delete = %d %s", response.Code, response.Body.String())
	}
	if _, err = history.GetAccountByID(ctx, account.ID); err != nil {
		t.Fatalf("busy account was deleted: %v", err)
	}
}

func TestRunWithHistoryAccountRejectsDeletedIdentity(t *testing.T) {
	history := newTestHistory(t)
	ctx := context.Background()
	account, err := history.UpsertCredentials(ctx, store.Credentials{Email: "stale-history@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runWithHistoryAccount(ctx, account.ID, LoginRequest{Email: account.Email, Password: "pw"}, func(context.Context) (*login.LoginResult, error) {
		return &login.LoginResult{AccessToken: "fixture-at", RefreshToken: "fixture-rt"}, nil
	})
	if err != nil {
		t.Fatalf("existing history login = %v", err)
	}
	if err = history.DeleteAccountByID(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	_, err = runWithHistoryAccount(ctx, account.ID, LoginRequest{Email: account.Email, Password: "pw"}, func(context.Context) (*login.LoginResult, error) {
		t.Fatal("deleted identity reached login runner")
		return nil, nil
	})
	if !errors.Is(err, store.ErrAccountNotFound) {
		t.Fatalf("stale history login = %v", err)
	}
	if _, err = history.GetAccount(ctx, account.Email); !errors.Is(err, store.ErrAccountNotFound) {
		t.Fatalf("stale history login recreated account: %v", err)
	}
}

func TestHistoryLoginRejectsRetainedDeletedAccount(t *testing.T) {
	history := newTestHistory(t)
	ctx := context.Background()
	account, attempt, err := history.BeginAttempt(ctx, store.Credentials{Email: "deleted-history@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if err := history.FinishAttempt(ctx, attempt.ID, false, nil, &store.Failure{Code: "account_deleted", AccountStatus: "deleted"}); err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	request := httptest.NewRequest(http.MethodPost, "/api/history/login", strings.NewReader(`{"account_id":`+itoa(account.ID)+`}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handleHistoryLogin(nil)(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"account_not_found"`) {
		t.Fatalf("deleted history login = %d %s", response.Code, response.Body.String())
	}
}

func TestHistoryLoginRejectsIncompleteBindingRequest(t *testing.T) {
	newTestHistory(t)
	for _, body := range []string{
		`{"account_id":1,"auto_deliver":true,"expected_binding_operation_id":"old"}`,
		`{"account_id":1,"auto_deliver":true,"expected_remote_account_id":42,"expected_credential_version":7}`,
		`{"account_id":1,"expected_binding_operation_id":"old","expected_remote_account_id":42,"expected_credential_version":7}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/history/login", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handleHistoryLogin(nil)(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_binding"`) {
			t.Fatalf("incomplete binding returned %d %s", response.Code, response.Body.String())
		}
	}
}

func TestHistoryReloginRejectsChangedIssuerSubject(t *testing.T) {
	history := newTestHistory(t)
	ctx := context.Background()
	req := LoginRequest{Email: "issuer@example.test", Password: "pw"}
	// These fixture tokens come from the mocked login runner.
	token := func(subject string) string {
		return "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://issuer.example","sub":"`+subject+`"}`)) + ".sig"
	}
	first, err := runWithHistory(ctx, req, func(context.Context) (*login.LoginResult, error) {
		return &login.LoginResult{Email: req.Email, AccessToken: token("one"), RefreshToken: "old", PlanType: "plus"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runWithHistoryAccount(ctx, first.AccountID, req, func(context.Context) (*login.LoginResult, error) {
		return &login.LoginResult{Email: req.Email, AccessToken: token("two"), RefreshToken: "other", PlanType: "free"}, nil
	})
	if err == nil {
		t.Fatal("different issuer subject was accepted")
	}
	_, saved, err := history.GetOAuthResultByID(ctx, first.AccountID)
	if err != nil || saved.PlanType != "plus" || saved.RefreshToken != "old" {
		t.Fatalf("original account credentials changed: err=%v", err)
	}
}
