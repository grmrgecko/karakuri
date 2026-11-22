package http

import (
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCORSOriginsIsAList locks server.cors_origins as a comma-separated list,
// the form server.trusted_proxies beside it takes. The whole setting was
// handed to the CORS layer as one origin, so naming two origins allowed
// neither and a second frontend host could not be configured. An empty
// setting still allows no origin: an empty list would allow every one.
func TestCORSOriginsIsAList(t *testing.T) {
	cases := []struct {
		name    string
		setting string
		origin  string
		want    string
	}{
		{"the only origin", "https://home.example", "https://home.example", "https://home.example"},
		{"the first of two", "https://home.example, https://panel.example", "https://home.example", "https://home.example"},
		{"the second of two", "https://home.example, https://panel.example", "https://panel.example", "https://panel.example"},
		{"an origin not listed", "https://home.example, https://panel.example", "https://other.example", ""},
		{"the wildcard", "*", "https://other.example", "*"},
		{"an empty setting", "", "https://home.example", ""},
		{"a setting of only separators", " , ", "https://home.example", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := corsHandler(tc.setting)(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
				w.WriteHeader(nethttp.StatusNoContent)
			}))
			req := httptest.NewRequest(nethttp.MethodGet, "/api/health", nil)
			req.Header.Set("Origin", tc.origin)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			assert.Equal(t, tc.want, rec.Header().Get("Access-Control-Allow-Origin"),
				"only a listed origin is allowed, and it is answered by name")
		})
	}
}
