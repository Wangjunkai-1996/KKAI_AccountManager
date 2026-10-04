// Package probe checks AUTH's current access token with one bounded model request.
// It never refreshes credentials or changes account state.
package probe

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	Model           = "gpt-5.6-luna"
	ProtocolVersion = 1
	endpoint        = "https://chatgpt.com/backend-api/codex/responses"
	clientVersion   = "0.146.0"
	requestTimeout  = 20 * time.Second
	errorLimit      = 64 << 10
	eventLimit      = 256 << 10
	responseLimit   = 1 << 20
)

// Result contains only safe metadata. Zero HTTP values mean no such response.
type Result struct {
	Outcome           string
	HTTPStatus        int
	ProxyHTTPStatus   int
	StreamErrorStatus int
	ErrorCode         string
	StreamErrorCode   string
	Message           string
	FailureStage      string
	RetryAfterSeconds int
	DurationMS        int64
	RequestAttempted  bool
}

type Client struct {
	http  *http.Client
	proxy bool
}

type proxyStatusKey struct{}

// NewClient uses an explicit HTTP proxy, or IPv4 direct when proxy is empty.
// The caller decides whether an absent default proxy is an invalid configuration.
func NewClient(proxy string) (*Client, error) {
	var proxyURL *url.URL
	if strings.TrimSpace(proxy) != "" {
		var err error
		proxyURL, err = url.Parse(strings.TrimSpace(proxy))
		if err != nil || proxyURL.Scheme != "http" || proxyURL.Hostname() == "" || proxyURL.Opaque != "" || (proxyURL.Path != "" && proxyURL.Path != "/") || proxyURL.RawQuery != "" || proxyURL.Fragment != "" {
			return nil, errors.New("检测仅支持有效的 HTTP 代理地址")
		}
		if port := proxyURL.Port(); port != "" {
			p, err := strconv.Atoi(port)
			if err != nil || p < 1 || p > 65535 {
				return nil, errors.New("HTTP 代理端口无效")
			}
		}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if proxyURL == nil {
				network = "tcp4"
			}
			return dialer.DialContext(ctx, network, address)
		},
		TLSHandshakeTimeout:    8 * time.Second,
		ResponseHeaderTimeout:  15 * time.Second,
		MaxResponseHeaderBytes: errorLimit,
		// Each probe is a single small POST. Fresh connections also avoid the
		// transport's retries of requests on stale pooled connections.
		DisableKeepAlives: true,
		OnProxyConnectResponse: func(ctx context.Context, _ *url.URL, _ *http.Request, response *http.Response) error {
			if status, ok := ctx.Value(proxyStatusKey{}).(*atomic.Int32); ok {
				status.Store(int32(response.StatusCode))
			}
			return nil
		},
	}
	if proxyURL != nil {
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &Client{http: &http.Client{
		Transport:     transport,
		Timeout:       requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, proxy: proxyURL != nil}, nil
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

// AccessTokenExpired examines only a reliable positive integer exp from this AT.
// Opaque/malformed tokens and unknown exp remain eligible for an upstream check.
// This is metadata parsing, not JWT signature or identity validation.
func AccessTokenExpired(token string, now time.Time) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(parts[1]) > errorLimit {
		return false
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		Exp json.RawMessage `json:"exp"`
	}
	if json.Unmarshal(body, &claims) != nil {
		return false
	}
	exp, err := strconv.ParseInt(string(claims.Exp), 10, 64)
	return err == nil && exp > 0 && exp <= now.Unix()-30
}

func (c *Client) Check(parent context.Context, accessToken, chatGPTAccountID string) (result Result) {
	started := time.Now()
	defer func() { result.DurationMS = time.Since(started).Milliseconds() }()
	if strings.TrimSpace(accessToken) == "" {
		return failure("credential_missing", "precheck")
	}
	if strings.TrimSpace(chatGPTAccountID) == "" || strings.ContainsAny(accessToken+chatGPTAccountID, "\r\n") {
		return failure("credential_incomplete", "precheck")
	}
	if AccessTokenExpired(accessToken, started) {
		return failure("access_token_expired", "precheck")
	}
	ctx, cancel := context.WithTimeout(parent, requestTimeout)
	defer cancel()
	var proxyStatus atomic.Int32
	ctx = context.WithValue(ctx, proxyStatusKey{}, &proxyStatus)
	payload := `{"model":"` + Model + `","instructions":"Reply with OK only.","input":[{"role":"user","content":[{"type":"input_text","text":"Reply with OK only."}]}],"stream":true,"store":false}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
	if err != nil {
		return failure("request_rejected", "precheck")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("ChatGPT-Account-Id", chatGPTAccountID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("Originator", "codex-tui")
	req.Header.Set("version", clientVersion)
	req.Header.Set("User-Agent", "codex-tui/"+clientVersion+" (Ubuntu 22.4.0; x86_64) xterm-256color")
	resp, err := c.http.Do(req)
	if err != nil {
		result = transportFailure(ctx, err, c.proxy, int(proxyStatus.Load()))
		result.RequestAttempted = true
		return result
	}
	defer resp.Body.Close()
	if strings.EqualFold(strings.TrimSpace(resp.Header.Get("cf-mitigated")), "challenge") {
		result = failure("upstream_challenge", "target_http")
	} else if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, errorLimit+1))
		if len(body) > errorLimit {
			result = failure("protocol_error", "target_http")
			result.ErrorCode = "response_too_large"
		} else if readErr != nil {
			// The target's HTTP status is already established. A truncated error
			// body cannot erase a real 401 or manufacture a more specific cause.
			result = classifyHTTP(resp.StatusCode, nil)
		} else {
			result = classifyHTTP(resp.StatusCode, body)
		}
	} else {
		contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
		mediaType, _, _ := mime.ParseMediaType(contentType)
		// A sys1 OAuth sample returned a complete SSE body without this header.
		// Missing type may use the same strict parser; an explicitly different
		// type is still rejected, and header absence never relaxes completion.
		if contentType != "" && mediaType != "text/event-stream" {
			result = failure("protocol_error", "target_http")
		} else {
			result = readStream(ctx, resp.Body)
		}
	}
	result.HTTPStatus = resp.StatusCode
	if status := int(proxyStatus.Load()); status != http.StatusOK {
		result.ProxyHTTPStatus = status
	}
	result.RequestAttempted = true
	result.RetryAfterSeconds = retryAfter(resp.Header.Get("Retry-After"), time.Now())
	return result
}

func transportFailure(ctx context.Context, err error, usingProxy bool, proxyStatus int) Result {
	outcome := "network_error"
	var netErr net.Error
	switch {
	case errors.Is(ctx.Err(), context.Canceled), errors.Is(err, context.Canceled):
		outcome = "canceled"
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		outcome = "timeout"
	case proxyStatus != 0 && proxyStatus != 200:
		outcome = "proxy_error"
	case usingProxy:
		// Only failure to connect to the proxy itself is attributed to it;
		// TLS/reset errors after CONNECT stay an inconclusive network error.
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "proxyconnect" {
			outcome = "proxy_error"
		}
	}
	stage := "network"
	if outcome == "proxy_error" {
		stage = "proxy_connect"
	}
	result := failure(outcome, stage)
	if proxyStatus != 200 {
		result.ProxyHTTPStatus = proxyStatus
	}
	return result
}

type upstreamError struct {
	Code       string          `json:"code"`
	Type       string          `json:"type"`
	Message    string          `json:"message"`
	Status     json.RawMessage `json:"status"`
	StatusCode json.RawMessage `json:"status_code"`
	Error      json.RawMessage `json:"error"`
}

func extractError(raw []byte) upstreamError {
	var item upstreamError
	if json.Unmarshal(raw, &item) != nil {
		return upstreamError{}
	}
	if len(item.Error) > 0 && string(item.Error) != "null" {
		var nested upstreamError
		if json.Unmarshal(item.Error, &nested) == nil {
			if nested.Code != "" {
				item.Code = nested.Code
			}
			if nested.Type != "" {
				item.Type = nested.Type
			}
			if nested.Message != "" {
				item.Message = nested.Message
			}
			if len(nested.Status) > 0 {
				item.Status = nested.Status
			}
			if len(nested.StatusCode) > 0 {
				item.StatusCode = nested.StatusCode
			}
		} else {
			var code string
			if json.Unmarshal(item.Error, &code) == nil && item.Code == "" {
				item.Code = code
			}
		}
	}
	return item
}

func classifyHTTP(status int, body []byte) Result {
	// Generic Cloudflare headers or HTML are not evidence of a challenge.
	lower := bytes.ToLower(body)
	if status == http.StatusForbidden && bytes.HasPrefix(bytes.TrimSpace(lower), []byte("<")) && (bytes.Contains(lower, []byte("/cdn-cgi/challenge-platform/")) || bytes.Contains(lower, []byte("cf-chl-"))) {
		return failure("upstream_challenge", "target_http")
	}
	upstream := extractError(body)
	result := classifyError(upstream)
	if result.Outcome == "" {
		outcome := "request_rejected"
		switch {
		case status == 401:
			outcome = "unauthorized"
		case status == 403:
			outcome = "forbidden"
		case status == 429:
			outcome = "rate_limited"
		case status >= 500:
			outcome = "upstream_error"
		}
		result = failure(outcome, "target_http")
		result.ErrorCode = "unknown_upstream_error"
	}
	result.FailureStage = "target_http"
	return result
}

// classifyError uses exact known codes and two narrowly scoped model messages.
// Upstream free text is never returned or persisted.
func classifyError(item upstreamError) Result {
	for _, code := range []string{item.Code, item.Type} {
		code = strings.ToLower(strings.TrimSpace(code))
		outcome := ""
		switch code {
		case "token_expired", "access_token_expired":
			outcome = "access_token_expired"
		case "token_revoked", "token_invalidated":
			outcome = "credential_revoked"
		case "invalid_token", "invalid_api_key", "authentication_error", "unauthorized":
			outcome = "unauthorized"
		case "account_deactivated", "user_deactivated", "account_disabled", "user_disabled":
			outcome = "account_disabled"
		case "account_deleted", "user_deleted":
			outcome = "account_deleted"
		case "deleted_or_deactivated":
			outcome = "account_unavailable"
		case "unsupported_country_region_territory":
			outcome = "region_restricted"
		case "rate_limit_exceeded", "rate_limit_error", "too_many_requests":
			outcome = "rate_limited"
		case "insufficient_quota", "quota_exceeded", "usage_limit_reached":
			outcome = "quota_exhausted"
		case "model_not_found", "model_not_supported", "unsupported_model", "model_access_denied":
			outcome = "model_unavailable"
		case "server_error", "internal_server_error", "server_is_overloaded", "service_unavailable":
			outcome = "upstream_error"
		}
		if outcome != "" {
			r := failure(outcome, "")
			r.ErrorCode = code
			return r
		}
	}
	message := strings.ToLower(strings.Join(strings.Fields(item.Message), " "))
	if strings.Contains(message, "you do not have an account because it has been deleted or deactivated.") {
		r := failure("account_unavailable", "")
		r.ErrorCode = "deleted_or_deactivated"
		return r
	}
	if strings.Contains(message, "model") && (strings.Contains(message, "is not supported when using codex with a chatgpt account") || strings.Contains(message, "does not exist or you do not have access to it")) {
		r := failure("model_unavailable", "")
		r.ErrorCode = "model_unavailable"
		return r
	}
	return Result{}
}

func retryAfter(raw string, now time.Time) int {
	seconds, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		at, err := http.ParseTime(raw)
		if err != nil {
			return 0
		}
		seconds = int(at.Sub(now).Seconds())
	}
	if seconds < 0 {
		return 0
	}
	if seconds > 86400 {
		return 86400
	}
	return seconds
}

func failure(outcome, stage string) Result {
	messages := map[string]string{
		"ok": "本次模型调用正常", "credential_missing": "缺少访问令牌，请重新登录", "credential_incomplete": "本地凭据信息不完整",
		"access_token_expired": "访问令牌已过期，凭据待更新", "unauthorized": "当前访问令牌鉴权失败", "credential_revoked": "当前访问令牌已撤销或失效",
		"account_disabled": "上游报告账号停用", "account_deleted": "上游报告账号已删除", "account_unavailable": "上游报告账号删除或停用，未明确区分",
		"upstream_challenge": "上游要求安全验证，无法判断账号是否失效", "forbidden": "请求被拒绝，原因待确认", "region_restricted": "上游报告地区限制",
		"rate_limited": "当前请求受到限流", "quota_exhausted": "当前额度不足", "model_unavailable": "此账号无法调用指定模型",
		"request_rejected": "检测请求被拒绝，请检查协议兼容性", "upstream_error": "上游暂时异常", "proxy_error": "代理连接或认证失败",
		"network_error": "网络连接失败，凭据状态未确定", "timeout": "检测超时，结果未确认", "canceled": "检测已取消",
		"incomplete": "未取得完整模型调用结果", "protocol_error": "检测响应格式异常或超出限制",
	}
	return Result{Outcome: outcome, FailureStage: stage, Message: messages[outcome]}
}
