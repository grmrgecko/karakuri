package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// asyncNode runs a node's Execute on its own goroutine so a fake-clock test can
// advance time while it is parked.
type asyncNode struct {
	done chan nodeOutcome
}

type nodeOutcome struct {
	out map[string][]*engine.Msg
	err error
}

// runAsync starts Execute in the background and returns a handle to its result.
func runAsync(node engine.NodeType, ec engine.ExecContext, inputs map[string]*engine.Msg) *asyncNode {
	a := &asyncNode{done: make(chan nodeOutcome, 1)}
	go func() {
		out, err := node.Execute(context.Background(), ec, inputs)
		a.done <- nodeOutcome{out: out, err: err}
	}()
	return a
}

// wait blocks for the result, failing the test if the node never returns.
func (a *asyncNode) wait(t *testing.T) nodeOutcome {
	t.Helper()
	select {
	case res := <-a.done:
		require.NoError(t, res.err, "the node must complete once its wait is over")
		return res
	case <-time.After(2 * time.Second):
		require.FailNow(t, "the node never returned after the clock was advanced past its wait")
		return nodeOutcome{}
	}
}

// pending reports whether the node is still running.
func (a *asyncNode) pending() bool {
	select {
	case res := <-a.done:
		a.done <- res
		return false
	default:
		return true
	}
}

// TestDelayFixedModeParksThenPassesThrough covers the plain delay. It parks
// rather than sleeping, which is what gives the worker slot back, and the
// message it releases is the one it was given — a delay postpones, it does not
// transform.
func TestDelayFixedModeParksThenPassesThrough(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"mode":"fixed","duration":"5s"}`, fc)
	in := &engine.Msg{Payload: "data"}

	run := runAsync(DelayNode{}, ec, map[string]*engine.Msg{"in": in})
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second),
		"the delay must park on the injectable clock, or it holds its worker slot for the whole wait")
	require.True(t, run.pending(), "the delay returned before time moved, so it delayed nothing")

	fc.Advance(5 * time.Second)
	assert.True(t, sameMessage(only(run.wait(t).out, "out"), in),
		"a delay postpones a message; it must arrive intact")
}

// TestDelayRandomModeStaysWithinBounds covers the jittered delay, which exists
// to spread a burst of messages out rather than sending them together. The
// bound is what makes it usable: an author sets a window and the wait has to
// land inside it, upper bound exclusive.
func TestDelayRandomModeStaysWithinBounds(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"mode":"random","min_duration":"2s","max_duration":"4s"}`, fc)

	run := runAsync(DelayNode{}, ec, map[string]*engine.Msg{"in": {Payload: 1}})
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second), "the delay must park on the clock")

	waited := fc.AfterCalls()[0]
	assert.GreaterOrEqual(t, waited, 2*time.Second, "the wait must be at least the configured minimum, got %s", waited)
	assert.Less(t, waited, 4*time.Second, "the wait must stay under the configured maximum, got %s", waited)

	fc.Advance(4 * time.Second)
	run.wait(t)
}

// TestDelayRateModeDropsOverTheLimit covers rate limiting with drop, which is
// how a flow protects a device that cannot be commanded ten times a second.
// The last assertion is the one that matters: dropped messages must not push
// the next slot out, or a steady stream of traffic would starve the limiter
// forever and nothing would ever pass again.
func TestDelayRateModeDropsOverTheLimit(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"mode":"rate","interval":"10s","on_limit":"drop"}`, fc)
	send := func() bool {
		t.Helper()
		out, err := DelayNode{}.Execute(context.Background(), ec,
			map[string]*engine.Msg{"in": {Payload: 1}})
		require.NoError(t, err, "a rate-limited message must be judged, not fail the node")
		return only(out, "out") != nil
	}

	require.True(t, send(), "the first message opens the interval and passes at once")
	assert.False(t, send(), "a message inside the interval must be dropped")
	assert.False(t, send(), "a second message inside the interval must also be dropped")

	fc.Advance(10 * time.Second)
	assert.True(t, send(),
		"the interval has elapsed, so this must pass: drops must not push the next slot out or nothing ever passes again")
}

// TestDelayRateModeQueuesOverTheLimit covers the other limit policy: instead of
// discarding the message, hold it until its slot. That is what a flow wants
// when every message matters and only the pace has to be controlled.
func TestDelayRateModeQueuesOverTheLimit(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"mode":"rate","interval":"10s","on_limit":"queue"}`, fc)

	// The first message passes at once.
	out, err := DelayNode{}.Execute(context.Background(), ec,
		map[string]*engine.Msg{"in": {Payload: "first"}})
	require.NoError(t, err, "the first message must be handled")
	require.NotNil(t, only(out, "out"), "the first message opens the interval and passes at once")

	// The second is held until its slot rather than dropped.
	run := runAsync(DelayNode{}, ec, map[string]*engine.Msg{"in": {Payload: "second"}})
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second), "a queued message must park until its slot")
	require.True(t, run.pending(), "the queued message was emitted at once, so the limit did nothing")

	fc.Advance(10 * time.Second)
	got := only(run.wait(t).out, "out")
	require.NotNil(t, got, "the queued message must be released once its slot arrives, not discarded")
	assert.Equal(t, "second", got.Payload, "queueing keeps the message, so it must be the one that was held")
}

// TestDelayDebounceEmitsOnlyTheLastMessage covers debounce, which is what turns
// a chattering sensor into one event. Every arrival restarts the quiet period
// and replaces the pending message, so what finally comes out is the newest
// value after things settle — not the first, which is the state that started
// the commotion.
func TestDelayDebounceEmitsOnlyTheLastMessage(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"mode":"debounce","quiet":"5s"}`, fc)

	// The first message starts the window and does the waiting.
	waiter := runAsync(DelayNode{}, ec, map[string]*engine.Msg{"in": {Payload: "first"}})
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second), "the first message opens the quiet window")

	// Later messages replace the pending one and return right away.
	for _, payload := range []string{"second", "third"} {
		fc.Advance(time.Second)
		out, err := DelayNode{}.Execute(context.Background(), ec,
			map[string]*engine.Msg{"in": {Payload: payload}})
		require.NoErrorf(t, err, "the %s message must be accepted into the open window", payload)
		assert.Nilf(t, only(out, "out"),
			"the %s message must not be emitted while a window is open, or debouncing emits every message", payload)
	}
	require.True(t, waiter.pending(), "the window is still open, so nothing may have been emitted yet")

	// Each arrival restarts the window, so the original 5s is not enough.
	fc.Advance(3 * time.Second)
	require.True(t, fc.BlockUntilAfterCalls(2, time.Second), "a newer message extends the original deadline")
	require.True(t, waiter.pending(),
		"the window restarted on the last arrival, so five seconds from the first message must not be enough")
	fc.Advance(2 * time.Second)

	got := only(waiter.wait(t).out, "out")
	require.NotNil(t, got, "the window has finally been quiet, so the pending message must come out")
	assert.Equal(t, "third", got.Payload, "debounce emits the newest value after things settle, not the one that started it")
}

// TestDelayDebounceStartsFreshAfterEmitting covers the state left behind. A
// second burst has to open its own window rather than find the first one still
// recorded — a debounce that only worked once would look fine in a test that
// stopped after one message and fail on the second event of the day.
func TestDelayDebounceStartsFreshAfterEmitting(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"mode":"debounce","quiet":"5s"}`, fc)

	first := runAsync(DelayNode{}, ec, map[string]*engine.Msg{"in": {Payload: "a"}})
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second), "the first message opens a window")
	fc.Advance(5 * time.Second)

	got := only(first.wait(t).out, "out")
	require.NotNil(t, got, "the first window must emit when it goes quiet")
	require.Equal(t, "a", got.Payload, "the first window holds the only message it saw")

	// A later message must open a new window rather than find a stale one.
	second := runAsync(DelayNode{}, ec, map[string]*engine.Msg{"in": {Payload: "b"}})
	require.True(t, fc.BlockUntilAfterCalls(2, 2*time.Second),
		"a message after the window closed must open a new one, not be swallowed by the old state")
	fc.Advance(5 * time.Second)

	got = only(second.wait(t).out, "out")
	require.NotNil(t, got, "the second window must emit too, or debounce works exactly once")
	assert.Equal(t, "b", got.Payload, "the second window holds its own message")
}

// TestDelayRejectsBadConfig covers the configs the node refuses, against the
// four working modes above. Each duration field is free text an author types,
// and a value that would not parse must fail the node rather than read as zero
// — a delay of zero is a passthrough, which is exactly the bug that hides.
func TestDelayRejectsBadConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
	}{
		{name: "unparsable duration", cfg: `{"mode":"fixed","duration":"nonsense"}`},
		{name: "unknown mode", cfg: `{"mode":"teleport"}`},
		{name: "bad interval", cfg: `{"mode":"rate","interval":"soon"}`},
		{name: "bad quiet period", cfg: `{"mode":"debounce","quiet":"a while"}`},
	}
	for _, c := range cases {
		_, err := DelayNode{}.Execute(context.Background(), newTestCtx(c.cfg, nil),
			map[string]*engine.Msg{"in": {Payload: 1}})
		assert.Errorf(t, err, "%s must fail the node, not read as zero and turn the delay into a passthrough", c.name)
	}
}

// TestWaitDurationsReadBareNumbersAsSeconds covers the Delay and Trigger wait
// fields. Every other duration field reads a bare number as seconds, including
// this Delay node's rate interval and quiet period, but these two failed with
// a missing unit error. An empty value must still fail, since a wait of zero
// is a passthrough.
func TestWaitDurationsReadBareNumbersAsSeconds(t *testing.T) {
	tests := []struct {
		name string
		node engine.NodeType
		cfg  string
		port string
	}{
		{"delay", DelayNode{}, `{"mode":"fixed","duration":%q}`, "out"},
		{"trigger", TriggerNode{}, `{"first_source":"nothing","second_source":"original","duration":%q}`, "expired"},
	}
	for _, tc := range tests {
		fc := engine.NewFakeClock(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
		waiter := runAsync(tc.node, newTestCtx(fmt.Sprintf(tc.cfg, "5"), fc), map[string]*engine.Msg{"in": {Payload: "held"}})
		require.Truef(t, fc.BlockUntilAfterCalls(1, 2*time.Second), "%s: a bare number must park the node, not fail it", tc.name)
		assert.Equalf(t, []time.Duration{5 * time.Second}, fc.AfterCalls(), "%s: a bare 5 is five seconds", tc.name)
		fc.Advance(5 * time.Second)
		assert.NotNilf(t, only(waiter.wait(t).out, tc.port), "%s: the held message leaves once the five seconds pass", tc.name)

		_, err := tc.node.Execute(context.Background(), newTestCtx(fmt.Sprintf(tc.cfg, ""), nil),
			map[string]*engine.Msg{"in": {Payload: "held"}})
		assert.Errorf(t, err, "%s: an empty duration must fail the node, not read as zero", tc.name)
	}
}

// TestDelayHonorsCancellation covers cancelling a run parked in a delay. The
// clock never advances, so the node can only return if cancellation interrupts
// the park — otherwise cancelling a flow with a one-hour delay would leave the
// run alive for an hour.
func TestDelayHonorsCancellation(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"mode":"fixed","duration":"1m"}`, fc)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := DelayNode{}.Execute(ctx, ec, map[string]*engine.Msg{"in": {}})
		done <- err
	}()
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second), "the delay must be parked before it can be cancelled out of")
	cancel()
	assert.Error(t, <-done,
		"cancelling must interrupt the park rather than let a long delay outlive the run")
}

// TestKeyedDelayResetAndMaximumWait keeps room timers independent and bounds
// a continuously retriggered debounce window.
func TestKeyedDelayResetAndMaximumWait(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"mode":"debounce","quiet":"5s","max_wait":"2s","key":"topic"}`, fc)
	a := runAsync(DelayNode{}, ec, map[string]*engine.Msg{"in": {Payload: "a-old", Topic: "a"}})
	require.True(t, fc.BlockUntilAfterCalls(1, time.Second))
	b := runAsync(DelayNode{}, ec, map[string]*engine.Msg{"in": {Payload: "b", Topic: "b"}})
	require.True(t, fc.BlockUntilAfterCalls(2, time.Second))
	out, err := (DelayNode{}).Execute(context.Background(), ec, map[string]*engine.Msg{"reset": {Topic: "a"}})
	require.NoError(t, err)
	assert.Empty(t, out)
	replacement := runAsync(DelayNode{}, ec, map[string]*engine.Msg{"in": {Payload: "a-new", Topic: "a"}})
	require.True(t, fc.BlockUntilAfterCalls(3, time.Second))
	fc.Advance(time.Second)
	out, err = (DelayNode{}).Execute(context.Background(), ec, map[string]*engine.Msg{"in": {Payload: "b-latest", Topic: "b"}})
	require.NoError(t, err)
	assert.Equal(t, "b", only(out, "superseded").Payload)
	fc.Advance(time.Second)
	assert.Equal(t, "a-old", only(a.wait(t).out, "superseded").Payload, "a reset invalidates only its old waiter")
	assert.Equal(t, "a-new", only(replacement.wait(t).out, "out").Payload, "an old waiter must not erase its replacement")
	assert.Equal(t, "b-latest", only(b.wait(t).out, "out").Payload, "maximum wait wins over continuing chatter")
}

// TestKeyedRateSchedulePreservesLegacy isolates new device schedules while
// retaining the persisted single-bucket admission timestamp.
func TestKeyedRateSchedulePreservesLegacy(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"mode":"rate","interval":"10s","key":"topic"}`, fc)
	ec.durable.Set("next_at", fc.Now().Add(time.Hour).Format(time.RFC3339Nano))
	for _, topic := range []string{"a", "b"} {
		in := map[string]*engine.Msg{"in": {Topic: topic, Payload: topic}}
		out, err := (DelayNode{}).Execute(context.Background(), ec, in)
		require.NoError(t, err)
		require.NotNil(t, only(out, "out"))
		out, err = (DelayNode{}).Execute(context.Background(), ec, in)
		require.NoError(t, err)
		require.NotNil(t, only(out, "dropped"))
	}
	out, err := (DelayNode{}).Execute(context.Background(), ec, map[string]*engine.Msg{"reset": {Topic: "a"}})
	require.NoError(t, err)
	assert.Empty(t, out)
	out, err = (DelayNode{}).Execute(context.Background(), ec, map[string]*engine.Msg{"in": {Topic: "a"}})
	require.NoError(t, err)
	require.NotNil(t, only(out, "out"))
	legacy, _ := ec.durable.Get("next_at")
	assert.Equal(t, fc.Now().Add(time.Hour).Format(time.RFC3339Nano), legacy, "keyed reset preserves the unkeyed schedule")
}

func TestDebounceResetRoutesLatestPendingMessage(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"mode":"debounce","quiet":"1s","key":"topic"}`, fc)
	first := &engine.Msg{Payload: "first", Topic: "room"}
	latest := &engine.Msg{Payload: "latest", Topic: "room"}
	pending := runAsync(DelayNode{}, ec, map[string]*engine.Msg{"in": first})
	require.True(t, fc.BlockUntilAfterCalls(1, time.Second))
	out, err := (DelayNode{}).Execute(context.Background(), ec, map[string]*engine.Msg{"in": latest})
	require.NoError(t, err)
	assert.Equal(t, first, only(out, "superseded"))
	out, err = (DelayNode{}).Execute(context.Background(), ec, map[string]*engine.Msg{"reset": {Topic: "room"}})
	require.NoError(t, err)
	assert.Empty(t, out)
	replacement := &engine.Msg{Payload: "replacement", Topic: "room"}
	next := runAsync(DelayNode{}, ec, map[string]*engine.Msg{"in": replacement})
	require.True(t, fc.BlockUntilAfterCalls(2, time.Second))
	fc.Advance(time.Second)
	assert.Equal(t, latest, only(pending.wait(t).out, "superseded"), "the first message was already superseded by the second arrival")
	assert.Equal(t, replacement, only(next.wait(t).out, "out"), "the reset waiter cannot consume the replacement window")
}
