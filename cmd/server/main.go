package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

//go:embed index.html
var indexHTML string

//go:embed client.js
var clientJS string

//go:embed account-checks.js
var accountChecksJS string

var (
	bindAddress = flag.String("bind", "127.0.0.1", "服务器监听地址")
	port        = flag.String("port", "8080", "服务器端口")
	headless    = flag.Bool("headless", false, "使用无头浏览器模式")
	openOnStart = flag.Bool("open-browser", runtime.GOOS == "darwin", "启动时打开管理页面")
	// Keep the default at one login so a fresh install does not fan out
	// multiple OAuth browser sessions against the same exit. Operators can
	// explicitly raise this with -max-concurrent after single-account checks.
	maxConcurrent = flag.Int("max-concurrent", 1, "同时登录的最大账号数（1-10）")
	rateLimit     = flag.Int("rate-limit", 30, "每个客户端 IP 每 10 分钟的请求上限")
	loginTimeout  = flag.Duration("login-timeout", 3*time.Minute, "单个账号登录的总超时时间")
	proxy         = flag.String("proxy", os.Getenv("OPENAI_LOGIN_PROXY"), "代理地址")
	upstreamProxy = flag.String("upstream-proxy", "", "用于连接出口代理的前置 HTTP 代理")
	browserCompat = flag.Bool("browser-compat", false, "使用已验证的 Mac Chrome 兼容浏览器配置")
)

// 简单的速率限制器
type rateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	limit    int
	window   time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		requests: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
}

func (rl *rateLimiter) allow(ip string) bool {
	return rl.reserve(ip) == 0
}

// reserve returns the minimum wait without consuming a slot on rejection.
func (rl *rateLimiter) reserve(ip string) time.Duration {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// 清理过期的请求记录
	times := rl.requests[ip]
	var validTimes []time.Time
	for _, t := range times {
		if t.After(cutoff) {
			validTimes = append(validTimes, t)
		}
	}

	// 检查是否超过限制
	if len(validTimes) >= rl.limit {
		rl.requests[ip] = validTimes
		return validTimes[0].Add(rl.window).Sub(now)
	}

	// 记录新请求
	validTimes = append(validTimes, now)
	rl.requests[ip] = validTimes
	return 0
}

var (
	limiter           = newRateLimiter(30, 10*time.Minute)
	proxyCheckLimiter = newRateLimiter(10, time.Minute)
	loginSlots        = make(chan struct{}, 1)
	emailPattern      = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
)

type LoginRequest struct {
	Email         string `json:"email"`
	Password      string `json:"password"`
	TotpSecret    string `json:"totp_secret"`
	Proxy         string `json:"proxy"`
	UpstreamProxy string `json:"upstream_proxy"`
}

type LoginResponse struct {
	Success    bool                  `json:"success"`
	Message    string                `json:"message"`
	Data       *login.LoginResult    `json:"data,omitempty"`
	Error      *login.LoginErrorInfo `json:"error,omitempty"`
	Code       string                `json:"code,omitempty"`
	RetryAfter int                   `json:"retry_after,omitempty"`
	Timings    map[string]int64      `json:"timings,omitempty"`
}

type proxyCheckRequest struct {
	Proxy string `json:"proxy"`
}

func main() {
	flag.Parse()
	if *maxConcurrent < 1 || *maxConcurrent > 10 {
		log.Fatal("❌ 最大并发数必须在 1 到 10 之间")
	}
	if *rateLimit < 1 || *loginTimeout <= 0 {
		log.Fatal("❌ 请求上限和登录总超时时间必须大于 0")
	}
	limiter = newRateLimiter(*rateLimit, 10*time.Minute)
	loginSlots = make(chan struct{}, *maxConcurrent)
	if err := login.ValidateHTTPProxy(*proxy); err != nil {
		log.Fatalf("❌ 代理配置无效: %v", err)
	}
	if err := login.ValidateHTTPProxy(*upstreamProxy); err != nil {
		log.Fatalf("❌ 前置代理配置无效: %v", err)
	}
	if strings.TrimSpace(*upstreamProxy) != "" && strings.TrimSpace(*proxy) == "" {
		log.Fatal("❌ 使用前置代理时必须配置出口 HTTP 代理")
	}
	if err := initLoginHistory(*historyDBPath, *historyKeyPath); err != nil {
		log.Fatalf("❌ 初始化账号历史数据库失败: %v", err)
	}
	sub2Importer = newSub2ImportService(loginHistory)
	sub2Importer.Start()
	accountChecker = newAccountCheckService(loginHistory, *proxy, *upstreamProxy)
	accountChecker.Start()
	defer accountChecker.Stop()

	// 创建登录服务
	service := login.NewService(login.Config{
		Headless:             *headless,
		Proxy:                *proxy,
		UpstreamProxy:        *upstreamProxy,
		RetryCount:           2,
		Timeout:              60 * time.Second,
		TotalTimeout:         *loginTimeout,
		BrowserCompatibility: *browserCompat,
	})
	recoveryService := newSub2RecoveryService(loginHistory, service, sub2Importer)
	recoveryService.Start()
	defer recoveryService.Stop()

	// 设置路由
	http.HandleFunc("/", serveIndex)
	http.HandleFunc("/api/client.js", serveClientJS)
	http.HandleFunc("/api/account-checks.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = io.WriteString(w, accountChecksJS)
	})
	http.HandleFunc("/api/account-checks", corsMiddleware(accountChecker.handleCollection))
	http.HandleFunc("/api/account-checks/", corsMiddleware(accountChecker.handleAction))
	http.HandleFunc("/api/login", corsMiddleware(handleLogin(service)))
	http.HandleFunc("/api/login/stream", corsMiddleware(handleLoginStream(service)))
	http.HandleFunc("/api/proxy-check", corsMiddleware(handleProxyCheck(service)))
	http.HandleFunc("/api/history/login", corsMiddleware(handleHistoryLogin(service)))
	http.HandleFunc("/api/history/", corsMiddleware(handleHistoryDelete()))
	http.HandleFunc("/api/history", corsMiddleware(handleHistory()))
	http.HandleFunc("/api/sub2/import", corsMiddleware(sub2Importer.handleImport))
	http.HandleFunc("/api/sub2/import/", corsMiddleware(sub2Importer.handleAction))
	http.HandleFunc("/api/account-recovery", corsMiddleware(recoveryService.handleCollection))
	http.HandleFunc("/api/account-recovery/", corsMiddleware(recoveryService.handleAction))
	http.HandleFunc("/health", handleHealth)

	addr := net.JoinHostPort(*bindAddress, *port)
	pageURL := "http://" + addr

	log.Printf("🚀 OpenAI 登录平台启动成功！")
	log.Printf("📍 访问地址: %s", pageURL)
	log.Printf("⚙️  无头模式: %v", *headless)
	if *proxy != "" {
		log.Printf("🔌 代理设置: 已配置 HTTP 代理")
	}
	log.Println()

	if *openOnStart {
		go func() {
			time.Sleep(500 * time.Millisecond)
			openBrowser(pageURL)
		}()
	}

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("❌ 服务器启动失败: %v", err)
	}
}

func handleLogin(service *login.Service) http.HandlerFunc {
	return handleLoginRequest(service, false)
}

func handleLoginStream(service *login.Service) http.HandlerFunc {
	return handleLoginRequest(service, true)
}

func handleProxyCheck(service *login.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			respondJSONStatus(w, http.StatusMethodNotAllowed, LoginResponse{Message: "Method not allowed"})
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			respondJSONStatus(w, http.StatusUnsupportedMediaType, LoginResponse{Message: "请求必须使用 application/json"})
			return
		}
		if delay := proxyCheckLimiter.reserve(getClientIP(r)); delay > 0 {
			seconds := int((delay + time.Second - 1) / time.Second)
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			respondJSONStatus(w, http.StatusTooManyRequests, LoginResponse{Message: "平台请求达到上限，等待后继续", Code: "platform_rate_limit", RetryAfter: seconds})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		var req proxyCheckRequest
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: "请求格式错误"})
			return
		}
		if err := decoder.Decode(new(interface{})); err != io.EOF {
			respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: "请求只能包含一个 JSON 对象"})
			return
		}
		result, err := service.CheckProxy(r.Context(), req.Proxy)
		if err != nil {
			message := result.Message
			if message == "" {
				message = "代理配置无效或无法检查"
			}
			respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: message})
			return
		}
		respondJSON(w, result)
	}
}

func handleLoginRequest(service *login.Service, stream bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Acquire the login slot before reserving the IP budget. A request that
		// cannot run because all slots are occupied must not turn into a rate
		// limit hit after the client retries it.
		req, status, err := decodeLoginRequest(w, r)
		if err != nil {
			respondJSONStatus(w, status, LoginResponse{
				Success: false,
				Message: err.Error(),
			})
			return
		}
		if err := validateLoginRequest(&req); err != nil {
			respondJSONStatus(w, http.StatusBadRequest, LoginResponse{
				Success: false,
				Message: err.Error(),
			})
			return
		}

		limitConcurrentLogins(loginSlots, func(w http.ResponseWriter, r *http.Request) {
			// 速率限制检查
			ip := getClientIP(r)
			if delay := limiter.reserve(ip); delay > 0 {
				seconds := int((delay + time.Second - 1) / time.Second)
				w.Header().Set("Retry-After", strconv.Itoa(seconds))
				respondJSONStatus(w, http.StatusTooManyRequests, LoginResponse{
					Success: false,
					Message: "平台请求达到上限，等待后继续",
					Code:    "platform_rate_limit", RetryAfter: seconds,
				})
				return
			}

			if stream {
				log.Printf("📧 收到登录请求: %s", maskEmail(req.Email))
				serveLoginStream(w, r, func(ctx context.Context) (*login.LoginResult, error) {
					return runWithHistory(ctx, req, func(ctx context.Context) (*login.LoginResult, error) {
						return service.LoginWithProxiesContext(ctx, req.Email, req.Password, req.TotpSecret, req.Proxy, req.UpstreamProxy)
					})
				})
				return
			}
			serveLoginJSON(w, r, service, req)
		})(w, r)
	}
}

func decodeLoginRequest(w http.ResponseWriter, r *http.Request) (LoginRequest, int, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return LoginRequest{}, http.StatusUnsupportedMediaType, errors.New("请求必须使用 application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req LoginRequest
	decoder := json.NewDecoder(r.Body)
	decodeErr := decoder.Decode(&req)
	if decodeErr == nil {
		if err := decoder.Decode(new(interface{})); err != io.EOF {
			decodeErr = errors.New("请求只能包含一个 JSON 对象")
			if err != nil {
				decodeErr = err
			}
		}
	}
	if decodeErr != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(decodeErr, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		return LoginRequest{}, status, errors.New("请求格式错误: " + decodeErr.Error())
	}
	return req, http.StatusOK, nil
}

func serveLoginJSON(w http.ResponseWriter, r *http.Request, service *login.Service, req LoginRequest) {
	log.Printf("📧 收到登录请求: %s", maskEmail(req.Email))
	result, err := runWithHistory(r.Context(), req, func(ctx context.Context) (*login.LoginResult, error) {
		return service.LoginWithProxiesContext(ctx, req.Email, req.Password, req.TotpSecret, req.Proxy, req.UpstreamProxy)
	})
	if err != nil {
		info := describeLoginError(err)
		log.Printf("❌ 登录失败: stage=%s code=%s status=%d message=%s", info.Stage, info.Code, info.HTTPStatus, info.Message)
		respondJSONStatus(w, loginHTTPStatus(err), LoginResponse{
			Success: false,
			Message: "登录失败: " + info.Message,
			Error:   &info,
		})
		return
	}
	log.Printf("✅ 登录成功: %s (%s)", maskEmail(req.Email), result.PlanType)
	respondJSON(w, LoginResponse{
		Success: true,
		Message: "登录成功",
		Data:    result,
	})
}

func loginHTTPStatus(err error) int {
	if errors.Is(err, store.ErrAccountBusy) {
		return http.StatusConflict
	}
	if errors.Is(err, store.ErrAccountNotFound) {
		return http.StatusNotFound
	}
	if upstreamStatus, ok := login.AuthHTTPStatus(err); ok {
		return upstreamStatus
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	if errors.Is(err, context.Canceled) {
		return http.StatusRequestTimeout
	}
	return http.StatusBadGateway
}

type loginStreamProgress struct {
	Type string `json:"type"`
	login.Progress
}

type loginStreamResult struct {
	Type string `json:"type"`
	LoginResponse
}

func serveLoginStream(w http.ResponseWriter, r *http.Request, run func(context.Context) (*login.LoginResult, error)) {
	_, ok := w.(http.Flusher)
	if !ok {
		respondJSONStatus(w, http.StatusInternalServerError, LoginResponse{Message: "服务器不支持实时进度"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	defer controller.SetWriteDeadline(time.Time{})

	writeEvent := func(payload interface{}) bool {
		_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
		data, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
		return controller.Flush() == nil
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	progressCh := make(chan login.Progress, 32)
	progressCtx := login.WithProgress(ctx, func(progress login.Progress) {
		select {
		case progressCh <- progress:
		default:
		}
	})
	type loginOutcome struct {
		result *login.LoginResult
		err    error
	}
	outcomeCh := make(chan loginOutcome, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		result, err := run(progressCtx)
		outcomeCh <- loginOutcome{result: result, err: err}
	}()
	// Keep the concurrency slot until disconnect cancellation has cleaned up Chrome.
	defer func() { cancel(); <-finished }()

	if !writeEvent(loginStreamProgress{Type: "progress", Progress: login.Progress{
		Stage:   "queued",
		Message: "已开始登录",
	}}) {
		return
	}
	timings := make(map[string]int64)
	lastStage := "unknown"
	sendProgress := func(progress login.Progress) bool {
		lastStage = progress.Stage
		if progress.StageMS > 0 {
			timings[progress.Stage] += progress.StageMS
		}
		return writeEvent(loginStreamProgress{Type: "progress", Progress: progress})
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case progress := <-progressCh:
			if !sendProgress(progress) {
				return
			}
		case outcome := <-outcomeCh:
			// The runner has finished; deliver its remaining stages before the result.
			for len(progressCh) > 0 {
				if !sendProgress(<-progressCh) {
					return
				}
			}
			log.Printf("登录阶段耗时(ms): %v", timings)
			if outcome.err != nil {
				info := describeLoginError(outcome.err)
				if info.Stage == "unknown" || info.Code == "timeout" || info.Code == "canceled" {
					info.Stage = lastStage
				}
				log.Printf("❌ 登录失败: stage=%s code=%s status=%d", info.Stage, info.Code, info.HTTPStatus)
				writeEvent(loginStreamResult{Type: "result", LoginResponse: LoginResponse{
					Success: false,
					Message: "登录失败: " + info.Message,
					Error:   &info,
					Timings: timings,
				}})
				return
			}
			writeEvent(loginStreamResult{Type: "result", LoginResponse: LoginResponse{
				Success: true,
				Message: "登录成功",
				Data:    outcome.result,
				Timings: timings,
			}})
			return
		case <-ticker.C:
			_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			if controller.Flush() != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func limitConcurrentLogins(slots chan struct{}, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			next(w, r)
		default:
			w.Header().Set("Retry-After", "5")
			respondJSONStatus(w, http.StatusTooManyRequests, LoginResponse{Message: "登录任务已满，请 5 秒后重试", Code: "platform_busy", RetryAfter: 5})
		}
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, map[string]interface{}{
		"status":         "ok",
		"time":           time.Now().Format(time.RFC3339),
		"max_concurrent": *maxConcurrent,
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, indexHTML)
}

func serveClientJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = io.WriteString(w, clientJS)
}

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			parsed, err := url.Parse(origin)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
				parsed.Host != r.Host || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
				respondJSONStatus(w, http.StatusForbidden, LoginResponse{Message: "不允许来自其他网站的请求"})
				return
			}
		}

		if r.Method == http.MethodOptions {
			w.Header().Set("Allow", "POST, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next(w, r)
	}
}

func respondJSON(w http.ResponseWriter, data interface{}) {
	respondJSONStatus(w, http.StatusOK, data)
}

func respondJSONStatus(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// 验证登录请求
func validateLoginRequest(req *LoginRequest) error {
	// 验证邮箱格式
	if !emailPattern.MatchString(req.Email) {
		return fmt.Errorf("邮箱格式不正确")
	}

	// 验证密码长度
	if len(req.Password) < 8 {
		return fmt.Errorf("密码长度至少8位")
	}

	// 验证 TOTP 密钥格式
	if !login.ValidateTOTPSecret(req.TotpSecret) {
		return fmt.Errorf("TOTP 密钥格式不正确（应为 Base32 格式）")
	}
	if err := login.ValidateHTTPProxy(req.Proxy); err != nil {
		return fmt.Errorf("代理配置无效: %w", err)
	}
	if err := login.ValidateHTTPProxy(req.UpstreamProxy); err != nil {
		return fmt.Errorf("前置代理配置无效: %w", err)
	}

	return nil
}

// 获取客户端 IP
func getClientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// 隐藏邮箱中间部分
func maskEmail(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return email
	}

	local := parts[0]
	domain := parts[1]

	if len(local) <= 2 {
		return email
	}

	masked := string(local[0]) + "***" + string(local[len(local)-1]) + "@" + domain
	return masked
}

func openBrowser(url string) {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	default:
		log.Printf("ℹ️  请手动打开浏览器访问: %s", url)
		return
	}

	if err := exec.Command(cmd, args...).Start(); err != nil {
		log.Printf("ℹ️  无法自动打开浏览器，请手动访问: %s", url)
	}
}
