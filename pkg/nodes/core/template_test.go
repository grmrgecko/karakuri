package core

import (
	"context"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runTemplate executes the template node against one message.
func runTemplate(t *testing.T, cfg string, msg *engine.Msg) (*testCtx, *engine.Msg) {
	t.Helper()
	ec := newTestCtx(cfg, nil)
	out, err := TemplateNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": msg})
	require.NoError(t, err, "this template must render")
	return ec, only(out, "out")
}

// TestTemplateOutputTypes covers the scalar types a rendered template can be
// read as. A template always produces text, so the output format is what turns
// it back into a value — without it every computed number would reach a device
// as a quoted string.
func TestTemplateOutputTypes(t *testing.T) {
	cases := []struct {
		name     string
		template string
		format   string
		want     any
	}{
		{"string keeps the text", "{{ payload.n }} degrees", "string", "3 degrees"},
		{"integer parses a whole number", "{{ payload.n }}", "integer", float64(3)},
		{"float parses a decimal", "{{ payload.f }}", "float", 1.5},
		{"boolean parses a flag", "{{ payload.b }}", "boolean", true},
		{"empty format defaults to string", "{{ payload.n }}", "", "3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := `{"template":"` + c.template + `","output_format":"` + c.format + `"}`
			_, out := runTemplate(t, cfg, &engine.Msg{Payload: map[string]any{"n": float64(3), "f": 1.5, "b": true}})
			assert.Equal(t, c.want, out.Payload, "the output format decides how the rendered text is read back")
		})
	}
}

// TestTemplateParsesObjects covers the JSON and YAML output types, which are
// how a template builds a structured value rather than a scalar.
func TestTemplateParsesObjects(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		cfg := `{"template":"{\"room\":\"{{ payload.room }}\",\"on\":true}","output_format":"json"}`
		_, out := runTemplate(t, cfg, &engine.Msg{Payload: map[string]any{"room": "office"}})
		obj, ok := out.Payload.(map[string]any)
		require.Truef(t, ok, "payload = %#v, want the rendered text parsed as an object", out.Payload)
		assert.Equal(t, "office", obj["room"], "the interpolated value must survive the parse")
		assert.Equal(t, true, obj["on"], "the literal parts of the template must survive too")
	})

	t.Run("yaml", func(t *testing.T) {
		cfg := `{"template":"room: {{ payload.room }}\ncount: 2","output_format":"yaml"}`
		_, out := runTemplate(t, cfg, &engine.Msg{Payload: map[string]any{"room": "office"}})
		obj, ok := out.Payload.(map[string]any)
		require.Truef(t, ok, "payload = %#v, want the rendered text parsed as an object", out.Payload)
		assert.Equal(t, "office", obj["room"], "the interpolated value must survive the parse")
		assert.Equal(t, float64(2), obj["count"],
			"a YAML integer must arrive as float64 like every other number in the engine, or expressions compare it wrong")
	})
}

// TestTemplateRejectsMistypedOutput pairs with the conversions above: text that
// cannot be read as the chosen type fails the node. The output port declared
// integer, so emitting zero or the text instead would break the promise the
// port made to everything downstream.
func TestTemplateRejectsMistypedOutput(t *testing.T) {
	ec := newTestCtx(`{"template":"n/a","output_format":"integer"}`, nil)
	_, err := TemplateNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {}})
	assert.Error(t, err,
		"the port declared an integer, so text that is not one must fail rather than emit zero")
}

// TestTemplatePlainFormatSkipsInterpolation covers the plain format, which is
// the only way to emit text containing literal double braces — a payload for
// another templating system, or documentation about this one.
func TestTemplatePlainFormatSkipsInterpolation(t *testing.T) {
	cfg := `{"template":"literal {{ payload.n }} braces","format":"plain"}`
	_, out := runTemplate(t, cfg, &engine.Msg{Payload: map[string]any{"n": float64(3)}})
	assert.Equal(t, "literal {{ payload.n }} braces", out.Payload,
		"plain format must emit the template verbatim, or literal braces are impossible to write")
}

// TestTemplateWritesToScopes covers where a rendered value lands. Writing to a
// message property has to merge rather than replace, so a template adding a
// label does not discard the reading it was computed from; writing to a
// variable has to leave the message alone entirely.
func TestTemplateWritesToScopes(t *testing.T) {
	t.Run("message property", func(t *testing.T) {
		cfg := `{"template":"{{ payload.room }}","scope":"msg","property":"payload.label"}`
		in := &engine.Msg{Payload: map[string]any{"room": "office"}}
		_, out := runTemplate(t, cfg, in)

		obj, ok := out.Payload.(map[string]any)
		require.True(t, ok, "writing a property leaves the payload an object")
		assert.Equal(t, "office", obj["label"], "the rendered value must land at the configured property")
		assert.Equal(t, "office", obj["room"],
			"the payload's existing keys must survive, or adding a label discards the reading it came from")
		assert.NotContains(t, in.Payload.(map[string]any), "label",
			"the input must not be written through: the engine recorded it and other branches may hold it")
	})

	t.Run("topic", func(t *testing.T) {
		cfg := `{"template":"home/{{ payload.room }}","scope":"msg","property":"topic"}`
		_, out := runTemplate(t, cfg, &engine.Msg{Payload: map[string]any{"room": "office"}})
		assert.Equal(t, "home/office", out.Topic,
			"a template may build the topic, which is how one flow addresses a device per room")
	})

	for _, scope := range []string{engine.ScopeFlow, engine.ScopeGlobal, engine.ScopeRun} {
		t.Run(scope+" variable", func(t *testing.T) {
			cfg := `{"template":"{{ payload.n }}","output_format":"integer","scope":"` +
				scope + `","property":"reading"}`
			in := &engine.Msg{Payload: map[string]any{"n": float64(7)}}
			ec, out := runTemplate(t, cfg, in)

			got, ok := ec.GetScoped(scope, "reading")
			require.Truef(t, ok, "the %s variable must actually be written", scope)
			assert.Equalf(t, float64(7), got, "the %s variable must hold the rendered value, typed as configured", scope)

			payload, ok := out.Payload.(map[string]any)
			require.True(t, ok, "the message passes through unchanged")
			assert.Equalf(t, float64(7), payload["n"],
				"a write to the %s scope must leave the message alone; only the variable changes", scope)
		})
	}
}

// TestTemplateRejectsUnknownScope covers a typo in the scope name, beside the
// four working scopes above. Falling back to a default scope would write the
// value somewhere nothing reads, and the flow would look like it worked.
func TestTemplateRejectsUnknownScope(t *testing.T) {
	ec := newTestCtx(`{"template":"x","scope":"glabal","property":"a"}`, nil)
	_, err := TemplateNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {}})
	assert.Error(t, err,
		"a misspelled scope must fail: writing to a default one would put the value where nothing reads it")
}

// TestTemplateReadsScopesInExpressions covers templates that interpolate the
// flow and global scopes, which is the read side of the same feature.
func TestTemplateReadsScopesInExpressions(t *testing.T) {
	ec := newTestCtx(`{"template":"{{ flow.site }}/{{ global.env }}"}`, nil)
	ec.flowVars["site"] = "hq"
	ec.globalVars["env"] = "prod"

	out, err := TemplateNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {}})
	require.NoError(t, err, "a template reading variable scopes must render")
	assert.Equal(t, "hq/prod", only(out, "out").Payload,
		"both persisted scopes must be readable from a template, which is the read side of writing them")
}

// TestManualNodePayloadTypes covers the value node's typed payload. The author
// types text into one field and picks how it is read, which is what lets the
// node's output port declare a type at all — the editor derives it from the
// same setting.
func TestManualNodePayloadTypes(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		want any
	}{
		{"string", `{"payload":"hello there","output_format":"string"}`, "hello there"},
		{"integer", `{"payload":"42","output_format":"integer"}`, float64(42)},
		{"float", `{"payload":"1.5","output_format":"float"}`, 1.5},
		{"boolean", `{"payload":"false","output_format":"boolean"}`, false},
		{"json scalar", `{"payload":"true","output_format":"json"}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := ManualNode{}.Execute(context.Background(), newTestCtx(c.cfg, nil), nil)
			require.NoError(t, err, "the authored text must be readable as the chosen type")
			assert.Equal(t, c.want, only(out, "out").Payload,
				"the chosen format decides the type, which is what the output port declares")
		})
	}
}

// TestManualNodeParsesDocuments covers the two document formats, which is how a
// value node stands in for a real payload — a webhook body or a device reading
// — while a flow is being built.
func TestManualNodeParsesDocuments(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
	}{
		{"json", `{"payload":"{\"room\":\"office\",\"n\":2}","output_format":"json"}`},
		{"yaml", `{"payload":"room: office\nn: 2","output_format":"yaml"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := ManualNode{}.Execute(context.Background(), newTestCtx(c.cfg, nil), nil)
			require.NoError(t, err, "a well-formed document must parse")

			obj, ok := only(out, "out").Payload.(map[string]any)
			require.Truef(t, ok, "payload = %#v, want a parsed object", only(out, "out").Payload)
			assert.Equal(t, "office", obj["room"], "the parsed document must hold what was written")
			assert.Equal(t, float64(2), obj["n"],
				"numbers arrive as float64 from both formats, so a rule comparing them behaves the same either way")
		})
	}
}

// TestManualNodeRejectsMistypedPayload pairs with the working types above: text
// that cannot be read as the chosen type fails the node. The output port
// declared a float, and emitting anything else would break every edge validated
// against it.
func TestManualNodeRejectsMistypedPayload(t *testing.T) {
	ec := newTestCtx(`{"payload":"not a number","output_format":"float"}`, nil)
	_, err := ManualNode{}.Execute(context.Background(), ec, nil)
	assert.Error(t, err,
		"the port declared a float, so text that is not one must fail rather than emit a surprise")
}

// TestManualNodeReadsLegacyStructuralPayload covers configs saved before the
// type setting existed, where the editor stored a decoded JSON value rather
// than the text an author typed. Those must keep working untouched.
func TestManualNodeReadsLegacyStructuralPayload(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		want any
	}{
		{"object", `{"payload":{"a":1}}`, nil},
		{"number", `{"payload":7}`, float64(7)},
		{"boolean", `{"payload":true}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := ManualNode{}.Execute(context.Background(), newTestCtx(c.cfg, nil), nil)
			require.NoError(t, err, "a config saved before the type setting existed must still run")

			got := only(out, "out").Payload
			if c.want == nil {
				obj, ok := got.(map[string]any)
				require.Truef(t, ok, "payload = %#v, want the stored object", got)
				assert.Equal(t, float64(1), obj["a"], "a stored decoded object must come back as it was saved")
				return
			}
			assert.Equal(t, c.want, got, "a stored decoded value must come back as it was saved")
		})
	}
}

// TestManualNodeStringPayloadMeansTheSameEitherWay covers the one shape both
// config forms share: a stored JSON string is the text an author typed, and
// reading text as a string returns the text, so old and new agree.
func TestManualNodeStringPayloadMeansTheSameEitherWay(t *testing.T) {
	for _, cfg := range []string{
		`{"payload":"hello"}`,
		`{"payload":"hello","output_format":"string"}`,
	} {
		out, err := ManualNode{}.Execute(context.Background(), newTestCtx(cfg, nil), nil)
		require.NoErrorf(t, err, "%s must run", cfg)
		assert.Equalf(t, "hello", only(out, "out").Payload,
			"%s: the old and new config forms must agree on a plain string, or upgrading changes what a flow emits", cfg)
	}
}

// TestManualNodeUnsetFormatKeepsUnparseableText covers the ambiguous case: a
// stored JSON string with no type chosen. A config written before the setting
// existed meant the string itself, so text that is not valid JSON is kept as
// text rather than failing a flow that worked yesterday. Choosing a type is
// what turns the check strict.
func TestManualNodeUnsetFormatKeepsUnparseableText(t *testing.T) {
	// No type chosen: the legacy reading wins over an error.
	out, err := ManualNode{}.Execute(context.Background(), newTestCtx(`{"payload":"just some words"}`, nil), nil)
	require.NoError(t, err, "a config that worked before the setting existed must keep working")
	assert.Equal(t, "just some words", only(out, "out").Payload,
		"with no type chosen, text that is not JSON is the string itself, which is what it always meant")

	// Valid JSON with no type chosen still parses, as it always did.
	out, err = ManualNode{}.Execute(context.Background(), newTestCtx(`{"payload":"{\"a\":1}"}`, nil), nil)
	require.NoError(t, err, "valid JSON with no type chosen must still parse")
	obj, ok := only(out, "out").Payload.(map[string]any)
	require.Truef(t, ok, "payload = %#v, want a parsed object", only(out, "out").Payload)
	assert.Equal(t, float64(1), obj["a"], "the old behaviour parsed JSON text, and that must not change")

	// Choosing JSON explicitly makes the same bad text an error.
	_, err = ManualNode{}.Execute(context.Background(),
		newTestCtx(`{"payload":"just some words","output_format":"json"}`, nil), nil)
	assert.Error(t, err,
		"choosing a type is what makes the check strict: the port now declares an object and must not emit a string")
}

// TestTemplateRejectsNonFiniteFloat locks that the float output type only
// produces numbers JSON can carry. strconv.ParseFloat reads "NaN" and "Inf" as
// floats, and a payload holding one cannot be stored in run history or sent on.
func TestTemplateRejectsNonFiniteFloat(t *testing.T) {
	for _, text := range []string{"NaN", "Inf", "-infinity"} {
		t.Run(text, func(t *testing.T) {
			ec := newTestCtx(`{"template":"`+text+`","output_format":"float"}`, nil)
			_, err := TemplateNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {}})
			assert.Error(t, err, "the port declared a number, so a non-finite word must fail rather than emit one")
		})
	}
}
