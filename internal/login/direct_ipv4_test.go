package login

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestDirectIPv4ProxySelection(t *testing.T) {
	service := NewService(Config{Proxy: "http://exit.example:8080", UpstreamProxy: "http://upstream.example:8080"})
	for _, test := range []struct {
		name, proxy, upstream, wantProxy, wantUpstream string
		wantError                                      bool
	}{
		{name: "inherit", wantProxy: service.config.Proxy, wantUpstream: service.config.UpstreamProxy},
		{name: "override", proxy: "http://other.example:80", wantProxy: "http://other.example:80", wantUpstream: service.config.UpstreamProxy},
		{name: "explicit direct", proxy: " direct "},
		{name: "direct with upstream", proxy: "direct", upstream: "http://other.example:80", wantError: true},
		{name: "direct is not an upstream", upstream: "direct", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := service.loginConfig(test.proxy, test.upstream)
			if (err != nil) != test.wantError {
				t.Fatalf("loginConfig error = %v", err)
			}
			if err == nil && (got.Proxy != test.wantProxy || got.UpstreamProxy != test.wantUpstream) {
				t.Fatalf("unexpected resolved proxies: %+v", got)
			}
		})
	}
	if service.config.Proxy != "http://exit.example:8080" || service.config.UpstreamProxy != "http://upstream.example:8080" {
		t.Fatal("per-request direct mutated the shared defaults")
	}
	if ValidateLoginProxy("direct") != nil || ValidateHTTPProxy("direct") == nil {
		t.Fatal("direct must be accepted only as a login exit selector")
	}
}

func TestDirectIPv4RelayDNSAndCancellation(t *testing.T) {
	for _, cancelByContext := range []bool{false, true} {
		t.Run(fmt.Sprintf("context=%v", cancelByContext), func(t *testing.T) {
			target, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			remoteClosed := make(chan struct{})
			go func() {
				conn, err := target.Accept()
				if err == nil {
					_, _ = io.Copy(conn, conn)
					conn.Close()
				}
				close(remoteClosed)
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			relay, stop, err := startDirectIPv4Relay(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer stop()
			relayURL, _ := url.Parse(relay)
			if relayURL.Hostname() != "127.0.0.1" || !shouldUseNativeChrome(Config{Proxy: relay}) {
				t.Fatal("direct relay must stay on loopback and retain native Chrome")
			}
			conn, err := net.DialTimeout("tcp", relayURL.Host, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			_, port, _ := net.SplitHostPort(target.Addr().String())
			// localhost has dual-stack records on common hosts; the destination
			// only accepts IPv4, so this exercises DNS without pinning an address.
			_, _ = fmt.Fprintf(conn, "CONNECT localhost:%s HTTP/1.1\r\nHost: localhost:%s\r\n\r\n", port, port)
			reader := bufio.NewReader(conn)
			response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
			if err != nil || response.StatusCode != http.StatusOK {
				t.Fatalf("CONNECT response = %v, error = %v", response, err)
			}
			_, _ = io.WriteString(conn, "echo")
			got := make([]byte, 4)
			if _, err := io.ReadFull(reader, got); err != nil || string(got) != "echo" {
				t.Fatalf("tunnel echo = %q, error = %v", got, err)
			}
			if cancelByContext {
				cancel()
			} else {
				stop()
			}
			if _, err := reader.ReadByte(); err != io.EOF {
				t.Fatalf("client connection after cleanup = %v, want EOF", err)
			}
			select {
			case <-remoteClosed:
			case <-time.After(time.Second):
				t.Fatal("destination connection survived cleanup")
			}
			stop() // Idempotent and waits for cancellation cleanup to finish.
			if conn, err := net.DialTimeout("tcp", relayURL.Host, time.Second); err == nil {
				conn.Close()
				t.Fatal("listener survived cleanup")
			}
		})
	}
}

func TestDirectIPv4RejectsIPv6AndHTTP(t *testing.T) {
	if conn, err := dialDirectIPv4(context.Background(), "tcp", "[::1]:443"); err == nil {
		conn.Close()
		t.Fatal("IPv4 direct dial accepted an IPv6 literal")
	}
	relay, stop, err := startDirectIPv4Relay(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(relay)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("non-CONNECT response = %d", response.StatusCode)
	}
}

func TestDirectIPv4TokenExchangeUsesConfiguredBrowserExit(t *testing.T) {
	destination := make(chan string, 1)
	exit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			destination <- r.Host
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer exit.Close()
	config := Config{Proxy: exit.URL, Timeout: time.Second}
	if !shouldUseNativeChrome(config) {
		t.Fatal("the shared loopback exit did not select native Chrome")
	}
	if _, err := NewService(config).exchangeCode(context.Background(), "fixture-code", "fixture-verifier"); err == nil {
		t.Fatal("token exchange unexpectedly passed the rejecting fixture")
	}
	select {
	case got := <-destination:
		if got != "auth.openai.com:443" {
			t.Fatalf("token exchange CONNECT destination = %q", got)
		}
	default:
		t.Fatal("token exchange bypassed the browser's configured exit")
	}
}
