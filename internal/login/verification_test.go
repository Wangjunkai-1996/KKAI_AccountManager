package login

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

func TestDiscardEvidencePreservesStage(t *testing.T) {
	for _, code := range []string{"invalid_password", "email_verification_required"} {
		for _, stage := range []string{LoginStageToken, LoginStageProxy, LoginStageUnknown} {
			info := DescribeError(&authHTTPStatusError{Status: http.StatusForbidden, Stage: stage, UpstreamCode: code})
			if info.Stage != stage {
				t.Fatalf("upstream %q at %q was assigned to %q", code, stage, info.Stage)
			}
		}
	}
}

func TestVerificationErrorClassification(t *testing.T) {
	for _, tc := range []struct{ raw, stage, code, message string }{
		{"invalid_password", LoginStagePassword, LoginErrorPasswordRejected, "密码错误"},
		{"incorrect_password", LoginStagePassword, LoginErrorPasswordRejected, "密码错误"},
		{"invalid_totp", LoginStageMFA, LoginErrorTOTPRejected, "TOTP 验证码被拒绝"},
		{"invalid_otp", LoginStageMFA, LoginErrorVerificationCodeRejected, "验证类型待确认"},
		{"invalid_mfa", LoginStageMFA, LoginErrorVerificationCodeRejected, "验证类型待确认"},
		{"incorrect_code", LoginStageMFA, LoginErrorVerificationCodeRejected, "验证类型待确认"},
		{"invalid_credentials", LoginStagePassword, LoginErrorCredentialsRejected, "登录凭据被拒绝"},
		{LoginErrorEmailVerificationRequired, LoginStageEmail, LoginErrorEmailVerificationRequired, "邮箱验证码"},
		{LoginErrorPhoneVerificationRequired, LoginStagePhone, LoginErrorPhoneVerificationRequired, "手机或短信验证"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			failure := &authHTTPStatusError{Status: http.StatusForbidden, Stage: tc.stage, UpstreamCode: tc.raw, Message: "private upstream message"}
			for _, err := range []error{failure, errors.Join(context.DeadlineExceeded, failure), errors.Join(ErrAuthPageTimeout, failure)} {
				info := DescribeError(err)
				if info.Code != tc.code || info.Stage != tc.stage || info.HTTPStatus != 403 || info.Retryable || !IsDeterministicFailure(info) || !strings.Contains(info.Message, tc.message) || strings.Contains(info.Message, "private") {
					t.Fatalf("wrong safe terminal classification: %+v", info)
				}
				if tc.raw != tc.code && info.UpstreamCode != tc.raw {
					t.Fatalf("upstream code missing: %+v", info)
				}
				if NormalizeErrorInfo(info) != info || retryableLoginError(err) {
					t.Fatal("terminal classification changed on reuse")
				}
			}
			calls := 0
			_, err := loginWithRetries(context.Background(), 3, func(context.Context) (*LoginResult, error) {
				calls++
				return nil, failure
			})
			if calls != 1 || err == nil {
				t.Fatalf("deterministic error retried: calls=%d", calls)
			}
			if info := DescribeError(errors.Join(context.Canceled, failure)); info.Code != LoginErrorCanceled || info.Retryable {
				t.Fatalf("cancellation lost precedence: %+v", info)
			}
		})
	}
	for _, status := range []int{429, 503} {
		if info := DescribeError(&authHTTPStatusError{Status: status}); !info.Retryable || IsDeterministicFailure(info) {
			t.Fatalf("temporary HTTP failure changed: %+v", info)
		}
	}
}

type verificationFixturePage struct {
	playwright.Page
	address, body, alert  string
	code, phone, password bool
}

func (p verificationFixturePage) URL() string            { return p.address }
func (p verificationFixturePage) Title() (string, error) { return "fixture", nil }
func (p verificationFixturePage) Locator(selector string, _ ...playwright.PageLocatorOptions) playwright.Locator {
	visible, content := false, ""
	switch {
	case selector == "body":
		visible, content = true, p.body
	case strings.Contains(selector, "role='alert'"):
		visible, content = p.alert != "", p.alert
	case strings.Contains(selector, "one-time-code"):
		visible = p.code
	case strings.Contains(selector, "type='tel'"):
		visible = p.phone
	case strings.Contains(selector, "type='password'"):
		visible = p.password
	}
	return verificationFixtureLocator{visible: visible, content: content}
}

type verificationFixtureLocator struct {
	unusedAuthLocator
	visible bool
	content string
}

func (l verificationFixtureLocator) First() playwright.Locator { return l }
func (l verificationFixtureLocator) IsVisible(...playwright.LocatorIsVisibleOptions) (bool, error) {
	return l.visible, nil
}
func (l verificationFixtureLocator) Evaluate(string, any, ...playwright.LocatorEvaluateOptions) (any, error) {
	// The unit fixture models visible prompt text. Real DOM filtering is covered
	// by TestVerificationPagesBrowser.
	return l.content, nil
}

func (l verificationFixtureLocator) InnerText(...playwright.LocatorInnerTextOptions) (string, error) {
	return l.content, nil
}

func TestVerificationPageEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		page verificationFixturePage
		code string
		totp bool
	}{
		{"email", verificationFixturePage{address: "https://auth.openai.com/email-verification", code: true}, LoginErrorEmailVerificationRequired, false},
		{"phone", verificationFixturePage{address: "https://auth.openai.com/phone-verification", phone: true}, LoginErrorPhoneVerificationRequired, false},
		{"SMS", verificationFixturePage{address: "https://auth.openai.com/mfa-challenge/sms", code: true}, LoginErrorPhoneVerificationRequired, false},
		{"email through MFA", verificationFixturePage{address: "https://auth.openai.com/mfa-challenge/fixture", body: "Enter the code we sent to your email", code: true}, LoginErrorEmailVerificationRequired, false},
		{"ordinary email input", verificationFixturePage{address: "https://auth.openai.com/log-in", body: "Email phone SMS password"}, "", false},
		{"untrusted origin", verificationFixturePage{address: "https://auth.openai.com.evil.test/email-verification", code: true}, "", false},
		{"no code control", verificationFixturePage{address: "https://auth.openai.com/email-verification", body: "Check your email"}, "", false},
		{"unknown MFA", verificationFixturePage{address: "https://auth.openai.com/mfa-challenge/fixture", body: "Enter your code", code: true}, "", false},
		{"authenticator alternative", verificationFixturePage{address: "https://auth.openai.com/mfa-challenge/fixture", body: "Enter your code. Use an authenticator app instead", code: true}, "", false},
		{"observed authenticator prompt", verificationFixturePage{address: "https://auth.openai.com/mfa-challenge/opaque-id", body: "Check your authenticator app\nEnter the one-time authentication code shown in your app to verify your identity.", code: true}, "", true},
		{"observed current-code prompt", verificationFixturePage{address: "https://auth.openai.com/mfa-challenge/opaque-id", body: "Check your authenticator app\nEnter the current 6-digit code for your account from your authenticator app.", code: true}, "", true},
		{"authenticator heading only", verificationFixturePage{address: "https://auth.openai.com/mfa-challenge/opaque-id", body: "Check your authenticator app", code: true}, "", false},
		{"app instruction only", verificationFixturePage{address: "https://auth.openai.com/mfa-challenge/opaque-id", body: "Enter the one-time authentication code shown in your app to verify your identity.", code: true}, "", false},
		{"authenticator alternative label", verificationFixturePage{address: "https://auth.openai.com/mfa-challenge/opaque-id", body: "Enter your code. Other methods: Check your authenticator app", code: true}, "", false},
		{"observed prompt without input", verificationFixturePage{address: "https://auth.openai.com/mfa-challenge/opaque-id", body: "Check your authenticator app\nEnter the one-time authentication code shown in your app to verify your identity."}, "", false},
		{"observed prompt on untrusted origin", verificationFixturePage{address: "https://other.example/mfa-challenge/opaque-id", body: "Check your authenticator app\nEnter the one-time authentication code shown in your app to verify your identity.", code: true}, "", false},
		{"authenticator MFA", verificationFixturePage{address: "https://auth.openai.com/mfa-challenge/fixture", body: "Enter the code from your authenticator app", code: true}, "", true},
		{"TOTP path", verificationFixturePage{address: "https://auth.openai.com/mfa-challenge/totp", code: true}, "", true},
		{"password rejection", verificationFixturePage{address: "https://auth.openai.com/log-in/password", password: true, alert: "Incorrect password"}, LoginErrorPasswordRejected, false},
		{"generic credentials", verificationFixturePage{address: "https://auth.openai.com/log-in/password", password: true, alert: "Incorrect email or password"}, LoginErrorCredentialsRejected, false},
		{"hidden password message", verificationFixturePage{address: "https://auth.openai.com/log-in/password", password: true, body: "Incorrect password"}, "", false},
		{"generic code rejection", verificationFixturePage{address: "https://auth.openai.com/mfa-challenge/fixture", code: true, alert: "Incorrect code"}, LoginErrorVerificationCodeRejected, false},
		{"TOTP rejection", verificationFixturePage{address: "https://auth.openai.com/mfa-challenge/totp", code: true, alert: "Incorrect code"}, LoginErrorTOTPRejected, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := accountRejectionOnPage(tc.page)
			if tc.code == "" && err != nil || tc.code != "" && DescribeError(err).Code != tc.code || isTOTPChallenge(tc.page) != tc.totp {
				t.Fatalf("wrong page evidence: code=%s TOTP=%v", DescribeError(err).Code, isTOTPChallenge(tc.page))
			}
		})
	}
}

func TestVerificationWaitStopsBeforeTimeoutOrTOTP(t *testing.T) {
	for _, tc := range []struct{ path, code string }{{"/email-verification", LoginErrorEmailVerificationRequired}, {"/mfa-challenge/sms", LoginErrorPhoneVerificationRequired}} {
		page := verificationFixturePage{address: "https://auth.openai.com" + tc.path, code: true}
		for _, expired := range []bool{false, true} {
			ctx := context.Background()
			if expired {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			}
			_, callbackErr := waitOAuthCallbackContext(ctx, page, nil, nil, time.Second, "", "unused")
			selectorErr := waitForAuthSelector(ctx, page, "input[type='password']", time.Second, nil, nil)
			for _, err := range []error{callbackErr, selectorErr} {
				if info := DescribeError(err); info.Code != tc.code || info.Retryable || info.HTTPStatus != 0 {
					t.Fatalf("verification replaced by timeout: %+v", info)
				}
			}
		}
	}
	page := verificationFixturePage{address: "https://auth.openai.com/mfa-challenge/unknown", code: true}
	_, err := waitOAuthCallbackContext(context.Background(), page, nil, nil, 300*time.Millisecond, "", "unused")
	if !errors.Is(err, ErrAuthPageTimeout) {
		t.Fatalf("unknown code control was treated as TOTP: %v", err)
	}
}

func TestVerificationHTTPKeepsStatusAndEvidence(t *testing.T) {
	page := verificationFixturePage{address: "https://auth.openai.com/mfa-challenge/totp", code: true}
	failure := &authHTTPStatusError{Status: 403, Stage: LoginStageMFA, UpstreamCode: "incorrect_code"}
	info := DescribeError(authFailureWithPage(page, failure))
	if info.Code != LoginErrorTOTPRejected || info.UpstreamCode != "incorrect_code" || info.HTTPStatus != 403 || info.Retryable {
		t.Fatalf("TOTP page evidence lost: %+v", info)
	}
	page.address = "https://auth.openai.com/email-verification"
	info = DescribeError(authFailureWithPage(page, &authHTTPStatusError{Status: 403, Message: "Forbidden"}))
	if info.Code != LoginErrorEmailVerificationRequired || info.HTTPStatus != 403 || info.Retryable {
		t.Fatalf("verification page overridden by HTTP failure: %+v", info)
	}
	statuses := make(chan error, 1)
	statuses <- &authHTTPStatusError{Status: 403, Message: "Forbidden"}
	_, err := waitOAuthCallbackContext(context.Background(), page, nil, nil, time.Second, "", "unused", statuses)
	if info := DescribeError(err); info.Code != LoginErrorEmailVerificationRequired || info.HTTPStatus != 403 {
		t.Fatalf("page check discarded queued HTTP evidence: %+v", info)
	}
}

func TestVerificationPagesBrowser(t *testing.T) {
	if os.Getenv("OPENAI_LOGIN_BROWSER_TEST") != "1" {
		t.Skip("set OPENAI_LOGIN_BROWSER_TEST=1 for local verification page fixtures")
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
	for _, tc := range []struct {
		name, path, html, code string
		totp                   bool
	}{
		{"email", "/email-verification", `<h1>Check your email</h1><input name="code" autocomplete="one-time-code">`, LoginErrorEmailVerificationRequired, false},
		{"phone", "/phone-verification", `<h1>Verify your phone</h1><input type="tel">`, LoginErrorPhoneVerificationRequired, false},
		{"SMS", "/mfa-challenge/sms", `<input name="code" autocomplete="one-time-code">`, LoginErrorPhoneVerificationRequired, false},
		{"password", "/log-in/password", `<input type="password"><p role="alert">Incorrect password</p>`, LoginErrorPasswordRejected, false},
		{"TOTP rejected", "/mfa-challenge/totp", `<input name="code"><p role="alert">Incorrect code</p>`, LoginErrorTOTPRejected, true},
		{"observed TOTP", "/mfa-challenge/opaque-id", `<h1>Check your authenticator app</h1><p>Enter the one-time authentication code shown in your app to verify your identity.</p><input name="code" type="text" autocomplete="one-time-code">`, "", true},
		{"observed current-code TOTP", "/mfa-challenge/opaque-id", `<h1>Check your authenticator app</h1><p>Enter the current 6-digit code for your account from your authenticator app.</p><input name="code" type="text" autocomplete="one-time-code"><button>Continue</button>`, "", true},
		{"current-code alternative only", "/mfa-challenge/opaque-id", `<h1>Enter your verification code</h1><input name="code"><a href="/other-method">Check your authenticator app. Enter the current 6-digit code for your account from your authenticator app.</a>`, "", false},
		{"observed TOTP verify button", "/mfa-challenge/opaque-id", `<h1>Check your authenticator app</h1><p>Enter the one-time authentication code shown in your app to verify your identity.</p><input name="code" type="text" autocomplete="one-time-code"><button type="submit">Verify</button>`, "", true},
		{"observed TOTP authenticator button", "/mfa-challenge/opaque-id", `<h1>Check your authenticator app</h1><p>Enter the one-time authentication code shown in your app to verify your identity.</p><form><input name="code" type="text" autocomplete="one-time-code"><button><span>Authenticator</span></button></form>`, "", true},
		{"email with email button", "/mfa-challenge/opaque-id", `<h1>Check your email</h1><form><input name="code"><button><span>Email</span></button></form>`, LoginErrorEmailVerificationRequired, false},
		{"observed TOTP otp input", "/mfa-challenge/opaque-id", `<h1>Check your authenticator app</h1><p>Enter the one-time authentication code shown in your app to verify your identity.</p><input name="otp" type="text">`, "", true},
		{"authenticator alternative only", "/mfa-challenge/opaque-id", `<h1>Enter your verification code</h1><input name="code"><a href="/other-method">Check your authenticator app</a>`, "", false},
		{"authenticator alternative instruction", "/mfa-challenge/opaque-id", `<h1>Enter your verification code</h1><input name="code"><a href="/other-method">Use a code from your authenticator app instead</a>`, "", false},
		{"authenticator role button alternative", "/mfa-challenge/opaque-id", `<h1>Enter your verification code</h1><input name="code"><div role="button"><span>Use a code from your authenticator app instead</span></div>`, "", false},
		{"email role link alternative", "/mfa-challenge/opaque-id", `<h1>Enter your verification code</h1><input name="code"><div role="link"><span>Check your email</span></div>`, "", false},
		{"TOTP with alternative email", "/mfa-challenge/opaque-id", `<h1>Check your authenticator app</h1><p>Enter the one-time authentication code shown in your app to verify your identity.</p><input name="code"><a href="/email-method">Check your email</a>`, "", true},
		{"hidden authenticator prompt", "/mfa-challenge/opaque-id", `<h1>Enter your verification code</h1><div hidden>Check your authenticator app<p>Enter the one-time authentication code shown in your app to verify your identity.</p></div><input name="code">`, "", false},
		{"invisible authenticator prompt", "/mfa-challenge/opaque-id", `<h1>Enter your verification code</h1><div style="visibility:hidden"><h1>Check your authenticator app</h1><p>Enter the one-time authentication code shown in your app to verify your identity.</p></div><input name="code">`, "", false},
		{"unrendered authenticator prompt", "/mfa-challenge/opaque-id", `<h1>Enter your verification code</h1><div style="display:none"><h1>Check your authenticator app</h1><p>Enter the one-time authentication code shown in your app to verify your identity.</p></div><input name="code">`, "", false},
		{"script and style authenticator prompt", "/mfa-challenge/opaque-id", `<h1>Enter your verification code</h1><script type="application/json">"Check your authenticator app. Enter the one-time authentication code shown in your app to verify your identity."</script><style>/* Check your authenticator app. Enter the one-time authentication code shown in your app to verify your identity. */</style><input name="code">`, "", false},
		{"ordinary email", "/log-in", `<input type="email"><p>Use your phone or email</p>`, "", false},
		{"hidden code", "/email-verification", `<input name="code" hidden><p>Loading</p>`, "", false},
		{"hidden rejection", "/log-in/password", `<input type="password"><p role="alert" hidden>Incorrect password</p>`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := browser.NewPage()
			if err != nil {
				t.Fatal(err)
			}
			defer page.Close()
			if err := page.Route("**/*", func(route playwright.Route) {
				if err := route.Fulfill(playwright.RouteFulfillOptions{Status: playwright.Int(200), ContentType: playwright.String("text/html"), Body: tc.html}); err != nil {
					t.Error(err)
				}
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := page.Goto("https://auth.openai.com" + tc.path); err != nil {
				t.Fatal(err)
			}
			if got := isTOTPChallenge(page); got != tc.totp {
				t.Fatalf("TOTP detection=%v want=%v", got, tc.totp)
			}
			err = accountRejectionOnPage(page)
			if tc.code == "" && err != nil || tc.code != "" && DescribeError(err).Code != tc.code {
				t.Fatalf("wrong real DOM evidence: %+v", DescribeError(err))
			}
			if tc.code != "" {
				_, err = waitOAuthCallbackContext(context.Background(), page, nil, nil, time.Second, "", "unused")
				if info := DescribeError(err); info.Code != tc.code || info.Retryable || info.HTTPStatus != 0 {
					t.Fatalf("wait lost page evidence: %+v", info)
				}
			}
		})
	}
}
