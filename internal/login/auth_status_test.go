package login

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

func TestAuthChallengePreservesHTTPStatus(t *testing.T) {
	statuses := make(chan error, 1)
	challenges := make(chan struct{}, 1)
	statuses <- &authHTTPStatusError{Status: http.StatusForbidden, Cause: ErrCloudflareChallenge}
	challenges <- struct{}{}
	err := detectAccessBlock(nil, challenges, statuses)
	if status, ok := AuthHTTPStatus(err); !ok || status != http.StatusForbidden {
		t.Fatalf("status = %d, present = %v, error = %v", status, ok, err)
	}
	if !errors.Is(err, ErrCloudflareChallenge) {
		t.Fatalf("challenge cause lost: %v", err)
	}
}

func TestAuthChallengeStatusBrowser(t *testing.T) {
	if os.Getenv("OPENAI_LOGIN_BROWSER_TEST") != "1" {
		t.Skip("set OPENAI_LOGIN_BROWSER_TEST=1 for the local auth status regression")
	}
	pw, err := playwright.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Stop()
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true), Channel: playwright.String("chrome")})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	for _, stage := range []string{"email", "password"} {
		t.Run(stage, func(t *testing.T) {
			page, err := browser.NewPage()
			if err != nil {
				t.Fatal(err)
			}
			defer page.Close()
			var submissions atomic.Int32
			if err := page.Route("**/*", func(route playwright.Route) {
				if route.Request().Method() == http.MethodPost || stage == "email" {
					submissions.Add(1)
					err := route.Fulfill(playwright.RouteFulfillOptions{
						Status:      playwright.Int(http.StatusForbidden),
						Headers:     map[string]string{"cf-mitigated": "challenge"},
						ContentType: playwright.String("text/html"),
						Body:        `<title>Just a moment...</title><p>Verifying you are human</p>`,
					})
					if err != nil {
						t.Error(err)
					}
					return
				}
				err := route.Fulfill(playwright.RouteFulfillOptions{
					ContentType: playwright.String("text/html"),
					Body:        `<form onsubmit="event.preventDefault();fetch('/api/accounts/authorize/continue',{method:'POST'})"><input type="email" name="username"><button type="submit">Continue</button></form>`,
				})
				if err != nil {
					t.Error(err)
				}
			}); err != nil {
				t.Fatal(err)
			}
			statuses := make(chan error, 4)
			challenges := make(chan struct{}, 1)
			observeAuthResponses(page, challenges, statuses)
			if _, err := page.Goto("https://auth.openai.com/log-in"); err != nil {
				t.Fatal(err)
			}
			selector := "input[type='email']"
			if stage == "password" {
				if err := waitForAuthSelector(context.Background(), page, selector, time.Second, challenges, statuses); err != nil {
					t.Fatal(err)
				}
				if err := page.Fill(selector, "fixture@example.com"); err != nil {
					t.Fatal(err)
				}
				if err := page.Click("button[type='submit']"); err != nil {
					t.Fatal(err)
				}
				selector = "input[type='password']"
			}
			started := time.Now()
			err = waitForAuthSelector(context.Background(), page, selector, time.Minute, challenges, statuses)
			if elapsed := time.Since(started); elapsed > 2*time.Second {
				t.Fatalf("auth rejection took %s instead of returning promptly", elapsed)
			}
			status, ok := AuthHTTPStatus(err)
			if !ok || status != http.StatusForbidden || !errors.Is(err, ErrCloudflareChallenge) || !strings.Contains(err.Error(), "Cloudflare challenge") {
				t.Fatalf("lost HTTP status or challenge detail: %v", err)
			}
			var statusErr *authHTTPStatusError
			if !errors.As(err, &statusErr) || !statusErr.retryable() {
				t.Fatalf("retry policy did not allow challenge retry: %v", err)
			}
			if submissions.Load() != 1 {
				t.Fatalf("auth submitted %d times, want once", submissions.Load())
			}
		})
	}
	t.Run("background_response_challenges", func(t *testing.T) {
		page, err := browser.NewPage()
		if err != nil {
			t.Fatal(err)
		}
		defer page.Close()
		var backgroundResponses atomic.Int32
		if err := page.Route("**/*", func(route playwright.Route) {
			options := playwright.RouteFulfillOptions{
				ContentType: playwright.String("text/html"),
				Body:        `<title>Log in</title><input type="email" name="username">`,
			}
			if route.Request().URL() != "https://auth.openai.com/log-in" {
				backgroundResponses.Add(1)
				options.Status = playwright.Int(http.StatusForbidden)
				options.Headers = map[string]string{"cf-mitigated": "challenge", "Access-Control-Allow-Origin": "*"}
				options.Body = "background challenge"
			}
			if err := route.Fulfill(options); err != nil {
				t.Error(err)
			}
		}); err != nil {
			t.Fatal(err)
		}
		statuses := make(chan error, 4)
		challenges := make(chan struct{}, 4)
		observeAuthResponses(page, challenges, statuses)
		if _, err := page.Goto("https://auth.openai.com/log-in"); err != nil {
			t.Fatal(err)
		}
		// All four responses contain challenge headers, but none is an auth
		// navigation/submission: excluded paths, another origin, and an asset GET.
		if _, err := page.Evaluate(`async () => {
			for (const [url, method] of [
				['/cdn-cgi/challenge', 'POST'],
				['/awe/background', 'POST'],
				['https://background.example/collect', 'POST'],
				['/assets/background', 'GET']
			]) {
				const response = await fetch(url, {method});
				if (response.status !== 403) throw new Error('fixture did not return 403');
				await response.text();
			}
		}`); err != nil {
			t.Fatal(err)
		}
		if backgroundResponses.Load() != 4 {
			t.Fatalf("background responses = %d, want 4", backgroundResponses.Load())
		}
		if err := waitForAuthSelector(context.Background(), page, "input[type='email']", time.Second, challenges, statuses); err != nil {
			t.Fatalf("background response blocked visible email form: %v", err)
		}
		if err := page.Fill("input[type='email']", "fixture@example.com"); err != nil {
			t.Fatal(err)
		}
		if err := detectAccessBlock(page, challenges, statuses); err != nil {
			t.Fatalf("background response incorrectly detected as login rejection: %v", err)
		}
	})
}

func TestInitialChallengeRecoveryDoesNotLeakSignal(t *testing.T) {
	if os.Getenv("OPENAI_LOGIN_BROWSER_TEST") != "1" {
		t.Skip("set OPENAI_LOGIN_BROWSER_TEST=1 for the local auth challenge recovery regression")
	}
	pw, err := playwright.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Stop()
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true), Channel: playwright.String("chrome")})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()

	const authorizeURL = "https://auth.openai.com/oauth/authorize?fixture=challenge"
	var first int32 = 1
	if err := page.Route("**/*", func(route playwright.Route) {
		requestURL := route.Request().URL()
		if requestURL == authorizeURL && atomic.CompareAndSwapInt32(&first, 1, 0) {
			err := route.Fulfill(playwright.RouteFulfillOptions{
				Status:      playwright.Int(http.StatusOK),
				Headers:     map[string]string{"cf-mitigated": "challenge"},
				ContentType: playwright.String("text/html"),
				Body:        `<title>Just a moment...</title><p>Verifying you are human</p><script>setTimeout(() => location.href = 'https://auth.openai.com/oauth/authorize?fixture=challenge', 200)</script>`,
			})
			if err != nil {
				t.Error(err)
			}
			return
		}
		if requestURL == authorizeURL {
			err := route.Fulfill(playwright.RouteFulfillOptions{
				ContentType: playwright.String("text/html"),
				Body:        `<form><input type="email" name="username"></form>`,
			})
			if err != nil {
				t.Error(err)
			}
			return
		}
		if err := route.Fulfill(playwright.RouteFulfillOptions{ContentType: playwright.String("text/html"), Body: "fixture"}); err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}

	statuses := make(chan error, 4)
	challenges := make(chan struct{}, 4)
	observeAuthResponses(page, challenges, statuses)
	if err := navigateAuthorize(context.Background(), page, authorizeURL, challenges, statuses); err != nil {
		t.Fatalf("navigateAuthorize() error = %v", err)
	}
	if err := waitForAuthSelector(context.Background(), page, "input[type='email']", time.Second, challenges, statuses); err != nil {
		t.Fatalf("recovered email form was rejected by a stale challenge signal: %v", err)
	}
}
