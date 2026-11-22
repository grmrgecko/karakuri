package core

import (
	"context"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// filterSeq feeds a sequence of payloads through one filter node instance and
// reports which of them passed. The node keeps its state in the context's node
// store, so reusing the context is what makes the sequence meaningful.
func filterSeq(t *testing.T, cfg string, msgs []*engine.Msg) []bool {
	t.Helper()
	ec := newTestCtx(cfg, nil)
	passed := make([]bool, 0, len(msgs))
	for i, msg := range msgs {
		out, err := FilterNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": msg})
		require.NoErrorf(t, err, "message %d must be judged, not fail the node", i)
		passed = append(passed, only(out, "out") != nil)
	}
	return passed
}

// vals builds messages carrying bare payload values on one topic.
func vals(topic string, values ...any) []*engine.Msg {
	msgs := make([]*engine.Msg, 0, len(values))
	for _, v := range values {
		msgs = append(msgs, &engine.Msg{Payload: v, Topic: topic})
	}
	return msgs
}

// TestFilterBlocksUnchangedValues covers the default mode, which is what stops
// a sensor reporting the same reading every ten seconds from running a flow
// every ten seconds. The first message passes because there is nothing to
// compare it with, and 20 passes again at the end because the value before it
// was 21 — the filter remembers the last value, not every value it has seen.
func TestFilterBlocksUnchangedValues(t *testing.T) {
	assert.Equal(t,
		[]bool{true, false, true, false, true},
		filterSeq(t, `{"mode":"changed"}`, vals("sensor/a", 20, 20, 21, 21, 20)),
		"only a value differing from the one before it may pass")
}

// TestFilterCanBlockTheFirstMessage covers pass_first, which decides what
// happens on the message that has nothing to compare against. Blocking it is
// what a flow wants after a restart: the first reading is the state as it
// already was, not a change worth acting on.
func TestFilterCanBlockTheFirstMessage(t *testing.T) {
	assert.Equal(t,
		[]bool{false, false, true},
		filterSeq(t, `{"mode":"changed","pass_first":false}`, vals("sensor/a", 20, 20, 21)),
		"with pass_first off the opening reading is held back, and only a real change gets through")
}

// TestFilterChangedByAmount covers the deadband, measured against the last
// value that *passed* rather than the last one seen. That distinction is the
// whole point: against the last seen value, a reading drifting by 1 each time
// would never pass however far it wandered. Here 23 passes because 23 - 20 = 3
// exceeds the delta of 2, even though each step was only 1.
func TestFilterChangedByAmount(t *testing.T) {
	assert.Equal(t,
		[]bool{true, false, false, true, true},
		filterSeq(t, `{"mode":"changed_by","delta":2}`, vals("sensor/a", 20, 21, 22, 23, 20)),
		"the deadband is measured from the last value that passed, so slow drift still eventually gets through")
}

// TestFilterChangedByPercent covers the proportional deadband, for readings
// whose meaningful swing scales with their size. From a baseline of 100, 105 is
// a 5% change and is blocked; 120 is 20% and passes.
func TestFilterChangedByPercent(t *testing.T) {
	assert.Equal(t,
		[]bool{true, false, true},
		filterSeq(t, `{"mode":"changed_by_percent","delta":10}`, vals("sensor/a", 100, 105, 120)),
		"5 percent is under the 10 percent threshold and 20 percent is over it")
}

// TestFilterDirectionalModes covers the one-way modes, which is how a flow
// reacts to a temperature climbing without also firing as it falls back. A
// reading that moved the wrong way is blocked outright rather than resetting
// the baseline.
func TestFilterDirectionalModes(t *testing.T) {
	assert.Equal(t,
		[]bool{true, true, false, true},
		filterSeq(t, `{"mode":"greater","delta":1}`, vals("s", 10, 12, 11, 20)),
		"rising values pass and the dip to 11 does not, since it moved the wrong way")

	assert.Equal(t,
		[]bool{true, true, false, true},
		filterSeq(t, `{"mode":"less","delta":1}`, vals("s", 10, 8, 9, 2)),
		"falling values pass and the rise to 9 does not")
}

// TestFilterTracksTopicsSeparately covers per-topic memory, which is what makes
// one filter node usable behind a wildcard subscription. Without it a room
// reporting 20 would block the next room reporting 20, so half a house's
// readings would vanish into a node that looks like it is working.
func TestFilterTracksTopicsSeparately(t *testing.T) {
	ec := newTestCtx(`{"mode":"changed","per_topic":true}`, nil)
	send := func(topic string, value any) bool {
		t.Helper()
		out, err := FilterNode{}.Execute(context.Background(), ec,
			map[string]*engine.Msg{"in": {Payload: value, Topic: topic}})
		require.NoError(t, err, "the filter must judge every message")
		return only(out, "out") != nil
	}

	assert.True(t, send("sensor/a", 20), "the first message of a topic has nothing to compare against")
	assert.True(t, send("sensor/b", 20),
		"the same value on another topic is that topic's first message, not a repeat")
	assert.False(t, send("sensor/a", 20), "a genuine repeat on sensor/a must be blocked")
	assert.True(t, send("sensor/a", 21), "a change on sensor/a must pass")
	assert.False(t, send("sensor/b", 20),
		"sensor/b's own last value was 20, so this is a repeat: each topic keeps its own memory")
}

// TestFilterSharedStateWhenNotPerTopic is the same fixture with the setting
// off, which is what makes the setting visible at all: one memory for the whole
// node, so the same value on another topic reads as a repeat. That is what a
// flow wants when several sources report one thing and only the change matters.
func TestFilterSharedStateWhenNotPerTopic(t *testing.T) {
	ec := newTestCtx(`{"mode":"changed","per_topic":false}`, nil)
	send := func(topic string, value any) bool {
		t.Helper()
		out, err := FilterNode{}.Execute(context.Background(), ec,
			map[string]*engine.Msg{"in": {Payload: value, Topic: topic}})
		require.NoError(t, err, "the filter must judge every message")
		return only(out, "out") != nil
	}

	assert.True(t, send("sensor/a", 20), "the first message has nothing to compare against")
	assert.False(t, send("sensor/b", 20),
		"with per_topic off there is one memory for the node, so the topic makes no difference")
}

// TestFilterWatchesNestedProperty covers watching one field of a composite
// reading. A sensor that reports temperature and humidity together changes its
// payload constantly, so a filter on the whole message would pass everything;
// naming the property is what makes it useful.
func TestFilterWatchesNestedProperty(t *testing.T) {
	cfg := `{"mode":"changed","property":"payload.temperature"}`
	msgs := []*engine.Msg{
		{Payload: map[string]any{"temperature": 20.0, "humidity": 50.0}},
		{Payload: map[string]any{"temperature": 20.0, "humidity": 80.0}},
		{Payload: map[string]any{"temperature": 21.0, "humidity": 80.0}},
	}
	assert.Equal(t, []bool{true, false, true}, filterSeq(t, cfg, msgs),
		"only the watched property may count: the humidity changing on message two must not let it through")
}

// TestFilterResetPortClearsMemory covers the reset input, which is how a flow
// makes the filter forget — after a restart, or when a downstream device was
// power-cycled and needs to be told the current state again. The reset message
// itself is not emitted: it is a control signal, not a reading.
func TestFilterResetPortClearsMemory(t *testing.T) {
	ec := newTestCtx(`{"mode":"changed"}`, nil)
	send := func(inputs map[string]*engine.Msg) bool {
		t.Helper()
		out, err := FilterNode{}.Execute(context.Background(), ec, inputs)
		require.NoError(t, err, "the filter must handle both its input ports")
		return only(out, "out") != nil
	}

	require.True(t, send(map[string]*engine.Msg{"in": {Payload: 20}}), "the first message has nothing to compare against")
	require.False(t, send(map[string]*engine.Msg{"in": {Payload: 20}}), "the repeat must be blocked before the reset means anything")

	assert.False(t, send(map[string]*engine.Msg{"reset": {}}),
		"a reset is a control signal, not a reading, so it must not be emitted as one")
	assert.True(t, send(map[string]*engine.Msg{"in": {Payload: 20}}),
		"after a reset the filter has no memory, so the same value is new again")
}

// TestFilterRejectsBadConfigAndInput covers the cases where the filter cannot
// answer. Each has to fail the node rather than block the message, because a
// blocked message and a broken filter look identical from outside — a flow that
// silently stopped would be indistinguishable from one whose readings settled.
func TestFilterRejectsBadConfigAndInput(t *testing.T) {
	_, err := FilterNode{}.Execute(context.Background(),
		newTestCtx(`{"mode":"changed","property":"payload.absent"}`, nil),
		map[string]*engine.Msg{"in": {Payload: map[string]any{}}})
	assert.Error(t, err, "a property that is not there must fail, not read as an unchanging absence")

	_, err = FilterNode{}.Execute(context.Background(),
		newTestCtx(`{"mode":"vibes"}`, nil),
		map[string]*engine.Msg{"in": {Payload: 1}})
	assert.Error(t, err, "an unknown mode must fail rather than fall back to a mode the author did not choose")

	// A numeric mode over text can only be caught once there is a previous
	// value to compare against, so the first message must succeed.
	ec := newTestCtx(`{"mode":"changed_by","delta":1}`, nil)
	_, err = FilterNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {Payload: 1}})
	require.NoError(t, err, "the first message has nothing to compare against and must pass")

	_, err = FilterNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {Payload: "warm"}})
	assert.Error(t, err, "a deadband cannot be measured against text, so it must fail rather than block silently")
}

// TestFilterBlocksUnchangedBinaryValues locks change detection for a value
// that is not valid UTF-8. The last value is stored as JSON, which turns bytes
// into base64 and rewrites invalid text, so the stored form never equalled the
// next identical message and an unchanged camera frame passed every time. A
// frame that does differ still passes.
func TestFilterBlocksUnchangedBinaryValues(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10}
	other := []byte{0xff, 0xd8, 0xff, 0xe1, 0x00, 0x10}

	assert.Equal(t,
		[]bool{true, false, true, false},
		filterSeq(t, `{"mode":"changed"}`, vals("camera/a", jpeg, jpeg, other, other)),
		"only a frame whose bytes differ from the one before it may pass")
	assert.Equal(t,
		[]bool{true, false, true},
		filterSeq(t, `{"mode":"changed"}`, vals("sensor/a", "caf\xe9", "caf\xe9", "caf\xe8")),
		"text that is not UTF-8 is compared by its bytes too")
}
