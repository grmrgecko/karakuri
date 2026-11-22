package core

import (
	"context"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runSort sorts one message and returns the single emitted result.
func runSort(t *testing.T, cfg string, in *engine.Msg) *engine.Msg {
	t.Helper()
	out, err := SortNode{}.Execute(context.Background(), newTestCtx(cfg, nil),
		map[string]*engine.Msg{"in": in})
	require.NoError(t, err, "this array must be sortable")

	msg := only(out, "out")
	require.NotNil(t, msg, "sorting reorders a message, it never drops it")
	return msg
}

// arrayPayload asserts the payload is an array and returns it.
func arrayPayload(t *testing.T, msg *engine.Msg) []any {
	t.Helper()
	items, ok := msg.Payload.([]any)
	require.Truef(t, ok, "payload is %T, want an array to read the order off", msg.Payload)
	return items
}

// TestSortArrayPayloadNumerically covers the default: numbers sort by value.
// The fixture is chosen so a text sort would give a visibly different answer —
// as text, 10 comes before 2 and 33 before 4.
func TestSortArrayPayloadNumerically(t *testing.T) {
	msg := runSort(t, `{"mode":"property"}`, &engine.Msg{Payload: []any{10.0, 2.0, 33.0, 4.0}})
	assert.Equal(t, []any{2.0, 4.0, 10.0, 33.0}, arrayPayload(t, msg),
		"numbers must sort by value; as text this would come back 10, 2, 33, 4")
}

// TestSortArrayDescendingByElementKey covers sorting objects by one of their
// fields, which is what a list of sensor readings actually looks like, and the
// descending order that puts the highest first.
func TestSortArrayDescendingByElementKey(t *testing.T) {
	msg := runSort(t, `{"mode":"property","element_key":"temp","order":"desc"}`,
		&engine.Msg{Payload: []any{
			map[string]any{"id": "a", "temp": 18.0},
			map[string]any{"id": "b", "temp": 24.0},
			map[string]any{"id": "c", "temp": 21.0},
		}})

	// 24, 21, 18 descending, so the ids come back b, c, a.
	items := arrayPayload(t, msg)
	got := make([]any, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		require.True(t, ok, "the elements are objects and must stay objects")
		got = append(got, obj["id"])
	}
	assert.Equal(t, []any{"b", "c", "a"}, got, "elements must be ordered by the named key, highest first")
}

// TestSortArrayAsTextIgnoresCaseByDefault pins case-insensitive text ordering.
// A byte-wise sort would put every capitalised word first, so "apple" would
// come last here — which is not what anyone means by sorting a list of names.
func TestSortArrayAsTextIgnoresCaseByDefault(t *testing.T) {
	msg := runSort(t, `{"mode":"property","value_type":"string"}`,
		&engine.Msg{Payload: []any{"Banana", "apple", "Cherry"}})
	assert.Equal(t, []any{"apple", "Banana", "Cherry"}, arrayPayload(t, msg),
		"case must not decide the order; sorting by byte value would put apple last")
}

// TestSortArrayAutoReadsNumericStrings covers auto mode over numbers that
// arrived as text, which is what a JSON API or an MQTT payload commonly sends.
// Read as text the order would be "10", "100", "9"; read as numbers it is the
// order a person expects.
func TestSortArrayAutoReadsNumericStrings(t *testing.T) {
	msg := runSort(t, `{"mode":"property"}`, &engine.Msg{Payload: []any{"10", "9", "100"}})
	assert.Equal(t, []any{"9", "10", "100"}, arrayPayload(t, msg),
		"numeric text must sort by value: as text this would come back 10, 100, 9")
}

// TestSortLeavesTheInputMessageAlone covers the copy the node makes. Sorting a
// slice in place would reorder the array the engine recorded in run history and
// the one any parallel branch is still reading, so the output has to be a new
// slice.
func TestSortLeavesTheInputMessageAlone(t *testing.T) {
	in := &engine.Msg{Payload: map[string]any{"items": []any{3.0, 1.0, 2.0}}}
	msg := runSort(t, `{"mode":"property","property":"payload.items"}`, in)

	original, ok := in.Payload.(map[string]any)["items"].([]any)
	require.True(t, ok, "the fixture's array must still be one")
	assert.Equal(t, []any{3.0, 1.0, 2.0}, original,
		"the input must keep its order: run history and any parallel branch are still holding it")

	sorted, ok := msg.Payload.(map[string]any)["items"].([]any)
	require.True(t, ok, "the emitted array must still be an array")
	assert.Equal(t, []any{1.0, 2.0, 3.0}, sorted, "the emitted copy is the sorted one")
}

// TestSortRejectsANonArrayProperty covers sorting something that is not a list.
// It fails rather than passing the value through: a sort node that silently did
// nothing would leave the author looking downstream for the bug.
func TestSortRejectsANonArrayProperty(t *testing.T) {
	_, err := SortNode{}.Execute(context.Background(), newTestCtx(`{"mode":"property"}`, nil),
		map[string]*engine.Msg{"in": {Payload: "not an array"}})
	assert.Error(t, err, "a value with no order to sort must fail rather than pass through unsorted")
}

// TestSortSequenceReleasesPartsInOrder covers sorting a fanned-out group rather
// than an array. The node holds every part until the group is complete, then
// releases them renumbered — the renumbering is the point, because a join
// downstream rebuilds the array from the frame indexes, so parts released in
// sorted order but carrying their old indexes would come back unsorted.
//
// The join at the end is what proves it: it is the consumer the renumbering
// exists for.
func TestSortSequenceReleasesPartsInOrder(t *testing.T) {
	parts := runSplit(t, `{"mode":"array"}`, &engine.Msg{Payload: []any{"pear", "apple", "fig"}})

	ec := newTestCtx(`{"mode":"sequence","key":"payload"}`, nil)
	var emitted []*engine.Msg
	for i, p := range parts {
		out, err := SortNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": p})
		require.NoErrorf(t, err, "part %d must be accepted into the sequence", i)
		emitted = append(emitted, out["out"]...)
	}

	require.Len(t, emitted, 3, "nothing may be released until the group is complete, and then all of it must be")
	assert.Equal(t, []any{"apple", "fig", "pear"}, payloadsOf(emitted), "the parts must come out in sorted order")

	for i := range emitted {
		require.Lenf(t, emitted[i].Group, 1, "message %d must still carry its fan-out frame: %+v", i, emitted[i].Group)
		frame := emitted[i].Group[0]
		assert.Equalf(t, i, frame.Index,
			"message %d must be renumbered to its sorted position, or a join rebuilds the old order", i)
		assert.Equalf(t, 3, frame.Count, "message %d must keep the group size, which is how a join knows it is complete", i)
	}

	// The join is the consumer the renumbering exists for.
	joined := feedJoin(t, `{"mode":"array"}`, nil, emitted)
	require.Len(t, joined, 1, "a complete group joins into one message")
	assert.Equal(t, []any{"apple", "fig", "pear"}, joined[0].Payload,
		"the rebuilt array must be sorted, which only works if the frame indexes were rewritten")
}

// TestSortSequenceNeedsACompletionRule covers sequence mode on a message with
// no group frame. Nothing would ever tell the node the set was complete, so it
// would hold every message forever; failing at the first message is how the
// author finds out, rather than watching a flow quietly stop.
func TestSortSequenceNeedsACompletionRule(t *testing.T) {
	_, err := SortNode{}.Execute(context.Background(), newTestCtx(`{"mode":"sequence"}`, nil),
		map[string]*engine.Msg{"in": {Payload: 1}})
	assert.Error(t, err,
		"with no group, no count, and no timeout the node could never release anything, so it must say so at once")
}

// TestSortSequenceClosesAStreamOnCount covers the completion rule for a stream
// that never carried a group: the author names how many messages make a set,
// and the node releases them sorted once that many have arrived.
func TestSortSequenceClosesAStreamOnCount(t *testing.T) {
	ec := newTestCtx(`{"mode":"sequence","key":"payload","count":"3","order":"desc"}`, nil)
	var emitted []*engine.Msg
	for _, v := range []any{2.0, 3.0, 1.0} {
		out, err := SortNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {Payload: v}})
		require.NoErrorf(t, err, "message %v must be accepted into the stream", v)
		emitted = append(emitted, out["out"]...)
	}

	require.Len(t, emitted, 3, "the count closes the set, so all three must be released at the third message")
	assert.Equal(t, []any{3.0, 2.0, 1.0}, payloadsOf(emitted), "descending order means the largest is released first")
}

// TestSortRejectsUnknownSettings covers a setting the node does not recognise,
// against the working configurations above. Falling back to a default would
// silently sort by something the author did not choose, which is worse than not
// sorting at all — the output looks plausible and is wrong.
func TestSortRejectsUnknownSettings(t *testing.T) {
	cases := map[string]string{
		"mode":       `{"mode":"sideways"}`,
		"order":      `{"mode":"property","order":"random"}`,
		"value_type": `{"mode":"property","value_type":"duration"}`,
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := SortNode{}.Execute(context.Background(), newTestCtx(cfg, nil),
				map[string]*engine.Msg{"in": {Payload: []any{1.0}}})
			assert.Errorf(t, err,
				"an unknown %s must fail rather than fall back: a plausible wrong order is worse than none", name)
		})
	}
}

// payloadsOf renders emitted payloads for failure messages.
func payloadsOf(msgs []*engine.Msg) []any {
	out := make([]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Payload)
	}
	return out
}

// TestSortKeepsOrderingAroundNotANumber locks that a value reading "NaN" is
// not compared as a number. It parses as a float that is neither below nor
// above anything, which made it equal to every neighbor and left the numbers
// on either side of it unsorted.
func TestSortKeepsOrderingAroundNotANumber(t *testing.T) {
	for _, tc := range []struct {
		name, cfg string
		in, want  []any
	}{
		{"numbers", `{"value_type":"number"}`, []any{"10", "NaN", "9"}, []any{"NaN", "9", "10"}},
		{"auto", `{}`, []any{"2", "nan", "1"}, []any{"1", "2", "nan"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := runSort(t, tc.cfg, &engine.Msg{Payload: tc.in})
			assert.Equal(t, tc.want, arrayPayload(t, msg),
				"a value that is not a number sorts as one that cannot be read, and the rest still order")
		})
	}
}
