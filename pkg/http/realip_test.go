package http

import (
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// TestTrustedRealIP pins who may set the address webhook IP allowlists check.
// Only a peer in server.trusted_proxies may replace RemoteAddr, and
// X-Forwarded-For is read from the right so a client behind a real proxy
// cannot forge its address by prepending one: nginx's $proxy_add_x_forwarded_for
// appends the connecting address, so "203.0.113.9, 198.51.100.7" from the proxy
// means the client claimed 203.0.113.9 and actually connected from
// 198.51.100.7.
func TestTrustedRealIP(t *testing.T) {
	nets := parseTrusted("127.0.0.1, 10.0.0.0/8", zap.NewNop())
	tests := []struct {
		name, peer, xff, realIP, want string
	}{
		{"an untrusted peer keeps its own address", "192.0.2.50:4000", "203.0.113.9", "", "192.0.2.50:4000"},
		{"a trusted proxy reports the client it connected", "127.0.0.1:5000", "198.51.100.7", "", "198.51.100.7"},
		{"a forged hop before the real client is ignored", "127.0.0.1:5000", "203.0.113.9, 198.51.100.7", "", "198.51.100.7"},
		{"trusted hops are skipped from the right", "10.1.2.3:80", "198.51.100.7, 10.9.9.9", "", "198.51.100.7"},
		{"X-Real-IP is used without X-Forwarded-For", "127.0.0.1:5000", "", "198.51.100.8", "198.51.100.8"},
		{"a malformed hop leaves the peer address", "127.0.0.1:5000", "not-an-ip", "", "127.0.0.1:5000"},
	}
	for _, tc := range tests {
		var got string
		handler := trustedRealIP(nets)(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			got = r.RemoteAddr
		}))
		req := httptest.NewRequest(nethttp.MethodPost, "/hook/x", nil)
		req.RemoteAddr = tc.peer
		if tc.xff != "" {
			req.Header.Set("X-Forwarded-For", tc.xff)
		}
		if tc.realIP != "" {
			req.Header.Set("X-Real-IP", tc.realIP)
		}
		handler.ServeHTTP(httptest.NewRecorder(), req)
		assert.Equal(t, tc.want, got, tc.name)
	}
}
