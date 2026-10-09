// Package login provides OpenAI automatic login functionality
package login

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mxschmitt/playwright-go"
)

var ErrCloudflareChallenge = errors.New("auth.openai.com 被 Cloudflare challenge 拦截")
var ErrUnsupportedRegion = errors.New("OpenAI 拒绝当前网络出口：Country, region, or territory not supported")
var ErrUnexpectedAuthPage = errors.New("OpenAI 登录流程未进入预期页面")
var ErrAuthConnectionReset = errors.New("访问 auth.openai.com 时连接被重置")

// authRejectionError keeps callback/page errors without inventing an HTTP status.
type authRejectionError struct {
	Code, Message, Stage string
}

func (e *authRejectionError) Error() string {
	return "OAuth 登录失败: " + normalizeAuthCode(e.Code) + " " + sanitizeLoginText(e.Message)
}

type authHTTPStatusError struct {
	Status       int
	StatusText   string
	Path         string
	Stage        string
	UpstreamCode string
	Message      string
	Cause        error
	RetryAfter   time.Duration
	// RetryAfterConsumed distinguishes an explicit Retry-After that was
	// consumed while waiting on a browser challenge from an absent header.
	// A consumed delay permits the next retry immediately.
	RetryAfterConsumed bool
}

func (e *authHTTPStatusError) Unwrap() error                     { return e.Cause }
func (e *authHTTPStatusError) RetryAfterDuration() time.Duration { return e.RetryAfter }

// AuthHTTPStatus exposes an upstream auth status for the HTTP wrapper.
func AuthHTTPStatus(err error) (int, bool) {
	var statusErr *authHTTPStatusError
	if !errors.As(err, &statusErr) {
		return 0, false
	}
	return statusErr.Status, true
}

func (e *authHTTPStatusError) Error() string {
	status := fmt.Sprintf("HTTP %d", e.Status)
	if text := strings.TrimSpace(e.StatusText); text != "" {
		status += " " + text
	}
	detail := strings.TrimSpace(strings.Join([]string{sanitizeLoginText(e.Path), sanitizeLoginText(e.Message)}, ": "))
	if detail == "" {
		return "OpenAI 登录请求返回 " + status
	}
	return "OpenAI 登录请求返回 " + status + ": " + detail
}

func (e *authHTTPStatusError) retryable() bool {
	if accountStatusForAuthError(e.UpstreamCode, e.Message) != "" {
		return false
	}
	if errors.Is(e, ErrCloudflareChallenge) {
		return true
	}
	return e.Status == http.StatusRequestTimeout || e.Status == http.StatusTooManyRequests || e.Status >= http.StatusInternalServerError
}

const (
	oauthClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	oauthAuthorize   = "https://auth.openai.com/oauth/authorize"
	oauthToken       = "https://auth.openai.com/oauth/token"
	oauthRedirectURI = "http://localhost:1455/auth/callback"
	localClashProxy  = "http://127.0.0.1:7897"
	compatUserAgent  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	compatBrowserArg = "--disable-blink-features=AutomationControlled"
)

// Config holds the configuration for the login service
type Config struct {
	Headless      bool
	Proxy         string
	UpstreamProxy string
	RetryCount    int
	// RetryCountSet makes zero an explicit choice. When false, a zero value
	// keeps the service default for callers that construct Config literals.
	RetryCountSet bool
	Timeout       time.Duration
	TotalTimeout  time.Duration
	// BrowserCompatibility matches the known-good Mac helper profile for the
	// Playwright-launched path. Native system Chrome keeps its real UA/client
	// hints consistent and does not apply this override.
	BrowserCompatibility bool
}

// Service handles OpenAI login operations
type Service struct {
	config Config
}

type loginRetryBudget struct{ remaining int }
type loginRetryBudgetKey struct{}

// LoginResult contains the result of a successful login
type LoginResult struct {
	AccountID        int64 `json:"account_id,omitempty"`
	Delivery         any   `json:"delivery,omitempty"`
	AccessToken      string
	RefreshToken     string
	ChatGPTAccountID string
	OrganizationID   string
	PlanType         string
	Email            string
	ExpiresAt        int64
	ExpiresIn        int
}

// NewService creates a new login service
func NewService(config Config) *Service {
	if config.Timeout == 0 {
		config.Timeout = 60 * time.Second
	}
	if config.TotalTimeout <= 0 {
		config.TotalTimeout = 3 * time.Minute
	}
	if !config.RetryCountSet && config.RetryCount == 0 {
		config.RetryCount = 2
	}
	if config.RetryCount < 0 {
		config.RetryCount = 0
	}
	config.RetryCountSet = true
	return &Service{config: config}
}

func browserLaunchOptions(config Config) playwright.BrowserTypeLaunchOptions {
	options := playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(config.Headless),
		Channel:  playwright.String("chrome"),
		Timeout:  playwright.Float(float64(config.Timeout.Milliseconds())),
		Args:     []string{"--disable-dev-shm-usage", "--no-sandbox", "--disable-setuid-sandbox"},
	}
	if strings.TrimSpace(config.Proxy) == "" {
		options.Args = append(options.Args, "--no-proxy-server")
	}
	// Keep headed Playwright sessions close to the regular Chrome path. The
	// default webdriver flag is a stronger fingerprint difference than the
	// proxy itself and is unnecessary for this login flow.
	if !config.Headless {
		options.Args = append(options.Args, compatBrowserArg)
	}
	return options
}

func browserContextOptions(config Config) playwright.BrowserNewContextOptions {
	options := playwright.BrowserNewContextOptions{
		Viewport: &playwright.Size{
			Width:  1920,
			Height: 1080,
		},
		HasTouch:          playwright.Bool(false),
		IsMobile:          playwright.Bool(false),
		JavaScriptEnabled: playwright.Bool(true),
	}
	if config.BrowserCompatibility {
		options.UserAgent = playwright.String(compatUserAgent)
	}
	return options
}

// Login performs the complete login flow
func (s *Service) Login(email, password, totpSecret string) (*LoginResult, error) {
	return s.LoginWithProxiesContext(context.Background(), email, password, totpSecret, "", "")
}

func (s *Service) login(ctx context.Context, email, password, totpSecret string) (*LoginResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.config.UpstreamProxy != "" {
		return s.loginThroughUpstream(ctx, email, password, totpSecret, s.config.UpstreamProxy)
	}
	if s.config.Proxy == "" {
		relay, stop, err := startDirectIPv4Relay(ctx)
		if err != nil {
			return nil, fmt.Errorf("启动 IPv4 直连失败: %w", err)
		}
		defer stop()
		config := s.config
		config.Proxy = relay
		log.Printf("   🌐 IPv4 直连：浏览器与 token 交换使用服务器本机出口")
		return NewService(config).loginWithRetries(ctx, email, password, totpSecret)
	}

	result, err := s.loginWithRetries(ctx, email, password, totpSecret)
	if ctx.Err() != nil || !shouldTryLocalClash(s.config.Proxy, err) || !loginRetryBudgetAvailable(ctx) || !localProxyAvailable(ctx) {
		return result, err
	}

	log.Printf("   🔌 目标代理直连被重置，自动尝试本机 Clash %s", localClashProxy)
	return s.loginThroughUpstream(ctx, email, password, totpSecret, localClashProxy)
}

func (s *Service) loginThroughUpstream(ctx context.Context, email, password, totpSecret, upstream string) (*LoginResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	relay, closeRelay, err := startProxyRelay(s.config.Proxy, upstream)
	if err != nil {
		return nil, err
	}
	defer closeRelay()
	config := s.config
	config.Proxy = relay
	config.UpstreamProxy = ""
	log.Printf("   🔌 已启用前置代理，浏览器与 token 交换使用同一出口代理")
	return NewService(config).loginWithRetries(ctx, email, password, totpSecret)
}

func (s *Service) loginWithRetries(ctx context.Context, email, password, totpSecret string) (*LoginResult, error) {
	return loginWithRetries(ctx, s.config.RetryCount, func(ctx context.Context) (*LoginResult, error) {
		return s.loginAttempt(ctx, email, password, totpSecret)
	})
}

func loginWithRetries(ctx context.Context, retryCount int, attemptFunc func(context.Context) (*LoginResult, error)) (*LoginResult, error) {
	if retryCount < 0 {
		retryCount = 0
	}
	maxAttempts := retryCount
	if maxAttempts < int(^uint(0)>>1) {
		maxAttempts++
	}
	if budget, ok := ctx.Value(loginRetryBudgetKey{}).(*loginRetryBudget); ok {
		if budget.remaining < maxAttempts {
			maxAttempts = budget.remaining
		}
	}
	if maxAttempts < 1 {
		return nil, context.DeadlineExceeded
	}
	effectiveRetryCount := maxAttempts - 1
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if attempt > 0 {
			log.Printf("   🔄 重试 %d/%d", attempt, effectiveRetryCount)
			var retryAfter time.Duration
			retryAfterConsumed := false
			var statusErr *authHTTPStatusError
			if errors.As(lastErr, &statusErr) {
				retryAfter = statusErr.RetryAfter
				retryAfterConsumed = statusErr.RetryAfterConsumed
			}
			delay := retryDelay(attempt, retryAfter)
			if retryAfterConsumed {
				delay = 0
			}
			if retryDelayExceedsDeadline(ctx, delay) {
				return nil, errors.Join(context.DeadlineExceeded, lastErr)
			}
			message := fmt.Sprintf("正在等待第 %d/%d 次重试", attempt, effectiveRetryCount)
			if errors.Is(lastErr, ErrCloudflareChallenge) {
				message = fmt.Sprintf("浏览器验证未通过，正在等待第 %d/%d 次自动重试", attempt, effectiveRetryCount)
			}
			emitProgress(ctx, "retry_wait", message)
			if err := waitContext(ctx, delay); err != nil {
				return nil, errors.Join(err, lastErr)
			}
		}

		if budget, ok := ctx.Value(loginRetryBudgetKey{}).(*loginRetryBudget); ok {
			budget.remaining--
		}
		result, err := attemptFunc(ctx)
		if ctxErr := ctx.Err(); ctxErr != nil {
			if err != nil {
				return nil, errors.Join(ctxErr, err)
			}
			return nil, ctxErr
		}
		if err == nil {
			return result, nil
		}

		lastErr = err
		failure := DescribeError(err)
		log.Printf("   ⚠️  登录失败: stage=%s code=%s http_status=%d message=%s", failure.Stage, failure.Code, failure.HTTPStatus, failure.Message)
		if failure.AccountStatus != "" || !retryableLoginError(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("登录失败（已重试 %d 次）: %w", effectiveRetryCount, lastErr)
}

func loginRetryBudgetAvailable(ctx context.Context) bool {
	budget, ok := ctx.Value(loginRetryBudgetKey{}).(*loginRetryBudget)
	return !ok || budget.remaining > 0
}

// retryableLoginError is deliberately allowlisted. A browser flow can return
// many deterministic errors (invalid credentials, OAuth rejection, malformed
// tokens, and page/configuration errors); replaying those only creates more
// upstream traffic and obscures the useful failure. Temporary HTTP failures,
// Cloudflare challenges, and transient transport errors are safe to retry.
func retryableLoginError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var statusErr *authHTTPStatusError
	if errors.As(err, &statusErr) {
		return statusErr.retryable()
	}
	if errors.Is(err, ErrCloudflareChallenge) {
		return true
	}
	if errors.Is(err, playwright.ErrTimeout) || errors.Is(err, playwright.ErrTargetClosed) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) && (networkErr.Timeout() || networkErr.Temporary()) {
		return true
	}
	return false
}

const (
	retryBaseDelay          = 2 * time.Second
	retryMaxDelay           = 30 * time.Second
	challengeProbeTimeoutMS = 500
)

// retryDelay returns a delay for a retry. A valid Retry-After header takes
// precedence and is preserved; otherwise a bounded local backoff with a small
// random jitter prevents concurrent logins from retrying in lockstep.
func retryDelay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	if attempt < 1 {
		attempt = 1
	}
	delay := retryBaseDelay
	for i := 1; i < attempt && delay < retryMaxDelay; i++ {
		delay *= 2
	}
	if delay > retryMaxDelay {
		delay = retryMaxDelay
	}
	// Add up to 25% jitter while keeping the result within the same bounded
	// backoff range. crypto/rand is already used by the login flow.
	jitter := delay / 4
	if jitter <= 0 {
		return delay
	}
	span := big.NewInt(int64(jitter*2 + 1))
	offset, err := rand.Int(rand.Reader, span)
	if err != nil {
		return delay
	}
	result := delay - jitter + time.Duration(offset.Int64())
	if result > retryMaxDelay {
		return retryMaxDelay
	}
	return result
}

func retryDelayExceedsDeadline(ctx context.Context, delay time.Duration) bool {
	deadline, ok := ctx.Deadline()
	return ok && delay >= time.Until(deadline)
}

// parseRetryAfter parses the HTTP Retry-After syntax (seconds or HTTP-date).
// Invalid, negative, or already elapsed values are treated as absent.
func parseRetryAfter(raw string, now time.Time) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		const maxDuration = time.Duration(1<<63 - 1)
		if seconds >= int64(maxDuration/time.Second) {
			return maxDuration
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(raw)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

func localProxyAvailable(ctx context.Context) bool {
	dialer := net.Dialer{Timeout: 500 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "tcp", "127.0.0.1:7897")
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func shouldTryLocalClash(proxy string, err error) bool {
	if !errors.Is(err, ErrAuthConnectionReset) {
		return false
	}
	u, parseErr := parseHTTPProxy(proxy)
	if parseErr != nil || u == nil {
		return false
	}
	host := u.Hostname()
	return !strings.EqualFold(host, "localhost") && !net.ParseIP(host).IsLoopback()
}

// loginAttempt performs a single login attempt
func (s *Service) loginAttempt(ctx context.Context, email, password, totpSecret string) (*LoginResult, error) {
	// 初始化 Playwright。无代理的有头模式连接到临时配置目录中的系统
	// Chrome；代理/headless 仍使用 Playwright 启动路径。
	emitProgress(ctx, "browser", "正在启动浏览器")
	log.Printf("   🌐 启动浏览器...")
	var (
		pw          *playwright.Playwright
		browser     playwright.Browser
		nativeClose func()
		err         error
	)
	if shouldUseNativeChrome(s.config) {
		native, nativeErr := startNativeChrome(ctx, s.config)
		if nativeErr != nil {
			return nil, nativeErr
		}
		pw, browser, nativeClose = native.pw, native.browser, native.close
		log.Printf("   🌐 已连接系统 Chrome（临时配置目录，CDP localhost）")
	} else {
		pw, err = runPlaywright()
		if err != nil {
			return nil, fmt.Errorf("playwright 启动失败: %w", err)
		}
	}
	stopDriver := sync.OnceFunc(func() { _ = pw.Stop() })
	defer stopDriver()
	stopLaunchCancellation := context.AfterFunc(ctx, stopDriver)
	defer stopLaunchCancellation()

	if browser == nil {
		// 浏览器选项
		launchOptions := browserLaunchOptions(s.config)

		if s.config.Proxy != "" {
			proxyURL, err := parseHTTPProxy(s.config.Proxy)
			if err != nil {
				return nil, err
			}
			proxy := &playwright.Proxy{Server: "http://" + proxyURL.Host}
			if proxyURL.User != nil {
				username := proxyURL.User.Username()
				password, hasPassword := proxyURL.User.Password()
				proxy.Username = playwright.String(username)
				if hasPassword {
					proxy.Password = playwright.String(password)
				}
			}
			launchOptions.Proxy = proxy
		}

		browser, err = pw.Chromium.Launch(launchOptions)
		if err != nil {
			return nil, fmt.Errorf("浏览器启动失败: %w", err)
		}
	}
	if !stopLaunchCancellation() {
		// Wait for cancellation's driver shutdown before using its result.
		stopDriver()
	}
	closeBrowser := sync.OnceFunc(func() {
		_ = browser.Close()
		if nativeClose != nil {
			nativeClose()
		}
	})
	cancelDone := make(chan struct{})
	stopCancellation := context.AfterFunc(ctx, func() {
		defer close(cancelDone)
		closeBrowser()
	})
	defer func() {
		if !stopCancellation() {
			<-cancelDone
		}
		closeBrowser()
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// CDP exposes Chrome's already-created default context. It is isolated by
	// the temporary user-data-dir; NewContext is retained for Playwright's
	// launched browser path where the context options are applied.
	var browserContext playwright.BrowserContext
	if nativeClose != nil {
		contexts := browser.Contexts()
		if len(contexts) == 0 {
			return nil, errors.New("系统 Chrome 未提供默认浏览器上下文")
		}
		browserContext = contexts[0]
	} else {
		contextOptions := browserContextOptions(s.config)
		browserContext, err = browser.NewContext(contextOptions)
		if err != nil {
			return nil, fmt.Errorf("创建浏览器上下文失败: %w", err)
		}
	}

	var page playwright.Page
	if nativeClose != nil {
		// Reuse Chrome's initial tab. Creating a second CDP page makes the
		// OAuth navigation look like a fresh automation tab to Cloudflare.
		pages := browserContext.Pages()
		if len(pages) > 0 {
			page = pages[0]
		}
	}
	if page == nil {
		page, err = browserContext.NewPage()
		if err != nil {
			return nil, fmt.Errorf("创建页面失败: %w", err)
		}
	}

	// Let Chrome/Playwright emit its own Accept-Language header. Overriding it
	// here can disagree with navigator.language (for example, a zh-CN system
	// locale), which creates an unnecessary browser fingerprint mismatch.

	// 执行登录流程
	emitProgress(ctx, "authorize", "正在访问 OpenAI 授权页面")
	log.Printf("   🔑 开始登录流程...")

	state, err := randomString(32)
	if err != nil {
		return nil, fmt.Errorf("生成 OAuth state 失败: %w", err)
	}
	codeVerifier, err := randomString(64)
	if err != nil {
		return nil, fmt.Errorf("生成 PKCE verifier 失败: %w", err)
	}
	challenge := sha256.Sum256([]byte(codeVerifier))
	params := url.Values{
		"response_type":              {"code"},
		"client_id":                  {oauthClientID},
		"redirect_uri":               {oauthRedirectURI},
		"scope":                      {"openid profile email offline_access"},
		"state":                      {state},
		"code_challenge":             {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method":      {"S256"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
	}
	authorizeURL := oauthAuthorize + "?" + params.Encode()
	callbackURLCh := make(chan string, 1)
	cloudflareChallengeCh := make(chan struct{}, 1)
	authStatusCh := make(chan error, 4)
	page.OnRequest(func(request playwright.Request) {
		requestURL := request.URL()
		if request.Method() != http.MethodGet || request.IsNavigationRequest() {
			if requestPath := authDiagnosticPath(requestURL); requestPath != "" {
				log.Printf("   🌐 OpenAI 请求: %s %s", request.Method(), requestPath)
			}
		}
		if strings.HasPrefix(requestURL, oauthRedirectURI+"?") || requestURL == oauthRedirectURI {
			select {
			case callbackURLCh <- requestURL:
			default:
			}
		}
	})
	observeAuthResponses(page, cloudflareChallengeCh, authStatusCh)
	page.OnRequestFailed(func(request playwright.Request) {
		if requestPath := authDiagnosticPath(request.URL()); requestPath != "" {
			if err := request.Failure(); err != nil {
				log.Printf("   ⚠️  OpenAI 请求失败: %s %s: %s", request.Method(), sanitizeLoginText(requestPath), DescribeError(err).Message)
			}
		}
	})

	// Initial navigation may need time to complete a browser challenge. No
	// authentication submission is replayed while waiting.
	if err := navigateAuthorize(ctx, page, authorizeURL, cloudflareChallengeCh, authStatusCh); err != nil {
		return nil, err
	}

	if err := randomDelay(ctx, 100, 250); err != nil {
		return nil, err
	}

	// 2. 输入邮箱
	emitProgress(ctx, "email", "正在输入邮箱")
	log.Printf("   📧 输入邮箱...")
	if err := waitForAuthSelector(ctx, page, "input[name='username'], input[type='email']", 20*time.Second, cloudflareChallengeCh, authStatusCh); err != nil {
		return nil, fmt.Errorf("邮箱输入框未找到: %w", err)
	}

	if err := page.Fill("input[name='username'], input[type='email']", email); err != nil {
		return nil, fmt.Errorf("填写邮箱失败: %w", err)
	}

	if err := randomDelay(ctx, 100, 250); err != nil {
		return nil, err
	}

	if err := page.Click("button[type='submit'], button:has-text('Continue'), button:has-text('继续'), button:has-text('下一步')"); err != nil {
		return nil, fmt.Errorf("点击继续按钮失败: %w", err)
	}

	// 3. 输入密码
	emitProgress(ctx, "password", "正在输入密码")
	log.Printf("   🔐 输入密码...")
	if err := waitForAuthSelector(ctx, page, "input[name='password'], input[type='password']", s.config.Timeout, cloudflareChallengeCh, authStatusCh); err != nil {
		if errors.Is(err, ErrUnexpectedAuthPage) {
			return nil, fmt.Errorf("%w（%s）", err, authPageSummary(page, email))
		}
		return nil, err
	}

	if err := page.Fill("input[name='password'], input[type='password']", password); err != nil {
		return nil, fmt.Errorf("填写密码失败: %w", err)
	}

	if err := randomDelay(ctx, 100, 250); err != nil {
		return nil, err
	}

	if err := page.Click("button[type='submit'], button:has-text('Continue'), button:has-text('继续'), button:has-text('Log in'), button:has-text('登录')"); err != nil {
		return nil, fmt.Errorf("点击登录按钮失败: %w", err)
	}

	// 4. 处理可能延迟出现的 2FA，并捕获 OAuth 回调。
	emitProgress(ctx, "oauth", "正在等待登录确认")
	log.Printf("   🎯 等待 OAuth 回调...")
	callbackURL, err := waitOAuthCallbackContext(ctx, page, callbackURLCh, cloudflareChallengeCh, s.config.Timeout, email, totpSecret, authStatusCh)
	if err != nil {
		return nil, err
	}

	code, err := oauthCallbackCode(callbackURL, state)
	if err != nil {
		return nil, err
	}

	emitProgress(ctx, "token", "正在交换登录令牌")
	tokens, err := s.exchangeCode(ctx, code, codeVerifier)
	if err != nil {
		return nil, err
	}
	if tokens.AccessToken == "" {
		return nil, errors.New("OAuth token 响应缺少 access_token")
	}
	if tokens.RefreshToken == "" {
		return nil, errors.New("OAuth token 响应缺少 refresh_token")
	}

	identity, err := parseOAuthIdentity(tokens.AccessToken, tokens.IDToken)
	if err != nil {
		return nil, fmt.Errorf("解析 OAuth 身份信息失败: %w", err)
	}
	if identity.ExpiresAt <= 0 && tokens.ExpiresIn > 0 {
		identity.ExpiresAt = time.Now().Unix() + tokens.ExpiresIn
	}
	expiresIn := int(tokens.ExpiresIn)
	if expiresIn == 0 && identity.ExpiresAt > time.Now().Unix() {
		expiresIn = int(identity.ExpiresAt - time.Now().Unix())
	}

	log.Printf("   ✅ OAuth token 交换成功")
	return &LoginResult{
		AccessToken:      tokens.AccessToken,
		RefreshToken:     tokens.RefreshToken,
		ChatGPTAccountID: identity.ChatGPTAccountID,
		OrganizationID:   identity.OrganizationID,
		PlanType:         identity.PlanType,
		Email:            identity.Email,
		ExpiresAt:        identity.ExpiresAt,
		ExpiresIn:        expiresIn,
	}, nil
}

func waitOAuthCallback(page playwright.Page, callbacks <-chan string, challenges <-chan struct{}, timeout time.Duration, email, totpSecret string, authStatuses ...<-chan error) (string, error) {
	return waitOAuthCallbackContext(context.Background(), page, callbacks, challenges, timeout, email, totpSecret, authStatuses...)
}

func waitOAuthCallbackContext(ctx context.Context, page playwright.Page, callbacks <-chan string, challenges <-chan struct{}, timeout time.Duration, email, totpSecret string, authStatuses ...<-chan error) (string, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	var authStatusCh <-chan error
	if len(authStatuses) > 0 {
		authStatusCh = authStatuses[0]
	}
	mfaSubmitted := false
	consentSubmitted := false
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := takeAuthFailure(nil, authStatusCh); err != nil {
			if !errors.Is(err, ErrCloudflareChallenge) {
				return "", authFailureWithPage(page, err)
			}
			if recoveryErr := waitChallengeRecovery(ctx, page, challenges, authStatusCh, err); recoveryErr != nil {
				return "", recoveryErr
			}
		}
		if takeSignal(challenges) {
			if recoveryErr := waitChallengeRecovery(ctx, page, challenges, authStatusCh, cloudflareError()); recoveryErr != nil {
				return "", recoveryErr
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case callback := <-callbacks:
			return callback, nil
		case <-challenges:
			if err := waitChallengeRecovery(ctx, page, challenges, authStatusCh, cloudflareError()); err != nil {
				return "", err
			}
		case err := <-authStatusCh:
			if err != nil {
				if errors.Is(err, ErrCloudflareChallenge) {
					if recoveryErr := waitChallengeRecovery(ctx, page, challenges, authStatusCh, err); recoveryErr != nil {
						return "", recoveryErr
					}
					continue
				}
				return "", authFailureWithPage(page, err)
			}
		case <-deadline.C:
			return "", fmt.Errorf("%w：未收到 OAuth 回调（%s）", ErrUnexpectedAuthPage, authPageSummary(page, email))
		case <-tick.C:
			u, err := url.Parse(page.URL())
			if err != nil || u.Scheme != "https" || u.Host != "auth.openai.com" {
				continue
			}
			if rejection := accountRejectionOnPage(page); rejection != nil {
				return "", rejection
			}
			if strings.HasPrefix(u.Path, "/mfa-challenge/") && !mfaSubmitted {
				codeInput := page.Locator("input[name='code'], input[autocomplete='one-time-code']").First()
				if count, countErr := codeInput.Count(); countErr != nil || count == 0 {
					continue
				}
				code, codeErr := GenerateTOTP(totpSecret)
				if codeErr != nil {
					return "", fmt.Errorf("生成 TOTP 失败: %w", codeErr)
				}
				emitProgress(ctx, "mfa", "正在输入二次验证码")
				log.Printf("   🔢 检测到二次验证，输入 TOTP...")

				if fillErr := codeInput.Fill(code); fillErr != nil {
					return "", fmt.Errorf("填写 TOTP 失败: %w", fillErr)
				}

				button, count, buttonErr := findAuthActionButton(page)
				if buttonErr != nil {
					return "", fmt.Errorf("提交 TOTP 失败: %w", buttonErr)
				}
				if count == 0 {
					return "", errors.New("提交 TOTP 失败：未找到验证按钮")
				}
				if clickErr := button.Click(playwright.LocatorClickOptions{Timeout: playwright.Float(10000)}); clickErr != nil {
					return "", fmt.Errorf("提交 TOTP 失败: %w", clickErr)
				}
				mfaSubmitted = true
				continue
			}
			if u.Path != "/sign-in-with-chatgpt/codex/consent" || consentSubmitted {
				continue
			}
			emitProgress(ctx, "consent", "正在确认授权")
			log.Printf("   ✅ 确认 Codex 授权...")
			if err := clickCodexConsent(ctx, page, email); err != nil {
				return "", err
			}
			consentSubmitted = true
		}
	}
}

// waitChallengeRecovery gives the current browser a bounded chance to finish
// a verification. If it remains blocked, the outer login retry creates a fresh
// browser attempt instead of replaying credentials in the challenged page.
func waitChallengeRecovery(ctx context.Context, page playwright.Page, challenges <-chan struct{}, statuses <-chan error, terminal error) error {
	if terminal == nil {
		terminal = cloudflareError()
	}
	if page == nil {
		return terminal
	}
	if !isCloudflareChallenge(page) {
		if err := drainChallengeSignals(challenges, statuses); err != nil {
			return authFailureWithPage(page, err)
		}
		// A challenge signal can arrive after the browser has already
		// completed verification. Continue with the recovered page instead of
		// discarding it and replaying the whole login attempt.
		return nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	waitStarted := time.Now()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !isCloudflareChallenge(page) {
			if err := drainChallengeSignals(challenges, statuses); err != nil {
				return authFailureWithPage(page, err)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err, ok := <-statuses:
			if !ok {
				statuses = nil
				continue
			}
			if err != nil && !errors.Is(err, ErrCloudflareChallenge) {
				return authFailureWithPage(page, err)
			}
		case _, ok := <-challenges:
			if !ok {
				challenges = nil
			}
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return subtractElapsedRetryAfter(terminal, time.Since(waitStarted))
			}
			return waitCtx.Err()
		case <-ticker.C:
		}
	}
}

var authActionButtonNames = []string{"Continue", "继续", "Next", "下一步", "Submit", "提交", "Authorize", "授权", "Verify", "验证"}

// findAuthActionButton accepts localized accessible names and then the same
// known labels in visible text before falling back to native submit controls.
func findAuthActionButton(page playwright.Page) (playwright.Locator, int, error) {
	candidates := make([]playwright.Locator, 0, len(authActionButtonNames)+2)
	for _, name := range authActionButtonNames {
		candidate := page.GetByRole(*playwright.AriaRoleButton, playwright.PageGetByRoleOptions{
			Name:  name,
			Exact: playwright.Bool(true),
		})
		candidates = append(candidates, candidate.Filter(playwright.LocatorFilterOptions{Visible: playwright.Bool(true)}))
	}
	// Some consent pages render a type=button control and submit through
	// JavaScript. Restrict the text fallback to the same known action labels so
	// a localized page cannot make us click a cancel/back button.
	candidates = append(candidates, page.Locator("button:has-text('Continue'), button:has-text('继续'), button:has-text('Next'), button:has-text('下一步'), button:has-text('Submit'), button:has-text('提交'), button:has-text('Authorize'), button:has-text('授权'), button:has-text('Verify'), button:has-text('验证')").Filter(playwright.LocatorFilterOptions{Visible: playwright.Bool(true)}).First())
	candidates = append(candidates, page.Locator("button[type='submit']:visible, input[type='submit']:visible").First())
	for _, candidate := range candidates {
		count, err := candidate.Count()
		if err != nil {
			return nil, 0, err
		}
		if count > 0 {
			return candidate.First(), count, nil
		}
	}
	return candidates[0], 0, nil
}

func clickCodexConsent(ctx context.Context, page playwright.Page, email string) error {
	for attempt := 0; attempt < 3; attempt++ {
		button, count, err := findAuthActionButton(page)
		if err != nil {
			return fmt.Errorf("%w：确认 Codex 授权失败（%s）: %v", ErrUnexpectedAuthPage, authPageSummary(page, email), err)
		}
		if count > 0 {
			if err := button.Click(playwright.LocatorClickOptions{Timeout: playwright.Float(10000)}); err == nil {
				return nil
			}
		}
		body, _ := page.Locator("body").InnerText()
		if !strings.Contains(body, "Route Error (403") && !hasCloudflareChallengeSignals(page.URL(), "", body) {
			break
		}
		if attempt == 2 {
			break
		}
		if err := waitContext(ctx, time.Duration(attempt+1)*time.Second); err != nil {
			return err
		}
		if _, err := page.Reload(playwright.PageReloadOptions{
			WaitUntil: playwright.WaitUntilStateDomcontentloaded,
			Timeout:   playwright.Float(30000),
		}); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return fmt.Errorf("%w：确认 Codex 授权失败（%s）", ErrUnexpectedAuthPage, authPageSummary(page, email))
}

// LoginWithProxy runs one login with a request-specific HTTP proxy.
// An empty proxy keeps the service-level proxy; "direct" explicitly clears it.
func (s *Service) LoginWithProxy(email, password, totpSecret, proxy string) (*LoginResult, error) {
	return s.LoginWithProxies(email, password, totpSecret, proxy, "")
}

// LoginWithProxies optionally overrides the exit and upstream proxies for one login.
func (s *Service) LoginWithProxies(email, password, totpSecret, proxy, upstream string) (*LoginResult, error) {
	return s.LoginWithProxiesContext(context.Background(), email, password, totpSecret, proxy, upstream)
}

// LoginWithProxiesContext applies one deadline across browser attempts, proxy fallback,
// and token exchange. Cancellation waits for browser cleanup before returning.
func (s *Service) LoginWithProxiesContext(ctx context.Context, email, password, totpSecret, proxy, upstream string) (*LoginResult, error) {
	config, err := s.loginConfig(proxy, upstream)
	if err != nil {
		return nil, err
	}
	service := NewService(config)
	ctx, cancel := context.WithTimeout(ctx, service.config.TotalTimeout)
	defer cancel()
	attemptBudget := service.config.RetryCount
	if attemptBudget < int(^uint(0)>>1) {
		attemptBudget++
	}
	if attemptBudget < 1 {
		attemptBudget = 1
	}
	ctx = context.WithValue(ctx, loginRetryBudgetKey{}, &loginRetryBudget{remaining: attemptBudget})
	ctx = startProgress(ctx)
	defer finishProgress(ctx)
	result, err := service.login(ctx, email, password, totpSecret)
	if ctxErr := ctx.Err(); ctxErr != nil {
		if err != nil {
			return nil, errors.Join(ctxErr, err)
		}
		return nil, ctxErr
	}
	if err == nil {
		emitProgress(ctx, "complete", "登录完成")
	}
	return result, err
}

func (s *Service) loginConfig(proxy, upstream string) (Config, error) {
	config := s.config
	config.Proxy = strings.TrimSpace(config.Proxy)
	config.UpstreamProxy = strings.TrimSpace(config.UpstreamProxy)
	proxy = strings.TrimSpace(proxy)
	upstream = strings.TrimSpace(upstream)
	if proxy != "" {
		config.Proxy = proxy
	}
	if upstream != "" {
		config.UpstreamProxy = upstream
	}
	if strings.EqualFold(strings.TrimSpace(config.Proxy), "direct") {
		if upstream != "" {
			return Config{}, errors.New("IPv4 直连不能同时填写前置代理")
		}
		config.Proxy, config.UpstreamProxy = "", ""
	}
	if err := ValidateHTTPProxy(config.Proxy); err != nil {
		return Config{}, err
	}
	if err := ValidateHTTPProxy(config.UpstreamProxy); err != nil {
		return Config{}, fmt.Errorf("前置代理无效: %w", err)
	}
	if config.UpstreamProxy != "" && config.Proxy == "" {
		return Config{}, errors.New("填写前置代理时还需要填写 HTTP 出口代理")
	}
	return config, nil
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type oauthIdentity struct {
	ChatGPTAccountID string
	OrganizationID   string
	PlanType         string
	Email            string
	ExpiresAt        int64
}

func (s *Service) exchangeCode(ctx context.Context, code, verifier string) (*oauthTokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {oauthClientID},
		"code":          {code},
		"redirect_uri":  {oauthRedirectURI},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthToken, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("创建 OAuth token 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: s.config.Timeout}
	if s.config.Proxy != "" {
		proxyURL, err := parseHTTPProxy(s.config.Proxy)
		if err != nil {
			return nil, err
		}
		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		defer transport.CloseIdleConnections()
		client.Transport = transport
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 OAuth token 失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("读取 OAuth token 响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code, message := authResponseDetails(body)
		var cause error
		if strings.EqualFold(resp.Header.Get("cf-mitigated"), "challenge") {
			cause = ErrCloudflareChallenge
			message = "令牌交换需要浏览器验证"
		}
		return nil, &authHTTPStatusError{
			Cause:        cause,
			Status:       resp.StatusCode,
			StatusText:   resp.Status,
			Path:         "auth.openai.com/oauth/token",
			Stage:        LoginStageToken,
			UpstreamCode: code,
			Message:      message,
			RetryAfter:   parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
	}
	var tokens oauthTokenResponse
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, fmt.Errorf("解析 OAuth token 响应失败: %w", err)
	}
	return &tokens, nil
}

// navigateAuthorize performs the only navigation that may be allowed to
// recover from a Cloudflare browser challenge. Authentication form
// submissions are never replayed here.
func navigateAuthorize(ctx context.Context, page playwright.Page, authorizeURL string, challenges <-chan struct{}, statuses <-chan error) error {
	response, gotoErr := page.Goto(authorizeURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	})

	var pending error
	select {
	case pending = <-statuses:
	default:
	}
	if pending != nil && !errors.Is(pending, ErrCloudflareChallenge) {
		return authFailureWithPage(page, pending)
	}

	challenge := errors.Is(pending, ErrCloudflareChallenge)
	if response != nil {
		header, _ := response.HeaderValue("cf-mitigated")
		challenge = challenge || strings.EqualFold(strings.TrimSpace(header), "challenge")
	}
	challenge = challenge || isCloudflareChallenge(page)
	if challenge {
		// The response callback may have queued the initial challenge signal
		// after Goto returned. Consume that signal before entering the recovery
		// window so it cannot be mistaken for a later auth submission.
		if err := drainChallengeSignals(challenges, statuses); err != nil {
			return authFailureWithPage(page, err)
		}
		var challengeErr *authHTTPStatusError
		if errors.As(pending, &challengeErr) {
			return waitInitialChallenge(ctx, page, challenges, statuses, challengeErr)
		}
		if response != nil {
			header, _ := response.HeaderValue("cf-mitigated")
			if strings.EqualFold(strings.TrimSpace(header), "challenge") || response.Status() >= http.StatusBadRequest {
				retryAfter, _ := response.HeaderValue("retry-after")
				path := authDiagnosticPath(response.URL())
				return waitInitialChallenge(ctx, page, challenges, statuses, &authHTTPStatusError{
					Status:     response.Status(),
					StatusText: response.StatusText(),
					Path:       path,
					Stage:      LoginStageChallenge,
					Message:    "Cloudflare challenge：浏览器验证未通过，当前尝试结束并自动重试",
					Cause:      ErrCloudflareChallenge,
					RetryAfter: parseRetryAfter(retryAfter, time.Now()),
				})
			}
		}
		return waitInitialChallenge(ctx, page, challenges, statuses, cloudflareError())
	}

	if gotoErr != nil {
		if strings.Contains(gotoErr.Error(), "net::ERR_CONNECTION_RESET") {
			return ErrAuthConnectionReset
		}
		return fmt.Errorf("访问登录页失败（请检查 HTTP 代理连通性）: %w", gotoErr)
	}
	if response != nil && response.Status() >= 400 {
		retryAfter, _ := response.HeaderValue("retry-after")
		path := authDiagnosticPath(response.URL())
		return &authHTTPStatusError{
			Status:     response.Status(),
			StatusText: response.StatusText(),
			Path:       path,
			Stage:      authStageForPath(path),
			RetryAfter: parseRetryAfter(retryAfter, time.Now()),
		}
	}
	if err := drainChallengeSignals(challenges, statuses); err != nil {
		return authFailureWithPage(page, err)
	}
	return nil
}

func waitInitialChallenge(ctx context.Context, page playwright.Page, challenges <-chan struct{}, statuses <-chan error, terminal error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	waitStarted := time.Now()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := waitCtx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				if parentErr := ctx.Err(); parentErr != nil {
					return parentErr
				}
				return subtractElapsedRetryAfter(terminal, time.Since(waitStarted))
			}
			return err
		}
		if rejection := accountRejectionOnPage(page); rejection != nil {
			return rejection
		}
		if isUnsupportedRegion(page) {
			return ErrUnsupportedRegion
		}
		if !isCloudflareChallenge(page) {
			visible, err := page.Locator("input[name='username'], input[type='email']").First().IsVisible()
			if err == nil && visible {
				if err := drainChallengeSignals(challenges, statuses); err != nil {
					return authFailureWithPage(page, err)
				}
				return nil
			}
		}
		select {
		case err, ok := <-statuses:
			if !ok {
				statuses = nil
				continue
			}
			if errors.Is(err, ErrCloudflareChallenge) {
				continue
			}
			if err != nil {
				return authFailureWithPage(page, err)
			}
		case _, ok := <-challenges:
			if !ok {
				challenges = nil
				continue
			}
			// A delayed challenge response can arrive after the page has already
			// completed verification. Let the next iteration observe the recovered
			// form instead of turning that late signal into a false failure.
		case <-ticker.C:
		}
	}
}

// subtractElapsedRetryAfter avoids waiting twice for a server-provided delay:
// the browser challenge window already consumed part of Retry-After.
func subtractElapsedRetryAfter(err error, elapsed time.Duration) error {
	if elapsed <= 0 {
		return err
	}
	var statusErr *authHTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.RetryAfter <= 0 {
		return err
	}
	adjusted := *statusErr
	adjusted.RetryAfter -= elapsed
	if adjusted.RetryAfter < 0 {
		adjusted.RetryAfter = 0
	}
	adjusted.RetryAfterConsumed = adjusted.RetryAfter == 0
	return &adjusted
}

func drainChallengeSignals(challenges <-chan struct{}, statuses <-chan error) error {
	for challenges != nil || statuses != nil {
		drained := false
		if challenges != nil {
			select {
			case _, ok := <-challenges:
				drained = true
				if !ok {
					challenges = nil
				}
			default:
			}
		}
		if statuses != nil {
			select {
			case err, ok := <-statuses:
				drained = true
				if !ok {
					statuses = nil
					continue
				}
				if err != nil && !errors.Is(err, ErrCloudflareChallenge) {
					return err
				}
			default:
			}
		}
		if !drained {
			return nil
		}
	}
	return nil
}

func parseHTTPProxy(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	proxyURL, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("代理地址无效")
	}
	if proxyURL.Scheme != "http" {
		return nil, errors.New("代理地址必须使用 http:// 格式")
	}
	if proxyURL.Hostname() == "" {
		return nil, errors.New("代理地址缺少主机名")
	}
	if proxyURL.Path != "" && proxyURL.Path != "/" || proxyURL.RawQuery != "" || proxyURL.Fragment != "" {
		return nil, errors.New("代理地址不能包含路径、查询参数或片段")
	}
	return proxyURL, nil
}

// ValidateHTTPProxy validates an optional HTTP proxy URL before login starts.
func ValidateHTTPProxy(raw string) error {
	_, err := parseHTTPProxy(raw)
	return err
}

// ValidateLoginProxy additionally accepts the explicit IPv4 direct selector.
// Upstream proxies must continue to use ValidateHTTPProxy.
func ValidateLoginProxy(raw string) error {
	if strings.EqualFold(strings.TrimSpace(raw), "direct") {
		return nil
	}
	return ValidateHTTPProxy(raw)
}

func parseOAuthIdentity(accessToken, idToken string) (*oauthIdentity, error) {
	identity := &oauthIdentity{}
	var idTokenExpiresAt int64
	if idToken != "" {
		claims, err := decodeOAuthClaims(idToken)
		if err != nil {
			return nil, err
		}
		identity.Email = stringClaim(claims, "email")
		idTokenExpiresAt = intClaim(claims, "exp")
		if auth, ok := claims["https://api.openai.com/auth"].(map[string]interface{}); ok {
			identity.ChatGPTAccountID = stringClaim(auth, "chatgpt_account_id")
			identity.PlanType = stringClaim(auth, "chatgpt_plan_type")
			if orgs, ok := auth["organizations"].([]interface{}); ok {
				for _, item := range orgs {
					org, ok := item.(map[string]interface{})
					if !ok {
						continue
					}
					if identity.OrganizationID == "" || boolClaim(org, "is_default") {
						identity.OrganizationID = stringClaim(org, "id")
					}
				}
			}
		}
	}
	if access, err := ParseJWT(accessToken); err == nil {
		if identity.ChatGPTAccountID == "" {
			identity.ChatGPTAccountID = access.ChatGPTAccountID
		}
		if identity.OrganizationID == "" {
			identity.OrganizationID = access.OrganizationID
		}
		if identity.PlanType == "" {
			identity.PlanType = access.ChatGPTPlanType
		}
		if identity.Email == "" {
			identity.Email = access.Email
		}
		// The access token is the credential used by downstream API calls, so
		// its exp claim is the expiry shown in history. The ID token's exp is
		// only a fallback when the access token has no usable expiry claim.
		if access.ExpiresAt > 0 {
			identity.ExpiresAt = access.ExpiresAt
		}
	}
	if identity.ExpiresAt == 0 {
		identity.ExpiresAt = idTokenExpiresAt
	}
	if identity.Email == "" {
		return nil, errors.New("token 中缺少 email")
	}
	return identity, nil
}

func decodeOAuthClaims(token string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("JWT 格式错误")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("JWT payload 解码失败: %w", err)
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("JWT payload 解析失败: %w", err)
	}
	return claims, nil
}

func stringClaim(claims map[string]interface{}, key string) string {
	value, _ := claims[key].(string)
	return value
}

func intClaim(claims map[string]interface{}, key string) int64 {
	value, _ := claims[key].(float64)
	return int64(value)
}

func boolClaim(claims map[string]interface{}, key string) bool {
	value, _ := claims[key].(bool)
	return value
}

func randomString(size int) (string, error) {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func isCloudflareChallenge(page playwright.Page) bool {
	titleLocator := page.Locator("title")
	timeout := playwright.Float(challengeProbeTimeoutMS)
	title, _ := titleLocator.TextContent(playwright.LocatorTextContentOptions{Timeout: timeout})
	text, _ := page.Locator("body").InnerText(playwright.LocatorInnerTextOptions{Timeout: timeout})
	return hasCloudflareChallengeSignals(page.URL(), title, text)
}

func hasCloudflareChallengeSignals(pageURL, title, visibleText string) bool {
	if strings.Contains(pageURL, "__cf_chl") {
		return true
	}
	title = strings.ToLower(title)
	if !containsAny(title, []string{"just a moment", "cloudflare", "请稍候"}) {
		return false
	}
	return containsAny(strings.ToLower(visibleText), []string{
		"verify you are human",
		"verifying you are human",
		"checking your browser",
		"enable javascript and cookies to continue",
		"performing security verification",
		"验证您是真人",
		"验证你是真人",
		"正在验证您是否是真人",
	})
}

func observeAuthResponses(page playwright.Page, challenges chan<- struct{}, statuses chan<- error) {
	page.OnResponse(func(response playwright.Response) {
		header, _ := response.HeaderValue("cf-mitigated")
		isChallenge := strings.EqualFold(strings.TrimSpace(header), "challenge")
		request := response.Request()
		requestPath := authDiagnosticPath(response.URL())
		if requestPath != "" && (request.Method() != http.MethodGet || request.IsNavigationRequest() || response.Status() >= 400 || isChallenge) {
			contentType, _ := response.HeaderValue("content-type")
			log.Printf("   🌐 OpenAI 响应: %s HTTP %d %s content-type=%q cf-mitigated=%q",
				request.Method(), response.Status(), requestPath, contentType, header)
		}
		// Background resources can receive challenges while the login form still
		// works. Only authentication submissions and navigation stop this flow.
		if requestPath == "" || (request.Method() == http.MethodGet && !request.IsNavigationRequest()) {
			return
		}
		if response.Status() >= http.StatusBadRequest {
			retryAfter, _ := response.HeaderValue("retry-after")
			contentType, _ := response.HeaderValue("content-type")
			err := &authHTTPStatusError{
				Status:     response.Status(),
				StatusText: response.StatusText(),
				Path:       requestPath,
				Stage:      authStageForPath(requestPath),
				RetryAfter: parseRetryAfter(retryAfter, time.Now()),
			}
			if isChallenge {
				err.Cause = ErrCloudflareChallenge
				err.Stage = LoginStageChallenge
				err.Message = "Cloudflare challenge：浏览器验证未通过，当前尝试结束并自动重试"
			} else if strings.Contains(strings.ToLower(contentType), "json") {
				// Body() needs the Playwright event loop to advance, so let this
				// response callback return before waiting for it.
				go func() {
					bodyCh := make(chan []byte, 1)
					go func() {
						body, bodyErr := response.Body()
						if bodyErr != nil {
							body = nil
						}
						bodyCh <- body
					}()
					select {
					case body := <-bodyCh:
						if len(body) > 64<<10 {
							body = body[:64<<10]
						}
						err.UpstreamCode, err.Message = authResponseDetails(body)
					case <-time.After(500 * time.Millisecond):
					}
					select {
					case statuses <- err:
					default:
					}
				}()
				return
			}
			select {
			case statuses <- err:
			default:
			}
			// Keep the HTTP status and challenge together instead of racing two errors.
			return
		}
		if isChallenge {
			select {
			case challenges <- struct{}{}:
			default:
			}
		}
	})
}

func takeAuthFailure(challenges <-chan struct{}, statuses <-chan error) error {
	select {
	case err := <-statuses:
		if err != nil {
			return err
		}
	default:
	}
	if takeSignal(challenges) {
		return cloudflareError()
	}
	return nil
}

func waitForAuthSelector(ctx context.Context, page playwright.Page, selector string, timeout time.Duration, challenges <-chan struct{}, statuses <-chan error) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	locator := page.Locator(selector).First()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := takeAuthFailure(nil, statuses); err != nil {
			if !errors.Is(err, ErrCloudflareChallenge) {
				return authFailureWithPage(page, err)
			}
			if recoveryErr := waitChallengeRecovery(ctx, page, challenges, statuses, err); recoveryErr != nil {
				return recoveryErr
			}
			continue
		}
		if takeSignal(challenges) {
			if recoveryErr := waitChallengeRecovery(ctx, page, challenges, statuses, cloudflareError()); recoveryErr != nil {
				return recoveryErr
			}
			continue
		}
		if rejection := accountRejectionOnPage(page); rejection != nil {
			return rejection
		}
		visible, err := locator.IsVisible()
		if err != nil {
			return err
		}
		if visible {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-statuses:
			if err != nil {
				if errors.Is(err, ErrCloudflareChallenge) {
					if recoveryErr := waitChallengeRecovery(ctx, page, challenges, statuses, err); recoveryErr != nil {
						return recoveryErr
					}
					continue
				}
				return authFailureWithPage(page, err)
			}
		case <-challenges:
			if recoveryErr := waitChallengeRecovery(ctx, page, challenges, statuses, cloudflareError()); recoveryErr != nil {
				return recoveryErr
			}
			continue
		case <-deadline.C:
			if err := detectAccessBlock(page, challenges, statuses); err != nil {
				return err
			}
			return fmt.Errorf("%w：等待登录输入框超时", ErrUnexpectedAuthPage)
		case <-tick.C:
		}
	}
}

func detectAccessBlock(page playwright.Page, cloudflareChallengeCh <-chan struct{}, authStatuses ...<-chan error) error {
	var statuses <-chan error
	if len(authStatuses) > 0 {
		statuses = authStatuses[0]
	}
	if err := takeAuthFailure(cloudflareChallengeCh, statuses); err != nil {
		return authFailureWithPage(page, err)
	}
	if isUnsupportedRegion(page) {
		return ErrUnsupportedRegion
	}
	if isCloudflareChallenge(page) {
		return cloudflareError()
	}
	if rejection := accountRejectionOnPage(page); rejection != nil {
		return rejection
	}
	return nil
}

func oauthCallbackCode(rawURL, expectedState string) (string, error) {
	callback, err := url.Parse(rawURL)
	if err != nil {
		return "", errors.New("解析 OAuth 回调失败")
	}
	query := callback.Query()
	if query.Get("state") != expectedState {
		return "", errors.New("OAuth state 校验失败")
	}
	if code := query.Get("error"); code != "" {
		normalized := normalizeAuthCode(code)
		if normalized == "" {
			normalized = LoginErrorOAuth
		}
		return "", &authRejectionError{Code: normalized, Message: sanitizeLoginText(query.Get("error_description")), Stage: "oauth"}
	}
	if code := query.Get("code"); code != "" {
		return code, nil
	}
	return "", errors.New("OAuth 回调缺少 authorization code")
}

func authFailureWithPage(page playwright.Page, failure error) error {
	var statusErr *authHTTPStatusError
	if !errors.As(failure, &statusErr) || statusErr.Message != "" || statusErr.UpstreamCode != "" || errors.Is(failure, ErrCloudflareChallenge) {
		return failure
	}
	if rejection := accountRejectionOnPage(page); rejection != nil {
		withMessage := *statusErr
		withMessage.Message = rejection.(*authRejectionError).Message
		return &withMessage
	}
	return failure
}

func accountRejectionOnPage(page playwright.Page) error {
	if page == nil {
		return nil
	}
	u, err := url.Parse(page.URL())
	if err != nil || u.Scheme != "https" || u.Host != "auth.openai.com" {
		return nil
	}
	body, err := page.Locator("body").InnerText(playwright.LocatorInnerTextOptions{Timeout: playwright.Float(1000)})
	if err != nil {
		return nil
	}
	// Only a specific visible account rejection is evidence, never a generic
	// 403, hidden script text, or another origin. No page body leaves this helper.
	const message = "You do not have an account because it has been deleted or deactivated."
	if strings.Contains(strings.ToLower(strings.Join(strings.Fields(body), " ")), strings.ToLower(message)) {
		return &authRejectionError{Code: "account_unavailable", Message: message}
	}
	return nil
}

func openAIRequestPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Hostname() != "openai.com" && !strings.HasSuffix(u.Hostname(), ".openai.com")) {
		return ""
	}
	return u.Host + u.EscapedPath()
}

func authDiagnosticPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() != "auth.openai.com" || strings.HasPrefix(u.Path, "/awe/") || strings.HasPrefix(u.Path, "/cdn-cgi/") {
		return ""
	}
	return u.Host + u.EscapedPath()
}

func authPageSummary(page playwright.Page, email string) string {
	title, _ := page.Title()
	_ = email // retained in the helper signature for callers and compatibility
	return fmt.Sprintf("%s title=%q", openAIRequestPath(page.URL()), sanitizeLoginText(title))
}

func cloudflareError() error {
	return fmt.Errorf("%w：浏览器验证未通过，当前尝试结束并自动重试", ErrCloudflareChallenge)
}

func takeSignal(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func isUnsupportedRegion(page playwright.Page) bool {
	content, _ := page.Locator("body").InnerText(playwright.LocatorInnerTextOptions{Timeout: playwright.Float(challengeProbeTimeoutMS)})
	content = strings.ToLower(content)
	return strings.Contains(content, "unsupported_country_region_territory") ||
		strings.Contains(content, "country, region, or territory not supported")
}

// containsAny checks if the string contains any of the substrings
func containsAny(s string, substrs []string) bool {
	for _, substr := range substrs {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

func randomDelay(ctx context.Context, minMs, maxMs int) error {
	if minMs < 0 {
		minMs = 0
	}
	if maxMs < minMs {
		maxMs = minMs
	}
	delay := minMs
	if maxMs > minMs {
		if n, err := rand.Int(rand.Reader, big.NewInt(int64(maxMs-minMs+1))); err == nil {
			delay += int(n.Int64())
		}
	}
	return waitContext(ctx, time.Duration(delay)*time.Millisecond)
}

func waitContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
