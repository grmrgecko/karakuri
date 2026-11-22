// Package sandbox runs user-supplied scripts in isolated interpreters that
// have no access to the host filesystem, process table, or environment. Each
// supported language runs inside an interpreter that provides no I/O of its
// own, so the only way out of a script is through the host functions this
// package installs. Network access is one of those host functions and is
// gated by a configurable policy.
package sandbox

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// Language identifiers for the supported script runtimes.
const (
	LangJavaScript = "javascript"
	LangPython     = "python"
	LangBash       = "bash"
)

// Limits bound what a single script execution may consume. Zero values fall
// back to the defaults applied by Normalize.
type Limits struct {
	// Timeout is the wall-clock budget for one execution.
	Timeout time.Duration
	// MaxOutput caps captured stdout/console bytes.
	MaxOutput int
	// MaxHTTPRequests caps outbound calls per execution.
	MaxHTTPRequests int
	// MaxHTTPResponse caps bytes read from any single response body.
	MaxHTTPResponse int
	// MemoryLimitMB caps guest memory for runtimes that can enforce it.
	MemoryLimitMB int
}

// Default limits applied when a field is unset. They are deliberately modest:
// a flow node that needs more than this is doing something a script node is
// the wrong tool for.
const (
	defaultTimeout         = 30 * time.Second
	defaultMaxOutput       = 256 << 10
	defaultMaxHTTPRequests = 20
	defaultMaxHTTPResponse = 8 << 20
	defaultMemoryLimitMB   = 256
)

// Normalize fills unset fields with defaults.
func (l Limits) Normalize() Limits {
	if l.Timeout <= 0 {
		l.Timeout = defaultTimeout
	}
	if l.MaxOutput <= 0 {
		l.MaxOutput = defaultMaxOutput
	}
	if l.MaxHTTPRequests <= 0 {
		l.MaxHTTPRequests = defaultMaxHTTPRequests
	}
	if l.MaxHTTPResponse <= 0 {
		l.MaxHTTPResponse = defaultMaxHTTPResponse
	}
	if l.MemoryLimitMB <= 0 {
		l.MemoryLimitMB = defaultMemoryLimitMB
	}
	return l
}

// Request is one script execution: the source to run, the message data it
// operates on, and the limits and network policy that apply to it.
type Request struct {
	Language string
	Source   string

	// Payload, Topic, and Meta mirror the engine message fields and are
	// exposed to the script as top-level values. They carry the message on the
	// node's main input port.
	Payload any
	Topic   string
	Meta    map[string]any
	// Message is the complete first input message, including its ID, group
	// stack, and continuation marker. Bash exposes it as MSG. Sandbox callers
	// without an envelope retain the Payload, Topic, and Meta projection.
	Message *nodeapi.Msg
	// Inputs holds the payload delivered to every connected input port, keyed
	// by port ID, so a script that declared a signature reads its sockets by
	// the names it gave them. The main port appears here too, under its own ID;
	// Payload is the same value reached the short way.
	Inputs map[string]any
	// Vars, Flow, and Global are the run, flow, and global variable scopes at
	// the moment the script starts. Scripts see them as mutable `vars`, `flow`,
	// and `global` objects and can also reach them through get/set; writes come
	// back in the matching Result fields.
	Vars   map[string]any
	Flow   map[string]any
	Global map[string]any

	Limits  Limits
	Network NetworkPolicy
	// NodeInvoker is the optional server-flow capability exposed as nodes in
	// JavaScript and Python and as the node builtin in Bash. Standalone and
	// non-flow script contexts leave it nil.
	NodeInvoker nodeapi.ScriptNodeInvoker
}

// Result is what a script produced: its return value, the variables it left
// behind, and anything it wrote to the console.
type Result struct {
	// Value is the script's result, already converted to plain Go types
	// (maps, slices, strings, float64, bool, nil) so it can be used directly
	// as a message payload and serialized to run history.
	Value any
	// Vars, Flow, and Global are the three variable scopes as they stood when
	// the script ended. Runtimes collect them at exit rather than calling back
	// into the host per assignment, which keeps the sandbox free of live host
	// capabilities. The consequence is that a flow or global write lands when
	// the script finishes, so two concurrent runs touching the same key settle
	// last-writer-wins rather than serializing.
	Vars   map[string]any
	Flow   map[string]any
	Global map[string]any
	// MsgWrites holds message-scope writes as property path to value, in the
	// order they were made. The script node applies them to the outgoing
	// message; the sandbox does not resolve paths itself, so one implementation
	// of dot-path writing serves every node.
	MsgWrites []MsgWrite
	// Output holds console/print lines in emission order.
	Output []string
}

// MsgWrite is one message-scope assignment a script made.
type MsgWrite struct {
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// Runtime executes scripts for one language.
type Runtime interface {
	// Language returns the identifier this runtime handles.
	Language() string
	// Available reports whether the runtime can execute right now. Runtimes
	// that depend on an external asset report why they cannot.
	Available() error
	// Run executes a script and returns its result.
	Run(ctx context.Context, req Request) (*Result, error)
}

// registry holds the runtime for each language.
var (
	registryMu sync.RWMutex
	registry   = map[string]Runtime{}
)

// Register installs a runtime for its language, replacing any existing entry.
func Register(rt Runtime) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[rt.Language()] = rt
}

// Get returns the runtime registered for a language.
func Get(language string) (Runtime, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	rt, ok := registry[strings.ToLower(strings.TrimSpace(language))]
	return rt, ok
}

// Languages lists registered language identifiers in sorted order.
func Languages() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Process-wide execution policy, installed once at startup from configuration
// and read by every script execution. Holding it here rather than threading it
// through the engine keeps node code free of configuration plumbing.
var (
	defaultsMu     sync.RWMutex
	defaultLimits  = Limits{}.Normalize()
	defaultNetwork = NetworkFull
)

// SetDefaults installs the process-wide limits and network policy.
func SetDefaults(limits Limits, policy NetworkPolicy) {
	defaultsMu.Lock()
	defer defaultsMu.Unlock()
	defaultLimits = limits.Normalize()
	defaultNetwork = policy
}

// Defaults returns the process-wide limits and network policy.
func Defaults() (Limits, NetworkPolicy) {
	defaultsMu.RLock()
	defer defaultsMu.RUnlock()
	return defaultLimits, defaultNetwork
}

// Run executes a script with the runtime registered for its language.
func Run(ctx context.Context, req Request) (*Result, error) {
	req.Limits = req.Limits.Normalize()
	req.Payload = encodeBinary(req.Payload)
	req.Inputs = encodeBinaryMap(req.Inputs)
	req.Meta = encodeBinaryMap(req.Meta)
	// The scopes are snapshots of stores every run shares, copied one level
	// deep. A runtime that hands the script live objects would let a write
	// inside a nested variable reach the store itself, unsynchronized and
	// whether or not the script goes on to succeed.
	req.Vars = encodeBinaryMap(req.Vars)
	req.Flow = encodeBinaryMap(req.Flow)
	req.Global = encodeBinaryMap(req.Global)
	rt, ok := Get(req.Language)
	if !ok {
		return nil, fmt.Errorf("no sandbox runtime for language %q", req.Language)
	}
	if err := rt.Available(); err != nil {
		return nil, fmt.Errorf("%s runtime unavailable: %w", req.Language, err)
	}

	ctx, cancel := context.WithTimeout(ctx, req.Limits.Timeout)
	defer cancel()
	return rt.Run(ctx, req)
}

// encodeBinary rewrites binary values as base64 text on their way into a
// script. Every language sees the same thing that way: the JSON transports
// would encode bytes as base64 anyway, and the in-process JS runtime would
// otherwise hand a script an array of numbers for the same payload. A script
// that needs the bytes back decodes the text, the same as a base64 node does.
func encodeBinary(v any) any {
	switch t := v.(type) {
	case []byte:
		return base64.StdEncoding.EncodeToString(t)
	case map[string]any:
		return encodeBinaryMap(t)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = encodeBinary(item)
		}
		return out
	default:
		return copyTyped(v)
	}
}

// copyTyped rebuilds a collection a node built with a concrete Go type, such
// as []map[string]any or map[string][]string, as the plain map and list forms
// the cases above copy. A runtime that reads Go values in place would
// otherwise hand the script the caller's own collection to write into.
// Anything that is not a string-keyed map or a list is returned as it is, and
// so is a named byte slice, which holds bytes rather than elements.
func copyTyped(v any) any {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map:
		if rv.IsNil() || rv.Type().Key().Kind() != reflect.String {
			return v
		}
		out := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = encodeBinary(iter.Value().Interface())
		}
		return out
	case reflect.Slice:
		if rv.IsNil() || rv.Type().Elem().Kind() == reflect.Uint8 {
			return v
		}
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = encodeBinary(rv.Index(i).Interface())
		}
		return out
	default:
		return v
	}
}

// encodeBinaryMap applies encodeBinary to every value of a map, into a copy so
// the caller's message is left as it was. Nested maps and lists are copied
// too, which is what keeps a script's writes out of the values it was handed.
func encodeBinaryMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = encodeBinary(v)
	}
	return out
}

// ---- Shared console capture ----

// console accumulates script output up to a byte budget. Once the budget is
// exhausted further writes are dropped and a single truncation marker is
// appended, so a runaway print loop cannot exhaust memory or flood run history.
type console struct {
	mu        sync.Mutex
	lines     []string
	pending   strings.Builder
	written   int
	max       int
	truncated bool
}

func newConsole(max int) *console { return &console{max: max} }

// add appends one console line, respecting the byte budget.
func (c *console) add(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addLocked(line)
}

// addLocked appends a line with the mutex already held.
func (c *console) addLocked(line string) {
	if c.truncated {
		return
	}
	if c.written+len(line) > c.max {
		c.lines = append(c.lines, "... output truncated")
		c.truncated = true
		return
	}
	c.written += len(line)
	c.lines = append(c.lines, line)
}

// Write lets the console stand in for an io.Writer for runtimes that emit raw
// stdout. Writes are buffered until a newline arrives: an unbuffered guest
// emits one write per print argument, and splitting on write boundaries would
// scatter a single line across several entries.
func (c *console) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	text := string(p)
	for {
		idx := strings.IndexByte(text, '\n')
		if idx < 0 {
			c.pending.WriteString(text)
			break
		}
		c.pending.WriteString(text[:idx])
		c.addLocked(c.pending.String())
		c.pending.Reset()
		text = text[idx+1:]
	}
	return len(p), nil
}

// snapshot returns the captured lines, flushing any trailing partial line so
// output that never ended with a newline is not lost.
func (c *console) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending.Len() > 0 {
		c.addLocked(c.pending.String())
		c.pending.Reset()
	}
	out := make([]string, len(c.lines))
	copy(out, c.lines)
	return out
}

// ---- Shared HTTP bridge ----

// httpRequest is the language-neutral shape scripts use to describe a call.
type httpRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// httpResponse is the language-neutral shape returned to scripts. Errors are
// reported in-band so scripts can handle them without language-specific
// exception plumbing.
type httpResponse struct {
	Status  int               `json:"status"`
	OK      bool              `json:"ok"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Error   string            `json:"error,omitempty"`
}

// httpBridge performs outbound calls on behalf of a script under the request's
// network policy and per-execution call budget.
type httpBridge struct {
	client *http.Client
	limits Limits

	mu    sync.Mutex
	calls int
}

// newHTTPBridge builds a bridge for one execution.
func newHTTPBridge(policy NetworkPolicy, limits Limits) *httpBridge {
	return &httpBridge{
		client: HTTPClient(policy, limits.Timeout),
		limits: limits,
	}
}

// do performs one call, converting every failure into a populated Error field
// rather than a Go error so each runtime can surface it idiomatically.
func (b *httpBridge) do(ctx context.Context, req httpRequest) httpResponse {
	b.mu.Lock()
	b.calls++
	over := b.calls > b.limits.MaxHTTPRequests
	b.mu.Unlock()
	if over {
		return httpResponse{Error: fmt.Sprintf("http request limit of %d exceeded", b.limits.MaxHTTPRequests)}
	}

	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	if req.URL == "" {
		return httpResponse{Error: "url is required"}
	}
	// Only the web schemes are reachable; file:// and friends would otherwise
	// hand a script the host filesystem through the HTTP client.
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		return httpResponse{Error: "only http:// and https:// URLs are allowed"}
	}

	var body io.Reader
	if req.Body != "" {
		body = strings.NewReader(req.Body)
	}
	hreq, err := http.NewRequestWithContext(ctx, method, req.URL, body)
	if err != nil {
		return httpResponse{Error: err.Error()}
	}
	for k, v := range req.Headers {
		hreq.Header.Set(k, v)
	}
	if hreq.Header.Get("User-Agent") == "" {
		hreq.Header.Set("User-Agent", "karakuri-script")
	}

	resp, err := b.client.Do(hreq)
	if err != nil {
		return httpResponse{Error: err.Error()}
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(b.limits.MaxHTTPResponse)))
	if err != nil {
		return httpResponse{Error: err.Error()}
	}

	headers := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		headers[strings.ToLower(k)] = resp.Header.Get(k)
	}
	return httpResponse{
		Status:  resp.StatusCode,
		OK:      resp.StatusCode >= 200 && resp.StatusCode < 300,
		Headers: headers,
		Body:    string(data),
	}
}
