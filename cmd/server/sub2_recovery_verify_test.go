package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func TestRecoveryVerifySSE(t *testing.T) {
	complete := "data: {\"type\":\"test_complete\",\"success\":true}\n\n"
	for _, tc := range []struct {
		name string
		body string
		ok   bool
	}{
		{"complete", "data: {\"type\":\"test_start\"}\n\n" + complete, true},
		{"crlf", strings.ReplaceAll(complete, "\n", "\r\n"), true},
		{"multiline", "data: {\"type\":\"test_complete\",\n" + "data: \"success\":true}\n\n", true},
		{"done_after_complete", complete + "data: [DONE]\n\n", true},
		{"empty", "", false},
		{"truncated_content", "data: {\"type\":\"content\",\"text\":\"ok\"}\n\n", false},
		{"truncated_frame", strings.TrimSuffix(complete, "\n"), false},
		{"truncated_after_success", complete + "data: {\"type\":", false},
		{"failure", "data: {\"type\":\"test_complete\",\"success\":false}\n\n", false},
		{"missing_success", "data: {\"type\":\"test_complete\"}\n\n", false},
		{"error_then_success", "data: {\"type\":\"error\",\"error\":\"at-secret\"}\n\n" + complete, false},
		{"error_after_success", complete + "data: {\"type\":\"error\",\"error\":\"at-secret\"}\n\n", false},
		{"event_error", "event: error\ndata: {}\n\n" + complete, false},
		{"error_property", "data: {\"type\":\"test_complete\",\"success\":true,\"error\":\"at-secret\"}\n\n", false},
		{"invalid_json", "data: {\n\n" + complete, false},
		{"done_only", "data: [DONE]\n\n", false},
		{"oversized", complete + strings.Repeat(": keepalive\n\n", recoveryProbeLimit/10), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := readRecoveryProbeEvents(strings.NewReader(tc.body))
			if (err == nil) != tc.ok {
				t.Fatalf("error=%v want success=%v", err, tc.ok)
			}
			if err != nil && strings.Contains(err.Error(), "at-secret") {
				t.Fatal("probe error exposed upstream body")
			}
		})
	}
	if err := readRecoveryProbeEvents(io.MultiReader(strings.NewReader(complete), recoveryVerifyReadFailure{})); err == nil {
		t.Fatal("read failure after success must not pass")
	}
}

type recoveryVerifyReadFailure struct{}

func (recoveryVerifyReadFailure) Read([]byte) (int, error) {
	return 0, errors.New("at-secret")
}

func TestRecoveryVerifyProbeHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/admin/accounts/42/test" || r.Header.Get("x-api-key") != "test-admin" {
			t.Error("wrong probe target or authorization")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["model_id"] != store.AccountCheckModel || len(body) != 1 {
			t.Error("probe must use current check model and account-owned route")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, "data: {\"type\":\"test_complete\",\"success\":true}\n\n")
	}))
	defer server.Close()
	svc := &sub2RecoveryService{sub2: &sub2ImportService{baseURL: server.URL, adminAPIKey: "test-admin", client: server.Client()}}
	if err := svc.verifySub2AccountProbe(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryVerifyProbeTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"test_start\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	svc := &sub2RecoveryService{sub2: &sub2ImportService{baseURL: server.URL, adminAPIKey: "test-admin", client: server.Client()}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := svc.verifySub2AccountProbe(ctx, 42); err == nil {
		t.Fatal("timeout must fail closed")
	}
}

func recoveryVerifyDetailFixture() map[string]any {
	return map[string]any{
		"id": float64(42), "platform": "openai", "type": "oauth", "status": "active", "error_message": "",
		"credentials":        map[string]any{"email": "recover@example.test", "chatgpt_account_id": "workspace"},
		"credentials_status": map[string]any{"has_access_token": true, "has_refresh_token": true},
		"extra":              map[string]any{"kkai_auth_import": map[string]any{"source_account_id": float64(7)}},
	}
}

func TestRecoveryVerifyActualDTO(t *testing.T) {
	account := store.Account{Email: "recover@example.test", ChatGPTAccountID: "workspace"}
	for _, tc := range []struct {
		name       string
		change     func(map[string]any)
		identityOK bool
		readyOK    bool
	}{
		{"ready", func(map[string]any) {}, true, true},
		{"error_can_reauthorize", func(d map[string]any) { d["status"] = "error" }, true, false},
		{"missing_status", func(d map[string]any) { delete(d, "status") }, true, false},
		{"error_message", func(d map[string]any) { d["error_message"] = "401" }, true, false},
		{"missing_credentials_status", func(d map[string]any) { delete(d, "credentials_status") }, true, false},
		{"missing_rt", func(d map[string]any) { delete(d["credentials_status"].(map[string]any), "has_refresh_token") }, true, false},
		{"false_at", func(d map[string]any) { d["credentials_status"].(map[string]any)["has_access_token"] = false }, true, false},
		{"missing_type", func(d map[string]any) { delete(d, "type") }, false, false},
		{"wrong_platform", func(d map[string]any) { d["platform"] = "anthropic" }, false, false},
		{"wrong_identity", func(d map[string]any) { d["credentials"].(map[string]any)["email"] = "other@example.test" }, false, false},
		{"wrong_workspace", func(d map[string]any) { d["credentials"].(map[string]any)["chatgpt_account_id"] = "other" }, false, false},
		{"missing_marker", func(d map[string]any) { delete(d, "extra") }, false, false},
		{"synthetic", func(d map[string]any) { d["extra"].(map[string]any)["synthetic_ui_test"] = true }, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detail := recoveryVerifyDetailFixture()
			tc.change(detail)
			if err := verifySub2RecoveryIdentity(detail, 42, 7, account); (err == nil) != tc.identityOK {
				t.Fatalf("identity error=%v want success=%v", err, tc.identityOK)
			}
			if err := verifySub2RecoveryDetail(detail, 42, 7, account); (err == nil) != tc.readyOK {
				t.Fatalf("ready error=%v want success=%v", err, tc.readyOK)
			}
		})
	}
}

func TestRecoveryVerifyExternalAssociation(t *testing.T) {
	account := store.Account{Email: "recover@example.test", ChatGPTAccountID: "workspace"}
	binding := store.Sub2Import{AccountID: 7, Sub2AccountID: 42, State: "imported", OperationID: "linked-verified"}
	detail := recoveryVerifyDetailFixture()
	delete(detail, "extra")
	if err := verifySub2RecoveryDetail(detail, 42, 7, account, binding); err != nil {
		t.Fatalf("verified external association rejected: %v", err)
	}
	if err := verifySub2RecoveryIdentity(detail, 42, 7, account); err == nil {
		t.Fatal("missing local association accepted")
	}
	detail["extra"] = map[string]any{"kkai_auth_import": map[string]any{"source_account_id": float64(8)}}
	if err := verifySub2RecoveryIdentity(detail, 42, 7, account, binding); err == nil {
		t.Fatal("foreign import marker accepted")
	}
	delete(detail, "extra")
	detail["credentials"].(map[string]any)["chatgpt_account_id"] = "different-workspace"
	if err := verifySub2RecoveryIdentity(detail, 42, 7, account, binding); err == nil {
		t.Fatal("wrong workspace accepted")
	}
}
