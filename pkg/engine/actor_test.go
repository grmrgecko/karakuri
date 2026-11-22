package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNodesActAsTheirFlow covers the flow half of SC-001: whatever a node
// does through its context, such as commanding a device, is recorded as the
// flow and run that ran it.
func TestNodesActAsTheirFlow(t *testing.T) {
	var got events.Actor
	fc := engine.NewFakeClock(time.Now())
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(),
		&stubNode{typ: "act", inputs: []string{"in"}, fn: func(ctx context.Context, _ engine.ExecContext, _ map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			got = events.ActorFrom(ctx)
			return nil, nil
		}},
	)
	g := graph([]flow.Node{gnode("t", "trigger"), gnode("a", "act")}, []flow.Edge{gedge("t", "out", "a", "in")})
	h := startRun(t, e, g)
	snap := waitDone(t, h)
	require.Equal(t, engine.RunStatusSucceeded, snap.Status)

	assert.Equal(t, events.ActorFlow, got.Kind)
	assert.Equal(t, "test-flow", got.Name)
	assert.Equal(t, snap.FlowID.String(), got.ID)
	assert.Equal(t, snap.ID.String(), got.RunID)
	assert.Equal(t, events.ViaFlow, got.Via)
}
