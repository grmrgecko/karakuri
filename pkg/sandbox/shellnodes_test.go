package sandbox

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/interp"
)

// shellNodeInvokerFixture records the mediated boundary and can vary each reply.
type shellNodeInvokerFixture struct {
	metas  []nodeapi.NodeMeta
	result *nodeapi.NodeCallResult
	err    error
	invoke func(int, nodeapi.NodeCall) (*nodeapi.NodeCallResult, error)

	mu       sync.Mutex
	recorded []nodeapi.NodeCall
}

func (i *shellNodeInvokerFixture) ScriptNodeFunctions() []nodeapi.NodeMeta { return i.metas }

func (i *shellNodeInvokerFixture) CallScriptNode(_ context.Context, call nodeapi.NodeCall) (*nodeapi.NodeCallResult, error) {
	i.mu.Lock()
	i.recorded = append(i.recorded, call)
	number := len(i.recorded)
	i.mu.Unlock()
	if i.invoke != nil {
		return i.invoke(number, call)
	}
	return i.result, i.err
}

func (i *shellNodeInvokerFixture) calls() []nodeapi.NodeCall {
	i.mu.Lock()
	defer i.mu.Unlock()
	calls := make([]nodeapi.NodeCall, len(i.recorded))
	copy(calls, i.recorded)
	return calls
}

// TestShellNodeCapabilityIsOptIn keeps the external-command denial in place for
// standalone, device, transform, and remote-agent shell users.
func TestShellNodeCapabilityIsOptIn(t *testing.T) {
	_, err := run(t, LangBash, `node test-node '{}'`, Request{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `command "node" is not available in the sandbox`)
}

// TestShellNodeFunctionsAndOutputs covers exact names, aliases, computed generic
// calls, forwarded invocation fields, complete output envelopes, and base64 data.
func TestShellNodeFunctionsAndOutputs(t *testing.T) {
	invoker := &shellNodeInvokerFixture{
		metas: []nodeapi.NodeMeta{{Type: "test-node"}, {Type: "alias-node"}, {Type: "testNode"}},
		result: &nodeapi.NodeCallResult{
			NodeType: "test-node", Sequence: 1, Attempts: 1,
			Outputs: map[string][]*nodeapi.Msg{
				"out": {{
					ID: "result-id", Payload: []byte("hi"), Topic: "binary", Continuation: true,
					Meta:  map[string]any{"source": "call"},
					Group: []nodeapi.GroupFrame{{ID: "g", Index: 2, Count: 4}},
				}},
				"empty": {},
			},
		},
	}
	result, err := run(t, LangBash, `
		kind="test"
		kind="$kind-node"
		canonical=$(node test-node '{"configured":true}' '{"in":{"id":"input-id","payload":{"n":1},"topic":"input-topic","meta":{"request":7},"group":[{"id":"in-group","index":1,"count":2}],"continuation":true}}' '{"id":"stable","retry":{"max_attempts":3,"delay_ms":5,"backoff":2,"max_delay_ms":20,"jitter":0.1},"params":{"threshold":4}}')
		alias=$(node aliasNode '{}')
		priority=$(node testNode '{}')
		generic=$(node call "$kind" '{}')
		printf '%s' "$canonical"
	`, Request{NodeInvoker: invoker})
	require.NoError(t, err)

	calls := invoker.calls()
	require.Len(t, calls, 4)
	assert.Equal(t, "test-node", calls[0].Type)
	assert.Equal(t, "alias-node", calls[1].Type, "an alias resolves to its exact registered type")
	assert.Equal(t, "testNode", calls[2].Type, "an exact type wins over another type's derived alias")
	assert.Equal(t, "test-node", calls[3].Type, "a computed generic type remains unchanged")
	assert.JSONEq(t, `{"configured":true}`, string(calls[0].Config))
	input := calls[0].Inputs["in"]
	require.NotNil(t, input)
	assert.Equal(t, "input-id", input.ID)
	assert.Equal(t, map[string]any{"n": float64(1)}, input.Payload)
	assert.Equal(t, "input-topic", input.Topic)
	assert.Equal(t, map[string]any{"request": float64(7)}, input.Meta)
	assert.Equal(t, []nodeapi.GroupFrame{{ID: "in-group", Index: 1, Count: 2}}, input.Group)
	assert.True(t, input.Continuation)
	assert.Equal(t, "stable", calls[0].Options.ID)
	require.NotNil(t, calls[0].Options.Retry)
	assert.Equal(t, 3, calls[0].Options.Retry.MaxAttempts)
	assert.Equal(t, 5, calls[0].Options.Retry.DelayMs)
	assert.Equal(t, float64(2), calls[0].Options.Retry.Backoff)
	assert.Equal(t, 20, calls[0].Options.Retry.MaxDelayMs)
	assert.Equal(t, 0.1, calls[0].Options.Retry.Jitter)
	assert.Equal(t, map[string]any{"threshold": float64(4)}, calls[0].Options.Params)

	doc, ok := result.Value.(map[string]any)
	require.True(t, ok)
	out, ok := doc["out"].([]any)
	require.True(t, ok)
	require.Len(t, out, 1)
	message, ok := out[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "result-id", message["id"])
	assert.Equal(t, "aGk=", message["payload"])
	assert.Equal(t, "binary", message["topic"])
	assert.Equal(t, map[string]any{"source": "call"}, message["meta"])
	assert.Equal(t, true, message["continuation"])
	group := message["group"].([]any)[0].(map[string]any)
	assert.Equal(t, float64(2), group["index"])
	assert.Equal(t, []any{}, doc["empty"], "an ordinary output with no messages stays an empty array")
}

// TestShellMessageEnvelope verifies that MSG retains fields omitted by the old
// payload/topic/meta projection and that binary data uses the sandbox base64 form.
func TestShellMessageEnvelope(t *testing.T) {
	result, err := run(t, LangBash, `printf '%s' "$MSG"`, Request{
		Payload: "projected",
		Topic:   "projected-topic",
		Message: &nodeapi.Msg{
			ID: "message-id", Payload: []byte("hi"), Topic: "actual-topic", Continuation: true,
			Meta:  map[string]any{"binary": []byte("meta")},
			Group: []nodeapi.GroupFrame{{ID: "g", Index: 2, Count: 4}},
		},
	})
	require.NoError(t, err)
	doc, ok := result.Value.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "message-id", doc["id"])
	assert.Equal(t, "aGk=", doc["payload"])
	assert.Equal(t, "actual-topic", doc["topic"])
	assert.Equal(t, map[string]any{"binary": "bWV0YQ=="}, doc["meta"])
	assert.Equal(t, true, doc["continuation"])
	group := doc["group"].([]any)[0].(map[string]any)
	assert.Equal(t, "g", group["id"])
	assert.Equal(t, float64(2), group["index"])
	assert.Equal(t, float64(4), group["count"])

	t.Run("callers without an envelope retain the projection", func(t *testing.T) {
		result, err := run(t, LangBash, `printf '%s' "$MSG"`, Request{
			Payload: "projected", Topic: "topic", Meta: map[string]any{"request": "r"},
		})
		require.NoError(t, err)
		doc := result.Value.(map[string]any)
		assert.Equal(t, "", doc["id"])
		assert.Equal(t, "projected", doc["payload"])
		assert.Equal(t, "topic", doc["topic"])
		assert.Equal(t, map[string]any{"request": "r"}, doc["meta"])
	})
}

// TestShellNodeFailures checks both the catchable JSON status contract and the
// classified cause retained when that status ends the script.
func TestShellNodeFailures(t *testing.T) {
	newInvoker := func() *shellNodeInvokerFixture {
		return &shellNodeInvokerFixture{
			metas: []nodeapi.NodeMeta{{Type: "test-node"}},
			err: &nodeapi.NodeCallError{
				NodeType: "test-node", InvocationID: "stable", Sequence: 2, Attempts: 3,
				Retryable: true, RetryAfterMS: 25, Details: map[string]any{"kind": "temporary"},
				Cause: errors.New("boom"),
			},
		}
	}
	t.Run("an if statement catches the structured document", func(t *testing.T) {
		result, err := run(t, LangBash, `
			if failure=$(node test-node '{}'); then
				printf '{"unexpected":true}'
			else
				printf '%s' "$failure"
			fi
		`, Request{NodeInvoker: newInvoker()})
		require.NoError(t, err)
		doc := result.Value.(map[string]any)
		assert.Equal(t, "NodeCallError", doc["name"])
		assert.Contains(t, doc["message"], "boom")
		assert.Equal(t, "test-node", doc["node_type"])
		assert.Equal(t, "stable", doc["invocation_id"])
		assert.Equal(t, float64(2), doc["sequence"])
		assert.Equal(t, float64(3), doc["attempts"])
		assert.Equal(t, true, doc["retryable"])
		assert.Equal(t, float64(25), doc["retry_after_ms"])
		assert.Equal(t, map[string]any{"kind": "temporary"}, doc["details"])
		fatal, present := doc["fatal"]
		assert.True(t, present, "fatal is part of the error document even when false")
		assert.Equal(t, false, fatal)
	})

	t.Run("an unhandled status retains NodeCallError", func(t *testing.T) {
		_, err := run(t, LangBash, `node test-node '{}'`, Request{NodeInvoker: newInvoker()})
		require.Error(t, err)
		var callErr *nodeapi.NodeCallError
		require.ErrorAs(t, err, &callErr)
		assert.Equal(t, 3, callErr.Attempts)
		assert.Contains(t, callErr.Error(), "boom")
		var status interp.ExitStatus
		require.ErrorAs(t, err, &status)
		assert.Equal(t, interp.ExitStatus(1), status)
	})
}

// TestShellNodeResultFailures proves nil and unencodable host results still
// produce the same structured shell failure instead of a plain handler abort.
func TestShellNodeResultFailures(t *testing.T) {
	tests := []struct {
		name     string
		result   *nodeapi.NodeCallResult
		want     string
		sequence float64
		attempts float64
	}{
		{name: "nil result", want: "no result"},
		{
			name: "unencodable output", want: "script boundary", sequence: 4, attempts: 2,
			result: &nodeapi.NodeCallResult{
				NodeType: "test-node", Sequence: 4, Attempts: 2,
				Outputs: map[string][]*nodeapi.Msg{"out": {{Payload: math.Inf(1)}}},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			invoker := &shellNodeInvokerFixture{metas: []nodeapi.NodeMeta{{Type: "test-node"}}, result: tc.result}
			result, err := run(t, LangBash, `
				if failure=$(node test-node '{}'); then :; fi
				printf '%s' "$failure"
			`, Request{NodeInvoker: invoker})
			require.NoError(t, err)
			doc := result.Value.(map[string]any)
			assert.Equal(t, "NodeCallError", doc["name"])
			assert.Contains(t, doc["message"], tc.want)
			assert.Equal(t, "test-node", doc["node_type"])
			assert.Equal(t, tc.sequence, doc["sequence"])
			assert.Equal(t, tc.attempts, doc["attempts"])
		})
	}
}

// TestShellNodeArgumentFailuresStopBeforeAdmission exercises the JSON boundary
// with malformed and unknown fields; none may consume an invocation slot.
func TestShellNodeArgumentFailuresStopBeforeAdmission(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{name: "malformed config", command: `node test-node '{'`, want: "invalid node call config"},
		{name: "null inputs", command: `node test-node '{}' null`, want: "inputs must be a JSON object"},
		{name: "unknown option", command: `node test-node '{}' '{}' '{"unknown":true}'`, want: `unknown field "unknown"`},
		{name: "unknown retry key", command: `node test-node '{}' '{}' '{"retry":{"maximum":3}}'`, want: "maximum"},
		{name: "trailing JSON value", command: `node test-node '{} {}'`, want: "multiple JSON values"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			invoker := &shellNodeInvokerFixture{metas: []nodeapi.NodeMeta{{Type: "test-node"}}}
			result, err := run(t, LangBash, `
				if failure=$(`+tc.command+`); then :; fi
				printf '%s' "$failure"
			`, Request{NodeInvoker: invoker})
			require.NoError(t, err)
			assert.Empty(t, invoker.calls(), "local JSON validation must not admit a node call")
			doc := result.Value.(map[string]any)
			assert.Equal(t, "NodeCallError", doc["name"])
			assert.Equal(t, float64(0), doc["attempts"])
			assert.Contains(t, doc["message"], tc.want)
		})
	}
}

// TestShellNodeBackgroundCallsAreRejected pins the mvdan lifecycle boundary:
// Runner.Run does not wait for '&', so these forms fail before any admission.
func TestShellNodeBackgroundCallsAreRejected(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{name: "direct", source: `node test-node '{}' & wait`},
		{name: "block", source: `{ node test-node '{}'; } & wait`},
		{name: "shell function", source: `call_node() { node test-node '{}'; }; call_node & wait`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			invoker := &shellNodeInvokerFixture{metas: []nodeapi.NodeMeta{{Type: "test-node"}}}
			_, err := run(t, LangBash, tc.source, Request{NodeInvoker: invoker})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "background commands")
			assert.Empty(t, invoker.calls())
		})
	}
}

// TestShellNodeDynamicEvaluationIsRejected closes the runtime-created AST path:
// eval and traps can conceal a background call that the original tree preflight
// cannot see, so the capable shell refuses them before any invocation.
func TestShellNodeDynamicEvaluationIsRejected(t *testing.T) {
	for _, source := range []string{
		`eval 'node test-node "{}" &'`,
		`command eval 'node test-node "{}" &'`,
		`command -- eval 'node test-node "{}" &'`,
		`e\val 'node test-node "{}" &'`,
		`trap 'node test-node "{}"' EXIT`,
		`cmd=eval; "$cmd" 'node test-node "{}" &'`,
		`shopt -s expand_aliases; alias n=node; n test-node '{}' &`,
	} {
		invoker := &shellNodeInvokerFixture{metas: []nodeapi.NodeMeta{{Type: "test-node"}}}
		_, err := run(t, LangBash, source, Request{NodeInvoker: invoker})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "conceal background execution")
		assert.Empty(t, invoker.calls())
	}
}

// TestShellNodePipelinesAreRejected prevents concurrent pipeline handlers from
// blocking output or outliving the invocation budget. Rejection happens before
// the first admission, regardless of result size.
func TestShellNodePipelinesAreRejected(t *testing.T) {
	for _, source := range []string{
		`node test-node '{}' | node test-node '{}' >/dev/null`,
		`command -- node test-node '{}' | :`,
		`n\ode test-node '{}' | :`,
	} {
		invoker := &shellNodeInvokerFixture{metas: []nodeapi.NodeMeta{{Type: "test-node"}}}
		_, err := run(t, LangBash, source, Request{NodeInvoker: invoker})
		require.Error(t, err)
		assert.Empty(t, invoker.calls())
	}
}

// TestShellNodeFunctionRedefinitionIsRejected prevents an early function body
// from hiding a node call behind a later declaration with the same name.
func TestShellNodeFunctionRedefinitionIsRejected(t *testing.T) {
	invoker := &shellNodeInvokerFixture{metas: []nodeapi.NodeMeta{{Type: "test-node"}}}
	_, err := run(t, LangBash, `
		f() { node test-node '{}'; }
		f | :
		f() { :; }
	`, Request{NodeInvoker: invoker})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "function redefinition")
	assert.Empty(t, invoker.calls())
}

// TestShellNodePreservesStdin proves a top-level call does not consume the
// original payload stream that argument-less jsonget reads.
func TestShellNodePreservesStdin(t *testing.T) {
	invoker := &shellNodeInvokerFixture{
		metas:  []nodeapi.NodeMeta{{Type: "test-node"}},
		result: &nodeapi.NodeCallResult{NodeType: "test-node", Attempts: 1, Outputs: map[string][]*nodeapi.Msg{}},
	}
	result, err := run(t, LangBash, `
		node test-node '{}' >/dev/null
		jsonget temperature
	`, Request{Payload: map[string]any{"temperature": 42}, NodeInvoker: invoker})
	require.NoError(t, err)
	assert.Equal(t, float64(42), result.Value)
	assert.Len(t, invoker.calls(), 1)
}
