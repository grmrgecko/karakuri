package flow

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
)

// subflowRegistry extends the base test registry with the subflow node types.
func subflowRegistry() stubRegistry {
	reg := testRegistry()
	reg[NodeTypeSubflow] = NodeMeta{Type: NodeTypeSubflow, DynamicPorts: true}
	reg[NodeTypeSubflowInput] = NodeMeta{
		Type: NodeTypeSubflowInput, Category: CategoryPort,
		Outputs:    []PortSpec{{ID: "out", Type: PortAny}},
		ConfigSpec: []FieldSpec{{ID: "port_id", Type: FieldString, Required: true}},
	}
	reg[NodeTypeSubflowOutput] = NodeMeta{
		Type: NodeTypeSubflowOutput, Category: CategoryPort,
		Inputs:     []PortSpec{{ID: "in", Type: PortAny}},
		ConfigSpec: []FieldSpec{{ID: "port_id", Type: FieldString, Required: true}},
	}
	return reg
}

// portNode builds a pseudo-port node at a Y position.
func portNode(id, nodeType, portID string, y float64) Node {
	return Node{
		ID: id, Type: nodeType, Position: XY{Y: y},
		Config: json.RawMessage(fmt.Sprintf(`{"port_id":%q}`, portID)),
	}
}

// simpleSubgraph is an in -> delay -> out subflow body.
func simpleSubgraph() Graph {
	return Graph{
		Nodes: []Node{
			portNode("pi", NodeTypeSubflowInput, "in", 0),
			node("d", "delay", `{"duration":"1s"}`),
			portNode("po", NodeTypeSubflowOutput, "out", 0),
		},
		Edges: []Edge{
			edge("e1", "pi", "out", "d", "in"),
			edge("e2", "d", "out", "po", "in"),
		},
	}
}

// subflowNode builds a subflow instance node referencing a local subgraph.
func subflowNode(id, subgraphID string) Node {
	return Node{ID: id, Type: NodeTypeSubflow, SubgraphID: subgraphID}
}

// details extracts the per-node problem map from a validation error, which is
// what the editor keys its highlights off.
func details(t *testing.T, err error) map[string]string {
	t.Helper()
	require.Error(t, err, "this graph must be rejected; there is nothing to inspect otherwise")

	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr, "validation problems reach the editor as an AppError")
	return appErr.Details
}

// TestSubflowPortsDerivation covers the ports a placed subflow draws from the
// pseudo-port nodes inside it. Order is vertical position rather than node
// order, so an author arranges the sockets by dragging the port nodes; a port
// node with no id yet is skipped rather than drawing a nameless socket, which
// is what one looks like the moment it is dropped on the canvas.
func TestSubflowPortsDerivation(t *testing.T) {
	g := Graph{
		Nodes: []Node{
			portNode("b", NodeTypeSubflowInput, "second", 10),
			portNode("a", NodeTypeSubflowInput, "first", 0),
			{ID: "l", Type: NodeTypeSubflowInput, Position: XY{Y: 20},
				Config: json.RawMessage(`{"port_id":"third","label":"Third","port_type":"any"}`)},
			portNode("o", NodeTypeSubflowOutput, "result", 0),
			{ID: "skip", Type: NodeTypeSubflowInput}, // No port_id: skipped.
			node("d", "delay", ""),
		},
	}
	ins, outs := SubflowPorts(&g)

	require.Len(t, ins, 3, "the port node with no id must be skipped rather than drawn nameless: %+v", ins)
	assert.Equal(t, []string{"first", "second", "third"}, []string{ins[0].ID, ins[1].ID, ins[2].ID},
		"ports are ordered by vertical position, so dragging a port node reorders the sockets")
	assert.Equal(t, "first", ins[0].Label, "a port with no label falls back to its id")
	assert.Equal(t, "Third", ins[2].Label, "an authored label is what the socket is drawn with")
	assert.Equal(t, PortAny, ins[0].Type, "an undeclared port carries the message")
	assert.Equal(t, PortAny, ins[2].Type, "a port declared any carries the message too")

	require.Len(t, outs, 1, "one output port node means one output socket")
	assert.Equal(t, "result", outs[0].ID, "the output socket is named by its port node")
}

// TestValidateSubflowGraphOK is the baseline the rejection tests below are
// measured against: a flow wired through a subflow whose body is complete must
// validate, or none of the specific rejections mean anything.
func TestValidateSubflowGraphOK(t *testing.T) {
	g := &Graph{
		Nodes: []Node{
			node("t", "manual", ""),
			subflowNode("s1", "sg1"),
			node("dbg", "debug", ""),
		},
		Edges: []Edge{
			edge("e1", "t", "out", "s1", "in"),
			edge("e2", "s1", "out", "dbg", "in"),
		},
		Subgraphs: map[string]Graph{"sg1": simpleSubgraph()},
	}
	require.NoError(t, Validate(g, subflowRegistry()),
		"a flow wired through a complete subflow must validate")
}

// TestValidatePortNodesOnlyInsideSubgraphs covers where a pseudo-port node is
// allowed to be. It defines a boundary, so at the top level of a flow it has no
// boundary to define and would sit there doing nothing; inside a subflow
// definition it is the whole point. The same node, judged by which document it
// is in.
func TestValidatePortNodesOnlyInsideSubgraphs(t *testing.T) {
	g := &Graph{Nodes: []Node{portNode("pi", NodeTypeSubflowInput, "in", 0)}}

	d := details(t, Validate(g, subflowRegistry()))
	assert.Contains(t, d["node:pi"], "only allowed inside subflow graphs",
		"a port node at the top level of a flow defines no boundary")

	assert.NoError(t, ValidateFull(g, subflowRegistry(), nil, true),
		"the same node in a subflow definition is exactly what declares its interface")
}

// TestValidateSubflowInputDefaultType covers the check that a value
// parameter's default is read as a type its declared port type accepts: a
// string parameter whose default parses to an object would emit what the port
// promises to reject.
func TestValidateSubflowInputDefaultType(t *testing.T) {
	g := &Graph{Nodes: []Node{{
		ID: "pi", Type: NodeTypeSubflowInput,
		Config: json.RawMessage(`{"port_id":"limit","port_type":"string","payload":"{}","output_format":"json"}`),
	}}}
	d := details(t, ValidateFull(g, subflowRegistry(), nil, true))
	assert.Contains(t, d["node:pi"], "cannot accept",
		"a JSON default on a string parameter would emit what the port promises to reject")

	// A default the port type accepts is fine, including through widening.
	g.Nodes[0].Config = json.RawMessage(`{"port_id":"limit","port_type":"number","payload":"1","output_format":"integer"}`)
	assert.NoError(t, ValidateFull(g, subflowRegistry(), nil, true),
		"an integer default satisfies a number parameter: integer widens to number")

	// Draft saves stay lenient: an author mid-edit is not held to the pairing.
	g.Nodes[0].Config = json.RawMessage(`{"port_id":"limit","port_type":"string","payload":"{}","output_format":"json"}`)
	assert.NoError(t, ValidateDraft(g, subflowRegistry(), nil, true),
		"a draft must store the mismatch, since the format is chosen before the default is retyped")
}

// TestValidateDuplicatePortIDs covers two port nodes claiming one name. The
// port id is what an edge on the placed subflow is stored against, so two of
// them makes every edge to that socket ambiguous.
func TestValidateDuplicatePortIDs(t *testing.T) {
	sub := Graph{
		Nodes: []Node{
			portNode("p1", NodeTypeSubflowInput, "in", 0),
			portNode("p2", NodeTypeSubflowInput, "in", 10),
		},
	}
	g := &Graph{
		Nodes:     []Node{subflowNode("s1", "sg1")},
		Subgraphs: map[string]Graph{"sg1": sub},
	}
	d := details(t, Validate(g, subflowRegistry()))
	assert.Contains(t, d["subgraph:sg1/node:p2"], "duplicate subflow port ID",
		"the problem must be reported against the second claimant, inside the subgraph that holds it")
}

// TestValidateSubflowRefRules covers what a placed subflow must point at. A
// subflow is either local to this flow or shared from the library, never both
// and never neither: the two references resolve through different stores, so a
// node carrying both has no defined body.
func TestValidateSubflowRefRules(t *testing.T) {
	id := uuid.New()
	g := &Graph{
		Nodes: []Node{
			{ID: "none", Type: NodeTypeSubflow},
			{ID: "both", Type: NodeTypeSubflow, SubflowID: &id, SubgraphID: "sg1"},
			{ID: "missing", Type: NodeTypeSubflow, SubgraphID: "nope"},
		},
		Subgraphs: map[string]Graph{"sg1": simpleSubgraph()},
	}
	d := details(t, Validate(g, subflowRegistry()))
	assert.Contains(t, d["node:none"], "references neither", "a subflow node with no body has nothing to run")
	assert.Contains(t, d["node:both"], "not both", "the two references resolve through different stores, so both is undefined")
	assert.Contains(t, d["node:missing"], "does not exist", "a local reference to a subgraph that was deleted must be caught at deploy")
}

// TestValidateSubflowEdgeHandlesMatchDerivedPorts covers edges against a
// subflow's derived sockets. Renaming a port node inside a subflow leaves every
// edge outside it pointing at a handle that no longer exists, and that has to
// surface as a problem on the edge rather than as a message routed nowhere.
func TestValidateSubflowEdgeHandlesMatchDerivedPorts(t *testing.T) {
	g := &Graph{
		Nodes: []Node{
			node("t", "manual", ""),
			subflowNode("s1", "sg1"),
		},
		Edges: []Edge{
			edge("e1", "t", "out", "s1", "bogus"),
		},
		Subgraphs: map[string]Graph{"sg1": simpleSubgraph()},
	}
	d := details(t, Validate(g, subflowRegistry()))
	assert.Contains(t, d["edge:e1"], `no input port "bogus"`,
		"an edge to a socket the subflow does not declare must be reported against the edge")
}

// TestValidateSubgraphContentsRecursively covers validation reaching inside a
// subflow body. A subflow is not a black box at deploy: a node inside it that
// is missing a required field fails the run just as surely, so the problem is
// reported with the path that locates it in the drilled view.
func TestValidateSubgraphContentsRecursively(t *testing.T) {
	sub := simpleSubgraph()
	sub.Nodes[1].Config = nil // Delay loses its required duration.
	g := &Graph{
		Nodes:     []Node{subflowNode("s1", "sg1")},
		Subgraphs: map[string]Graph{"sg1": sub},
	}
	d := details(t, Validate(g, subflowRegistry()))
	assert.Contains(t, d["subgraph:sg1/node:d"], `required config field "duration"`,
		"a problem inside a subflow must be reported with the path that locates the node in the drilled view")
}

// TestValidateLocalSubflowCycle covers a local subflow that eventually
// includes itself. Flattening a subflow inlines its body, so a cycle is not a
// slow flow but an unbounded expansion; it has to be caught at validation
// rather than at the depth cap during a run.
func TestValidateLocalSubflowCycle(t *testing.T) {
	// sg1 includes sg2 which includes sg1 again.
	sg1 := Graph{Nodes: []Node{subflowNode("inner", "sg2")}}
	sg2 := Graph{Nodes: []Node{subflowNode("deeper", "sg1")}}
	g := &Graph{
		Nodes:     []Node{subflowNode("s1", "sg1")},
		Subgraphs: map[string]Graph{"sg1": sg1, "sg2": sg2},
	}
	d := details(t, Validate(g, subflowRegistry()))
	assert.Contains(t, strings.Join(valuesOf(d), "\n"), "circular subflow reference",
		"sg1 includes sg2 which includes sg1, which would expand without bound: %v", d)
}

// stubResolver stands in for the shared-subflow library, which is a database
// table in production. It is a plain map with the same interface the validator
// calls, so the cycle and missing-reference paths can be driven without one.
type stubResolver struct {
	graphs map[uuid.UUID]*Graph
	self   uuid.UUID
}

func (r stubResolver) ResolveShared(id uuid.UUID) (*Graph, error) {
	g, ok := r.graphs[id]
	if !ok {
		return nil, errors.New("not deployed")
	}
	return g, nil
}

func (r stubResolver) SelfFlowID() uuid.UUID { return r.self }

// TestValidateSharedSubflowSelfReference covers a shared subflow placed inside
// itself, which is the shortest cycle there is and the easiest to create by
// accident: promoting a subflow and then dropping it into its own definition.
func TestValidateSharedSubflowSelfReference(t *testing.T) {
	self := uuid.New()
	shared := &Graph{Nodes: []Node{
		portNode("pi", NodeTypeSubflowInput, "in", 0),
	}}
	g := &Graph{
		Nodes: []Node{{ID: "s1", Type: NodeTypeSubflow, SubflowID: &self}},
	}
	resolver := stubResolver{graphs: map[uuid.UUID]*Graph{self: shared}, self: self}
	d := details(t, ValidateFull(g, subflowRegistry(), resolver, false))
	assert.Contains(t, d["node:s1"], "circular subflow reference",
		"a shared subflow placed inside its own definition would expand without bound")
}

// TestValidateSharedSubflowMutualCycle covers the cycle that spans two
// documents: this flow places another shared subflow, which places this one
// back. Neither document is wrong on its own, so the check has to follow the
// reference through the resolver to see it.
func TestValidateSharedSubflowMutualCycle(t *testing.T) {
	self := uuid.New()
	other := uuid.New()
	// The other shared subflow references this flow back.
	otherGraph := &Graph{Nodes: []Node{{ID: "back", Type: NodeTypeSubflow, SubflowID: &self}}}
	g := &Graph{Nodes: []Node{{ID: "s1", Type: NodeTypeSubflow, SubflowID: &other}}}
	resolver := stubResolver{graphs: map[uuid.UUID]*Graph{other: otherGraph}, self: self}
	d := details(t, ValidateFull(g, subflowRegistry(), resolver, false))
	assert.Contains(t, strings.Join(valuesOf(d), "\n"), "circular subflow reference",
		"neither document is wrong alone, so the cycle is only visible through the resolver: %v", d)
}

// TestValidateUnresolvableSharedSubflow covers a shared subflow that is
// referenced but not deployed, and the deliberate hole beside it: with no
// resolver the reference passes. Validation runs in places that have no
// library to consult — an import, an export, a draft save — and refusing there
// would reject a document that is perfectly fine.
func TestValidateUnresolvableSharedSubflow(t *testing.T) {
	missing := uuid.New()
	g := &Graph{Nodes: []Node{{ID: "s1", Type: NodeTypeSubflow, SubflowID: &missing}}}
	resolver := stubResolver{graphs: map[uuid.UUID]*Graph{}}
	d := details(t, ValidateFull(g, subflowRegistry(), resolver, false))
	assert.Contains(t, d["node:s1"], "not deployed",
		"with a library to consult, a reference it cannot satisfy must be reported")

	assert.NoError(t, ValidateFull(g, subflowRegistry(), nil, false),
		"with no library to consult the reference is unjudgeable, so validation must not invent a problem")
}
