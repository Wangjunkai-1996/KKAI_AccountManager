package login

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestDescribeErrorAuthStatus(t *testing.T) {
	err := &authHTTPStatusError{
		Status:  http.StatusForbidden,
		Path:    "auth.openai.com/api/accounts/authorize/continue",
		Message: `invalid account_password=super-secret email=alice@example.com`,
	}
	info := DescribeError(err)
	if info.Stage != LoginStageEmail || info.Code != LoginErrorAuthHTTPStatus {
		t.Fatalf("DescribeError() = %#v, want email/auth_http_status", info)
	}
	if info.HTTPStatus != http.StatusForbidden || info.Retryable {
		t.Fatalf("status/retryable = %d/%v, want 403/false", info.HTTPStatus, info.Retryable)
	}
	if strings.Contains(info.Message, "super-secret") || strings.Contains(info.Message, "alice@example.com") {
		t.Fatalf("safe message leaked secret: %q", info.Message)
	}
}

func TestDescribeErrorClassifiesSentinels(t *testing.T) {
	for _, tt := range []struct {
		name, stage, code string
		err               error
	}{
		{"challenge", LoginStageChallenge, LoginErrorCloudflareChallenge, ErrCloudflareChallenge},
		{"region", LoginStageAuthorize, LoginErrorUnsupportedRegion, ErrUnsupportedRegion},
		{"reset", LoginStageProxy, LoginErrorConnectionReset, ErrAuthConnectionReset},
		{"timeout", LoginStageBrowser, LoginErrorTimeout, context.DeadlineExceeded},
		{"canceled", LoginStageBrowser, LoginErrorCanceled, context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			info := DescribeError(tt.err)
			if info.Stage != tt.stage || info.Code != tt.code {
				t.Fatalf("DescribeError() = %#v, want stage=%q code=%q", info, tt.stage, tt.code)
			}
		})
	}
}

func TestDescribeErrorBrowserRuntimePermission(t *testing.T) {
	permissionErr := fmt.Errorf("playwright 启动失败: %w", &os.PathError{
		Op: "fork/exec", Path: "/private/runtime/node", Err: fs.ErrPermission,
	})
	for _, tt := range []struct {
		name, code    string
		err           error
		wantRetryable bool
	}{
		{"driver execute denied", LoginErrorBrowserRuntimePermission, permissionErr, false},
		{"cancellation wins", LoginErrorCanceled, errors.Join(context.Canceled, permissionErr), false},
		{"timeout wins", LoginErrorTimeout, errors.Join(context.DeadlineExceeded, permissionErr), true},
		{"database open denied", LoginErrorUnknown, &os.PathError{Op: "open", Path: "/private/accounts.db", Err: fs.ErrPermission}, false},
		{"plain permission", LoginErrorUnknown, fs.ErrPermission, false},
		{"driver missing", LoginErrorUnknown, &os.PathError{Op: "fork/exec", Path: "/private/runtime/node", Err: fs.ErrNotExist}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			info := DescribeError(tt.err)
			if info.Code != tt.code || info.Retryable != tt.wantRetryable || info.AccountStatus != "" || strings.Contains(info.Message, "/private/") {
				t.Fatalf("DescribeError() = %#v, want safe %s retryable=%v", info, tt.code, tt.wantRetryable)
			}
			if tt.code == LoginErrorBrowserRuntimePermission && (info.Stage != LoginStageBrowser || info.Message != "浏览器运行环境权限不足，请联系管理员检查 Node 与驱动权限") {
				t.Fatalf("runtime permission diagnosis = %#v", info)
			}
		})
	}
}

func TestAuthResponseMessageAllowlistAndBound(t *testing.T) {
	body := []byte(`{"error":"invalid_grant","error_description":"bad authorization code","message":"email=alice@example.com password=hunter2","access_token":"should-not-appear"}`)
	message := authResponseMessage(body)
	if !strings.Contains(message, "invalid_grant") || !strings.Contains(message, "bad authorization code") {
		t.Fatalf("message = %q, want allowlisted fields", message)
	}
	if strings.Contains(message, "should-not-appear") || strings.Contains(message, "alice@example.com") || strings.Contains(message, "hunter2") {
		t.Fatalf("message leaked disallowed data: %q", message)
	}
	if got := authResponseMessage([]byte("raw upstream body with secrets")); got != "" {
		t.Fatalf("non-JSON body message = %q, want empty", got)
	}
}

func TestAuthResponseDetailsNestedError(t *testing.T) {
	body := []byte(`{"error":{"code":"invalid_mfa","message":"invalid one-time code","secret":"do-not-read"}}`)
	code, message := authResponseDetails(body)
	if code != "invalid_mfa" || !strings.Contains(message, "invalid one-time code") {
		t.Fatalf("authResponseDetails() = %q, %q", code, message)
	}
	if strings.Contains(message, "do-not-read") {
		t.Fatalf("nested detail leaked disallowed fields: %q", message)
	}
}

func TestAuthResponseDetailsCodePrecedence(t *testing.T) {
	for _, tt := range []struct {
		name, body, wantCode, wantMessage string
	}{
		{"top-level code", `{"code":"account_deleted","type":"ignored_type","error":{"code":"user_deleted","type":"ignored_nested_type","message":"Account unavailable"}}`, "account_deleted", "Account unavailable"},
		{"nested code", `{"error":{"code":"user_deactivated","type":"ignored_type","message":"User unavailable"}}`, "user_deactivated", "User unavailable"},
		{"nested type", `{"error":{"type":"account_disabled","message":"Account unavailable"}}`, "account_disabled", "Account unavailable"},
		{"top-level type", `{"type":"user_disabled","error_description":"Account unavailable"}`, "user_disabled", "Account unavailable"},
		{"string error", `{"error":"account_deactivated","error_description":"Account unavailable"}`, "account_deactivated", "Account unavailable"},
		{"string error before type", `{"error":"user_deactivated","type":"invalid_request_error","error_description":"Account unavailable"}`, "user_deactivated", "Account unavailable"},
		{"human string is not a code", `{"error":"Account has been deleted","error_description":"Please contact support"}`, "", "Please contact support"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, message := authResponseDetails([]byte(tt.body))
			if code != tt.wantCode || !strings.Contains(message, tt.wantMessage) {
				t.Fatalf("authResponseDetails() = %q, %q; want code=%q message containing %q", code, message, tt.wantCode, tt.wantMessage)
			}
		})
	}
}

func TestAccountStatusForAuthError(t *testing.T) {
	const exactMessage = "You do not have an account because it has been deleted or deactivated."
	for _, tt := range []struct {
		name, code, message, want string
	}{
		{"account deleted", "account_deleted", "", "deleted"},
		{"user deleted", "user_deleted", "", "deleted"},
		{"account deactivated", "account_deactivated", "", "deactivated"},
		{"user deactivated", "user_deactivated", "", "deactivated"},
		{"account disabled", "account_disabled", "", "deactivated"},
		{"user disabled", "user_disabled", "", "deactivated"},
		{"exact visible sentence", "", exactMessage, "deleted_or_deactivated"},
		{"sentence with prefix and help", "access_denied", "access_denied: You do not have an account because it has been deleted or deactivated. Contact support for help.", "deleted_or_deactivated"},
		{"sentence takes precedence over code", "user_deactivated", exactMessage, "deleted_or_deactivated"},
		{"sentence with whitespace", "", "You do not have an account because it has been deleted\n or deactivated. Contact support.", "deleted_or_deactivated"},
		{"access denied", "access_denied", "", ""},
		{"invalid grant", "invalid_grant", "", ""},
		{"ordinary forbidden", "", "HTTP 403 Forbidden", ""},
		{"organization deleted", "organization_deleted", "", ""},
		{"deleted API key", "api_key_deleted", "API key has been deleted", ""},
		{"incomplete sentence not enough", "", "Your account has been deleted or deactivated.", ""},
		{"ambiguous deletion", "", "Your account may be deleted", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := accountStatusForAuthError(tt.code, tt.message); got != tt.want {
				t.Fatalf("accountStatusForAuthError(%q, %q) = %q, want %q", tt.code, tt.message, got, tt.want)
			}
		})
	}
}

func TestDescribeErrorAccountStatus(t *testing.T) {
	for _, tt := range []struct {
		name                 string
		err                  error
		wantCode, wantStatus string
	}{
		{"HTTP deleted", &authHTTPStatusError{Status: http.StatusServiceUnavailable, UpstreamCode: "account_deleted", Message: "Account unavailable"}, "account_deleted", "deleted"},
		{"HTTP generic", &authHTTPStatusError{Status: http.StatusForbidden, UpstreamCode: "access_denied", Message: "Access denied"}, "access_denied", ""},
		{"callback deactivated", &authRejectionError{Code: "user_deactivated", Message: "Account unavailable", Stage: LoginStageAuthorize}, "user_deactivated", "deactivated"},
		{"wrapped callback deleted", fmt.Errorf("callback failed: %w", &authRejectionError{Code: "account_deleted", Message: "Account unavailable"}), "account_deleted", "deleted"},
		{"callback exact sentence", &authRejectionError{Code: "account_unavailable", Message: "You do not have an account because it has been deleted or deactivated."}, "account_unavailable", "deleted_or_deactivated"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			info := DescribeError(tt.err)
			if info.Code != tt.wantCode || info.AccountStatus != tt.wantStatus || info.Retryable {
				t.Fatalf("DescribeError() = %#v, want code=%q status=%q retryable=false", info, tt.wantCode, tt.wantStatus)
			}
		})
	}
	if info := DescribeError(&authHTTPStatusError{Status: http.StatusServiceUnavailable, UpstreamCode: "access_denied"}); !info.Retryable || info.AccountStatus != "" {
		t.Fatalf("unrelated HTTP 503 should retain retry policy: %#v", info)
	}
	if info := DescribeError(&authHTTPStatusError{Status: http.StatusBadRequest, UpstreamCode: "invalid_grant"}); info.Retryable || info.AccountStatus != "" {
		t.Fatalf("unrelated HTTP 400 should remain generic terminal status: %#v", info)
	}
}

func TestAuthResponseMessageBounded(t *testing.T) {
	message := authResponseMessage([]byte(`{"error_description":"` + strings.Repeat("x", 2048) + `"}`))
	if len(message) > 515 || !strings.HasSuffix(message, "…") {
		t.Fatalf("message length/suffix = %d/%q, want bounded with ellipsis", len(message), message[len(message)-minInt(len(message), 4):])
	}
}

func TestAuthStageForPath(t *testing.T) {
	for _, tt := range []struct{ path, want string }{
		{"auth.openai.com/log-in", LoginStageEmail},
		{"auth.openai.com/api/accounts/authorize/continue", LoginStageEmail},
		{"auth.openai.com/password/verify", LoginStagePassword},
		{"auth.openai.com/mfa-challenge/fixture", LoginStageMFA},
		{"auth.openai.com/workspace/select", LoginStageConsent},
		{"auth.openai.com/api/oauth/oauth2/auth", LoginStageAuthorize},
		{"auth.openai.com/oauth/token", LoginStageToken},
		{"other.example/path", LoginStageUnknown},
	} {
		if got := authStageForPath(tt.path); got != tt.want {
			t.Errorf("authStageForPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestDescribeErrorDoesNotExposeUnexpectedPageBody(t *testing.T) {
	err := fmt.Errorf("%w: title=Login text=card_number=4111111111111111", ErrUnexpectedAuthPage)
	info := DescribeError(err)
	if strings.Contains(info.Message, "4111111111111111") || info.Message != "未进入预期认证页面" {
		t.Fatalf("unexpected page message = %q", info.Message)
	}
}

func TestDescribeErrorPreservesWrappedClassification(t *testing.T) {
	err := fmt.Errorf("attempt failed: %w", errors.New("password form missing"))
	if got := DescribeError(err); got.Stage != LoginStagePassword {
		t.Fatalf("wrapped password error = %#v", got)
	}
}

func TestDescribeErrorDoesNotExposeLibraryErrorCredentials(t *testing.T) {
	err := errors.New("Fill input[type=password] failed with literal hunter2 and token abc123")
	info := DescribeError(err)
	if strings.Contains(info.Message, "hunter2") || strings.Contains(info.Message, "abc123") {
		t.Fatalf("library error leaked credential: %q", info.Message)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
