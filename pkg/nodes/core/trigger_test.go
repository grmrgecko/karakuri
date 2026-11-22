package core

import (
	"context"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTriggerEmitsNowThenAfterTheWait covers the trigger node's shape: send
// something immediately, then send something else once the wait elapses. That
// is the "turn the light on now and off in five minutes" pattern, and the
// immediate half has to go out through Emit rather than the return value, or
// nothing would happen until the wait was over.
//
// The delayed message keeps the original topic: a flow that addressed a device
// on the way in must address the same device on the way out.
func TestTriggerEmitsNowThenAfterTheWait(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"first_source":"original","duration":"5m",
		"second_source":"payload","second_payload":{"state":"off"}}`, fc)
	in := &engine.Msg{Payload: map[string]any{"state": "on"}, Topic: "light/kitchen"}

	run := runAsync(TriggerNode{}, ec, map[string]*engine.Msg{"in": in})
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second), "the trigger must park for the configured wait")

	// The immediate message goes out through Emit, before Execute returns.
	emits := ec.emitted()
	require.Len(t, emits, 1, "exactly one immediate message must go out: %+v", emits)
	assert.Equal(t, "out", emits[0].port, "the immediate message leaves on the normal output")
	assert.True(t, sameMessage(emits[0].msg, in),
		"the first message is the input as it arrived, sent before the wait rather than after it")
	require.True(t, run.pending(), "the node must still be waiting; returning now would fire both messages at once")

	fc.Advance(5 * time.Minute)

	expired := only(run.wait(t).out, "expired")
	require.NotNil(t, expired, "the delayed message must go out on the expired port once the wait elapses")

	payload, ok := expired.Payload.(map[string]any)
	require.True(t, ok, "the configured second payload is an object")
	assert.Equal(t, "off", payload["state"], "the second message carries the configured payload, not the first one's")
	assert.Equal(t, "light/kitchen", expired.Topic,
		"the topic carries over, or the off message would not address the device the on message did")
}

// TestTriggerExtendsTheWaitOnANewMessage covers the extend policy, which is the
// motion-sensor case: each new detection pushes the lights-off deadline out,
// so a room in use never goes dark. The extending message emits nothing itself
// — it is a reprieve, not an event.
func TestTriggerExtendsTheWaitOnANewMessage(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"first_source":"nothing","duration":"5m","retrigger":"extend",
		"second_source":"payload","second_payload":{"state":"off"}}`, fc)

	waiter := runAsync(TriggerNode{}, ec, map[string]*engine.Msg{"in": {Payload: 1}})
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second), "the first message arms the wait")

	// Three minutes in, a new message pushes the deadline out.
	fc.Advance(3 * time.Minute)
	out, err := TriggerNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {Payload: 2}})
	require.NoError(t, err, "a message arriving while armed must be accepted")
	assert.Empty(t, out, "an extending message is a reprieve, not an event, so it emits nothing: %v", out)

	// The original five minutes is no longer enough.
	fc.Advance(2 * time.Minute)
	require.True(t, waiter.pending(),
		"the deadline moved to three minutes after the second message, so the original one must not fire")
	fc.Advance(3 * time.Minute)

	assert.NotNil(t, only(waiter.wait(t).out, "expired"),
		"the extended wait has now elapsed, so the delayed message must finally go out")
}

// TestTriggerIgnoresMessagesWhileArmed covers the ignore policy, which is what
// a flow wants when the first event is the one that counts: a door opening
// starts a five-minute countdown that further openings must not disturb. The
// immediate message is not resent either, or the light would flash on each
// time.
func TestTriggerIgnoresMessagesWhileArmed(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"first_source":"original","duration":"5m","retrigger":"ignore",
		"second_source":"payload","second_payload":{"state":"off"}}`, fc)

	waiter := runAsync(TriggerNode{}, ec, map[string]*engine.Msg{"in": {Payload: 1}})
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second), "the first message arms the wait")

	fc.Advance(3 * time.Minute)
	out, err := TriggerNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {Payload: 2}})
	require.NoError(t, err, "a message arriving while armed must be accepted, then ignored")
	assert.Empty(t, out, "an ignored message emits nothing: %v", out)
	assert.Len(t, ec.emitted(), 1,
		"the immediate message must not be resent, or the light flashes on again with every event")

	// The deadline did not move.
	fc.Advance(2 * time.Minute)
	assert.NotNil(t, only(waiter.wait(t).out, "expired"),
		"the original deadline stands under the ignore policy, so five minutes from the first message must fire")
}

// TestTriggerRestartResendsAndSupersedes covers the restart policy, where a new
// message starts the whole cycle again: the immediate message is resent and a
// fresh wait begins. The superseded run is the awkward part — it is a
// goroutine already parked on the old deadline, and when it wakes it has to
// find it has been replaced and emit nothing, or the delayed message would fire
// early.
func TestTriggerRestartResendsAndSupersedes(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"first_source":"original","duration":"5m","retrigger":"restart",
		"second_source":"payload","second_payload":{"state":"off"}}`, fc)

	first := runAsync(TriggerNode{}, ec, map[string]*engine.Msg{"in": {Payload: 1}})
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second), "the first message arms the wait")

	fc.Advance(3 * time.Minute)
	second := runAsync(TriggerNode{}, ec, map[string]*engine.Msg{"in": {Payload: 2}})
	require.True(t, fc.BlockUntilAfterCalls(2, 2*time.Second), "the restarting message arms a wait of its own")

	// The superseded run gives up quietly when it reaches its old deadline.
	fc.Advance(2 * time.Minute)
	assert.Empty(t, first.wait(t).out,
		"the superseded run must emit nothing when it wakes, or the delayed message fires at the old deadline")
	require.True(t, second.pending(), "the restarted wait runs from the second message, so it cannot have fired yet")

	assert.Len(t, ec.emitted(), 2, "restart resends the immediate message, so there is one per cycle")

	fc.Advance(3 * time.Minute)
	assert.NotNil(t, only(second.wait(t).out, "expired"), "the restarted wait must fire at its own deadline")
}

// TestTriggerResetCancelsThePendingMessage covers the reset input, which is how
// a flow calls off a pending action — someone turned the light off by hand, so
// the scheduled off message is no longer wanted. The parked goroutine still has
// to wake at its deadline and emit nothing, and the node has to arm again from
// the next message rather than stay dead.
func TestTriggerResetCancelsThePendingMessage(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"first_source":"original","duration":"5m",
		"second_source":"payload","second_payload":{"state":"off"}}`, fc)

	waiter := runAsync(TriggerNode{}, ec, map[string]*engine.Msg{"in": {Payload: 1}})
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second), "the first message arms the wait")

	// A reset arrives on its own port, as it would from another branch.
	out, err := TriggerNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"reset": {Payload: "stop"}})
	require.NoError(t, err, "a reset must be accepted while the node is armed")
	assert.Empty(t, out, "a reset is a control signal and emits nothing itself: %v", out)

	// The waiting run keeps its park but emits nothing when it wakes.
	fc.Advance(5 * time.Minute)
	assert.Empty(t, waiter.wait(t).out,
		"the cancelled message must not go out when the parked run reaches its deadline")

	// After a reset the node arms again from the next message.
	next := runAsync(TriggerNode{}, ec, map[string]*engine.Msg{"in": {Payload: 2}})
	require.True(t, fc.BlockUntilAfterCalls(2, 2*time.Second),
		"a reset cancels the pending message, it does not disable the node")
	fc.Advance(5 * time.Minute)
	assert.NotNil(t, only(next.wait(t).out, "expired"), "the re-armed wait must fire normally")
}

// TestTriggerSecondSourceNothingEmitsOnlyTheFirst covers using the node as a
// plain gate: send the message on and then do nothing at the deadline. The wait
// still happens, so the node stays armed and further messages are still subject
// to the retrigger policy.
func TestTriggerSecondSourceNothingEmitsOnlyTheFirst(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"first_source":"original","duration":"1m","second_source":"nothing"}`, fc)

	run := runAsync(TriggerNode{}, ec, map[string]*engine.Msg{"in": {Payload: 1}})
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second),
		"the wait still runs, so the node stays armed even with nothing to send at the end")
	fc.Advance(time.Minute)

	out := run.wait(t).out
	assert.Empty(t, out, "with the second source set to nothing, the deadline must produce no message: %v", out)
	assert.Len(t, ec.emitted(), 1, "the immediate message must still go out; only the delayed one is suppressed")
}

// TestTriggerRejectsBadDuration covers the duration field against the working
// waits above. It must fail rather than read as zero: a trigger with no wait
// would send both messages at once, turning a light on and off in the same
// instant.
func TestTriggerRejectsBadDuration(t *testing.T) {
	_, err := TriggerNode{}.Execute(context.Background(),
		newTestCtx(`{"duration":"whenever"}`, nil),
		map[string]*engine.Msg{"in": {Payload: 1}})
	assert.Error(t, err,
		"an unparsable duration must fail the node: a wait of zero sends both messages at once")
}

// TestTriggerKeyedReset keeps a reset in one room from cancelling another
// room's delayed action.
func TestTriggerKeyedReset(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"duration":"2s","first_source":"nothing","second_source":"original","key":"topic"}`, fc)
	a := runAsync(TriggerNode{}, ec, map[string]*engine.Msg{"in": {Payload: "a", Topic: "a"}})
	require.True(t, fc.BlockUntilAfterCalls(1, time.Second))
	b := runAsync(TriggerNode{}, ec, map[string]*engine.Msg{"in": {Payload: "b", Topic: "b"}})
	require.True(t, fc.BlockUntilAfterCalls(2, time.Second))
	out, err := (TriggerNode{}).Execute(context.Background(), ec, map[string]*engine.Msg{"reset": {Topic: "a"}})
	require.NoError(t, err)
	assert.Empty(t, out)
	fc.Advance(2 * time.Second)
	assert.Empty(t, a.wait(t).out, "the selected reset prevents its expiration")
	assert.Equal(t, "b", only(b.wait(t).out, "expired").Payload, "another key still expires")
}
