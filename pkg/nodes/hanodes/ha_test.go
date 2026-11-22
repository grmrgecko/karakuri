package hanodes_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/connmgr"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant/hatest"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/nodes/core"
	"github.com/grmrgecko/karakuri/pkg/nodes/hanodes"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testEncryptionKey is a fixed 32-byte hex key for connection config crypto.
const testEncryptionKey = "6368616e676520746869732070617373776f726420746f206120736563726574"

const testToken = "node-test-token"

// setupDB opens an in-memory database with migrations and an encryption key.
func setupDB(t *testing.T) {
	t.Helper()
	require.NoError(t, config.Load(), "the connection store needs a config before it can encrypt")
	config.C.Security.EncryptionKey = testEncryptionKey
	require.NoError(t, db.Init(db.Options{
		Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1,
	}), "the tests need a database to store the connection and flow in")
	require.NoError(t, migrate.AutoMigrate(), "the tables are created by the real migrations")
}

// createConnection stores an enabled HA connection pointing at the fake server.
func createConnection(t *testing.T, baseURL string) *models.Connection {
	t.Helper()
	conn := &models.Connection{Name: "Test HA", Kind: homeassistant.Kind, Enabled: true}
	require.NoError(t, conn.SetConfig(homeassistant.Config{BaseURL: baseURL, Token: testToken}),
		"the connection config is encrypted on the way in and must round-trip")
	require.NoError(t, db.DB.Create(conn).Error, "the connection must be stored for the manager to find it")
	return conn
}

// createDeployedFlow stores an enabled flow whose deployed graph is
// ha-state(light.kitchen, to "off") -> ha-call-service(switch.turn_off).
func createDeployedFlow(t *testing.T, connID uuid.UUID) *models.Flow {
	t.Helper()
	user := &models.User{ID: uuid.New(), Username: "tester", Email: "t@example.com", PasswordHash: "x"}
	require.NoError(t, db.DB.Create(user).Error, "a flow needs an owner")

	graph := map[string]any{
		"nodes": []map[string]any{
			{"id": "state", "type": "ha-state", "position": map[string]any{"x": 0, "y": 0},
				"config": map[string]any{
					"connection": connID.String(),
					"entity_id":  "light.kitchen",
					"to":         "off",
				}},
			{"id": "call", "type": "ha-call-service", "position": map[string]any{"x": 200, "y": 0},
				"config": map[string]any{
					"connection": connID.String(),
					"domain":     "switch",
					"service":    "turn_off",
					"entity_id":  "switch.zigbee_plug",
				}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "state", "source_handle": "out", "target": "call", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
	raw, err := json.Marshal(graph)
	require.NoError(t, err, "the graph fixture must encode")

	f := &models.Flow{Name: "HA Test Flow", OwnerID: user.ID, Enabled: true}
	require.NoError(t, db.DB.Create(f).Error, "the flow must be stored")

	version := &models.FlowVersion{FlowID: f.ID, Version: 1, Graph: string(raw)}
	require.NoError(t, db.DB.Create(version).Error, "the runner arms the deployed version, so one must exist")

	f.DeployedVersionID = &version.ID
	require.NoError(t, db.DB.Model(f).Update("deployed_version_id", version.ID).Error,
		"the flow must point at the version, or the runner finds nothing to arm")
	return f
}

// createDeployedAssistFlow stores an enabled flow that answers one Assist
// route with a fixed sentence. The transport is the part under test.
func createDeployedAssistFlow(t *testing.T, connID uuid.UUID) *models.Flow {
	t.Helper()
	user := &models.User{ID: uuid.New(), Username: "assist-tester", Email: "assist@example.com", PasswordHash: "x"}
	require.NoError(t, db.DB.Create(user).Error, "an Assist flow needs an owner")

	graph := map[string]any{
		"nodes": []map[string]any{
			{"id": "assist", "type": "ha-assist", "position": map[string]any{"x": 0, "y": 0},
				"config": map[string]any{"connection": connID.String(), "agent_id": "house", "continue_conversation": "no"}},
			{"id": "answer", "type": "template", "position": map[string]any{"x": 200, "y": 0},
				"config": map[string]any{"template": "The garage is closed.", "format": "plain", "output_format": "string"}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "assist", "source_handle": "out", "target": "answer", "target_handle": "in"},
			{"id": "e2", "source": "answer", "source_handle": "out", "target": "assist", "target_handle": "response"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
	raw, err := json.Marshal(graph)
	require.NoError(t, err, "the Assist graph fixture must encode")
	f := &models.Flow{Name: "Assist transport", OwnerID: user.ID, Enabled: true}
	require.NoError(t, db.DB.Create(f).Error, "the Assist flow must be stored for the runner")
	version := &models.FlowVersion{FlowID: f.ID, Version: 1, Graph: string(raw)}
	require.NoError(t, db.DB.Create(version).Error, "the runner needs a deployed Assist graph")
	f.DeployedVersionID = &version.ID
	require.NoError(t, db.DB.Model(f).Update("deployed_version_id", version.ID).Error,
		"the Assist flow must point at its deployed graph")
	return f
}

// waitFor polls a condition until it holds or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.FailNow(t, msg)
}

// zigbeeCalls counts recorded switch.turn_off calls targeting the plug.
func zigbeeCalls(srv *hatest.Server) int {
	n := 0
	for _, c := range srv.ServiceCalls() {
		if c.Domain != "switch" || c.Service != "turn_off" {
			continue
		}
		ids, _ := c.Target["entity_id"].([]any)
		for _, id := range ids {
			if id == "switch.zigbee_plug" {
				n++
			}
		}
	}
	return n
}

// TestHAFlowThroughRunner assembles the whole arming path — the connection
// manager, the driver, the runner, the engine, and a fake Home Assistant — and
// checks that a pushed state change ends in a service call. Every piece is
// covered separately; what only appears here is whether a *deployed* flow
// actually arms, since the trigger's filter is read from the stored graph.
//
// The three non-matching events are the point of the middle section: a trigger
// that fired on all of them would look identical on the matching one, and the
// difference is whether an automation runs when nobody touched anything.
// Disarming is checked because a flow disabled in the UI that keeps firing is
// the worst kind of stale state.
func TestHAFlowThroughRunner(t *testing.T) {
	srv := hatest.New(testToken)
	defer srv.Close()
	srv.SetState("light.kitchen", "on", map[string]any{"friendly_name": "Kitchen Light"})
	srv.SetState("switch.zigbee_plug", "on", nil)
	setupDB(t)

	// Fresh registry so the test does not pollute the process default.
	reg := engine.NewRegistry()
	core.RegisterAll(reg)
	hanodes.RegisterAll(reg)
	integrations.RegisterKind(homeassistant.KindDef())

	conn := createConnection(t, srv.URL)
	f := createDeployedFlow(t, conn.ID)

	// Connection manager with the real driver.
	mgr := connmgr.New(nil, nil)
	require.NoError(t, mgr.Start(), "the connection manager must bring the stored connection up")
	t.Cleanup(mgr.Stop)

	// Engine wired to the manager; no history sink needed.
	eng := engine.New(engine.Options{Registry: reg, ConnResolver: mgr.Resolve})
	eng.Start()
	t.Cleanup(eng.Stop)

	// Arm the deployed flow through the real runner.
	runner := engine.NewRunner(eng, nil)
	require.NoError(t, runner.Start(), "the runner must arm every enabled, deployed flow")
	t.Cleanup(runner.Stop)

	waitFor(t, 10*time.Second, func() bool { return srv.SubscribeCount("state_changed") >= 1 },
		"the deployed flow's trigger never reached the server, so it was never really armed")

	// A matching state change fires the flow, which calls the service.
	srv.PushStateChanged("light.kitchen",
		hatest.StateObj("light.kitchen", "on", nil),
		hatest.StateObj("light.kitchen", "off", nil))
	waitFor(t, 10*time.Second, func() bool { return zigbeeCalls(srv) == 1 },
		"a matching state change did not run the flow through to its service call")

	// Non-matching deliveries must not fire: wrong entity, wrong target
	// state, and an unchanged state.
	srv.PushStateChanged("sensor.other",
		hatest.StateObj("sensor.other", "1", nil),
		hatest.StateObj("sensor.other", "2", nil))
	srv.PushStateChanged("light.kitchen",
		hatest.StateObj("light.kitchen", "off", nil),
		hatest.StateObj("light.kitchen", "on", nil))
	srv.PushStateChanged("light.kitchen",
		hatest.StateObj("light.kitchen", "on", nil),
		hatest.StateObj("light.kitchen", "on", nil))
	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, 1, zigbeeCalls(srv),
		"a wrong entity, the wrong target state, and an unchanged state must all be ignored, "+
			"or the automation runs when nobody touched anything")

	// A second matching change fires again.
	srv.PushStateChanged("light.kitchen",
		hatest.StateObj("light.kitchen", "on", nil),
		hatest.StateObj("light.kitchen", "off", nil))
	waitFor(t, 10*time.Second, func() bool { return zigbeeCalls(srv) == 2 },
		"the trigger fired once and then stopped, so it does not stay armed")

	// Disarming stops the trigger.
	runner.Disarm(f.ID)
	time.Sleep(200 * time.Millisecond)
	srv.PushStateChanged("light.kitchen",
		hatest.StateObj("light.kitchen", "on", nil),
		hatest.StateObj("light.kitchen", "off", nil))
	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, 2, zigbeeCalls(srv),
		"a flow disabled in the UI must stop acting on the house, which is the whole meaning of the switch")
}

// TestHAAssistFlowThroughRunner pins the event and service pair used by the
// custom component. It exercises the deployed trigger, the real WebSocket
// driver, and the response action together so the Assist path cannot drift
// back to a separate HTTP credential.
func TestHAAssistFlowThroughRunner(t *testing.T) {
	srv := hatest.New(testToken)
	defer srv.Close()
	setupDB(t)

	reg := engine.NewRegistry()
	core.RegisterAll(reg)
	hanodes.RegisterAll(reg)
	integrations.RegisterKind(homeassistant.KindDef())

	conn := createConnection(t, srv.URL)
	createDeployedAssistFlow(t, conn.ID)
	mgr := connmgr.New(nil, nil)
	require.NoError(t, mgr.Start(), "the Home Assistant connection must start")
	t.Cleanup(mgr.Stop)

	eng := engine.New(engine.Options{Registry: reg, ConnResolver: mgr.Resolve})
	eng.Start()
	t.Cleanup(eng.Stop)
	runner := engine.NewRunner(eng, nil)
	require.NoError(t, runner.Start(), "the runner must arm the Assist flow")
	t.Cleanup(runner.Stop)

	waitFor(t, 10*time.Second, func() bool {
		return srv.SubscribeCount("karakuri_assist_request") >= 1
	}, "the Assist node never subscribed through the Home Assistant connection")
	srv.PushEvent("karakuri_assist_request", map[string]any{
		"event_type": "karakuri_assist_request",
		"data": map[string]any{
			"request_id": "request-1", "agent_id": "house",
			"text": "Is the garage closed?", "conversation_id": "conversation-1", "language": "en-US",
		},
	})
	waitFor(t, 10*time.Second, func() bool {
		for _, call := range srv.ServiceCalls() {
			if call.Domain == "karakuri_assist" && call.Service == "respond" {
				return true
			}
		}
		return false
	}, "the Assist response did not return through Home Assistant")

	calls := srv.ServiceCalls()
	call := calls[len(calls)-1]
	assert.Equal(t, "request-1", call.ServiceData["request_id"], "Home Assistant must receive the reply for its pending turn")
	assert.Equal(t, "house", call.ServiceData["agent_id"], "the route must survive the flow")
	assert.Equal(t, "The garage is closed.", call.ServiceData["response"], "the service must carry the flow's spoken answer")
	assert.Equal(t, false, call.ServiceData["continue_conversation"], "the Assist return controls whether it listens again")
}

// TestHACallServiceRetry is the reliability story this project exists for,
// assembled end to end. A Zigbee device that misses a command is the everyday
// failure in a real house; the flow author's answer is a retry policy, and this
// checks it actually re-issues the call rather than merely marking the node
// retryable. Two scripted failures then a success means three calls must reach
// Home Assistant and the run must come out succeeded — a run that reported
// failure after the light did turn off would be worse than no retry at all.
func TestHACallServiceRetry(t *testing.T) {
	srv := hatest.New(testToken)
	defer srv.Close()
	srv.FailService("switch", "turn_off", 2, "Zigbee device unreachable")
	setupDB(t)

	reg := engine.NewRegistry()
	core.RegisterAll(reg)
	hanodes.RegisterAll(reg)
	integrations.RegisterKind(homeassistant.KindDef())

	conn := createConnection(t, srv.URL)
	mgr := connmgr.New(nil, nil)
	require.NoError(t, mgr.Start(), "the connection manager must bring the stored connection up")
	t.Cleanup(mgr.Stop)

	driver, ok := mgr.Get(conn.ID)
	require.True(t, ok, "the enabled connection must have a running driver")
	waitFor(t, 5*time.Second, func() bool {
		return driver.Status().State == integrations.StateConnected
	}, "the driver never connected, so the retries would be against a dead session")

	eng := engine.New(engine.Options{Registry: reg, ConnResolver: mgr.Resolve})
	eng.Start()
	t.Cleanup(eng.Stop)

	// manual -> ha-call-service with a 3-attempt retry policy.
	cfg, _ := json.Marshal(map[string]any{
		"connection": conn.ID.String(),
		"domain":     "switch",
		"service":    "turn_off",
		"entity_id":  "switch.zigbee_plug",
	})
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "t", Type: "manual"},
			{ID: "call", Type: "ha-call-service", Config: cfg,
				Retry: &flow.RetryPolicy{MaxAttempts: 3, DelayMs: 10}},
		},
		Edges: []flow.Edge{
			{ID: "e1", Source: "t", SourceHandle: "out", Target: "call", TargetHandle: "in"},
		},
	}

	h, err := eng.RunFlow(engine.RunOptions{
		FlowID:        uuid.New(),
		FlowName:      "retry-flow",
		FlowVersionID: 1,
		TriggerNodeID: "t",
		Graph:         g,
		InitialMsg:    &engine.Msg{},
	})
	require.NoError(t, err, "the run must start")

	select {
	case <-h.Done():
	case <-time.After(15 * time.Second):
		require.FailNow(t, "the run never settled")
	}

	snap := h.Snapshot()
	require.Equalf(t, engine.RunStatusSucceeded, snap.Status,
		"the third attempt succeeded, so the run must: reporting failure after the device did respond is worse than no retry: %s",
		snap.Error)
	assert.Len(t, srv.ServiceCalls(), 3,
		"two scripted failures plus one success means three calls must actually reach Home Assistant")
}
