package login

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCheckProxyURLDirect403IsReachable(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer target.Close()

	result, err := checkProxyURL(context.Background(), target.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reachable || result.HTTPStatus != http.StatusForbidden || result.Mode != "direct" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !strings.Contains(result.Message, "不代表登录一定成功") {
		t.Fatalf("message = %q, want 403 connectivity explanation", result.Message)
	}
}

func TestCheckProxyURLUsesHTTPProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.RequestURI == "" || !strings.HasPrefix(r.RequestURI, "http://") {
			t.Errorf("proxy request = %s %s, want absolute-form GET", r.Method, r.RequestURI)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxy.Close()

	result, err := checkProxyURL(context.Background(), "http://target.invalid/log-in", proxy.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reachable || result.HTTPStatus != http.StatusNoContent || result.Mode != "proxy" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCheckProxyURLProxyAuthFailure(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer proxy.Close()

	result, err := checkProxyURL(context.Background(), "http://target.invalid/log-in", proxy.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Reachable || result.HTTPStatus != http.StatusProxyAuthRequired {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Message != "代理认证失败（HTTP 407）" {
		t.Fatalf("message = %q", result.Message)
	}
}

func TestCheckProxyURLTimeoutIsSafe(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer target.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result, err := checkProxyURL(ctx, target.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Reachable || result.Message != "代理连接超时" {
		t.Fatalf("unexpected result: %+v", result)
	}
}
