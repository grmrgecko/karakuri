package core

import (
	"encoding/json"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolvePorts resolves a node type's ports for one config, the way
// validation and the editor do.
func resolvePorts(t *testing.T, meta engine.NodeMeta, config string) flow.PortSet {
	t.Helper()
	n := &flow.Node{ID: "n", Type: meta.Type, Config: json.RawMessage(config)}
	return flow.NodePorts(meta, n, nil)
}

// TestTriggerPortTypes covers the trigger node's outputs, which carry the
// input onward only when their source setting says so: a fixed payload is a
// configured JSON value of unknowable type, and propagation must not dress it
// in the upstream's type.
func TestTriggerPortTypes(t *testing.T) {
	meta := TriggerNode{}.Meta()
	cases := []struct {
		name   string
		config string
		port   string
		keeps  bool
	}{
		{"out carries the original", `{"first_source":"original"}`, "out", true},
		{"out with a fixed payload", `{"first_source":"payload"}`, "out", false},
		{"out unset reads as fixed", `{}`, "out", false},
		{"expired carries the original", `{"second_source":"original"}`, "expired", true},
		{"expired default is fixed", `{"second_source":"payload"}`, "expired", false},
		{"expired unset reads as fixed", `{}`, "expired", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			set := resolvePorts(t, meta, c.config)
			p, ok := set.Output(c.port)
			require.Truef(t, ok, "port %q must exist whatever the config says", c.port)

			assert.Equal(t, flow.PortAny, p.Type, "a trigger declares no payload of its own; it forwards or replaces")
			if c.keeps {
				assert.NotEmpty(t, p.TypeFrom, "forwarding the original means the upstream type must carry through")
			} else {
				assert.Empty(t, p.TypeFrom,
					"a configured payload is of unknowable type, so propagation must not dress it in the upstream's")
			}
		})
	}
}

// TestSortPortTypes covers the mode-scoped path rule: property mode reads and
// rewrites an array on the message, and sequence mode passes messages through
// untouched, so a stale property path from a mode switch must not color it.
func TestSortPortTypes(t *testing.T) {
	meta := SortNode{}.Meta()

	set := resolvePorts(t, meta, `{"mode":"property","property":"payload"}`)
	in, _ := set.Input("in")
	assert.Equal(t, flow.PortObject, in.Type, "property mode sorts an array, so it needs one on the way in")
	out, _ := set.Output("out")
	assert.Equal(t, flow.PortObject, out.Type, "property mode emits the same array, reordered")

	// The property path is left over from the previous mode. Sequence mode
	// never reads it, so it must not color the ports.
	set = resolvePorts(t, meta, `{"mode":"sequence","property":"payload.readings"}`)
	in, _ = set.Input("in")
	assert.Equal(t, flow.PortAny, in.Type, "sequence mode takes whole messages, so a stale path must not declare an array")
	out, _ = set.Output("out")
	assert.Equal(t, flow.PortAny, out.Type, "sequence mode reorders messages rather than producing a payload of its own")
	assert.Equal(t, "in", out.TypeFrom, "sequence mode passes messages through, so the upstream type must carry")
}

// TestTemplatePortTypes covers the template output across its three settings:
// what is written, as which type, and where it lands.
func TestTemplatePortTypes(t *testing.T) {
	meta := TemplateNode{}.Meta()
	cases := []struct {
		name   string
		config string
		want   string
		keeps  bool
	}{
		{"unconfigured coerces to string", `{}`, flow.PortString, false},
		{"json into the payload", `{"output_format":"json"}`, flow.PortObject, false},
		{"nested payload write", `{"scope":"msg","property":"payload.greeting","output_format":"string"}`, flow.PortObject, false},
		{"metadata write passes through", `{"scope":"msg","property":"meta.note"}`, flow.PortAny, true},
		{"variable scope passes through", `{"scope":"flow","property":"greeting"}`, flow.PortAny, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			set := resolvePorts(t, meta, c.config)
			out, _ := set.Output("out")
			assert.Equal(t, c.want, out.Type, "the output type follows what the template writes and where")

			// Propagation only engages on an undeclared type, so what matters
			// is whether an "any" port still carries TypeFrom.
			propagates := out.Type == flow.PortAny && out.TypeFrom != ""
			assert.Equal(t, c.keeps, propagates,
				"a template writing outside the payload leaves it alone and must carry the upstream type through")
		})
	}
}

// TestXMLEncodeInputType covers the root-lifts-the-requirement rule: without a
// configured root the encoder needs a decoded document, and with one it wraps
// whatever it is given.
func TestXMLEncodeInputType(t *testing.T) {
	meta := XMLEncodeNode{}.Meta()

	set := resolvePorts(t, meta, `{}`)
	in, _ := set.Input("in")
	assert.Equal(t, flow.PortObject, in.Type,
		"with no root element the payload has to supply the document structure itself")

	set = resolvePorts(t, meta, `{"root":"envelope"}`)
	in, _ = set.Input("in")
	assert.Equal(t, flow.PortAny, in.Type,
		"a configured root supplies the structure, so a scalar can encode as that element's text")
}

// TestSubflowBoundaryPortTypes covers the pseudo-nodes advertising their
// declared port_type on their own ports, so the inside of a subflow sees the
// boundary's contract.
func TestSubflowBoundaryPortTypes(t *testing.T) {
	inMeta := SubflowInputNode{}.Meta()

	set := resolvePorts(t, inMeta, `{"port_id":"in","port_type":"string"}`)
	out, _ := set.Output("out")
	assert.Equal(t, flow.PortString, out.Type,
		"a declared boundary type must be what the inside of the subflow is checked against")

	set = resolvePorts(t, inMeta, `{"port_id":"in"}`)
	out, _ = set.Output("out")
	assert.Equal(t, flow.PortAny, out.Type, "an undeclared boundary carries the message, whatever it holds")

	outMeta := SubflowOutputNode{}.Meta()
	set = resolvePorts(t, outMeta, `{"port_id":"out","port_type":"object"}`)
	in, _ := set.Input("in")
	assert.Equal(t, flow.PortObject, in.Type,
		"an output boundary's declared type is the contract the inside must satisfy")
}

// TestManualPortType covers the value node's rule-derived output, which used
// to be a hardcoded special case in port resolution.
func TestManualPortType(t *testing.T) {
	meta := ManualNode{}.Meta()
	cases := map[string]string{
		`{}`:                          flow.PortAny,
		`{"output_format":"json"}`:    flow.PortObject,
		`{"output_format":"string"}`:  flow.PortString,
		`{"output_format":"integer"}`: flow.PortInteger,
		`{"output_format":"boolean"}`: flow.PortBool,
	}
	for config, want := range cases {
		set := resolvePorts(t, meta, config)
		out, _ := set.Output("out")
		assert.Equalf(t, want, out.Type, "the value node's output type comes from its rule, not a special case: %s", config)
	}
}

// TestExplicitFormatCases covers the formats that used to reach the right type
// only by falling through to the rule's default.
func TestExplicitFormatCases(t *testing.T) {
	set := resolvePorts(t, WebhookNode{}.Meta(), `{"body_format":"query"}`)
	out, _ := set.Output("out")
	assert.Equal(t, flow.PortObject, out.Type, "a parsed query string is an object of its parameters")

	set = resolvePorts(t, FileReadNode{}.Meta(), `{"format":"messages"}`)
	out, _ = set.Output("out")
	assert.Equal(t, flow.PortString, out.Type,
		"reading a file as messages fans out one message per line, so each carries a string, not the whole file")
}

// TestConverterOffPayloadPassThrough covers the converter output carrying the
// upstream type when the result lands off the payload, instead of shrugging.
func TestConverterOffPayloadPassThrough(t *testing.T) {
	meta := JSONNode{}.Meta()
	set := resolvePorts(t, meta, `{"property":"meta.body","target":"meta.parsed"}`)
	out, _ := set.Output("out")

	assert.Equal(t, flow.PortAny, out.Type,
		"decoding into metadata leaves the payload alone, so the port must not claim it became an object")
	assert.Equal(t, "in", out.TypeFrom,
		"the payload passes through untouched, so whatever fed this node is what leaves it")
}

// TestEnforceScriptType covers the runtime half of a script's declared
// signature: a value that does not match the declaration fails, and binary
// decodes the base64 text a script hands across into real bytes.
func TestEnforceScriptType(t *testing.T) {
	check := func(portType string, payload any) error {
		msg := &engine.Msg{Payload: payload}
		return enforceScriptType(flow.PortSpec{ID: "out", Type: portType}, msg)
	}

	accepts := []struct {
		portType string
		payload  any
		why      string
	}{
		{flow.PortAny, map[string]any{"a": 1}, "any is the wildcard and declares nothing"},
		{flow.PortString, "hi", ""},
		{flow.PortBool, true, ""},
		{flow.PortNumber, 1.5, ""},
		{flow.PortInteger, 3.0, "a whole number is an integer even though JavaScript hands it across as a float"},
		{flow.PortObject, map[string]any{}, ""},
		{flow.PortObject, []any{1.0}, "an array is a JSON document too, so object covers both"},
		{flow.PortBinary, []byte("raw"), "bytes that are already bytes pass through undecoded"},
	}
	for _, c := range accepts {
		assert.NoErrorf(t, check(c.portType, c.payload), "%s must accept %#v: %s", c.portType, c.payload, c.why)
	}

	rejects := []struct {
		portType string
		payload  any
		why      string
	}{
		{flow.PortString, 5.0, "a number is not text, and coercing it would make the declared type meaningless"},
		{flow.PortNumber, "1.5", "numeric text is still text: a downstream node doing arithmetic would concatenate"},
		{flow.PortInteger, 3.5, "a fraction is not a whole number"},
		{flow.PortObject, "hi", "a scalar has no fields for a downstream read to find"},
		{flow.PortBinary, "not base64!!", "a source names its format, and bytes that do not match it must fail"},
	}
	for _, c := range rejects {
		assert.Errorf(t, check(c.portType, c.payload), "%s must reject %#v: %s", c.portType, c.payload, c.why)
	}

	// Binary is the one type that converts rather than only checking: a script
	// hands bytes across as base64 text, and the port promises real bytes.
	msg := &engine.Msg{Payload: "aGVsbG8="}
	require.NoError(t, enforceScriptType(flow.PortSpec{ID: "out", Type: flow.PortBinary}, msg),
		"a binary port must accept the base64 text a script returns")
	assert.Equal(t, []byte("hello"), msg.Payload,
		"the text must be decoded in place, or the port's declared binary type is a lie downstream")
}
