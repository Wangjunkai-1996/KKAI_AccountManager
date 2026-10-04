package login

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

func TestOAuthCallbackAccountRejection(t *testing.T) {
	for _, tt := range []struct{ name, state, code, description, want string }{
		{"explicit deactivation", "expected", "user_deactivated", "This account is deactivated", "deactivated"},
		{"ambiguous official wording", "expected", "access_denied", "You do not have an account because it has been deleted or deactivated.", "deleted_or_deactivated"},
		{"generic denial", "expected", "access_denied", "Permission denied", ""},
		{"state takes precedence", "wrong", "user_deactivated", "This account is deactivated", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			query := url.Values{"state": {tt.state}, "error": {tt.code}, "error_description": {tt.description + " email=fixture@example.test password=do-not-show"}}
			code, err := oauthCallbackCode(oauthRedirectURI+"?"+query.Encode(), "expected")
			if err == nil || code != "" {
				t.Fatalf("callback returned code=%q error=%v", code, err)
			}
			info := DescribeError(err)
			if info.AccountStatus != tt.want || info.HTTPStatus != 0 || info.Retryable {
				t.Fatalf("wrong rejection metadata: %+v", info)
			}
			if tt.state == "expected" && (info.Code != tt.code || info.Stage != "oauth") {
				t.Fatalf("lost callback code/stage: %+v", info)
			}
			if strings.Contains(info.Message, "do-not-show") || strings.Contains(info.Message, "fixture@example.test") {
				t.Fatal("callback rejection exposed credentials")
			}
		})
	}
	code, err := oauthCallbackCode(oauthRedirectURI+"?state=expected&code=authorization-code", "expected")
	if err != nil || code != "authorization-code" {
		t.Fatalf("success callback changed: code=%q error=%v", code, err)
	}
}

func TestAccountRejectionPageBrowser(t *testing.T) {
	if os.Getenv("OPENAI_LOGIN_BROWSER_TEST") != "1" {
		t.Skip("set OPENAI_LOGIN_BROWSER_TEST=1 for the local account rejection fixture")
	}
	pw, err := playwright.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Stop()
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	const message = "You do not have an account because it has been deleted or deactivated."
	for _, tt := range []struct{ name, address, html, want string }{
		{"visible error", "https://auth.openai.com/error", "<main>" + message + "</main>", "deleted_or_deactivated"},
		{"generic forbidden", "https://auth.openai.com/error", "<main>403 Forbidden</main>", ""},
		{"hidden template", "https://auth.openai.com/log-in", "<main>Log in</main><div hidden>" + message + "</div>", ""},
		{"other origin", "https://example.test/error", "<main>" + message + "</main>", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			page, err := browser.NewPage()
			if err != nil {
				t.Fatal(err)
			}
			defer page.Close()
			if err := page.Route("**/*", func(route playwright.Route) {
				if err := route.Fulfill(playwright.RouteFulfillOptions{Status: playwright.Int(200), ContentType: playwright.String("text/html"), Body: tt.html}); err != nil {
					t.Error(err)
				}
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := page.Goto(tt.address); err != nil {
				t.Fatal(err)
			}
			info := DescribeError(accountRejectionOnPage(page))
			if info.AccountStatus != tt.want || info.HTTPStatus != 0 {
				t.Fatalf("page status = %+v", info)
			}
			if tt.want != "" {
				httpFailure := authFailureWithPage(page, &authHTTPStatusError{Status: 403})
				if info := DescribeError(httpFailure); info.HTTPStatus != 403 || info.AccountStatus != tt.want {
					t.Fatalf("HTML error lost its HTTP status or account detail: %+v", info)
				}
				err := waitForAuthSelector(context.Background(), page, "input[type=password]", time.Second, nil, nil)
				if DescribeError(err).AccountStatus != tt.want {
					t.Fatalf("selector wait lost explicit page rejection: %v", err)
				}
				_, err = waitOAuthCallbackContext(context.Background(), page, nil, nil, time.Second, "", "")
				if DescribeError(err).AccountStatus != tt.want {
					t.Fatalf("callback wait lost explicit page rejection: %v", err)
				}
			}
		})
	}
}
