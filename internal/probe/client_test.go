package probe

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const completed = `data: {"type":"response.completed","response":{"status":"completed","output":[{"content":[{"type":"output_text","text":"OK"}]}]}}` + "\n\n"

func fixtureClient(t *testing.T, status int, contentType, body string, headers http.Header) *Client {
	t.Helper()
	c, err := NewClient("")
	if err != nil {
		t.Fatal(err)
	}
	c.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		h := headers.Clone()
		if h == nil {
			h = make(http.Header)
		}
		h.Set("Content-Type", contentType)
		return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	t.Cleanup(c.Close)
	return c
}

func TestProbeAccessTokenExpiry(t *testing.T) {
	now := time.Unix(2000000000, 0)
	jwt := func(body string) string { return "e30." + base64.RawURLEncoding.EncodeToString([]byte(body)) + ".sig" }
	for _, tc := range []struct {
		name, token string
		expired     bool
	}{
		{"expired", jwt(`{"exp":1999999969}`), true},
		{"boundary", jwt(`{"exp":1999999970}`), true},
		{"grace", jwt(`{"exp":1999999971}`), false},
		{"future", jwt(`{"exp":2100000000}`), false},
		{"missing", jwt(`{"iat":1}`), false},
		{"zero", jwt(`{"exp":0}`), false},
		{"negative", jwt(`{"exp":-1}`), false},
		{"string", jwt(`{"exp":"1"}`), false},
		{"fraction", jwt(`{"exp":1.5}`), false},
		{"overflow", jwt(`{"exp":9223372036854775808}`), false},
		{"largest", jwt(`{"exp":9223372036854775807}`), false},
		{"opaque", "opaque-access-token", false},
		{"malformed", "a.!bad.sig", false},
		{"unrelated claims", jwt(`{"exp":1,"iat":"unrelated","email":null}`), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AccessTokenExpired(tc.token, now); got != tc.expired {
				t.Fatalf("expired=%v want %v", got, tc.expired)
			}
		})
	}
}

func TestProbeRequestAndLocalPrechecks(t *testing.T) {
	c, err := NewClient("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var calls int
	c.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.String() != endpoint || req.Method != "POST" {
			t.Fatal("unexpected target")
		}
		if req.Header.Get("Authorization") != "Bearer opaque" || req.Header.Get("ChatGPT-Account-Id") != "account-1" {
			t.Fatal("credential header mismatch")
		}
		if req.Header.Get("Originator") != "codex-tui" || req.Header.Get("version") != clientVersion || !strings.HasPrefix(req.Header.Get("User-Agent"), "codex-tui/"+clientVersion+" ") {
			t.Fatal("client identity mismatch")
		}
		if req.Header.Get("Idempotency-Key") != "" || req.Header.Get("X-Idempotency-Key") != "" {
			t.Fatal("unexpected replay headers")
		}
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != Model || payload["stream"] != true || payload["store"] != false || payload["instructions"] == "" {
			t.Fatal("invalid payload")
		}
		deadline, ok := req.Context().Deadline()
		if !ok || time.Until(deadline) > requestTimeout {
			t.Fatal("missing request deadline")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(completed)), Request: req}, nil
	})
	for _, tc := range []struct{ at, account, outcome string }{
		{"", "account-1", "credential_missing"},
		{"opaque", "", "credential_incomplete"},
		{"opaque\ninjected", "account-1", "credential_incomplete"},
		{"e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"exp":1}`)) + ".sig", "account-1", "access_token_expired"},
	} {
		r := c.Check(context.Background(), tc.at, tc.account)
		if r.Outcome != tc.outcome || r.RequestAttempted || r.HTTPStatus != 0 {
			t.Fatalf("unexpected precheck: %+v", r)
		}
	}
	if calls != 0 {
		t.Fatal("precheck sent a request")
	}
	r := c.Check(context.Background(), "opaque", "account-1")
	if r.Outcome != "ok" || !r.RequestAttempted || r.HTTPStatus != 200 || calls != 1 {
		t.Fatalf("unexpected result: %+v calls=%d", r, calls)
	}
}

func TestProbeClassification(t *testing.T) {
	for _, tc := range []struct {
		name                string
		status              int
		body, outcome, code string
		header              http.Header
	}{
		{"unauthorized", 401, `{"error":{"message":"secret-token-should-not-leak"}}`, "unauthorized", "unknown_upstream_error", nil},
		{"expired", 401, `{"error":{"code":"token_expired"}}`, "access_token_expired", "token_expired", nil},
		{"revoked", 401, `{"error":{"code":"token_revoked"}}`, "credential_revoked", "token_revoked", nil},
		{"disabled", 403, `{"error":{"code":"account_deactivated"}}`, "account_disabled", "account_deactivated", nil},
		{"mixed", 403, `{"error":{"message":"You do not have an account because it has been deleted or deactivated."}}`, "account_unavailable", "deleted_or_deactivated", nil},
		{"cf header", 403, "<html>challenge</html>", "upstream_challenge", "", http.Header{"Cf-Mitigated": {"challenge"}}},
		{"cf marker", 403, `<html><script src="/cdn-cgi/challenge-platform/a"></script></html>`, "upstream_challenge", "", nil},
		{"cloudflare alone", 403, "<html>forbidden</html>", "forbidden", "unknown_upstream_error", http.Header{"Server": {"cloudflare"}, "Cf-Ray": {"fixture"}}},
		{"no html marker guess", 403, `{"error":{"message":"cf-chl- text"}}`, "forbidden", "unknown_upstream_error", nil},
		{"region", 403, `{"error":{"code":"unsupported_country_region_territory"}}`, "region_restricted", "unsupported_country_region_territory", nil},
		{"rate", 429, `{}`, "rate_limited", "unknown_upstream_error", http.Header{"Retry-After": {"45"}}},
		{"quota", 429, `{"error":{"code":"insufficient_quota"}}`, "quota_exhausted", "insufficient_quota", nil},
		{"model", 404, `{"error":{"code":"model_not_found"}}`, "model_unavailable", "model_not_found", nil},
		{"model wording", 400, `{"error":{"type":"invalid_request_error","message":"The 'gpt-5.6-luna' model is not supported when using Codex with a ChatGPT account."}}`, "model_unavailable", "model_unavailable", nil},
		{"bad request", 400, `{"error":{"code":"invalid_request_error"}}`, "request_rejected", "unknown_upstream_error", nil},
		{"server", 503, `{}`, "upstream_error", "unknown_upstream_error", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixtureClient(t, tc.status, "application/json", tc.body, tc.header)
			r := c.Check(context.Background(), "opaque", "account-1")
			if r.Outcome != tc.outcome || r.HTTPStatus != tc.status || r.ErrorCode != tc.code || r.StreamErrorStatus != 0 {
				t.Fatalf("unexpected result: %+v", r)
			}
			if strings.Contains(r.Message, "secret") || strings.Contains(r.Message, "gpt-5.6") {
				t.Fatal("raw upstream text leaked")
			}
			if tc.name == "rate" && r.RetryAfterSeconds != 45 {
				t.Fatal("retry after lost")
			}
		})
	}
}

func TestProbeSSE(t *testing.T) {
	for _, tc := range []struct {
		name, body, outcome, streamCode string
		streamStatus                    int
	}{
		{"completed", completed, "ok", "", 0},
		{"delta and complete", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", "ok", "", 0},
		{"frame without newline", strings.TrimRight(completed, "\n"), "ok", "", 0},
		{"multiline CRLF", ": heartbeat\r\nevent: response.completed\r\ndata: {\"response\":{\"status\":\"completed\",\r\ndata: \"output\":[{\"content\":[{\"type\":\"output_text\",\"text\":\"OK\"}]}]}}\r\n\r\n", "ok", "", 0},
		{"created EOF", "data: {\"type\":\"response.created\"}\n\n", "incomplete", "", 0},
		{"done", "data: [DONE]\n\n", "incomplete", "", 0},
		{"empty", "", "incomplete", "", 0},
		{"empty completion", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", "incomplete", "", 0},
		{"failed status", strings.Replace(completed, `"status":"completed"`, `"status":"failed"`, 1), "incomplete", "", 0},
		{"missing status", strings.Replace(completed, `"status":"completed",`, "", 1), "incomplete", "", 0},
		{"revoked nested", "data: {\"type\":\"error\",\"error\":{\"code\":\"token_revoked\",\"status\":401}}\n\n", "credential_revoked", "token_revoked", 401},
		{"expired flat", "data: {\"type\":\"error\",\"code\":\"token_expired\",\"message\":\"do not expose\"}\n\n", "access_token_expired", "token_expired", 0},
		{"failed error", "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_is_overloaded\"}}}\n\n", "upstream_error", "server_is_overloaded", 0},
		{"completed error", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"error\":{\"code\":\"invalid_token\"}}}\n\n", "unauthorized", "invalid_token", 0},
		{"status 401", "data: {\"type\":\"error\",\"status_code\":401}\n\n", "unauthorized", "unknown_upstream_error", 401},
		{"string status", "data: {\"type\":\"error\",\"status_code\":\"401\",\"message\":\"401 secret\"}\n\n", "upstream_error", "unknown_upstream_error", 0},
		{"incomplete error", "data: {\"type\":\"response.incomplete\",\"response\":{\"error\":{\"code\":\"model_not_found\"}}}\n\n", "model_unavailable", "model_not_found", 0},
		{"bad json", "data: {broken}\n\n", "protocol_error", "", 0},
		{"conflicting name", "event: response.failed\n" + completed, "protocol_error", "", 0},
		{"no done alias", strings.ReplaceAll(completed, "response.completed", "response.done"), "incomplete", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixtureClient(t, 200, "text/event-stream; charset=utf-8", tc.body, nil).Check(context.Background(), "opaque", "account-1")
			if r.Outcome != tc.outcome || r.HTTPStatus != 200 || r.StreamErrorCode != tc.streamCode || r.StreamErrorStatus != tc.streamStatus {
				t.Fatalf("unexpected result: %+v", r)
			}
		})
	}
}

func TestProbeResponseLimitsAndUnexpectedContent(t *testing.T) {
	for _, tc := range []struct {
		name, body, ct string
		status         int
	}{
		{"wrong content", completed, "text/html", 200},
		{"oversized error", strings.Repeat("x", errorLimit+1), "application/json", 401},
		{"oversized event", "data: " + strings.Repeat("x", eventLimit+1), "text/event-stream", 200},
		{"oversized stream", strings.Repeat(": "+strings.Repeat("x", 1021)+"\n", 1026), "text/event-stream", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixtureClient(t, tc.status, tc.ct, tc.body, nil).Check(context.Background(), "opaque", "account-1")
			if r.Outcome != "protocol_error" {
				t.Fatalf("unexpected result: %+v", r)
			}
		})
	}
}

func TestProbeMissingContentType(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"complete SSE", completed, true},
		{"HTML", "<html>challenge</html>", false},
		{"JSON", `{"type":"response.completed","response":{"status":"completed","output":[{"content":[{"type":"output_text","text":"OK"}]}]}}`, false},
		{"empty", "", false},
		{"incomplete SSE", "data: {\"type\":\"response.created\"}\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fixtureClient(t, 200, "", tc.body, nil)
			underlying := client.http.Transport
			client.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				response, err := underlying.RoundTrip(req)
				response.Header.Del("Content-Type")
				return response, err
			})
			result := client.Check(context.Background(), "opaque", "account-1")
			if (result.Outcome == "ok") != tc.ok || result.HTTPStatus != 200 {
				t.Fatalf("unexpected missing-type result: %+v", result)
			}
		})
	}
}

func TestProbeRedirectCanceledAndDeadline(t *testing.T) {
	c := fixtureClient(t, 302, "text/event-stream", completed, http.Header{"Location": {"https://other.invalid/credentials"}})
	var calls int
	underlying := c.http.Transport
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) { calls++; return underlying.RoundTrip(r) })
	r := c.Check(context.Background(), "opaque", "account-1")
	if r.Outcome != "request_rejected" || r.HTTPStatus != 302 || calls != 1 {
		t.Fatalf("redirect followed: %+v calls=%d", r, calls)
	}
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := c.Check(ctx, "opaque", "account-1"); got.Outcome != "canceled" {
		t.Fatalf("cancel outcome: %+v", got)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if got := c.Check(ctx, "opaque", "account-1"); got.Outcome != "timeout" {
		t.Fatalf("timeout outcome: %+v", got)
	}
}

func TestProbeProxy407AndNoEnvironmentProxy(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "CONNECT" || r.Host != "chatgpt.com:443" {
			t.Error("unexpected proxy target")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("target bearer leaked to proxy")
		}
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer server.Close()
	t.Setenv("HTTPS_PROXY", server.URL)
	t.Setenv("HTTP_PROXY", server.URL)
	t.Setenv("ALL_PROXY", server.URL)
	direct, err := NewClient("")
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	if direct.http.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("direct inherits environment proxy")
	}
	c, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := c.Check(context.Background(), "opaque", "account-1")
	if r.Outcome != "proxy_error" || r.ProxyHTTPStatus != 407 || r.HTTPStatus != 0 || calls.Load() != 1 {
		t.Fatalf("unexpected proxy result: %+v calls=%d", r, calls.Load())
	}
}

func TestProbeProxyValidation(t *testing.T) {
	for _, value := range []string{"https://proxy.invalid:80", "socks5://proxy.invalid:80", "http://proxy.invalid:0", "http://proxy.invalid:99999", "http://proxy.invalid:80/path", "http://proxy.invalid:80?token=secret", "http://", "http://proxy.invalid:abc"} {
		if c, err := NewClient(value); err == nil {
			c.Close()
			t.Fatal("accepted invalid proxy")
		}
	}
	for _, value := range []string{"http://proxy.invalid:80", "http://user:password@127.0.0.1:7269", "http://proxy.invalid/"} {
		c, err := NewClient(value)
		if err != nil {
			t.Fatal("rejected valid proxy")
		}
		c.Close()
	}
	if got := retryAfter(strconv.Itoa(99999999), time.Now()); got != 86400 {
		t.Fatal("retry bound missing")
	}
}

type fragmentReader struct{ io.Reader }

func (r fragmentReader) Read(p []byte) (int, error) { return r.Reader.Read(p[:1]) }

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("sensitive transport detail") }

func TestProbeFragmentedStreamAndTruncatedHTTPError(t *testing.T) {
	c, err := NewClient("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(fragmentReader{strings.NewReader(completed)}), Request: req}, nil
	})
	if got := c.Check(context.Background(), "opaque", "account-1"); got.Outcome != "ok" {
		t.Fatalf("fragmented response: %+v", got)
	}
	c.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Header: http.Header{}, Body: io.NopCloser(brokenReader{}), Request: req}, nil
	})
	got := c.Check(context.Background(), "opaque", "account-1")
	if got.Outcome != "unauthorized" || got.HTTPStatus != 401 || strings.Contains(got.Message, "sensitive") {
		t.Fatalf("truncated 401: %+v", got)
	}
}
