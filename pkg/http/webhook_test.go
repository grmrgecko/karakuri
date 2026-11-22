package http

import (
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newHookServer builds a test server exposing /hook/{token} over a fresh
// registry, through the production route registration.
func newHookServer(reg *engine.WebhookRegistry) *httptest.Server {
	r := chi.NewRouter()
	mountWebhooks(r, reg)
	return httptest.NewServer(r)
}

// TestWebhookRouteTakesEveryConfiguredMethod locks the route to the trigger's
// own method list. The Webhook node offers PUT, PATCH, DELETE, HEAD, and any
// method, but the route was registered for GET and POST only, so a trigger
// set to PUT never fired: the request was answered before the registry was
// asked. A method the trigger does not list is still refused with 405.
func TestWebhookRouteTakesEveryConfiguredMethod(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		method     string
		want       int
		fires      bool
	}{
		{"a listed PUT", "PUT,PATCH", nethttp.MethodPut, nethttp.StatusAccepted, true},
		{"a listed PATCH", "PUT,PATCH", nethttp.MethodPatch, nethttp.StatusAccepted, true},
		{"a listed DELETE", "DELETE", nethttp.MethodDelete, nethttp.StatusAccepted, true},
		{"a listed HEAD", "HEAD", nethttp.MethodHead, nethttp.StatusAccepted, true},
		{"any method", "ANY", nethttp.MethodPut, nethttp.StatusAccepted, true},
		{"a method the trigger does not list", "PUT,PATCH", nethttp.MethodDelete, nethttp.StatusMethodNotAllowed, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := engine.NewWebhookRegistry(nil)
			fired := make(chan *engine.Msg, 1)
			unregister := reg.Register(engine.WebhookRegistration{
				Token: "m", Methods: tc.configured, BodyFormat: "text",
				Emit: func(m *engine.Msg) { fired <- m },
			})
			defer unregister()
			srv := newHookServer(reg)
			defer srv.Close()

			req, err := nethttp.NewRequest(tc.method, srv.URL+"/hook/m", nil)
			require.NoError(t, err, "the request must build")
			resp, err := nethttp.DefaultClient.Do(req)
			require.NoError(t, err, "the hook route must answer")
			_ = resp.Body.Close()

			assert.Equal(t, tc.want, resp.StatusCode, "the trigger's method list decides, not the route")
			select {
			case msg := <-fired:
				require.True(t, tc.fires, "a refused method must not start a run")
				assert.Equal(t, tc.method, msg.Meta["method"], "the run is told which method fired it")
			default:
				assert.False(t, tc.fires, "an accepted method must start a run")
			}
		})
	}
}

// TestWebhookRouteFiresRun drives /hook/{token} over a real HTTP server with
// the same route registration production uses. The reply is 202 rather than 200
// because the run has not finished — the request only started it — and the
// message carries the token as its topic and the request's own metadata, which
// is what a rule downstream branches on.
func TestWebhookRouteFiresRun(t *testing.T) {
	reg := engine.NewWebhookRegistry(nil)
	fired := make(chan *engine.Msg, 1)
	unregister := reg.Register(engine.WebhookRegistration{Token: "e2e-hook", Methods: "POST", Emit: func(m *engine.Msg) { fired <- m }})
	defer unregister()

	srv := newHookServer(reg)
	defer srv.Close()

	resp, err := nethttp.Post(srv.URL+"/hook/e2e-hook", "application/json", strings.NewReader(`{"value":42}`))
	require.NoError(t, err, "the hook route must accept the request")
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, nethttp.StatusAccepted, resp.StatusCode,
		"an asynchronous hook answers 202: the run was started, not completed")

	var body map[string]map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body), "the reply must be JSON like every other response")
	assert.Equal(t, "accepted", body["data"]["status"], "the caller is told the request was taken, nothing more")

	select {
	case msg := <-fired:
		payload, ok := msg.Payload.(map[string]any)
		require.Truef(t, ok, "the request body must be decoded into the payload, got %T", msg.Payload)
		assert.Equal(t, float64(42), payload["value"], "the body is what the flow acts on")
		assert.Equal(t, "e2e-hook", msg.Topic, "the topic is the token, so a shared handler can tell hooks apart")
		assert.Equal(t, "POST", msg.Meta["method"], "the method belongs in metadata for a rule to branch on")
		assert.Equal(t, "application/json", msg.Meta["content_type"], "the content type tells a script how the body was sent")
	default:
		require.FailNow(t, "the route answered but never started a run")
	}
}

// TestWebhookRouteGETQueryParams covers the query body format, which is how a
// device that can only issue a GET sends data. The format is declared on the
// registration rather than guessed from the method, and the values arrive as
// the strings a query string carries — nothing tries to read "1" as a number.
func TestWebhookRouteGETQueryParams(t *testing.T) {
	reg := engine.NewWebhookRegistry(nil)
	fired := make(chan *engine.Msg, 1)
	reg.Register(engine.WebhookRegistration{
		Token: "g", Methods: "GET", BodyFormat: "query",
		Emit: func(m *engine.Msg) { fired <- m },
	})

	srv := newHookServer(reg)
	defer srv.Close()

	resp, err := nethttp.Get(srv.URL + "/hook/g?a=1&b=x")
	require.NoError(t, err, "the hook route must accept the request")
	_ = resp.Body.Close()
	require.Equal(t, nethttp.StatusAccepted, resp.StatusCode, "the run was started")

	msg := <-fired
	payload, ok := msg.Payload.(map[string]any)
	require.Truef(t, ok, "the query string must be decoded into an object, got %T", msg.Payload)
	assert.Equal(t, map[string]any{"a": "1", "b": "x"}, payload,
		"query values arrive as the strings they are: a source names its format, it does not guess types")
}

// TestWebhookRouteUnknownToken covers a token nothing has armed. It answers 404
// rather than an error mentioning webhooks, so the endpoint reveals nothing
// about which tokens exist to someone guessing at them.
func TestWebhookRouteUnknownToken(t *testing.T) {
	srv := newHookServer(engine.NewWebhookRegistry(nil))
	defer srv.Close()

	resp, err := nethttp.Post(srv.URL+"/hook/nope", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err, "the request must reach the server")
	_ = resp.Body.Close()

	assert.Equal(t, nethttp.StatusNotFound, resp.StatusCode,
		"an unarmed token is indistinguishable from no route at all, so nothing is revealed to someone guessing")
}

// TestWebhookRouteMethodNotAllowed covers an armed token called with a method
// it did not register. It answers 405 rather than 404 — the token exists, the
// method is wrong, and the difference is what tells a caller to fix their
// request rather than their URL. The emit callback fails the test if it runs,
// since the point is that no run is started at all.
func TestWebhookRouteMethodNotAllowed(t *testing.T) {
	reg := engine.NewWebhookRegistry(nil)
	reg.Register(engine.WebhookRegistration{Token: "post-only", Methods: "POST",
		Emit: func(*engine.Msg) {
			assert.Fail(t, "a disallowed method must not start a run")
		}})

	srv := newHookServer(reg)
	defer srv.Close()

	resp, err := nethttp.Get(srv.URL + "/hook/post-only")
	require.NoError(t, err, "the request must reach the server")
	_ = resp.Body.Close()

	assert.Equal(t, nethttp.StatusMethodNotAllowed, resp.StatusCode,
		"the token is armed but not for GET, which is a 405 rather than a 404")
}
