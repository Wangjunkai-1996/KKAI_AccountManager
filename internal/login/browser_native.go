package login

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// nativeChromeSession is a regular system Chrome process connected through
// CDP. The temporary profile makes each login isolated and keeps credentials
// out of the user's normal browser profile.
type nativeChromeSession struct {
	pw      *playwright.Playwright
	browser playwright.Browser
	close   func()
}

func shouldUseNativeChrome(config Config) bool {
	if config.Headless {
		return false
	}
	proxy := strings.TrimSpace(config.Proxy)
	if proxy == "" {
		return true
	}
	proxyURL, err := parseHTTPProxy(proxy)
	return err == nil && proxyURL != nil && proxyURL.User == nil
}

// startNativeChrome starts the installed Chrome binary as a normal process,
// then attaches Playwright over its localhost DevTools endpoint.
func startNativeChrome(ctx context.Context, config Config) (*nativeChromeSession, error) {
	executable, err := findSystemChrome()
	if err != nil {
		return nil, err
	}
	profileDir, err := os.MkdirTemp("", "openai-login-chrome-")
	if err != nil {
		return nil, fmt.Errorf("创建 Chrome 临时配置目录失败: %w", err)
	}
	removeProfile := sync.OnceFunc(func() { _ = os.RemoveAll(profileDir) })

	port, err := freeTCPPort()
	if err != nil {
		removeProfile()
		return nil, fmt.Errorf("分配 Chrome 调试端口失败: %w", err)
	}
	args := []string{
		"--remote-debugging-address=127.0.0.1",
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-dev-shm-usage",
		"--window-size=1920,1080",
	}
	if proxy := strings.TrimSpace(config.Proxy); proxy != "" {
		proxyURL, err := parseHTTPProxy(proxy)
		if err != nil {
			removeProfile()
			return nil, err
		}
		// Chrome accepts unauthenticated proxy endpoints natively. Authenticated
		// proxies stay on Playwright's proxy path, which handles credentials.
		if proxyURL.User != nil {
			removeProfile()
			return nil, errors.New("带认证的代理不能使用原生 Chrome")
		}
		args = append(args, "--proxy-server=http://"+proxyURL.Host)
	} else {
		args = append(args, "--no-proxy-server")
	}
	// The service may run as root on a dedicated Linux host. Keep the native
	// Chrome path usable there while retaining Chrome's normal rendering path.
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		args = append(args, "--no-sandbox", "--disable-setuid-sandbox")
	}

	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		removeProfile()
		return nil, fmt.Errorf("启动系统 Chrome 失败: %w", err)
	}
	processDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(processDone)
	}()
	stopProcess := sync.OnceFunc(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-processDone
		removeProfile()
	})

	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitForChromeCDP(ctx, endpoint, config.Timeout, processDone); err != nil {
		stopProcess()
		return nil, err
	}

	pw, err := runPlaywright()
	if err != nil {
		stopProcess()
		return nil, fmt.Errorf("playwright 启动失败: %w", err)
	}
	stopDriver := sync.OnceFunc(func() { _ = pw.Stop() })
	stopConnectCancellation := context.AfterFunc(ctx, stopDriver)
	browser, err := pw.Chromium.ConnectOverCDP(endpoint, playwright.BrowserTypeConnectOverCDPOptions{
		Timeout:    playwright.Float(float64(config.Timeout.Milliseconds())),
		IsLocal:    playwright.Bool(true),
		NoDefaults: playwright.Bool(true),
	})
	if !stopConnectCancellation() {
		stopDriver()
	}
	if err != nil {
		stopDriver()
		stopProcess()
		return nil, fmt.Errorf("连接系统 Chrome 调试端口失败: %w", err)
	}

	// loginAttempt owns Playwright/browser shutdown. This callback only tears
	// down the native process and its temporary profile, preventing duplicate
	// pw.Stop/browser.Close calls during cancellation cleanup.
	closeSession := sync.OnceFunc(stopProcess)
	return &nativeChromeSession{pw: pw, browser: browser, close: closeSession}, nil
}

func findSystemChrome() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("OPENAI_LOGIN_CHROME_PATH")); configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured, nil
		}
		return "", fmt.Errorf("OPENAI_LOGIN_CHROME_PATH 不可执行: %s", configured)
	}
	candidates := []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			filepath.Join(os.Getenv("HOME"), "Applications/Google Chrome.app/Contents/MacOS/Google Chrome"),
		)
	} else if runtime.GOOS == "linux" {
		candidates = append(candidates,
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/opt/google/chrome/google-chrome",
		)
	}
	for _, candidate := range candidates {
		if strings.ContainsRune(candidate, os.PathSeparator) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
			continue
		}
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved, nil
		}
	}
	return "", errors.New("未找到系统 Google Chrome；请安装 Chrome 或设置 OPENAI_LOGIN_CHROME_PATH")
}

func freeTCPPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func waitForChromeCDP(ctx context.Context, endpoint string, timeout time.Duration, processDone <-chan struct{}) error {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := &http.Client{Timeout: 500 * time.Millisecond}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(waitCtx, http.MethodGet, endpoint+"/json/version", nil)
		if err == nil {
			response, requestErr := client.Do(request)
			if requestErr == nil {
				_, _ = io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
					return nil
				}
			}
		}
		select {
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("等待系统 Chrome 调试端口超时（%s）", endpoint)
			}
			return waitCtx.Err()
		case <-processDone:
			return errors.New("系统 Chrome 启动后立即退出")
		case <-ticker.C:
		}
	}
}
