package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func TestSub2RecoveryRechecksRemoteOwnershipAfterLogin(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   string
		change func(*recoveryFixture)
	}{
		{name: "email changed", code: "identity_changed", change: func(f *recoveryFixture) { f.detailEmail = "replacement@example.test" }},
		{name: "disabled", code: "account_unavailable", change: func(f *recoveryFixture) { f.detailStatus = "disabled" }},
		{name: "import marker removed", code: "identity_changed", change: func(f *recoveryFixture) { f.externalAccount = true }},
		{name: "recovery marker replaced", code: "identity_changed", change: func(f *recoveryFixture) {
			f.recoveryMarker = map[string]any{"task_id": float64(9999), "credential_attempt_id": float64(9999)}
		}},
		{name: "schedule changed", code: "manual_pause", change: func(f *recoveryFixture) { f.schedule = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecoveryFixture(t, true)
			loginBeforeChange := f.service.loginWithProxies
			f.service.loginWithProxies = func(ctx context.Context, email, password, totp, proxy, upstream string) (*login.LoginResult, error) {
				result, err := loginBeforeChange(ctx, email, password, totp, proxy, upstream)
				f.mu.Lock()
				tc.change(f)
				f.mu.Unlock()
				return result, err
			}
			f.service.process(f.task)
			got := f.state(t)
			if got.ErrorCode != tc.code || !got.RequiresAction || got.ResultCredentialAttemptID == 0 {
				t.Fatalf("remote change was not stopped with saved credentials: %+v", got)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.applyCalls != 0 || f.testCalls != 0 || strings.Contains(strings.Join(f.events, " "), "enable") {
				t.Fatalf("remote change was overwritten: apply=%d probe=%d events=%v", f.applyCalls, f.testCalls, f.events)
			}
		})
	}
}

func TestSub2RecoveryRechecksLocalBindingAfterLogin(t *testing.T) {
	f := newRecoveryFixture(t, true)
	loginBeforeChange := f.service.loginWithProxies
	f.service.loginWithProxies = func(ctx context.Context, email, password, totp, proxy, upstream string) (*login.LoginResult, error) {
		result, err := loginBeforeChange(ctx, email, password, totp, proxy, upstream)
		if _, updateErr := f.db.Exec(`UPDATE sub2_imports SET operation_id='replacement-operation' WHERE account_id=?`, f.account.ID); updateErr != nil {
			t.Fatal(updateErr)
		}
		return result, err
	}
	f.service.process(f.task)
	got := f.state(t)
	if got.ErrorCode != "identity_changed" || !got.RequiresAction || f.applyCalls != 0 || f.schedule {
		t.Fatalf("changed local binding reached remote write: task=%+v apply=%d scheduled=%v", got, f.applyCalls, f.schedule)
	}
}

func TestSub2RecoveryFreshReadFailureKeepsCheckpointForRetry(t *testing.T) {
	f := newRecoveryFixture(t, true)
	base := f.service.sub2.client.Transport
	f.service.sub2.client.Transport = recoveryConcurrencyTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.Path == "/admin/accounts/901" && f.loginCalls > 0 {
			return nil, context.DeadlineExceeded
		}
		return base.RoundTrip(r)
	})
	f.service.process(f.task)
	got := f.state(t)
	if got.State != store.RecoveryUnknown || got.ErrorCode != "timeout" || got.RequiresAction || !got.Resumable || got.RetryAction != "resume" || got.NextRetryAt == nil || f.applyCalls != 0 || f.schedule {
		t.Fatalf("fresh-read failure lost safe retry: task=%+v apply=%d scheduled=%v", got, f.applyCalls, f.schedule)
	}
}

func TestSub2RecoveryFreshVersionReadStorageFailurePauses(t *testing.T) {
	f := newRecoveryFixture(t, true)
	base := f.service.sub2.client.Transport
	f.service.sub2.client.Transport = recoveryConcurrencyTransport(func(r *http.Request) (*http.Response, error) {
		response, err := base.RoundTrip(r)
		if r.Method == http.MethodGet && r.URL.Path == "/admin/accounts/901" && f.loginCalls > 0 {
			if closeErr := f.store.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
		}
		return response, err
	})
	f.service.process(f.task)
	if !f.service.paused.Load() || f.applyCalls != 0 || f.schedule {
		t.Fatalf("storage failure did not stop before write: paused=%v apply=%d scheduled=%v", f.service.paused.Load(), f.applyCalls, f.schedule)
	}
}

func TestSub2RecoveryConcurrent401AfterProbeRemainsRetryable(t *testing.T) {
	f := newRecoveryFixture(t, true)
	base := f.service.sub2.client.Transport
	f.service.sub2.client.Transport = recoveryConcurrencyTransport(func(r *http.Request) (*http.Response, error) {
		response, err := base.RoundTrip(r)
		if r.URL.Path == "/admin/accounts/901/probe" {
			f.mu.Lock()
			f.detailStatus, f.detailError = "error", "Token revoked (401): invalidated oauth token"
			f.mu.Unlock()
		}
		return response, err
	})
	f.service.process(f.task)
	got := f.state(t)
	if got.State != store.RecoveryUnknown || got.ErrorCode != "probe_failed" || got.RequiresAction || got.NextRetryAt == nil || f.schedule {
		t.Fatalf("runtime error became terminal or enabled scheduling: %+v schedule=%v", got, f.schedule)
	}
}
