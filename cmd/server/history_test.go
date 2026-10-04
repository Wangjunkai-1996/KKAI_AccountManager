package main

import (
	"context"
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
