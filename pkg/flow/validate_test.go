package flow

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
)

// stubRegistry is a map-backed MetaRegistry for validation tests.
type stubRegistry map[string]NodeMeta

func (r stubRegistry) MetaFor(nodeType string) (NodeMeta, bool) {
	m, ok := r[nodeType]
	return m, ok
}

// testRegistry returns node metadata covering all validation paths.
func testRegistry() stubRegistry {
	return stubRegistry{
		"manual": {
			Type: "manual", Category: CategoryTrigger,
			// The rule mirrors the real node's: the output takes the type of
			// the configured payload, resolved like any config-typed port.
			Outputs: []PortSpec{{ID: "out", Type: PortAny, TypeRule: payloadFormatRule()}},
		},
		"delay": {
			Type:    "delay",
			Inputs:  []PortSpec{{ID: "in", Type: PortAny}},
			Outputs: []PortSpec{{ID: "out", Type: PortAny}},
			ConfigSpec: []FieldSpec{
				{ID: "duration", Type: FieldDuration, Required: true},
			},
		},
		"debug": {
			Type:   "debug",
			Inputs: []PortSpec{{ID: "in", Type: PortAny}},
			ConfigSpec: []FieldSpec{
				{ID: "note", Type: FieldText},
			},
		},
		"note": {
			Type: "note", Category: CategoryAnnotation,
			ConfigSpec: []FieldSpec{
				{ID: "text", Type: FieldText, Required: true},
				{ID: "color", Type: FieldSelect, Options: []Option{{Value: "yellow"}, {Value: "blue"}}},
			},
		},
		"compare": {
			Type:    "compare",
			Inputs:  []PortSpec{{ID: "in", Type: PortAny}},
			Outputs: []PortSpec{{ID: "result", Type: PortBool}},
			ConfigSpec: []FieldSpec{
				{ID: "threshold", Type: FieldNumber, Required: true},
				{ID: "invert", Type: FieldBoolean},
				{ID: "mode", Type: FieldSelect, Options: []Option{{Value: "gt"}, {Value: "lt"}}},
				{ID: "custom", Type: FieldString, Required: true, ShowIf: &ShowIf{Field: "mode", Equals: "custom"}},
				// Equals as []any is the shape a list arrives in from JSON
				// metadata; matching any element is the contract ConfigForm
				// mirrors client-side.
				{ID: "bounds", Type: FieldString, Required: true, ShowIf: &ShowIf{Field: "mode", Equals: []any{"lt", "between"}}},
			},
		},
		"gate": {
			Type:   "gate",
			Inputs: []PortSpec{{ID: "cond", Type: PortBool}, {ID: "any_in", Type: PortAny}},
		},
		"konst": {
			Type: "konst", Pure: true,
			Outputs: []PortSpec{{ID: "out", Type: PortNumber}},
		},
		// The port-field trio mirrors the AI agent shape: a required single
		// capability port plus an unbounded multi port, fed by pure sources.
		"agent": {
			Type:    "agent",
			Inputs:  []PortSpec{{ID: "in", Type: PortAny}},
			Outputs: []PortSpec{{ID: "out", Type: PortString}},
			ConfigSpec: []FieldSpec{
				{ID: "model", Label: "Model", Type: FieldJSON, Port: "model-handle", Required: true},
				{ID: "tools", Label: "Tools", Type: FieldJSON, Port: "toolset", Multi: true},
			},
		},
		"model": {
			Type: "model", Pure: true,
			Outputs: []PortSpec{{ID: "out", Type: "model-handle"}},
		},
		"toolbox": {
			Type: "toolbox", Pure: true,
			Outputs: []PortSpec{{ID: "out", Type: "toolset"}},
		},
	}
}

// node builds a test node with optional config JSON.
func node(id, nodeType, config string) Node {
	n := Node{ID: id, Type: nodeType}
	if config != "" {
		n.Config = json.RawMessage(config)
	}
	return n
}

// edge builds a test edge.
func edge(id, source, sourceHandle, target, targetHandle string) Edge {
	return Edge{ID: id, Source: source, SourceHandle: sourceHandle, Target: target, TargetHandle: targetHandle}
}

// TestValidate covers the problems a graph is rejected for and, just as much,
// the graphs that must keep validating. Validation runs on every deploy, so a
// rule that grew too strict stops an author deploying a flow that was fine
// yesterday; the valid rows are there to catch that, not as filler.
//
// Problems are matched as substrings of a detail value because the details map
// is keyed by node id, which is what the editor highlights, while the message
// is what it prints.
func TestValidate(t *testing.T) {
	tests := []struct {
		name  string
		graph Graph
		// wantProblems are substrings that must each appear in a detail value.
		// Empty means the graph must be valid.
		wantProblems []string
	}{
		{
			name:  "empty graph is valid",
			graph: Graph{},
		},
		{
			name: "valid manual delay debug chain",
			graph: Graph{
				Nodes: []Node{
					node("n1", "manual", ""),
					node("n2", "delay", `{"duration":"5s"}`),
					node("n3", "debug", `{"note":"hi"}`),
				},
				Edges: []Edge{
					edge("e1", "n1", "out", "n2", "in"),
					edge("e2", "n2", "out", "n3", "in"),
				},
			},
		},
		{
			name: "unknown node type",
			graph: Graph{
				Nodes: []Node{node("n1", "nope", "")},
			},
			wantProblems: []string{`unknown node type "nope"`},
		},
		{
			name: "duplicate node IDs",
			graph: Graph{
				Nodes: []Node{node("n1", "manual", ""), node("n1", "debug", "")},
			},
			wantProblems: []string{`duplicate node ID "n1"`},
		},
		{
			name: "edge references missing nodes",
			graph: Graph{
				Nodes: []Node{node("n1", "manual", "")},
				Edges: []Edge{edge("e1", "ghost", "out", "phantom", "in")},
			},
			wantProblems: []string{
				`source node "ghost" does not exist`,
				`target node "phantom" does not exist`,
			},
		},
		{
			name: "edge references missing ports",
			graph: Graph{
				Nodes: []Node{node("n1", "manual", ""), node("n2", "debug", "")},
				Edges: []Edge{edge("e1", "n1", "bogus", "n2", "missing")},
			},
			wantProblems: []string{
				`source node "n1" has no output port "bogus"`,
				`target node "n2" has no input port "missing"`,
			},
		},
		{
			// Both ends declare a type and they disagree, which is the case a
			// typed input exists to catch.
			name: "port type incompatibility",
			graph: Graph{
				Nodes: []Node{
					node("n1", "manual", `{"output_format":"string"}`),
					node("n2", "gate", ""),
				},
				Edges: []Edge{edge("e1", "n1", "out", "n2", "cond")},
			},
			wantProblems: []string{`port type "string" is not compatible with "bool"`},
		},
		{
			// An undeclared payload is not knowable until the run, so a message
			// edge carrying one into a typed input is allowed.
			name: "undeclared output feeds a typed input",
			graph: Graph{
				Nodes: []Node{node("n1", "manual", ""), node("n2", "gate", "")},
				Edges: []Edge{edge("e1", "n1", "out", "n2", "cond")},
			},
		},
		{
			name: "any port accepts any type",
			graph: Graph{
				Nodes: []Node{node("n1", "manual", ""), node("n2", "gate", "")},
				Edges: []Edge{edge("e1", "n1", "out", "n2", "any_in")},
			},
		},
		{
			name: "bool output to bool input is valid",
			graph: Graph{
				Nodes: []Node{
					node("n1", "compare", `{"threshold":5}`),
					node("n2", "gate", ""),
				},
				Edges: []Edge{edge("e1", "n1", "result", "n2", "cond")},
			},
		},
		{
			name: "multiple edges into one input port",
			graph: Graph{
				Nodes: []Node{
					node("n1", "manual", ""),
					node("n2", "delay", `{"duration":"1s"}`),
					node("n3", "debug", ""),
				},
				Edges: []Edge{
					edge("e1", "n1", "out", "n3", "in"),
					edge("e2", "n2", "out", "n3", "in"),
				},
			},
			wantProblems: []string{`input port "in" on node "n3" already has an edge`},
		},
		{
			name: "note node may not have edges",
			graph: Graph{
				Nodes: []Node{
					node("n1", "manual", ""),
					node("n2", "note", `{"text":"hello"}`),
				},
				Edges: []Edge{edge("e1", "n1", "out", "n2", "in")},
			},
			wantProblems: []string{`annotation node "n2" may not have edges`},
		},
		{
			name: "missing required config field",
			graph: Graph{
				Nodes: []Node{node("n1", "delay", "")},
			},
			wantProblems: []string{`required config field "duration" is missing`},
		},
		{
			name: "empty required string config field",
			graph: Graph{
				Nodes: []Node{node("n1", "delay", `{"duration":""}`)},
			},
			wantProblems: []string{`required config field "duration" is empty`},
		},
		{
			name: "select value not in options",
			graph: Graph{
				Nodes: []Node{node("n1", "note", `{"text":"hi","color":"purple"}`)},
			},
			wantProblems: []string{`config field "color" value "purple" is not a valid option`},
		},
		{
			name: "number field must be a number",
			graph: Graph{
				Nodes: []Node{node("n1", "compare", `{"threshold":"high"}`)},
			},
			wantProblems: []string{`config field "threshold" must be a number`},
		},
		{
			name: "boolean field must be a boolean",
			graph: Graph{
				Nodes: []Node{node("n1", "compare", `{"threshold":1,"invert":"yes"}`)},
			},
			wantProblems: []string{`config field "invert" must be a boolean`},
		},
		{
			name: "config is not a JSON object",
			graph: Graph{
				Nodes: []Node{node("n1", "delay", `[1,2,3]`)},
			},
			wantProblems: []string{"config is not a valid JSON object"},
		},
		{
			name: "required field hidden by show_if is skipped",
			graph: Graph{
				Nodes: []Node{node("n1", "compare", `{"threshold":1,"mode":"gt"}`)},
			},
		},
		{
			name: "required field revealed by show_if is enforced",
			graph: Graph{
				Nodes: []Node{node("n1", "compare", `{"threshold":1,"mode":"custom"}`)},
			},
			wantProblems: []string{`required config field "custom" is missing`},
		},
		{
			name: "required field revealed by a show_if value list is enforced",
			graph: Graph{
				Nodes: []Node{node("n1", "compare", `{"threshold":1,"mode":"lt"}`)},
			},
			wantProblems: []string{`required config field "bounds" is missing`},
		},
		{
			// The editor disables a wired field, so a parameter port nothing
			// feeds would leave the field with no value at all.
			name: "exposed parameter without a wire",
			graph: Graph{
				Nodes: []Node{
					{ID: "n1", Type: "compare", Config: json.RawMessage(`{"threshold":1}`),
						Params: []string{"threshold"}},
				},
			},
			wantProblems: []string{`config field "threshold" is exposed as an input but nothing is wired to it`},
		},
		{
			// A wired field is supplied by its value edge; the dead literal is
			// not required to also be filled in.
			name: "wired parameter satisfies a required field",
			graph: Graph{
				Nodes: []Node{
					node("k", "konst", ""),
					{ID: "n1", Type: "compare", Params: []string{"threshold"}},
				},
				Edges: []Edge{
					{ID: "e1", Source: "k", SourceHandle: "out", Target: "n1",
						TargetHandle: ParamPortID("threshold"), Kind: EdgeValue},
				},
			},
		},
		{
			// A multi port takes as many wires as the author attaches; each
			// value lands in the field's array. This is the tool-fan-in shape
			// the single-source rule must not reject.
			name: "multi port field accepts several value edges",
			graph: Graph{
				Nodes: []Node{
					node("m", "model", ""),
					node("t1", "toolbox", ""),
					node("t2", "toolbox", ""),
					node("a", "agent", ""),
				},
				Edges: []Edge{
					{ID: "e1", Source: "m", SourceHandle: "out", Target: "a",
						TargetHandle: ParamPortID("model"), Kind: EdgeValue},
					{ID: "e2", Source: "t1", SourceHandle: "out", Target: "a",
						TargetHandle: ParamPortID("tools"), Kind: EdgeValue},
					{ID: "e3", Source: "t2", SourceHandle: "out", Target: "a",
						TargetHandle: ParamPortID("tools"), Kind: EdgeValue},
				},
			},
		},
		{
			// A single-source port field keeps the one-edge rule even though
			// its multi sibling is exempt.
			name: "single port field still rejects a second edge",
			graph: Graph{
				Nodes: []Node{
					node("m1", "model", ""),
					node("m2", "model", ""),
					node("a", "agent", ""),
				},
				Edges: []Edge{
					{ID: "e1", Source: "m1", SourceHandle: "out", Target: "a",
						TargetHandle: ParamPortID("model"), Kind: EdgeValue},
					{ID: "e2", Source: "m2", SourceHandle: "out", Target: "a",
						TargetHandle: ParamPortID("model"), Kind: EdgeValue},
				},
			},
			wantProblems: []string{`already has an edge`},
		},
		{
			// A required port field has no typed-in fallback, so leaving it
			// unwired is the deploy-time hole this check exists for; the
			// optional multi port beside it may stay empty.
			name: "required port field must be wired",
			graph: Graph{
				Nodes: []Node{node("a", "agent", "")},
			},
			wantProblems: []string{`input port "model" needs a value edge`},
		},
		{
			name: "optional multi port may stay unwired",
			graph: Graph{
				Nodes: []Node{
					node("m", "model", ""),
					node("a", "agent", ""),
				},
				Edges: []Edge{
					{ID: "e1", Source: "m", SourceHandle: "out", Target: "a",
						TargetHandle: ParamPortID("model"), Kind: EdgeValue},
				},
			},
		},
		{
			name: "port field type mismatch is rejected",
			graph: Graph{
				Nodes: []Node{
					node("k", "konst", ""),
					node("m", "model", ""),
					node("a", "agent", ""),
				},
				Edges: []Edge{
					{ID: "e1", Source: "m", SourceHandle: "out", Target: "a",
						TargetHandle: ParamPortID("model"), Kind: EdgeValue},
					{ID: "e2", Source: "k", SourceHandle: "out", Target: "a",
						TargetHandle: ParamPortID("tools"), Kind: EdgeValue},
				},
			},
			wantProblems: []string{`port type "number" is not compatible with "toolset"`},
		},
		{
			// A port field is a permanent socket rather than a toggled one, so
			// listing it in Params is a declaration that cannot work.
			name: "port field cannot be toggled through params",
			graph: Graph{
				Nodes: []Node{
					node("m", "model", ""),
					{ID: "a", Type: "agent", Params: []string{"tools"}},
				},
				Edges: []Edge{
					{ID: "e1", Source: "m", SourceHandle: "out", Target: "a",
						TargetHandle: ParamPortID("model"), Kind: EdgeValue},
				},
			},
			wantProblems: []string{`config field "tools" is a permanent input port`},
		},
		{
			name: "multiple problems reported together",
			graph: Graph{
				Nodes: []Node{
					node("n1", "nope", ""),
					node("n2", "delay", ""),
				},
				Edges: []Edge{edge("e1", "ghost", "out", "n2", "in")},
			},
			wantProblems: []string{
				`unknown node type "nope"`,
				`required config field "duration" is missing`,
				`source node "ghost" does not exist`,
			},
		},
	}

	reg := testRegistry()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(&tt.graph, reg)

			if len(tt.wantProblems) == 0 {
				require.NoError(t, err, "this graph must keep validating, or a deploy that worked yesterday stops working")
				return
			}

			var appErr *apperrors.AppError
			require.ErrorAs(t, err, &appErr, "validation problems reach the editor as an AppError")
			require.Equal(t, "VALIDATION_ERROR", appErr.Code, "the editor renders problems only for this code")

			// Every expected problem must appear in some detail value; the keys
			// are node ids, so the message is what is searched.
			joined := strings.Join(valuesOf(appErr.Details), "\n")
			for _, want := range tt.wantProblems {
				assert.Containsf(t, joined, want, "no reported problem mentions %q; got %v", want, appErr.Details)
			}
		})
	}
}

// TestDraftKeepsUnknownNodeTypes pins the lenient half of the validation
// split for missing node types: a flow holding a node whose plugin is
// disabled must still save as a draft, or disabling the plugin would lock the
// flow out of editing, while deploy keeps rejecting the same graph. The edge
// into the unknown node must not surface its own problem either, since its
// endpoint cannot resolve ports.
func TestDraftKeepsUnknownNodeTypes(t *testing.T) {
	g := Graph{
		Nodes: []Node{
			node("n1", "nope", ""),
			node("n2", "debug", ""),
		},
		Edges: []Edge{edge("e1", "n1", "out", "n2", "in")},
	}
	reg := testRegistry()
	assert.NoError(t, ValidateDraft(&g, reg, nil, false),
		"a draft must store while the node's plugin is away")
	assert.Error(t, ValidateFull(&g, reg, nil, false),
		"deploy must still reject a node nothing can run")
}

func TestRetryPolicyValidation(t *testing.T) {
	reg := stubRegistry{"request": {Type: "request", Retryable: true}, "value": {Type: "value"}}
	for _, tc := range []struct {
		name, typ string
		policy    RetryPolicy
		valid     bool
	}{
		{"valid", "request", RetryPolicy{MaxAttempts: 3, DelayMs: 10, Backoff: 2, Jitter: 0.2}, true},
		{"zero attempts", "request", RetryPolicy{}, false},
		{"infinite backoff", "request", RetryPolicy{MaxAttempts: 3, Backoff: math.Inf(1)}, false},
		{"nonfinite jitter", "request", RetryPolicy{MaxAttempts: 3, Jitter: math.NaN()}, false},
		{"negative delay", "request", RetryPolicy{MaxAttempts: 1, DelayMs: -1}, false},
		{"shrinking backoff", "request", RetryPolicy{MaxAttempts: 3, Backoff: 0.5}, false},
		{"jitter range", "request", RetryPolicy{MaxAttempts: 3, Jitter: 1.1}, false},
		{"non retryable", "value", RetryPolicy{MaxAttempts: 1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(&Graph{Nodes: []Node{{ID: "n", Type: tc.typ, Retry: &tc.policy}}}, reg)
			if tc.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}
