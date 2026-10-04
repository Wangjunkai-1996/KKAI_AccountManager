package login

import (
	"bufio"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestProxyRelayAuthenticated(t *testing.T) {
	basic := func(value string) string { return "Basic " + base64.StdEncoding.EncodeToString([]byte(value)) }
	targetAuth := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetAuth <- r.Header.Get("Proxy-Authorization")
		io.WriteString(w, "target response")
	}))
	defer target.Close()
	upstreamRequest := make(chan *http.Request, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamRequest <- r
		if r.Method != http.MethodConnect || r.Header.Get("Proxy-Authorization") != basic("upstream-user:upstream-password") {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		remote, err := net.Dial("tcp", target.Listener.Addr().String())
		if err != nil {
			t.Errorf("dial fake target: %v", err)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer remote.Close()
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack fake upstream: %v", err)
			return
		}
		defer conn.Close()
		io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		done := make(chan struct{})
		go func() { io.Copy(remote, buffered); close(done) }()
		io.Copy(conn, remote)
		conn.Close()
		<-done
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	upstreamURL.User = url.UserPassword("upstream-user", "upstream-password")
	relay, stop, err := startProxyRelay("http://target-user:target-password@proxy.example.test", upstreamURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	relayURL, err := url.Parse(relay)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(relayURL), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Get("http://origin.example.test/resource")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "target response" {
		t.Fatalf("response = %q, error = %v", body, err)
	}
	if auth := <-targetAuth; auth != basic("target-user:target-password") {
		t.Fatalf("target proxy received wrong authentication: %q", auth)
	}
	request := <-upstreamRequest
	if request.Host != "proxy.example.test:80" {
		t.Fatalf("upstream CONNECT target = %q, want default port 80", request.Host)
	}
	if relayURL.Hostname() != "127.0.0.1" || relayURL.User.Username() != "target-user" {
		t.Fatalf("relay did not preserve target userinfo and bind loopback: %s", relayURL.Redacted())
	}
}

func TestProxyRelayRejectsUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer upstream.Close()
	relay, stop, err := startProxyRelay("http://proxy.example.test:8080", upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	relayURL, _ := url.Parse(relay)
	transport := &http.Transport{Proxy: http.ProxyURL(relayURL), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Get("http://origin.example.test/resource")
	if err == nil {
		response.Body.Close()
		t.Fatal("upstream rejection unexpectedly established a tunnel")
	}
}

func TestProxyRelayCleanup(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	requestReceived := make(chan struct{})
	upstreamClosed := make(chan error, 1)
	go func() {
		conn, err := upstream.Accept()
		if err != nil {
			upstreamClosed <- err
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		if _, err := http.ReadRequest(reader); err != nil {
			upstreamClosed <- err
			return
		}
		close(requestReceived)
		_, err = reader.ReadByte()
		upstreamClosed <- err
	}()
	relay, stop, err := startProxyRelay("http://proxy.example.test:8080", "http://"+upstream.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	relayURL, _ := url.Parse(relay)
	conn, err := net.DialTimeout("tcp", relayURL.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case <-requestReceived:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream CONNECT did not arrive")
	}
	stopped := make(chan struct{})
	go func() { stop(); stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup did not stop a pending CONNECT handshake")
	}
	select {
	case err := <-upstreamClosed:
		if err != io.EOF {
			t.Fatalf("upstream connection closed with %v, want EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("upstream connection stayed open")
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("relay client closed with %v, want EOF", err)
	}
	if conn, err := net.DialTimeout("tcp", relayURL.Host, time.Second); err == nil {
		conn.Close()
		t.Fatal("relay listener stayed open after cleanup")
	}
}

func TestProxyRelayInvalidURLs(t *testing.T) {
	for _, urls := range [][2]string{
		{"", "http://upstream.example.test:8080"},
		{"http://proxy.example.test:8080", ""},
		{"https://proxy.example.test:8080", "http://upstream.example.test:8080"},
		{"http://proxy.example.test:8080", "socks5://upstream.example.test:8080"},
	} {
		if _, stop, err := startProxyRelay(urls[0], urls[1]); err == nil {
			stop()
			t.Errorf("startProxyRelay(%q, %q) accepted invalid URLs", urls[0], urls[1])
		}
	}
}
