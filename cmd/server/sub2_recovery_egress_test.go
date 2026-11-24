package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

type recoveryEgressFixture struct {
	service  *sub2ImportService
	account  store.Account
	binding  store.Sub2Import
	detail   map[string]any
	routes   []map[string]any
	proxies  map[string]map[string]any
	requests []string
}

func newRecoveryEgressFixture(t *testing.T) *recoveryEgressFixture {
	t.Helper()
	f := &recoveryEgressFixture{
		account: store.Account{ID: 7, Email: "recover@example.test"},
		binding: store.Sub2Import{AccountID: 7, Sub2AccountID: 42, State: "imported", OperationID: "operation", DestinationKey: "fixture"},
		detail: map[string]any{
			"id": 42, "platform": "openai", "type": "oauth",
			"credentials": map[string]any{"email": "recover@example.test"},
			"extra":       map[string]any{"kkai_auth_import": map[string]any{"source_account_id": 7}},
			"egress_mode": "pool", "egress_pool": map[string]any{"route_ids": []int64{10, 20, 30, 40}, "primary_route_id": 20},
		},
		proxies: map[string]map[string]any{},
	}
	for _, id := range []int64{10, 20, 30, 40, 99} {
		f.routes = append(f.routes, map[string]any{"id": id, "kind": "proxy", "protocol": "http", "state": "active", "eligible": true, "proxy_id": id * 10})
		f.proxies[strconv.FormatInt(id*10, 10)] = map[string]any{"protocol": "http", "host": "proxy" + strconv.FormatInt(id, 10) + ".example", "port": 8080, "status": "active"}
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodGet {
			t.Errorf("proxy selection wrote to Sub2: %s %s", r.Method, r.URL.Path)
			http.Error(w, "writes forbidden", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/admin/accounts/42":
			writeRecoveryEnvelope(w, f.detail)
		case r.URL.Path == "/admin/egress-routes/assignable":
			writeRecoveryEnvelope(w, map[string]any{"items": f.routes})
		case strings.HasPrefix(r.URL.Path, "/admin/proxies/"):
			proxy, ok := f.proxies[strings.TrimPrefix(r.URL.Path, "/admin/proxies/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			writeRecoveryEnvelope(w, proxy)
		default:
			t.Errorf("unexpected Sub2 request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)
	f.service = &sub2ImportService{store: &store.Store{}, baseURL: api.URL, adminAPIKey: "fixture-key", destinationKey: "fixture", client: api.Client()}
	return f
}

func TestBoundAccountLoginProxyUsesCurrentPrimaryAndFreshCredentials(t *testing.T) {
	f := newRecoveryEgressFixture(t)
	ctx := withSub2StatusCache(context.Background())
	check := func(_ context.Context, _ string) (login.ProxyCheckResult, error) {
		return login.ProxyCheckResult{Reachable: true}, nil
	}
	proxy, err := f.service.boundAccountLoginProxy(ctx, f.account, f.binding, "http://old-login.example:8080", check)
	if err != nil || proxy != "http://proxy20.example:8080" {
		t.Fatalf("did not use the current Sub2 primary: proxy=%q err=%v", proxy, err)
	}
	// Sub2 can change the primary and rotate its credentials between logins.
	f.detail["egress_pool"].(map[string]any)["primary_route_id"] = 30
	f.proxies["300"]["username"], f.proxies["300"]["password"] = "current-user", "fresh-secret"
	proxy, err = f.service.boundAccountLoginProxy(ctx, f.account, f.binding, proxy, check)
	if err != nil || proxy != "http://current-user:fresh-secret@proxy30.example:8080" {
		t.Fatal("relogin reused the old exit or stale proxy credentials")
	}
	f.proxies["300"]["password"] = "rotated-secret"
	proxy, err = f.service.boundAccountLoginProxy(ctx, f.account, f.binding, proxy, check)
	if err != nil || proxy != "http://current-user:rotated-secret@proxy30.example:8080" {
		t.Fatal("proxy credentials were not fetched again before relogin")
	}
}

func TestBoundAccountLoginProxyPrefersCurrentPool(t *testing.T) {
	for _, tc := range []struct {
		name               string
		unavailablePrimary bool
		wantChecks         []string
		wantProxy          string
	}{
		{name: "failed primary then first configured backup", wantChecks: []string{"http://proxy20.example:8080", "http://proxy10.example:8080"}, wantProxy: "http://proxy10.example:8080"},
		{name: "skip unavailable and failed backups", unavailablePrimary: true, wantChecks: []string{"http://proxy10.example:8080", "http://proxy30.example:8080", "http://proxy40.example:8080"}, wantProxy: "http://proxy40.example:8080"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecoveryEgressFixture(t)
			if tc.unavailablePrimary {
				f.routes[1]["state"], f.routes[1]["eligible"] = "inactive", false
			}
			var checked []string
			check := func(_ context.Context, proxy string) (login.ProxyCheckResult, error) {
				checked = append(checked, proxy)
				if proxy == "http://proxy30.example:8080" {
					return login.ProxyCheckResult{}, errors.New("connection refused")
				}
				return login.ProxyCheckResult{Reachable: proxy == tc.wantProxy}, nil
			}
			proxy, err := f.service.boundAccountLoginProxy(context.Background(), f.account, f.binding, "http://old-login.example:8080", check)
			if err != nil || proxy != tc.wantProxy || !reflect.DeepEqual(checked, tc.wantChecks) {
				t.Fatalf("unexpected recovery exit: proxy=%q checks=%v err=%v", proxy, checked, err)
			}
			for _, request := range f.requests {
				if request == "GET /admin/proxies/990" || tc.unavailablePrimary && request == "GET /admin/proxies/200" {
					t.Fatalf("selected an unbound or unavailable exit: %v", f.requests)
				}
			}
		})
	}
}

func TestBoundAccountLoginProxyAllUnavailableRequiresAction(t *testing.T) {
	for _, tc := range []string{"catalog unavailable", "proxy authentication failed"} {
		t.Run(tc, func(t *testing.T) {
			f := newRecoveryEgressFixture(t)
			if tc == "catalog unavailable" {
				f.routes[0]["kind"] = "direct"
				f.routes[1]["state"], f.routes[1]["eligible"] = "inactive", false
				f.routes[2]["protocol"] = "socks5"
				f.routes = append(f.routes[:3], f.routes[4:]...) // Deleted route 40; unrelated route 99 remains healthy.
			}
			checks := 0
			check := func(_ context.Context, _ string) (login.ProxyCheckResult, error) {
				checks++
				return login.ProxyCheckResult{Reachable: false, HTTPStatus: http.StatusProxyAuthRequired}, nil
			}
			proxy, err := f.service.boundAccountLoginProxy(context.Background(), f.account, f.binding, "http://old-login.example:8080", check)
			var failure *recoveryOperationError
			if proxy != "" || !errors.As(err, &failure) || failure.Code != "egress_unavailable" || !failure.RequiresAction {
				t.Fatalf("unavailable exits did not stop login: proxy=%q err=%v", proxy, err)
			}
			if tc == "catalog unavailable" && checks != 0 {
				t.Fatalf("unusable routes reached the connectivity check: %d", checks)
			}
		})
	}
}

func TestBoundAccountLoginProxyTransientFailuresPersistRetry(t *testing.T) {
	for _, path := range []string{"/admin/accounts/42", "/admin/egress-routes/assignable", "/admin/proxies/"} {
		for _, tc := range []struct {
			name       string
			status     int
			transport  error
			code       string
			wantAction bool
		}{
			{name: "rate limit", status: http.StatusTooManyRequests, code: "rate_limited"},
			{name: "unavailable", status: http.StatusServiceUnavailable, code: "upstream_error"},
			{name: "timeout", transport: context.DeadlineExceeded, code: "timeout"},
			{name: "authentication", status: http.StatusUnauthorized, code: "sub2_auth_rejected", wantAction: true},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				f := newRecoveryEgressFixture(t)
				base := f.service.client.Transport
				f.service.client.Transport = recoveryConcurrencyTransport(func(r *http.Request) (*http.Response, error) {
					if strings.HasPrefix(r.URL.Path, path) {
						if tc.transport != nil {
							return nil, tc.transport
						}
						return &http.Response{StatusCode: tc.status, Header: http.Header{"Retry-After": []string{"3600"}}, Body: io.NopCloser(strings.NewReader("secret upstream body"))}, nil
					}
					return base.RoundTrip(r)
				})
				checks := 0
				proxy, err := f.service.boundAccountLoginProxy(context.Background(), f.account, f.binding, "http://unbound.example:8080", func(context.Context, string) (login.ProxyCheckResult, error) {
					checks++
					return login.ProxyCheckResult{Reachable: true}, nil
				})
				failure := classifyRecoveryError(err)
				if err == nil || proxy != "" || checks != 0 || failure.Code != tc.code || failure.RequiresAction != tc.wantAction || strings.Contains(err.Error(), "secret") {
					t.Fatalf("classification lost or unbound fallback used: proxy=%q checks=%d error=%+v", proxy, checks, failure)
				}
				recovery := newRecoveryFixture(t, false)
				started := time.Now()
				recovery.service.recordRecoveryFailure(recovery.task, store.RecoveryLoggingIn, err)
				got := recovery.state(t)
				if got.RequiresAction != tc.wantAction || got.ErrorCode != tc.code {
					t.Fatalf("durable failure classification changed: %+v", got)
				}
				if !tc.wantAction {
					if got.RetryAction != "relogin" || got.NextRetryAt == nil || !got.NextRetryAt.After(started) {
						t.Fatalf("transient egress failure has no retry: %+v", got)
					}
					if tc.status != 0 && got.NextRetryAt.Before(started.Add(3599*time.Second)) {
						t.Fatalf("Retry-After was lost: %+v", got)
					}
				}
			})
		}
	}
}

func TestBoundAccountLoginProxyLegacyAndAbsentEgress(t *testing.T) {
	for _, tc := range []struct {
		name       string
		proxyID    any
		withFields bool
		wantProxy  string
		wantError  bool
	}{
		{name: "old DTO preserves saved proxy", wantProxy: "http://saved.example:8080"},
		{name: "legacy proxy ID resolves current route", proxyID: 300, withFields: true, wantProxy: "http://proxy30.example:8080"},
		{name: "explicit direct cannot use AUTH server IP", withFields: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecoveryEgressFixture(t)
			delete(f.detail, "egress_mode")
			delete(f.detail, "egress_pool")
			if tc.withFields {
				f.detail["egress_mode"], f.detail["proxy_id"] = "legacy", tc.proxyID
			}
			check := func(_ context.Context, _ string) (login.ProxyCheckResult, error) {
				return login.ProxyCheckResult{Reachable: true}, nil
			}
			proxy, err := f.service.boundAccountLoginProxy(context.Background(), f.account, f.binding, "http://saved.example:8080", check)
			if tc.wantError {
				var failure *recoveryOperationError
				if proxy != "" || !errors.As(err, &failure) || failure.Code != "egress_unavailable" || !failure.RequiresAction {
					t.Fatalf("explicit direct should stop login: proxy=%q err=%v", proxy, err)
				}
			} else if err != nil || proxy != tc.wantProxy {
				t.Fatalf("legacy compatibility failed: proxy=%q err=%v", proxy, err)
			}
		})
	}
}

func TestRecoveryLoginUsesCurrentSub2EgressAndKeepsRemoteEgressUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name      string
		wantProxy string
	}{
		{name: "current primary", wantProxy: "http://primary.example:8080"},
		{name: "reachable backup", wantProxy: "http://backup.example:8080"},
		{name: "IPv4 fallback", wantProxy: "direct"},
		{name: "all exits unreachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecoveryFixture(t, true)
			const savedProxy = "http://old-login.example:8080"
			if _, err := f.store.UpsertCredentials(context.Background(), store.Credentials{Email: f.account.Email, Password: "password", Proxy: savedProxy}); err != nil {
				t.Fatal(err)
			}
			writes := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes++
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("read recovery mutation: %v", err)
					}
					for _, field := range []string{"egress_mode", "egress_pool", "proxy_id"} {
						if bytes.Contains(body, []byte(`"`+field+`"`)) {
							t.Errorf("recovery changed Sub2 egress in %s %s: field %s", r.Method, r.URL.Path, field)
						}
					}
					r.Body = io.NopCloser(bytes.NewReader(body))
				}
				switch r.URL.Path {
				case "/admin/egress-routes/assignable":
					writeRecoveryEnvelope(w, map[string]any{"items": []any{
						map[string]any{"id": 10, "kind": "proxy", "protocol": "http", "state": "active", "eligible": true, "proxy_id": 100},
						map[string]any{"id": 20, "kind": "proxy", "protocol": "http", "state": "active", "eligible": true, "proxy_id": 200},
					}})
				case "/admin/proxies/100", "/admin/proxies/200":
					host := "backup.example"
					if r.URL.Path == "/admin/proxies/200" {
						host = "primary.example"
					}
					writeRecoveryEnvelope(w, map[string]any{"protocol": "http", "host": host, "port": 8080, "status": "active"})
				case "/admin/accounts/901":
					recorder := httptest.NewRecorder()
					f.handle(recorder, r)
					var envelope struct {
						Data map[string]any `json:"data"`
					}
					if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
						t.Errorf("decode account fixture: %v", err)
					}
					envelope.Data["egress_mode"] = "pool"
					envelope.Data["egress_pool"] = map[string]any{"route_ids": []int64{10, 20}, "primary_route_id": 20}
					writeRecoveryEnvelope(w, envelope.Data)
				default:
					f.handle(w, r)
				}
			}))
			t.Cleanup(api.Close)
			f.service.sub2.baseURL, f.service.sub2.client = api.URL, api.Client()
			var checked []string
			f.service.checkLoginProxy = func(_ context.Context, proxy string) (login.ProxyCheckResult, error) {
				checked = append(checked, proxy)
				return login.ProxyCheckResult{Reachable: proxy == tc.wantProxy}, nil
			}
			f.service.loginWithProxies = func(_ context.Context, email, _, _, proxy, upstream string) (*login.LoginResult, error) {
				f.loginCalls++
				if proxy != tc.wantProxy || upstream != "" {
					t.Fatalf("recovery login did not receive the selected Sub2 proxy: proxy=%q upstream=%q", proxy, upstream)
				}
				return &login.LoginResult{Email: email, AccessToken: "new-at", RefreshToken: "new-rt", ChatGPTAccountID: "workspace", PlanType: "plus"}, nil
			}
			f.service.process(f.task)
			state := f.state(t)
			credentials, err := f.store.GetCredentialsByID(context.Background(), f.account.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantProxy == "" {
				if f.loginCalls != 0 || f.applyCalls != 0 || state.ErrorCode != "network_error" || state.RequiresAction || state.NextRetryAt == nil || state.RetryAction != "relogin" || credentials.Proxy != savedProxy {
					t.Fatalf("unavailable egress did not stop before login: state=%+v login=%d apply=%d", state, f.loginCalls, f.applyCalls)
				}
				return
			}
			if state.State != store.RecoveryCompleted || f.loginCalls != 1 || f.applyCalls != 1 || writes == 0 || credentials.Proxy != tc.wantProxy {
				t.Fatalf("recovery failed to persist selected exit: state=%+v login=%d apply=%d writes=%d", state, f.loginCalls, f.applyCalls, writes)
			}
			wantChecks := []string{"http://primary.example:8080"}
			if tc.wantProxy != wantChecks[0] {
				wantChecks = append(wantChecks, "http://backup.example:8080")
			}
			if tc.wantProxy == "direct" {
				wantChecks = append(wantChecks, "direct")
			}
			if !reflect.DeepEqual(checked, wantChecks) {
				t.Fatalf("unexpected recovery exit order: %v", checked)
			}
		})
	}
}

func TestBoundAccountLoginProxyDirectFallbackCancellationAndFailure(t *testing.T) {
	for _, mode := range []string{"unreachable", "canceled", "fallback canceled"} {
		t.Run(mode, func(t *testing.T) {
			f := newRecoveryEgressFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			directChecks := 0
			proxy, err := f.service.boundAccountLoginProxy(ctx, f.account, f.binding, "", func(checkCtx context.Context, proxy string) (login.ProxyCheckResult, error) {
				if proxy == "direct" {
					directChecks++
					if _, ok := checkCtx.Deadline(); !ok {
						t.Fatal("fallback probe is unbounded")
					}
					if mode == "fallback canceled" {
						cancel()
						return login.ProxyCheckResult{Reachable: true}, nil
					}
				}
				if mode == "canceled" {
					cancel()
				}
				return login.ProxyCheckResult{}, nil
			})
			if err == nil || proxy != "" || mode == "canceled" && directChecks != 0 || mode != "canceled" && directChecks != 1 {
				t.Fatalf("unsafe fallback: proxy=%q err=%v directChecks=%d", proxy, err, directChecks)
			}
		})
	}
}
