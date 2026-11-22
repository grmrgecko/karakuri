package flow

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
)

// toolTestRegistry extends the shared test registry with the tool node's
// static metadata, mirroring what ainodes declares.
func toolTestRegistry() stubRegistry {
	reg := testRegistry()
	reg[NodeTypeTool] = NodeMeta{
		Type: NodeTypeTool, Pure: true, DynamicPorts: true,
		Inputs:  []PortSpec{{ID: ToolReturnPort, Type: PortAny, Optional: true}},
		Outputs: []PortSpec{{ID: "out", Type: "ai-tool"}, {ID: ToolCallPort, Type: PortObject}},
		ConfigSpec: []FieldSpec{
			{ID: "name", Type: FieldString, Required: true},
			{ID: "description", Type: FieldText, Required: true},
			{ID: "tool_args", Type: FieldToolArgs},
			{ID: "timeout", Type: FieldNumber},
		},
	}
	return reg
}

// TestToolArgPorts covers the config-to-ports derivation the editor mirrors:
// each declared argument becomes one param-kind output under the arg: prefix,
// unnamed and duplicate arguments are dropped, and type and label default the
// way the docs promise (string, the ID).
func TestToolArgPorts(t *testing.T) {
	cfg := json.RawMessage(`{"name":"lookup","tool_args":[
		{"id":"city"},
		{"id":"days","label":"Days ahead","type":"integer"},
		{"id":""},
		{"id":"city","type":"number"}
	]}`)
	ports := ToolArgPorts(cfg)
	require.Len(t, ports, 2, "unnamed arguments and duplicates contribute no port")

	assert.Equal(t, "arg:city", ports[0].ID, "argument ports are namespaced so they never collide with call or out")
	assert.Equal(t, PortString, ports[0].Type, "an argument with no type defaults to string")
	assert.Equal(t, "city", ports[0].Label, "the label defaults to the ID")
	assert.Equal(t, PortKindParam, ports[0].Kind, "argument ports carry the param kind, marking them value-only sources")
	assert.Equal(t, "integer", ports[1].Type, "a declared type is kept as declared")
	assert.Equal(t, "Days ahead", ports[1].Label)

	argID, ok := ToolArgID(ports[0].ID)
	require.True(t, ok, "an arg port ID must round-trip")
	assert.Equal(t, "city", argID)
	_, ok = ToolArgID(ToolCallPort)
	assert.False(t, ok, "the call port is not an argument port")
}

// TestToolWiringValidation pins the deploy gate for tool nodes against the
// lenient draft path: strict validation requires the call and return wires
// and a unique name, and rejects the edge shapes that could never work — a
// message edge leaving an argument port, and a value edge leaving call. The
// same wiring holes must still save as a draft, since they are exactly the
// states an author passes through while building the loop.
func TestToolWiringValidation(t *testing.T) {
	toolCfg := `{"name":"lookup","description":"d","tool_args":[{"id":"city"}]}`
	reg := toolTestRegistry()

	// Wiring holes: two tools sharing a name, neither loop wired. These are
	// exactly the states an author passes through mid-edit, so the draft path
	// must keep saving them while deploy rejects each one.
	holes := Graph{
		Nodes: []Node{
			node("t", "manual", ""),
			node("tool1", NodeTypeTool, toolCfg),
			node("tool2", NodeTypeTool, toolCfg),
		},
	}
	require.NoError(t, ValidateDraft(&holes, reg, nil, false),
		"every wiring hole here is a state an author passes through mid-edit")
	err := ValidateFull(&holes, reg, nil, false)
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr, "deploy must reject the graph")
	joined := strings.Join(valuesOf(appErr.Details), "\n")
	for _, want := range []string{
		`tool name "lookup" is already used`,
		"the call output is not wired",
		"nothing is wired into the return input",
	} {
		assert.Containsf(t, joined, want, "no reported problem mentions %q; got %v", want, appErr.Details)
	}

	// Wrong-kind edges are structural, not mid-edit states, so they are
	// rejected on drafts too — the same footing as a value edge targeting a
	// message port.
	badEdges := Graph{
		Nodes: []Node{
			node("t", "manual", ""),
			node("tool1", NodeTypeTool, toolCfg),
			node("d1", "debug", ""),
			{ID: "d2", Type: "debug", Params: []string{"note"}},
		},
		Edges: []Edge{
			// A message edge leaving an argument port: values do not travel
			// the message path.
			edge("e1", "tool1", ToolArgPortID("city"), "d1", "in"),
			// A value edge leaving the call output: the call message is not a
			// value.
			{ID: "e2", Source: "tool1", SourceHandle: ToolCallPort,
				Target: "d2", TargetHandle: ParamPortID("note"), Kind: EdgeValue},
		},
	}
	err = ValidateDraft(&badEdges, reg, nil, false)
	require.ErrorAs(t, err, &appErr, "wrong-kind edges must be rejected even on drafts")
	joined = strings.Join(valuesOf(appErr.Details), "\n")
	assert.Contains(t, joined, "needs a value edge, not a message edge")
	assert.Contains(t, joined, "cannot supply a value")

	// The complete loop is the positive boundary: call wired to a branch, the
	// branch wired back into return, one tool, one name.
	valid := Graph{
		Nodes: []Node{
			node("t", "manual", ""),
			node("tool", NodeTypeTool, toolCfg),
			node("d", "debug", ""),
			node("dly", "delay", `{"duration":"1s"}`),
		},
		Edges: []Edge{
			edge("e1", "t", "out", "d", "in"),
			edge("e2", "tool", ToolCallPort, "dly", "in"),
			edge("e3", "dly", "out", "tool", ToolReturnPort),
		},
	}
	assert.NoError(t, ValidateFull(&valid, reg, nil, false),
		"a wired call/return loop is a legal graph even though it is a cycle")
}
