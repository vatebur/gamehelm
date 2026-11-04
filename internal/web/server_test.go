package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIPTrustsOnlyConfiguredProxyChain(t *testing.T) {
	tests := []struct {
		name      string
		trusted   []netip.Prefix
		remote    string
		forwarded string
		want      string
	}{
		{name: "direct spoof ignored", remote: "192.168.1.10:1234", forwarded: "203.0.113.5", want: "192.168.1.10"},
		{name: "proxy without opt in", remote: "127.0.0.1:1234", forwarded: "203.0.113.5", want: "127.0.0.1"},
		{name: "trusted proxy", trusted: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}, remote: "127.0.0.1:1234", forwarded: "203.0.113.5", want: "203.0.113.5"},
		{name: "client supplied first entry ignored", trusted: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}, remote: "127.0.0.1:1234", forwarded: "198.51.100.99, 203.0.113.5", want: "203.0.113.5"},
		{name: "invalid entry falls back", trusted: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}, remote: "127.0.0.1:1234", forwarded: "bad-input", want: "127.0.0.1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := &Server{proxies: test.trusted}
			request := httptest.NewRequest(http.MethodGet, "http://192.0.2.2:8231/", nil)
			request.RemoteAddr = test.remote
			request.Header.Set("X-Forwarded-For", test.forwarded)
			if got := s.clientIP(request); got != test.want {
				t.Fatalf("client IP = %q, want %q", got, test.want)
			}
		})
	}
}
