package login

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

func dialDirectIPv4(ctx context.Context, _ string, address string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	return dialer.DialContext(ctx, "tcp4", address)
}

// startDirectIPv4Relay keeps Chrome and token exchange on the server's IPv4
// egress. DNS is resolved for each CONNECT; no upstream proxy or fixed IP is used.
func startDirectIPv4Relay(parent context.Context) (string, func(), error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	var mu sync.Mutex
	var handlers sync.WaitGroup
	var once sync.Once
	closed := false
	connections := make(map[net.Conn]struct{})
	track := func(conn net.Conn) bool {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			conn.Close()
			return false
		}
		connections[conn] = struct{}{}
		return true
	}
	untrack := func(conn net.Conn) {
		conn.Close()
		mu.Lock()
		delete(connections, conn)
		mu.Unlock()
	}
	server := &http.Server{ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if closed {
			mu.Unlock()
			http.Error(w, "direct connection closed", http.StatusServiceUnavailable)
			return
		}
		handlers.Add(1)
		mu.Unlock()
		defer handlers.Done()
		if r.Method != http.MethodConnect {
			http.Error(w, "HTTPS CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		if host, port, err := net.SplitHostPort(r.Host); err != nil || host == "" || port == "" {
			http.Error(w, "invalid CONNECT destination", http.StatusBadRequest)
			return
		}
		remote, err := dialDirectIPv4(ctx, "tcp", r.Host)
		if err != nil {
			http.Error(w, "IPv4 connection failed", http.StatusBadGateway)
			return
		}
		if !track(remote) {
			return
		}
		defer untrack(remote)
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		if !track(client) {
			return
		}
		defer untrack(client)
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err := buffered.Flush(); err != nil {
			return
		}
		done := make(chan struct{})
		go func() { _, _ = io.Copy(remote, buffered); remote.Close(); close(done) }()
		_, _ = io.Copy(client, remote)
		client.Close()
		remote.Close()
		<-done
	})
	served := make(chan struct{})
	go func() { _ = server.Serve(listener); close(served) }()
	stop := func() {
		once.Do(func() {
			cancel()
			mu.Lock()
			closed = true
			for conn := range connections {
				conn.Close()
			}
			mu.Unlock()
			_ = server.Close()
			handlers.Wait()
			<-served
		})
	}
	stopOnCancel := context.AfterFunc(parent, stop)
	return "http://" + listener.Addr().String(), func() { stopOnCancel(); stop() }, nil
}
