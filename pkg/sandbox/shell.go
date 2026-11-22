package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// ShellRuntime executes bash with mvdan.cc/sh, an interpreter written in Go
// that never shells out on its own. Every external command and every file
// open goes through a handler, and both handlers deny by default, so the
// script is limited to shell builtins, arithmetic, and the few commands this
// runtime implements itself.
type ShellRuntime struct{}

func (ShellRuntime) Language() string { return LangBash }

// Available reports the runtime ready; the interpreter is compiled in.
func (ShellRuntime) Available() error { return nil }

// Run parses and interprets the script. Standard output becomes the result
// and standard error becomes console output, so a script logs with `log` or
// an explicit `>&2` redirect without corrupting its return value.
func (ShellRuntime) Run(ctx context.Context, req Request) (*Result, error) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).
		Parse(strings.NewReader(req.Source), "script.sh")
	if err != nil {
		return nil, fmt.Errorf("bash syntax error: %w", err)
	}
	if req.NodeInvoker != nil {
		if err := rejectBackgroundShellNodeCalls(file); err != nil {
			return nil, err
		}
	}

	out := newConsole(req.Limits.MaxOutput)
	bridge := newHTTPBridge(req.Network, req.Limits)
	stdout := &limitedBuffer{max: req.Limits.MaxOutput}

	// Encode the message for the script. Bash has no structured values, so
	// the payload arrives both as JSON on stdin and as environment variables,
	// and each variable scope arrives as a JSON document to read with jsonget.
	payloadJSON := toJSONString(req.Payload)
	metaJSON := toJSONString(req.Meta)
	message := &nodeapi.Msg{Payload: req.Payload, Topic: req.Topic, Meta: req.Meta}
	if req.Message != nil {
		message = req.Message
	}
	messageCopy := *message
	messageCopy.Payload = encodeBinary(messageCopy.Payload)
	messageCopy.Meta = encodeBinaryMap(messageCopy.Meta)

	// Variable writes accumulate here: a shell script has no return channel
	// besides stdout, which is already the result, so `setvar` collects.
	scopes := newScopeState(req)
	nodes := newShellNodeBridge(req.NodeInvoker)

	runner, err := interp.New(
		interp.StdIO(strings.NewReader(payloadJSON), stdout, out),
		interp.Env(expand.ListEnviron(
			"PAYLOAD="+payloadJSON,
			"TOPIC="+req.Topic,
			"META="+metaJSON,
			"INPUT="+toJSONString(orEmptyMap(req.Inputs)),
			"MSG="+toJSONString(&messageCopy),
			"VARS="+toJSONString(req.Vars),
			"FLOW="+toJSONString(req.Flow),
			"GLOBAL="+toJSONString(req.Global),
			"PATH=",
			"HOME=",
		)),
		// The process's own directory would otherwise become the script's $PWD.
		interp.Dir(sandboxDir),
		interp.ExecHandlers(sandboxExecHandler(bridge, scopes, nodes)),
		interp.OpenHandler(sandboxOpenHandler),
		interp.ReadDirHandler2(sandboxReadDirHandler),
		interp.StatHandler(sandboxStatHandler),
	)
	if err != nil {
		return nil, err
	}

	if err := runner.Run(ctx, file); err != nil {
		// A node error wraps an exit status so shell conditions can catch it. Keep
		// the structured cause when that status becomes the script's final result.
		var callErr *nodeapi.NodeCallError
		if errors.As(err, &callErr) {
			return nil, err
		}
		// A non-zero exit status is a script failure the flow should see.
		var status interp.ExitStatus
		if ok := asExitStatus(err, &status); ok {
			return nil, fmt.Errorf("script exited with status %d: %s",
				int(status), strings.TrimSpace(strings.Join(out.snapshot(), "; ")))
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("script timed out")
		}
		return nil, err
	}
	res := &Result{
		Value:  parseShellResult(stdout.String()),
		Output: out.snapshot(),
	}
	scopes.snapshot(res)
	return res, nil
}

// sandboxExecHandler denies every external command and implements the small
// set of commands a sandboxed script legitimately needs. The default branch
// is a denial, so a new command is only reachable by being added here.
func sandboxExecHandler(bridge *httpBridge, scopes *scopeState, nodes *shellNodeBridge) func(interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
		return func(ctx context.Context, args []string) error {
			if len(args) == 0 {
				return nil
			}
			hc := interp.HandlerCtx(ctx)
			if args[0] == "node" && nodes != nil {
				return nodes.call(ctx, hc, args[1:])
			}
			switch args[0] {
			case "log":
				// Write to stderr so logging never pollutes the result.
				fmt.Fprintln(hc.Stderr, strings.Join(args[1:], " "))
				return nil
			case "http":
				return shellHTTP(ctx, bridge, hc, args[1:])
			case "jsonget":
				return shellJSONGet(hc, args[1:])
			case "setvar":
				return shellSetVar(scopes, args[1:])
			case "getvar":
				return shellGetVar(hc, scopes, args[1:])
			default:
				return fmt.Errorf("command %q is not available in the sandbox", args[0])
			}
		}
	}
}

// sandboxOpenHandler denies filesystem access. The null device stays open
// because redirecting to /dev/null is ordinary shell usage and reveals
// nothing about the host.
func sandboxOpenHandler(ctx context.Context, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	if path == "/dev/null" {
		return devNull{}, nil
	}
	return nil, fmt.Errorf("file access to %q is not available in the sandbox", path)
}

// sandboxDir is the working directory a script sees. The filesystem root
// exists on every host and says nothing about where the server runs.
var sandboxDir = string(filepath.Separator)

// sandboxReadDirHandler reports every directory as empty. The interpreter
// reads directories to expand globs, so without it a pattern lists host file
// names even though no file can be opened. An empty listing rather than an
// error keeps the pattern literal, the way a pattern matching nothing does;
// an error would fail the whole word and drop it from the command.
func sandboxReadDirHandler(ctx context.Context, path string) ([]fs.DirEntry, error) {
	return nil, nil
}

// sandboxStatHandler reports every path as missing. File tests and cd go
// through it, and answering from the host would let a script map the
// filesystem one path at a time.
func sandboxStatHandler(ctx context.Context, path string, followSymlinks bool) (fs.FileInfo, error) {
	return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrNotExist}
}

// devNull discards writes and reads as an empty file.
type devNull struct{}

func (devNull) Read([]byte) (int, error)    { return 0, io.EOF }
func (devNull) Write(p []byte) (int, error) { return len(p), nil }
func (devNull) Close() error                { return nil }

// shellHTTP implements the `http` command:
//
//	http <method> <url> [body] [header:value ...]
//
// The response body is written to stdout and the status to stderr, so a
// script can capture one with $(...) while still seeing the other.
func shellHTTP(ctx context.Context, bridge *httpBridge, hc interp.HandlerContext, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: http <method> <url> [body] [header:value ...]")
	}
	req := httpRequest{Method: args[0], URL: args[1], Headers: map[string]string{}}
	if len(args) > 2 {
		req.Body = args[2]
	}
	if len(args) > 3 {
		for _, h := range args[3:] {
			name, value, found := strings.Cut(h, ":")
			if !found {
				return fmt.Errorf("malformed header %q; expected name:value", h)
			}
			req.Headers[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
	}

	resp := bridge.do(ctx, req)
	if resp.Error != "" {
		fmt.Fprintln(hc.Stderr, "http error: "+resp.Error)
		return interp.ExitStatus(1)
	}
	fmt.Fprintln(hc.Stderr, fmt.Sprintf("http status %d", resp.Status))
	fmt.Fprint(hc.Stdout, resp.Body)
	if !resp.OK {
		return interp.ExitStatus(1)
	}
	return nil
}

// shellScopeFlag pulls a leading `-s <scope>` off the arguments. The flag
// leads rather than trails because a value may contain spaces and is joined
// from everything left over, which would make a trailing scope ambiguous.
func shellScopeFlag(args []string) (string, []string, error) {
	if len(args) >= 2 && (args[0] == "-s" || args[0] == "--scope") {
		return args[1], args[2:], nil
	}
	if len(args) == 1 && (args[0] == "-s" || args[0] == "--scope") {
		return "", nil, fmt.Errorf("missing scope after %s", args[0])
	}
	return "", args, nil
}

// shellSetVar implements `setvar [-s <scope>] <name> [value]`, the shell's
// channel for writing variables. A value that parses as JSON is stored as the
// decoded value so numbers and objects survive; anything else is stored as
// text. With no value the variable is cleared. The scope defaults to msg, so
// `setvar payload hello` rewrites the outgoing payload.
func shellSetVar(scopes *scopeState, args []string) error {
	scope, args, err := shellScopeFlag(args)
	if err != nil {
		return fmt.Errorf("setvar: %w", err)
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: setvar [-s <scope>] <name> [value]")
	}
	name := args[0]
	if name == "" {
		return fmt.Errorf("setvar: variable name is empty")
	}
	var value any
	if len(args) > 1 {
		value = parseShellResult(strings.Join(args[1:], " "))
	}
	if err := scopes.set(name, scope, value); err != nil {
		return fmt.Errorf("setvar: %w", err)
	}
	return nil
}

// shellGetVar implements `getvar [-s <scope>] [name]`, printing a variable so
// a script can capture it: count=$(getvar -s flow count). Strings print as
// themselves and everything else as JSON, matching how jsonget reports values.
func shellGetVar(hc interp.HandlerContext, scopes *scopeState, args []string) error {
	scope, args, err := shellScopeFlag(args)
	if err != nil {
		return fmt.Errorf("getvar: %w", err)
	}
	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	value, err := scopes.get(name, scope)
	if err != nil {
		return fmt.Errorf("getvar: %w", err)
	}
	if value == nil {
		return nil
	}
	if s, ok := value.(string); ok {
		fmt.Fprintln(hc.Stdout, s)
		return nil
	}
	fmt.Fprintln(hc.Stdout, toJSONString(value))
	return nil
}

// shellJSONGet implements `jsonget <path> [json]`, a minimal accessor so
// scripts can read fields out of the payload without an external tool. The
// path is dot-separated with numeric segments indexing arrays. With no second
// argument it reads the JSON document from stdin.
func shellJSONGet(hc interp.HandlerContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: jsonget <path> [json]")
	}
	source := ""
	if len(args) > 1 {
		source = args[1]
	} else {
		data, err := io.ReadAll(io.LimitReader(hc.Stdin, 1<<20))
		if err != nil {
			return err
		}
		source = string(data)
	}

	// Numbers stay as the document wrote them. A float64 rounds an integer
	// past 2^53, such as a nanosecond timestamp or a 64-bit ID.
	var doc any
	dec := json.NewDecoder(strings.NewReader(source))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("jsonget: input is not valid JSON: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("jsonget: input is not valid JSON: unexpected data after the document")
	}

	value := jsonNumbers(doc)
	if path := strings.TrimSpace(args[0]); path != "" && path != "." {
		for _, segment := range strings.Split(path, ".") {
			if segment == "" {
				continue
			}
			next, ok := jsonIndex(value, segment)
			if !ok {
				return interp.ExitStatus(1)
			}
			value = next
		}
	}

	// Scalars print bare so they interpolate cleanly; containers print JSON.
	switch v := value.(type) {
	case string:
		fmt.Fprintln(hc.Stdout, v)
	case nil:
		fmt.Fprintln(hc.Stdout, "")
	default:
		// Numbers print as JSON writes them: the default float form puts
		// 1764547200 in exponent notation, which shell arithmetic cannot read.
		fmt.Fprintln(hc.Stdout, toJSONString(v))
	}
	return nil
}

// jsonIndex resolves one path segment against a decoded JSON value.
func jsonIndex(value any, segment string) (any, bool) {
	switch v := value.(type) {
	case map[string]any:
		got, ok := v[segment]
		return got, ok
	case []any:
		idx, err := strconv.Atoi(segment)
		if err != nil {
			return nil, false
		}
		if idx < 0 || idx >= len(v) {
			return nil, false
		}
		return v[idx], true
	default:
		return nil, false
	}
}

// jsonNumbers settles the numbers of a document decoded with UseNumber. An
// integer written in plain digits keeps its text at any size. Every other
// number becomes a float64, so 1.0 and 1e3 still print as 1 and 1000.
func jsonNumbers(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			v[key] = jsonNumbers(item)
		}
	case []any:
		for i, item := range v {
			v[i] = jsonNumbers(item)
		}
	case json.Number:
		if strings.ContainsAny(v.String(), ".eE") {
			if f, err := v.Float64(); err == nil {
				return f
			}
		}
	}
	return value
}

// parseShellResult turns captured stdout into a payload: JSON when the output
// parses as JSON, otherwise the trimmed text.
func parseShellResult(s string) any {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
		return parsed
	}
	return trimmed
}

// asExitStatus reports whether err carries a shell exit status.
func asExitStatus(err error, out *interp.ExitStatus) bool {
	status, ok := err.(interp.ExitStatus)
	if ok {
		*out = status
	}
	return ok
}

// toJSONString marshals a value, falling back to an empty JSON object so the
// script always receives parseable input.
func toJSONString(v any) string {
	if v == nil {
		return "null"
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(data)
}

// limitedBuffer accumulates output up to a byte cap, discarding the excess.
// Pipeline stages and background statements write it from their own
// goroutines, so it holds mu.
type limitedBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.max - len(b.buf); room > 0 {
		if len(p) > room {
			b.buf = append(b.buf, p[:room]...)
		} else {
			b.buf = append(b.buf, p...)
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
