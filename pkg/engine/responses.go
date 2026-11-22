package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// RequestIDMeta is the message metadata key carrying the pending HTTP request
// a run may answer. Nodes read it to find the response slot to fill.
const RequestIDMeta = "request_id"

// Reserved run variables that shape the HTTP reply to a synchronous webhook.
// A script sets them like any other variable; the Webhook return input and the
// end-of-run responder both read them, so the same names work whether the
// flow answers early or lets the run finish.
const (
	VarResponseCode    = "response_code"
	VarResponseBody    = "response_body"
	VarResponseHeaders = "response_headers"
)

// HTTPResponse is the reply a flow produces for a synchronous webhook.
type HTTPResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body"`
}

// ResponseFromVars overlays a run's response variables on a base response.
// Variables win over the base where they are set, so a node's configuration
// supplies the defaults and a script overrides only what it cares about.
func ResponseFromVars(vars map[string]any, base *HTTPResponse) *HTTPResponse {
	out := &HTTPResponse{Status: base.Status, Body: base.Body}
	out.Headers = make(map[string]string, len(base.Headers))
	for name, value := range base.Headers {
		out.Headers[http.CanonicalHeaderKey(name)] = value
	}

	if code, ok := varInt(vars[VarResponseCode]); ok {
		out.Status = code
	}
	if body, ok := vars[VarResponseBody]; ok && body != nil {
		out.Body = VarBody(body)
	}
	// Header values are stringified individually: a script writing a number or
	// bool into the map should not lose the whole header set. Names are
	// canonicalized so a header written in another case replaces the base
	// one instead of sitting beside it.
	if headers, ok := vars[VarResponseHeaders].(map[string]any); ok {
		for name, value := range headers {
			out.Headers[http.CanonicalHeaderKey(name)] = VarBody(value)
		}
	}
	return out
}

// RunCompletionResponse builds the reply for a synchronous webhook whose run
// ended without answering: the run's response variables over a default that
// reports the run's own outcome.
func RunCompletionResponse(runStatus string, vars map[string]any) *HTTPResponse {
	status := 200
	if runStatus != RunStatusSucceeded {
		status = 500
	}
	return ResponseFromVars(vars, &HTTPResponse{
		Status:  status,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    fmt.Sprintf(`{"status":%q}`, runStatus),
	})
}

// VarBody renders a variable as a response body or header value: strings pass
// through untouched so a script can return HTML or plain text, and everything
// else is JSON-encoded.
func VarBody(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return value
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(encoded)
}

// varInt reads a status code from a variable. Scripts and JSON both deliver
// numbers as float64, and an out-of-range value is ignored rather than sent.
func varInt(v any) (int, bool) {
	var code int
	switch value := v.(type) {
	case int:
		code = value
	case int64:
		code = int(value)
	case float64:
		code = int(value)
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return 0, false
		}
		code = int(parsed)
	default:
		return 0, false
	}
	if code < 100 || code > 599 {
		return 0, false
	}
	return code, true
}

// ResponseRegistry tracks webhook requests whose HTTP response is produced by
// the flow run rather than sent immediately. The HTTP handler registers a slot
// before emitting the trigger message and waits on it; a Webhook return
// inside the run fills it.
//
// The registry is the seam that keeps the engine free of HTTP types on the
// hot path: a run carries only the request ID in message metadata, and a run
// that never returns a response is answered when the run completes.
type ResponseRegistry struct {
	mu      sync.Mutex
	pending map[string]chan *HTTPResponse
}

// NewResponseRegistry creates an empty registry.
func NewResponseRegistry() *ResponseRegistry {
	return &ResponseRegistry{pending: make(map[string]chan *HTTPResponse)}
}

// Register opens a response slot for a request. The returned channel receives
// at most one response; release removes the slot and must always be called.
func (r *ResponseRegistry) Register(requestID string) (<-chan *HTTPResponse, func()) {
	ch := make(chan *HTTPResponse, 1)
	r.mu.Lock()
	r.pending[requestID] = ch
	r.mu.Unlock()

	return ch, func() {
		r.mu.Lock()
		delete(r.pending, requestID)
		r.mu.Unlock()
	}
}

// Respond delivers a response for a request. It reports whether a slot was
// waiting, so a node can tell the author that the run was not started by a
// synchronous webhook. Only the first response for a request is delivered;
// later ones are dropped rather than blocking the run.
func (r *ResponseRegistry) Respond(requestID string, resp *HTTPResponse) bool {
	r.mu.Lock()
	ch, ok := r.pending[requestID]
	if ok {
		delete(r.pending, requestID)
	}
	r.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- resp:
		return true
	default:
		return false
	}
}

// defaultResponses is the process-wide registry shared by the HTTP webhook
// route and the Webhook return execution.
var defaultResponses = NewResponseRegistry()

// Responses returns the process-wide response registry.
func Responses() *ResponseRegistry { return defaultResponses }
