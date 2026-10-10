package login

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// LoginErrorInfo is the safe, structured form of a login failure. Message is
// bounded and redacted; it never contains credentials or an upstream body.
type LoginErrorInfo struct {
	Stage         string `json:"stage"`
	Code          string `json:"code"`
	HTTPStatus    int    `json:"http_status,omitempty"`
	Retryable     bool   `json:"retryable"`
	Message       string `json:"message"`
	AccountStatus string `json:"account_status,omitempty"`
}

const (
	LoginStageUnknown       = "unknown"
	LoginStageAuthorize     = "authorize"
	LoginStageEmail         = "email"
	LoginStagePassword      = "password"
	LoginStageMFA           = "mfa"
	LoginStageConsent       = "consent"
	LoginStageToken         = "token"
	LoginStageIdentity      = "identity"
	LoginStageBrowser       = "browser"
	LoginStageProxy         = "proxy"
	LoginStageChallenge     = "challenge"
	LoginStageConfiguration = "configuration"
)

const (
	LoginErrorUnknown                  = "login_failed"
	LoginErrorCloudflareChallenge      = "cloudflare_challenge"
	LoginErrorUnsupportedRegion        = "unsupported_region"
	LoginErrorConnectionReset          = "connection_reset"
	LoginErrorTimeout                  = "timeout"
	LoginErrorCanceled                 = "canceled"
	LoginErrorAuthHTTPStatus           = "auth_http_status"
	LoginErrorUnexpectedPage           = "unexpected_auth_page"
	LoginErrorOAuth                    = "oauth_error"
	LoginErrorTokenExchange            = "token_exchange"
	LoginErrorIdentity                 = "identity_error"
	LoginErrorConfiguration            = "invalid_configuration"
	LoginErrorBrowserRuntimePermission = "browser_runtime_permission"
)

var (
	emailInError  = regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)
	secretInError = regexp.MustCompile(`(?i)(password|passwd|token|secret|code|authorization|cookie|credential|api[_-]?key|totp|verifier)(\s*[:=]\s*)[^,;\s)]+`)
	jwtInError    = regexp.MustCompile(`\b[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}\b`)
	urlInError    = regexp.MustCompile(`https?://[^\s"'<>]+`)
	authCode      = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)
)

// DescribeError classifies err and returns only safe details suitable for an
// HTTP response or structured log. It preserves errors.Is/errors.As behavior
// on the original error; callers should use the original error for control
// flow and this value for presentation.
func DescribeError(err error) LoginErrorInfo {
	if err == nil {
		return LoginErrorInfo{Stage: LoginStageUnknown, Code: LoginErrorUnknown, Message: ""}
	}
	var pathErr *os.PathError
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) &&
		errors.As(err, &pathErr) && pathErr.Op == "fork/exec" && errors.Is(pathErr, fs.ErrPermission) {
		return LoginErrorInfo{
			Stage: LoginStageBrowser, Code: LoginErrorBrowserRuntimePermission,
			Message: "浏览器运行环境权限不足，请联系管理员检查 Node 与驱动权限",
		}
	}
	info := LoginErrorInfo{
		Stage:   classifyLoginStage(err),
		Code:    classifyLoginCode(err),
		Message: safeLoginMessage(err),
	}
	statusAccountStatus := ""
	var statusErr *authHTTPStatusError
	if errors.As(err, &statusErr) {
		info.HTTPStatus = statusErr.Status
		info.Retryable = statusErr.retryable()
		statusAccountStatus = accountStatusForAuthError(statusErr.UpstreamCode, statusErr.Message)
		if statusErr.UpstreamCode != "" && !errors.Is(err, ErrCloudflareChallenge) {
			if code := normalizeAuthCode(statusErr.UpstreamCode); code != "" {
				info.Code = code
			}
		}
		if statusErr.Stage != "" {
			info.Stage = statusErr.Stage
		}
	}
	if errors.Is(err, ErrCloudflareChallenge) {
		info.Retryable = true
	}
	if retryableLoginError(err) {
		info.Retryable = true
	}
	var rejection *authRejectionError
	if errors.As(err, &rejection) {
		if code := normalizeAuthCode(rejection.Code); code != "" {
			info.Code = code
		}
		if rejection.Stage != "" {
			info.Stage = rejection.Stage
		}
		if message := sanitizeLoginText(rejection.Message); message != "" {
			info.Message = message
		}
	}
	if statusAccountStatus != "" {
		info.AccountStatus = statusAccountStatus
	} else {
		info.AccountStatus = accountStatusForAuthError(info.Code, info.Message)
	}
	if info.AccountStatus != "" {
		info.Retryable = false
	}
	if info.Stage == "" {
		info.Stage = LoginStageUnknown
	}
	if info.Code == "" {
		info.Code = LoginErrorUnknown
	}
	return info
}

func classifyLoginCode(err error) string {
	var rejection *authRejectionError
	if errors.As(err, &rejection) {
		if code := normalizeAuthCode(rejection.Code); code != "" {
			return code
		}
		return LoginErrorOAuth
	}
	switch {
	case errors.Is(err, ErrCloudflareChallenge):
		return LoginErrorCloudflareChallenge
	case errors.Is(err, ErrUnsupportedRegion):
		return LoginErrorUnsupportedRegion
	case errors.Is(err, ErrAuthConnectionReset):
		return LoginErrorConnectionReset
	case errors.Is(err, ErrAuthTransientNetwork):
		return LoginErrorConnectionReset
	case errors.Is(err, context.DeadlineExceeded):
		return LoginErrorTimeout
	case errors.Is(err, context.Canceled):
		return LoginErrorCanceled
	case errors.Is(err, ErrUnexpectedAuthPage):
		return LoginErrorUnexpectedPage
	}
	var statusErr *authHTTPStatusError
	if errors.As(err, &statusErr) {
		return LoginErrorAuthHTTPStatus
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "代理"), strings.Contains(text, "proxy"):
		return LoginErrorConfiguration
	case strings.Contains(text, "oauth"):
		return LoginErrorOAuth
	case strings.Contains(text, "token"):
		return LoginErrorTokenExchange
	case strings.Contains(text, "身份"), strings.Contains(text, "jwt"), strings.Contains(text, "identity"):
		return LoginErrorIdentity
	default:
		return LoginErrorUnknown
	}
}

func classifyLoginStage(err error) string {
	var rejection *authRejectionError
	if errors.As(err, &rejection) && rejection.Stage != "" {
		return rejection.Stage
	}
	var statusErr *authHTTPStatusError
	if errors.As(err, &statusErr) {
		if statusErr.Stage != "" {
			return statusErr.Stage
		}
		if stage := authStageForPath(statusErr.Path); stage != LoginStageUnknown {
			return stage
		}
	}
	text := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, ErrCloudflareChallenge), strings.Contains(text, "cloudflare"), strings.Contains(text, "真人"):
		return LoginStageChallenge
	case errors.Is(err, ErrUnsupportedRegion), strings.Contains(text, "region"), strings.Contains(text, "territory"):
		return LoginStageAuthorize
	case strings.Contains(text, "邮箱"), strings.Contains(text, "email"):
		return LoginStageEmail
	case strings.Contains(text, "密码"), strings.Contains(text, "password"):
		return LoginStagePassword
	case strings.Contains(text, "totp"), strings.Contains(text, "mfa"), strings.Contains(text, "二次验证"):
		return LoginStageMFA
	case strings.Contains(text, "consent"), strings.Contains(text, "授权"):
		return LoginStageConsent
	case strings.Contains(text, "token"):
		return LoginStageToken
	case strings.Contains(text, "身份"), strings.Contains(text, "jwt"), strings.Contains(text, "identity"):
		return LoginStageIdentity
	case strings.Contains(text, "代理"), strings.Contains(text, "proxy"), errors.Is(err, ErrAuthConnectionReset), errors.Is(err, ErrAuthTransientNetwork):
		return LoginStageProxy
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return LoginStageBrowser
	default:
		return LoginStageUnknown
	}
}

func safeLoginMessage(err error) string {
	if err == nil {
		return ""
	}
	var rejection *authRejectionError
	if errors.As(err, &rejection) {
		if message := sanitizeLoginText(rejection.Message); message != "" {
			return message
		}
		return "OAuth 登录失败"
	}
	var statusErr *authHTTPStatusError
	if errors.As(err, &statusErr) {
		if errors.Is(err, ErrCloudflareChallenge) {
			return "浏览器验证未通过，已纳入自动重试"
		}
		if message := sanitizeLoginText(statusErr.Message); message != "" {
			return message
		}
		return fmt.Sprintf("OpenAI 认证服务返回 HTTP %d", statusErr.Status)
	}
	if errors.Is(err, ErrCloudflareChallenge) {
		return "浏览器验证未通过，已纳入自动重试"
	}
	if errors.Is(err, ErrUnsupportedRegion) {
		return ErrUnsupportedRegion.Error()
	}
	if errors.Is(err, ErrAuthConnectionReset) {
		return ErrAuthConnectionReset.Error()
	}
	if errors.Is(err, ErrAuthTransientNetwork) {
		return "认证站暂时无法连接，请稍后重试"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "登录请求超时"
	}
	if errors.Is(err, context.Canceled) {
		return "登录请求已取消"
	}
	if errors.Is(err, ErrUnexpectedAuthPage) {
		return "未进入预期认证页面"
	}
	// Browser and transport libraries may include credentials or response
	// snippets in their errors. Do not expose arbitrary library text.
	switch classifyLoginCode(err) {
	case LoginErrorConfiguration:
		return "代理配置无效"
	case LoginErrorOAuth:
		return "OAuth 登录失败"
	case LoginErrorTokenExchange:
		return "OAuth token 交换失败"
	case LoginErrorIdentity:
		return "OAuth 身份信息无效"
	default:
		return "登录流程失败"
	}
}

func sanitizeLoginText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	text = emailInError.ReplaceAllString(text, "[email]")
	text = jwtInError.ReplaceAllString(text, "[jwt]")
	text = secretInError.ReplaceAllString(text, "$1$2[redacted]")
	text = urlInError.ReplaceAllStringFunc(text, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return "[url]"
		}
		return u.Scheme + "://" + u.Hostname() + u.EscapedPath()
	})
	if len(text) > 512 {
		text = text[:512]
		for len(text) > 0 && !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		text = strings.TrimSpace(text) + "…"
	}
	return text
}

// authResponseMessage extracts only the allowlisted error fields and bounds
// the result. A non-JSON body is deliberately discarded.
func authResponseMessage(body []byte) string {
	_, message := authResponseDetails(body)
	return message
}

func authResponseDetails(body []byte) (string, string) {
	var failure struct {
		Error            json.RawMessage `json:"error"`
		Code             string          `json:"code"`
		Type             string          `json:"type"`
		ErrorDescription string          `json:"error_description"`
		Message          string          `json:"message"`
	}
	if err := json.Unmarshal(body, &failure); err != nil {
		return "", ""
	}
	parts := []string{failure.ErrorDescription, failure.Message}
	var errorText string
	var nestedCode, nestedType string
	if len(failure.Error) > 0 {
		var text string
		if json.Unmarshal(failure.Error, &text) == nil {
			errorText = text
		} else {
			var nested struct {
				Code    string `json:"code"`
				Type    string `json:"type"`
				Message string `json:"message"`
			}
			if json.Unmarshal(failure.Error, &nested) == nil {
				nestedCode, nestedType = nested.Code, nested.Type
				parts = append(parts, nested.Message)
			}
		}
	}
	parts = append([]string{errorText}, parts...)
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			clean = append(clean, part)
		}
	}
	code := ""
	for _, candidate := range []string{failure.Code, nestedCode, errorText, nestedType, failure.Type} {
		if code = normalizeAuthCode(candidate); code != "" {
			break
		}
	}
	return code, sanitizeLoginText(strings.Join(clean, ": "))
}

func accountStatusForAuthError(code, message string) string {
	const deletedOrDeactivated = "you do not have an account because it has been deleted or deactivated."
	if strings.Contains(strings.ToLower(strings.Join(strings.Fields(message), " ")), deletedOrDeactivated) {
		return "deleted_or_deactivated"
	}
	switch normalizeAuthCode(code) {
	case "account_deleted", "user_deleted":
		return "deleted"
	case "account_deactivated", "user_deactivated", "account_disabled", "user_disabled":
		return "deactivated"
	}
	return ""
}

func normalizeAuthCode(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if !authCode.MatchString(raw) {
		return ""
	}
	return raw
}

func authStageForPath(path string) string {
	path = strings.ToLower(path)
	switch {
	case strings.Contains(path, "/oauth/token"):
		return LoginStageToken
	case strings.Contains(path, "/mfa"):
		return LoginStageMFA
	case strings.Contains(path, "/workspace/select"), strings.Contains(path, "/consent"):
		return LoginStageConsent
	case strings.Contains(path, "/password/verify"):
		return LoginStagePassword
	case strings.Contains(path, "/api/accounts/authorize/continue"), strings.Contains(path, "/log-in"):
		return LoginStageEmail
	case strings.Contains(path, "/api/oauth/oauth2/auth"), strings.Contains(path, "/oauth/authorize"):
		return LoginStageAuthorize
	default:
		return LoginStageUnknown
	}
}
