package core

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// sinkNode records every message delivered to it, with the port it arrived
// on, so restart tests can count deliveries across server lifetimes.
type sinkNode struct {
	mu   sync.Mutex
	msgs []*engine.Msg
}

func (s *sinkNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{Type: "test-sink", Label: "sink",
		Inputs: []engine.PortSpec{{ID: "in", Type: flow.PortAny}}}
}

func (s *sinkNode) Execute(_ context.Context, _ engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, inputs["in"])
	return nil, nil
}

// received returns a copy of the delivered messages.
func (s *sinkNode) received() []*engine.Msg {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*engine.Msg(nil), s.msgs...)
}

// durableHarness is one flow and the server lifetimes that run it against a
// shared in-memory SQLite database.
type durableHarness struct {
	t     *testing.T
	clock *engine.FakeClock
	sinks map[string]*sinkNode
	flow  *models.Flow
	graph *flow.Graph
	eng   *engine.Engine
	run   *engine.Runner
}

// newDurableHarness stores g as the enabled, deployed graph of a new flow.
// Every node of type test-sink records into the sink named by its ID.
func newDurableHarness(t *testing.T, g *flow.Graph) *durableHarness {
	t.Helper()
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())
	suffix := uuid.NewString()[:8]
	owner := &models.User{ID: uuid.New(), Username: "owner-" + suffix, Email: suffix + "@example.test", PasswordHash: "x"}
	require.NoError(t, db.DB.Create(owner).Error)
	raw, err := json.Marshal(g)
	require.NoError(t, err)
	f := &models.Flow{ID: uuid.New(), Name: "durable", Slug: "durable-" + suffix, Enabled: true, OwnerID: owner.ID}
	require.NoError(t, db.DB.Create(f).Error)
	v := &models.FlowVersion{FlowID: f.ID, Version: 1, Graph: string(raw)}
	require.NoError(t, db.DB.Create(v).Error)
	f.DeployedVersionID = &v.ID
	require.NoError(t, db.DB.Model(f).Update("deployed_version_id", v.ID).Error)

	h := &durableHarness{
		t: t, clock: engine.NewFakeClock(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)),
		sinks: make(map[string]*sinkNode), flow: f, graph: g,
	}
	for _, n := range g.Nodes {
		if n.Type == "test-sink" {
			h.sinks[n.ID] = &sinkNode{}
		}
	}
	t.Cleanup(h.stop)
	return h
}

// start begins a server lifetime the way serve does: load durable node state
// and stored waits, then arm the flow.
func (h *durableHarness) start() {
	h.t.Helper()
	reg := engine.NewRegistry()
	RegisterAll(reg)
	sinks := &sinkRouter{h: h}
	reg.Register(sinks)
	h.eng = engine.New(engine.Options{
		Registry: reg, Clock: h.clock, Logger: zap.NewNop(),
		History:       engine.NewDBHistory(zap.NewNop()),
		WaitSink:      engine.NewDBWaitSink(zap.NewNop()),
		NodeStateSink: engine.NewDBNodeStateSink(zap.NewNop()),
	})
	state, err := models.ListNodeStoreEntries()
	require.NoError(h.t, err)
	h.eng.LoadNodeState(state)
	rows, err := models.ListPendingWaits()
	require.NoError(h.t, err)
	h.eng.LoadWaits(rows)
	h.eng.Start()
	h.run = engine.NewRunner(h.eng, zap.NewNop())
	require.NoError(h.t, h.run.Start())
}

// stop ends the server lifetime the way serve does.
func (h *durableHarness) stop() {
	if h.eng == nil {
		return
	}
	h.run.Stop()
	h.eng.Stop()
	h.eng, h.run = nil, nil
}

// restart stops the server, lets downtime pass, and starts it again.
func (h *durableHarness) restart(downtime time.Duration) {
	h.t.Helper()
	h.stop()
	h.clock.Advance(downtime)
	h.start()
}

// send starts a deployed run from the named trigger node with msg.
func (h *durableHarness) send(trigger string, msg *engine.Msg) *engine.RunHandle {
	h.t.Helper()
	handle, err := h.eng.RunFlow(engine.RunOptions{
		FlowID: h.flow.ID, FlowVersionID: *h.flow.DeployedVersionID, TriggerNodeID: trigger,
		Graph: h.graph, InitialMsg: msg, DurableWaits: true,
	})
	require.NoError(h.t, err)
	return handle
}

// settled waits until no runs of the flow are active.
func (h *durableHarness) settled() {
	h.t.Helper()
	require.Eventually(h.t, func() bool { return len(h.eng.ActiveRuns()) == 0 }, 3*time.Second, time.Millisecond,
		"the flow's runs must settle")
}

// parked waits until the clock has seen n After calls in total.
func (h *durableHarness) parked(n int) {
	h.t.Helper()
	require.True(h.t, h.clock.BlockUntilAfterCalls(n, 3*time.Second), "a node must park on its wait")
}

// stored counts the flow's rows in pending_waits.
func (h *durableHarness) stored() int {
	h.t.Helper()
	var n int64
	require.NoError(h.t, db.DB.Model(&models.PendingWait{}).Where("flow_id = ?", h.flow.ID).Count(&n).Error)
	return int(n)
}

// eventuallyStored waits for the flow's stored wait count to reach n.
func (h *durableHarness) eventuallyStored(n int) {
	h.t.Helper()
	require.Eventually(h.t, func() bool { return h.stored() == n }, 3*time.Second, time.Millisecond,
		"the flow must have %d stored waits", n)
}

// sinkRouter is the test-sink node type, delivering to the harness sink with
// the node's ID.
type sinkRouter struct{ h *durableHarness }

func (r *sinkRouter) Meta() engine.NodeMeta { return (&sinkNode{}).Meta() }

func (r *sinkRouter) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	return r.h.sinks[ec.NodeID()].Execute(ctx, ec, inputs)
}
