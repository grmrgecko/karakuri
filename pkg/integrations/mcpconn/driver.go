// Package mcpconn implements the MCP server connection driver: a client over
// the official modelcontextprotocol/go-sdk speaking streamable HTTP or SSE to
// a remote MCP server, or stdio to a local one it spawns. A connection is how
// a server is named once and reused — the mcp-tools node hands its tools to an
// AI agent, and mcp-call invokes one directly from a flow. The driver keeps
// one session, dialed lazily and redialed after a transport failure, so every
// consumer shares the handshake.
package mcpconn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/version"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapio"
)

// Kind is the driver kind registered with the integrations registry.
const Kind = "mcp"

// Transport selection values.
const (
	TransportStreamable = "streamable"
	TransportSSE        = "sse"
	TransportStdio      = "stdio"
)

// dialTimeout bounds the connect handshake; a tool call's own context bounds
// everything after.
const dialTimeout = 15 * time.Second

// Config is the driver configuration stored (encrypted) on a connection. The
// HTTP transports read URL, Bearer, and Headers; stdio reads Command, Args,
// and Env — the server is a subprocess this server spawns, the form most MCP
// servers ship in.
type Config struct {
	URL       string            `json:"url"`
	Transport string            `json:"transport"`
	Bearer    string            `json:"bearer_token"`
	Headers   map[string]string `json:"headers"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
}

// Validate checks that the config is complete for its transport: an http(s)
// URL for the HTTP transports, a command for stdio.
func (c Config) Validate() error {
	switch c.Transport {
	case TransportStdio:
		if strings.TrimSpace(c.Command) == "" {
			return fmt.Errorf("mcp stdio config has no command")
		}
		return nil
	case "", TransportStreamable, TransportSSE:
		u := strings.TrimSpace(c.URL)
		if u == "" {
			return fmt.Errorf("mcp config has no url")
		}
		parsed, err := url.Parse(u)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("mcp url %q is not an http(s) URL", u)
		}
		return nil
	default:
		return fmt.Errorf("mcp transport %q is not streamable, sse, or stdio", c.Transport)
	}
}

// Tool is one tool a connected MCP server offers, reduced to what the AI
// nodes need: identity, description, and the arguments schema as plain data.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// Image is one image content block a tool returned: raw bytes and their MIME
// type. A screenshot tool answers with these, and the agent forwards them to a
// vision model.
type Image struct {
	MIME string
	Data []byte
}

// ToolResult is one tool invocation's outcome. IsError is the MCP-level
// failure flag: the transport worked and the server answered, but the tool
// itself failed — for an agent that is an observation, not a node error.
// Images carries any image content blocks separately from the text/structured
// Value, since a screenshot is not something to stringify.
type ToolResult struct {
	Value   any
	Images  []Image
	IsError bool
}

// Driver is one MCP server connection.
type Driver struct {
	id  uuid.UUID
	cfg Config

	log           *zap.Logger
	publishStatus func(integrations.Status)

	mu     sync.Mutex
	status integrations.Status
	sess   *mcp.ClientSession
	runCtx context.Context
	cancel context.CancelFunc
}

// New builds an MCP driver from a connection model. Registered as the factory
// for the "mcp" kind.
func New(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
	var cfg Config
	if err := json.Unmarshal(model.Config, &cfg); err != nil {
		return nil, fmt.Errorf("invalid mcp config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg.URL = strings.TrimSpace(cfg.URL)

	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}
	return &Driver{
		id:            model.ID,
		cfg:           cfg,
		log:           log,
		publishStatus: deps.PublishStatus,
		status:        integrations.Status{State: integrations.StateConnecting, Since: time.Now()},
	}, nil
}

// ID returns the connection's database identifier.
func (d *Driver) ID() uuid.UUID { return d.id }

// Kind returns the driver kind.
func (d *Driver) Kind() string { return Kind }

// Status returns the connection's current lifecycle state.
func (d *Driver) Status() integrations.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

// setStatus records a state transition and relays it to the manager.
func (d *Driver) setStatus(state, errMsg string) {
	d.mu.Lock()
	if d.status.State == state && d.status.Error == errMsg {
		d.mu.Unlock()
		return
	}
	d.status = integrations.Status{State: state, Error: errMsg, Since: time.Now()}
	status := d.status
	publish := d.publishStatus
	d.mu.Unlock()

	d.log.Info("mcp connection status", zap.String("state", state), zap.String("error", errMsg))
	if publish != nil {
		publish(status)
	}
}

// Start dials and initializes the session in the background so the status
// reports a reachable server. A failure leaves the driver usable: the first
// tool listing redials.
func (d *Driver) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.runCtx = runCtx
	d.cancel = cancel
	d.mu.Unlock()

	d.setStatus(integrations.StateConnecting, "")
	go func() {
		if _, err := d.session(runCtx); err != nil {
			if runCtx.Err() == nil {
				d.setStatus(integrations.StateError, err.Error())
			}
			return
		}
		d.setStatus(integrations.StateConnected, "")
	}()
	return nil
}

// Stop closes the session and marks the connection disabled. The run context
// is canceled under the lock, so a dial finishing afterwards sees it and
// closes its own session instead of storing one nothing will close.
func (d *Driver) Stop() {
	d.mu.Lock()
	if d.cancel != nil {
		d.cancel()
	}
	sess := d.sess
	d.cancel = nil
	d.sess = nil
	d.mu.Unlock()

	if sess != nil {
		_ = sess.Close()
	}
	d.setStatus(integrations.StateDisabled, "")
}

// transport builds the configured client transport. Both HTTP transports take
// a client whose round-tripper injects the configured auth headers. The SSE
// transport's event stream is bound to runCtx rather than the dial context.
// Stdio
// builds a fresh command per dial — an exec.Cmd starts once, and a redial is a
// new subprocess — inheriting this process's environment with the configured
// entries layered on, and its stderr relayed to the log so a failing server
// says why.
func (d *Driver) transport(runCtx context.Context) mcp.Transport {
	if d.cfg.Transport == TransportStdio {
		cmd := exec.Command(strings.TrimSpace(d.cfg.Command), d.cfg.Args...)
		cmd.Env = os.Environ()
		keys := make([]string, 0, len(d.cfg.Env))
		for k := range d.cfg.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			cmd.Env = append(cmd.Env, k+"="+d.cfg.Env[k])
		}
		cmd.Stderr = &zapio.Writer{Log: d.log.Named("mcp-server"), Level: zap.DebugLevel}
		return &discoverShim{inner: &mcp.CommandTransport{Command: cmd}}
	}
	client := &http.Client{Transport: &headerTransport{cfg: d.cfg}}
	if d.cfg.Transport == TransportSSE {
		return &sseStream{
			inner:  &mcp.SSEClientTransport{Endpoint: d.cfg.URL, HTTPClient: client},
			runCtx: runCtx,
		}
	}
	return &mcp.StreamableClientTransport{Endpoint: d.cfg.URL, HTTPClient: client}
}

// session returns the live session, dialing one when none is held. The dial
// runs on the driver's own context rather than the caller's, so a session
// outlives the run that happened to need it first.
func (d *Driver) session(ctx context.Context) (*mcp.ClientSession, error) {
	d.mu.Lock()
	if d.sess != nil {
		sess := d.sess
		d.mu.Unlock()
		return sess, nil
	}
	runCtx := d.runCtx
	d.mu.Unlock()
	if runCtx == nil {
		return nil, fmt.Errorf("mcp connection is not started")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	dialCtx, done := context.WithTimeout(runCtx, dialTimeout)
	defer done()
	client := mcp.NewClient(&mcp.Implementation{Name: "karakuri", Version: version.Version}, nil)
	sess, err := client.Connect(dialCtx, d.transport(runCtx), nil)
	if err != nil {
		return nil, fmt.Errorf("connecting to MCP server: %w", err)
	}

	d.mu.Lock()
	if runCtx.Err() != nil {
		d.mu.Unlock()
		_ = sess.Close()
		return nil, fmt.Errorf("mcp connection stopped")
	}
	// A concurrent dial may have won; keep the first session and fold this
	// one away rather than leaking its stream.
	if d.sess != nil {
		held := d.sess
		d.mu.Unlock()
		_ = sess.Close()
		return held, nil
	}
	d.sess = sess
	d.mu.Unlock()
	return sess, nil
}

// dropSession discards a session after a transport failure so the next call
// redials instead of reusing a stream the server has forgotten.
func (d *Driver) dropSession(sess *mcp.ClientSession) {
	d.mu.Lock()
	if d.sess == sess {
		d.sess = nil
	}
	d.mu.Unlock()
	_ = sess.Close()
}

// Tools lists the server's tools.
func (d *Driver) Tools(ctx context.Context) ([]Tool, error) {
	sess, err := d.session(ctx)
	if err != nil {
		if ctx.Err() == nil {
			d.setStatus(integrations.StateError, err.Error())
		}
		return nil, err
	}
	var out []Tool
	for tool, err := range sess.Tools(ctx, nil) {
		if err != nil {
			if !callerLeft(ctx) {
				d.dropSession(sess)
			}
			if ctx.Err() == nil {
				d.setStatus(integrations.StateError, err.Error())
			}
			return nil, fmt.Errorf("listing MCP tools: %w", err)
		}
		out = append(out, Tool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schemaMap(tool.InputSchema),
		})
	}
	d.setStatus(integrations.StateConnected, "")
	return out, nil
}

// CallTool invokes one tool. A transport failure is an error and drops the
// session; a tool-level failure comes back as a result with IsError set,
// because the server answered and what it said belongs to the caller. A
// caller that cancels fails alone and the session stays.
func (d *Driver) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
	sess, err := d.session(ctx)
	if err != nil {
		if ctx.Err() == nil {
			d.setStatus(integrations.StateError, err.Error())
		}
		return nil, err
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		// A refusal is an answer: the session works, and closing it would end
		// a stdio server's process and the state it holds.
		if !refused(err) && !callerLeft(ctx) {
			d.dropSession(sess)
			if ctx.Err() == nil {
				d.setStatus(integrations.StateError, err.Error())
			}
		}
		return nil, fmt.Errorf("calling MCP tool %q: %w", name, err)
	}
	d.setStatus(integrations.StateConnected, "")
	value, images := resultValue(res)
	return &ToolResult{Value: value, Images: images, IsError: res.IsError}, nil
}

// callerLeft reports whether a call ended because its caller canceled it,
// such as a canceled flow run. The session is shared, so closing it would
// fail every other run's call in flight and end a stdio server's process. A
// deadline is not a cancel: a server that stopped answering shows up as one,
// and the redial is what recovers it.
func callerLeft(ctx context.Context) bool {
	return errors.Is(ctx.Err(), context.Canceled)
}

// refused reports whether err is the server's own JSON-RPC answer to a
// request it would not carry out, such as an unknown tool or arguments it
// rejects. The SDK reports its transport failures with codes of its own, so
// only the protocol's request errors count.
func refused(err error) bool {
	var wire *jsonrpc.Error
	if !errors.As(err, &wire) {
		return false
	}
	switch wire.Code {
	case jsonrpc.CodeInvalidRequest, jsonrpc.CodeMethodNotFound, jsonrpc.CodeInvalidParams, jsonrpc.CodeInternalError:
		return true
	}
	return false
}

// resultValue reduces a tool result to a payload and its images. The payload
// is the structured content when the server sent one, otherwise its text
// blocks joined; text is returned as the string it is, since the consumer
// knows whether it wants to parse it. Image blocks are collected separately
// and regardless of the structured/text split, so a screenshot survives a
// result that also carries a JSON metadata block.
func resultValue(res *mcp.CallToolResult) (any, []Image) {
	var images []Image
	for _, c := range res.Content {
		if img, ok := c.(*mcp.ImageContent); ok {
			images = append(images, Image{MIME: img.MIMEType, Data: img.Data})
		}
	}
	if res.StructuredContent != nil {
		return res.StructuredContent, images
	}
	var parts []string
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n"), images
}

// schemaMap renders a tool's input schema as a plain map. The SDK documents
// the client-side value as the server schema's default JSON unmarshaling, but
// the field is typed any, so it is re-marshaled rather than asserted.
func schemaMap(schema any) map[string]any {
	if schema == nil {
		return nil
	}
	if m, ok := schema.(map[string]any); ok {
		return m
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// sseStream opens the SSE transport on a context that outlives the dial. The
// go-sdk v1.7.0 SSE client issues its long-lived event-stream GET on the
// context passed to Connect, so opening it on the dial-timeout context closes
// the stream as soon as the dial returns. The stream instead runs on a child
// of the driver's run context, canceled when the connection closes or the
// driver stops; the dial context still aborts a handshake that hangs.
type sseStream struct {
	inner  mcp.Transport
	runCtx context.Context
}

func (s *sseStream) Connect(ctx context.Context) (mcp.Connection, error) {
	streamCtx, cancel := context.WithCancel(s.runCtx)
	stop := context.AfterFunc(ctx, cancel)
	conn, err := s.inner.Connect(streamCtx)
	if !stop() {
		// The dial context ended during the handshake and canceled the stream.
		if err == nil {
			_ = conn.Close()
		}
		return nil, fmt.Errorf("connecting sse stream: %w", context.Cause(ctx))
	}
	if err != nil {
		cancel()
		return nil, err
	}
	return &sseStreamConn{Connection: conn, cancel: cancel}, nil
}

// sseStreamConn releases the stream context when the session closes.
type sseStreamConn struct {
	mcp.Connection
	cancel context.CancelFunc
}

func (c *sseStreamConn) Close() error {
	err := c.Connection.Close()
	c.cancel()
	return err
}

// discoverShim wraps the stdio transport so the SDK's SEP-2575
// server/discover probe never reaches the subprocess. Servers built on older
// runtimes (rmcp's 2024-11-05 stdio framing among them) treat the unknown
// pre-initialize request as fatal and close their pipe, which also kills the
// legacy-initialize fallback the SDK would otherwise perform. The shim
// answers the probe itself with method-not-found, steering the SDK onto the
// legacy handshake over a pipe that is still alive. A server that does speak
// SEP-2575 loses nothing but the stateless fast path.
type discoverShim struct {
	inner mcp.Transport
}

func (s *discoverShim) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := s.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &discoverShimConn{
		Connection: conn,
		synth:      make(chan jsonrpc.Message, 1),
		real:       make(chan shimRead, 1),
	}, nil
}

// shimRead is one pumped result from the underlying connection.
type shimRead struct {
	msg jsonrpc.Message
	err error
}

// discoverShimConn intercepts server/discover writes and answers them on the
// read side. The SDK's read loop blocks inside Read on the subprocess pipe,
// so a synthesized reply cannot wait for the pipe to yield: a pump goroutine
// forwards real messages into a channel and Read selects across both.
type discoverShimConn struct {
	mcp.Connection
	synth    chan jsonrpc.Message
	real     chan shimRead
	pumpOnce sync.Once
}

func (c *discoverShimConn) Write(ctx context.Context, msg jsonrpc.Message) error {
	if req, ok := msg.(*jsonrpc.Request); ok && req.Method == "server/discover" {
		c.synth <- &jsonrpc.Response{
			ID:    req.ID,
			Error: &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "method not found"},
		}
		return nil
	}
	return c.Connection.Write(ctx, msg)
}

func (c *discoverShimConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	c.pumpOnce.Do(func() {
		go func() {
			// Close unblocks the underlying Read, which ends the pump; the
			// buffered channel holds the final error without a receiver.
			for {
				msg, err := c.Connection.Read(context.Background())
				c.real <- shimRead{msg: msg, err: err}
				if err != nil {
					return
				}
			}
		}()
	})
	select {
	case msg := <-c.synth:
		return msg, nil
	case r := <-c.real:
		return r.msg, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// headerTransport injects the configured auth headers into every request the
// MCP client makes.
type headerTransport struct {
	cfg Config
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	if t.cfg.Bearer != "" {
		clone.Header.Set("Authorization", "Bearer "+t.cfg.Bearer)
	}
	for k, v := range t.cfg.Headers {
		if k != "" && v != "" {
			clone.Header.Set(k, v)
		}
	}
	return http.DefaultTransport.RoundTrip(clone)
}

// TestConnection verifies a config by dialing the server and listing its
// tools with a throwaway client. Used as the kind's Test hook.
func TestConnection(ctx context.Context, _ uuid.UUID, cfgJSON []byte) error {
	var cfg Config
	if err := json.Unmarshal(cfgJSON, &cfg); err != nil {
		return fmt.Errorf("invalid mcp config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	cfg.URL = strings.TrimSpace(cfg.URL)

	testCtx, done := context.WithTimeout(ctx, dialTimeout)
	defer done()
	d := &Driver{cfg: cfg, log: zap.NewNop(), runCtx: testCtx}
	sess, err := d.session(testCtx)
	if err != nil {
		return err
	}
	defer sess.Close()
	_, err = sess.ListTools(testCtx, nil)
	return err
}

// KindDef returns the MCP server connection kind: config schema, secrets, the
// driver factory, and the validation and test hooks. Registered by the AI
// plugin — MCP arrived for the agent's tools, though mcp-call stands on its
// own.
func KindDef() integrations.ConnectionKind {
	return integrations.ConnectionKind{
		Kind:  Kind,
		Label: "MCP Server",
		ConfigSpec: []flow.FieldSpec{
			{
				ID: "transport", Label: "Transport", Type: flow.FieldSelect,
				Default: TransportStreamable,
				Options: []flow.Option{
					{Value: TransportStreamable, Label: "Streamable HTTP"},
					{Value: TransportSSE, Label: "SSE (legacy)"},
					{Value: TransportStdio, Label: "Stdio (local command)"},
				},
				Help: "Streamable HTTP is the current protocol; SSE serves older servers; stdio spawns a local server command and speaks over its pipes.",
			},
			{
				ID: "url", Label: "Endpoint URL", Type: flow.FieldString,
				Required: true, Placeholder: "https://mcp.example.com/mcp",
				ShowIf: &flow.ShowIf{Field: "transport", Equals: []string{TransportStreamable, TransportSSE}},
				Help:   "HTTP endpoint of the MCP server.",
			},
			{
				ID: "bearer_token", Label: "Bearer token", Type: flow.FieldString,
				Control: "password",
				ShowIf:  &flow.ShowIf{Field: "transport", Equals: []string{TransportStreamable, TransportSSE}},
				Help:    "Sent as an Authorization: Bearer header when set.",
			},
			{
				ID: "headers", Label: "Headers", Type: flow.FieldJSON, Control: "map",
				ShowIf: &flow.ShowIf{Field: "transport", Equals: []string{TransportStreamable, TransportSSE}},
				Help:   "Extra headers sent on every request, for API keys that ride a custom header.",
			},
			{
				ID: "command", Label: "Command", Type: flow.FieldString,
				Required: true, Placeholder: "/usr/local/bin/my-mcp-server",
				ShowIf: &flow.ShowIf{Field: "transport", Equals: TransportStdio},
				Help:   "Server executable, resolved through PATH when not absolute. Runs as the karakuri server's own user.",
			},
			{
				ID: "args", Label: "Arguments", Type: flow.FieldJSON, Control: "list",
				ShowIf: &flow.ShowIf{Field: "transport", Equals: TransportStdio},
				Help:   "Command-line arguments, one per entry.",
			},
			{
				ID: "env", Label: "Environment", Type: flow.FieldJSON, Control: "map",
				ShowIf: &flow.ShowIf{Field: "transport", Equals: TransportStdio},
				Help:   "Environment variables set for the server, over the karakuri server's own.",
			},
		},
		Secrets: []string{"bearer_token"},
		Factory: New,
		Validate: func(cfg []byte) error {
			var c Config
			if err := json.Unmarshal(cfg, &c); err != nil {
				return fmt.Errorf("invalid mcp config: %w", err)
			}
			return c.Validate()
		},
		Test: TestConnection,
	}
}
