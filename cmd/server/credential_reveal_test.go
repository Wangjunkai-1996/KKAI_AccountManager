package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func TestHistoryCredentialReveal(t *testing.T) {
	history := newTestHistory(t)
	account, err := history.UpsertCredentials(context.Background(), store.Credentials{Email: "reveal@example.test", Password: "fixture-password", TOTPSecret: "JBSWY3DPEHPK3PXP", Proxy: "http://proxy-user:proxy-password@localhost:1234"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/history/" + itoa(account.ID) + "/credentials/reveal"
	for _, tc := range []struct {
		name, method, path, body, header, origin, fetchSite string
		status                                              int
		code                                                string
	}{
		{name: "password", method: "POST", body: `{"field":"password"}`, header: "XMLHttpRequest", status: 200},
		{name: "totp", method: "POST", body: `{"field":"totp_secret"}`, header: "XMLHttpRequest", status: 200},
		{name: "get", method: "GET", body: `{"field":"password"}`, header: "XMLHttpRequest", status: 405, code: "method_not_allowed"},
		{name: "no custom header", method: "POST", body: `{"field":"password"}`, status: 403, code: "request_forbidden"},
		{name: "cross origin", method: "POST", body: `{"field":"password"}`, header: "XMLHttpRequest", origin: "https://other.example", status: 403},
		{name: "cross site", method: "POST", body: `{"field":"password"}`, header: "XMLHttpRequest", fetchSite: "cross-site", status: 403},
		{name: "tokens denied", method: "POST", body: `{"field":"access_token"}`, header: "XMLHttpRequest", status: 400},
		{name: "multiple fields denied", method: "POST", body: `{"field":"password","fields":["totp_secret"]}`, header: "XMLHttpRequest", status: 400},
		{name: "multiple objects denied", method: "POST", body: `{"field":"password"}{}`, header: "XMLHttpRequest", status: 400},
		{name: "unknown account", method: "POST", path: "/api/history/999/credentials/reveal", body: `{"field":"password"}`, header: "XMLHttpRequest", status: 404, code: "account_not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requestPath := path
			if tc.path != "" {
				requestPath = tc.path
			}
			r := httptest.NewRequest(tc.method, requestPath, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Requested-With", tc.header)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			w := httptest.NewRecorder()
			corsMiddleware(handleHistoryDelete())(w, r)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d cache=%s", w.Code, w.Header().Get("Cache-Control"))
			}
			var body map[string]any
			if json.Unmarshal(w.Body.Bytes(), &body) != nil {
				t.Fatal("invalid JSON")
			}
			if tc.code != "" && body["code"] != tc.code {
				t.Fatalf("wrong safe code: %v", body["code"])
			}
			if tc.status == 200 {
				field := "password"
				want := "fixture-password"
				if tc.name == "totp" {
					field = "totp_secret"
					want = "JBSWY3DPEHPK3PXP"
				}
				if len(body) != 4 || body["value"] != want || body["field"] != field || body["account_id"] != float64(account.ID) {
					t.Fatal("reveal did not return exactly the requested field")
				}
			} else if strings.Contains(w.Body.String(), "fixture-password") || strings.Contains(w.Body.String(), "JBSWY3DPEHPK3PXP") {
				t.Fatal("secret leaked on failure")
			}
			if strings.Contains(w.Body.String(), "proxy-password") {
				t.Fatal("unrequested secret leaked")
			}
		})
	}
	for _, endpoint := range []string{"/api/history", "/api/history/" + itoa(account.ID)} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", endpoint, nil)
		if endpoint == "/api/history" {
			handleHistory()(w, r)
		} else {
			handleHistoryDelete()(w, r)
		}
		if w.Code != 200 || strings.Contains(w.Body.String(), "fixture-password") || strings.Contains(w.Body.String(), "JBSWY3DPEHPK3PXP") {
			t.Fatal("ordinary history leaked secret or failed")
		}
		if endpoint != "/api/history" && !strings.Contains(w.Body.String(), `"credentials_meta":{"password":{"state":"saved"},"totp_secret":{"state":"saved"}}`) {
			t.Fatal("missing credential metadata")
		}
	}
	if _, err := history.UpsertCredentials(context.Background(), store.Credentials{Email: account.Email, Password: "fixture-password"}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", path, strings.NewReader(`{"field":"totp_secret"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	handleHistoryDelete()(w, r)
	if w.Code != 404 || !strings.Contains(w.Body.String(), `"code":"credential_missing"`) {
		t.Fatal("missing field response incorrect")
	}
}
