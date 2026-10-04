package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
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

// verifySub2AccountProbe uses the existing admin SSE test. Sub2 resolves the
// account's own credentials and proxy/egress; AUTH never supplies an override.
func (s *sub2RecoveryService) verifySub2AccountProbe(ctx context.Context, accountID int64) error {
	if s == nil || s.sub2 == nil || s.sub2.client == nil || accountID <= 0 {
		return errors.New("Sub2 probe unavailable")
	}
	probeCtx, cancel := context.WithTimeout(ctx, recoveryProbeTimeout)
	defer cancel()
	body := strings.NewReader(`{"model_id":"` + store.AccountCheckModel + `"}`)
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost,
		s.sub2.baseURL+"/admin/accounts/"+strconv.FormatInt(accountID, 10)+"/test", body)
	if err != nil {
		return errors.New("Sub2 probe request unavailable")
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
		return errors.New("Sub2 probe request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("Sub2 probe returned an unsuccessful status")
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		return errors.New("Sub2 probe did not return SSE")
	}
	if err := readRecoveryProbeEvents(resp.Body); err != nil {
		return err
	}
	if probeCtx.Err() != nil {
		return errors.New("Sub2 probe timed out or was canceled")
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
				return errors.New("Sub2 probe reported an error")
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
						return errors.New("Sub2 probe returned invalid SSE data")
					}
					if event.Type == "error" || event.Type == "response.failed" || event.Type == "test_failed" ||
						(event.Success != nil && !*event.Success) ||
						(len(event.Error) > 0 && string(event.Error) != "null" && string(event.Error) != `""`) {
						return errors.New("Sub2 probe reported an error")
					}
					if event.Type == "test_complete" {
						if event.Success == nil || !*event.Success {
							return errors.New("Sub2 probe did not complete successfully")
						}
						completed = true
					}
				} else if !completed {
					return errors.New("Sub2 probe ended before successful completion")
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
		return errors.New("Sub2 probe stream failed or was truncated")
	}
	if !completed {
		return errors.New("Sub2 probe ended before successful completion")
	}
	return nil
}

// verifySub2RecoveryIdentity validates the actual redacted account DTO. An
// account may legitimately be in error before reauthorization, so this helper
// checks identity and type independently from readiness.
func verifySub2RecoveryIdentity(detail map[string]any, expectedSub2ID, accountID int64, account store.Account) error {
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
	extra, ok := detail["extra"].(map[string]any)
	if !ok {
		return errors.New("Sub2 import marker missing")
	}
	if synthetic, _ := extra["synthetic_ui_test"].(bool); synthetic {
		return errors.New("Sub2 synthetic account cannot be recovered")
	}
	marker, ok := extra["kkai_auth_import"].(map[string]any)
	if !ok || toFloat(marker["source_account_id"]) != float64(accountID) {
		return errors.New("Sub2 import marker mismatch")
	}
	return nil
}

// verifySub2RecoveryDetail is the post-apply gate. Token plaintext is never
// returned by the DTO; credential existence plus the account probe verifies it.
func verifySub2RecoveryDetail(detail map[string]any, expectedSub2ID, accountID int64, account store.Account) error {
	if err := verifySub2RecoveryIdentity(detail, expectedSub2ID, accountID, account); err != nil {
		return err
	}
	if status, ok := detail["status"].(string); !ok || status != "active" {
		return errors.New("Sub2 account is not active")
	}
	if message, ok := detail["error_message"].(string); !ok || strings.TrimSpace(message) != "" {
		return errors.New("Sub2 account error is not cleared")
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
