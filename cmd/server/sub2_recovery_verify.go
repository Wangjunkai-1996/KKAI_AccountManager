package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

const (
	recoveryProbeLimit   = 2 << 20
	recoveryProbeTimeout = 90 * time.Second
)

// Only fixed codes/messages leave the recovery boundary. Upstream bodies and
// transport errors can contain credentials and must never be persisted.
type recoveryOperationError struct {
	Code              string
	HTTPStatus        int
	RetryAfterSeconds int
	RequiresAction    bool
	CredentialInvalid bool
}

func (e *recoveryOperationError) Error() string {
	messages := map[string]string{
		"group_unavailable":  "所选 Sub2 分组已删除、停用或不支持 OpenAI，请重新选择",
		"import_unconfirmed": "尚未核对到 Sub2 创建结果；可重新核对，避免重复创建",
		"network_error":      "网络暂不可用，稍后自动重试", "timeout": "请求超时，稍后自动重试",
		"interrupted": "任务被中断，稍后自动核对", "rate_limited": "请求受到限流，等待后自动重试",
		"upstream_error": "上游服务暂不可用，稍后自动重试", "probe_failed": "Sub2 检测未通过，稍后自动复查",
		"protocol_error": "Sub2 响应未能确认，稍后自动核对", "credential_invalid": "新凭据已被上游明确拒绝，将重新登录",
		"sub2_auth_rejected": "Sub2 管理认证失败，请检查管理密钥或权限", "account_missing": "原 Sub2 账号不存在，请核对绑定",
		"request_rejected": "Sub2 拒绝恢复请求，请核对接口配置", "configuration": "恢复服务配置不完整，请检查配置",
		"account_unavailable": "上游明确报告账号停用或删除，请人工处理", "login_required": "登录需要人工处理，请检查账号信息或验证要求",
		"login_failed": "登录暂未完成，稍后自动重试", "identity_changed": "账号身份或绑定发生变化，请人工核对",
		"challenge":    "认证站暂时要求浏览器验证，稍后自动重试",
		"manual_pause": "账号已被人工暂停或禁用，保持原状态", "version_changed": "本地凭据版本已变化，请核对后继续",
		"checkpoint_unconfirmed": "无法确认恢复检查点归属，请人工核对", "account_busy": "账号正在处理其他任务，稍后自动重试",
	}
	if message := messages[e.Code]; message != "" {
		return message
	}
	return "恢复结果暂未确认，稍后自动核对"
}

func classifyRecoveryError(err error) *recoveryOperationError {
	var failure *recoveryOperationError
	if errors.As(err, &failure) {
		return failure
	}
	var api *sub2APIError
	if errors.As(err, &api) {
		return recoveryHTTPError(api.Status, api.RetryAfterSeconds)
	}
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout() {
		return &recoveryOperationError{Code: "timeout"}
	}
	if errors.Is(err, context.Canceled) {
		return &recoveryOperationError{Code: "interrupted"}
	}
	return &recoveryOperationError{Code: "network_error"}
}

func recoveryHTTPError(status, retryAfter int) *recoveryOperationError {
	failure := &recoveryOperationError{HTTPStatus: status, RetryAfterSeconds: retryAfter}
	switch {
	case status == http.StatusTooManyRequests:
		failure.Code = "rate_limited"
	case status >= 500:
		failure.Code = "upstream_error"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		failure.Code, failure.RequiresAction = "sub2_auth_rejected", true
	case status == http.StatusNotFound:
		failure.Code, failure.RequiresAction = "account_missing", true
	case status >= 400:
		failure.Code, failure.RequiresAction = "request_rejected", true
	default:
		failure.Code = "protocol_error"
	}
	return failure
}

func recoveryRetryAfter(raw string) int {
	seconds, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		if at, parseErr := http.ParseTime(raw); parseErr == nil {
			seconds = int(time.Until(at).Seconds()) + 1
		}
	}
	if seconds < 0 {
		return 0
	}
	if seconds > 86400 {
		return 86400
	}
	return seconds
}

// parseRecoveryWrappedError accepts the exact error string emitted by Sub2's
// account-test endpoint when an upstream request fails.  The status and JSON
// body are both validated before they are used as probe evidence; arbitrary
// text containing "401" must remain an opaque probe failure.
func parseRecoveryWrappedError(raw string) (status int, code string, ok bool) {
	const prefix = "API returned "
	if !strings.HasPrefix(raw, prefix) {
		return 0, "", false
	}
	rest := strings.TrimPrefix(raw, prefix)
	separator := strings.Index(rest, ": ")
	if separator <= 0 || separator == len(rest)-2 {
		return 0, "", false
	}
	statusRaw := rest[:separator]
	if strings.Trim(statusRaw, "0123456789") != "" {
		return 0, "", false
	}
	status, err := strconv.Atoi(statusRaw)
	if err != nil || status < 100 || status > 599 {
		return 0, "", false
	}
	var envelope struct {
		Code       string          `json:"code"`
		Status     int             `json:"status"`
		StatusCode int             `json:"status_code"`
		Error      json.RawMessage `json:"error"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(rest[separator+2:])), &envelope) != nil {
		return 0, "", false
	}
	if envelope.Code != "" {
		code = envelope.Code
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		var nested struct {
			Code       string `json:"code"`
			Type       string `json:"type"`
			Status     int    `json:"status"`
			StatusCode int    `json:"status_code"`
		}
		if json.Unmarshal(envelope.Error, &nested) == nil {
			if nested.Code != "" {
				code = nested.Code
			} else if nested.Type != "" {
				code = nested.Type
			}
			if nested.Status != 0 {
				status = nested.Status
			} else if nested.StatusCode != 0 {
				status = nested.StatusCode
			}
		}
	}
	if envelope.Status != 0 {
		status = envelope.Status
	} else if envelope.StatusCode != 0 {
		status = envelope.StatusCode
	}
	return status, code, true
}

// An SSE error is account-test evidence, unlike the HTTP status of the
// management endpoint. Only explicit structured codes/statuses invalidate AT.
func recoveryProbeEventError(payload []byte) *recoveryOperationError {
	var event struct {
		Code       string          `json:"code"`
		ErrorCode  string          `json:"error_code"`
		Status     int             `json:"status"`
		StatusCode int             `json:"status_code"`
		RetryAfter int             `json:"retry_after"`
		Error      json.RawMessage `json:"error"`
	}
	failure := &recoveryOperationError{Code: "probe_failed"}
	if json.Unmarshal(payload, &event) != nil {
		return failure
	}
	if len(event.Error) > 0 {
		var nested struct {
			Code       string `json:"code"`
			Type       string `json:"type"`
			Status     int    `json:"status"`
			StatusCode int    `json:"status_code"`
		}
		if json.Unmarshal(event.Error, &nested) == nil {
			if nested.Code != "" {
				event.Code = nested.Code
			} else if nested.Type != "" {
				event.Code = nested.Type
			}
			if nested.Status != 0 {
				event.Status = nested.Status
			}
			if nested.StatusCode != 0 {
				event.StatusCode = nested.StatusCode
			}
		} else {
			var code string
			if json.Unmarshal(event.Error, &code) == nil {
				if status, wrappedCode, ok := parseRecoveryWrappedError(code); ok {
					event.Status = status
					event.Code = wrappedCode
				}
			}
		}
	}
	if event.Code == "" {
		event.Code = event.ErrorCode
	}
	if event.Status == 0 {
		event.Status = event.StatusCode
	}
	failure.HTTPStatus = event.Status
	if event.RetryAfter > 0 && event.RetryAfter <= 86400 {
		failure.RetryAfterSeconds = event.RetryAfter
	}
	switch strings.ToLower(strings.TrimSpace(event.Code)) {
	case "account_deactivated", "account_deleted", "user_deactivated", "user_deleted", "account_disabled", "user_disabled":
		failure.Code, failure.RequiresAction = "account_unavailable", true
	case "token_revoked", "token_invalidated", "token_expired", "access_token_expired", "invalid_token", "unauthorized", "authentication_error":
		failure.Code, failure.CredentialInvalid = "credential_invalid", true
	case "rate_limit_exceeded", "rate_limited", "too_many_requests":
		failure.Code = "rate_limited"
	case "server_error", "service_unavailable", "upstream_error":
		failure.Code = "upstream_error"
	default:
		switch {
		case event.Status == http.StatusUnauthorized:
			failure.Code, failure.CredentialInvalid = "credential_invalid", true
		case event.Status == http.StatusTooManyRequests:
			failure.Code = "rate_limited"
		case event.Status >= 500:
			failure.Code = "upstream_error"
		}
	}
	return failure
}

// verifySub2AccountProbe uses the existing admin SSE test. Sub2 resolves the
// account's own credentials and proxy/egress; AUTH never supplies an override.
func (s *sub2RecoveryService) verifySub2AccountProbe(ctx context.Context, accountID int64) error {
	if s == nil || s.sub2 == nil || s.sub2.client == nil || accountID <= 0 {
		return &recoveryOperationError{Code: "configuration", RequiresAction: true}
	}
	probeCtx, cancel := context.WithTimeout(ctx, recoveryProbeTimeout)
	defer cancel()
	body := strings.NewReader(`{"model_id":"` + store.AccountCheckModel + `"}`)
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost,
		s.sub2.baseURL+"/admin/accounts/"+strconv.FormatInt(accountID, 10)+"/test", body)
	if err != nil {
		return &recoveryOperationError{Code: "configuration", RequiresAction: true}
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("x-api-key", s.sub2.adminAPIKey)
	client := *s.sub2.client
	client.Timeout = recoveryProbeTimeout
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return classifyRecoveryError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return recoveryHTTPError(resp.StatusCode, recoveryRetryAfter(resp.Header.Get("Retry-After")))
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		return &recoveryOperationError{Code: "protocol_error"}
	}
	if err := readRecoveryProbeEvents(resp.Body); err != nil {
		if probeCtx.Err() != nil {
			return classifyRecoveryError(probeCtx.Err())
		}
		return err
	}
	if probeCtx.Err() != nil {
		return classifyRecoveryError(probeCtx.Err())
	}
	return nil
}

func readRecoveryProbeEvents(body io.Reader) error {
	limited := &io.LimitedReader{R: body, N: recoveryProbeLimit + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 256<<10)
	completed := false
	var data strings.Builder
	eventName := ""
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if eventName == "error" || eventName == "response.failed" {
				return recoveryProbeEventError([]byte(data.String()))
			}
			if data.Len() > 0 {
				payload := strings.TrimSpace(data.String())
				if payload != "[DONE]" {
					var event struct {
						Type    string          `json:"type"`
						Success *bool           `json:"success"`
						Error   json.RawMessage `json:"error"`
					}
					if err := json.Unmarshal([]byte(payload), &event); err != nil || event.Type == "" {
						return &recoveryOperationError{Code: "protocol_error"}
					}
					if event.Type == "error" || event.Type == "response.failed" || event.Type == "test_failed" ||
						(event.Success != nil && !*event.Success) ||
						(len(event.Error) > 0 && string(event.Error) != "null" && string(event.Error) != `""`) {
						return recoveryProbeEventError([]byte(payload))
					}
					if event.Type == "test_complete" {
						if event.Success == nil || !*event.Success {
							return &recoveryOperationError{Code: "probe_failed"}
						}
						completed = true
					}
				} else if !completed {
					return &recoveryOperationError{Code: "protocol_error"}
				}
			}
			data.Reset()
			eventName = ""
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		} else if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
	}
	if scanner.Err() != nil || limited.N <= 0 || data.Len() > 0 || eventName != "" {
		return &recoveryOperationError{Code: "protocol_error"}
	}
	if !completed {
		return &recoveryOperationError{Code: "protocol_error"}
	}
	return nil
}

// verifySub2RecoveryIdentity validates the actual redacted account DTO. An
// account may legitimately be in error before reauthorization, so this helper
// checks identity and type independently from readiness.
func verifySub2RecoveryIdentity(detail map[string]any, expectedSub2ID, accountID int64, account store.Account, bindings ...store.Sub2Import) error {
	// A persisted binding identifies the remote record; changing workspace or
	// plan must not prevent updating it or resuming saved OAuth credentials.
	account.ChatGPTAccountID = ""
	if err := verifySub2AccountIdentity(detail, expectedSub2ID, accountID, account); err != nil {
		return err
	}
	extra, _ := detail["extra"].(map[string]any)
	if _, exists := extra["kkai_auth_import"]; exists {
		return nil // A present marker was checked by verifySub2AccountIdentity.
	}
	for _, binding := range bindings {
		if binding.AccountID == accountID && binding.Sub2AccountID == expectedSub2ID && binding.State == "imported" && strings.HasPrefix(binding.OperationID, "linked-") {
			return nil
		}
	}
	return errors.New("Sub2 import marker or verified local association missing")
}

// Association and recovery both validate credential identity, never the
// editable display name. A present foreign import marker remains a conflict.
func verifySub2AccountIdentity(detail map[string]any, expectedSub2ID, accountID int64, account store.Account) error {
	if expectedSub2ID <= 0 || accountID <= 0 || toFloat(detail["id"]) != float64(expectedSub2ID) {
		return errors.New("Sub2 account id mismatch")
	}
	if platform, ok := detail["platform"].(string); !ok || platform != "openai" {
		return errors.New("Sub2 platform mismatch")
	}
	if typ, ok := detail["type"].(string); !ok || typ != "oauth" {
		return errors.New("Sub2 account type is not oauth")
	}
	credentials, ok := detail["credentials"].(map[string]any)
	if !ok {
		return errors.New("Sub2 credential identity missing")
	}
	email, ok := credentials["email"].(string)
	if !ok || strings.TrimSpace(email) == "" || !strings.EqualFold(strings.TrimSpace(email), strings.TrimSpace(account.Email)) {
		return errors.New("Sub2 credential email mismatch")
	}
	if account.ChatGPTAccountID != "" {
		id, ok := credentials["chatgpt_account_id"].(string)
		if !ok || id != account.ChatGPTAccountID {
			return errors.New("Sub2 credential account mismatch")
		}
	}
	for _, key := range []string{"email", "account_email"} {
		if value, ok := detail[key].(string); ok && strings.TrimSpace(value) != "" && !strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(account.Email)) {
			return errors.New("Sub2 email mismatch")
		}
	}
	extra, _ := detail["extra"].(map[string]any)
	if synthetic, _ := extra["synthetic_ui_test"].(bool); synthetic {
		return errors.New("Sub2 synthetic account cannot be recovered")
	}
	if rawMarker, exists := extra["kkai_auth_import"]; exists {
		marker, ok := rawMarker.(map[string]any)
		if !ok || toFloat(marker["source_account_id"]) != float64(accountID) {
			return errors.New("Sub2 import marker mismatch")
		}
	}
	return nil
}

// verifySub2RecoveryDetail is the post-apply gate. Token plaintext is never
// returned by the DTO; credential existence plus the account probe verifies it.
func verifySub2RecoveryDetail(detail map[string]any, expectedSub2ID, accountID int64, account store.Account, bindings ...store.Sub2Import) error {
	if err := verifySub2RecoveryMetadata(detail, expectedSub2ID, accountID, account, bindings...); err != nil {
		return err
	}
	if status, ok := detail["status"].(string); !ok || status != "active" {
		return errors.New("Sub2 account is not active")
	}
	if message, ok := detail["error_message"].(string); !ok || strings.TrimSpace(message) != "" {
		return errors.New("Sub2 account error is not cleared")
	}
	return nil
}

func verifySub2RecoveryMetadata(detail map[string]any, expectedSub2ID, accountID int64, account store.Account, bindings ...store.Sub2Import) error {
	if err := verifySub2RecoveryIdentity(detail, expectedSub2ID, accountID, account, bindings...); err != nil {
		return err
	}
	credentials, _ := detail["credentials"].(map[string]any)
	if id, ok := credentials["chatgpt_account_id"].(string); !ok || id == "" || id != account.ChatGPTAccountID {
		return errors.New("Sub2 updated workspace mismatch")
	}
	if org, _ := credentials["organization_id"].(string); org != account.OrganizationID {
		return errors.New("Sub2 updated organization mismatch")
	}
	if plan, _ := credentials["plan_type"].(string); account.PlanType != "" && plan != account.PlanType {
		return errors.New("Sub2 updated plan mismatch")
	}
	if account.ExpiresAt > 0 && int64(toFloat(credentials["expires_at"])) != account.ExpiresAt {
		return errors.New("Sub2 updated credential expiry mismatch")
	}
	status, ok := detail["credentials_status"].(map[string]any)
	if !ok {
		return errors.New("Sub2 credentials status missing")
	}
	for _, key := range []string{"has_access_token", "has_refresh_token"} {
		value, ok := status[key].(bool)
		if !ok || !value {
			return errors.New("Sub2 credentials incomplete")
		}
	}
	return nil
}
