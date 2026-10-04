package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
)

func TestServeLoginStreamSuccessQueuesBeforeResult(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/login/stream", nil)
	rec := httptest.NewRecorder()
	serveLoginStream(rec, req, func(ctx context.Context) (*login.LoginResult, error) {
		if ctx == nil {
			t.Fatal("runner received a nil context")
		}
		return &login.LoginResult{Email: "user@example.com", AccessToken: "access", RefreshToken: "refresh"}, nil
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content type = %q, want text/event-stream", got)
	}
	if rec.Header().Get("Cache-Control") != "no-cache, no-store" {
		t.Fatalf("cache control = %q", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatalf("X-Accel-Buffering = %q", rec.Header().Get("X-Accel-Buffering"))
	}
	body := rec.Body.String()
	queued := strings.Index(body, `"stage":"queued"`)
	result := strings.Index(body, `"type":"result"`)
	if queued < 0 || result < 0 || queued > result {
		t.Fatalf("queued event was not sent before result: %q", body)
	}
	if !strings.Contains(body, `"success":true`) {
		t.Fatalf("success result missing: %q", body)
	}
	// The UI copies the refresh token (the rt value), so keep the complete
	// result payload available to history re-login consumers.
	if !strings.Contains(body, `"AccessToken":"access"`) || !strings.Contains(body, `"RefreshToken":"refresh"`) {
		t.Fatalf("login result tokens missing: %q", body)
	}
}

func TestServeLoginStreamFailureUsesStructuredSafeResult(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/login/stream", nil)
	rec := httptest.NewRecorder()
	serveLoginStream(rec, req, func(context.Context) (*login.LoginResult, error) {
		return nil, errors.New("password=super-secret-token")
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("stream HTTP status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"type":"result"`) || !strings.Contains(body, `"success":false`) {
		t.Fatalf("structured failure result missing: %q", body)
	}
	if strings.Contains(body, "super-secret-token") {
		t.Fatalf("failure stream leaked sensitive error text: %q", body)
	}
	if !strings.Contains(body, `"error":`) || !strings.Contains(body, `"code":`) {
		t.Fatalf("structured error details missing: %q", body)
	}
}

func TestServeLoginStreamCancellationWaitsForRunnerCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/login/stream", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	runnerStarted := make(chan struct{})
	runnerCleaned := make(chan struct{})
	handlerDone := make(chan struct{})
	go func() {
		serveLoginStream(rec, req, func(ctx context.Context) (*login.LoginResult, error) {
			close(runnerStarted)
			<-ctx.Done()
			close(runnerCleaned)
			return nil, ctx.Err()
		})
		close(handlerDone)
	}()
	select {
	case <-runnerStarted:
	case <-time.After(time.Second):
		t.Fatal("stream runner did not start")
	}
	cancel()
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("stream handler did not return after cancellation")
	}
	select {
	case <-runnerCleaned:
	default:
		t.Fatal("stream handler returned before runner cleanup")
	}
}

func TestHandleLoginStreamValidationStaysJSON(t *testing.T) {
	oldLimiter, oldSlots := limiter, loginSlots
	t.Cleanup(func() {
		limiter, loginSlots = oldLimiter, oldSlots
	})
	limiter = newRateLimiter(100, 10*time.Minute)
	loginSlots = make(chan struct{}, 1)

	badType := httptest.NewRequest(http.MethodPost, "/api/login/stream", strings.NewReader(`{}`))
	badType.Header.Set("Content-Type", "text/plain")
	badTypeRecorder := httptest.NewRecorder()
	handleLoginStream(nil)(badTypeRecorder, badType)
	if badTypeRecorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("bad content type status = %d", badTypeRecorder.Code)
	}
	if strings.HasPrefix(badTypeRecorder.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatal("validation error incorrectly used SSE content type")
	}

	valid := `{"email":"test@example.com","password":"test-password","totp_secret":"JBSWY3DPEHPK3PXP"}`
	loginSlots <- struct{}{}
	busy := httptest.NewRequest(http.MethodPost, "/api/login/stream", strings.NewReader(valid))
	busy.Header.Set("Content-Type", "application/json")
	busyRecorder := httptest.NewRecorder()
	handleLoginStream(nil)(busyRecorder, busy)
	if busyRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("busy status = %d, want %d", busyRecorder.Code, http.StatusTooManyRequests)
	}
	if strings.HasPrefix(busyRecorder.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatal("busy response incorrectly used SSE content type")
	}
}

func TestReserveReturnsRemainingWaitWithoutConsumingRejection(t *testing.T) {
	const ip = "192.0.2.10"
	rl := newRateLimiter(1, time.Minute)
	acceptedAt := time.Now().Add(-25 * time.Second)
	rl.requests[ip] = []time.Time{acceptedAt.Add(-time.Minute), acceptedAt}
	for i := 0; i < 3; i++ {
		wait := rl.reserve(ip)
		if wait <= 30*time.Second || wait > 35*time.Second {
			t.Fatalf("remaining wait = %s, want about 35s", wait)
		}
		if got := rl.requests[ip]; len(got) != 1 || !got[0].Equal(acceptedAt) {
			t.Fatalf("rejection consumed or extended the accepted request: %v", got)
		}
	}
	rl.requests[ip][0] = time.Now().Add(-time.Minute - time.Second)
	if wait := rl.reserve(ip); wait != 0 || len(rl.requests[ip]) != 1 {
		t.Fatalf("expired reservation was not replaced: wait=%s history=%v", wait, rl.requests[ip])
	}
}

func TestLoginRejectionsCarryJSONRetryAfter(t *testing.T) {
	oldLimiter, oldSlots := limiter, loginSlots
	t.Cleanup(func() { limiter, loginSlots = oldLimiter, oldSlots })
	valid := `{"email":"test@example.com","password":"test-password","totp_secret":"JBSWY3DPEHPK3PXP"}`
	for _, code := range []string{"platform_rate_limit", "platform_busy"} {
		t.Run(code, func(t *testing.T) {
			limiter = newRateLimiter(1, time.Minute)
			loginSlots = make(chan struct{}, 1)
			req := httptest.NewRequest(http.MethodPost, "/api/login/stream", strings.NewReader(valid))
			req.Header.Set("Content-Type", "application/json")
			if code == "platform_rate_limit" {
				limiter.reserve(getClientIP(req))
			} else {
				loginSlots <- struct{}{}
			}
			rec := httptest.NewRecorder()
			handleLoginStream(nil)(rec, req)
			var result LoginResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatalf("rejection is not JSON: %v", err)
			}
			if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("response = %d %q", rec.Code, rec.Header().Get("Content-Type"))
			}
			if result.Code != code || result.Success || result.RetryAfter <= 0 {
				t.Fatalf("rejection metadata = %#v", result)
			}
			if rec.Header().Get("Retry-After") != strconv.Itoa(result.RetryAfter) {
				t.Fatalf("Retry-After header and body disagree: %q vs %d", rec.Header().Get("Retry-After"), result.RetryAfter)
			}
			if code == "platform_busy" && (result.RetryAfter != 5 || len(loginSlots) != 1) {
				t.Fatalf("busy retry delay or slot changed: delay=%d slots=%d", result.RetryAfter, len(loginSlots))
			}
		})
	}
}

func TestProxyCheckJSONGuardsPreserveLoginBudget(t *testing.T) {
	oldLimiter, oldProxyLimiter := limiter, proxyCheckLimiter
	t.Cleanup(func() { limiter, proxyCheckLimiter = oldLimiter, oldProxyLimiter })
	limiter = newRateLimiter(1, time.Minute)
	proxyCheckLimiter = newRateLimiter(20, time.Minute)
	service := login.NewService(login.Config{})
	invalidProxy := `{"proxy":"socks5://proxy.invalid:1234"}`
	tests := []struct {
		name        string
		contentType string
		body        string
		status      int
		message     string
	}{
		{"missing content type", "", invalidProxy, http.StatusUnsupportedMediaType, "application/json"},
		{"wrong content type", "text/plain", invalidProxy, http.StatusUnsupportedMediaType, "application/json"},
		{"malformed JSON", "application/json", "{", http.StatusBadRequest, "请求格式错误"},
		{"trailing JSON", "application/json", invalidProxy + `{}`, http.StatusBadRequest, "一个 JSON 对象"},
		{"trailing garbage", "application/json", invalidProxy + `garbage`, http.StatusBadRequest, "一个 JSON 对象"},
		{"invalid proxy", "application/json; charset=utf-8", invalidProxy, http.StatusBadRequest, "代理配置"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/proxy-check", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			rec := httptest.NewRecorder()
			handleProxyCheck(service)(rec, req)
			var result LoginResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatalf("proxy-check rejection is not JSON: %v", err)
			}
			if rec.Code != tt.status || !strings.Contains(result.Message, tt.message) {
				t.Fatalf("response = %d %q, want %d containing %q", rec.Code, result.Message, tt.status, tt.message)
			}
			if len(limiter.requests) != 0 {
				t.Fatal("proxy check consumed the login rate limit")
			}
		})
	}
	const ip = "192.0.2.1"
	if wait := limiter.reserve(ip); wait != 0 {
		t.Fatalf("proxy checks consumed the login budget: remaining wait %s", wait)
	}
	if len(proxyCheckLimiter.requests) == 0 {
		t.Fatal("proxy checks did not use their own limiter")
	}
}
