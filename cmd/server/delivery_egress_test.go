package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func TestResolveEgressRoutesKeepsOrderAndCredentialsServerSide(t *testing.T) {
	var requested []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "fixture" {
			t.Errorf("missing admin API key")
		}
		requested = append(requested, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/admin/egress-routes/assignable":
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"items": []any{
				map[string]any{"id": 10, "kind": "proxy", "name": "first", "protocol": "http", "state": "active", "eligible": true, "proxy_id": 100},
				map[string]any{"id": 20, "kind": "proxy", "name": "second", "protocol": "http", "state": "active", "eligible": true, "proxy_id": 200},
			}}})
		case "/admin/proxies/100":
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"protocol": "http", "host": "first.example", "port": 8080, "username": "user", "password": "p@ss:word", "status": "active"}})
		case "/admin/proxies/200":
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"protocol": "http", "host": "second.example", "port": 8081, "status": "active"}})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	s := &sub2ImportService{store: &store.Store{}, baseURL: api.URL, adminAPIKey: "fixture", client: api.Client()}
	routes, err := s.resolveEgressRoutes(context.Background(), []int64{20, 10})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual([]int64{routes[0].RouteID, routes[1].RouteID}, []int64{20, 10}) {
		t.Fatalf("route order changed: %+v", routes)
	}
	if routes[0].ProxyURL != "http://second.example:8081" || routes[1].ProxyURL != "http://user:p%40ss%3Aword@first.example:8080" {
		t.Fatal("proxy URL lost credentials or selection order")
	}
	if routes[1].RouteKey != "http|first.example|8080|user|p@ss:word" {
		t.Fatal("portable Sub2 proxy key does not match its import format")
	}
	if !reflect.DeepEqual(requested, []string{"/admin/egress-routes/assignable", "/admin/proxies/200", "/admin/proxies/100"}) {
		t.Fatalf("unexpected request sequence: %v", requested)
	}
	previous := sub2Importer
	sub2Importer = s
	t.Cleanup(func() { sub2Importer = previous })
	req := LoginRequest{Email: "test@example.com", Password: "test-password", TotpSecret: "JBSWY3DPEHPK3PXP", Proxy: "invalid manual proxy", DeliveryOptions: &store.DeliveryOptions{EgressRouteIDs: []int64{10, 20}, Priority: 1, Concurrency: 3}}
	if err := validateLoginIdentity(&req); err != nil {
		t.Fatal(err)
	}
	if err := applyLoginEgress(context.Background(), &req); err != nil {
		t.Fatal(err)
	}
	if req.Proxy != routes[1].ProxyURL || req.UpstreamProxy != "" {
		t.Fatal("login did not use first selected Sub2 proxy")
	}
	if err := validateLoginRequest(&req); err != nil {
		t.Fatalf("selected route should override an unused manual proxy: %v", err)
	}
}

func TestAccountLoginProxyFallsBackToDirectAfterStaleSavedExit(t *testing.T) {
	for _, tc := range []struct {
		name            string
		saved           string
		savedReachable  bool
		savedHTTPStatus int
		directReachable bool
		want            string
		wantError       bool
		wantCalls       []string
	}{
		{name: "reachable saved exit", saved: "http://saved.example:32102", savedReachable: true, want: "http://saved.example:32102", wantCalls: []string{"http://saved.example:32102"}},
		{name: "stale exit falls back to direct", saved: "http://saved.example:32102", directReachable: true, want: "direct", wantCalls: []string{"http://saved.example:32102", "direct"}},
		{name: "proxy auth is explicit", saved: "http://saved.example:32102", savedHTTPStatus: http.StatusProxyAuthRequired, directReachable: true, wantError: true, wantCalls: []string{"http://saved.example:32102"}},
		{name: "empty saved exit", want: "", wantCalls: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			history, accountID := testOAuthStore(t)
			account, err := history.GetAccountByID(context.Background(), accountID)
			if err != nil {
				t.Fatal(err)
			}
			s := &sub2ImportService{store: history, baseURL: "http://sub2.example", adminAPIKey: "fixture", destinationKey: "fixture"}
			var calls []string
			check := func(_ context.Context, proxy string) (login.ProxyCheckResult, error) {
				calls = append(calls, proxy)
				if proxy == tc.saved && tc.savedHTTPStatus != 0 {
					return login.ProxyCheckResult{HTTPStatus: tc.savedHTTPStatus}, nil
				}
				if proxy == tc.saved {
					if tc.savedReachable {
						return login.ProxyCheckResult{Reachable: true}, nil
					}
					return login.ProxyCheckResult{}, errors.New("saved exit failed")
				}
				if tc.directReachable {
					return login.ProxyCheckResult{Reachable: true}, nil
				}
				return login.ProxyCheckResult{}, errors.New("direct failed")
			}
			got, err := s.accountLoginProxy(context.Background(), account, &store.DeliveryOptions{Priority: 1, Concurrency: 3}, tc.saved, check)
			if tc.wantError {
				if err == nil || classifyRecoveryError(err).Code != "egress_unavailable" {
					t.Fatalf("error=%v, want egress_unavailable", err)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("proxy=%q err=%v, want %q", got, err, tc.want)
			}
			if !reflect.DeepEqual(calls, tc.wantCalls) {
				t.Fatalf("probe calls=%v, want %v", calls, tc.wantCalls)
			}
		})
	}
}

func TestAccountLoginProxyMissingBindingAlsoUsesDirectFallback(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "unbound", true: "remote-missing"}[missing], func(t *testing.T) {
			history, accountID := testOAuthStore(t)
			ctx := context.Background()
			if missing {
				binding, err := history.LinkSub2Account(ctx, "fixture", accountID, 42, "linked-fixture")
				if err != nil {
					t.Fatal(err)
				}
				if err := history.MarkSub2AccountMissing(ctx, binding); err != nil {
					t.Fatal(err)
				}
			}
			account, err := history.GetAccountByID(ctx, accountID)
			if err != nil {
				t.Fatal(err)
			}
			s := &sub2ImportService{store: history, baseURL: "http://sub2.example", adminAPIKey: "fixture", destinationKey: "fixture"}
			calls := []string{}
			got, err := s.accountLoginProxy(ctx, account, &store.DeliveryOptions{Priority: 1, Concurrency: 3}, "http://stale.example:32102", func(_ context.Context, proxy string) (login.ProxyCheckResult, error) {
				calls = append(calls, proxy)
				if proxy == "direct" {
					return login.ProxyCheckResult{Reachable: true}, nil
				}
				return login.ProxyCheckResult{}, errors.New("saved tunnel failed")
			})
			if err != nil || got != "direct" || !reflect.DeepEqual(calls, []string{"http://stale.example:32102", "direct"}) {
				t.Fatalf("missing=%v proxy=%q calls=%v err=%v", missing, got, calls, err)
			}
		})
	}
}

func TestResolveEgressRoutesRejectsUnusableWithoutLeakingSecrets(t *testing.T) {
	for _, tc := range []struct {
		name    string
		route   map[string]any
		proxy   map[string]any
		wantGET bool
	}{
		{name: "direct route", route: map[string]any{"id": 1, "kind": "direct", "state": "active", "eligible": true}},
		{name: "inactive route", route: map[string]any{"id": 1, "kind": "proxy", "protocol": "http", "state": "inactive", "eligible": false, "proxy_id": 7}},
		{name: "unsupported protocol", route: map[string]any{"id": 1, "kind": "proxy", "protocol": "socks5", "state": "active", "eligible": true, "proxy_id": 7}},
		{name: "inactive proxy", route: map[string]any{"id": 1, "kind": "proxy", "protocol": "http", "state": "active", "eligible": true, "proxy_id": 7}, proxy: map[string]any{"protocol": "http", "host": "proxy.example", "port": 8080, "username": "user", "password": "secret", "status": "inactive"}, wantGET: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxyGET := false
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/admin/egress-routes/assignable" {
					json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"items": []any{tc.route}}})
					return
				}
				proxyGET = true
				json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": tc.proxy})
			}))
			defer api.Close()
			s := &sub2ImportService{store: &store.Store{}, baseURL: api.URL, adminAPIKey: "fixture", client: api.Client()}
			_, err := s.resolveEgressRoutes(context.Background(), []int64{1})
			if err == nil || strings.Contains(err.Error(), "secret") || proxyGET != tc.wantGET {
				t.Fatalf("unsafe route: err=%v proxyGET=%v", err, proxyGET)
			}
		})
	}
}

func TestLoadDefaultLoginDeliveryOptionsResolvesPrimary(t *testing.T) {
	history, accountID := testOAuthStore(t)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/admin/egress-routes/assignable":
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"items": []any{map[string]any{"id": 3, "kind": "proxy", "protocol": "http", "state": "active", "eligible": true, "proxy_id": 30}}}})
		case "/admin/proxies/30":
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"protocol": "http", "host": "default.example", "port": 8080, "status": "active"}})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	s := &sub2ImportService{store: history, baseURL: api.URL, adminAPIKey: "fixture", destinationKey: "fixture", client: api.Client()}
	defaults := store.DeliveryOptions{GroupIDs: []int64{}, EgressRouteIDs: []int64{3}, Priority: 1, Concurrency: 3, Merchant: "merchant", Deadline: "2026-12-31"}
	if err := history.SaveDeliveryDefaults(context.Background(), s.destinationKey, defaults); err != nil {
		t.Fatal(err)
	}
	previous := sub2Importer
	sub2Importer = s
	t.Cleanup(func() { sub2Importer = previous })
	req := LoginRequest{AutoDeliver: true, Proxy: "direct"}
	if err := loadDefaultLoginDeliveryOptions(context.Background(), &req); err != nil {
		t.Fatal(err)
	}
	if req.DeliveryOptions == nil || !reflect.DeepEqual(req.DeliveryOptions.EgressRouteIDs, []int64{3}) {
		t.Fatalf("saved defaults not loaded: %+v", req.DeliveryOptions)
	}
	if err := applyLoginEgress(context.Background(), &req); err != nil {
		t.Fatal(err)
	}
	if req.Proxy != "http://default.example:8080" {
		t.Fatal("login did not use the default selected exit")
	}
	loginOnly := LoginRequest{}
	if err := loadDefaultLoginDeliveryOptions(context.Background(), &loginOnly); err != nil {
		t.Fatal(err)
	}
	if err := applyLoginEgress(context.Background(), &loginOnly); err != nil || loginOnly.Proxy != req.Proxy {
		t.Fatalf("login-only request lost the saved default proxy: %v", err)
	}
	account, err := history.GetAccountByID(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := s.accountLoginProxy(context.Background(), account, nil, "http://old.example:8080", nil)
	if err != nil || proxy != req.Proxy {
		t.Fatalf("unbound history login did not refresh the default proxy: %v", err)
	}
	for _, explicit := range []LoginRequest{
		{Proxy: "direct"},
		{DeliveryOptions: &store.DeliveryOptions{Priority: 1, Concurrency: 3}},
	} {
		if err := loadDefaultLoginDeliveryOptions(context.Background(), &explicit); err != nil {
			t.Fatal(err)
		}
		if explicit.DeliveryOptions != nil && len(explicit.DeliveryOptions.EgressRouteIDs) > 0 {
			t.Fatal("saved proxy overrides an explicit empty selection or direct login")
		}
	}
}

func TestAccountLoginProxyPreservesProbeCancellationAndTimeout(t *testing.T) {
	history, accountID := testOAuthStore(t)
	account, err := history.GetAccountByID(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	s := &sub2ImportService{store: history, baseURL: "http://sub2.example", adminAPIKey: "fixture", destinationKey: "fixture"}
	for _, tc := range []struct {
		name, want string
		ctx        func() (context.Context, context.CancelFunc)
	}{
		{name: "timeout", want: "timeout", ctx: func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), time.Millisecond)
		}},
		{name: "canceled", want: "interrupted", ctx: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, func() {}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := tc.ctx()
			defer cancel()
			_, err := s.accountLoginProxy(ctx, account, &store.DeliveryOptions{Priority: 1, Concurrency: 3}, "http://stale.example:32102", func(ctx context.Context, _ string) (login.ProxyCheckResult, error) {
				<-ctx.Done()
				return login.ProxyCheckResult{}, ctx.Err()
			})
			if err == nil || classifyRecoveryError(err).Code != tc.want {
				t.Fatalf("error=%v, want %s", err, tc.want)
			}
		})
	}
}

func TestHistoryDeliveryOptionsAvailableBeforeRelogin(t *testing.T) {
	history, accountID := testOAuthStore(t)
	options := store.DeliveryOptions{EgressRouteIDs: []int64{7, 8}, Priority: 1, Concurrency: 3, Merchant: "merchant", Deadline: "2026-12-31"}
	if _, err := history.QueueAccountDelivery(context.Background(), accountID, "fixture", options); err != nil {
		t.Fatal(err)
	}
	importer := &sub2ImportService{store: history, destinationKey: "fixture"}
	loaded, err := importer.historyDeliveryOptions(context.Background(), accountID)
	if err != nil || loaded == nil || !reflect.DeepEqual(loaded.EgressRouteIDs, options.EgressRouteIDs) {
		t.Fatalf("history relogin lost its delivery route: %+v, %v", loaded, err)
	}
}
