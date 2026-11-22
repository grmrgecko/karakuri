package core

import (
	"context"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func respondMsg(requestID string, payload any) *engine.Msg {
	return &engine.Msg{Payload: payload, Meta: map[string]any{engine.RequestIDMeta: requestID}}
}

// TestWebhookReturnAnswersWaitingRequest covers the callback half of a
// synchronous webhook, including the run-variable overrides a script can set.
func TestWebhookReturnAnswersWaitingRequest(t *testing.T) {
	ec := newTestCtx(`{"response_mode":"sync","response_status":201,"response_body_source":"payload","response_content_type":"application/json","response_headers":{"X-Config":"kept"}}`, nil)
	ec.SetVar(engine.VarResponseCode, float64(202))
	ec.SetVar(engine.VarResponseHeaders, map[string]any{"X-Var": "added"})
	responses, release := engine.Responses().Register("req-return")
	defer release()

	out, err := WebhookNode{}.Execute(context.Background(), ec,
		map[string]*engine.Msg{"response": respondMsg("req-return", map[string]any{"ok": true})})
	require.NoError(t, err, "returning to a synchronous webhook must answer its waiting request")
	assert.Empty(t, out, "the returned message closes the request branch")

	select {
	case resp := <-responses:
		assert.Equal(t, 202, resp.Status, "the response_code variable must override the configured status")
		assert.JSONEq(t, `{"ok":true}`, resp.Body, "the returned payload becomes the response body")
		assert.Equal(t, "application/json", resp.Headers["Content-Type"])
		assert.Equal(t, "kept", resp.Headers["X-Config"])
		assert.Equal(t, "added", resp.Headers["X-Var"])
	case <-time.After(time.Second):
		require.FailNow(t, "the webhook did not answer the waiting request")
	}
}

// TestWebhookReturnWithoutRequestIsTestable covers editor test runs, where no
// HTTP handler is waiting but the return path should still finish cleanly.
func TestWebhookReturnWithoutRequestIsTestable(t *testing.T) {
	ec := newTestCtx(`{"response_mode":"sync"}`, nil)
	out, err := WebhookNode{}.Execute(context.Background(), ec,
		map[string]*engine.Msg{"response": {Payload: "data"}})
	require.NoError(t, err, "a test return without a live request must not fail")
	assert.Empty(t, out)
	assert.Len(t, ec.debugs, 1, "the run history must explain why no HTTP response was sent")
}

// TestWebhookReturnSendsBinaryPayloadAsBytes locks that a binary payload is
// answered as the bytes it holds. Encoding it as JSON answered the caller with
// a quoted base64 string, so a flow returning an image under image/jpeg sent a
// body no client could open.
func TestWebhookReturnSendsBinaryPayloadAsBytes(t *testing.T) {
	ec := newTestCtx(`{"response_mode":"sync","response_content_type":"image/jpeg"}`, nil)
	responses, release := engine.Responses().Register("req-binary")
	defer release()

	image := []byte{0xff, 0xd8, 0xff, 0xe0}
	_, err := WebhookNode{}.Execute(context.Background(), ec,
		map[string]*engine.Msg{"response": respondMsg("req-binary", image)})
	require.NoError(t, err, "returning a binary payload to a synchronous webhook must answer its waiting request")

	select {
	case resp := <-responses:
		assert.Equal(t, image, []byte(resp.Body), "the response body must be the payload's bytes, not their base64 text")
	case <-time.After(time.Second):
		require.FailNow(t, "the webhook did not answer the waiting request")
	}
}

// TestWebhookReturnReadsConfiguredHeadersAsWritten locks two things about the
// response headers object. A numeric or boolean value is sent as its text: it
// used to fail the whole config, so the trigger never armed. A name written
// in another case still names the same header, so a "content-type" entry
// replaces the configured content type instead of travelling beside it and
// leaving the reply's type to map order.
func TestWebhookReturnReadsConfiguredHeadersAsWritten(t *testing.T) {
	ec := newTestCtx(`{"response_mode":"sync","response_content_type":"application/json",
		"response_headers":{"content-type":"text/html","X-Count":3,"X-Plain":"kept"}}`, nil)
	responses, release := engine.Responses().Register("req-headers")
	defer release()

	_, err := WebhookNode{}.Execute(context.Background(), ec,
		map[string]*engine.Msg{"response": respondMsg("req-headers", "<p>ok</p>")})
	require.NoError(t, err, "a numeric header value must not fail the node's configuration")

	select {
	case resp := <-responses:
		assert.Equal(t, "3", resp.Headers["X-Count"], "a numeric header value is sent as its digits")
		assert.Equal(t, "kept", resp.Headers["X-Plain"], "a text header beside it is unchanged")
		assert.Equal(t, "text/html", resp.Headers["Content-Type"],
			"a content-type header written in lower case must replace the configured content type")
		assert.NotContains(t, resp.Headers, "content-type",
			"the same header must not be sent twice under two spellings")
	case <-time.After(time.Second):
		require.FailNow(t, "the webhook did not answer the waiting request")
	}
}
