package engine_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodes/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFaceplateFeedsParameters pins how a faceplate reaches the flow: each
// control is a value output, a wired parameter pulls the applied value, and a
// control with nothing applied yet supplies its default. A faceplate is
// otherwise inert; it has no message ports and never runs on its own.
func TestFaceplateFeedsParameters(t *testing.T) {
	got := make(chan map[string]any, 1)
	consumer := &stubNode{
		typ: "consumer", inputs: []string{"in"}, outputs: outPorts,
		configSpec: []engine.FieldSpec{
			{ID: "max", Label: "Max", Type: flow.FieldNumber},
			{ID: "mode", Label: "Mode", Type: flow.FieldString},
		},
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			var cfg map[string]any
			require.NoError(t, ec.Config(&cfg), "the consumer's config must decode")
			got <- cfg
			return map[string]*engine.Msg{"out": {Payload: "done"}}, nil
		},
	}
	reg := engine.NewRegistry()
	reg.Register(triggerStub())
	reg.Register(consumer)
	reg.Register(core.FaceplateNode{})
	e := engine.New(engine.Options{Registry: reg, Clock: engine.NewFakeClock(time.Now()), Publisher: &fakePub{}})
	e.Start()
	t.Cleanup(e.Stop)

	faceplate, err := json.Marshal(flow.FaceplateConfig{
		Name: "Simple",
		Controls: []flow.FaceplateControl{
			{ID: "max", Label: "Max", Type: flow.ControlNumber, Default: 80.0},
			{ID: "mode", Label: "Mode", Type: flow.ControlString, Default: "day"},
		},
		Values: map[string]any{"max": 65.0},
	})
	require.NoError(t, err, "the faceplate config must encode")
	g := graph(
		[]flow.Node{
			gnode("t", "trigger"),
			{ID: "fp", Type: flow.NodeTypeFaceplate, Config: faceplate},
			{ID: "c", Type: "consumer", Config: json.RawMessage(`{"max":1,"mode":"x"}`), Params: []string{"max", "mode"}},
		},
		[]flow.Edge{gedge("t", "out", "c", "in"), valueEdge("fp", "max", "c", "max"), valueEdge("fp", "mode", "c", "mode")},
	)

	snap := waitDone(t, startRun(t, e, g))
	require.Equalf(t, "succeeded", snap.Status, "the run must complete: %s", snap.Error)
	cfg := <-got
	assert.Equal(t, 65.0, cfg["max"], "an applied value must reach the wired parameter")
	assert.Equal(t, "day", cfg["mode"], "a control with nothing applied must supply its default")
}

// TestSubflowFaceplateValuesPerCaller pins that each node calling a shared
// subflow keeps its own faceplate values: they are written over the
// subflow's own values for that caller only, and the shared graph, which the
// resolver may cache, is left untouched. Two TVs on one subflow must not
// share a tuning.
func TestSubflowFaceplateValuesPerCaller(t *testing.T) {
	reg := engine.NewRegistry()
	core.RegisterAll(reg)
	inner, err := json.Marshal(flow.FaceplateConfig{Name: "TV", Controls: []flow.FaceplateControl{
		{ID: "max", Label: "Max", Type: flow.ControlNumber},
	}, Values: map[string]any{"max": 10.0}})
	require.NoError(t, err, "the inner faceplate must encode")
	shared := &flow.Graph{Nodes: []flow.Node{{ID: "fp", Type: flow.NodeTypeFaceplate, Config: inner}}}
	sharedID := uuid.New()
	resolver := func(id uuid.UUID) (*flow.Graph, error) { return shared, nil }

	g := &flow.Graph{Nodes: []flow.Node{
		{ID: "living", Type: flow.NodeTypeSubflow, SubflowID: &sharedID, Config: json.RawMessage(`{"faceplate_values":{"fp":{"max":55}}}`)},
		{ID: "bedroom", Type: flow.NodeTypeSubflow, SubflowID: &sharedID},
	}}
	flat, err := engine.FlattenGraph(g, resolver, reg)
	require.NoError(t, err, "the graph must flatten")

	values := map[string]float64{}
	for _, n := range flat.Nodes {
		cfg, err := flow.ParseFaceplate(n.Config)
		require.NoError(t, err, "the flattened faceplate must decode")
		values[n.ID] = cfg.Values["max"].(float64)
	}
	assert.Equal(t, 55.0, values["living/fp"], "the living room caller's own value applies to its instance")
	assert.Equal(t, 10.0, values["bedroom/fp"], "a caller without values keeps the subflow's own")
	cfg, _ := flow.ParseFaceplate(shared.Nodes[0].Config)
	assert.Equal(t, 10.0, cfg.Values["max"], "the shared graph must not be changed by one caller's values")
}

// TestSubflowFaceplateOverrideMustFitControl pins spec 042 FR-015: a caller's
// stored faceplate value applies only while it fits the shared subflow's
// deployed control, and the subflow's own value stands otherwise. The values
// were merged unchecked, so after the subflow retyped a control the caller's
// old text went out on a number port.
func TestSubflowFaceplateOverrideMustFitControl(t *testing.T) {
	reg := engine.NewRegistry()
	core.RegisterAll(reg)
	limit := 100.0
	inner, err := json.Marshal(flow.FaceplateConfig{Name: "TV", Controls: []flow.FaceplateControl{
		{ID: "max", Label: "Max", Type: flow.ControlNumber, Max: &limit},
	}, Values: map[string]any{"max": 10.0}})
	require.NoError(t, err, "the inner faceplate must encode")
	shared := &flow.Graph{Nodes: []flow.Node{{ID: "fp", Type: flow.NodeTypeFaceplate, Config: inner}}}
	sharedID := uuid.New()
	resolver := func(uuid.UUID) (*flow.Graph, error) { return shared, nil }

	cases := []struct {
		name   string
		values string
		want   float64
	}{
		{name: "a number in range applies", values: `{"max":55}`, want: 55},
		{name: "text held from before max became a number", values: `{"max":"loud"}`, want: 10},
		{name: "a number past the control's maximum", values: `{"max":500}`, want: 10},
		{name: "a value cleared to null", values: `{"max":null}`, want: 10},
		{name: "a control the subflow removed", values: `{"gone":7}`, want: 10},
	}
	for _, tc := range cases {
		g := &flow.Graph{Nodes: []flow.Node{{
			ID: "living", Type: flow.NodeTypeSubflow, SubflowID: &sharedID,
			Config: json.RawMessage(`{"faceplate_values":{"fp":` + tc.values + `}}`),
		}}}
		flat, err := engine.FlattenGraph(g, resolver, reg)
		require.NoError(t, err, "%s: the graph must flatten", tc.name)
		require.Len(t, flat.Nodes, 1, "%s: the faceplate is the only node", tc.name)
		cfg, err := flow.ParseFaceplate(flat.Nodes[0].Config)
		require.NoError(t, err, "%s: the flattened faceplate must decode", tc.name)
		assert.Equal(t, tc.want, cfg.ControlValue(cfg.Controls[0]), "%s", tc.name)
		assert.NotContains(t, cfg.Values, "gone", "%s: a value with no control is not carried into the run", tc.name)
	}
}
