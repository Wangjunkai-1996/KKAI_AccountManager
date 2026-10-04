package login

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// startProxyRelay reaches the target HTTP proxy through an upstream HTTP proxy.
// Target proxy authentication remains in the returned URL and inside the tunnel.
func startProxyRelay(proxy, upstream string) (string, func(), error) {
	targetURL, err := parseHTTPProxy(proxy)
	if err != nil {
		return "", nil, err
	}
	upstreamURL, err := parseHTTPProxy(upstream)
	if err != nil {
		return "", nil, err
	}
	if targetURL == nil || upstreamURL == nil {
		return "", nil, errors.New("代理中转需要目标 HTTP 代理和上游 HTTP 代理")
	}
	address := func(u *url.URL) string {
		port := u.Port()
		if port == "" {
			port = "80"
		}
		return net.JoinHostPort(u.Hostname(), port)
	}
	targetAddress, upstreamAddress := address(targetURL), address(upstreamURL)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var wg sync.WaitGroup
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
	stop := func() {
		once.Do(func() {
			mu.Lock()
			closed = true
			cancel()
			listener.Close()
			for conn := range connections {
				conn.Close()
			}
			mu.Unlock()
			wg.Wait()
		})
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			if !track(client) {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer untrack(client)
				dialer := net.Dialer{Timeout: 10 * time.Second}
				tunnel, err := dialer.DialContext(ctx, "tcp", upstreamAddress)
				if err != nil {
					return
				}
				if !track(tunnel) {
					return
				}
				defer untrack(tunnel)
				tunnel.SetDeadline(time.Now().Add(10 * time.Second))
				request := &http.Request{
					Method: http.MethodConnect,
					URL:    &url.URL{Opaque: targetAddress},
					Host:   targetAddress,
					Header: make(http.Header),
				}
				if upstreamURL.User != nil {
					password, _ := upstreamURL.User.Password()
					credentials := upstreamURL.User.Username() + ":" + password
					request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(credentials)))
				}
				if err := request.Write(tunnel); err != nil {
					return
				}
				reader := bufio.NewReader(tunnel)
				response, err := http.ReadResponse(reader, request)
				if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
					return
				}
				tunnel.SetDeadline(time.Time{})
				done := make(chan struct{}, 2)
				go func() { io.Copy(tunnel, client); done <- struct{}{} }()
				go func() { io.Copy(client, reader); done <- struct{}{} }()
				<-done
				client.Close()
				tunnel.Close()
				<-done
			}()
		}
	}()
	targetURL.Host = listener.Addr().String()
	return targetURL.String(), stop, nil
}
