package login

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

func TestShouldTryLocalClash(t *testing.T) {
	for _, tt := range []struct {
		name  string
		proxy string
		err   error
		want  bool
	}{
		{"remote reset", "http://proxy.example.com:8080", ErrAuthConnectionReset, true},
		{"success", "http://proxy.example.com:8080", nil, false},
		{"challenge", "http://proxy.example.com:8080", ErrCloudflareChallenge, false},
		{"region refusal", "http://proxy.example.com:8080", ErrUnsupportedRegion, false},
		{"auth page error", "http://proxy.example.com:8080", ErrUnexpectedAuthPage, false},
		{"no proxy", "", ErrAuthConnectionReset, false},
		{"local IPv4", "http://127.0.0.1:7897", ErrAuthConnectionReset, false},
		{"local IPv6", "http://[::1]:7897", ErrAuthConnectionReset, false},
		{"localhost", "http://localhost:7897", ErrAuthConnectionReset, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldTryLocalClash(tt.proxy, tt.err); got != tt.want {
				t.Fatalf("shouldTryLocalClash() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWaitInitialChallengeHonorsParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	terminal := &authHTTPStatusError{
		Status:  http.StatusForbidden,
		Cause:   ErrCloudflareChallenge,
		Message: "challenge",
	}
	err := waitInitialChallenge(ctx, nil, make(chan struct{}), make(chan error), terminal)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitInitialChallenge() error = %v, want context canceled", err)
	}
}

func TestDrainChallengeSignalsConsumesChallengeEventsAndPreservesFailures(t *testing.T) {
	challenges := make(chan struct{}, 1)
	challenges <- struct{}{}
	statuses := make(chan error, 2)
	statuses <- cloudflareError()
	want := &authHTTPStatusError{Status: http.StatusForbidden, Message: "non-challenge"}
	statuses <- want

	err := drainChallengeSignals(challenges, statuses)
	var statusErr *authHTTPStatusError
	if !errors.As(err, &statusErr) || statusErr != want {
		t.Fatalf("drainChallengeSignals() error = %v, want preserved status error", err)
	}
	if len(challenges) != 0 {
		t.Fatalf("challenge signals remaining = %d, want 0", len(challenges))
	}
}

func TestBrowserCompatibilityOptions(t *testing.T) {
	for _, tt := range []struct {
		name    string
		compat  bool
		wantArg bool
		wantUA  string
	}{
		{name: "default headed profile", compat: false, wantArg: true},
		{
			name:    "Mac compatibility profile",
			compat:  true,
			wantArg: true,
			wantUA:  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := NewService(Config{BrowserCompatibility: tt.compat}).config
			launch := browserLaunchOptions(config)
			foundArg := false
			for _, arg := range launch.Args {
				if arg == compatBrowserArg {
					foundArg = true
					break
				}
			}
			if foundArg != tt.wantArg {
				t.Fatalf("compatibility launch arg present = %v, want %v; args=%v", foundArg, tt.wantArg, launch.Args)
			}

			context := browserContextOptions(config)
			if tt.wantUA == "" {
				if context.UserAgent != nil {
					t.Fatalf("default UserAgent = %q, want nil", *context.UserAgent)
				}
				return
			}
			if context.UserAgent == nil || *context.UserAgent != tt.wantUA {
				var got string
				if context.UserAgent != nil {
					got = *context.UserAgent
				}
				t.Fatalf("compatibility UserAgent = %q, want %q", got, tt.wantUA)
			}
		})
	}

	proxyLaunch := browserLaunchOptions(NewService(Config{Proxy: "http://127.0.0.1:7897"}).config)
	for _, arg := range proxyLaunch.Args {
		if arg == "--no-proxy-server" {
			t.Fatal("proxy launch unexpectedly disables proxies")
		}
	}
}

func TestDetectAccessBlockUsesCloudflareResponseSignal(t *testing.T) {
	challenge := make(chan struct{}, 1)
	challenge <- struct{}{}

	err := detectAccessBlock(nil, challenge)
	if !errors.Is(err, ErrCloudflareChallenge) {
		t.Fatalf("detectAccessBlock() error = %v, want ErrCloudflareChallenge", err)
	}
}

func TestAuthHTTPStatusErrorClassification(t *testing.T) {
	for _, tt := range []struct {
		status    int
		retryable bool
		wantText  string
	}{
		{http.StatusPaymentRequired, false, "HTTP 402"},
		{http.StatusForbidden, false, "HTTP 403"},
		{http.StatusUnauthorized, false, "HTTP 401"},
		{http.StatusTooManyRequests, true, "HTTP 429"},
		{http.StatusBadGateway, true, "HTTP 502"},
	} {
		err := &authHTTPStatusError{
			Status:     tt.status,
			StatusText: "fixture",
			Path:       "auth.openai.com/log-in",
			Message:    "account rejected",
		}
		if got := err.retryable(); got != tt.retryable {
			t.Errorf("status %d retryable = %v, want %v", tt.status, got, tt.retryable)
		}
		if !strings.Contains(err.Error(), tt.wantText) || !strings.Contains(err.Error(), "account rejected") {
			t.Errorf("status %d error = %q, want status and message", tt.status, err)
		}
	}
}

func TestAuthHTTPStatus(t *testing.T) {
	if status, ok := AuthHTTPStatus(&authHTTPStatusError{Status: http.StatusForbidden}); !ok || status != http.StatusForbidden {
		t.Fatalf("AuthHTTPStatus() = %d, %v; want 403, true", status, ok)
	}
	if status, ok := AuthHTTPStatus(errors.New("network failure")); ok || status != 0 {
		t.Fatalf("AuthHTTPStatus() = %d, %v; want 0, false", status, ok)
	}
}

func TestDetectAccessBlockUsesAuthHTTPStatus(t *testing.T) {
	statuses := make(chan error, 1)
	want := &authHTTPStatusError{Status: http.StatusForbidden, Path: "auth.openai.com/log-in"}
	statuses <- want

	err := detectAccessBlock(nil, make(chan struct{}), statuses)
	var got *authHTTPStatusError
	if !errors.As(err, &got) || got.Status != want.Status {
		t.Fatalf("detectAccessBlock() error = %v, want HTTP %d status error", err, want.Status)
	}
}

func TestHasCloudflareChallengeSignals(t *testing.T) {
	tests := []struct {
		name        string
		pageURL     string
		title       string
		visibleText string
		want        bool
	}{
		{name: "ordinary login", title: "Log in - OpenAI", visibleText: "Welcome back Continue", want: false},
		{name: "Cloudflare mention alone", title: "Log in - OpenAI", visibleText: "Cloudflare cf-chl", want: false},
		{name: "generic unavailable page", title: "Unable to load site", visibleText: "Unable to load site Please try again", want: false},
		{name: "waiting title alone", title: "Just a moment...", visibleText: "Loading", want: false},
		{name: "verification text alone", title: "Log in - OpenAI", visibleText: "Verify you are human", want: false},
		{name: "challenge URL", pageURL: "https://auth.openai.com/log-in?__cf_chl_tk=example", want: true},
		{name: "visible verification", title: "Just a moment...", visibleText: "Verifying you are human. This may take a few seconds.", want: true},
		{name: "browser verification", title: "Cloudflare", visibleText: "Checking your browser before accessing the site", want: true},
		{name: "visible Chinese verification", title: "请稍候", visibleText: "正在验证您是否是真人", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasCloudflareChallengeSignals(tt.pageURL, tt.title, tt.visibleText); got != tt.want {
				t.Errorf("hasCloudflareChallengeSignals() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWaitOAuthCallbackBrowser(t *testing.T) {
	if os.Getenv("OPENAI_LOGIN_BROWSER_TEST") != "1" {
		t.Skip("set OPENAI_LOGIN_BROWSER_TEST=1 to run the Playwright regression test")
	}

	const (
		consentURL  = "https://auth.openai.com/sign-in-with-chatgpt/codex/consent"
		callbackURL = "http://localhost:1455/auth/callback?code=fixture&state=fixture"
	)

	pw, err := playwright.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Stop()
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
		Channel:  playwright.String("chrome"),
	})
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
		requestURL := route.Request().URL()
		body := `<button type="button">Continue somewhere else</button><button type="button" onclick="document.body.dataset.consentClicked='true';location.href='` + callbackURL + `'"><span>Continue</span></button>`
		if strings.HasPrefix(requestURL, "http://localhost:1455/auth/callback") {
			body = "callback"
		}
		if err := route.Fulfill(playwright.RouteFulfillOptions{
			Body:        body,
			ContentType: playwright.String("text/html"),
		}); err != nil {
			t.Errorf("fulfill %s: %v", requestURL, err)
		}
	}); err != nil {
		t.Fatal(err)
	}

	t.Run("consent click reaches callback", func(t *testing.T) {
		page, err := context.NewPage()
		if err != nil {
			t.Fatal(err)
		}
		defer page.Close()
		callbacks := make(chan string, 1)
		page.OnRequest(func(request playwright.Request) {
			if strings.HasPrefix(request.URL(), "http://localhost:1455/auth/callback") {
				select {
				case callbacks <- request.URL():
				default:
				}
			}
		})
		if _, err := page.Goto(consentURL, playwright.PageGotoOptions{
			WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		}); err != nil {
			t.Fatal(err)
		}
		got, err := waitOAuthCallback(page, callbacks, make(chan struct{}), 3*time.Second, "fixture@example.com", "JBSWY3DPEHPK3PXP")
		if err != nil {
			t.Fatal(err)
		}
		if got != callbackURL {
			t.Fatalf("callback = %q, want %q", got, callbackURL)
		}
	})

	t.Run("buffered callback skips consent", func(t *testing.T) {
		page, err := context.NewPage()
		if err != nil {
			t.Fatal(err)
		}
		defer page.Close()
		callbacks := make(chan string, 1)
		callbacks <- callbackURL
		if _, err := page.Goto(consentURL, playwright.PageGotoOptions{
			WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		}); err != nil {
			t.Fatal(err)
		}
		got, err := waitOAuthCallback(page, callbacks, make(chan struct{}), 3*time.Second, "fixture@example.com", "JBSWY3DPEHPK3PXP")
		if err != nil {
			t.Fatal(err)
		}
		if got != callbackURL {
			t.Fatalf("callback = %q, want %q", got, callbackURL)
		}
		if page.URL() != consentURL {
			t.Fatalf("consent page navigated unexpectedly: %q", page.URL())
		}
		clicked, err := page.Locator("body").GetAttribute("data-consent-clicked")
		if err != nil {
			t.Fatal(err)
		}
		if clicked != "" {
			t.Fatalf("consent button was clicked unexpectedly: %q", clicked)
		}
	})
}

func TestWaitOAuthCallbackDelayedMFABrowser(t *testing.T) {
	if os.Getenv("OPENAI_LOGIN_BROWSER_TEST") != "1" {
		t.Skip("set OPENAI_LOGIN_BROWSER_TEST=1 to run the Playwright regression test")
	}

	const (
		loginURL    = "https://auth.openai.com/log-in"
		mfaURL      = "https://auth.openai.com/mfa-challenge/fixture"
		consentURL  = "https://auth.openai.com/sign-in-with-chatgpt/codex/consent"
		callbackURL = "http://localhost:1455/auth/callback?code=fixture&state=fixture"
	)

	pw, err := playwright.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Stop()
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
		Channel:  playwright.String("chrome"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	context, err := browser.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	defer context.Close()

	var mfaSubmissions int32
	var mfaPostData atomic.Value
	if err := context.Route("**/*", func(route playwright.Route) {
		request := route.Request()
		requestURL := request.URL()
		body := "<html><body>fixture</body></html>"
		switch {
		case requestURL == loginURL:
			// Keep the browser on the post-password page long enough to catch
			// implementations that use a short fixed MFA wait.
			body = `<html><body><p>Password accepted</p><script>setTimeout(() => location.href = '/mfa-challenge/fixture', 11000)</script></body></html>`
		case requestURL == mfaURL:
			body = `<html><body><form method="post" action="/mfa-submit"><input name="code" autocomplete="one-time-code"><button type="submit">继续</button></form></body></html>`
		case strings.HasSuffix(requestURL, "/mfa-submit") && request.Method() == "POST":
			postData, postErr := request.PostData()
			if postErr != nil {
				t.Errorf("read MFA form data: %v", postErr)
			}
			mfaPostData.Store(postData)
			atomic.AddInt32(&mfaSubmissions, 1)
			// Leave a short gap after MFA so the callback deadline must be
			// restarted when the challenge is submitted.
			body = `<html><body><script>setTimeout(() => location.href = '/sign-in-with-chatgpt/codex/consent', 1500)</script></body></html>`
		case strings.HasPrefix(requestURL, consentURL):
			body = `<html><body><button type="button" onclick="location.href='` + callbackURL + `'">授权</button></body></html>`
		case strings.HasPrefix(requestURL, "http://localhost:1455/auth/callback"):
			body = "callback"
		}
		if err := route.Fulfill(playwright.RouteFulfillOptions{
			Body:        body,
			ContentType: playwright.String("text/html; charset=utf-8"),
		}); err != nil {
			t.Errorf("fulfill %s: %v", requestURL, err)
		}
	}); err != nil {
		t.Fatal(err)
	}

	page, err := context.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	callbacks := make(chan string, 1)
	page.OnRequest(func(request playwright.Request) {
		if strings.HasPrefix(request.URL(), "http://localhost:1455/auth/callback") {
			select {
			case callbacks <- request.URL():
			default:
			}
		}
	})
	if _, err := page.Goto(loginURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := waitOAuthCallback(page, callbacks, make(chan struct{}), 20*time.Second, "fixture@example.com", "JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	if got != callbackURL {
		t.Fatalf("callback = %q, want %q", got, callbackURL)
	}
	if got := atomic.LoadInt32(&mfaSubmissions); got != 1 {
		t.Fatalf("MFA submissions = %d, want 1", got)
	}
	postData, ok := mfaPostData.Load().(string)
	if !ok || !strings.HasPrefix(postData, "code=") || strings.TrimPrefix(postData, "code=") == "" {
		t.Fatalf("MFA form did not include a code: %q", postData)
	}
}

func TestValidateHTTPProxy(t *testing.T) {
	proxyURL, err := parseHTTPProxy("http://test-user:test-password@proxy.example.com:8080")
	if err != nil {
		t.Fatalf("parseHTTPProxy() error = %v", err)
	}
	if proxyURL.Host != "proxy.example.com:8080" || proxyURL.User.Username() != "test-user" {
		t.Fatalf("unexpected proxy URL: %v", proxyURL)
	}
	password, ok := proxyURL.User.Password()
	if !ok || password != "test-password" {
		t.Fatalf("unexpected proxy password: %q", password)
	}

	for _, raw := range []string{
		"https://proxy.example.com:8080",
		"proxy.example.com:8080",
		"http:///missing-host",
		"http://proxy.example.com/path",
	} {
		if err := ValidateHTTPProxy(raw); err == nil {
			t.Errorf("ValidateHTTPProxy(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestParseOAuthIdentityUsesAccessTokenExpiryAndIDClaims(t *testing.T) {
	idToken := testJWT(t, map[string]interface{}{
		"email": "user@example.com",
		"exp":   float64(2000000000),
		"https://api.openai.com/auth": map[string]interface{}{
			"chatgpt_account_id": "acct_123",
			"chatgpt_plan_type":  "plus",
			"organizations": []interface{}{
				map[string]interface{}{"id": "org_default", "is_default": true},
			},
		},
	})

	identity, err := parseOAuthIdentity(testJWT(t, map[string]interface{}{"exp": float64(2100000000)}), idToken)
	if err != nil {
		t.Fatalf("parseOAuthIdentity() error = %v", err)
	}
	if identity.Email != "user@example.com" || identity.ChatGPTAccountID != "acct_123" || identity.OrganizationID != "org_default" || identity.PlanType != "plus" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
	if identity.ExpiresAt != 2100000000 {
		t.Fatalf("expiry = %d, want access-token exp 2100000000", identity.ExpiresAt)
	}
}

func TestParseOAuthIdentityFallsBackToIDTokenExpiry(t *testing.T) {
	idToken := testJWT(t, map[string]interface{}{
		"email": "user@example.com",
		"exp":   float64(2000000000),
	})
	identity, err := parseOAuthIdentity(testJWT(t, map[string]interface{}{}), idToken)
	if err != nil {
		t.Fatalf("parseOAuthIdentity() error = %v", err)
	}
	if identity.ExpiresAt != 2000000000 {
		t.Fatalf("expiry = %d, want ID-token fallback 2000000000", identity.ExpiresAt)
	}
}

func testJWT(t *testing.T, claims map[string]interface{}) string {
	t.Helper()
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString(body)
	return "e30." + encode + ".sig"
}
