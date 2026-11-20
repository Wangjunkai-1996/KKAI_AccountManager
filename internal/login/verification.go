package login

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/mxschmitt/playwright-go"
)

const (
	LoginErrorEmailVerificationRequired = "email_verification_required"
	LoginErrorPhoneVerificationRequired = "phone_verification_required"
	LoginErrorPasswordRejected          = "password_rejected"
	LoginErrorTOTPRejected              = "totp_rejected"
	LoginErrorVerificationCodeRejected  = "verification_code_rejected"
	LoginErrorCredentialsRejected       = "credentials_rejected"
)

// NormalizeErrorInfo is the single mapping for current and saved auth errors.
// Generic credentials/code errors never imply a specific password or MFA method.
func NormalizeErrorInfo(info LoginErrorInfo) LoginErrorInfo {
	original := normalizeAuthCode(info.Code)
	code, stage, message := original, info.Stage, ""
	switch original {
	case LoginErrorEmailVerificationRequired, "email_verification_needed":
		code, message = LoginErrorEmailVerificationRequired, "需要邮箱验证码，已停止自动重试"
	case LoginErrorPhoneVerificationRequired, "phone_verification_needed", "sms_verification_required":
		code, stage, message = LoginErrorPhoneVerificationRequired, LoginStagePhone, "需要手机或短信验证，已停止自动重试"
	case LoginErrorPasswordRejected, "invalid_password", "incorrect_password", "wrong_password":
		code, message = LoginErrorPasswordRejected, "密码错误，已停止自动重试"
	case LoginErrorTOTPRejected, "invalid_totp", "incorrect_totp":
		code, stage, message = LoginErrorTOTPRejected, LoginStageMFA, "TOTP 验证码被拒绝，已停止自动重试"
	case LoginErrorVerificationCodeRejected, "incorrect_code", "invalid_code", "invalid_otp", "invalid_mfa":
		code, message = LoginErrorVerificationCodeRejected, "验证码错误，验证类型待确认，已停止自动重试"
	case LoginErrorCredentialsRejected, "invalid_credentials", "incorrect_credentials":
		code, message = LoginErrorCredentialsRejected, "登录凭据被拒绝，已停止自动重试"
	}
	if message == "" {
		return info
	}
	if info.UpstreamCode == "" && original != code {
		info.UpstreamCode = original
	}
	info.UpstreamCode = normalizeAuthCode(info.UpstreamCode)
	info.Code, info.Stage, info.Message, info.Retryable = code, stage, message, false
	if info.Stage == "" {
		info.Stage = LoginStageUnknown
	}
	return info
}

func IsDeterministicFailure(info LoginErrorInfo) bool {
	switch NormalizeErrorInfo(info).Code {
	case LoginErrorEmailVerificationRequired, LoginErrorPhoneVerificationRequired,
		LoginErrorPasswordRejected, LoginErrorTOTPRejected,
		LoginErrorVerificationCodeRejected, LoginErrorCredentialsRejected:
		return true
	}
	return false
}

func verificationRejection(code, stage string) *authRejectionError {
	info := NormalizeErrorInfo(LoginErrorInfo{Code: code, Stage: stage})
	return &authRejectionError{Code: info.Code, Stage: info.Stage, Message: info.Message}
}

const authCodeInputSelector = "input[name='code']:visible, input[name='otp']:visible, input[autocomplete='one-time-code']:visible"

func visibleAuthControl(page playwright.Page, selector string) bool {
	visible, err := page.Locator(selector).First().IsVisible()
	return err == nil && visible
}

// This is page-type evidence, not a search for arbitrary email/phone words.
// The caller has already checked the trusted auth origin.
func authVerificationKind(page playwright.Page, path string) string {
	path = strings.ToLower(path)
	codeInput := visibleAuthControl(page, authCodeInputSelector)
	phoneInput := visibleAuthControl(page, "input[type='tel']:visible, input[name='phone_number']:visible")
	if (path == "/email-verification" || strings.HasPrefix(path, "/email-verification/")) && codeInput {
		return "email"
	}
	for _, prefix := range []string{"/phone-verification", "/phone-number-verification", "/phone-number", "/verify-phone", "/sms-verification", "/mfa-challenge/phone", "/mfa-challenge/sms"} {
		if (path == prefix || strings.HasPrefix(path, prefix+"/")) && (codeInput || phoneInput) {
			return "phone"
		}
	}
	if !strings.HasPrefix(path, "/mfa-challenge/") || !codeInput {
		return ""
	}
	// Read rendered text nodes in place: alternative-method controls are not
	// current challenge evidence, even when their labels also occur in the prompt.
	value, err := page.Locator("body").Evaluate(`body => {
		const walker = document.createTreeWalker(body, NodeFilter.SHOW_TEXT);
		const texts = [];
		while (walker.nextNode()) {
			const node = walker.currentNode;
			const parent = node.parentElement;
			if (!node.textContent.trim() || !parent || parent.closest('a,button,[role="link"],[role="button"],script,style,noscript,template,[hidden]')) continue;
			const visibility = getComputedStyle(parent).visibility;
			if (visibility === 'hidden' || visibility === 'collapse') continue;
			const range = document.createRange();
			range.selectNodeContents(node);
			if (Array.from(range.getClientRects()).some(rect => rect.width > 0 && rect.height > 0)) texts.push(node.textContent);
		}
		return texts.join(' ');
	}`, nil, playwright.LocatorEvaluateOptions{Timeout: playwright.Float(1000)})
	if err != nil {
		return ""
	}
	body, ok := value.(string)
	if !ok {
		return ""
	}
	body = strings.ToLower(strings.Join(strings.Fields(body), " "))
	if containsAny(body, []string{"enter the code sent to your email", "enter the code we sent to your email", "check your email", "邮箱验证码", "邮件中的验证码"}) {
		return "email"
	}
	if containsAny(body, []string{"enter the code sent to your phone", "enter the code we sent to your phone", "sent you a text message", "短信验证码", "手机验证码"}) {
		return "phone"
	}
	// The observed opaque challenge URL identifies TOTP through both its
	// current prompt lines; mentioning an authenticator alternative alone is insufficient.
	observedAuthenticatorPrompt := strings.Contains(body, "check your authenticator app") &&
		containsAny(body, []string{
			"enter the one-time authentication code shown in your app to verify your identity.",
			"enter the current 6-digit code for your account from your authenticator app.",
		})
	if observedAuthenticatorPrompt || path == "/mfa-challenge/totp" || strings.HasPrefix(path, "/mfa-challenge/totp/") ||
		path == "/mfa-challenge/authenticator" || strings.HasPrefix(path, "/mfa-challenge/authenticator/") ||
		containsAny(body, []string{"code from your authenticator", "code generated by your authenticator", "code in your authenticator", "open your authenticator app", "身份验证器中的验证码", "身份验证应用中的验证码", "打开身份验证器"}) {
		return "totp"
	}
	return ""
}

func verificationPageRejection(page playwright.Page, path string) error {
	kind := authVerificationKind(page, path)
	switch kind {
	case "email":
		return verificationRejection(LoginErrorEmailVerificationRequired, LoginStageEmail)
	case "phone":
		return verificationRejection(LoginErrorPhoneVerificationRequired, LoginStagePhone)
	}
	alert := page.Locator("[role='alert']:visible, [aria-live='assertive']:visible, [data-testid='error-message']:visible").First()
	visible, err := alert.IsVisible()
	if err != nil || !visible {
		return nil
	}
	text, err := alert.InnerText(playwright.LocatorInnerTextOptions{Timeout: playwright.Float(1000)})
	if err != nil {
		return nil
	}
	text = strings.ToLower(strings.Join(strings.Fields(text), " "))
	if strings.Contains(path, "/password") && visibleAuthControl(page, "input[type='password']:visible") {
		if containsAny(text, []string{"incorrect email or password", "wrong email or password", "invalid credentials", "邮箱或密码错误"}) {
			return verificationRejection(LoginErrorCredentialsRejected, LoginStagePassword)
		}
		if containsAny(text, []string{"incorrect password", "invalid password", "wrong password", "密码错误", "密码不正确"}) {
			return verificationRejection(LoginErrorPasswordRejected, LoginStagePassword)
		}
	}
	if strings.HasPrefix(path, "/mfa-challenge/") && containsAny(text, []string{"incorrect code", "invalid code", "code is incorrect", "code is invalid", "验证码错误", "验证码不正确"}) {
		if kind == "totp" {
			return verificationRejection(LoginErrorTOTPRejected, LoginStageMFA)
		}
		return verificationRejection(LoginErrorVerificationCodeRejected, LoginStageMFA)
	}
	return nil
}

func isTOTPChallenge(page playwright.Page) bool {
	if page == nil {
		return false
	}
	u, err := url.Parse(page.URL())
	if err != nil || u.Scheme != "https" || u.Host != "auth.openai.com" {
		return false
	}
	return authVerificationKind(page, u.Path) == "totp"
}

// A known verification page must not become a retryable timeout when a
// deadline wins the select. Explicit cancellation still takes precedence.
func authPageWaitFailure(page playwright.Page, err error) error {
	if !errors.Is(err, context.Canceled) {
		if rejection := accountRejectionOnPage(page); rejection != nil {
			return errors.Join(err, rejection)
		}
	}
	return authPageWaitContextError(err)
}
