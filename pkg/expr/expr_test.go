package expr

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// env returns the environment a node expression is evaluated against: the
// message itself, the outputs of upstream nodes, and the four variable scopes.
func env() map[string]any {
	return BuildEnv(Env{
		Payload: map[string]any{"temperature": 21.5, "name": "office", "on": true},
		Topic:   "home/office",
		Meta:    map[string]any{"qos": 1},
		Nodes: map[string]map[string]any{
			"switch_1": {"payload": map[string]any{"value": 42.0}, "topic": "t", "meta": map[string]any{}},
		},
		Vars:   map[string]any{"attempts": 2},
		Flow:   map[string]any{"count": 7},
		Global: map[string]any{"site": "hq"},
	})
}

// TestEvalEnvAccess pins the names an author may type in a condition field.
// They are the documented surface of every expression in the app — the switch
// node, change rules, templates — so a renamed binding silently breaks saved
// flows rather than failing to compile. A path that does not exist reads as
// nil instead of erroring, which is what lets a rule test for an absent field.
func TestEvalEnvAccess(t *testing.T) {
	cases := []struct {
		src  string
		want any
		why  string
	}{
		{src: "payload.temperature", want: 21.5},
		{src: "topic", want: "home/office"},
		{src: "meta.qos", want: 1},
		{src: "msg.payload.name", want: "office", why: "msg is the whole message, for authors who spell it out"},
		{src: "nodes.switch_1.payload.value", want: 42.0, why: "an upstream node's output is addressed by node id"},
		{src: "payload.temperature > 20", want: true},
		{src: "payload.missing", want: nil, why: "an absent field is nil, so a rule can test for it"},
	}
	for _, c := range cases {
		got, err := Eval(c.src, env())
		require.NoErrorf(t, err, "%q is a documented expression and must evaluate: %s", c.src, c.why)
		assert.Equalf(t, c.want, got, "Eval(%q): %s", c.src, c.why)
	}

	// An expression that will not compile is reported, never silently treated
	// as false: a switch node with a typo has to fail the node rather than
	// quietly route every message down the same branch.
	_, err := Eval("payload.[", env())
	assert.Error(t, err, "an unparseable expression must fail the node rather than evaluate to false")
}

// TestEvalTriggerEnvHasNoNodes covers the environment a trigger evaluates in.
// Nothing has run yet, so there are no node outputs to read; the binding is
// absent rather than empty, and reading it yields nil rather than erroring, so
// an expression shared between a trigger and a mid-graph node still evaluates
// in both places.
func TestEvalTriggerEnvHasNoNodes(t *testing.T) {
	e := BuildEnv(Env{Payload: map[string]any{}})
	assert.NotContains(t, e, "nodes", "nothing has run yet, so there are no node outputs to expose")

	got, err := Eval("nodes", e)
	require.NoError(t, err, "an undefined name must not fail the expression")
	assert.Nil(t, got, "reading an absent binding yields nil, so one expression works in both environments")
}

// TestCompileMemoizes covers the program cache. Expressions are compiled on
// every message, so the same source has to return the same compiled program
// rather than recompiling; and because the sources come from user config there
// is no bound on how many distinct ones a busy instance sees, which is what
// the cap is for.
func TestCompileMemoizes(t *testing.T) {
	first, err := Compile("payload.temperature + 1")
	require.NoError(t, err, "a valid expression must compile")
	second, err := Compile("payload.temperature + 1")
	require.NoError(t, err, "compiling the same source again must succeed")
	assert.Same(t, first, second, "the same source must return the cached program rather than recompiling per message")

	// Fill past the cap with distinct sources; the cache must stay usable and
	// must not grow without bound.
	for i := 0; i < cacheCap+10; i++ {
		src := "1 + " + strings.Repeat("1+", i%600) + "1"
		_, err := Compile(src)
		require.NoErrorf(t, err, "compile #%d must succeed while the cache is being evicted", i)
	}
	cache.mu.Lock()
	size := len(cache.programs)
	cache.mu.Unlock()
	assert.LessOrEqual(t, size, cacheCap, "the cache must stay within its cap: the sources come from user config and are unbounded")
}

// TestInterpolate pins the {{ }} substitution used in every text field that
// accepts an expression. In a string context the result is rendered as JSON,
// so a number loses its quotes and nil renders as null; text that is not a
// complete segment is left exactly as typed rather than being swallowed, which
// is what lets a template contain a literal brace.
func TestInterpolate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		why  string
	}{
		{name: "no segments", in: "plain text", want: "plain text"},
		{name: "single string", in: "{{ payload.name }}", want: "office"},
		{name: "a number renders without quotes", in: "{{ payload.temperature }}", want: "21.5"},
		{name: "multiple segments", in: "{{ payload.name }} is {{ payload.temperature }}C", want: "office is 21.5C"},
		{
			name: "a non-string result is marshaled", in: "v={{ payload }}",
			want: `v={"name":"office","on":true,"temperature":21.5}`,
			why:  "an object in a string context is its JSON",
		},
		{name: "nil renders null", in: "{{ payload.missing }}", want: "null"},
		{name: "boolean", in: "on={{ payload.on }}", want: "on=true"},
		{
			name: "an unterminated segment stays literal", in: "a {{ payload.name", want: "a {{ payload.name",
			why: "text that is not a complete segment is left as typed",
		},
		{name: "whitespace inside braces", in: "{{   topic   }}", want: "home/office"},
		{
			name: "a nested map literal", in: `set {{ {"state": {"temp": payload.temperature}} }} now`,
			want: `set {"state":{"temp":21.5}} now`,
			why:  "the literal's own closing braces do not end the segment",
		},
		{
			name: "a nested map literal against the closing marker", in: `{{{"state":{"on":payload.on}}}}`,
			want: `{"state":{"on":true}}`,
			why:  "the closing marker is the last two of the four braces",
		},
		{
			name: "segments on both sides of a nested map", in: `{{ topic }} {{ {"a": {"b": 1}} }} {{ payload.name }}`,
			want: `home/office {"a":{"b":1}} office`,
			why:  "each segment still ends at its own marker",
		},
	}
	for _, c := range cases {
		got, err := Interpolate(c.in, env())
		require.NoErrorf(t, err, "%s must interpolate: %s", c.name, c.why)
		assert.Equalf(t, c.want, got, "%s: %s", c.name, c.why)
	}

	_, err := Interpolate("x={{ payload.[ }}", env())
	assert.Error(t, err, "a segment that will not compile must fail rather than render as empty text")
}

// TestInterpolateKeepsMarkupCharactersReadable locks the text a structured
// value renders as. A template's output is a notification body or a request
// document as often as it is markup, and the default JSON encoder rewrites
// the ampersand and angle brackets as \u escapes, so a list of names or a URL
// with a query string reached its reader as escape codes.
func TestInterpolateKeepsMarkupCharactersReadable(t *testing.T) {
	e := BuildEnv(Env{Payload: map[string]any{"names": []any{"Tom & Ann", "<attic>"}}})
	got, err := Interpolate("Open: {{ payload.names }}", e)
	require.NoError(t, err, "a list in a string context must interpolate")
	assert.Equal(t, `Open: ["Tom & Ann","<attic>"]`, got,
		"a structured value must render as the JSON its author would write, not with its text escaped for HTML")
}

// TestInterpolateValue covers the typed variant. A field holding one whole
// expression keeps that expression's type, so a numeric config field bound to
// an expression stays a number; mixing it with any other text makes the result
// a string, because there is nothing else it could be.
func TestInterpolateValue(t *testing.T) {
	whole, err := InterpolateValue("{{ payload.temperature }}", env())
	require.NoError(t, err, "a whole-field expression must evaluate")
	assert.Equal(t, 21.5, whole, "one whole expression keeps its own type, so a numeric field stays numeric")

	mixed, err := InterpolateValue("temp: {{ payload.temperature }}", env())
	require.NoError(t, err, "a mixed field must interpolate")
	assert.Equal(t, "temp: 21.5", mixed, "text around an expression can only produce a string")

	nested, err := InterpolateValue(`{{ {"state": {"temp": payload.temperature}} }}`, env())
	require.NoError(t, err, "a whole-field nested map literal must evaluate")
	assert.Equal(t, map[string]any{"state": map[string]any{"temp": 21.5}}, nested,
		"a map literal ending in }} is still one whole expression and keeps its type")

	two, err := InterpolateValue("{{ payload.name }} and {{ topic }}", env())
	require.NoError(t, err, "two segments must interpolate")
	assert.Equal(t, "office and home/office", two, "two segments are text even though the field starts and ends with markers")
}

// TestInterpolateDocument covers the whole-document walk behind a widget's
// command payload and an MQTT publish body: every string in the document is
// interpolated in place, with the same whole-field typing rule as a single
// field. That rule is what lets an author write a JSON template whose numbers
// arrive as numbers — a device that receives "102" instead of 102 rejects it.
func TestInterpolateDocument(t *testing.T) {
	values := map[string]any{"value": 40, "on": true, "mode": "Cool"}
	source := `{
	  "state": "{{ on ? 'ON' : 'OFF' }}",
	  "brightness": "{{ value * 255 / 100 }}",
	  "label": "level {{ value }}",
	  "mode": "{{ mode == 'Cool' ? 3 : 1 }}",
	  "steps": ["{{ value }}", 2],
	  "fixed": "colorloop"
	}`

	var document any
	require.NoError(t, json.Unmarshal([]byte(source), &document), "the fixture must be JSON")

	filled, err := InterpolateDocument(document, values)
	require.NoError(t, err, "a document of valid expressions must fill")

	out, err := json.Marshal(filled)
	require.NoError(t, err, "the filled document must re-encode")
	// brightness is 40 * 255 / 100 = 102, and stays a number because the field
	// holds nothing but the expression; label keeps its surrounding text and
	// so stays a string.
	assert.JSONEq(t,
		`{"brightness":102,"fixed":"colorloop","label":"level 40","mode":3,"state":"ON","steps":[40,2]}`,
		string(out),
		"whole-field expressions keep their type through the walk; everything else interpolates as text")

	_, err = InterpolateDocument(map[string]any{"a": "{{ ( }}"}, map[string]any{})
	assert.Error(t, err, "one unparseable expression must fail the document rather than publish a half-filled payload")
}
