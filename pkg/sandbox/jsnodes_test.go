package sandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nodeInvokerFixture records admissions and returns configured invocation data.
type nodeInvokerFixture struct {
	metas    []nodeapi.NodeMeta
	result   *nodeapi.NodeCallResult
	err      error
	calls    int
	lastCall nodeapi.NodeCall
}

func (i *nodeInvokerFixture) ScriptNodeFunctions() []nodeapi.NodeMeta { return i.metas }
func (i *nodeInvokerFixture) CallScriptNode(ctx context.Context, call nodeapi.NodeCall) (*nodeapi.NodeCallResult, error) {
	i.calls++
	i.lastCall = call
	return i.result, i.err
}

// TestJavaScriptNodeCapabilityIsOptIn keeps standalone and unrelated sandbox
// users on the existing global surface.
func TestJavaScriptNodeCapabilityIsOptIn(t *testing.T) {
	result, err := run(t, LangJavaScript, `return typeof nodes;`, Request{})
	require.NoError(t, err)
	assert.Equal(t, "undefined", result.Value)
}

// TestJavaScriptNodeFunctionsAndBinaryOutputs covers canonical, alias, and
// dynamic calls, optional arguments, empty arrays, and base64 binary results.
func TestJavaScriptNodeFunctionsAndBinaryOutputs(t *testing.T) {
	invoker := &nodeInvokerFixture{
		metas: []nodeapi.NodeMeta{{
			Type:    "test-node",
			Outputs: []nodeapi.PortSpec{{ID: "out", Type: "binary"}, {ID: "empty", Type: "any"}},
		}},
		result: &nodeapi.NodeCallResult{
			NodeType: "test-node", Sequence: 1, Attempts: 1,
			Outputs: map[string][]*nodeapi.Msg{
				"out": {{
					Payload: []byte("hi"), Topic: "binary", Continuation: true,
					Meta:  map[string]any{"source": "call"},
					Group: []nodeapi.GroupFrame{{ID: "g", Index: 2, Count: 4}},
				}},
				"empty": {},
			},
		},
	}
	result, err := run(t, LangJavaScript, `
		const canonical = nodes["test-node"]({});
		const alias = nodes.testNode({}, undefined, { id: "stable" });
		const dynamic = nodes.call("test-node", {}, undefined, { id: "stable" });
		return {
			canonical: canonical.out[0].payload,
			alias: alias.out[0].payload,
			dynamic: dynamic.out[0].payload,
			empty: alias.empty.length,
			topic: canonical.out[0].topic,
			source: canonical.out[0].meta.source,
			group: canonical.out[0].group[0].index,
			continuation: canonical.out[0].continuation,
		};
	`, Request{NodeInvoker: invoker})
	require.NoError(t, err)
	assert.Equal(t, 3, invoker.calls)
	doc := result.Value.(map[string]any)
	assert.Equal(t, "aGk=", doc["canonical"])
	assert.Equal(t, "aGk=", doc["alias"])
	assert.Equal(t, "aGk=", doc["dynamic"])
	assert.Equal(t, float64(0), doc["empty"])
	assert.Equal(t, "binary", doc["topic"])
	assert.Equal(t, "call", doc["source"])
	assert.Equal(t, float64(2), doc["group"])
	assert.Equal(t, true, doc["continuation"])
	assert.Equal(t, "stable", invoker.lastCall.Options.ID)
}

// TestJavaScriptNodeErrorsAreStructured verifies that execution failures and
// local argument failures are both catchable NodeCallError objects.
func TestJavaScriptNodeErrorsAreStructured(t *testing.T) {
	t.Run("execution", func(t *testing.T) {
		invoker := &nodeInvokerFixture{
			metas: []nodeapi.NodeMeta{{Type: "test-node"}},
			err: &nodeapi.NodeCallError{
				NodeType: "test-node", InvocationID: "stable", Sequence: 2, Attempts: 3,
				Retryable: true, RetryAfterMS: 25, Details: map[string]any{"kind": "temporary"},
				Cause: errors.New("boom"),
			},
		}
		result, err := run(t, LangJavaScript, `
			try { nodes.testNode({}); }
			catch (error) {
				return {
					name: error.name, message: error.message, nodeType: error.nodeType,
					invocationId: error.invocationId, sequence: error.sequence,
					attempts: error.attempts, retryable: error.retryable,
					retryAfterMs: error.retryAfterMs, kind: error.details.kind,
				};
			}
		`, Request{NodeInvoker: invoker})
		require.NoError(t, err)
		doc := result.Value.(map[string]any)
		assert.Equal(t, "NodeCallError", doc["name"])
		assert.Contains(t, doc["message"], "boom")
		assert.Equal(t, "test-node", doc["nodeType"])
		assert.Equal(t, "stable", doc["invocationId"])
		assert.Equal(t, float64(2), doc["sequence"])
		assert.Equal(t, float64(3), doc["attempts"])
		assert.Equal(t, true, doc["retryable"])
		assert.Equal(t, float64(25), doc["retryAfterMs"])
		assert.Equal(t, "temporary", doc["kind"])
	})

	t.Run("config required", func(t *testing.T) {
		invoker := &nodeInvokerFixture{metas: []nodeapi.NodeMeta{{Type: "test-node"}}}
		result, err := run(t, LangJavaScript, `
			try { nodes.testNode(); }
			catch (error) { return { name: error.name, attempts: error.attempts, message: error.message }; }
		`, Request{NodeInvoker: invoker})
		require.NoError(t, err)
		assert.Zero(t, invoker.calls, "argument validation happens before admission or action")
		doc := result.Value.(map[string]any)
		assert.Equal(t, "NodeCallError", doc["name"])
		assert.Equal(t, float64(0), doc["attempts"])
		assert.Contains(t, doc["message"], "config")
	})

	t.Run("computed unknown option", func(t *testing.T) {
		invoker := &nodeInvokerFixture{metas: []nodeapi.NodeMeta{{Type: "test-node"}}}
		result, err := run(t, LangJavaScript, `
			const options = { retry: { maximum: 3 } };
			try { nodes.testNode({}, undefined, options); }
			catch (error) { return { name: error.name, attempts: error.attempts, message: error.message }; }
		`, Request{NodeInvoker: invoker})
		require.NoError(t, err)
		assert.Zero(t, invoker.calls, "an unknown computed option must not reach the action")
		doc := result.Value.(map[string]any)
		assert.Equal(t, "NodeCallError", doc["name"])
		assert.Equal(t, float64(0), doc["attempts"])
		assert.Contains(t, doc["message"], "maximum")
	})
}
