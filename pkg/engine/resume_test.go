package engine_test

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// waitNode holds each message for its configured duration in a stored wait,
// the smallest node that exercises the whole durable wait contract: arm,
// suspend, resume, and claim.
type waitNode struct{}

func (waitNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:       "hold",
		Label:      "hold",
		Inputs:     []engine.PortSpec{{ID: "in", Type: flow.PortAny}},
		Outputs:    []engine.PortSpec{{ID: "out", Type: flow.PortAny}},
		ConfigSpec: engine.MissedDeadlineFields(),
	}
}

func (waitNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg struct {
		Duration string `json:"duration"`
	}
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	d, err := time.ParseDuration(cfg.Duration)
	if err != nil {
		return nil, err
	}
	ws, id := engine.WaitsFor(ec)
	state, err := json.Marshal(inputs["in"])
	if err != nil {
		return nil, err
	}
	key := "exec:" + id
	w, _ := ws.Update(key, func(engine.Wait, bool) *engine.Wait {
		return &engine.Wait{Deadline: ec.Clock().Now().Add(d), State: state}
	})
	return holdUntil(ctx, ec, ws, key, w)
}

func (waitNode) Resume(ctx context.Context, ec engine.ExecContext, key string, w engine.Wait) (map[string][]*engine.Msg, error) {
	ws, _ := engine.WaitsFor(ec)
	return holdUntil(ctx, ec, ws, key, w)
}

// holdUntil parks to the wait's deadline and emits its message after a
// successful claim. A suspended park leaves the wait stored.
func holdUntil(ctx context.Context, ec engine.ExecContext, ws engine.WaitStore, key string, w engine.Wait) (map[string][]*engine.Msg, error) {
	if remaining := w.Deadline.Sub(ec.Clock().Now()); remaining > 0 {
		if err := ec.Park(ctx, remaining); err != nil {
			if !engine.Suspended(ctx) {
				ws.Update(key, func(cur engine.Wait, ok bool) *engine.Wait {
					if ok && cur.Token != w.Token {
						return &cur
					}
					return nil
				})
			}
			return nil, err
		}
	}
	_, ok, err := ws.Claim(key, w.Token)
	if err != nil || !ok {
		return nil, err
	}
	var msg engine.Msg
	if err := json.Unmarshal(w.State, &msg); err != nil {
		return nil, err
	}
	return engine.One("out", &msg), nil
}

// captureNode records every message it receives.
type captureNode struct {
	mu   sync.Mutex
	msgs []*engine.Msg
	got  chan struct{}
}

func newCapture() *captureNode { return &captureNode{got: make(chan struct{}, 16)} }

func (c *captureNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{Type: "capture", Label: "capture", Inputs: []engine.PortSpec{{ID: "in", Type: flow.PortAny}}}
}

func (c *captureNode) Execute(_ context.Context, _ engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	c.mu.Lock()
	c.msgs = append(c.msgs, inputs["in"])
	c.mu.Unlock()
	c.got <- struct{}{}
	return nil, nil
}

func (c *captureNode) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.msgs)
}

func (c *captureNode) last() *engine.Msg {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.msgs[len(c.msgs)-1]
}

// waitFor blocks until the capture node has received a message.
func (c *captureNode) waitFor(t *testing.T) {
	t.Helper()
	select {
	case <-c.got:
	case <-time.After(waitTimeout):
		require.FailNow(t, "the held message never arrived downstream")
	}
}

// durableEnv is one server lifetime: an engine and runner over the shared
// database, with the registry the flow's nodes come from.
type durableEnv struct {
	eng    *engine.Engine
	runner *engine.Runner
}

// setupWaitDB opens a fresh in-memory SQLite database with the real
// migrations.
func setupWaitDB(t *testing.T) {
	t.Helper()
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}),
		"the tests need a database for stored waits")
	require.NoError(t, migrate.AutoMigrate(), "the pending_waits table comes from the real migrations")
}

// deployFlow stores an enabled flow whose deployed graph is
// trigger → hold → capture, with the hold node's config given.
func deployFlow(t *testing.T, holdCfg string) *models.Flow {
	t.Helper()
	return deployGraph(t, graph([]flow.Node{
		gnode("t", "trigger"),
		{ID: "h", Type: "hold", Config: json.RawMessage(holdCfg)},
		gnode("c", "capture"),
	}, []flow.Edge{gedge("t", "out", "h", "in"), gedge("h", "out", "c", "in")}))
}

// deployGraph stores an enabled flow whose deployed graph is g, written
// straight to the database as a past deploy left it.
func deployGraph(t *testing.T, g *flow.Graph) *models.Flow {
	t.Helper()
	raw, err := json.Marshal(g)
	require.NoError(t, err)
	suffix := uuid.NewString()[:8]
	owner := &models.User{ID: uuid.New(), Username: "owner-" + suffix, Email: suffix + "@example.test", PasswordHash: "x"}
	require.NoError(t, db.DB.Create(owner).Error, "a flow needs an owner")
	f := &models.Flow{ID: uuid.New(), Name: "hold", Slug: "hold-" + suffix, Enabled: true, OwnerID: owner.ID}
	require.NoError(t, db.DB.Create(f).Error, "the flow row must exist for the runner to arm it")
	v := &models.FlowVersion{FlowID: f.ID, Version: 1, Graph: string(raw)}
	require.NoError(t, db.DB.Create(v).Error)
	f.DeployedVersionID = &v.ID
	require.NoError(t, db.DB.Model(f).Update("deployed_version_id", v.ID).Error)
	return f
}

// startServer builds an engine and runner over the database, loads stored
// waits, and arms enabled flows, as serve does at startup.
func startServer(t *testing.T, fc *engine.FakeClock, capture *captureNode) *durableEnv {
	t.Helper()
	reg := engine.NewRegistry()
	reg.Register(triggerStub())
	reg.Register(waitNode{})
	reg.Register(capture)
	eng := engine.New(engine.Options{
		Registry: reg, Clock: fc, Logger: zap.NewNop(),
		History:  engine.NewDBHistory(zap.NewNop()),
		WaitSink: engine.NewDBWaitSink(zap.NewNop()),
	})
	rows, err := models.ListPendingWaits()
	require.NoError(t, err)
	eng.LoadWaits(rows)
	eng.Start()
	runner := engine.NewRunner(eng, zap.NewNop())
	require.NoError(t, runner.Start())
	return &durableEnv{eng: eng, runner: runner}
}

// stop shuts a server down the way serve does.
func (s *durableEnv) stop() {
	s.runner.Stop()
	s.eng.Stop()
}

// startHold starts a deployed run of the flow that parks in the hold node.
func startHold(t *testing.T, s *durableEnv, fc *engine.FakeClock, f *models.Flow, parks int) *engine.RunHandle {
	t.Helper()
	v, err := models.FindFlowVersionByID(*f.DeployedVersionID)
	require.NoError(t, err)
	var g flow.Graph
	require.NoError(t, json.Unmarshal([]byte(v.Graph), &g))
	h, err := s.eng.RunFlow(engine.RunOptions{
		FlowID: f.ID, FlowVersionID: v.ID, TriggerNodeID: "t", Graph: &g, DurableWaits: true,
		InitialMsg: &engine.Msg{Payload: "hello", Topic: "light/porch"},
	})
	require.NoError(t, err)
	require.True(t, fc.BlockUntilAfterCalls(parks, 2*time.Second), "the hold node must park on its wait")
	return h
}

// storedWaits counts the flow's rows in pending_waits.
func storedWaits(t *testing.T, flowID uuid.UUID) int {
	t.Helper()
	var n int64
	require.NoError(t, db.DB.Model(&models.PendingWait{}).Where("flow_id = ?", flowID).Count(&n).Error)
	return int(n)
}

// TestWaitSurvivesRestart covers US1: a wait suspended by shutdown is stored,
// restored by the next start, and delivered once at its original deadline
// with the run that armed it linked from the run that finished it.
func TestWaitSurvivesRestart(t *testing.T) {
	setupWaitDB(t)
	fc := engine.NewFakeClock(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	capture := newCapture()
	f := deployFlow(t, `{"duration":"2m"}`)

	first := startServer(t, fc, capture)
	h := startHold(t, first, fc, f, 1)
	fc.Advance(30 * time.Second)
	first.stop()
	snap := waitDone(t, h)
	assert.Equal(t, engine.RunStatusCanceled, snap.Status, "shutdown cancels the parked run")
	assert.Equal(t, engine.ErrSuspended.Error(), snap.Error, "the run says its wait was carried over")
	require.Equal(t, 1, storedWaits(t, f.ID), "a suspended wait stays stored for the next start")
	assert.Zero(t, capture.count(), "nothing is delivered while the server is down")

	second := startServer(t, fc, capture)
	t.Cleanup(second.stop)
	waits := second.eng.PendingWaits(f.ID)
	require.Len(t, waits, 1, "the restored wait is listed")
	assert.Equal(t, time.Date(2026, 10, 30, 12, 2, 0, 0, time.UTC), waits[0].Deadline,
		"the deadline is the original one, not two minutes from the restart")

	require.True(t, fc.BlockUntilAfterCalls(2, 2*time.Second), "the resumed run parks until the deadline")
	fc.Advance(90 * time.Second)
	capture.waitFor(t)
	msg := capture.last()
	assert.Equal(t, "hello", msg.Payload, "the held message is the one that arrived before the restart")
	assert.Equal(t, "light/porch", msg.Topic)
	resumed, ok := msg.Meta["resumed"].(map[string]any)
	require.True(t, ok, "a resumed delivery says so: %+v", msg.Meta)
	assert.Equal(t, h.ID.String(), resumed["from_run_id"], "the delivery links the run that armed the wait")
	assert.EqualValues(t, 0, resumed["late_ms"], "a deadline still ahead at restore is on time")
	assert.Eventually(t, func() bool { return storedWaits(t, f.ID) == 0 }, waitTimeout, 10*time.Millisecond,
		"a delivered wait is removed")
}

// TestWaitSurvivesRestartBesideBrokenFragment covers a deployed flow that
// also carries a disconnected, bypassed node whose type is no longer
// registered, as when its plugin is disabled after deploy. Runs never reach
// the fragment, so restoring the working branch's wait must not depend on
// the whole canvas flattening.
func TestWaitSurvivesRestartBesideBrokenFragment(t *testing.T) {
	setupWaitDB(t)
	fc := engine.NewFakeClock(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	capture := newCapture()
	g := graph([]flow.Node{
		gnode("t", "trigger"),
		{ID: "h", Type: "hold", Config: json.RawMessage(`{"duration":"2m"}`)},
		gnode("c", "capture"),
		{ID: "x", Type: "retired-plugin-node", Bypass: true},
	}, []flow.Edge{gedge("t", "out", "h", "in"), gedge("h", "out", "c", "in")})
	f := deployGraph(t, g)

	first := startServer(t, fc, capture)
	startHold(t, first, fc, f, 1)
	first.stop()
	require.Equal(t, 1, storedWaits(t, f.ID), "a suspended wait stays stored for the next start")

	second := startServer(t, fc, capture)
	t.Cleanup(second.stop)
	require.True(t, fc.BlockUntilAfterCalls(2, 2*time.Second), "the restored wait resumes and parks until its deadline")
	fc.Advance(2 * time.Minute)
	capture.waitFor(t)
	assert.Equal(t, "hello", capture.last().Payload, "the held message is delivered after the restart")
}

// TestMissedDeadlines covers US2: a deadline that passed while the server was
// down is delivered late by default, dropped when the node says drop, and
// delivered as on time when it falls within the grace period.
func TestMissedDeadlines(t *testing.T) {
	cases := []struct {
		name      string
		cfg       string
		downtime  time.Duration
		delivered bool
	}{
		{"deliver late by default", `{"duration":"30s"}`, 10 * time.Minute, true},
		{"drop when configured", `{"duration":"30s","missed_deadline":"drop","missed_grace":"5s"}`, 10 * time.Minute, false},
		{"drop within grace counts as on time", `{"duration":"30s","missed_deadline":"drop","missed_grace":"5m"}`, time.Minute, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupWaitDB(t)
			fc := engine.NewFakeClock(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
			capture := newCapture()
			f := deployFlow(t, tc.cfg)

			first := startServer(t, fc, capture)
			startHold(t, first, fc, f, 1)
			first.stop()
			fc.Advance(tc.downtime)

			second := startServer(t, fc, capture)
			t.Cleanup(second.stop)
			if tc.delivered {
				capture.waitFor(t)
				resumed, _ := capture.last().Meta["resumed"].(map[string]any)
				assert.Positive(t, resumed["late_ms"], "a late delivery reports how late it was")
			} else {
				assert.Eventually(t, func() bool {
					var runs []models.Run
					_ = db.DB.Where("flow_id = ? AND status = ?", f.ID, engine.RunStatusCanceled).Find(&runs).Error
					for _, r := range runs {
						if r.ResumedFromRunID != nil && r.Error != "" && r.Error != engine.ErrSuspended.Error() {
							return true
						}
					}
					return false
				}, waitTimeout, 10*time.Millisecond, "a dropped wait leaves a canceled run saying why")
				assert.Zero(t, capture.count(), "a dropped wait delivers nothing")
			}
			assert.Eventually(t, func() bool { return storedWaits(t, f.ID) == 0 }, waitTimeout, 10*time.Millisecond,
				"the wait is gone either way")
		})
	}
}

// TestOperatorCancelReleasesWait covers the other side of suspension: a run
// cancelled by an operator deletes its wait instead of carrying it over.
func TestOperatorCancelReleasesWait(t *testing.T) {
	setupWaitDB(t)
	fc := engine.NewFakeClock(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	capture := newCapture()
	f := deployFlow(t, `{"duration":"2m"}`)
	s := startServer(t, fc, capture)
	t.Cleanup(s.stop)

	h := startHold(t, s, fc, f, 1)
	require.True(t, s.eng.CancelRun(h.ID))
	waitDone(t, h)
	assert.Empty(t, s.eng.PendingWaits(f.ID), "a cancelled run's wait does not outlive it")
	assert.Eventually(t, func() bool { return storedWaits(t, f.ID) == 0 }, waitTimeout, 10*time.Millisecond)
}

// TestRearmDoesNotDuplicateLiveWaiter covers a rearm while a run is parked
// (a connection change does this): the wait already has its waiter, so no
// second run is started and the message is delivered once.
func TestRearmDoesNotDuplicateLiveWaiter(t *testing.T) {
	setupWaitDB(t)
	fc := engine.NewFakeClock(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	capture := newCapture()
	f := deployFlow(t, `{"duration":"2m"}`)
	s := startServer(t, fc, capture)
	t.Cleanup(s.stop)

	startHold(t, s, fc, f, 1)
	s.runner.Rearm(f.ID)
	assert.Len(t, s.eng.ActiveRuns(), 1, "the parked run is the only waiter")
	fc.Advance(2 * time.Minute)
	capture.waitFor(t)
	assert.Never(t, func() bool { return capture.count() > 1 }, 200*time.Millisecond, 20*time.Millisecond,
		"the wait is delivered once")
}

// renamedCapture is a capture node under another type name, so a test can
// tell which deployed version a delivery ran.
type renamedCapture struct {
	*captureNode
	typ string
}

func (c renamedCapture) Meta() engine.NodeMeta {
	m := c.captureNode.Meta()
	m.Type = c.typ
	return m
}

// TestRedeployResumesQueuedWaitsOnNewVersion covers a redeploy while restored
// waits still queue behind the flow's run limit. The queue used to outlive the
// rearm and keep its waits reserved, so the new version's restore skipped them
// and the old queue later resumed them on the previous graph. Both waits must
// run the version deployed when they finally start.
func TestRedeployResumesQueuedWaitsOnNewVersion(t *testing.T) {
	setupWaitDB(t)
	fc := engine.NewFakeClock(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	v1Capture := newCapture()
	f := deployFlow(t, `{"duration":"1h"}`)

	// Store two waits the way a shutdown leaves them.
	first := startServer(t, fc, v1Capture)
	h1 := startHold(t, first, fc, f, 1)
	h2 := startHold(t, first, fc, f, 2)
	first.stop()
	waitDone(t, h1)
	waitDone(t, h2)
	require.Equal(t, 2, storedWaits(t, f.ID), "both suspended waits stay stored")

	// Restart with room for one run: the first wait parks, the second queues
	// behind the limit.
	v2Capture := renamedCapture{captureNode: newCapture(), typ: "capture-v2"}
	reg := engine.NewRegistry()
	reg.Register(triggerStub())
	reg.Register(waitNode{})
	reg.Register(v1Capture)
	reg.Register(v2Capture)
	eng := engine.New(engine.Options{
		Registry: reg, Clock: fc, Logger: zap.NewNop(), MaxRunsPerFlow: 1,
		History:  engine.NewDBHistory(zap.NewNop()),
		WaitSink: engine.NewDBWaitSink(zap.NewNop()),
	})
	rows, err := models.ListPendingWaits()
	require.NoError(t, err)
	eng.LoadWaits(rows)
	eng.Start()
	runner := engine.NewRunner(eng, zap.NewNop())
	second := &durableEnv{eng: eng, runner: runner}
	t.Cleanup(second.stop)
	require.NoError(t, runner.Start())
	// 2 parks on the first server, then 1 park and 1 limit backoff here.
	require.True(t, fc.BlockUntilAfterCalls(4, 2*time.Second), "one wait parks and the other backs off on the run limit")

	// Redeploy with a different downstream node, as flowops does.
	raw, err := json.Marshal(graph([]flow.Node{
		gnode("t", "trigger"),
		{ID: "h", Type: "hold", Config: json.RawMessage(`{"duration":"1h"}`)},
		gnode("c2", "capture-v2"),
	}, []flow.Edge{gedge("t", "out", "h", "in"), gedge("h", "out", "c2", "in")}))
	require.NoError(t, err)
	v2 := &models.FlowVersion{FlowID: f.ID, Version: 2, Graph: string(raw)}
	require.NoError(t, db.DB.Create(v2).Error)
	require.NoError(t, db.DB.Model(f).Update("deployed_version_id", v2.ID).Error)
	eng.SuspendFlowRuns(f.ID)
	runner.Rearm(f.ID)

	// Step past the deadline, then past each limit backoff, until both waits
	// are delivered.
	fc.Advance(time.Hour)
	require.Eventually(t, func() bool {
		if v1Capture.count()+v2Capture.count() >= 2 {
			return true
		}
		fc.Advance(30 * time.Second)
		return false
	}, waitTimeout, 50*time.Millisecond, "both waits are delivered")
	assert.Zero(t, v1Capture.count(), "no wait resumes on the version the redeploy replaced")
	assert.Equal(t, 2, v2Capture.count(), "both waits resume on the deployed version")
}

// TestClaimFailureDoesNotEmit covers FR-008: a claim that cannot store its
// delete refuses delivery rather than risk a second one after a restart.
func TestClaimFailureDoesNotEmit(t *testing.T) {
	ws := &failingClaimStore{WaitStore: engine.NewMemWaitStore()}
	w, _ := ws.Update("k", func(engine.Wait, bool) *engine.Wait {
		return &engine.Wait{Deadline: time.Now(), State: json.RawMessage(`{"payload":1}`)}
	})
	ctx := newTestExec()
	out, err := holdUntil(context.Background(), ctx, ws, "k", w)
	require.Error(t, err, "the claim error is returned so the node fails")
	assert.Nil(t, out, "nothing is emitted when the claim is not stored")
}

// failingClaimStore is a wait store whose claims cannot be stored.
type failingClaimStore struct{ engine.WaitStore }

func (failingClaimStore) Claim(string, string) (engine.Wait, bool, error) {
	return engine.Wait{}, false, errors.New("database is locked")
}

// testExec is the bare ExecContext holdUntil needs: a clock.
type testExec struct{ engine.ExecContext }

func newTestExec() *testExec { return &testExec{} }

func (testExec) Clock() engine.Clock { return engine.RealClock{} }

// skipTrigger records one skipped event when armed.
type skipTrigger struct{}

func (skipTrigger) Meta() engine.NodeMeta {
	return engine.NodeMeta{Type: "skipper", Label: "skipper", Category: flow.CategoryTrigger,
		Outputs: []engine.PortSpec{{ID: "out", Type: flow.PortAny}}}
}

func (skipTrigger) Execute(_ context.Context, _ engine.ExecContext, in map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	return engine.One("out", in[engine.TriggerInput]), nil
}

func (skipTrigger) Start(ctx context.Context, ec engine.ExecContext, _ func(*engine.Msg)) error {
	ec.(engine.TriggerRecorder).Skipped(&engine.Msg{Payload: map[string]any{"title": "Vacation"}}, "missed start; dropped")
	<-ctx.Done()
	return nil
}

// TestTriggerSkippedIsRecorded covers TriggerRecorder: an armed trigger that
// skips an event leaves a canceled run giving the reason and the event.
func TestTriggerSkippedIsRecorded(t *testing.T) {
	setupWaitDB(t)
	suffix := uuid.NewString()[:8]
	owner := &models.User{ID: uuid.New(), Username: "owner-" + suffix, Email: suffix + "@example.test", PasswordHash: "x"}
	require.NoError(t, db.DB.Create(owner).Error)
	raw, err := json.Marshal(graph([]flow.Node{gnode("s", "skipper")}, nil))
	require.NoError(t, err)
	f := &models.Flow{ID: uuid.New(), Name: "skip", Slug: "skip-" + suffix, Enabled: true, OwnerID: owner.ID}
	require.NoError(t, db.DB.Create(f).Error)
	v := &models.FlowVersion{FlowID: f.ID, Version: 1, Graph: string(raw)}
	require.NoError(t, db.DB.Create(v).Error)
	require.NoError(t, db.DB.Model(f).Update("deployed_version_id", v.ID).Error)

	reg := engine.NewRegistry()
	reg.Register(skipTrigger{})
	eng := engine.New(engine.Options{Registry: reg, Logger: zap.NewNop(), History: engine.NewDBHistory(zap.NewNop())})
	eng.Start()
	runner := engine.NewRunner(eng, zap.NewNop())
	require.NoError(t, runner.Start())
	t.Cleanup(func() { runner.Stop(); eng.Stop() })

	var run models.Run
	require.Eventually(t, func() bool {
		return db.DB.Where("flow_id = ?", f.ID).First(&run).Error == nil && run.EndedAt != nil
	}, waitTimeout, 10*time.Millisecond, "the skipped event is recorded as a run")
	assert.Equal(t, engine.RunStatusCanceled, run.Status)
	assert.Equal(t, "missed start; dropped", run.Error)
	assert.Equal(t, "s", run.TriggerNodeID)
	assert.Equal(t, v.ID, run.FlowVersionID)
	assert.JSONEq(t, `{"title":"Vacation"}`, run.TriggerPayload)
}

// configTrigger reports the config it was armed with.
type configTrigger struct{ armed chan float64 }

func (configTrigger) Meta() engine.NodeMeta {
	return engine.NodeMeta{Type: "config-trigger", Label: "config-trigger", Category: flow.CategoryTrigger,
		Outputs: []engine.PortSpec{{ID: "out", Type: flow.PortAny}}, ConfigSpec: paramSpec}
}

func (configTrigger) Execute(_ context.Context, _ engine.ExecContext, in map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	return engine.One("out", in[engine.TriggerInput]), nil
}

func (c configTrigger) Start(ctx context.Context, ec engine.ExecContext, _ func(*engine.Msg)) error {
	var cfg struct {
		Threshold float64 `json:"threshold"`
	}
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	c.armed <- cfg.Threshold
	<-ctx.Done()
	return nil
}

// TestArmedTriggerReadsWiredParameter locks that a trigger is armed with the
// config its runs execute with. A field exposed as a parameter is supplied
// only by its wire, and deploy accepts whatever literal sits behind it, so a
// trigger armed on the literal watched for a setting nobody could see or
// change. The unwired row pins the literal for a field with no wire.
func TestArmedTriggerReadsWiredParameter(t *testing.T) {
	for _, tc := range []struct {
		name  string
		wired bool
		want  float64
	}{
		{"a wired field takes the pulled value", true, 21.5},
		{"an unwired field keeps its literal", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupWaitDB(t)
			var runs int32
			trigger := configTrigger{armed: make(chan float64, 1)}
			node := flow.Node{ID: "s", Type: "config-trigger", Config: json.RawMessage(`{"threshold":1}`)}
			var edges []flow.Edge
			if tc.wired {
				node.Params = []string{"threshold"}
				edges = append(edges, valueEdge("konst", "out", "s", "threshold"))
			}
			f := deployGraph(t, graph([]flow.Node{gnode("konst", "konst"), node}, edges))

			reg := engine.NewRegistry()
			reg.Register(trigger)
			reg.Register(constNode("konst", 21.5, &runs))
			eng := engine.New(engine.Options{Registry: reg, Logger: zap.NewNop(), History: engine.NewDBHistory(zap.NewNop())})
			eng.Start()
			runner := engine.NewRunner(eng, zap.NewNop())
			require.NoError(t, runner.Start())
			t.Cleanup(func() { runner.Stop(); eng.Stop() })

			select {
			case got := <-trigger.armed:
				assert.Equal(t, tc.want, got, "the armed trigger must read the same config a run of flow %s would", f.ID)
			case <-time.After(waitTimeout):
				require.FailNow(t, "the trigger was never armed")
			}
		})
	}
}
