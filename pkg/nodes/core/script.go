package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
	"go.uber.org/zap"
)

// ScriptNode runs a user-supplied script in a sandboxed interpreter and emits
// its return value as the message payload. The interpreters have no access to
// the host filesystem, process table, or environment; outbound network calls
// are the one capability they are granted, and the server's script.network
// policy decides how far those reach.
type ScriptNode struct{}

func (ScriptNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "script",
		Label:       "Script",
		Folder:      flow.FolderLogic,
		Description: "Runs a sandboxed script and emits its result",
		Icon:        "las la-code",
		// A script declares its own signature, so its ports come from config
		// rather than from this metadata; see flow.ScriptPorts. The specs below
		// are the shape an undeclared script keeps.
		DynamicPorts: true,
		// A script is pulled like any other value source. What it does inside is
		// the author's to decide, and a pulled result is memoized for the run,
		// so an HTTP call in a script runs once however many parameters read it.
		Pure: true,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny},
			{ID: flow.ScriptErrorPort, Label: "Error", Type: flow.PortAny, Kind: flow.PortKindError, TypeFrom: "in"},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:       "language",
				Label:    "Language",
				Type:     flow.FieldSelect,
				Required: true,
				Default:  sandbox.LangJavaScript,
				Options: []engine.Option{
					{Value: sandbox.LangJavaScript, Label: "JavaScript"},
					{Value: sandbox.LangPython, Label: "Python"},
					{Value: sandbox.LangBash, Label: "Bash"},
				},
				Help: "JavaScript and Bash are always available. Python needs the CPython WebAssembly module installed on the server.",
			},
			{
				ID:            "script",
				Label:         "Script",
				Type:          flow.FieldScript,
				Required:      true,
				LanguageField: "language",
				Examples:      scriptExamples,
				Help:          "Receives payload, topic, and meta from the first input port, and input.<port> for every port by name. Return a value to emit it as the new payload. get(name, scope) and set(name, value, scope) read and write variables, defaulting to the message payload; the scopes are msg, flow, global, and run.",
			},
			{
				ID:    "inputs",
				Label: "Inputs",
				Type:  flow.FieldPorts,
				// The default spells out the implicit port a new node starts
				// with, so the signature is visible and editable in the config
				// panel rather than only appearing once something is declared.
				Default: []any{
					map[string]any{"id": "in", "label": "In", "type": flow.PortAny},
				},
				Help: "Ports this script accepts, readable as input.<id>. The first port also supplies payload, topic, and meta. Remove them all for a single input that takes anything.",
			},
			{
				ID:    "outputs",
				Label: "Outputs",
				Type:  flow.FieldPorts,
				Default: []any{
					map[string]any{"id": "out", "label": "Out", "type": flow.PortAny},
				},
				Help: "Ports this script emits. Remove them all for a single untyped output. With more than one, return an object keyed by port ID.",
			},
			{
				ID:          "timeout",
				Label:       "Timeout (seconds)",
				Type:        flow.FieldNumber,
				Placeholder: "30",
				Help:        "Per-execution time budget. Leave empty to use the server default.",
			},
		},
		Retryable: true,
	}
}

// scriptExamples are the starter scripts the editor prefills per language.
// Each one demonstrates the same thing: read the payload, log something, and
// return a value, with the rest of the signature — the other input ports and
// the other output ports — shown commented out beside it, since a starter
// script runs against the default single-port shape.
var scriptExamples = map[string]string{
	sandbox.LangJavaScript: `// payload, topic, and meta hold the message on the first input port.
// Return a value to emit it as the new payload.

console.log("received", payload);

const reading = Number(payload.temperature ?? 0);

// Reading another input port: declare a port "threshold" under Inputs and
// read it as input.threshold. Only the first port arrives as payload; the
// rest are reachable through input alone, and an unconnected port is
// undefined, so give it a fallback.
// const limit = Number(input.threshold ?? 30);

// get(name, scope) and set(name, value, scope) read and write variables.
// The scope defaults to "msg" and the name to "payload", so get() is the
// payload. Use "flow" and "global" for values that outlive this run, and
// "run" to share with other nodes outside this message's path.
// set("count", (get("count", "flow") || 0) + 1, "flow");

// http(url) or http({method, url, headers, body}) performs a request
// under the server's network policy.
// const res = http("https://api.example.com/status");
// console.log(res.status, res.json());

// Emitting on another output port: declare "hot" and "cold" under Outputs
// and return an object keyed by port ID. A key left out is a branch not
// taken, so this sends the reading down one side and nothing down the other.
// return reading > 30 ? { hot: reading } : { cold: reading };

return {
  celsius: reading,
  fahrenheit: reading * 9 / 5 + 32,
  hot: reading > 30,
  topic: topic,
};
`,
	sandbox.LangPython: `# payload, topic, and meta hold the message on the first input port.
# Set "result" to emit it as the new payload.
import karakuri

print("received", payload)

reading = float(payload.get("temperature", 0))

# Reading another input port: declare a port "threshold" under Inputs and
# read it as input["threshold"]. Only the first port arrives as payload; the
# rest are reachable through input alone, and an unconnected port is missing
# from the dict, so read it with a fallback. input shadows the builtin
# input(), which stays reachable as builtins.input.
# limit = float(input.get("threshold", 30))

# get(name, scope) and set(name, value, scope) read and write variables.
# The scope defaults to "msg" and the name to "payload". Use "flow" and
# "global" for values that outlive this run, and "run" to share with other
# nodes outside this message's path.
# set("count", (get("count", "flow") or 0) + 1, "flow")

# karakuri.get / karakuri.post perform HTTP requests under the server's
# network policy. They are the request helpers, not the variable ones.
# res = karakuri.get("https://api.example.com/status")
# print(res.status, res.json())

# Emitting on another output port: declare "hot" and "cold" under Outputs
# and set result to a dict keyed by port ID. A key left out is a branch not
# taken, so this sends the reading down one side and nothing down the other.
# result = {"hot": reading} if reading > 30 else {"cold": reading}

result = {
    "celsius": reading,
    "fahrenheit": reading * 9 / 5 + 32,
    "hot": reading > 30,
    "topic": topic,
}
`,
	sandbox.LangBash: `# $PAYLOAD holds the first input port's message as JSON, $TOPIC the topic,
# $META the metadata.
# Anything printed to stdout becomes the new payload; use "log" for messages.

log "received $PAYLOAD"

reading=$(jsonget temperature "$PAYLOAD")
[ -z "$reading" ] && reading=0

# Reading another input port: declare a port "threshold" under Inputs and
# read it out of $INPUT, which holds every connected port keyed by name.
# Only the first port arrives as $PAYLOAD, and jsonget prints nothing for an
# unconnected port, so fill in a fallback.
# limit=$(jsonget threshold "$INPUT")
# [ -z "$limit" ] && limit=30

# getvar and setvar read and write variables, defaulting to the message.
# Pass -s to pick a scope: msg, flow, global, or run.
# count=$(getvar -s flow count)
# setvar -s flow count $((count+1))

# "http <method> <url> [body] [header:value ...]" performs a request under
# the server's network policy and prints the response body.
# body=$(http GET https://api.example.com/status)

# Emitting on another output port: declare "hot" and "cold" under Outputs and
# print an object keyed by port ID. A key left out is a branch not taken, so
# this sends the reading down one side and nothing down the other. The shell
# test is integer-only, so trim any decimal before comparing.
# if [ "${reading%%.*}" -gt 30 ]; then
#   printf '{"hot": %s}\n' "$reading"
# else
#   printf '{"cold": %s}\n' "$reading"
# fi

printf '{"celsius": %s, "topic": "%s"}\n' "$reading" "$TOPIC"
`,
}

// scriptConfig is the decoded script node configuration.
type scriptConfig struct {
	Language string `json:"language"`
	Script   string `json:"script"`
	Timeout  int    `json:"timeout"`
	// Inputs and Outputs are the author's port declaration, which gives the
	// node its signature on the canvas and decides how its result is routed.
	Inputs  []flow.ScriptPort `json:"inputs"`
	Outputs []flow.ScriptPort `json:"outputs"`
}

// Execute runs the script against the incoming message. Console output is
// emitted as a debug event so an author can watch a script's logs in the run
// panel without wiring a debug node.
func (ScriptNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (outputs map[string][]*engine.Msg, retErr error) {
	var calls *scriptCallTracker
	defer func() {
		if calls == nil || retErr == nil {
			return
		}
		if fatal := calls.fatalError(); fatal != nil {
			retErr = fatal
		}
		if calls.started() || calls.fatalError() != nil {
			outputs = nil
			retErr = &engine.ExecutionError{Message: retErr.Error(), Retryable: false, Cause: retErr}
		}
	}()

	var cfg scriptConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Script) == "" {
		return nil, errors.New("script node has no script")
	}
	if cfg.Language == "" {
		cfg.Language = sandbox.LangJavaScript
	}

	// The main port supplies payload/topic/meta, and every connected port is
	// also readable by the name its author gave it.
	msg := inbound(ec, inputs)
	fullMessage := mainInput(ec.Ports(), inputs, inputOrder(ec.Ports(), inputs))
	named := namedInputs(inputs)

	// Build the execution request from the server policy, overriding the
	// timeout when the node configures a shorter or longer budget.
	limits, policy := sandbox.Defaults()
	if cfg.Timeout > 0 {
		limits.Timeout = time.Duration(cfg.Timeout) * time.Second
	}

	// Every scope is an initial snapshot. Canonical before/after diffs later
	// apply only script changes, leaving writes made reentrantly by called nodes
	// intact. Called nodes still see these snapshots, not uncommitted JS writes.
	runInitial := ec.Vars()
	flowInitial := ec.ScopedVars(engine.ScopeFlow)
	globalInitial := ec.ScopedVars(engine.ScopeGlobal)
	runBefore := snapshotScriptScope(runInitial)
	flowBefore := snapshotScriptScope(flowInitial)
	globalBefore := snapshotScriptScope(globalInitial)
	calls = newScriptCallTracker(ec)

	result, err := sandbox.Run(ctx, sandbox.Request{
		Language:    cfg.Language,
		Source:      cfg.Script,
		Payload:     msg.Payload,
		Topic:       msg.Topic,
		Meta:        msg.Meta,
		Message:     fullMessage,
		Inputs:      named,
		Vars:        runInitial,
		Flow:        flowInitial,
		Global:      globalInitial,
		Limits:      limits,
		Network:     policy,
		NodeInvoker: calls.capability(cfg.Language),
	})

	// Surface console output either way: a failing script's logs are usually
	// the fastest route to understanding why it failed.
	if result != nil && len(result.Output) > 0 {
		ec.Debug(result.Output, cfg.Language+" output")
	}

	// Count exhaustion is fatal even when JavaScript caught the exception. Do
	// not commit staged variables or process a result after that boundary.
	if fatal := calls.fatalError(); fatal != nil {
		return nil, fatal
	}

	// A failure is returned as a failure, the same as any other node: the
	// engine honors the retry policy and then routes to the Error output when
	// something is wired to it, or fails the run when nothing is. The deferred
	// tracker makes any failure after a node call nonretryable.
	if err != nil {
		ec.Logger().Debug("script failed",
			zap.String("language", cfg.Language), zap.Error(err))
		return nil, err
	}

	// Interpreter failures never reach writeback, even if a runtime returns
	// partial output alongside its error.
	if result != nil {
		applyScriptVars(ec, result, runBefore, flowBefore, globalBefore)
	}

	// Apply message-scope writes, then the return value. A script that both
	// sets payload and returns gets the return value, which is the older and
	// more visible of the two ways to say what the payload should be.
	out := passThrough(msg)
	for _, write := range result.MsgWrites {
		if err := setProperty(out, write.Path, write.Value); err != nil {
			return nil, fmt.Errorf("script wrote to %q: %w", write.Path, err)
		}
	}
	if result.Value != nil {
		out.Payload = result.Value
	}
	return scriptOutputs(cfg, out, result.Value)
}

// scriptOutputs routes a script's result to the ports it declared. One output
// takes the value as its payload, which is what a script has always done. More
// than one means the script chose to fan out, so its return value is read as an
// object keyed by port ID: a key it did not set is a branch not taken.
func scriptOutputs(cfg scriptConfig, out *engine.Msg, value any) (map[string][]*engine.Msg, error) {
	_, declared := flow.ScriptPortsFrom(cfg.Inputs, cfg.Outputs)
	// The error port is appended by ScriptPorts and is never a result port.
	var ports []flow.PortSpec
	for _, p := range declared {
		if p.Kind != flow.PortKindError {
			ports = append(ports, p)
		}
	}
	if len(ports) <= 1 {
		port := "out"
		if len(ports) == 1 {
			port = ports[0].ID
			if err := enforceScriptType(ports[0], out); err != nil {
				return nil, err
			}
		}
		return engine.One(port, out), nil
	}

	byPort, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("script declares %d outputs, so it must return an object keyed by port ID, got %T",
			len(ports), value)
	}
	result := make(map[string][]*engine.Msg, len(ports))
	for _, p := range ports {
		branch, present := byPort[p.ID]
		if !present || branch == nil {
			continue
		}
		msg := passThrough(out)
		msg.Payload = branch
		if err := enforceScriptType(p, msg); err != nil {
			return nil, err
		}
		result[p.ID] = []*engine.Msg{msg}
	}
	return result, nil
}

// enforceScriptType checks an emitted payload against the declared port type,
// which is what makes a script's signature a contract rather than a hope: a
// value that does not match fails the node onto its error branch instead of
// flowing on mislabelled. Binary is the one coercion — scripts hand bytes
// across as base64 text (sandbox.encodeBinary), so a binary port decodes the
// text into the bytes it promises downstream.
func enforceScriptType(spec flow.PortSpec, msg *engine.Msg) error {
	mismatch := func() error {
		return fmt.Errorf("output %q declares %s, but the script produced %T", spec.ID, spec.Type, msg.Payload)
	}
	switch spec.Type {
	case "", flow.PortAny:
		return nil
	case flow.PortString:
		if _, ok := msg.Payload.(string); !ok {
			return mismatch()
		}
	case flow.PortBool:
		if _, ok := msg.Payload.(bool); !ok {
			return mismatch()
		}
	case flow.PortNumber, flow.PortFloat:
		if _, ok := toNumber(msg.Payload); !ok {
			return mismatch()
		}
	case flow.PortInteger:
		n, ok := toNumber(msg.Payload)
		if !ok {
			return mismatch()
		}
		if n != math.Trunc(n) {
			return fmt.Errorf("output %q declares integer, but the script produced %v", spec.ID, n)
		}
	case flow.PortObject:
		switch msg.Payload.(type) {
		case map[string]any, []any:
		default:
			return mismatch()
		}
	case flow.PortBinary:
		switch t := msg.Payload.(type) {
		case []byte:
		case string:
			decoded, err := decodeBase64(t, "")
			if err != nil {
				return fmt.Errorf("output %q declares binary, but the script produced text that is not base64: %w", spec.ID, err)
			}
			msg.Payload = decoded
		default:
			return mismatch()
		}
	}
	return nil
}

// applyScriptVars writes only values that differ canonically from the script's
// initial snapshots. This preserves concurrent writes made by called nodes.
// Run variables keep their existing rule: null clears a key, while deleting a
// run-scope property outright leaves it alone.
func applyScriptVars(ec engine.ExecContext, result *sandbox.Result, runBefore, flowBefore, globalBefore canonicalScriptScope) {
	for key, value := range result.Vars {
		if !runBefore.changed(key, value) {
			continue
		}
		ec.SetVar(key, value)
	}
	applyPersistentScope(ec, engine.ScopeFlow, flowBefore, result.Flow)
	applyPersistentScope(ec, engine.ScopeGlobal, globalBefore, result.Global)
}

// applyPersistentScope writes changed values and clears keys the script
// dropped. A runtime that returned nothing for the scope is treated as "no
// writes" rather than "cleared everything", so collection failure never wipes
// stored state.
func applyPersistentScope(ec engine.ExecContext, scope string, before canonicalScriptScope, after map[string]any) {
	if after == nil {
		return
	}
	for key, value := range after {
		if !before.changed(key, value) {
			continue
		}
		_ = ec.SetScoped(scope, key, value)
	}
	for key := range before {
		if _, still := after[key]; !still {
			_ = ec.SetScoped(scope, key, nil)
		}
	}
}

// canonicalScriptValue is an immutable JSON fingerprint of one initial value.
// Valid is false when the host value could not cross the JSON boundary.
type canonicalScriptValue struct {
	encoded string
	valid   bool
}

// canonicalScriptScope preserves initial fingerprints independently of the
// mutable request maps handed to goja.
type canonicalScriptScope map[string]canonicalScriptValue

// snapshotScriptScope canonicalizes the initial values before the interpreter
// can mutate any nested map or slice shared with its request.
func snapshotScriptScope(values map[string]any) canonicalScriptScope {
	before := make(canonicalScriptScope, len(values))
	for key, value := range values {
		encoded, err := json.Marshal(value)
		before[key] = canonicalScriptValue{encoded: string(encoded), valid: err == nil}
	}
	return before
}

// changed reports whether an after value differs canonically or is new.
func (s canonicalScriptScope) changed(key string, after any) bool {
	before, existed := s[key]
	if !existed {
		return true
	}
	encoded, err := json.Marshal(after)
	return err != nil || !before.valid || before.encoded != string(encoded)
}

// ScriptLanguageStatus reports each language and whether its runtime can run
// on this server, for the editor to show alongside the language picker.
func ScriptLanguageStatus() map[string]string {
	status := make(map[string]string)
	for _, lang := range []string{sandbox.LangJavaScript, sandbox.LangPython, sandbox.LangBash} {
		rt, ok := sandbox.Get(lang)
		if !ok {
			status[lang] = "not registered"
			continue
		}
		if err := rt.Available(); err != nil {
			status[lang] = err.Error()
			continue
		}
		status[lang] = ""
	}
	return status
}
