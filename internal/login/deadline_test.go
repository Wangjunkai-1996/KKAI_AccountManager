package login

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func TestDeadlineCanceledLoginDoesNotStartBrowser(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service := NewService(Config{})
	if service.config.TotalTimeout != 3*time.Minute {
		t.Fatalf("default total timeout = %s", service.config.TotalTimeout)
	}
	// No Playwright installation is needed for an already canceled request.
	result, err := service.LoginWithProxiesContext(ctx, "fixture@example.com", "unused", "unused", "", "")
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("result = %v, error = %v", result, err)
	}
}

func TestDeadlineInterruptsCallbackAndRetryDelay(t *testing.T) {
	for _, phase := range []string{"callback", "retry delay"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			var err error
			if phase == "callback" {
				// A nil page also proves cancellation returns before touching the UI.
				_, err = waitOAuthCallbackContext(ctx, nil, nil, nil, time.Minute, "", "")
			} else {
				err = waitContext(ctx, time.Minute)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v, want deadline exceeded", err)
			}
		})
	}
}

type deadlineTransport func(*http.Request) (*http.Response, error)

func (f deadlineTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestDeadlineCancelsTokenExchange(t *testing.T) {
	originalTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	requestStopped := make(chan struct{})
	http.DefaultTransport = deadlineTransport(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		close(requestStopped)
		return nil, req.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := NewService(Config{}).exchangeCode(ctx, "test-code", "test-verifier")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	select {
	case <-requestStopped:
	default:
		t.Fatal("token request continued after cancellation")
	}
}

func TestDeadlineBrowserCancellationClosesConnections(t *testing.T) {
	if os.Getenv("OPENAI_LOGIN_BROWSER_TEST") != "1" {
		t.Skip("set OPENAI_LOGIN_BROWSER_TEST=1 for the local Chrome cancellation regression")
	}
	// The local proxy accepts CONNECT but intentionally never completes TLS.
	// No requests reach OpenAI and no real account credentials are used.
	connected := make(chan struct{})
	var connectedOnce sync.Once
	var connections sync.Map
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		connections.Store(conn, struct{}{})
		defer connections.Delete(conn)
		defer conn.Close()
		_, _ = fmt.Fprint(buffered, "HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffered.Flush()
		if r.Host == "auth.openai.com:443" {
			connectedOnce.Do(func() { close(connected) })
		}
		_, _ = io.Copy(io.Discard, buffered)
	}))
	t.Cleanup(func() {
		proxy.Close()
		connections.Range(func(key, _ any) bool {
			_ = key.(net.Conn).Close()
			return true
		})
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := NewService(Config{Headless: true, TotalTimeout: 15 * time.Second}).LoginWithProxiesContext(ctx, "fixture@example.com", "unused", "unused", proxy.URL, "")
		done <- err
	}()
	select {
	case <-connected:
	case err := <-done:
		t.Fatalf("login returned before reaching proxy: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("browser did not reach local proxy")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("browser kept running after request cancellation")
	}
	// Returning from login must include Chrome shutdown, not leave detached work.
	deadline := time.Now().Add(time.Second)
	for {
		active := 0
		connections.Range(func(_, _ any) bool { active++; return true })
		if active == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d browser proxy connections survived cancellation", active)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
