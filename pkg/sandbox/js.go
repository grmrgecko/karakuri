package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dlclark/regexp2/v2"
	"github.com/dop251/goja"
)

// JSRuntime executes JavaScript with goja. A bare goja VM has no module
// loader, no timers, no filesystem, and no network: every capability a script
// has is one this runtime installs explicitly.
type JSRuntime struct{}

func (JSRuntime) Language() string { return LangJavaScript }

// Available reports the runtime ready; goja is compiled into the binary.
func (JSRuntime) Available() error { return nil }

// jsMaxCallStack is the deepest JS call chain allowed, about what Node.js
// permits by default.
const jsMaxCallStack = 10000

// init bounds a single regular-expression match. goja compiles patterns with
// backreferences or lookarounds through regexp2, which has no interrupt hook,
// so the script budget cannot stop a catastrophic backtrack; the engine's own
// match timeout can.
func init() {
	regexp2.DefaultMatchTimeout = 5 * time.Second
}

// jsWrapper wraps user source in a function body so top-level `return` works
// and user declarations cannot collide with the host values we install.
const jsWrapper = `(function(msg, payload, topic, meta, input) {
%s
})`

// Run compiles and executes the script, returning its value and console output.
func (JSRuntime) Run(ctx context.Context, req Request) (*Result, error) {
	vm := goja.New()
	// Field-name mapping keeps host structs idiomatic on the JS side.
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	// goja's default call stack is unbounded, so runaway recursion grows until
	// the process is out of memory rather than raising RangeError.
	vm.SetMaxCallStackSize(jsMaxCallStack)

	out := newConsole(req.Limits.MaxOutput)
	bridge := newHTTPBridge(req.Network, req.Limits)

	if err := installJSGlobals(vm, ctx, out, bridge); err != nil {
		return nil, err
	}
	if req.NodeInvoker != nil {
		if err := installJSNodes(vm, ctx, req.NodeInvoker); err != nil {
			return nil, err
		}
	}
	// The variable scopes are plain global objects the script mutates in place;
	// their final state is read back after the script returns. get/set write
	// through the same objects, so the two styles stay consistent.
	scopes := newScopeState(req)
	if err := installJSScopes(vm, scopes); err != nil {
		return nil, err
	}

	// Interrupt the VM when the execution budget runs out. goja checks the
	// interrupt flag between statements, so this stops runaway loops.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			vm.Interrupt(ctx.Err())
		case <-done:
		}
	}()

	// Compile the wrapped source. Syntax errors are reported against the
	// user's own line numbers, less the wrapper's first line.
	program, err := goja.Compile("script.js", fmt.Sprintf(jsWrapper, req.Source), false)
	if err != nil {
		return nil, fmt.Errorf("javascript syntax error: %s", jsSyntaxMessage(err))
	}

	fnVal, err := vm.RunProgram(program)
	if err != nil {
		return nil, jsError(err)
	}
	fn, ok := goja.AssertFunction(fnVal)
	if !ok {
		return nil, errors.New("javascript wrapper did not produce a callable")
	}

	// A message without metadata still has a meta object to read.
	meta := orEmptyMap(req.Meta)
	value, err := fn(goja.Undefined(),
		vm.ToValue(map[string]any{"payload": req.Payload, "topic": req.Topic, "meta": meta}),
		vm.ToValue(req.Payload),
		vm.ToValue(req.Topic),
		vm.ToValue(meta),
		vm.ToValue(orEmptyMap(req.Inputs)),
	)
	if err != nil {
		return nil, jsError(err)
	}

	result, err := jsExport(vm, value)
	if err != nil {
		return nil, err
	}
	return &Result{
		Value:     result,
		Vars:      jsScope(vm, "vars"),
		Flow:      jsScope(vm, "flow"),
		Global:    jsScope(vm, "global"),
		MsgWrites: scopes.writes,
		Output:    out.snapshot(),
	}, nil
}

// jsScope reads one scope global back as plain Go types. Reading the global
// rather than the map handed in covers scripts that replace the object
// wholesale instead of mutating it.
func jsScope(vm *goja.Runtime, name string) map[string]any {
	exported, err := jsExport(vm, vm.GlobalObject().Get(name))
	if err != nil {
		return nil
	}
	values, _ := exported.(map[string]any)
	return values
}

// installJSScopes exposes the three variable scopes as objects plus the
// get/set pair. Both default to the message scope and the payload property,
// which is what a script most often reads and writes.
func installJSScopes(vm *goja.Runtime, scopes *scopeState) error {
	for name, values := range map[string]map[string]any{
		"vars":   scopes.vars,
		"flow":   scopes.flow,
		"global": scopes.global,
	} {
		if err := vm.Set(name, vm.ToValue(values)); err != nil {
			return err
		}
	}

	// arg returns a string argument, treating undefined and null as absent so
	// get(undefined, "flow") behaves like the name was omitted.
	arg := func(call goja.FunctionCall, i int) string {
		v := call.Argument(i)
		if goja.IsUndefined(v) || goja.IsNull(v) {
			return ""
		}
		return v.String()
	}

	getFn := func(call goja.FunctionCall) goja.Value {
		// Scope objects may have been mutated since the state was built, so
		// read them back before resolving a non-message scope.
		syncJSScopes(vm, scopes)
		value, err := scopes.get(arg(call, 0), arg(call, 1))
		if err != nil {
			panic(vm.ToValue(vm.NewGoError(err)))
		}
		return vm.ToValue(value)
	}
	if err := vm.Set("get", getFn); err != nil {
		return err
	}

	setFn := func(call goja.FunctionCall) goja.Value {
		exported, err := jsExport(vm, call.Argument(1))
		if err != nil {
			panic(vm.ToValue(vm.NewGoError(err)))
		}
		if err := scopes.set(arg(call, 0), arg(call, 2), exported); err != nil {
			panic(vm.ToValue(vm.NewGoError(err)))
		}
		// Mirror the write into the scope global so a later read of
		// flow.count sees what set("count", 1, "flow") just wrote.
		writeJSScope(vm, scopes, arg(call, 2), arg(call, 0), call.Argument(1))
		return goja.Undefined()
	}
	return vm.Set("set", setFn)
}

// syncJSScopes refreshes the state's maps from the globals, so get() sees
// assignments made directly to the scope objects.
func syncJSScopes(vm *goja.Runtime, scopes *scopeState) {
	if v := jsScope(vm, "vars"); v != nil {
		scopes.vars = v
	}
	if v := jsScope(vm, "flow"); v != nil {
		scopes.flow = v
	}
	if v := jsScope(vm, "global"); v != nil {
		scopes.global = v
	}
}

// writeJSScope mirrors a set() into the matching scope global. Message writes
// have no global to mirror into: they are applied to the outgoing message
// after the script ends.
func writeJSScope(vm *goja.Runtime, scopes *scopeState, scope, name string, value goja.Value) {
	name, scope, err := normalize(name, scope)
	if err != nil {
		return
	}
	global := scopeObjectName(scope)
	if global == "" {
		return
	}
	obj := vm.GlobalObject().Get(global)
	if obj == nil {
		return
	}
	target, ok := obj.(*goja.Object)
	if !ok {
		return
	}
	if goja.IsUndefined(value) || goja.IsNull(value) {
		_ = target.Delete(name)
		return
	}
	_ = target.Set(name, value)
}

// installJSGlobals adds the console and http host functions. Nothing else is
// reachable from the VM.
func installJSGlobals(vm *goja.Runtime, ctx context.Context, out *console, bridge *httpBridge) error {
	// console.log and friends stringify their arguments the way a browser
	// console does: objects as JSON, everything else as its string form.
	logFn := func(call goja.FunctionCall) goja.Value {
		parts := make([]string, 0, len(call.Arguments))
		for _, arg := range call.Arguments {
			parts = append(parts, jsStringify(vm, arg))
		}
		out.add(strings.Join(parts, " "))
		return goja.Undefined()
	}
	consoleObj := vm.NewObject()
	for _, name := range []string{"log", "info", "warn", "error", "debug"} {
		if err := consoleObj.Set(name, logFn); err != nil {
			return err
		}
	}
	if err := vm.Set("console", consoleObj); err != nil {
		return err
	}

	// http performs a synchronous request under the network policy. It is
	// synchronous because the VM has no event loop: a promise would never be
	// resolved and scripts would silently return undefined.
	httpFn := func(call goja.FunctionCall) goja.Value {
		req := httpRequest{Method: "GET"}
		if len(call.Arguments) > 0 {
			// Accept both http(url) and http({url, ...}).
			if s, ok := call.Argument(0).Export().(string); ok {
				req.URL = s
			} else if err := jsDecode(vm, call.Argument(0), &req); err != nil {
				panic(vm.ToValue("http: " + err.Error()))
			}
		}
		// A second argument holds options when the first was a URL string.
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) {
			url := req.URL
			if err := jsDecode(vm, call.Argument(1), &req); err != nil {
				panic(vm.ToValue("http: " + err.Error()))
			}
			if req.URL == "" {
				req.URL = url
			}
		}
		resp := bridge.do(ctx, req)
		return vm.ToValue(map[string]any{
			"status":  resp.Status,
			"ok":      resp.OK,
			"headers": resp.Headers,
			"body":    resp.Body,
			"error":   resp.Error,
			// json parses the body on demand so callers can skip it for
			// non-JSON responses without paying for a failed parse.
			"json": func() any {
				var v any
				if err := json.Unmarshal([]byte(resp.Body), &v); err != nil {
					panic(vm.ToValue("http: response body is not JSON: " + err.Error()))
				}
				return v
			},
		})
	}
	return vm.Set("http", httpFn)
}

// jsStringify renders a value for console output.
func jsStringify(vm *goja.Runtime, v goja.Value) string {
	if v == nil || goja.IsUndefined(v) {
		return "undefined"
	}
	if goja.IsNull(v) {
		return "null"
	}
	if _, isObj := v.(*goja.Object); isObj {
		if s, err := jsonStringify(vm, v); err == nil {
			return s
		}
	}
	return v.String()
}

// jsonStringify calls the VM's own JSON.stringify so objects render with the
// same semantics a script would see.
func jsonStringify(vm *goja.Runtime, v goja.Value) (string, error) {
	jsonObj, ok := vm.GlobalObject().Get("JSON").(*goja.Object)
	if !ok {
		return "", errors.New("JSON is unavailable")
	}
	fn, ok := goja.AssertFunction(jsonObj.Get("stringify"))
	if !ok {
		return "", errors.New("JSON.stringify is unavailable")
	}
	res, err := fn(jsonObj, v)
	if err != nil {
		return "", err
	}
	if goja.IsUndefined(res) {
		return "undefined", nil
	}
	return res.String(), nil
}

// jsDecode converts a JS object into a Go struct through JSON so the host
// never holds a reference to a live VM value.
func jsDecode(vm *goja.Runtime, v goja.Value, target any) error {
	s, err := jsonStringify(vm, v)
	if err != nil {
		return err
	}
	if s == "undefined" || s == "" {
		return nil
	}
	return json.Unmarshal([]byte(s), target)
}

// jsExport converts the script's return value into plain Go types. The JSON
// round trip drops functions and cyclic references, which is what we want in a
// value that becomes a message payload and gets persisted to run history.
func jsExport(vm *goja.Runtime, v goja.Value) (any, error) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil, nil
	}
	switch v.ExportType().Kind().String() {
	case "string":
		return v.String(), nil
	case "bool":
		return v.ToBoolean(), nil
	}
	s, err := jsonStringify(vm, v)
	if err != nil {
		return v.Export(), nil
	}
	if s == "undefined" || s == "" {
		return nil, nil
	}
	var result any
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return v.Export(), nil
	}
	return result, nil
}

// jsError converts a goja failure into an error a flow author can act on:
// interrupts become timeout messages and thrown values keep their stack, with
// its frames numbered by the author's lines.
func jsError(err error) error {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		return fmt.Errorf("script timed out: %v", interrupted.Value())
	}
	var ex *goja.Exception
	if errors.As(err, &ex) {
		return fmt.Errorf("script threw: %s", jsStackLines(ex.String()))
	}
	return err
}

// jsWrapperLines is how many lines jsWrapper puts above the user's source.
const jsWrapperLines = 1

// jsSyntaxPosition matches the position goja's parser reports, and
// jsFramePosition the one a stack frame carries.
var (
	jsSyntaxPosition = regexp.MustCompile(`script\.js: Line (\d+):`)
	jsFramePosition  = regexp.MustCompile(`script\.js:(\d+):`)
)

// jsSyntaxMessage reports a parse error against the user's own lines, without
// the wrapper's filename or the line the wrapper adds.
func jsSyntaxMessage(err error) string {
	return jsSyntaxPosition.ReplaceAllStringFunc(err.Error(), func(match string) string {
		line := jsSyntaxPosition.FindStringSubmatch(match)[1]
		return "Line " + jsUserLine(line) + ":"
	})
}

// jsStackLines renumbers the frames of a thrown error's stack to the user's
// own lines. Only frame lines are touched: the message above them is the
// script's own text.
func jsStackLines(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "\tat ") {
			continue
		}
		lines[i] = jsFramePosition.ReplaceAllStringFunc(line, func(match string) string {
			return "script.js:" + jsUserLine(jsFramePosition.FindStringSubmatch(match)[1]) + ":"
		})
	}
	return strings.Join(lines, "\n")
}

// jsUserLine converts a line number in the wrapped source to the user's.
func jsUserLine(wrapped string) string {
	n, err := strconv.Atoi(wrapped)
	if err != nil || n <= jsWrapperLines {
		return wrapped
	}
	return strconv.Itoa(n - jsWrapperLines)
}
