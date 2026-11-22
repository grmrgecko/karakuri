package http

import (
	"encoding/json"
	"io"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSyncWebhookReturnsFlowResponse covers the synchronous path over a real
// HTTP connection, which is the only way to know the handler actually holds the
// request open. The reply is composed by the run rather than the route — status,
// headers, and body all come from the flow — so an integration that expects a
// particular API shape can be built entirely in the editor.
func TestSyncWebhookReturnsFlowResponse(t *testing.T) {
	reg := engine.NewWebhookRegistry(nil)
	unregister := reg.Register(engine.WebhookRegistration{
		Token:           "sync-hook",
		Methods:         "POST",
		Synchronous:     true,
		ResponseTimeout: 5 * time.Second,
		Emit: func(m *engine.Msg) {
			// Stand in for a run returning to its Webhook node.
			requestID, _ := m.Meta[engine.RequestIDMeta].(string)
			if !assert.NotEmpty(t, requestID, "the waiting request must be identified, or the run cannot answer it") {
				return
			}
			payload, _ := m.Payload.(map[string]any)
			go engine.Responses().Respond(requestID, &engine.HTTPResponse{
				Status:  201,
				Headers: map[string]string{"Content-Type": "application/json", "X-Flow": "yes"},
				Body:    `{"echo":` + jsonNumber(payload["value"]) + `}`,
			})
		},
	})
	defer unregister()

	srv := newHookServer(reg)
	defer srv.Close()

	resp, err := nethttp.Post(srv.URL+"/hook/sync-hook", "application/json",
		strings.NewReader(`{"value":42}`))
	require.NoError(t, err, "the request must be answered rather than dropped")
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, 201, resp.StatusCode,
		"the status comes from the run, not the route: an asynchronous hook would have answered 202")
	assert.Equal(t, "yes", resp.Header.Get("X-Flow"), "headers the run set must reach the caller")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "the reply body must be readable")

	var doc map[string]any
	require.NoErrorf(t, json.Unmarshal(body, &doc), "the run's body must arrive intact: %q", body)
	assert.Equal(t, float64(42), doc["echo"],
		"the run read the request body and composed the reply from it, which is the whole point of the mode")
}

// TestSyncWebhookTimesOut covers a run that never answers — a graph with no
// return input, or one that failed before reaching it. The handler gives up
// and returns 504 rather than holding the connection, because a caller left
// hanging is worse than a caller told the gateway timed out, and every held
// request costs a connection.
func TestSyncWebhookTimesOut(t *testing.T) {
	reg := engine.NewWebhookRegistry(nil)
	unregister := reg.Register(engine.WebhookRegistration{
		Token:           "silent-hook",
		Methods:         "POST",
		Synchronous:     true,
		ResponseTimeout: 200 * time.Millisecond,
		Emit:            func(*engine.Msg) {},
	})
	defer unregister()

	srv := newHookServer(reg)
	defer srv.Close()

	resp, err := nethttp.Post(srv.URL+"/hook/silent-hook", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err, "the request must be answered rather than held forever")
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, nethttp.StatusGatewayTimeout, resp.StatusCode,
		"a run that never answers must time out: a held connection costs more than a 504")
}

// TestWebhookMetaDescribesRequest covers the request detail a script reads off
// the message. Headers are lowercased so a rule need not guess the casing a
// client sent, the raw body is kept alongside the parsed payload for a
// signature check, and the Authorization header is withheld — every message is
// written to run history, so a credential that reached the metadata would be
// stored in the database and rendered in the debug panel.
func TestWebhookMetaDescribesRequest(t *testing.T) {
	reg := engine.NewWebhookRegistry(nil)
	fired := make(chan *engine.Msg, 1)
	unregister := reg.Register(engine.WebhookRegistration{
		Token: "meta-hook", Methods: "POST",
		Emit: func(m *engine.Msg) { fired <- m },
	})
	defer unregister()

	srv := newHookServer(reg)
	defer srv.Close()

	req, err := nethttp.NewRequest(nethttp.MethodPost,
		srv.URL+"/hook/meta-hook?mode=test", strings.NewReader(`{"value":1}`))
	require.NoError(t, err, "the test request must build")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Custom", "abc")
	req.Header.Set("Authorization", "Bearer super-secret")

	resp, err := nethttp.DefaultClient.Do(req)
	require.NoError(t, err, "the request must reach the server")
	_ = resp.Body.Close()

	select {
	case msg := <-fired:
		query, ok := msg.Meta["query"].(map[string]any)
		require.Truef(t, ok, "the query string must be exposed as an object, got %T", msg.Meta["query"])
		assert.Equal(t, "test", query["mode"], "query parameters must reach the flow even when the body is the payload")

		headers, ok := msg.Meta["headers"].(map[string]any)
		require.Truef(t, ok, "headers must be exposed as an object, got %T", msg.Meta["headers"])
		assert.Equal(t, "abc", headers["x-custom"],
			"header names are lowercased, so a rule need not guess the casing a client sent")
		assert.NotContains(t, headers, "authorization",
			"every message is written to run history, so a credential here would be stored and shown in the debug panel")

		assert.Equal(t, `{"value":1}`, msg.Meta["body"],
			"the raw body is kept beside the parsed payload, which is what a signature check has to hash")
		assert.NotEmpty(t, msg.Meta[engine.RequestIDMeta],
			"the request id is how a synchronous run finds the caller to answer")
	case <-time.After(2 * time.Second):
		require.FailNow(t, "the webhook never started a run")
	}
}

// jsonNumber renders a decoded JSON number back into source form.
func jsonNumber(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(data)
}
