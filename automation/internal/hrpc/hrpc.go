// Package hrpc is the wire between an automation session and the session helper
// it delegates to. It exists because a privileged process cannot reach a
// desktop: the session bus refuses a connection from any uid but its owner, so
// an agent running as root has to become the user to touch their session, and
// becoming a user is something only a fresh process can do.
//
// The protocol is deliberately small — one JSON request per capability call,
// one JSON response back, correlated by id so a dialog that sits open for a
// minute does not hold up the notification behind it. Errors carry their kind
// rather than only their text, because a caller with a second route to try
// checks for a missing tool before giving up, and that check has to survive the
// crossing.
package hrpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Request is one capability call.
type Request struct {
	ID     uint64          `json:"id"`
	Cap    string          `json:"cap"`
	Method string          `json:"method"`
	Args   json.RawMessage `json:"args,omitempty"`
}

// Response is the result of one call. Exactly one of Result and Err is set.
type Response struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Err    *WireError      `json:"err,omitempty"`
}

// Error kinds. Anything else crosses as plain text.
const (
	KindPlain       = ""
	KindUnsupported = "unsupported"
	KindMissingTool = "missing_tool"
)

// WireError carries a failure across the process boundary with enough of its
// identity to be rebuilt: the kind is what errors.Is and IsMissingTool read on
// the other side, and the tool names are what the message needs to stay useful.
type WireError struct {
	Kind    string   `json:"kind"`
	Message string   `json:"message"`
	Action  string   `json:"action,omitempty"`
	Tools   []string `json:"tools,omitempty"`
}

// Error renders the failure, so a WireError that reached a caller unrebuilt is
// still readable.
func (e *WireError) Error() string { return e.Message }

// Conn is one end of the pipe, framing JSON values in both directions. Writes
// are serialized; reads belong to whichever goroutine owns the loop.
type Conn struct {
	dec *json.Decoder
	enc *json.Encoder
	mu  sync.Mutex
	w   io.Closer
}

// NewConn frames a request/response stream over a reader and a writer.
func NewConn(r io.Reader, w io.Writer) *Conn {
	c := &Conn{dec: json.NewDecoder(r), enc: json.NewEncoder(w)}
	if closer, ok := w.(io.Closer); ok {
		c.w = closer
	}
	return c
}

// WriteRequest sends one call.
func (c *Conn) WriteRequest(req Request) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(req)
}

// WriteResponse sends one reply.
func (c *Conn) WriteResponse(resp Response) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(resp)
}

// ReadRequest reads the next call, returning io.EOF when the peer is gone.
func (c *Conn) ReadRequest() (Request, error) {
	var req Request
	err := c.dec.Decode(&req)
	return req, err
}

// ReadResponse reads the next reply.
func (c *Conn) ReadResponse() (Response, error) {
	var resp Response
	err := c.dec.Decode(&resp)
	return resp, err
}

// Close shuts the writing end, which is what tells the peer to stop.
func (c *Conn) Close() error {
	if c.w == nil {
		return nil
	}
	return c.w.Close()
}

// Marshal encodes call arguments, folding the error into the caller's path.
func Marshal(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encoding call arguments: %w", err)
	}
	return b, nil
}

// Unmarshal decodes call arguments, treating an absent body as the zero value
// so a method taking nothing needs no special case.
func Unmarshal(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("decoding call arguments: %w", err)
	}
	return nil
}

// ErrPeerGone reports that the helper stopped answering, which a caller turns
// into a restart rather than a failure the flow sees.
var ErrPeerGone = errors.New("automation: the session helper is no longer running")
