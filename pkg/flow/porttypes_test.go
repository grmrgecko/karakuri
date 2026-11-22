package flow

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decoderOut is the shape a decoder's output port has: an object, unless the
// result lands somewhere other than the payload.
func decoderOut() PortSpec {
	return PortSpec{
		ID: "out", Type: PortObject,
		TypeRule: &PortTypeRule{PayloadPath: []string{"target", "property"}},
	}
}

// TestResolvePortTypeCases covers picking a type from the node's config: the
// first matching case wins, alternatives within a case are an "or", and a
// config that matches nothing falls back to the default.
func TestResolvePortTypeCases(t *testing.T) {
	spec := PortSpec{
		ID: "out", Type: PortString,
		TypeRule: &PortTypeRule{
			Cases: []PortTypeCase{
				{Match: map[string][]string{"format": {"lines"}}, Type: PortObject},
				{Match: map[string][]string{"format": {"binary", "bytes"}}, Type: PortBinary},
			},
			Default: PortString,
		},
	}
	cases := []struct {
		config string
		want   string
		why    string
	}{
		{`{"format":"lines"}`, PortObject, "the matching case decides"},
		{`{"format":"binary"}`, PortBinary, "the matching case decides"},
		{`{"format":"bytes"}`, PortBinary, "alternatives within a case are an or"},
		{`{"format":"text"}`, PortString, "an unmatched value falls back to the default"},
		{`{}`, PortString, "an absent field falls back to the default"},
		{``, PortString, "an empty config falls back to the default"},
		{`not json`, PortString, "an unreadable config falls back to the default"},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, ResolvePortType(spec, json.RawMessage(c.config)),
			"ResolvePortType(%s): %s", c.config, c.why)
	}
}

// TestResolvePortTypeMatchesNonStrings covers rules written against booleans
// and numbers, which a select field is not the only way to configure.
func TestResolvePortTypeMatchesNonStrings(t *testing.T) {
	spec := PortSpec{
		ID: "out", Type: PortAny,
		TypeRule: &PortTypeRule{
			Cases:   []PortTypeCase{{Match: map[string][]string{"parse_json": {"false"}}, Type: PortString}},
			Default: PortAny,
		},
	}
	assert.Equal(t, PortString, ResolvePortType(spec, json.RawMessage(`{"parse_json":false}`)),
		"a boolean false must match the case written as the string \"false\"")
	assert.Equal(t, PortAny, ResolvePortType(spec, json.RawMessage(`{"parse_json":true}`)),
		"a boolean true must not match it, or the rule would fire on any boolean at all")
}

// TestResolvePortTypePayloadPath covers the rule that keeps a port type honest:
// it describes the payload, so a node writing somewhere else must not claim the
// payload became something it did not.
func TestResolvePortTypePayloadPath(t *testing.T) {
	cases := []struct {
		config string
		want   string
		why    string
	}{
		{`{}`, PortObject, "no path configured means the payload"},
		{`{"property":"payload"}`, PortObject, "the payload itself takes the derived type"},
		{`{"target":"payload"}`, PortObject, "the target wins over the property"},

		// Writing into the payload says nothing about the value at that path,
		// but does say the payload is an object.
		{`{"target":"payload.parsed"}`, PortObject, "a nested write makes the payload an object"},
		{`{"property":"payload.body"}`, PortObject, "a nested read means the payload is an object"},
		{`{"property":"body"}`, PortObject, "a bare path is read from the payload"},

		// The topic and metadata leave the payload exactly as it arrived.
		{`{"target":"meta.parsed"}`, PortAny, "writing metadata leaves the payload alone"},
		{`{"target":"topic"}`, PortAny, "writing the topic leaves the payload alone"},

		// An empty target falls back to the property that was converted, which
		// is what an unconfigured target means.
		{`{"property":"meta.body","target":""}`, PortAny, "an empty target falls back to the property"},
		{`{"property":"meta.body","target":"payload"}`, PortObject, "a set target wins"},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, ResolvePortType(decoderOut(), json.RawMessage(c.config)),
			"ResolvePortType(%s): %s", c.config, c.why)
	}
}

// TestResolvePortTypeWildcard covers the "*" alternative, which a case uses to
// turn on a field being set at all rather than holding a particular value.
func TestResolvePortTypeWildcard(t *testing.T) {
	spec := PortSpec{
		ID: "in", Type: PortObject,
		TypeRule: &PortTypeRule{
			Cases: []PortTypeCase{{Match: map[string][]string{"root": {"*"}}, Type: PortAny}},
		},
	}
	assert.Equal(t, PortAny, ResolvePortType(spec, json.RawMessage(`{"root":"envelope"}`)),
		"any non-empty value satisfies the wildcard, which is what turning the field on means")
	assert.Equal(t, PortObject, ResolvePortType(spec, json.RawMessage(`{"root":""}`)),
		"a field cleared back to empty is off again, so the declared type stands")
	assert.Equal(t, PortObject, ResolvePortType(spec, json.RawMessage(`{}`)),
		"a field never set is off, so the declared type stands")
}

// TestResolvePortTypeCasePayloadPath covers a case scoping the payload path to
// itself: a mode that never reads the path field must not have its ports
// colored by a stale value the field kept from another mode.
func TestResolvePortTypeCasePayloadPath(t *testing.T) {
	spec := PortSpec{
		ID: "out", Type: PortAny,
		TypeRule: &PortTypeRule{
			Cases: []PortTypeCase{{
				Match:       map[string][]string{"mode": {"", "property"}},
				Type:        PortObject,
				PayloadPath: []string{"property"},
			}},
			Default: PortAny,
		},
	}
	cases := []struct {
		config string
		want   string
		why    string
	}{
		{`{"mode":"property","property":"payload"}`, PortObject, "the matched case applies its own path"},
		{`{"mode":"property","property":"meta.x"}`, PortAny, "the case's path lands off the payload"},
		{`{"mode":"sequence","property":"payload.x"}`, PortAny, "an unmatched case ignores the stale path"},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, ResolvePortType(spec, json.RawMessage(c.config)),
			"ResolvePortType(%s): %s", c.config, c.why)
	}
}

// TestResolvePortTypesAssertedAnyClearsTypeFrom covers the propagation contract:
// a rule falling through to its default defers, so TypeFrom may still fill the
// type in from upstream, but a case that names "any" states the payload is
// config-defined and unknowable, which drops TypeFrom outright.
func TestResolvePortTypesAssertedAnyClearsTypeFrom(t *testing.T) {
	ports := []PortSpec{{
		ID: "out", Type: PortAny, TypeFrom: "in",
		TypeRule: &PortTypeRule{
			Cases:   []PortTypeCase{{Match: map[string][]string{"source": {"payload"}}, Type: PortAny}},
			Default: PortAny,
		},
	}}

	resolved := resolvePortTypes(ports, json.RawMessage(`{"source":"payload"}`))
	assert.Empty(t, resolved[0].TypeFrom,
		"a case naming any asserts the payload is config-defined, so nothing upstream can fill it in")
	assert.Equal(t, "in", ports[0].TypeFrom,
		"the slice belongs to the node type and must survive one placed node's resolution")

	deferred := resolvePortTypes(ports, json.RawMessage(`{"source":"original"}`))
	assert.Equal(t, "in", deferred[0].TypeFrom,
		"falling through to the default only defers, so propagation may still supply the type")
}

// TestResolvePortTypeWithoutARule covers the ordinary port, whose type does not
// depend on how the node is set up.
func TestResolvePortTypeWithoutARule(t *testing.T) {
	spec := PortSpec{ID: "out", Type: PortObject}
	assert.Equal(t, PortObject, ResolvePortType(spec, json.RawMessage(`{"format":"binary"}`)),
		"a port with no rule keeps its declared type however the node is configured")
}

// TestNodePortsResolvesTypes covers the derivation reaching the placed node's
// ports, which is what validation and the editor both read.
func TestNodePortsResolvesTypes(t *testing.T) {
	meta := NodeMeta{
		Type:    "decoder",
		Inputs:  []PortSpec{{ID: "in", Type: PortString, TypeRule: &PortTypeRule{PayloadPath: []string{"property"}}}},
		Outputs: []PortSpec{decoderOut()},
	}
	n := &Node{ID: "n1", Type: "decoder", Config: json.RawMessage(`{"property":"meta.body"}`)}

	set := NodePorts(meta, n, nil)

	in, ok := set.Input("in")
	require.True(t, ok, "the decoder declares an input, so the placed node must have one")
	assert.Equal(t, PortAny, in.Type, "reading from metadata says nothing about the payload arriving")

	out, ok := set.Output("out")
	require.True(t, ok, "the decoder declares an output, so the placed node must have one")
	assert.Equal(t, PortAny, out.Type, "the decoded result lands on the metadata, so the payload is untouched")

	// The node type's own slice is shared by every placed node of that type, so
	// resolving one node's ports must not rewrite it.
	assert.Equal(t, PortString, meta.Inputs[0].Type, "the node type's declared input must survive resolution")
	assert.Equal(t, PortObject, meta.Outputs[0].Type, "the node type's declared output must survive resolution")
}
