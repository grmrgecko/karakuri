package system

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type httpTestContext struct {
	nodeapi.ExecContext
	config []byte
}

// TestAgentHTTPDeadlinesAndInvalidConfiguration rejects unsafe settings before sending and bounds delayed peers.
func TestAgentHTTPDeadlinesAndInvalidConfiguration(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-r.Context().Done()
	}))
	defer peer.Close()
	for _, cfg := range []map[string]any{
		{"output_format": "unsupported"},
		{"headers": map[string]string{"X-Test": "private\r\nInjected: value"}},
	} {
		cfg["url"] = peer.URL
		_, err := (&httpRequestNode{}).Execute(context.Background(), httpContext(t, cfg), nil)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private")
	}
	assert.Zero(t, calls.Load())
	_, err := (&httpRequestNode{}).Execute(context.Background(), httpContext(t, map[string]any{"url": peer.URL, "timeout": 0.03}), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	peer.CloseClientConnections()
	assert.Equal(t, int32(1), calls.Load(), "a timed-out request must not be replayed")

	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(30*time.Millisecond, cancel)
	defer timer.Stop()
	_, err = (&httpRequestNode{}).Execute(ctx, httpContext(t, map[string]any{"url": peer.URL}), nil)
	assert.ErrorIs(t, err, context.Canceled)
}

func (c httpTestContext) Config(v any) error { return json.Unmarshal(c.config, v) }
func (httpTestContext) Interpolate(s string, m *nodeapi.Msg) (string, error) {
	return strings.ReplaceAll(s, "{{ topic }}", m.Topic), nil
}
func (httpTestContext) Clock() nodeapi.Clock { return nodeapi.RealClock{} }

func httpContext(t *testing.T, cfg map[string]any) httpTestContext {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	return httpTestContext{config: raw}
}

// TestAgentHTTPRequest pins real HTTP methods, response formats, bounds and unreplayed failed POSTs.
func TestAgentHTTPRequest(t *testing.T) {
	calls := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/echo":
			data, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "testing", r.Header.Get("X-Test"))
			assert.JSONEq(t, `{"value":7}`, string(data))
			w.Header().Set("X-Reply", "yes")
			io.WriteString(w, `{"ok":true}`)
		case "/binary":
			w.Write([]byte{0, 1, 2})
		case "/upload":
			data, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			assert.Equal(t, []byte{0, 1, 2}, data)
			assert.Equal(t, "application/octet-stream", r.Header.Get("Content-Type"))
			w.WriteHeader(204)
		case "/large":
			io.WriteString(w, strings.Repeat("x", 65))
		case "/fail":
			w.WriteHeader(500)
		case "/empty":
			w.WriteHeader(204)
		case "/bad-json":
			io.WriteString(w, "not json")
		}
	}))
	defer peer.Close()
	for _, tc := range []struct {
		name, path, format string
		fail               string
		want               any
	}{
		{name: "JSON post", path: "/echo", format: "json", want: map[string]any{"ok": true}},
		{name: "binary", path: "/binary", format: "binary", want: "AAEC"},
		{name: "binary upload", path: "/upload", format: "auto", want: nil},
		{name: "large", path: "/large", format: "text", fail: "exceeds 64 bytes"},
		{name: "failed POST", path: "/fail", format: "auto", fail: "status 500"},
		{name: "empty", path: "/empty", format: "json", want: nil},
		{name: "invalid JSON", path: "/bad-json", format: "json", fail: "not valid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{"url": peer.URL + tc.path, "output_format": tc.format}
			if tc.path == "/echo" || tc.path == "/fail" {
				cfg["method"] = "POST"
				cfg["body_property"] = "payload"
				cfg["headers"] = map[string]string{"X-Test": "{{ topic }}"}
			}
			input := &nodeapi.Msg{Topic: "testing", Payload: map[string]any{"value": 7}, Meta: map[string]any{"original": true}}
			if tc.path == "/upload" {
				cfg["method"], cfg["body_property"], cfg["body_format"] = "POST", "payload", "binary"
				input.Payload = "AAEC"
			}
			before := calls
			out, err := (&httpRequestNode{opts: agentnode.Options{MaxOutput: 64}}).Execute(context.Background(), httpContext(t, cfg), map[string]*nodeapi.Msg{"in": input})
			assert.Equal(t, before+1, calls, "each request must reach the peer exactly once")
			if tc.fail != "" {
				assert.ErrorContains(t, err, tc.fail)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, out["out"][0].Payload)
			assert.Equal(t, true, out["out"][0].Meta["original"])
		})
	}
}

// TestAgentHTTPHealth distinguishes reachable endpoints from configured healthy responses through real peers.
func TestAgentHTTPHealth(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/ready", 302)
			return
		}
		if r.URL.Path == "/failed" {
			w.WriteHeader(500)
		}
		io.WriteString(w, "ready")
	}))
	defer peer.Close()
	for _, tc := range []struct {
		name, path, method, statuses, body string
		follow                             bool
		healthy                            bool
		fail                               string
	}{
		{name: "default 500 reachable", path: "/failed", healthy: true},
		{name: "asserted 500 unhealthy", path: "/failed", statuses: "200-299", healthy: false},
		{name: "body matched", path: "/ready", statuses: "200", body: "ready", healthy: true},
		{name: "body unmatched", path: "/ready", body: "missing", healthy: false},
		{name: "HEAD redirect observed", path: "/redirect", method: "HEAD", statuses: "302", healthy: true},
		{name: "redirect followed", path: "/redirect", statuses: "200", follow: true, healthy: true},
		{name: "body overflow", path: "/ready", body: "ready", fail: "exceeds 4 bytes"},
		{name: "HEAD cannot check body", path: "/ready", method: "HEAD", body: "ready", fail: "requires GET"},
		{name: "invalid status paired with valid ranges", path: "/ready", statuses: "600", fail: "invalid expected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limit := 64
			if tc.name == "body overflow" {
				limit = 4
			}
			cfg := map[string]any{"kind": "http", "url": peer.URL + tc.path, "method": tc.method, "expected_statuses": tc.statuses, "body_contains": tc.body, "follow_redirects": tc.follow}
			out, err := (networkProbeNode{opts: agentnode.Options{MaxOutput: limit}}).Execute(context.Background(), httpContext(t, cfg), nil)
			if tc.fail != "" {
				assert.ErrorContains(t, err, tc.fail)
				return
			}
			require.NoError(t, err)
			value := out["out"][0].Payload.(map[string]any)
			assert.Equal(t, true, value["reachable"])
			assert.Equal(t, tc.healthy, value["healthy"])
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (networkProbeNode{}).Execute(canceled, httpContext(t, map[string]any{"kind": "http", "url": peer.URL}), nil)
	assert.ErrorIs(t, err, context.Canceled)
}

// TestAgentHTTPRedirectsAndTLS keeps credentials on their origin and requires explicit trust bypass for self-signed peers.
func TestAgentHTTPRedirectsAndTLS(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.Empty(t, r.Header.Get("X-API-Key"))
		io.WriteString(w, "ok")
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer source.Close()
	response, closeResponse, err := doAgentHTTP(context.Background(), source.URL, httpPolicy{Method: "GET", Headers: map[string]string{"Authorization": "Bearer private", "X-API-Key": "private"}}, nil, 1000000000)
	require.NoError(t, err)
	closeResponse()
	assert.Equal(t, 200, response.StatusCode)
	tlsPeer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer tlsPeer.Close()
	for _, insecure := range []bool{false, true} {
		response, closeResponse, err := doAgentHTTP(context.Background(), tlsPeer.URL, httpPolicy{Method: "GET", AllowInsecureTLS: insecure}, nil, 1000000000)
		if !insecure {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		closeResponse()
		assert.Equal(t, 200, response.StatusCode)
	}
}

// TestAgentHTTPHostHeader locks a configured Host header onto the request
// line's virtual host. The header was copied into the header map, which the
// client ignores in favor of the URL's host, so a request to an address with a
// Host override reached the default site instead of the named one.
func TestAgentHTTPHostHeader(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.Host)
	}))
	defer peer.Close()
	for _, tc := range []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{name: "no override keeps the URL host", want: strings.TrimPrefix(peer.URL, "http://")},
		{name: "configured host", headers: map[string]string{"Host": "site.example"}, want: "site.example"},
		{name: "lowercase name", headers: map[string]string{"host": "other.example"}, want: "other.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{"url": peer.URL, "output_format": "text", "headers": tc.headers}
			out, err := (&httpRequestNode{}).Execute(context.Background(), httpContext(t, cfg), map[string]*nodeapi.Msg{"in": {}})
			require.NoError(t, err)
			assert.Equal(t, tc.want, out["out"][0].Payload, "The peer must see the configured virtual host.")
		})
	}
}
