package main

import "testing"

func TestDirectProxyLoginValidation(t *testing.T) {
	for _, test := range []struct {
		proxy, upstream string
		wantError       bool
	}{
		{proxy: "direct"},
		{proxy: ""},
		{proxy: "http://exit.example:8080"},
		{proxy: "direct", upstream: "http://upstream.example:8080", wantError: true},
		{proxy: "http://exit.example:8080", upstream: "direct", wantError: true},
	} {
		err := validateLoginRequest(&LoginRequest{
			Email: "fixture@example.com", Password: "fixture-password", TotpSecret: "JBSWY3DPEHPK3PXP",
			Proxy: test.proxy, UpstreamProxy: test.upstream,
		})
		if (err != nil) != test.wantError {
			t.Fatalf("proxy=%q upstream=%q error=%v", test.proxy, test.upstream, err)
		}
	}
}
