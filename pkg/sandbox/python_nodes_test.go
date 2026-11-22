package sandbox

import (
	"errors"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPythonNodeCapabilityIsOptIn keeps standalone and unrelated Python
// sandbox users off the engine capability, including its RPC file.
func TestPythonNodeCapabilityIsOptIn(t *testing.T) {
	result, err := run(t, LangPython, `
import karakuri
try:
    open("/rpc/node").read()
    rpc = True
except FileNotFoundError:
    rpc = False
result = {
    "global": "nodes" in globals(),
    "module": hasattr(karakuri, "nodes"),
    "rpc": rpc,
}
`, Request{})
	require.NoError(t, err)
	doc, ok := result.Value.(map[string]any)
	require.True(t, ok, "the probe returns the three capability observations")
	assert.Equal(t, false, doc["global"], "a non-flow script must not receive a nodes global")
	assert.Equal(t, false, doc["module"], "karakuri.nodes must be absent rather than disabled")
	assert.Equal(t, false, doc["rpc"], "without an invoker the node RPC file must not exist")
}

// TestPythonNodeFunctionsAndBinaryOutputs covers exact, alias, and generic
// calls through real CPython, including complete message envelopes, an empty
// output array, forwarded invocation options, and the base64 binary boundary.
func TestPythonNodeFunctionsAndBinaryOutputs(t *testing.T) {
	invoker := &nodeInvokerFixture{
		metas: []nodeapi.NodeMeta{{
			Type: "test-node", Retryable: true,
			Inputs:  []nodeapi.PortSpec{{ID: "in", Type: "any"}},
			Outputs: []nodeapi.PortSpec{{ID: "out", Type: "binary"}, {ID: "empty", Type: "any"}},
			ConfigSpec: []nodeapi.FieldSpec{
				{ID: "form", Type: "string"},
				{ID: "factor", Type: "number", Port: "number"},
			},
		}},
		result: &nodeapi.NodeCallResult{
			NodeType: "test-node", Sequence: 1, Attempts: 1,
			Outputs: map[string][]*nodeapi.Msg{
				"out": {{
					ID: "message-id", Payload: []byte("hi"), Topic: "binary", Continuation: true,
					Meta:  map[string]any{"source": "call"},
					Group: []nodeapi.GroupFrame{{ID: "g", Index: 2, Count: 4}},
				}},
				"empty": nil,
			},
		},
	}
	result, err := run(t, LangPython, `
import karakuri
canonical = karakuri.nodes["test-node"]({"form": "canonical"})
alias = nodes.testNode({"form": "alias"}, None, {"id": "alias"})
dynamic = nodes.call(
    "test-node",
    {"form": "dynamic"},
    {"in": {
        "id": "input-id",
        "payload": {"value": 7},
        "topic": "input-topic",
        "meta": {"origin": "python"},
    }},
    {
        "id": "stable",
        "params": {"factor": 2},
        "retry": {
            "max_attempts": 3,
            "delay_ms": 10,
            "backoff": 2,
            "max_delay_ms": 50,
            "jitter": 0.1,
        },
    },
)
message = canonical["out"][0]
result = {
    "same_binding": karakuri.nodes is nodes,
    "canonical": message["payload"],
    "alias": alias["out"][0]["payload"],
    "dynamic": dynamic["out"][0]["payload"],
    "empty": len(alias["empty"]),
    "id": message["id"],
    "topic": message["topic"],
    "source": message["meta"]["source"],
    "group": [message["group"][0]["index"], message["group"][0]["count"]],
    "continuation": message["continuation"],
}
`, Request{NodeInvoker: invoker})
	require.NoError(t, err)
	assert.Equal(t, 3, invoker.calls, "each Python spelling must admit exactly one call")
	doc, ok := result.Value.(map[string]any)
	require.True(t, ok, "the script returns its output observations as an object")
	assert.Equal(t, true, doc["same_binding"], "the global and module attribute must share one catalog binding")
	assert.Equal(t, "aGk=", doc["canonical"], "binary output must retain the sandbox base64 representation")
	assert.Equal(t, "aGk=", doc["alias"])
	assert.Equal(t, "aGk=", doc["dynamic"])
	assert.Equal(t, float64(0), doc["empty"], "a nil host slice must still cross as an empty output array")
	assert.Equal(t, "message-id", doc["id"])
	assert.Equal(t, "binary", doc["topic"])
	assert.Equal(t, "call", doc["source"])
	assert.Equal(t, []any{float64(2), float64(4)}, doc["group"], "group position and size must survive the JSON boundary")
	assert.Equal(t, true, doc["continuation"])

	assert.Equal(t, "test-node", invoker.lastCall.Type, "an alias or generic name must resolve to the exact type")
	assert.JSONEq(t, `{"form":"dynamic"}`, string(invoker.lastCall.Config))
	input := invoker.lastCall.Inputs["in"]
	require.NotNil(t, input, "the complete input message must be forwarded")
	assert.Equal(t, "input-id", input.ID)
	assert.Equal(t, map[string]any{"value": float64(7)}, input.Payload)
	assert.Equal(t, "input-topic", input.Topic)
	assert.Equal(t, map[string]any{"origin": "python"}, input.Meta)
	assert.Equal(t, "stable", invoker.lastCall.Options.ID)
	assert.Equal(t, map[string]any{"factor": float64(2)}, invoker.lastCall.Options.Params)
	require.NotNil(t, invoker.lastCall.Options.Retry)
	assert.Equal(t, 3, invoker.lastCall.Options.Retry.MaxAttempts)
	assert.Equal(t, 10, invoker.lastCall.Options.Retry.DelayMs)
	assert.Equal(t, float64(2), invoker.lastCall.Options.Retry.Backoff)
	assert.Equal(t, 50, invoker.lastCall.Options.Retry.MaxDelayMs)
	assert.Equal(t, 0.1, invoker.lastCall.Options.Retry.Jitter)
}

// TestPythonNodeErrorsAreStructured verifies that host and local failures are
// catchable with invocation evidence, while malformed calls never reach the
// invoker's admission budget or a node action.
func TestPythonNodeErrorsAreStructured(t *testing.T) {
	t.Run("host failure", func(t *testing.T) {
		invoker := &nodeInvokerFixture{
			metas: []nodeapi.NodeMeta{{Type: "test-node"}},
			err: &nodeapi.NodeCallError{
				NodeType: "test-node", InvocationID: "stable", Sequence: 2, Attempts: 3,
				Retryable: true, RetryAfterMS: 25, Details: map[string]any{"kind": "temporary"},
				Fatal: true, Cause: errors.New("boom"),
			},
		}
		result, err := run(t, LangPython, `
import karakuri
try:
    nodes.testNode({}, None, {"id": "stable"})
except karakuri.NodeCallError as error:
    result = {
        "message": error.message,
        "string": str(error),
        "node_type": error.node_type,
        "invocation_id": error.invocation_id,
        "sequence": error.sequence,
        "attempts": error.attempts,
        "retryable": error.retryable,
        "retry_after_ms": error.retry_after_ms,
        "kind": error.details["kind"],
        "fatal": error.fatal,
    }
`, Request{NodeInvoker: invoker})
		require.NoError(t, err)
		assert.Equal(t, 1, invoker.calls)
		doc, ok := result.Value.(map[string]any)
		require.True(t, ok, "the caught exception fields must be returned as an object")
		assert.Equal(t, "boom", doc["message"], "the explicit JSON message must carry the non-serializable Cause")
		assert.Equal(t, "boom", doc["string"], "str(error) must be the node failure message")
		assert.Equal(t, "test-node", doc["node_type"])
		assert.Equal(t, "stable", doc["invocation_id"])
		assert.Equal(t, float64(2), doc["sequence"])
		assert.Equal(t, float64(3), doc["attempts"])
		assert.Equal(t, true, doc["retryable"])
		assert.Equal(t, float64(25), doc["retry_after_ms"])
		assert.Equal(t, "temporary", doc["kind"])
		assert.Equal(t, true, doc["fatal"])
	})

	t.Run("local argument failures", func(t *testing.T) {
		invoker := &nodeInvokerFixture{metas: []nodeapi.NodeMeta{{Type: "test-node"}}}
		result, err := run(t, LangPython, `
import karakuri
errors = []
calls = [
    lambda: nodes.testNode(),
    lambda: nodes.testNode({}, []),
    lambda: nodes.testNode({}, None, {"maximum": 3}),
    lambda: nodes.testNode({}, None, {"retry": {"maximum": 3}}),
    lambda: nodes.testNode({"bad": float("nan")}),
]
for call in calls:
    try:
        call()
    except karakuri.NodeCallError as error:
        errors.append({"message": error.message, "attempts": error.attempts})
result = errors
`, Request{NodeInvoker: invoker})
		require.NoError(t, err)
		assert.Zero(t, invoker.calls, "argument and JSON validation must finish before invocation admission")
		docs, ok := result.Value.([]any)
		require.True(t, ok, "each rejected call must return its catchable error")
		require.Len(t, docs, 5, "all five malformed calls must be caught")
		wantMessages := []string{"config", "inputs", "maximum", "maximum", "JSON"}
		for index, raw := range docs {
			doc, ok := raw.(map[string]any)
			require.True(t, ok, "error %d retains its fields", index)
			assert.Contains(t, doc["message"], wantMessages[index], "error %d must name the invalid argument", index)
			assert.Equal(t, float64(0), doc["attempts"], "local rejection has no execution attempts")
		}
	})
}

// TestDecodePythonNodeCallChecksOptions pins the host's independent wire
// validation beside a valid retry document. Guest-side checks cannot be the
// only defense against unknown fields being discarded by Go's JSON decoder.
func TestDecodePythonNodeCallChecksOptions(t *testing.T) {
	tests := []struct {
		name    string
		request string
		wantErr string
	}{
		{
			name:    "valid null inputs and retry policy",
			request: `{"type":"test-node","config":{},"inputs":null,"options":{"id":"stable","retry":{"max_attempts":2}}}`,
		},
		{
			name:    "unknown invocation option",
			request: `{"type":"test-node","config":{},"options":{"maximum":2}}`,
			wantErr: "maximum",
		},
		{
			name:    "unknown retry option",
			request: `{"type":"test-node","config":{},"options":{"retry":{"maximum":2}}}`,
			wantErr: "maximum",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			call, err := decodePythonNodeCall([]byte(tc.request))
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "test-node", call.Type)
			assert.Equal(t, "stable", call.Options.ID)
			require.NotNil(t, call.Options.Retry)
			assert.Equal(t, 2, call.Options.Retry.MaxAttempts)
		})
	}
}
