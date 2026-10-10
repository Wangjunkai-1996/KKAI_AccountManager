package login

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

func TestShouldUseNativeChrome(t *testing.T) {
	for _, test := range []struct {
		name   string
		config Config
		want   bool
	}{
		{name: "headed direct", config: Config{}, want: true},
		{name: "headless direct", config: Config{Headless: true}, want: false},
		{name: "headed unauthenticated proxy", config: Config{Proxy: "http://127.0.0.1:7897"}, want: true},
		{name: "headed authenticated proxy", config: Config{Proxy: "http://user:pass@127.0.0.1:7897"}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldUseNativeChrome(test.config); got != test.want {
				t.Fatalf("shouldUseNativeChrome() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestFindSystemChromeHonorsConfiguredPath(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "chrome")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_LOGIN_CHROME_PATH", file.Name())
	got, err := findSystemChrome()
	if err != nil {
		t.Fatalf("findSystemChrome() error = %v", err)
	}
	if got != file.Name() {
		t.Fatalf("findSystemChrome() = %q, want %q", got, file.Name())
	}
}

func TestWaitForChromeCDPContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitForChromeCDP(ctx, "http://127.0.0.1:1", time.Second, make(chan struct{}))
	if err != context.Canceled {
		t.Fatalf("waitForChromeCDP() error = %v, want context canceled", err)
	}
}

func TestNativeChromeStartupSmoke(t *testing.T) {
	if os.Getenv("OPENAI_LOGIN_STARTUP_SMOKE") != "1" {
		t.Skip("set OPENAI_LOGIN_STARTUP_SMOKE=1 to test native Chrome startup without network or account access")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session, err := startNativeChrome(ctx, Config{Timeout: 15 * time.Second, Proxy: ""})
	if err != nil {
		t.Fatalf("native Chrome startup failed: %v", err)
	}
	defer session.close()
	defer session.pw.Stop()
	defer session.browser.Close()

	contexts := session.browser.Contexts()
	if len(contexts) == 0 || len(contexts[0].Pages()) == 0 {
		t.Fatal("native Chrome did not expose an initial page")
	}
	value, err := contexts[0].Pages()[0].Evaluate("() => 1 + 1")
	if err != nil {
		t.Fatalf("native Chrome page evaluation failed: %v", err)
	}
	if fmt.Sprint(value) != "2" {
		t.Fatalf("native Chrome page evaluation = %v, want 2", value)
	}
}

func TestPlaywrightBrowserStartupSmoke(t *testing.T) {
	if os.Getenv("OPENAI_LOGIN_STARTUP_SMOKE") != "1" {
		t.Skip("set OPENAI_LOGIN_STARTUP_SMOKE=1 to test Playwright startup without network or account access")
	}
	executable, err := findSystemChrome()
	if err != nil {
		t.Fatalf("find system Chrome: %v", err)
	}
	for _, headless := range []bool{true, false} {
		t.Run(fmt.Sprintf("headless=%t", headless), func(t *testing.T) {
			pw, err := runPlaywright()
			if err != nil {
				t.Fatalf("Playwright driver startup failed: %v", err)
			}
			defer pw.Stop()
			browser, err := pw.Chromium.Launch(browserLaunchOptions(Config{
				Headless: headless,
				Timeout:  15 * time.Second,
			}, executable))
			if err != nil {
				t.Fatalf("Playwright browser startup failed: %v", err)
			}
			defer browser.Close()
			page, err := browser.NewPage()
			if err != nil {
				t.Fatalf("Playwright page creation failed: %v", err)
			}
			value, err := page.Evaluate("() => 1 + 1")
			if err != nil {
				t.Fatalf("Playwright page evaluation failed: %v", err)
			}
			if fmt.Sprint(value) != "2" {
				t.Fatalf("Playwright page evaluation = %v, want 2", value)
			}
		})
	}
}

func TestNativeChromeFingerprint(t *testing.T) {
	if os.Getenv("OPENAI_LOGIN_BROWSER_TEST") != "1" {
		t.Skip("set OPENAI_LOGIN_BROWSER_TEST=1 to inspect the native Chrome environment")
	}
	session, err := startNativeChrome(context.Background(), Config{
		Proxy:   "http://127.0.0.1:7897",
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.close()
	defer session.browser.Close()
	defer session.pw.Stop()

	contexts := session.browser.Contexts()
	if len(contexts) == 0 {
		t.Fatal("native Chrome did not expose a browser context")
	}
	pages := contexts[0].Pages()
	if len(pages) == 0 {
		t.Fatal("native Chrome did not expose an initial page")
	}
	value, err := pages[0].Evaluate(`() => ({webdriver: navigator.webdriver, userAgent: navigator.userAgent, language: navigator.language, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone})`)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(fmt.Sprintf("native Chrome environment: %v", value))
}

func TestNativeChromeAuthSurface(t *testing.T) {
	if os.Getenv("OPENAI_LOGIN_BROWSER_TEST") != "1" {
		t.Skip("set OPENAI_LOGIN_BROWSER_TEST=1 to compare auth and OAuth surfaces")
	}
	session, err := startNativeChrome(context.Background(), Config{
		Proxy:   "http://127.0.0.1:7897",
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.close()
	defer session.browser.Close()
	defer session.pw.Stop()
	contexts := session.browser.Contexts()
	if len(contexts) == 0 || len(contexts[0].Pages()) == 0 {
		t.Fatal("native Chrome did not expose an initial page")
	}
	page := contexts[0].Pages()[0]
	codeVerifier := "fixture-code-verifier-with-enough-length-for-pkce"
	codeChallenge := sha256.Sum256([]byte(codeVerifier))
	for _, target := range []string{
		"https://auth.openai.com/log-in",
		oauthAuthorize + "?response_type=code&client_id=" + oauthClientID + "&redirect_uri=" + url.QueryEscape(oauthRedirectURI) + "&scope=openid+profile+email+offline_access&state=fixture&code_challenge=" + base64.RawURLEncoding.EncodeToString(codeChallenge[:]) + "&code_challenge_method=S256&id_token_add_organizations=true&codex_cli_simplified_flow=true",
	} {
		response, gotoErr := page.Goto(target, playwright.PageGotoOptions{
			WaitUntil: playwright.WaitUntilStateDomcontentloaded,
			Timeout:   playwright.Float(30000),
		})
		status := 0
		if response != nil {
			status = response.Status()
		}
		title, _ := page.Title()
		t.Logf("auth surface path=%s status=%d goto_error=%v title=%q challenge=%v", authDiagnosticPath(target), status, gotoErr != nil, title, isCloudflareChallenge(page))
	}
}
