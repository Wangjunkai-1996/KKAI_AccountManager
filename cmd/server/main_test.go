package main

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func TestHTTPServerTimeoutPolicy(t *testing.T) {
	server := newHTTPServer("127.0.0.1:0", http.NewServeMux())
	if server.ReadHeaderTimeout != 10*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s, want 10s", server.ReadHeaderTimeout)
	}
	if server.IdleTimeout != 120*time.Second {
		t.Fatalf("IdleTimeout = %s, want 120s", server.IdleTimeout)
	}
	if server.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %s, want zero for SSE", server.WriteTimeout)
	}
}

func TestReadinessChecksDatabase(t *testing.T) {
	oldHistory, oldImporter := loginHistory, sub2Importer
	t.Cleanup(func() { loginHistory, sub2Importer = oldHistory, oldImporter })
	loginHistory = nil
	sub2Importer = nil
	recorder := httptest.NewRecorder()
	handleReadiness(recorder, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("uninitialized readiness status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}

	history, _ := testOAuthStore(t)
	loginHistory = history
	recorder = httptest.NewRecorder()
	handleReadiness(recorder, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ready" || body["database_ready"] != true {
		t.Fatalf("unexpected readiness body: %#v", body)
	}
	if err := history.Close(); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	handleReadiness(recorder, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed database readiness status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestLoginHTTPStatusDeadline(t *testing.T) {
	if got := loginHTTPStatus(context.DeadlineExceeded); got != http.StatusGatewayTimeout {
		t.Fatalf("loginHTTPStatus(deadline) = %d, want %d", got, http.StatusGatewayTimeout)
	}
}

func TestConcurrentLoginLimitAndRelease(t *testing.T) {
	slots := make(chan struct{}, 1)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	handler := limitConcurrentLogins(slots, func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusInternalServerError)
	})
	done := make(chan struct{})
	go func() {
		handler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/login", nil))
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first request did not acquire its slot")
	}
	busy := httptest.NewRecorder()
	handler(busy, httptest.NewRequest(http.MethodPost, "/api/login", nil))
	if busy.Code != http.StatusTooManyRequests || busy.Header().Get("Retry-After") != "5" {
		t.Fatalf("busy response = %d, Retry-After %q", busy.Code, busy.Header().Get("Retry-After"))
	}
	unblock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("first request did not finish")
	}
	if len(slots) != 0 {
		t.Fatal("slot was not released after a failed request")
	}
	next := httptest.NewRecorder()
	handler(next, httptest.NewRequest(http.MethodPost, "/api/login", nil))
	if next.Code != http.StatusInternalServerError || len(slots) != 0 {
		t.Fatalf("subsequent request = %d, occupied slots = %d", next.Code, len(slots))
	}
}

func TestBusyLoginPreservesConcurrentSlot(t *testing.T) {
	oldSlots := loginSlots
	t.Cleanup(func() { loginSlots = oldSlots })
	loginSlots = make(chan struct{}, 1)
	loginSlots <- struct{}{}
	validBody := `{"email":"test@example.com","password":"test-password","totp_secret":"JBSWY3DPEHPK3PXP"}`
	req := httptest.NewRequest(http.MethodPost, "/api/login/stream", strings.NewReader(validBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleLoginStream(nil)(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("busy status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	if len(loginSlots) != 1 {
		t.Fatal("busy request changed occupied login slots")
	}
}

func TestLoginRequestGuards(t *testing.T) {
	oldSlots := loginSlots
	t.Cleanup(func() { loginSlots = oldSlots })
	loginSlots = make(chan struct{}, 1)
	loginSlots <- struct{}{}
	validBody := `{"email":"test@example.com","password":"test-password","totp_secret":"JBSWY3DPEHPK3PXP"}`
	tests := []struct {
		name        string
		contentType string
		body        string
		status      int
	}{
		{"missing content type", "", validBody, http.StatusUnsupportedMediaType},
		{"malformed JSON", "application/json", "{", http.StatusBadRequest},
		{"invalid account", "application/json", `{}`, http.StatusBadRequest},
		{"oversized body", "application/json", `{"password":"` + strings.Repeat("x", 64<<10) + `"}`, http.StatusRequestEntityTooLarge},
		{"trailing JSON", "application/json", validBody + `{}`, http.StatusBadRequest},
		{"full concurrency", "application/json; charset=utf-8", validBody, http.StatusTooManyRequests},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			recorder := httptest.NewRecorder()
			handleLogin(nil)(recorder, req)
			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.status, recorder.Body.String())
			}
			if len(loginSlots) != 1 {
				t.Fatal("rejected request changed occupied login slots")
			}
		})
	}
}

func TestClientIPIgnoresForwardedIP(t *testing.T) {
	for i, expected := range []int{http.StatusBadRequest, http.StatusBadRequest} {
		req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{}`))
		req.RemoteAddr = "[::1]:12345"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", []string{"192.0.2.1", "192.0.2.2"}[i])
		req.Header.Set("X-Real-IP", "192.0.2.3")
		if ip := getClientIP(req); ip != "::1" {
			t.Fatalf("client IP = %q", ip)
		}
		recorder := httptest.NewRecorder()
		handleLogin(nil)(recorder, req)
		if recorder.Code != expected {
			t.Fatalf("request %d status = %d, want %d", i+1, recorder.Code, expected)
		}
	}
}

func TestLoginEndpointsHaveNoCumulativeRequestLimit(t *testing.T) {
	history := newTestHistory(t)
	oldSlots, oldImporter := loginSlots, sub2Importer
	t.Cleanup(func() { loginSlots, sub2Importer = oldSlots, oldImporter })
	loginSlots = make(chan struct{}, 1)
	sub2Importer = nil
	legacyFlag := flag.Lookup("rate-limit")
	if legacyFlag == nil {
		t.Fatal("legacy rate-limit flag must remain accepted")
	}
	oldFlag := legacyFlag.Value.String()
	t.Cleanup(func() { _ = legacyFlag.Value.Set(oldFlag) })
	if err := legacyFlag.Value.Set("1"); err != nil {
		t.Fatal(err)
	}
	// Stop at local proxy validation after admission, without starting a browser.
	account, err := history.UpsertCredentials(context.Background(), store.Credentials{
		Email: "batch@example.com", Password: "password-123", TOTPSecret: "JBSWY3DPEHPK3PXP", Proxy: "socks5://proxy.invalid:1234",
	})
	if err != nil {
		t.Fatal(err)
	}
	newBody := `{"email":"batch@example.com","password":"password-123","totp_secret":"JBSWY3DPEHPK3PXP","proxy":"socks5://proxy.invalid:1234"}`
	for _, tt := range []struct {
		path    string
		body    string
		handler http.HandlerFunc
	}{
		{"/api/login", newBody, handleLogin(nil)},
		{"/api/login/stream", newBody, handleLoginStream(nil)},
		{"/api/history/login", `{"account_id":` + itoa(account.ID) + `}`, handleHistoryLogin(nil)},
	} {
		t.Run(tt.path, func(t *testing.T) {
			for i := 0; i < 35; i++ {
				req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				tt.handler(rec, req)
				if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "代理配置无效") || rec.Header().Get("Retry-After") != "" {
					t.Fatalf("request %d = %d %s, Retry-After=%q", i+1, rec.Code, rec.Body.String(), rec.Header().Get("Retry-After"))
				}
				if len(loginSlots) != 0 {
					t.Fatal("completed request retained its concurrency slot")
				}
			}
			loginSlots <- struct{}{}
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			tt.handler(rec, req)
			<-loginSlots
			if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), `"code":"platform_busy"`) {
				t.Fatalf("busy request = %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestSameOriginRequests(t *testing.T) {
	for _, origin := range []string{"", "http://127.0.0.1:8080", "https://evil.example", "null", "http://127.0.0.1:8080@evil.example", "http://127.0.0.1:8080/path"} {
		t.Run(origin, func(t *testing.T) {
			called := false
			handler := corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/login", nil)
			req.Header.Set("Origin", origin)
			recorder := httptest.NewRecorder()
			handler(recorder, req)
			allowed := origin == "" || origin == "http://127.0.0.1:8080"
			if called != allowed {
				t.Fatalf("handler called = %v, allowed = %v", called, allowed)
			}
			if !allowed && recorder.Code != http.StatusForbidden {
				t.Fatalf("rejected origin status = %d", recorder.Code)
			}
			if recorder.Header().Get("Access-Control-Allow-Origin") == "*" {
				t.Fatal("wildcard CORS header was set")
			}
		})
	}
}
