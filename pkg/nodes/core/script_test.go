package core

import (
	"context"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// registerScriptRuntimes installs the always-available runtimes for tests.
// Python is excluded because it needs an external wasm module.
func registerScriptRuntimes(t *testing.T) {
	t.Helper()
	sandbox.Register(sandbox.JSRuntime{})
	sandbox.Register(sandbox.ShellRuntime{})
	sandbox.SetDefaults(sandbox.Limits{}, sandbox.NetworkNone)
}

// TestScriptNodeReadsDeclaredInputsByName covers the point of declaring a
// signature: each socket is readable under the name its author gave it, and
// the first declared port is the one payload/topic/meta come from. Before
// input existed the node picked one message out of a map, so which port a
// script saw was Go map order.
func TestScriptNodeReadsDeclaredInputsByName(t *testing.T) {
	registerScriptRuntimes(t)

	cfg := `{"language":"javascript",` +
		`"inputs":[{"id":"celsius","type":"float"},{"id":"label","type":"string"}],` +
		`"script":"return { c: input.celsius, label: input.label, main: payload };"}`
	ec := newTestCtx(cfg, nil).usePorts(ScriptNode{}.Meta(), cfg)
	outputs, err := ScriptNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{
		"celsius": {Payload: 21.5},
		"label":   {Payload: "attic"},
	})
	require.NoError(t, err, "a script reading its declared inputs must run")

	doc, ok := only(outputs, "out").Payload.(map[string]any)
	require.Truef(t, ok, "payload is %T, want the object the script returned", only(outputs, "out").Payload)

	assert.Equal(t, 21.5, doc["c"], "each socket must be readable under the name its author declared")
	assert.Equal(t, "attic", doc["label"], "each socket must be readable under the name its author declared")
	assert.Equal(t, 21.5, doc["main"],
		"payload comes from the first declared port, so which message a script sees is not Go map order")
}

// TestScriptNodeMergesInputEnvelopes covers the ambient envelope: metadata and
// topic ride on every port whatever its payload type, so a correlation key that
// arrived on a socket the script did not read still leaves on the output. The
// main port wins a conflict, and the group stack is never merged.
func TestScriptNodeMergesInputEnvelopes(t *testing.T) {
	registerScriptRuntimes(t)

	cfg := `{"language":"javascript",` +
		`"inputs":[{"id":"celsius","type":"float"},{"id":"label","type":"string"}],` +
		`"script":"return input.celsius;"}`
	ec := newTestCtx(cfg, nil).usePorts(ScriptNode{}.Meta(), cfg)
	outputs, err := ScriptNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{
		"celsius": {
			Payload: 21.5, Topic: "sensors/attic",
			Meta:  map[string]any{"source": "main"},
			Group: []engine.GroupFrame{{ID: "g1", Index: 2, Count: 4}},
		},
		"label": {
			Payload: "attic",
			Topic:   "labels/attic",
			// request_id arrives on the port the script never reads, and a
			// conflicting source the main port must win.
			Meta:  map[string]any{"request_id": "req-7", "source": "secondary"},
			Group: []engine.GroupFrame{{ID: "other", Index: 0, Count: 1}},
		},
	})
	require.NoError(t, err, "the script must run with both ports supplied")

	out := only(outputs, "out")
	assert.Equal(t, "req-7", out.Meta["request_id"],
		"metadata from a port the script never read must still leave the node, or a correlation key is lost")
	assert.Equal(t, "main", out.Meta["source"], "the main port wins a metadata conflict")
	assert.Equal(t, "sensors/attic", out.Topic, "the topic comes from the main port, not whichever arrived")

	require.Lenf(t, out.Group, 1, "the group stack must come from the main port alone: %+v", out.Group)
	assert.Equal(t, "g1", out.Group[0].ID,
		"merging the other port's stack would invent a fan-out the graph never performed")
}

// TestScriptNodeEmitsResult covers the ordinary case: a script's return value
// becomes the payload, and the rest of the message rides along untouched, so a
// script that computed a value does not also have to rebuild the envelope.
func TestScriptNodeEmitsResult(t *testing.T) {
	registerScriptRuntimes(t)

	ec := newTestCtx(`{"language":"javascript","script":"return { doubled: payload.n * 2 };"}`, nil)
	outputs, err := ScriptNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{
		"in": {Payload: map[string]any{"n": 4}, Topic: "t"},
	})
	require.NoError(t, err, "a script that returns a value must succeed")

	out := only(outputs, "out")
	require.NotNil(t, out, "a successful script emits its return value")

	doc, ok := out.Payload.(map[string]any)
	require.Truef(t, ok, "payload is %T, want the object the script returned", out.Payload)
	assert.Equal(t, float64(8), doc["doubled"], "4 * 2 = 8: the return value becomes the payload")
	assert.Equal(t, "t", out.Topic, "the topic rides along, so a script need not rebuild the envelope")
}

// TestScriptNodeCapturesConsoleAsDebug covers the only way a script author can
// see inside their own code. The sandbox has no terminal, so console output has
// to reach the debug panel or printing is a no-op and debugging is guesswork.
func TestScriptNodeCapturesConsoleAsDebug(t *testing.T) {
	registerScriptRuntimes(t)

	ec := newTestCtx(`{"language":"javascript","script":"console.log('working'); return 1;"}`, nil)
	_, err := ScriptNode{}.Execute(context.Background(), ec, nil)
	require.NoError(t, err, "printing must not fail the script")

	require.NotEmpty(t, ec.debugs, "console output must reach the debug panel, or printing is a no-op")
	lines, ok := ec.debugs[0].payload.([]string)
	require.Truef(t, ok, "debug payload is %#v, want the captured lines", ec.debugs[0].payload)
	require.NotEmpty(t, lines, "the captured output must not be empty")
	assert.Contains(t, lines[0], "working", "the line the script printed must be the line reported")
}

// TestScriptNodeFailsOnScriptError checks the node reports a script failure as
// a node failure. Routing it to the Error output is the engine's job, and only
// when an edge is wired there; see TestRetryExhaustedWithErrorBranch.
func TestScriptNodeFailsOnScriptError(t *testing.T) {
	registerScriptRuntimes(t)

	ec := newTestCtx(`{"language":"javascript","script":"throw new Error('nope');"}`, nil)
	outputs, err := ScriptNode{}.Execute(context.Background(), ec, nil)
	require.Errorf(t, err, "a script that throws must fail the node, got outputs %#v", outputs)
	assert.Contains(t, err.Error(), "nope",
		"the script's own message must survive, or the author sees a failure with no cause")
}

// TestScriptNodeRejectsEmptyScript covers a node with nothing to run, beside
// the working scripts above. An empty script returning nothing would emit a
// message with no payload, which looks like a script that computed null rather
// than one that was never written.
func TestScriptNodeRejectsEmptyScript(t *testing.T) {
	registerScriptRuntimes(t)

	ec := newTestCtx(`{"language":"javascript","script":"   "}`, nil)
	_, err := ScriptNode{}.Execute(context.Background(), ec, nil)
	assert.Error(t, err,
		"a blank script must fail rather than emit an empty payload that looks like a computed null")
}

// TestScriptNodeBashUsesStdoutAsResult pins the bash runtime's contract, which
// is necessarily different: a shell script has no return statement, so its
// stdout is the result. The `log` line is there to prove the two streams are
// kept apart — logging must not end up in the payload.
func TestScriptNodeBashUsesStdoutAsResult(t *testing.T) {
	registerScriptRuntimes(t)

	ec := newTestCtx(`{"language":"bash","script":"log ignored; echo '{\"ok\":true}'"}`, nil)
	outputs, err := ScriptNode{}.Execute(context.Background(), ec, nil)
	require.NoError(t, err, "a bash script printing JSON must succeed")

	doc, ok := only(outputs, "out").Payload.(map[string]any)
	require.Truef(t, ok, "payload is %#v, want the JSON the script printed", only(outputs, "out").Payload)
	assert.Equal(t, true, doc["ok"],
		"stdout is the bash result, and the logged line must not have been mixed into it")
}

// TestScriptNodeExamplesAreValid runs the starter scripts the editor drops into
// a new script node. They are the first code a new user sees, so an example
// that does not run — or that reads a field the sandbox does not actually
// expose — is a broken first impression that no other test would catch.
func TestScriptNodeExamplesAreValid(t *testing.T) {
	registerScriptRuntimes(t)

	for _, lang := range []string{sandbox.LangJavaScript, sandbox.LangBash} {
		t.Run(lang, func(t *testing.T) {
			source, ok := scriptExamples[lang]
			require.Truef(t, ok, "every offered language must ship a starter script, %s has none", lang)

			result, err := sandbox.Run(context.Background(), sandbox.Request{
				Language: lang,
				Source:   source,
				Payload:  map[string]any{"temperature": 31.5},
				Topic:    "sensors/office",
				Network:  sandbox.NetworkNone,
			})
			require.NoError(t, err, "the starter script is the first code a new user runs and must work")

			doc, ok := result.Value.(map[string]any)
			require.Truef(t, ok, "the example returned %T, want an object: %#v", result.Value, result.Value)
			assert.Equal(t, float64(31.5), doc["celsius"],
				"the example must read the payload the sandbox actually supplies, not a field it imagines")
		})
	}
}

// TestScriptNodeAppliesVars covers the script node's half of run variables:
// the current values go in, and what the script leaves behind is written back.
func TestScriptNodeAppliesVars(t *testing.T) {
	ec := newTestCtx(`{"language":"javascript","script":"vars.out = vars.in + 1; vars.gone = null; return 'done';"}`, nil)
	ec.SetVar("in", float64(41))
	ec.SetVar("gone", "still here")

	out, err := ScriptNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {}})
	require.NoError(t, err, "a script reading and writing variables must run")

	assert.Equal(t, "done", only(out, "out").Payload, "the return value is still the payload")

	written, _ := ec.GetVar("out")
	assert.Equal(t, float64(42), written, "41 + 1 = 42: what the script left in vars must be written back")

	_, present := ec.GetVar("gone")
	assert.False(t, present, "assigning null is the only way a script can remove a variable, so it must remove it")
}

// TestScriptNodeFailureDropsVars covers the boundary: a script that raises
// hands back no result, so its variable writes are discarded with it.
func TestScriptNodeFailureDropsVars(t *testing.T) {
	ec := newTestCtx(`{"language":"javascript","script":"vars.written = true; throw new Error('nope');"}`, nil)
	_, err := ScriptNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {}})
	require.Error(t, err, "the script throws after writing, so the node must fail")

	_, present := ec.GetVar("written")
	assert.False(t, present,
		"a script that raised hands back no result, so its half-finished writes must be discarded with it")
}
