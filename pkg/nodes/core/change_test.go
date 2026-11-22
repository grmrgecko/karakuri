package core

import (
	"context"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runChange applies a change node config to a message and returns the result.
func runChange(t *testing.T, cfg string, in *engine.Msg) *engine.Msg {
	t.Helper()
	out, err := ChangeNode{}.Execute(context.Background(), newTestCtx(cfg, nil),
		map[string]*engine.Msg{"in": in})
	require.NoError(t, err, "these rules must apply cleanly")

	msg := only(out, "out")
	require.NotNil(t, msg, "a change node always emits: it rewrites a message rather than filtering it")
	return msg
}

// TestChangeSetsValuesByType covers every value source a set rule can name.
// The source is what decides the resulting type, which is the whole reason it
// is a field rather than being guessed from the text: 128 typed as a number
// stays a number, and a device that receives "128" as a string rejects it.
func TestChangeSetsValuesByType(t *testing.T) {
	in := &engine.Msg{Payload: map[string]any{"state": "on"}, Topic: "light/kitchen"}
	out := runChange(t, `{"rules":[
		{"op":"set","target":"payload.brightness","from":"number","value":128},
		{"op":"set","target":"payload.on","from":"boolean","value":true},
		{"op":"set","target":"payload.label","from":"string","value":"kitchen is {{ payload.state }}"},
		{"op":"set","target":"payload.opts","from":"json","value":{"fade":2}},
		{"op":"set","target":"payload.doubled","from":"expression","value":"1 + 1"},
		{"op":"set","target":"topic","from":"string","value":"light/kitchen/set"}
	]}`, in)

	payload, ok := out.Payload.(map[string]any)
	require.True(t, ok, "setting properties leaves the payload an object")

	assert.Equal(t, float64(128), payload["brightness"], "a number source produces a number, not the text 128")
	assert.Equal(t, true, payload["on"], "a boolean source produces a boolean")
	assert.Equal(t, "kitchen is on", payload["label"], "a string source interpolates against the incoming message")

	opts, ok := payload["opts"].(map[string]any)
	require.Truef(t, ok, "a json source produces a decoded object, got %T", payload["opts"])
	assert.Equal(t, float64(2), opts["fade"], "the decoded object must hold what was written")

	assert.Equal(t, 2, payload["doubled"], "an expression source produces its evaluated result")
	assert.Equal(t, "light/kitchen/set", out.Topic, "a rule may target the topic as well as the payload")
}

// TestChangeCreatesNestedPaths covers writing to a path whose parents do not
// exist yet. They are created rather than the write failing, so an author
// building a command payload from an empty message does not have to set each
// level in its own rule.
func TestChangeCreatesNestedPaths(t *testing.T) {
	out := runChange(t, `{"rules":[
		{"op":"set","target":"payload.a.b.c","from":"number","value":1}
	]}`, &engine.Msg{Payload: map[string]any{}})

	assert.Equal(t, map[string]any{"a": map[string]any{"b": map[string]any{"c": float64(1)}}}, out.Payload,
		"the missing parents must be created, or building a nested payload needs one rule per level")
}

// TestChangeDeletesMovesAndCopies covers the three rules that relocate rather
// than write. Move and copy differ only in what happens to the source, which is
// the entire distinction between them and the thing worth pinning: a move that
// left the source behind would silently be a copy.
func TestChangeDeletesMovesAndCopies(t *testing.T) {
	in := &engine.Msg{
		Payload: map[string]any{"keep": 1, "drop": 2, "from": "value"},
		Meta:    map[string]any{"source": "test"},
	}
	out := runChange(t, `{"rules":[
		{"op":"delete","target":"payload.drop"},
		{"op":"move","target":"payload.to","value":"payload.from"},
		{"op":"copy","target":"meta.copied","value":"payload.keep"}
	]}`, in)

	payload, ok := out.Payload.(map[string]any)
	require.True(t, ok, "the payload stays an object")

	assert.NotContains(t, payload, "drop", "a delete rule must remove the property")
	assert.Equal(t, "value", payload["to"], "a move must land the value at its target")
	assert.NotContains(t, payload, "from", "a move must remove the source, or it is a copy wearing the wrong name")
	assert.Equal(t, 1, out.Meta["copied"], "a copy may target metadata as well as the payload")
	assert.Equal(t, 1, payload["keep"], "a copy must leave the source in place, which is what separates it from a move")
}

// TestChangeSearchAndReplace covers the replace_all switch against a value
// holding two matches. The default rewrites only the first, which is what makes
// the flag meaningful — without a payload containing a repeat, both settings
// would look identical.
func TestChangeSearchAndReplace(t *testing.T) {
	in := &engine.Msg{Payload: map[string]any{"text": "one two one two"}}

	// Text search rewrites the first match by default, every match when asked.
	out := runChange(t, `{"rules":[
		{"op":"change","target":"payload.text","search":"one","replace":"1"}
	]}`, in)
	assert.Equal(t, "1 two one two", out.Payload.(map[string]any)["text"],
		"by default only the first match is rewritten, leaving the second in place")

	out = runChange(t, `{"rules":[
		{"op":"change","target":"payload.text","search":"one","replace":"1","replace_all":true}
	]}`, in)
	assert.Equal(t, "1 two 1 two", out.Payload.(map[string]any)["text"],
		"replace_all must rewrite every match, which is only visible on a value that repeats")
}

// TestChangeReplacesWithRegexGroups covers capture groups in a replacement,
// which is what lets one rule rewrite a whole family of topics — turning every
// light.* topic into lamp.* without naming each room.
func TestChangeReplacesWithRegexGroups(t *testing.T) {
	out := runChange(t, `{"rules":[
		{"op":"change","target":"topic","search_type":"regex","search":"^light\\.(.+)$",
		 "replace":"lamp.$1","replace_all":true}
	]}`, &engine.Msg{Topic: "light.kitchen"})
	assert.Equal(t, "lamp.kitchen", out.Topic,
		"$1 must expand to the captured group, or the rule would have to be written per room")
}

// TestChangeReplacesWholeNonStringValues covers a replace rule over a value
// that is not text. There is no substring to search, so the value is compared
// whole and replaced with the typed replacement — the result stays a number
// rather than becoming the text "10".
func TestChangeReplacesWholeNonStringValues(t *testing.T) {
	out := runChange(t, `{"rules":[
		{"op":"change","target":"payload.level","search":"5","from":"number","replace":"10"}
	]}`, &engine.Msg{Payload: map[string]any{"level": float64(5)}})
	assert.Equal(t, float64(10), out.Payload.(map[string]any)["level"],
		"the replacement keeps the type its source names, so the value stays a number")
}

// TestChangeReplacementResolvesItsValueSource covers a replacement that is
// itself computed: the expression is evaluated first, then its result is what
// gets inserted. That is how a template placeholder is filled from elsewhere in
// the same message.
func TestChangeReplacementResolvesItsValueSource(t *testing.T) {
	out := runChange(t, `{"rules":[
		{"op":"change","target":"payload.text","search":"NAME","from":"expression",
		 "replace":"payload.who"}
	]}`, &engine.Msg{Payload: map[string]any{"text": "hello NAME", "who": "kitchen"}})
	assert.Equal(t, "hello kitchen", out.Payload.(map[string]any)["text"],
		"the expression must be evaluated before insertion, not inserted as its own source text")
}

// TestChangeReplaceLeavesNonMatchesAlone covers what a replace rule does when
// it finds nothing. Leaving the value untouched is obvious; the third row is
// the one that matters, since a rule writing its target unconditionally would
// create a property that never existed and change the payload's shape.
func TestChangeReplaceLeavesNonMatchesAlone(t *testing.T) {
	in := &engine.Msg{Payload: map[string]any{"text": "hello", "level": float64(5)}}
	out := runChange(t, `{"rules":[
		{"op":"change","target":"payload.text","search":"goodbye","replace":"hi"},
		{"op":"change","target":"payload.level","search":"9","from":"number","replace":"10"},
		{"op":"change","target":"payload.absent","search":"a","replace":"b"}
	]}`, in)

	payload, ok := out.Payload.(map[string]any)
	require.True(t, ok, "the payload stays an object")

	assert.Equal(t, "hello", payload["text"], "text that does not contain the search value must be left alone")
	assert.Equal(t, float64(5), payload["level"], "a number that does not equal the search value must be left alone")
	assert.NotContains(t, payload, "absent",
		"a replace on a property that is not there must not create it: that would change the payload's shape")
}

// TestChangeDoesNotMutateTheInputMessage covers the copy the node makes before
// it writes. The engine recorded the incoming message in run history and other
// branches may still be reading it, so writing through a nested map would
// rewrite what the debug panel shows and what a parallel branch sees. Nested
// maps are the case that matters: a shallow copy would pass a top-level check
// and still alias everything below it. An expression source returns objects
// held by the input itself, so its result needs the same copy.
func TestChangeDoesNotMutateTheInputMessage(t *testing.T) {
	in := &engine.Msg{
		Payload: map[string]any{"nested": map[string]any{"value": 1}},
		Meta:    map[string]any{"source": "test"},
	}
	runChange(t, `{"rules":[
		{"op":"set","target":"payload.nested.value","from":"number","value":99},
		{"op":"set","target":"meta.extra","from":"string","value":"x"}
	]}`, in)

	nested, ok := in.Payload.(map[string]any)["nested"].(map[string]any)
	require.True(t, ok, "the fixture's nested object must still be one")
	assert.Equal(t, 1, nested["value"],
		"a nested write must not reach the input: run history and any parallel branch are still holding it")
	assert.NotContains(t, in.Meta, "extra", "the input's metadata must not be written through either")

	in = &engine.Msg{Payload: map[string]any{"light": map[string]any{"state": "on"}}}
	runChange(t, `{"rules":[
		{"op":"set","target":"payload.snapshot","from":"expression","value":"payload.light"},
		{"op":"set","target":"payload.snapshot.state","from":"string","value":"off"}
	]}`, in)

	light, ok := in.Payload.(map[string]any)["light"].(map[string]any)
	require.True(t, ok, "the fixture's light object must still be one")
	assert.Equal(t, "on", light["state"],
		"an expression evaluates against the input, so the object it returns must be copied before a later rule writes into it")
}

// TestChangeRulesApplyInOrder covers rules being applied in sequence against
// the running message rather than all against the original. That is what lets a
// rule build on the one before it, and it is why the last rule to touch the
// payload is the one that decides the output port's type.
func TestChangeRulesApplyInOrder(t *testing.T) {
	out := runChange(t, `{"rules":[
		{"op":"set","target":"payload.first","from":"number","value":5},
		{"op":"copy","target":"payload.second","value":"payload.first"}
	]}`, &engine.Msg{Payload: map[string]any{}})

	assert.Equal(t, float64(5), out.Payload.(map[string]any)["second"],
		"the copy rule must see what the set rule before it wrote, not the message as it arrived")
}

// TestChangeReportsBadRules covers the rules the node refuses at run time,
// against the many working rules above. A change node that silently skipped a
// rule it could not apply would emit a message missing exactly the field the
// author added the rule for, and nothing downstream would know why.
func TestChangeReportsBadRules(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		why  string
	}{
		{name: "no rules", cfg: `{"rules":[]}`, why: "a change node that changes nothing is a mistake, not a passthrough"},
		{name: "unknown op", cfg: `{"rules":[{"op":"frobnicate","target":"payload.x"}]}`},
		{name: "no target", cfg: `{"rules":[{"op":"set","value":"x"}]}`, why: "there is nowhere to write it"},
		{name: "unknown value source", cfg: `{"rules":[{"op":"set","target":"payload.x","from":"telepathy","value":"x"}]}`},
		{
			name: "non-numeric number", cfg: `{"rules":[{"op":"set","target":"payload.x","from":"number","value":"abc"}]}`,
			why: "the source promised a number, so writing zero or the text instead would break the declared type",
		},
		{
			name: "missing move source", cfg: `{"rules":[{"op":"move","target":"payload.x","value":"payload.nope"}]}`,
			why: "a move with nothing to move would silently produce a message without the field",
		},
		{
			name: "sub-property of topic", cfg: `{"rules":[{"op":"set","target":"topic.sub","from":"string","value":"x"}]}`,
			why: "the topic is a string and has no properties to write inside",
		},
		{name: "empty search", cfg: `{"rules":[{"op":"change","target":"topic","replace":"y"}]}`},
		{
			name: "invalid regex", cfg: `{"rules":[{"op":"change","target":"topic","search_type":"regex","search":"a("}]}`,
			why: "an unparseable pattern matches nothing, which is indistinguishable from a rule that did not apply",
		},
	}
	for _, c := range cases {
		_, err := ChangeNode{}.Execute(context.Background(), newTestCtx(c.cfg, nil),
			map[string]*engine.Msg{"in": {Payload: map[string]any{}}})
		assert.Errorf(t, err, "%s must fail the node rather than be skipped: %s", c.name, c.why)
	}
}

// TestChangeCopyDetachesTheCopy locks that a copied object is a snapshot: a
// later rule rewriting the source must not rewrite the copy too. Copying the
// payload aside before changing it is how a flow keeps the "before" value, and
// a shared map made both paths read the new state.
func TestChangeCopyDetachesTheCopy(t *testing.T) {
	out := runChange(t, `{"rules":[
		{"op":"copy","target":"meta.before","value":"payload"},
		{"op":"set","target":"payload.snapshot","from":"property","value":"payload.light"},
		{"op":"set","target":"payload.light.state","from":"string","value":"off"}
	]}`, &engine.Msg{Payload: map[string]any{"light": map[string]any{"state": "on"}}})

	before, ok := out.Meta["before"].(map[string]any)
	require.True(t, ok, "the copy must land as an object: %#v", out.Meta["before"])
	assert.Equal(t, "on", before["light"].(map[string]any)["state"],
		"a copy op must keep the value as it was when the rule ran")

	snapshot, ok := out.Payload.(map[string]any)["snapshot"].(map[string]any)
	require.True(t, ok, "the property source must land as an object")
	assert.Equal(t, "on", snapshot["state"],
		"a set from a property must keep the value as it was when the rule ran")
}

// TestChangeMoveBetweenNestedPaths locks that a move keeps the value when its
// source and target overlap. Wrapping the payload under a key of itself is the
// ordinary way to build an envelope, and writing the target before clearing
// the source deleted the value that had just been moved.
func TestChangeMoveBetweenNestedPaths(t *testing.T) {
	cases := []struct {
		name   string
		rule   string
		in     any
		expect any
	}{
		{name: "a scalar payload into a key of the payload",
			rule: `{"op":"move","target":"payload.data","value":"payload"}`,
			in:   "on", expect: map[string]any{"data": "on"}},
		{name: "an object payload into a key of the payload",
			rule: `{"op":"move","target":"payload.data","value":"payload"}`,
			in:   map[string]any{"state": "on"}, expect: map[string]any{"data": map[string]any{"state": "on"}}},
		{name: "a property into a key of itself",
			rule: `{"op":"move","target":"payload.a.b","value":"payload.a"}`,
			in:   map[string]any{"a": float64(1)}, expect: map[string]any{"a": map[string]any{"b": float64(1)}}},
		{name: "a property up into its parent",
			rule:   `{"op":"move","target":"payload.a","value":"payload.a.b"}`,
			in:     map[string]any{"a": map[string]any{"b": map[string]any{"b": float64(1)}}},
			expect: map[string]any{"a": map[string]any{"b": float64(1)}}},
	}
	for _, c := range cases {
		out := runChange(t, `{"rules":[`+c.rule+`]}`, &engine.Msg{Payload: c.in})
		assert.Equalf(t, c.expect, out.Payload, "moving %s must keep the moved value", c.name)
	}
}

// TestChangeDoesNotWriteThroughNestedMeta locks that a write or a delete below
// the top level of meta stays off the input message. Only the top-level map is
// copied on the way in, so a path such as meta.http.status reached the nested
// object run history and every sibling branch still hold.
func TestChangeDoesNotWriteThroughNestedMeta(t *testing.T) {
	in := &engine.Msg{Meta: map[string]any{"http": map[string]any{"status": 200, "url": "http://device/"}}}
	out := runChange(t, `{"rules":[
		{"op":"set","target":"meta.http.status","from":"number","value":500},
		{"op":"delete","target":"meta.http.url"}
	]}`, in)

	assert.Equal(t, map[string]any{"status": float64(500)}, out.Meta["http"],
		"the output carries the rewritten metadata")
	assert.Equal(t, map[string]any{"status": 200, "url": "http://device/"}, in.Meta["http"],
		"the input's nested metadata must stay as it arrived")
}
