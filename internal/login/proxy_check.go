package login

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"syscall"
	"time"
)

const proxyCheckURL = "https://auth.openai.com/log-in"

// ProxyCheckResult describes a connectivity check without exposing proxy
// credentials or response bodies.
type ProxyCheckResult struct {
	Reachable  bool   `json:"reachable"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Message    string `json:"message"`
	ElapsedMS  int64  `json:"elapsed_ms"`
	Mode       string `json:"mode"`
}

// CheckProxy tests the configured/request proxy with one bounded HTTP request.
// An empty proxy uses the service configuration and otherwise checks direct.
func (s *Service) CheckProxy(ctx context.Context, proxy string) (ProxyCheckResult, error) {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		proxy = strings.TrimSpace(s.config.Proxy)
	}
	upstream := strings.TrimSpace(s.config.UpstreamProxy)
	return checkProxyURL(ctx, proxyCheckURL, proxy, upstream)
}

func checkProxyURL(parent context.Context, target, proxy, upstream string) (ProxyCheckResult, error) {
	started := time.Now()
	result := ProxyCheckResult{Mode: "direct"}
	finish := func() ProxyCheckResult {
		result.ElapsedMS = time.Since(started).Milliseconds()
		return result
	}
	if err := ValidateHTTPProxy(proxy); err != nil {
		return finish(), err
	}
	if err := ValidateHTTPProxy(upstream); err != nil {
		return finish(), errors.New("前置代理无效")
	}
	if upstream != "" && proxy == "" {
		return finish(), errors.New("填写前置代理时还需要填写 HTTP 出口代理")
	}

	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	result, checkErr := checkProxyURLOnce(ctx, target, proxy, upstream)
	if checkErr == nil || upstream != "" || !isConnectionResetError(checkErr) || !shouldTryLocalClash(proxy, ErrAuthConnectionReset) || !localProxyAvailable(ctx) {
		result.ElapsedMS = time.Since(started).Milliseconds()
		return result, nil
	}

	// Match login's narrowly scoped fallback: only a connection reset may use
	// the local Clash listener, and only after the configured proxy failed.
	result, _ = checkProxyURLOnce(ctx, target, proxy, localClashProxy)
	result.ElapsedMS = time.Since(started).Milliseconds()
	return result, nil
}

func checkProxyURLOnce(ctx context.Context, target, proxy, upstream string) (ProxyCheckResult, error) {
	result := ProxyCheckResult{Mode: "direct"}
	effectiveProxy := proxy
	var closeRelay func()
	if upstream != "" {
		relay, stop, err := startProxyRelay(proxy, upstream)
		if err != nil {
			result.Reachable = false
			result.Message = "代理中转启动失败"
			return result, err
		}
		effectiveProxy, closeRelay = relay, stop
		defer closeRelay()
	}
	if effectiveProxy != "" {
		proxyURL, err := parseHTTPProxy(effectiveProxy)
		if err != nil {
			return result, err
		}
		result.Mode = "proxy"
		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		defer transport.CloseIdleConnections()
		return doProxyCheck(ctx, target, result, transport)
	}
	// Explicit Proxy:nil prevents ambient HTTP(S)_PROXY variables from changing
	// the meaning of an empty proxy field.
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	return doProxyCheck(ctx, target, result, transport)
}

func doProxyCheck(ctx context.Context, target string, result ProxyCheckResult, transport *http.Transport) (ProxyCheckResult, error) {
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		result.Message = "认证站地址无效"
		return result, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := client.Do(req)
	if err != nil {
		result.Message = proxyCheckErrorMessage(err)
		return result, err
	}
	defer resp.Body.Close()
	result.HTTPStatus = resp.StatusCode
	if resp.StatusCode == http.StatusProxyAuthRequired {
		result.Message = "代理认证失败（HTTP 407）"
		return result, nil
	}
	result.Reachable = true
	if resp.StatusCode == http.StatusForbidden {
		result.Message = "网络可达，认证站返回 HTTP 403；不代表登录一定成功"
	} else {
		result.Message = "网络可达，认证站返回 HTTP " + resp.Status
	}
	return result, nil
}

func proxyCheckErrorMessage(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || (err != nil && strings.Contains(strings.ToLower(err.Error()), "timeout")) {
		return "代理连接超时"
	}
	if isConnectionResetError(err) {
		return "代理连接被重置"
	}
	return "无法连接认证站，请检查代理地址和网络"
}

func isConnectionResetError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "connection reset") || strings.Contains(text, "connection was reset")
}
