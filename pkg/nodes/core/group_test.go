package core

import (
	"context"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runSplit splits a message and returns the emitted parts.
func runSplit(t *testing.T, cfg string, in *engine.Msg) []*engine.Msg {
	t.Helper()
	out, err := SplitNode{}.Execute(context.Background(), newTestCtx(cfg, nil),
		map[string]*engine.Msg{"in": in})
	require.NoError(t, err, "this payload must be splittable")
	return out["out"]
}

// TestSplitArrayIntoParts covers the fan-out every other group node depends on.
// Each part carries a frame naming the set, its position in it, and how many
// there are — that is the whole contract a join later reads, and the shared
// group id is what keeps two concurrent splits from being merged into one.
func TestSplitArrayIntoParts(t *testing.T) {
	parts := runSplit(t, `{"mode":"auto"}`,
		&engine.Msg{Payload: []any{"a", "b", "c"}, Topic: "list"})

	require.Len(t, parts, 3, "one part per element")

	for i, p := range parts {
		require.Lenf(t, p.Group, 1, "part %d must carry exactly the frame this split pushed: %+v", i, p.Group)
		assert.Equalf(t, i, p.Group[0].Index, "part %d must know its position, which is how a join restores the order", i)
		assert.Equalf(t, 3, p.Group[0].Count, "part %d must carry the set size, which is how a join knows it is complete", i)
		assert.Equalf(t, "list", p.Topic, "part %d must keep the topic: splitting does not change what was addressed", i)
	}

	assert.Equal(t, "a", parts[0].Payload, "each part carries its own element")
	assert.Equal(t, "c", parts[2].Payload, "each part carries its own element")
	assert.Equal(t, parts[0].Group[0].ID, parts[1].Group[0].ID,
		"every part shares one group id, or a join would treat them as separate sets")
}

// TestSplitObjectPutsKeyOnTheTopic covers splitting an object, where the key
// has nowhere to go in the payload — it becomes the topic, so a flow acting on
// each entry still knows which one it has. The keys are sorted, which is what
// makes the frame indexes stable: Go map order is random, so without sorting a
// join would rebuild the object in a different order every run.
func TestSplitObjectPutsKeyOnTheTopic(t *testing.T) {
	parts := runSplit(t, `{"mode":"object","key_property":"topic"}`,
		&engine.Msg{Payload: map[string]any{"b": 2, "a": 1}})

	require.Len(t, parts, 2, "one part per key")

	// The fixture is written b before a, so sorting is visible.
	assert.Equal(t, "a", parts[0].Topic, "keys are sorted, so the numbering is stable across runs")
	assert.Equal(t, "b", parts[1].Topic, "keys are sorted, so the numbering is stable across runs")
	assert.Equal(t, 1, parts[0].Payload, "each part carries its key's value")
	assert.Equal(t, 2, parts[1].Payload, "each part carries its key's value")
}

// TestSplitStringOnSeparator covers splitting text, which is how a multi-line
// file or a delimited payload becomes one message per line.
func TestSplitStringOnSeparator(t *testing.T) {
	parts := runSplit(t, `{"mode":"string","separator":"\\n"}`,
		&engine.Msg{Payload: "one\ntwo\nthree"})

	require.Len(t, parts, 3, "three lines separated by two newlines make three parts")
	assert.Equal(t, "two", parts[1].Payload, "the separator must not be kept in the parts")
}

// TestSplitNestedProperty covers splitting a field of a document rather than
// the whole payload, which is the usual shape: an API response with the list
// nested under a key.
func TestSplitNestedProperty(t *testing.T) {
	parts := runSplit(t, `{"property":"payload.items"}`,
		&engine.Msg{Payload: map[string]any{"items": []any{1, 2}}})
	assert.Len(t, parts, 2, "the named property is what gets split, not the object holding it")
}

// TestSplitEmptyArrayProducesNothing covers an empty list, which is not a
// failure — an API legitimately returns no results. Emitting nothing is the
// only correct answer: a single message carrying null would run everything
// downstream once on data that does not exist.
func TestSplitEmptyArrayProducesNothing(t *testing.T) {
	parts := runSplit(t, `{"mode":"array"}`, &engine.Msg{Payload: []any{}})
	assert.Empty(t, parts, "no elements means no messages, not one carrying null")
}

// TestSplitRejectsMismatchedModeAndMissingProperty covers the splits the node
// refuses, against the working modes above. A declared mode meeting the wrong
// shape fails rather than falling back to auto: the author said what the
// payload would be, and a split that quietly did something else would fan out a
// different number of messages than the graph was built for.
func TestSplitRejectsMismatchedModeAndMissingProperty(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		in   *engine.Msg
	}{
		{name: "array mode on an object", cfg: `{"mode":"array"}`, in: &engine.Msg{Payload: map[string]any{"a": 1}}},
		{name: "object mode on an array", cfg: `{"mode":"object"}`, in: &engine.Msg{Payload: []any{1}}},
		{name: "missing property", cfg: `{"property":"payload.absent"}`, in: &engine.Msg{Payload: map[string]any{}}},
		{name: "unknown mode", cfg: `{"mode":"shred"}`, in: &engine.Msg{Payload: []any{1}}},
	}
	for _, c := range cases {
		_, err := SplitNode{}.Execute(context.Background(), newTestCtx(c.cfg, nil),
			map[string]*engine.Msg{"in": c.in})
		assert.Errorf(t, err,
			"%s must fail rather than fall back: a different number of parts changes what the whole graph does", c.name)
	}
}

// feedJoin sends parts through one join node instance and returns every message
// it emitted.
func feedJoin(t *testing.T, cfg string, clock engine.Clock, parts []*engine.Msg) []*engine.Msg {
	t.Helper()
	ec := newTestCtx(cfg, clock)
	var emitted []*engine.Msg
	for i, p := range parts {
		out, err := JoinNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": p})
		require.NoErrorf(t, err, "part %d must be accepted into the join", i)
		if msg := only(out, "out"); msg != nil {
			emitted = append(emitted, msg)
		}
	}
	return emitted
}

// TestJoinCombinesASplitGroupIntoAnArray covers the fan-in against a real
// split. Popping the frame is what takes the message back out of the group, so
// everything after the join runs once rather than once per part; leaving it on
// would fan out again downstream.
func TestJoinCombinesASplitGroupIntoAnArray(t *testing.T) {
	parts := runSplit(t, `{"mode":"array"}`, &engine.Msg{Payload: []any{"a", "b", "c"}})
	emitted := feedJoin(t, `{"mode":"array"}`, nil, parts)

	require.Len(t, emitted, 1, "nothing may be emitted until the group is complete, and then exactly one message is")
	assert.Equal(t, []any{"a", "b", "c"}, emitted[0].Payload, "the array must be rebuilt in the order it was split")
	assert.Empty(t, emitted[0].Group,
		"the join pops the frame, so the message is out of the group; leaving it on would fan out again downstream")
	assert.Equal(t, 3, emitted[0].Meta["parts"], "the part count belongs in metadata so a flow can check nothing was lost")
}

// TestJoinRestoresOrderFromOutOfOrderArrivals is why the frame carries an index
// at all. Fanned-out parts run concurrently and finish in no particular order —
// a slow HTTP request on part one means it arrives last — so the join has to
// rebuild by index rather than by arrival.
func TestJoinRestoresOrderFromOutOfOrderArrivals(t *testing.T) {
	parts := runSplit(t, `{"mode":"array"}`, &engine.Msg{Payload: []any{"first", "second", "third"}})
	// Deliver them backwards, the way concurrent instances may finish.
	shuffled := []*engine.Msg{parts[2], parts[0], parts[1]}
	emitted := feedJoin(t, `{"mode":"array"}`, nil, shuffled)

	require.Len(t, emitted, 1, "the group completes however the parts arrive")
	assert.Equal(t, []any{"first", "second", "third"}, emitted[0].Payload,
		"the array must be rebuilt by frame index, not by the order the parts came back")
}

// TestJoinModes covers the four ways a group is combined. Each is driven from a
// real split so the frames are the ones the split actually produced, and object
// mode is round-tripped from the object split above — splitting an object and
// joining it back must give the object.
func TestJoinModes(t *testing.T) {
	base := &engine.Msg{Payload: map[string]any{"a": 1, "b": 2}}
	objectParts := runSplit(t, `{"mode":"object","key_property":"topic"}`, base)

	// Object mode keys by the split's topic.
	emitted := feedJoin(t, `{"mode":"object","key_property":"topic"}`, nil, objectParts)
	require.Len(t, emitted, 1, "the group completes into one message")
	obj, ok := emitted[0].Payload.(map[string]any)
	require.Truef(t, ok, "object join = %#v, want an object", emitted[0].Payload)
	assert.Equal(t, map[string]any{"a": 1, "b": 2}, obj,
		"splitting an object and joining it back must give the object it came from")

	// String mode concatenates.
	stringParts := runSplit(t, `{"mode":"array"}`, &engine.Msg{Payload: []any{"x", "y"}})
	emitted = feedJoin(t, `{"mode":"string","separator":"-"}`, nil, stringParts)
	require.Len(t, emitted, 1, "the group completes into one message")
	assert.Equal(t, "x-y", emitted[0].Payload, "string mode joins the parts with the configured separator")

	// Merge mode folds object payloads together.
	mergeParts := runSplit(t, `{"mode":"array"}`, &engine.Msg{Payload: []any{
		map[string]any{"one": 1}, map[string]any{"two": 2},
	}})
	emitted = feedJoin(t, `{"mode":"merge"}`, nil, mergeParts)
	require.Len(t, emitted, 1, "the group completes into one message")
	merged, ok := emitted[0].Payload.(map[string]any)
	require.Truef(t, ok, "merge join = %#v, want an object", emitted[0].Payload)
	assert.Equal(t, map[string]any{"one": 1, "two": 2}, merged,
		"merge mode folds every part's object into one, keeping every key")
}

// TestJoinMergeRejectsNonObjectPayloads covers merge mode meeting parts it
// cannot fold. There is nothing to merge a string into, and quietly dropping it
// would produce an object missing exactly the values the author was collecting.
func TestJoinMergeRejectsNonObjectPayloads(t *testing.T) {
	parts := runSplit(t, `{"mode":"array"}`, &engine.Msg{Payload: []any{"not an object", "either"}})
	ec := newTestCtx(`{"mode":"merge"}`, nil)
	var lastErr error
	for _, p := range parts {
		if _, err := (JoinNode{}).Execute(context.Background(), ec, map[string]*engine.Msg{"in": p}); err != nil {
			lastErr = err
		}
	}
	assert.Error(t, lastErr, "a part with no fields to merge must fail rather than be silently dropped from the result")
}

// TestJoinStreamByCount covers joining messages that never came from a split —
// a repeating trigger or an MQTT subscription, where there is no frame to say
// when the set ends. The configured count is what closes each batch, and the
// node then starts a fresh one rather than joining everything into a single
// growing array.
func TestJoinStreamByCount(t *testing.T) {
	stream := []*engine.Msg{{Payload: 1}, {Payload: 2}, {Payload: 3}, {Payload: 4}}
	emitted := feedJoin(t, `{"mode":"array","count":"2"}`, nil, stream)

	require.Len(t, emitted, 2, "four messages at a count of two make two batches")
	assert.Equal(t, []any{1, 2}, emitted[0].Payload, "the first batch closes at the count")
	assert.Equal(t, []any{3, 4}, emitted[1].Payload,
		"the next messages start a fresh batch rather than growing the first")
}

// TestJoinStreamNeedsACountOrTimeout pairs with the count above: an ungrouped
// message with neither a count nor a timeout gives the node no way to ever
// finish, so it would hold every message forever. Failing at the first one is
// how the author finds out, rather than watching a flow quietly stop.
func TestJoinStreamNeedsACountOrTimeout(t *testing.T) {
	_, err := JoinNode{}.Execute(context.Background(), newTestCtx(`{"mode":"array"}`, nil),
		map[string]*engine.Msg{"in": {Payload: 1}})
	assert.Error(t, err,
		"with no group, no count, and no timeout the join could never complete, so it must say so at once")
}

// TestJoinTimeoutEmitsWhatArrived covers the timeout, which is what keeps a
// join from waiting forever for a message that is never coming — a device that
// went offline mid-batch. It emits what it has and marks the result incomplete,
// so a flow can tell a short batch from a full one rather than silently acting
// on partial data.
func TestJoinTimeoutEmitsWhatArrived(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"mode":"array","count":"5","timeout":"30s"}`, fc)

	// The first message opens the window and parks.
	waiter := runAsync(JoinNode{}, ec, map[string]*engine.Msg{"in": {Payload: 1}})
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second), "the first message opens the timeout window")

	// A second arrives, still short of the count of 5.
	out, err := JoinNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {Payload: 2}})
	require.NoError(t, err, "a message inside the window must be accepted")
	assert.Nil(t, only(out, "out"), "two of five is short of the count, so nothing may be emitted yet")

	fc.Advance(30 * time.Second)

	joined := only(waiter.wait(t).out, "out")
	require.NotNil(t, joined, "the timeout must emit what arrived rather than wait forever for the missing three")
	assert.Equal(t, []any{1, 2}, joined.Payload, "the two messages that did arrive must be in the result")
	assert.Equal(t, true, joined.Meta["incomplete"],
		"a short batch must be marked, or a flow cannot tell it from a full one")
}

// TestJoinIsNotConfusedByTwoGroups covers one join node handling two
// independent splits at once, which is what happens whenever a flow is busy.
// The group id is what keeps them apart; without it the interleaved parts would
// complete one wrong group of four.
func TestJoinIsNotConfusedByTwoGroups(t *testing.T) {
	// Two independent splits interleave through one join node.
	groupA := runSplit(t, `{"mode":"array"}`, &engine.Msg{Payload: []any{"a1", "a2"}})
	groupB := runSplit(t, `{"mode":"array"}`, &engine.Msg{Payload: []any{"b1", "b2"}})
	interleaved := []*engine.Msg{groupA[0], groupB[0], groupB[1], groupA[1]}
	emitted := feedJoin(t, `{"mode":"array"}`, nil, interleaved)

	require.Len(t, emitted, 2, "each group must complete on its own, keyed by its group id")
	// B's parts arrived second and third, so B completes first.
	assert.Equal(t, []any{"b1", "b2"}, emitted[0].Payload, "the group that completed first is emitted first, with only its own parts")
	assert.Equal(t, []any{"a1", "a2"}, emitted[1].Payload, "the other group completes separately, with only its own parts")
}

// TestAggregateComputesReductions covers every reduction over a group, with the
// arithmetic spelled out by the fixture: 10, 20, 30, 40 sum to 100 over 4
// values, so the average is 25, and with an even count the median is the mean
// of the middle two — (20 + 30) / 2 = 25 as well.
func TestAggregateComputesReductions(t *testing.T) {
	parts := runSplit(t, `{"mode":"array"}`, &engine.Msg{Payload: []any{10.0, 20.0, 30.0, 40.0}})
	ec := newTestCtx(`{"property":"payload","operations":"count,sum,min,max,avg,median,first,last"}`, nil)

	var summary *engine.Msg
	for _, p := range parts {
		out, err := AggregateNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": p})
		require.NoError(t, err, "every part must be accepted into the aggregate")
		if msg := only(out, "out"); msg != nil {
			summary = msg
		}
	}
	require.NotNil(t, summary, "the summary must be emitted once the group is complete")

	assert.Equal(t, map[string]any{
		"count": 4, "sum": 100.0, "min": 10.0, "max": 40.0,
		"avg": 25.0, "median": 25.0, "first": 10.0, "last": 40.0,
	}, summary.Payload,
		"10 + 20 + 30 + 40 = 100 over 4 values, so avg is 25; the median of an even count is (20 + 30) / 2")
}

// TestAggregateSkipsNonNumericValues covers a group with a value that is not a
// number, which is what a sensor reporting "n/a" or "unavailable" looks like.
// The numeric reductions skip it while count and concat include it, so a
// missing reading does not fail the whole aggregation but is still visible.
func TestAggregateSkipsNonNumericValues(t *testing.T) {
	parts := runSplit(t, `{"mode":"array"}`, &engine.Msg{Payload: []any{10.0, "n/a", 20.0}})
	ec := newTestCtx(`{"property":"payload","operations":"count,sum,concat"}`, nil)

	var summary *engine.Msg
	for _, p := range parts {
		out, _ := AggregateNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": p})
		if msg := only(out, "out"); msg != nil {
			summary = msg
		}
	}
	require.NotNil(t, summary, "the summary must be emitted once the group is complete")
	got, ok := summary.Payload.(map[string]any)
	require.True(t, ok, "the summary is an object of the requested reductions")

	assert.Equal(t, 3, got["count"], "count is how many values arrived, including the one that is not a number")
	assert.Equal(t, 30.0, got["sum"], "10 + 20 = 30: the non-numeric value is skipped rather than failing the sum")
	assert.Equal(t, "10, n/a, 20", got["concat"], "concat renders every value, which is how the gap stays visible")
}

// TestAggregateOmitsNumericFieldsWithNoNumbers covers a group with no numbers
// at all. The numeric fields are absent rather than zero, which is the
// distinction that matters: a sum of 0 is a real answer, and a rule comparing
// against it cannot tell "nothing to add" from "adds to nothing".
func TestAggregateOmitsNumericFieldsWithNoNumbers(t *testing.T) {
	parts := runSplit(t, `{"mode":"array"}`, &engine.Msg{Payload: []any{"a", "b"}})
	ec := newTestCtx(`{"property":"payload","operations":"count,sum,avg"}`, nil)

	var summary *engine.Msg
	for _, p := range parts {
		out, _ := AggregateNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": p})
		if msg := only(out, "out"); msg != nil {
			summary = msg
		}
	}
	require.NotNil(t, summary, "the summary must be emitted once the group is complete")
	got, ok := summary.Payload.(map[string]any)
	require.True(t, ok, "the summary is an object of the requested reductions")

	assert.NotContains(t, got, "sum",
		"sum must be absent, not zero: a real sum of zero would otherwise be indistinguishable from having nothing to add")
	assert.NotContains(t, got, "avg", "an average of no numbers has no value to report")
	assert.Equal(t, 2, got["count"], "count still applies: two values arrived, whatever they were")
}

// TestAggregateRejectsUnknownOperations covers the operation list, beside the
// reductions above. An unrecognised name must fail rather than be dropped from
// the list, or the summary would simply lack the field the author asked for and
// every downstream read of it would find nothing.
func TestAggregateRejectsUnknownOperations(t *testing.T) {
	_, err := AggregateNode{}.Execute(context.Background(),
		newTestCtx(`{"operations":"count,vibe"}`, nil),
		map[string]*engine.Msg{"in": {Payload: 1}})
	assert.Error(t, err, "an unknown operation must fail rather than be silently dropped from the summary")

	_, err = AggregateNode{}.Execute(context.Background(),
		newTestCtx(`{"operations":""}`, nil),
		map[string]*engine.Msg{"in": {Payload: 1}})
	assert.Error(t, err, "an aggregate that computes nothing has no summary to emit")
}

// TestBatchReleasesByCount covers batching an ungrouped stream into groups.
// Unlike a join it releases the messages rather than combining them, stamping
// each batch with its own group frame — and the two batches must carry
// different group ids, or a join downstream would merge them back into one.
func TestBatchReleasesByCount(t *testing.T) {
	ec := newTestCtx(`{"mode":"count","count":"3"}`, nil)
	var released []*engine.Msg
	for i := 1; i <= 6; i++ {
		out, err := BatchNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {Payload: i}})
		require.NoErrorf(t, err, "message %d must be accepted into a batch", i)
		released = append(released, out["out"]...)
	}

	require.Len(t, released, 6, "batching groups messages, it does not drop them: six in, six out")

	require.Lenf(t, released[0].Group, 1, "each released message carries its batch's frame: %+v", released[0].Group)
	assert.Equal(t, 3, released[0].Group[0].Count, "the frame's count is the batch size, which is how a join knows it is complete")
	assert.NotEqual(t, released[0].Group[0].ID, released[3].Group[0].ID,
		"the two batches must have different group ids, or a join downstream merges them back into one")
	assert.Equal(t, 1, released[0].Payload, "the batch releases the messages it collected, in order")
	assert.Equal(t, 3, released[2].Payload, "the batch releases the messages it collected, in order")
}

// TestBatchReleasesByInterval covers batching by time rather than count, which
// is what a flow wants when messages arrive irregularly and the batch has to go
// out on a schedule regardless of how many turned up.
func TestBatchReleasesByInterval(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"mode":"interval","interval":"1m"}`, fc)

	waiter := runAsync(BatchNode{}, ec, map[string]*engine.Msg{"in": {Payload: 1}})
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second), "the first message opens the interval")

	for _, payload := range []int{2, 3} {
		out, err := BatchNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {Payload: payload}})
		require.NoErrorf(t, err, "message %d must be accepted into the open batch", payload)
		assert.Emptyf(t, out["out"], "message %d must wait for the interval, not release the batch early", payload)
	}
	fc.Advance(time.Minute)

	released := waiter.wait(t).out["out"]
	require.Len(t, released, 3, "the interval releases everything collected while it was open")
	assert.Equal(t, 1, released[0].Payload, "the batch releases its messages in arrival order")
	assert.Equal(t, 3, released[2].Payload, "the batch releases its messages in arrival order")
}

// TestBatchIntervalMaxSizeReleasesEarly covers the size cap on an interval
// batch, which is what stops a burst of traffic from building an unbounded
// batch in memory. The second half is the part worth the setup: the goroutine
// parked on the interval is still there, and when it wakes it must find its
// batch already released and emit nothing — otherwise every capped batch would
// be delivered twice.
func TestBatchIntervalMaxSizeReleasesEarly(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"mode":"interval","interval":"1m","max_count":"2"}`, fc)

	waiter := runAsync(BatchNode{}, ec, map[string]*engine.Msg{"in": {Payload: 1}})
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second), "the first message opens the interval")

	// The second message hits the cap, so it releases the batch itself.
	out, err := BatchNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {Payload: 2}})
	require.NoError(t, err, "the message that hits the cap must be accepted")
	require.Len(t, out["out"], 2, "reaching the cap must release the batch without waiting out the interval")

	fc.Advance(time.Minute)
	assert.Empty(t, waiter.wait(t).out["out"],
		"the run parked on the interval must find its batch already gone, or every capped batch is delivered twice")
}

// TestBatchRejectsBadConfig covers the configs a batch node refuses, against
// the two working modes above. A batch with no size or no interval would never
// release, so a flow behind it would simply stop with nothing to show for it.
func TestBatchRejectsBadConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
	}{
		{name: "no size", cfg: `{"mode":"count","count":""}`},
		{name: "zero size", cfg: `{"mode":"count","count":"0"}`},
		{name: "non-numeric size", cfg: `{"mode":"count","count":"many"}`},
		{name: "no interval", cfg: `{"mode":"interval","interval":""}`},
		{name: "unknown mode", cfg: `{"mode":"clump"}`},
	}
	for _, c := range cases {
		_, err := BatchNode{}.Execute(context.Background(), newTestCtx(c.cfg, nil),
			map[string]*engine.Msg{"in": {Payload: 1}})
		assert.Errorf(t, err,
			"%s must fail the node: a batch that can never release stops the flow behind it with nothing to show", c.name)
	}
}

// TestSplitJoinRoundTripPreservesNesting covers nested fan-out, which is what
// the group *stack* exists for rather than a single frame. An outer split, then
// an inner split of each part, means each innermost message carries two frames;
// the inner join must pop only its own and leave the outer one, or the outer
// join never sees a complete set and the run hangs with nothing to point at.
func TestSplitJoinRoundTripPreservesNesting(t *testing.T) {
	outer := runSplit(t, `{"mode":"array"}`, &engine.Msg{Payload: []any{
		[]any{"a", "b"}, []any{"c", "d"},
	}})
	require.Len(t, outer, 2, "the outer split produces one part per nested array")

	joinCtx := newTestCtx(`{"mode":"array"}`, nil)
	var innerJoined []*engine.Msg
	for _, part := range outer {
		inner := runSplit(t, `{"mode":"array"}`, part)
		require.Len(t, inner, 2, "the inner split produces one part per element")

		for _, p := range inner {
			require.Lenf(t, p.Group, 2, "an innermost message carries both frames: %+v", p.Group)

			out, err := JoinNode{}.Execute(context.Background(), joinCtx, map[string]*engine.Msg{"in": p})
			require.NoError(t, err, "the inner join must accept its parts")
			if msg := only(out, "out"); msg != nil {
				innerJoined = append(innerJoined, msg)
			}
		}
	}
	require.Len(t, innerJoined, 2, "each inner group completes into one message")

	for _, msg := range innerJoined {
		assert.Lenf(t, msg.Group, 1,
			"the inner join must pop only its own frame and leave the outer one, or the outer join never completes: %+v",
			msg.Group)
	}

	// The outer join now completes from those two messages.
	outerCtx := newTestCtx(`{"mode":"array"}`, nil)
	var final *engine.Msg
	for _, msg := range innerJoined {
		out, err := JoinNode{}.Execute(context.Background(), outerCtx, map[string]*engine.Msg{"in": msg})
		require.NoError(t, err, "the outer join must accept the inner results")
		if got := only(out, "out"); got != nil {
			final = got
		}
	}
	require.NotNil(t, final, "the outer join must complete from the two inner results")
	assert.Empty(t, final.Group, "the last frame is popped, so the message is fully out of every group: %+v", final.Group)

	assert.Equal(t, []any{[]any{"a", "b"}, []any{"c", "d"}}, final.Payload,
		"the nesting must be rebuilt exactly as it was split apart")
}

// TestSplitObjectKeyLeavesTheInputAlone locks that writing each entry's key
// into the part's payload does not write it into the message being split. That
// message is shared with every other node wired to the same output, so the key
// showing up there both corrupts their data and races with them.
func TestSplitObjectKeyLeavesTheInputAlone(t *testing.T) {
	in := &engine.Msg{Payload: map[string]any{
		"a": map[string]any{"x": 1.0},
		"b": map[string]any{"x": 2.0},
	}}
	parts := runSplit(t, `{"mode":"object","key_property":"payload.name"}`, in)

	require.Len(t, parts, 2, "one part per entry")
	for _, p := range parts {
		assert.NotEmpty(t, p.Payload.(map[string]any)["name"], "each part carries its key")
	}
	assert.Equal(t, map[string]any{
		"a": map[string]any{"x": 1.0},
		"b": map[string]any{"x": 2.0},
	}, in.Payload, "the split message must be left exactly as it arrived")
}
