package flowops

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/nodes/core"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// waitSink records deliveries for the redeploy tests.
type waitSink struct {
	mu   sync.Mutex
	msgs []*engine.Msg
}

func (s *waitSink) Meta() engine.NodeMeta {
	return engine.NodeMeta{Type: "flowops-test-sink", Label: "sink", Inputs: []engine.PortSpec{{ID: "in", Type: flow.PortAny}}}
}

func (s *waitSink) Execute(_ context.Context, _ engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, inputs["in"])
	return nil, nil
}

func (s *waitSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.msgs)
}

var (
	registerOnce sync.Once
	sharedSink   = &waitSink{}
)

// waitEnv is a deployed, enabled flow under a real engine and runner.
type waitEnv struct {
	t     *testing.T
	clock *engine.FakeClock
	flow  *models.Flow
	user  uuid.UUID
	eng   *engine.Engine
}

// setupWaitFlow deploys and enables g, the way an author would through the
// editor, against a fresh database.
func setupWaitFlow(t *testing.T, g *flow.Graph) *waitEnv {
	t.Helper()
	registerOnce.Do(func() {
		core.RegisterAll(engine.Default())
		engine.Default().Register(sharedSink)
	})
	sharedSink.mu.Lock()
	sharedSink.msgs = nil
	sharedSink.mu.Unlock()

	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())
	suffix := uuid.NewString()[:8]
	owner := &models.User{ID: uuid.New(), Username: "owner-" + suffix, Email: suffix + "@example.test", PasswordHash: "x"}
	require.NoError(t, db.DB.Create(owner).Error)

	clock := engine.NewFakeClock(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	eng := engine.New(engine.Options{
		Registry: engine.Default(), Clock: clock, Logger: zap.NewNop(),
		History:  engine.NewDBHistory(zap.NewNop()),
		WaitSink: engine.NewDBWaitSink(zap.NewNop()),
	})
	eng.Start()
	runner := engine.NewRunner(eng, zap.NewNop())
	SetEngine(eng)
	SetTriggerRunner(runner)
	t.Cleanup(func() {
		runner.Stop()
		eng.Stop()
		SetEngine(nil)
		SetTriggerRunner(nil)
	})

	f, err := Create(context.Background(), "waits "+suffix, "", false, nil, owner.ID)
	require.NoError(t, err)
	env := &waitEnv{t: t, clock: clock, flow: f, user: owner.ID, eng: eng}
	env.deploy(g)
	require.NoError(t, SetEnabled(t.Context(), f, true))
	return env
}

// deploy saves g as the draft and deploys it.
func (w *waitEnv) deploy(g *flow.Graph) {
	w.t.Helper()
	require.NoError(w.t, SaveDraft(w.flow, g))
	_, err := Deploy(w.t.Context(), w.flow, "test", w.user)
	require.NoError(w.t, err)
}

// arm starts a deployed run from the start node and waits for it to park.
func (w *waitEnv) arm(g *flow.Graph, parks int) {
	w.t.Helper()
	_, err := w.eng.RunFlow(engine.RunOptions{
		FlowID: w.flow.ID, FlowVersionID: *w.flow.DeployedVersionID, TriggerNodeID: "start",
		Graph: g, InitialMsg: &engine.Msg{Payload: "on"}, DurableWaits: true,
	})
	require.NoError(w.t, err)
	require.Eventually(w.t, func() bool { return len(w.eng.PendingWaits(w.flow.ID)) == parks }, 3*time.Second, time.Millisecond,
		"the triggers must arm their waits")
}

// waitNodes returns the node IDs holding pending waits.
func (w *waitEnv) waitNodes() []string {
	var out []string
	for _, pw := range w.eng.PendingWaits(w.flow.ID) {
		out = append(out, pw.NodeID)
	}
	return out
}

// droppedReasons returns the errors of runs that record dropped waits.
func (w *waitEnv) droppedReasons() []string {
	var runs []models.Run
	require.NoError(w.t, db.DB.Where("flow_id = ? AND status = ? AND resumed_from_run_id IS NOT NULL",
		w.flow.ID, engine.RunStatusCanceled).Find(&runs).Error)
	var out []string
	for _, r := range runs {
		out = append(out, r.Error)
	}
	return out
}

// eventuallyDropped waits until the recorded dropped-wait runs carry exactly
// these reasons. History is written asynchronously, so a run row can exist
// before its reason does.
func (w *waitEnv) eventuallyDropped(reasons ...string) {
	w.t.Helper()
	require.Eventually(w.t, func() bool {
		got := w.droppedReasons()
		if len(got) != len(reasons) {
			return false
		}
		for i := range got {
			if got[i] != reasons[i] {
				return false
			}
		}
		return true
	}, 3*time.Second, time.Millisecond, "dropped waits must be recorded with %v; got %v", reasons, w.droppedReasons())
}

// twoTriggers is start feeding triggers a and b, each with a sink.
func twoTriggers(t *testing.T, bType string, aDuration string) *flow.Graph {
	trig := func(d string) json.RawMessage {
		raw, err := json.Marshal(map[string]any{"first_source": "nothing", "duration": d, "second_source": "original"})
		require.NoError(t, err)
		return raw
	}
	bCfg := trig("2m")
	if bType == "delay" {
		bCfg = json.RawMessage(`{"mode":"fixed","duration":"2m"}`)
	}
	g := &flow.Graph{Nodes: []flow.Node{
		{ID: "start", Type: "manual"},
		{ID: "a", Type: "trigger", Config: trig(aDuration)},
		{ID: "sink_a", Type: "flowops-test-sink"},
	}, Edges: []flow.Edge{
		{ID: "sa", Source: "start", SourceHandle: "out", Target: "a", TargetHandle: "in"},
		{ID: "as", Source: "a", SourceHandle: "expired", Target: "sink_a", TargetHandle: "in"},
	}}
	if bType != "" {
		out := "expired"
		if bType == "delay" {
			out = "out"
		}
		g.Nodes = append(g.Nodes, flow.Node{ID: "b", Type: bType, Config: bCfg}, flow.Node{ID: "sink_b", Type: "flowops-test-sink"})
		g.Edges = append(g.Edges,
			flow.Edge{ID: "sb", Source: "start", SourceHandle: "out", Target: "b", TargetHandle: "in"},
			flow.Edge{ID: "bs", Source: "b", SourceHandle: out, Target: "sink_b", TargetHandle: "in"})
	}
	return g
}

// TestRedeployKeepsWaitsOfSurvivingNodes covers US3: a redeploy keeps the
// waits of nodes that keep their ID and type, with their original deadlines,
// and drops and records the rest.
func TestRedeployKeepsWaitsOfSurvivingNodes(t *testing.T) {
	t.Run("removed node", func(t *testing.T) {
		g := twoTriggers(t, "trigger", "2m")
		w := setupWaitFlow(t, g)
		w.arm(g, 2)
		w.deploy(twoTriggers(t, "", "2m"))
		require.Eventually(t, func() bool { return len(w.waitNodes()) == 1 }, 3*time.Second, time.Millisecond)
		assert.Equal(t, []string{"a"}, w.waitNodes(), "only the surviving node keeps its wait")
		w.eventuallyDropped("node b removed by redeploy; wait dropped")
		require.True(t, w.clock.BlockUntilAfterCalls(4, 3*time.Second), "the kept wait resumes and parks")
		w.clock.Advance(2 * time.Minute)
		require.Eventually(t, func() bool { return sharedSink.count() == 1 }, 3*time.Second, time.Millisecond,
			"the kept wait delivers once, and the dropped one never")
	})

	t.Run("retyped node", func(t *testing.T) {
		g := twoTriggers(t, "trigger", "2m")
		w := setupWaitFlow(t, g)
		w.arm(g, 2)
		w.deploy(twoTriggers(t, "delay", "2m"))
		w.eventuallyDropped("node b changed type by redeploy; wait dropped")
		assert.Equal(t, []string{"a"}, w.waitNodes())
	})

	t.Run("changed duration keeps the deadline", func(t *testing.T) {
		g := twoTriggers(t, "", "2m")
		w := setupWaitFlow(t, g)
		w.arm(g, 1)
		w.deploy(twoTriggers(t, "", "10m"))
		require.Eventually(t, func() bool { return len(w.waitNodes()) == 1 }, 3*time.Second, time.Millisecond)
		assert.Equal(t, w.clock.Now().Add(2*time.Minute), w.eng.PendingWaits(w.flow.ID)[0].Deadline,
			"the new duration applies to the next message, not the pending one")
	})

	t.Run("unwired output delivers nothing", func(t *testing.T) {
		g := twoTriggers(t, "", "2m")
		w := setupWaitFlow(t, g)
		w.arm(g, 1)
		unwired := twoTriggers(t, "", "2m")
		unwired.Edges = unwired.Edges[:1]
		w.deploy(unwired)
		require.True(t, w.clock.BlockUntilAfterCalls(3, 3*time.Second))
		w.clock.Advance(2 * time.Minute)
		require.Eventually(t, func() bool { return len(w.waitNodes()) == 0 }, 3*time.Second, time.Millisecond,
			"the wait ends at its deadline")
		assert.Zero(t, sharedSink.count(), "an unwired output delivers nowhere")
	})
}

// TestDisableDropsWaits covers US3's disable: the flow's waits are dropped
// and recorded, and nothing fires later.
func TestDisableDropsWaits(t *testing.T) {
	g := twoTriggers(t, "trigger", "2m")
	w := setupWaitFlow(t, g)
	w.arm(g, 2)
	require.NoError(t, SetEnabled(t.Context(), w.flow, false))
	assert.Empty(t, w.waitNodes(), "a disabled flow holds no waits")
	w.eventuallyDropped("flow disabled; wait dropped", "flow disabled; wait dropped")
	w.clock.Advance(5 * time.Minute)
	assert.Never(t, func() bool { return sharedSink.count() > 0 }, 200*time.Millisecond, 20*time.Millisecond)
	var rows int64
	require.NoError(t, db.DB.Model(&models.PendingWait{}).Where("flow_id = ?", w.flow.ID).Count(&rows).Error)
	assert.Zero(t, rows, "the stored rows go too")
}

// TestRedeployResumesWaitInsideSubflow covers a wait held by a node inside a
// local subflow. The wait is keyed by the flattened ID "sub/d", which the
// placing graph does not contain until its body is expanded, so the resume run
// must cut the graph at the subflow node before it can find the waiting node.
func TestRedeployResumesWaitInsideSubflow(t *testing.T) {
	body := flow.Graph{
		Nodes: []flow.Node{
			{ID: "pi", Type: flow.NodeTypeSubflowInput, Config: json.RawMessage(`{"port_id":"in"}`)},
			{ID: "d", Type: "delay", Config: json.RawMessage(`{"mode":"fixed","duration":"2m"}`)},
			{ID: "po", Type: flow.NodeTypeSubflowOutput, Config: json.RawMessage(`{"port_id":"out"}`)},
		},
		Edges: []flow.Edge{
			{ID: "pd", Source: "pi", SourceHandle: "out", Target: "d", TargetHandle: "in"},
			{ID: "dp", Source: "d", SourceHandle: "out", Target: "po", TargetHandle: "in"},
		},
	}
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "start", Type: "manual"},
			{ID: "sub", Type: flow.NodeTypeSubflow, SubgraphID: "body"},
			{ID: "sink", Type: "flowops-test-sink"},
		},
		Edges: []flow.Edge{
			{ID: "ss", Source: "start", SourceHandle: "out", Target: "sub", TargetHandle: "in"},
			{ID: "sk", Source: "sub", SourceHandle: "out", Target: "sink", TargetHandle: "in"},
		},
		Subgraphs: map[string]flow.Graph{"body": body},
	}
	w := setupWaitFlow(t, g)
	w.arm(g, 1)
	require.Equal(t, []string{"sub/d"}, w.waitNodes(), "a wait inside a subflow is keyed by the flattened node ID")

	w.deploy(g)
	require.Eventually(t, func() bool {
		w.clock.Advance(time.Minute)
		return sharedSink.count() == 1
	}, 3*time.Second, 10*time.Millisecond, "the resumed wait inside the subflow delivers once its deadline passes")
	assert.Empty(t, w.droppedReasons(), "the wait is resumed, not dropped")
}

// TestRerunRefusesTruncatedPayload covers replaying a run whose trigger
// payload was over the 64 KiB history limit. History keeps only a marker
// object for it, and replaying that marker would run the deployed flow on a
// stand-in, so Rerun refuses it while a payload that fit still replays.
func TestRerunRefusesTruncatedPayload(t *testing.T) {
	g := &flow.Graph{
		Nodes: []flow.Node{{ID: "start", Type: "manual"}, {ID: "sink", Type: "flowops-test-sink"}},
		Edges: []flow.Edge{{ID: "ss", Source: "start", SourceHandle: "out", Target: "sink", TargetHandle: "in"}},
	}
	w := setupWaitFlow(t, g)
	fire := func(payload string) {
		// The latest run is found by start time, so each run starts later.
		w.clock.Advance(time.Second)
		h, err := w.eng.RunFlow(engine.RunOptions{
			FlowID: w.flow.ID, FlowVersionID: *w.flow.DeployedVersionID, TriggerNodeID: "start",
			Graph: g, InitialMsg: &engine.Msg{Payload: payload},
		})
		require.NoError(t, err)
		<-h.Done()
		require.Eventually(t, func() bool {
			run, err := models.FindLastRunByFlow(w.flow.ID)
			return err == nil && run.ID == h.ID && run.EndedAt != nil
		}, 3*time.Second, time.Millisecond, "the run must be persisted before it can be replayed")
	}

	fire("small")
	_, err := Rerun(w.flow, uuid.Nil)
	require.NoError(t, err, "a payload that fit in history replays")

	fire(strings.Repeat("x", 70*1024))
	_, err = Rerun(w.flow, uuid.Nil)
	require.Error(t, err, "the truncation marker must not be replayed as the payload")
	assert.Contains(t, err.Error(), "too large")
}

// TestDeleteCancelsActiveRuns locks that deleting a flow stops what it is
// doing. Delete disarmed the triggers and purged the waits but left the runs
// themselves alone, so a run parked on a timer went on to act for a flow that
// no longer existed. Disabling a flow already cancels its runs; deleting it
// must do no less.
func TestDeleteCancelsActiveRuns(t *testing.T) {
	g := twoTriggers(t, "trigger", "2m")
	w := setupWaitFlow(t, g)
	w.arm(g, 2)
	require.NotEmpty(t, w.eng.ActiveRuns(), "the armed run is active before the delete")

	require.NoError(t, Delete(w.flow))
	assert.Eventually(t, func() bool { return len(w.eng.ActiveRuns()) == 0 }, 3*time.Second, 5*time.Millisecond,
		"a deleted flow has no active runs")
	w.clock.Advance(5 * time.Minute)
	assert.Never(t, func() bool { return sharedSink.count() > 0 }, 200*time.Millisecond, 20*time.Millisecond,
		"nothing downstream of the timers runs after the delete")
}

// TestRerunRefusesResumedRun locks that Rerun only replays a trigger event.
// A run that resumed a restored wait is recorded under the waiting node with
// no trigger payload, and Rerun started that node as if it were the trigger,
// handing it an empty message on the trigger port.
func TestRerunRefusesResumedRun(t *testing.T) {
	env := setupWaitFlow(t, twoTriggers(t, "", "2m"))
	armed := uuid.New()
	require.NoError(t, models.ReplaceLastRun(&models.Run{
		ID: uuid.New(), FlowID: env.flow.ID, FlowVersionID: *env.flow.DeployedVersionID,
		TriggerNodeID: "a", TriggerPayload: "null", Status: engine.RunStatusSucceeded,
		StartedAt: time.Now(), ResumedFromRunID: &armed,
	}), "the resumed run must be the flow's last run")

	_, err := Rerun(env.flow, uuid.Nil)
	require.Error(t, err, "a resumed wait has no trigger event to replay")
	assert.Contains(t, err.Error(), "resumed", "the refusal says why the run cannot be replayed")
}

// TestRunFromNodeSeedsEveryFanOutBranch locks that the outputs read back from
// the last run hold every port a node emitted on. A switch below a split runs
// once per part and reports each time with only the port that part took. The
// later report used to replace the earlier one, so run-from-node refused the
// branch the first part took with MISSING_UPSTREAM_DATA.
func TestRunFromNodeSeedsEveryFanOutBranch(t *testing.T) {
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "start", Type: "manual"},
			{ID: "split", Type: "split", Config: json.RawMessage(`{"mode":"array","property":"payload"}`)},
			{ID: "sw", Type: "switch", Config: json.RawMessage(
				`{"property":"payload","match":"first","rules":[{"id":"low","operator":"lt","value":"10"},{"id":"high","operator":"otherwise"}]}`)},
			{ID: "sink_low", Type: "flowops-test-sink"},
			{ID: "sink_high", Type: "flowops-test-sink"},
		},
		Edges: []flow.Edge{
			{ID: "e1", Source: "start", SourceHandle: "out", Target: "split", TargetHandle: "in"},
			{ID: "e2", Source: "split", SourceHandle: "out", Target: "sw", TargetHandle: "in"},
			{ID: "e3", Source: "sw", SourceHandle: "low", Target: "sink_low", TargetHandle: "in"},
			{ID: "e4", Source: "sw", SourceHandle: "high", Target: "sink_high", TargetHandle: "in"},
		},
	}
	w := setupWaitFlow(t, g)
	// 1 takes the low port and 20 the high one.
	h, err := w.eng.RunFlow(engine.RunOptions{
		FlowID: w.flow.ID, FlowVersionID: *w.flow.DeployedVersionID, TriggerNodeID: "start",
		Graph: g, InitialMsg: &engine.Msg{Payload: []any{1.0, 20.0}},
	})
	require.NoError(t, err, "the run must start")
	<-h.Done()
	require.Equal(t, engine.RunStatusSucceeded, h.Snapshot().Status, h.Snapshot().Error)
	require.Eventually(t, func() bool {
		run, err := models.FindLastRunByFlow(w.flow.ID)
		return err == nil && run.ID == h.ID && run.EndedAt != nil
	}, 3*time.Second, time.Millisecond, "the run must be persisted before its outputs are read")

	outputs, err := lastRunOutputs(w.flow.ID)
	require.NoError(t, err, "the last run's outputs must load")
	for _, tc := range []struct {
		start string
		want  float64
	}{
		{start: "sink_low", want: 1},
		{start: "sink_high", want: 20},
	} {
		seed, err := buildSeedInputs(g, tc.start, outputs)
		if assert.NoError(t, err, "%s ran in the last run, so a run can start from it", tc.start) {
			require.Contains(t, seed, "in", "%s is seeded on its message input", tc.start)
			assert.Equal(t, tc.want, seed["in"].Payload, "%s is seeded with the part that took its branch", tc.start)
		}
	}
}

// TestListWaitsReadsMissedDeadlineInsideSubflow locks that a pending wait
// reports the missed_deadline setting of the node that armed it. Waits are
// keyed by the flattened node ID, so a delay inside a subflow is "sub/d", and
// reading only the top-level nodes reported it as "deliver" while the engine
// dropped it on restore.
func TestListWaitsReadsMissedDeadlineInsideSubflow(t *testing.T) {
	body := flow.Graph{
		Nodes: []flow.Node{
			{ID: "pi", Type: flow.NodeTypeSubflowInput, Config: json.RawMessage(`{"port_id":"in"}`)},
			{ID: "d", Type: "delay", Config: json.RawMessage(`{"mode":"fixed","duration":"2m","missed_deadline":"drop"}`)},
			{ID: "po", Type: flow.NodeTypeSubflowOutput, Config: json.RawMessage(`{"port_id":"out"}`)},
		},
		Edges: []flow.Edge{
			{ID: "pd", Source: "pi", SourceHandle: "out", Target: "d", TargetHandle: "in"},
			{ID: "dp", Source: "d", SourceHandle: "out", Target: "po", TargetHandle: "in"},
		},
	}
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "start", Type: "manual"},
			{ID: "sub", Type: flow.NodeTypeSubflow, SubgraphID: "body"},
			{ID: "top", Type: "delay", Config: json.RawMessage(`{"mode":"fixed","duration":"3m","missed_deadline":"drop"}`)},
			{ID: "plain", Type: "delay", Config: json.RawMessage(`{"mode":"fixed","duration":"4m"}`)},
			{ID: "sink", Type: "flowops-test-sink"},
		},
		Edges: []flow.Edge{
			{ID: "ss", Source: "start", SourceHandle: "out", Target: "sub", TargetHandle: "in"},
			{ID: "st", Source: "start", SourceHandle: "out", Target: "top", TargetHandle: "in"},
			{ID: "sp", Source: "start", SourceHandle: "out", Target: "plain", TargetHandle: "in"},
			{ID: "sk", Source: "sub", SourceHandle: "out", Target: "sink", TargetHandle: "in"},
		},
		Subgraphs: map[string]flow.Graph{"body": body},
	}
	w := setupWaitFlow(t, g)
	w.arm(g, 3)

	waits, err := ListWaits(w.flow)
	require.NoError(t, err, "the pending waits must list")
	byNode := map[string]string{}
	for _, pw := range waits {
		byNode[pw.NodeID] = pw.MissedDeadline
	}
	assert.Equal(t, map[string]string{"top": "drop", "sub/d": "drop", "plain": "deliver"}, byNode,
		"each wait reports its own node's setting, and a node that sets none delivers")
}
