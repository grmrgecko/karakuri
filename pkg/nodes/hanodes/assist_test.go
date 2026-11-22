package hanodes

import (
	"context"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAssistWireContract pins the request and response shapes shared with the
// Home Assistant component. In particular, the request ID and Assist
// conversation survive an AI node replacing the payload, while Home Assistant
// receives the complete transcript envelope and its raw provider responses.
func TestAssistWireContract(t *testing.T) {
	returnPorts := AssistNode{}.Meta().Inputs
	require.Len(t, returnPorts, 1, "HA Assist must show one return socket")
	assert.Equal(t, "response", returnPorts[0].ID)
	assert.Equal(t, "Response", returnPorts[0].Label)
	assert.Equal(t, "any", returnPorts[0].Type, "the socket accepts text, a response envelope, or an AI stream")

	incoming := &engine.Msg{
		Payload: map[string]any{
			"request_id": "request-1", "agent_id": "house",
			"text": "Is the garage closed?", "conversation_id": "ha-conversation-1", "language": "en-US",
			"device_id": "voice-kitchen", "area_id": "kitchen",
			"transcript": []any{map[string]any{"role": "user", "text": "Is the garage closed?"}},
		},
		Meta: map[string]any{engine.RequestIDMeta: "request-1", "source": "home_assistant_event"},
	}
	fc := &fakeConn{}
	triggered, err := AssistNode{}.Execute(context.Background(), &fakeEC{
		cfg: `{"connection":"ha-1","agent_id":"house"}`, conn: fc,
	}, map[string]*engine.Msg{engine.TriggerInput: incoming})
	require.NoError(t, err, "a request emitted by the Home Assistant component must enter the flow")
	triggerMsg := triggered["out"][0]
	assert.Equal(t, "ha_assist", triggerMsg.Meta["source"], "the run history must identify Assist instead of a generic event")
	assert.Equal(t, "kitchen", nestedString(triggerMsg.Meta, "ha_assist", "area_id"), "the room must survive payload changes downstream")

	transcript := []map[string]any{
		{"role": "user", "text": "Is the garage closed?"},
		{"role": "assistant", "text": "Yes, it is closed."},
	}
	responseMeta := triggerMsg.Meta
	responseEC := &fakeEC{cfg: `{"connection":"ha-1","agent_id":"house"}`, conn: fc}
	_, err = AssistNode{}.Execute(context.Background(), responseEC, map[string]*engine.Msg{
		"response": {Payload: map[string]any{
			"type": "tool.progress", "index": 0, "tool_call_id": "call-ha-1",
			"tool_name": "ha_get_state", "label": "Read garage door", "status": "running",
		}, Meta: responseMeta},
	})
	require.NoError(t, err, "Hermes tool progress must reach Home Assistant")
	require.Len(t, fc.calls, 1)
	assert.Equal(t, "call-ha-1", fc.calls[0].serviceData["tool_call_id"])
	assert.Equal(t, "ha_get_state", fc.calls[0].serviceData["tool_name"])
	assert.Equal(t, "running", fc.calls[0].serviceData["tool_status"])
	assert.Equal(t, "", fc.calls[0].serviceData["response"], "tool progress must not enter spoken text")
	_, err = AssistNode{}.Execute(context.Background(), responseEC, map[string]*engine.Msg{
		"response": {Payload: map[string]any{
			"type": "tool.progress", "index": 1, "tool_call_id": "call-ha-1",
			"tool_name": "ha_get_state", "status": "completed",
		}, Meta: responseMeta},
	})
	require.NoError(t, err, "Hermes tool completion must reach Home Assistant")
	_, err = AssistNode{}.Execute(context.Background(), responseEC, map[string]*engine.Msg{
		"response": {Payload: map[string]any{"type": "content.delta", "index": 3, "delta": "it is closed."}, Meta: responseMeta},
	})
	require.NoError(t, err, "an early out-of-order delta must wait for its predecessor")
	_, err = AssistNode{}.Execute(context.Background(), responseEC, map[string]*engine.Msg{
		"response": {Payload: map[string]any{"type": "content.delta", "index": 4, "delta": "\n"}, Meta: responseMeta},
	})
	require.NoError(t, err, "a provider's whitespace-only delta must wait for the missing predecessor")
	_, err = AssistNode{}.Execute(context.Background(), responseEC, map[string]*engine.Msg{
		"response": {Payload: map[string]any{"type": "content.delta", "index": 2, "delta": "Yes, "}, Meta: responseMeta},
	})
	require.NoError(t, err, "the missing delta must drain all contiguous stream events in order")
	require.Len(t, fc.calls, 5)
	assert.Equal(t, "Yes, ", fc.calls[2].serviceData["response"])
	assert.Equal(t, true, fc.calls[2].serviceData["stream"], "Home Assistant must keep waiting after a stream chunk")
	assert.Equal(t, "it is closed.", fc.calls[3].serviceData["response"])
	assert.Equal(t, "\n", fc.calls[4].serviceData["response"], "streaming must preserve provider whitespace")

	transcriptEnvelope := map[string]any{
		"transcript": transcript,
		"responses": []any{map[string]any{
			"model": "howl", "hermes": map[string]any{"completed": true},
		}},
	}
	responseEC.cfg = `{"connection":"ha-1","agent_id":"house","continue_conversation":"auto"}`
	out, err := AssistNode{}.Execute(context.Background(), responseEC, map[string]*engine.Msg{
		"response": {Payload: map[string]any{
			"type": "response.completed", "index": 5, "output": "Yes, it is closed.", "response": transcriptEnvelope,
		}, Meta: responseMeta},
	})
	require.NoError(t, err, "the complete AI transcript must answer Assist without a separate output wire")
	assert.Empty(t, out, "the return closes the request branch rather than starting another one")

	require.Len(t, fc.calls, 6, "tool progress, stream chunks, and the completed turn use separate Home Assistant service calls")
	call := fc.calls[5]
	assert.Equal(t, "karakuri_assist", call.domain, "the custom integration owns the response service")
	assert.Equal(t, "respond", call.service, "the response must resolve the pending Assist turn")
	assert.Equal(t, "request-1", call.serviceData["request_id"], "the service call must identify the pending request")
	assert.Equal(t, "house", call.serviceData["agent_id"], "the response must return to the configured conversation agent")
	assert.Equal(t, "Yes, it is closed.", call.serviceData["response"], "only the final answer is spoken")
	assert.NotContains(t, call.serviceData, "stream", "the final response closes the pending Assist turn")
	assert.Equal(t, "ha-conversation-1", call.serviceData["conversation_id"], "the response must correlate with Home Assistant's chat log")
	returned, ok := call.serviceData["transcript"].(map[string]any)
	require.True(t, ok, "Home Assistant must receive the transcript envelope, not only its entry array")
	returnedEntries, ok := returned["transcript"].([]any)
	require.True(t, ok, "the envelope must retain its reusable entry array")
	assert.Len(t, returnedEntries, 2, "the complete conversation must remain in the full envelope")
	finalEntry, ok := returnedEntries[1].(map[string]any)
	require.True(t, ok, "the final transcript entry must remain an object")
	assert.Equal(t, "Yes, it is closed.", finalEntry["text"], "the final assistant turn must pass through")
	assert.Equal(t, transcriptEnvelope["responses"], returned["responses"], "raw OpenAI responses must pass through without field filtering")
	assert.NotContains(t, call.serviceData, "continue_conversation", "automatic mode lets Home Assistant infer follow-up listening from punctuation")

	// Test Run has no pending Home Assistant request. A transcript may still
	// return for inspection when the answer is wired elsewhere, such as Debug.
	testConn := &fakeConn{}
	out, err = AssistNode{}.Execute(context.Background(), &fakeEC{
		cfg: `{"connection":"ha-1","agent_id":"house"}`, conn: testConn,
	}, map[string]*engine.Msg{
		"response": {Payload: transcript, Meta: map[string]any{"source": "ha_assist"}},
	})
	require.NoError(t, err, "a test transcript has no live Assist request to answer")
	assert.Empty(t, out, "an inspected transcript closes the return branch")
	assert.Empty(t, testConn.calls, "Test Run must not call Home Assistant without a request id")
}

// TestAssistAIDecisionContract pins the structured model reply sent through
// Karakuri's response envelope. Home Assistant must receive only the spoken
// field and the explicit follow-up choice, while streamed JSON stays hidden.
func TestAssistAIDecisionContract(t *testing.T) {
	fc := &fakeConn{}
	ec := &fakeEC{
		cfg:  `{"connection":"ha-1","agent_id":"house","continue_conversation":"ai"}`,
		conn: fc,
	}
	meta := map[string]any{
		engine.RequestIDMeta: "request-ai-1",
		"connection_id":      "ha-1",
		"ha_assist": map[string]any{
			"agent_id": "house", "conversation_id": "conversation-ai-1",
		},
	}
	envelope := map[string]any{
		"transcript": []any{
			map[string]any{"role": "user", "text": "Turn on a light."},
			map[string]any{"role": "assistant", "text": `{"response":"Which room?","continue_conversation":true}`},
		},
		"responses": []any{map[string]any{"id": "provider-response-1"}},
	}
	_, err := AssistNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{
		"response": {Payload: envelope, Meta: meta},
	})
	require.NoError(t, err, "a response envelope must expose the model's structured follow-up choice")
	require.Len(t, fc.calls, 1)
	call := fc.calls[0].serviceData
	assert.Equal(t, "Which room?", call["response"], "Home Assistant must speak only the response field")
	assert.Equal(t, true, call["continue_conversation"], "the model's choice must control follow-up listening")
	assert.Equal(t, envelope, call["transcript"], "the provider response envelope must survive the structured reply")
	fc.calls = nil

	_, err = AssistNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{
		"response": {Payload: map[string]any{
			"type": "content.delta", "index": 0,
			"delta": `{"response":"Which room?","continue_conversation":true}`,
		}, Meta: meta},
	})
	require.NoError(t, err, "AI decision JSON must remain buffered until the response is complete")
	assert.Empty(t, fc.calls, "Home Assistant must not speak structured JSON from a stream delta")

	_, err = AssistNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{
		"response": {Payload: map[string]any{
			"type": "response.completed", "index": 1,
			"output": map[string]any{
				"response": "Which room?", "continue_conversation": true,
			},
			"response": envelope,
		}, Meta: meta},
	})
	require.NoError(t, err, "a completed structured response must resolve the pending Assist turn")
	require.Len(t, fc.calls, 1)
	call = fc.calls[0].serviceData
	assert.Equal(t, "Which room?", call["response"], "Home Assistant must speak only the response field")
	assert.Equal(t, true, call["continue_conversation"], "the model's choice must control follow-up listening")
	assert.Equal(t, envelope, call["transcript"], "the provider response envelope must survive the structured reply")
}

// TestAssistStreamSkipsCompactionNotice locks that a streamed AI Agent which
// compacts its history mid-run still answers Home Assistant. The agent numbers
// its context.compacted event like any other stream event. Assist used to
// treat it as a final reply, fail it, and leave its index unconsumed, so every
// later delta and the completed response stayed buffered and the Assist turn
// never closed.
func TestAssistStreamSkipsCompactionNotice(t *testing.T) {
	fc := &fakeConn{}
	ec := &fakeEC{cfg: `{"connection":"ha-1","agent_id":"house"}`, conn: fc}
	meta := map[string]any{
		engine.RequestIDMeta: "request-compact-1",
		"connection_id":      "ha-1",
		"ha_assist":          map[string]any{"agent_id": "house", "conversation_id": "conversation-1"},
	}
	events := []map[string]any{
		{"type": "content.delta", "index": 0, "delta": "Checking. "},
		{"type": "context.compacted", "index": 1, "reason": "threshold", "before_tokens": 9000, "after_tokens": 1200},
		{"type": "content.delta", "index": 2, "delta": "The garage is closed."},
		{"type": "response.completed", "index": 3, "output": "Checking. The garage is closed.",
			"response": map[string]any{"transcript": []any{
				map[string]any{"role": "user", "text": "Is the garage closed?"},
				map[string]any{"role": "assistant", "text": "Checking. The garage is closed."},
			}}},
	}
	for _, event := range events {
		_, err := AssistNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{
			"response": {Payload: event, Meta: meta},
		})
		require.NoErrorf(t, err, "stream event %v of type %v must be accepted", event["index"], event["type"])
	}

	require.Len(t, fc.calls, 3, "the compaction notice must send nothing to Home Assistant")
	var spoken []any
	for _, call := range fc.calls {
		spoken = append(spoken, call.serviceData["response"])
	}
	assert.Equal(t, []any{"Checking. ", "The garage is closed.", "Checking. The garage is closed."}, spoken,
		"every delta and the final answer must reach Home Assistant in order")
	assert.NotContains(t, fc.calls[2].serviceData, "stream", "the final response must close the pending Assist turn")
}
