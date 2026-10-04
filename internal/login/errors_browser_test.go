package login

import (
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

func TestObserveAuthResponsesExtractsNestedMFAJSON(t *testing.T) {
	if os.Getenv("OPENAI_LOGIN_BROWSER_TEST") != "1" {
		t.Skip("set OPENAI_LOGIN_BROWSER_TEST=1 to run the local Playwright response-body regression")
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
	context, err := browser.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	defer context.Close()
	if err := context.Route("**/*", func(route playwright.Route) {
		if err := route.Fulfill(playwright.RouteFulfillOptions{
			Status:      playwright.Int(http.StatusForbidden),
			ContentType: playwright.String("application/json"),
			Body:        `{"error":{"code":"invalid_mfa","message":"invalid one-time code","secret":"do-not-return"}}`,
		}); err != nil {
			t.Errorf("fulfill: %v", err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	page, err := context.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	statuses := make(chan error, 1)
	challenges := make(chan struct{}, 1)
	observeAuthResponses(page, challenges, statuses)
	if _, err := page.Goto("https://auth.openai.com/mfa-challenge/fixture"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-statuses:
		var statusErr *authHTTPStatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("status error = %v, want authHTTPStatusError", err)
		}
		if statusErr.Status != http.StatusForbidden || statusErr.retryable() {
			t.Fatalf("status/retryable = %d/%v, want 403/false", statusErr.Status, statusErr.retryable())
		}
		if statusErr.Stage != LoginStageMFA || statusErr.UpstreamCode != "invalid_mfa" {
			t.Fatalf("stage/code = %q/%q, want mfa/invalid_mfa", statusErr.Stage, statusErr.UpstreamCode)
		}
		if statusErr.Message != "invalid one-time code" {
			t.Fatalf("message = %q, want nested message", statusErr.Message)
		}
		info := DescribeError(err)
		if info.Code != "invalid_mfa" || info.Stage != LoginStageMFA || info.Retryable {
			t.Fatalf("DescribeError() = %#v, want nested terminal detail", info)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for auth response status")
	}
}
