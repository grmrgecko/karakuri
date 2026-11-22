package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordedRequest is what the test server saw.
type recordedRequest struct {
	method string
	path   string
	query  string
	header http.Header
	body   string
}

// httpEcho starts a test server that records the request and replies with the
// given status, content type, and body.
func httpEcho(t *testing.T, status int, contentType, body string) (*httptest.Server, *recordedRequest) {
	t.Helper()
	rec := &recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rec.method, rec.path, rec.query = r.Method, r.URL.Path, r.URL.RawQuery
		rec.header, rec.body = r.Header.Clone(), string(raw)
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// callHTTP runs the node against a config with {{url}} replaced by the server.
func callHTTP(t *testing.T, srv *httptest.Server, cfg string, in *engine.Msg) (*engine.Msg, error) {
	t.Helper()
	cfg = strings.ReplaceAll(cfg, "{{url}}", srv.URL)
	out, err := HTTPRequestNode{}.Execute(context.Background(), newTestCtx(cfg, nil),
		map[string]*engine.Msg{"in": in})
	if err != nil {
		return nil, err
	}
	return only(out, "out"), nil
}

// TestHTTPRequestParsesJSONResponse covers the ordinary call against a real
// HTTP server. The status lands in metadata rather than the payload, so a flow
// can branch on it without the response body having to make room for it, and
// the recorded request proves the method and path the node actually sent.
func TestHTTPRequestParsesJSONResponse(t *testing.T) {
	srv, rec := httpEcho(t, 200, "application/json", `{"state":"on","level":42}`)

	got, err := callHTTP(t, srv, `{"method":"GET","url":"{{url}}/api/state"}`, &engine.Msg{})
	require.NoError(t, err, "a 200 with a JSON body must succeed")

	payload, ok := got.Payload.(map[string]any)
	require.Truef(t, ok, "payload is %T, want the body decoded as an object", got.Payload)
	assert.Equal(t, "on", payload["state"], "the decoded body is what the flow acts on")
	assert.Equal(t, float64(42), payload["level"], "numbers decode as float64 like everything else in the engine")

	assert.Equal(t, 200, got.Meta["status"],
		"the status belongs in metadata, so a flow can branch on it without the body making room")
	assert.Equal(t, "GET", rec.method, "the configured method is what reaches the server")
	assert.Equal(t, "/api/state", rec.path, "the configured path is what reaches the server")
}

// TestHTTPRequestReadsATextEndpoint covers an endpoint that answers plain text.
// The format is declared on the node rather than sniffed from the response, so
// a server that mislabels its content type cannot change what the port carries.
func TestHTTPRequestReadsATextEndpoint(t *testing.T) {
	srv, _ := httpEcho(t, 200, "text/plain", "just text")

	got, err := callHTTP(t, srv, `{"url":"{{url}}/","response_format":"text"}`, &engine.Msg{})
	require.NoError(t, err, "a text endpoint declared as text must succeed")
	assert.Equal(t, "just text", got.Payload, "the body arrives as the text it is")
}

// TestHTTPRequestResponseFormats is the sharpest statement of the
// declared-not-sniffed rule: one server answering with Content-Type
// application/json, read three different ways purely because the node said so.
// A node that sniffed would return an object every time and the format setting
// would be decoration.
func TestHTTPRequestResponseFormats(t *testing.T) {
	srv, _ := httpEcho(t, 200, "application/json", `{"a":1}`)

	got, err := callHTTP(t, srv, `{"url":"{{url}}/","response_format":"text"}`, &engine.Msg{})
	require.NoError(t, err, "reading a JSON endpoint as text must succeed")
	assert.Equal(t, `{"a":1}`, got.Payload,
		"the declared format wins over the server's content type, so this stays text")

	got, err = callHTTP(t, srv, `{"url":"{{url}}/","response_format":"binary"}`, &engine.Msg{})
	require.NoError(t, err, "reading the same endpoint as binary must succeed")
	raw, ok := got.Payload.([]byte)
	require.Truef(t, ok, "binary payload is %#v, want real bytes", got.Payload)
	assert.Equal(t, []byte(`{"a":1}`), raw,
		"binary hands the bytes over untouched, for a base64 node or a file write to deal with")
}

// TestHTTPRequestInterpolatesURLQueryAndHeaders covers building a request from
// the message. All three places interpolate, which is what lets one node call a
// per-device endpoint with a token the flow fetched, rather than needing a node
// per device.
func TestHTTPRequestInterpolatesURLQueryAndHeaders(t *testing.T) {
	srv, rec := httpEcho(t, 200, "application/json", `{}`)
	in := &engine.Msg{Payload: map[string]any{"id": "kitchen", "token": "s3cret"}}

	_, err := callHTTP(t, srv, `{
		"url":"{{url}}/api/{{ payload.id }}",
		"query":{"mode":"live","for":"{{ payload.id }}"},
		"headers":{"X-Token":"{{ payload.token }}"}
	}`, in)
	require.NoError(t, err, "a request built from the message must be sendable")

	assert.Equal(t, "/api/kitchen", rec.path, "the path must be interpolated from the payload")
	assert.Contains(t, rec.query, "for=kitchen", "an interpolated query parameter must reach the server")
	assert.Contains(t, rec.query, "mode=live", "a literal query parameter must survive alongside it")
	assert.Equal(t, "s3cret", rec.header.Get("X-Token"),
		"a header must interpolate too, which is how a token fetched by the flow is sent")
}

// TestHTTPRequestBodySources covers the three ways a request body is built,
// each with the content type that goes with it. The content type is the part
// that matters operationally: a server told the wrong one rejects the request,
// so each source has to set the header that matches what it actually sent.
func TestHTTPRequestBodySources(t *testing.T) {
	srv, rec := httpEcho(t, 200, "application/json", `{}`)
	in := &engine.Msg{Payload: map[string]any{"state": "on"}}

	// The message payload as JSON.
	_, err := callHTTP(t, srv, `{"method":"POST","url":"{{url}}/","body_source":"payload"}`, in)
	require.NoError(t, err, "posting the payload must succeed")

	var decoded map[string]any
	require.NoErrorf(t, json.Unmarshal([]byte(rec.body), &decoded), "the sent body must be JSON: %q", rec.body)
	assert.Equal(t, "on", decoded["state"], "the payload is what was sent")
	assert.Equal(t, "application/json", rec.header.Get("Content-Type"),
		"a JSON body must be announced as JSON, or the server rejects it")

	// A template body.
	_, err = callHTTP(t, srv,
		`{"method":"PUT","url":"{{url}}/","body_source":"template",
		  "body":"state={{ payload.state }}","content_type":"text/plain"}`, in)
	require.NoError(t, err, "a templated body must succeed")
	assert.Equal(t, "state=on", rec.body, "the template must be rendered against the message")
	assert.Equal(t, "text/plain", rec.header.Get("Content-Type"),
		"a template body carries whatever content type the author declared")

	// Form encoding.
	_, err = callHTTP(t, srv,
		`{"method":"POST","url":"{{url}}/","body_source":"form",
		  "form":{"state":"{{ payload.state }}"}}`, in)
	require.NoError(t, err, "a form body must succeed")
	assert.Equal(t, "state=on", rec.body, "form fields interpolate like everything else")
	assert.Equal(t, "application/x-www-form-urlencoded", rec.header.Get("Content-Type"),
		"a form body must announce form encoding, or the server parses it as something else")
}

// TestHTTPRequestAcceptsScalarFieldValues locks that the query, header, and
// form objects take numbers and booleans as well as text. The fields are JSON
// objects of name to value, and a page size written as 50 rather than "50"
// failed every run with a config decoding error that named a Go type. A value
// with no text form, such as a nested object, is still refused, by name.
func TestHTTPRequestAcceptsScalarFieldValues(t *testing.T) {
	srv, rec := httpEcho(t, 200, "application/json", `{}`)

	_, err := callHTTP(t, srv, `{
		"method":"POST","url":"{{url}}/",
		"query":{"limit":50,"active":true,"big":1000000,"name":"kitchen"},
		"headers":{"X-Retries":3},
		"body_source":"form","form":{"level":21.5,"on":false}
	}`, &engine.Msg{})
	require.NoError(t, err, "numbers and booleans in the field objects must be sent as their text")

	sent, err := url.ParseQuery(rec.query)
	require.NoError(t, err, "the server must receive a well-formed query")
	assert.Equal(t, "50", sent.Get("limit"), "a numeric query value is sent as its digits")
	assert.Equal(t, "true", sent.Get("active"), "a boolean query value is sent as true or false")
	assert.Equal(t, "1000000", sent.Get("big"), "a large number is sent without an exponent")
	assert.Equal(t, "kitchen", sent.Get("name"), "a text value beside them is unchanged")
	assert.Equal(t, "3", rec.header.Get("X-Retries"), "a numeric header value is sent as its digits")
	assert.Equal(t, "level=21.5&on=false", rec.body, "numeric and boolean form fields are sent as their text")

	_, err = callHTTP(t, srv, `{"url":"{{url}}/","query":{"filter":{"room":"kitchen"}}}`, &engine.Msg{})
	require.Error(t, err, "a nested object has no query text, so the node must refuse it")
	assert.Contains(t, err.Error(), "filter", "the refusal must name the field value at fault")
}

// TestHTTPRequestSendsBinaryPayloadAsBytes locks that a binary payload is the
// request body as it stands. Uploading a file read as binary is the case: the
// JSON encoder turned the bytes into a quoted base64 string, which no server
// expecting the file accepts.
func TestHTTPRequestSendsBinaryPayloadAsBytes(t *testing.T) {
	srv, rec := httpEcho(t, 200, "application/json", `{}`)
	_, err := callHTTP(t, srv, `{"method":"PUT","url":"{{url}}/","body_source":"payload"}`,
		&engine.Msg{Payload: []byte{0x89, 'P', 'N', 'G', 0x00, 0xff}})
	require.NoError(t, err, "posting a binary payload must succeed")

	assert.Equal(t, []byte{0x89, 'P', 'N', 'G', 0x00, 0xff}, []byte(rec.body),
		"the bytes are sent as they are, not as base64 text")
	assert.Equal(t, "application/octet-stream", rec.header.Get("Content-Type"),
		"bytes must not be announced as JSON")
}

// TestHTTPRequestGETSendsNoBody pins a fact about the world rather than about
// this code: many servers and proxies reject or ignore a GET carrying a body,
// so a node configured with a body source must still send none on a GET.
func TestHTTPRequestGETSendsNoBody(t *testing.T) {
	srv, rec := httpEcho(t, 200, "application/json", `{}`)
	in := &engine.Msg{Payload: map[string]any{"state": "on"}}

	_, err := callHTTP(t, srv, `{"method":"GET","url":"{{url}}/","body_source":"payload"}`, in)
	require.NoError(t, err, "a GET with a body source configured must still send")
	assert.Empty(t, rec.body,
		"a GET must send no body whatever the config says: many servers and proxies reject one that does")
}

// TestHTTPRequestAuthModes pins the two authorization headers against their
// real wire formats. Basic is decoded with the standard library's own parser
// rather than compared as a string, so the assertion is that a real server
// would read back the credentials that were configured.
func TestHTTPRequestAuthModes(t *testing.T) {
	srv, rec := httpEcho(t, 200, "application/json", `{}`)

	_, err := callHTTP(t, srv,
		`{"url":"{{url}}/","auth":"basic","username":"user","password":"pass"}`, &engine.Msg{})
	require.NoError(t, err, "a basic-auth request must send")

	user, pass, ok := parseBasic(rec.header.Get("Authorization"))
	require.Truef(t, ok, "the header must parse as Basic: %q", rec.header.Get("Authorization"))
	assert.Equal(t, "user", user, "a real server must read back the configured username")
	assert.Equal(t, "pass", pass, "a real server must read back the configured password")

	_, err = callHTTP(t, srv, `{"url":"{{url}}/","auth":"bearer","token":"abc123"}`, &engine.Msg{})
	require.NoError(t, err, "a bearer-auth request must send")
	assert.Equal(t, "Bearer abc123", rec.header.Get("Authorization"),
		"the scheme name and spacing are the wire format every server parses")
}

// parseBasic decodes a Basic authorization header.
func parseBasic(header string) (string, string, bool) {
	req := &http.Request{Header: http.Header{"Authorization": []string{header}}}
	return req.BasicAuth()
}

// TestHTTPRequestFailsOnErrorStatusByDefault covers the default that makes this
// node fit a reliability-focused engine: a 5xx fails the node, so the retry
// policy and the failure branch apply to it. Turning the check off is for a
// flow that wants to inspect the error response itself, and then the status and
// body have to actually arrive.
func TestHTTPRequestFailsOnErrorStatusByDefault(t *testing.T) {
	srv, _ := httpEcho(t, 503, "text/plain", "unavailable")

	_, err := callHTTP(t, srv, `{"url":"{{url}}/","response_format":"text"}`, &engine.Msg{})
	assert.Error(t, err, "a 503 must fail the node by default, so retries and the failure branch apply")

	got, err := callHTTP(t, srv,
		`{"url":"{{url}}/","response_format":"text","fail_on_error_status":false}`, &engine.Msg{})
	require.NoError(t, err, "with the check off, an error status is information rather than a failure")
	assert.Equal(t, 503, got.Meta["status"], "the status must be reported, or the flow cannot tell it failed")
	assert.Equal(t, "unavailable", got.Payload,
		"the error body must arrive too: inspecting it is the reason to turn the check off")
}

// TestHTTPRequestFollowsAndStopsAtRedirects covers redirect handling against
// two real servers. Following by default is what makes an ordinary API call
// work; turning it off is how a flow inspects the redirect itself, and then the
// 302 has to come back rather than being followed anyway.
func TestHTTPRequestFollowsAndStopsAtRedirects(t *testing.T) {
	target, _ := httpEcho(t, 200, "application/json", `{"arrived":true}`)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	got, err := callHTTP(t, redirector, `{"url":"{{url}}/"}`, &engine.Msg{})
	require.NoError(t, err, "a redirect must be followed by default")

	payload, ok := got.Payload.(map[string]any)
	require.Truef(t, ok, "payload = %v, want the redirect target's body", got.Payload)
	assert.Equal(t, true, payload["arrived"], "the body must come from where the redirect pointed")

	// With redirects off the 302 itself comes back, whose body is the HTML the
	// standard library writes, so it is read as text.
	got, err = callHTTP(t, redirector,
		`{"url":"{{url}}/","response_format":"text","follow_redirects":false,"fail_on_error_status":false}`,
		&engine.Msg{})
	require.NoError(t, err, "with following off the redirect response is the result, not a failure")
	assert.Equal(t, 302, got.Meta["status"],
		"the redirect itself must come back, or turning following off did nothing")
}

// TestHTTPRequestRejectsBadConfig covers the requests the node refuses before
// and during the call, against the many working calls above. Every one has to
// fail the node rather than emit an empty message, because the failure branch
// and the retry policy are the whole reason a flow uses this node instead of a
// script.
func TestHTTPRequestRejectsBadConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		why  string
	}{
		{name: "no URL", cfg: `{"method":"GET"}`, why: "there is nothing to call"},
		{
			name: "unsupported scheme", cfg: `{"url":"ftp://example.invalid/file"}`,
			why: "the node speaks HTTP, and pretending otherwise would fail confusingly later",
		},
		{
			name: "unparsable timeout", cfg: `{"url":"http://example.invalid","timeout":"soon"}`,
			why: "reading it as zero would mean either no timeout or an instant one, and neither is what was asked",
		},
		{name: "unknown body source", cfg: `{"method":"POST","url":"http://example.invalid","body_source":"telepathy"}`},
		{
			// Port 0 is never listening, so this fails at connect time.
			name: "unreachable host", cfg: `{"url":"http://127.0.0.1:0/","timeout":"2s"}`,
			why: "a connection failure is what a retry policy exists for, so it must reach the engine as a failure",
		},
	}
	for _, c := range cases {
		_, err := HTTPRequestNode{}.Execute(context.Background(), newTestCtx(c.cfg, nil),
			map[string]*engine.Msg{"in": {}})
		assert.Errorf(t, err, "%s must fail the node rather than emit an empty message: %s", c.name, c.why)
	}
}

// TestHTTPRequestCarriesGroupAndTopicThrough covers what survives a request.
// The group frame is the one that matters: a call inside a fan-out must keep
// its frame, or the join downstream never sees a complete set and the run hangs
// with no error to point at.
func TestHTTPRequestCarriesGroupAndTopicThrough(t *testing.T) {
	srv, _ := httpEcho(t, 200, "application/json", `{}`)
	in := &engine.Msg{
		Topic: "device/1",
		Group: []engine.GroupFrame{{ID: "g", Index: 2, Count: 5}},
		Meta:  map[string]any{"source": "test"},
	}

	got, err := callHTTP(t, srv, `{"url":"{{url}}/"}`, in)
	require.NoError(t, err, "the request must succeed")

	assert.Equal(t, "device/1", got.Topic, "the topic must survive, or a per-device flow loses which device it is on")
	require.Lenf(t, got.Group, 1, "the fan-out frame must survive: %+v", got.Group)
	assert.Equal(t, 2, got.Group[0].Index,
		"the frame's index must be unchanged, or the join downstream never sees a complete set")
	assert.Equal(t, "test", got.Meta["source"], "existing metadata must be kept, not replaced by the response's")
	assert.NotContains(t, in.Meta, "status",
		"the incoming message must not be written through: run history and other branches still hold it")
}

// TestDecodeHTTPBodyHonorsTheDeclaredFormat covers the decode step directly,
// including the two shapes only a real endpoint produces: an unset format on a
// config saved before the setting existed, and an empty body from a 204 or a
// HEAD. An empty body is a nil payload rather than an error, because there was
// no document to parse in the first place.
func TestDecodeHTTPBodyHonorsTheDeclaredFormat(t *testing.T) {
	// An unset format reads as JSON, which is what a config saved before the
	// setting existed meant.
	got, err := decodeHTTPBody([]byte(`{"a":1}`), "")
	require.NoError(t, err, "an unset format must read as JSON so existing flows keep working")
	obj, ok := got.(map[string]any)
	require.Truef(t, ok, "decoded %#v, want an object", got)
	assert.Equal(t, float64(1), obj["a"], "the body must be decoded, not handed over as text")

	got, err = decodeHTTPBody(nil, "json")
	require.NoError(t, err, "a 204 or a HEAD reply carries no document, which is not a parse failure")
	assert.Nil(t, got, "an empty body is a nil payload rather than an empty object")

	_, err = decodeHTTPBody([]byte("not json"), "json")
	assert.Error(t, err,
		"a body that is not what the format promised must fail, not arrive as a string on a port that said object")

	got, err = decodeHTTPBody([]byte(`{"a":1}`), "text")
	require.NoError(t, err, "declaring text must accept any body")
	assert.Equal(t, `{"a":1}`, got, "text takes the body as it is, whatever the server called it")
}

// TestHTTPRequestHeadersAreReachableByPath locks that the response headers in
// metadata are an object a dot path can walk. Held as a typed string map they
// read as absent to every node that resolves a property, so a switch on
// meta.headers.X-Rate-Remaining never matched in the run that made the request.
func TestHTTPRequestHeadersAreReachableByPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Rate-Remaining", "7")
		fmt.Fprint(w, `{}`)
	}))
	t.Cleanup(srv.Close)

	got, err := callHTTP(t, srv, `{"url":"{{url}}/"}`, &engine.Msg{})
	require.NoError(t, err, "a 200 with a JSON body must succeed")

	value, found := resolveProperty(got, "meta.headers.X-Rate-Remaining")
	require.True(t, found, "a response header must resolve by its dot path like any other metadata")
	assert.Equal(t, "7", value, "the header carries the value the server sent")
}
