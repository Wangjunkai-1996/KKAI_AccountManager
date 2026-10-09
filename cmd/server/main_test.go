package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

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

func TestBusyLoginDoesNotConsumeRateLimit(t *testing.T) {
	oldLimiter, oldSlots := limiter, loginSlots
	t.Cleanup(func() { limiter, loginSlots = oldLimiter, oldSlots })
	limiter = newRateLimiter(1, 10*time.Minute)
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
	if len(limiter.requests) != 0 {
		t.Fatalf("busy request consumed rate-limit budget: %#v", limiter.requests)
	}
}

func TestLoginRequestGuards(t *testing.T) {
	oldLimiter, oldSlots := limiter, loginSlots
	t.Cleanup(func() { limiter, loginSlots = oldLimiter, oldSlots })
	limiter = newRateLimiter(100, 10*time.Minute)
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

func TestLoginRateLimitIgnoresForwardedIP(t *testing.T) {
	oldLimiter := limiter
	t.Cleanup(func() { limiter = oldLimiter })
	limiter = newRateLimiter(1, 10*time.Minute)
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
	if len(limiter.requests) != 0 {
		t.Fatalf("invalid login requests consumed rate-limit budget: %#v", limiter.requests)
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
