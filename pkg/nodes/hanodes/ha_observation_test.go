package hanodes_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/grmrgecko/karakuri/pkg/connmgr"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/nodes/core"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant/hatest"
	"github.com/grmrgecko/karakuri/pkg/nodes/hanodes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func observationEngine(t *testing.T, srv *hatest.Server, clock engine.Clock) (*engine.Engine, uuid.UUID, *homeassistant.Driver) {
	t.Helper()
	id := uuid.New()
	cfg, err := json.Marshal(homeassistant.Config{BaseURL: srv.URL, Token: testToken})
	require.NoError(t, err)
	driver, err := homeassistant.New(integrations.ConnModel{ID: id, Config: cfg}, integrations.Deps{})
	require.NoError(t, err)
	require.NoError(t, driver.Start(context.Background()))
	t.Cleanup(driver.Stop)
	waitFor(t, 5*time.Second, func() bool { return driver.Status().State == integrations.StateConnected }, "HA peer must connect")
	reg := engine.NewRegistry()
	hanodes.RegisterAll(reg)
	eng := engine.New(engine.Options{Registry: reg, Clock: clock, ConnResolver: func(key string) (any, error) {
		if key != id.String() {
			return nil, fmt.Errorf("unknown connection")
		}
		return driver, nil
	}})
	eng.Start()
	t.Cleanup(eng.Stop)
	return eng, id, driver.(*homeassistant.Driver)
}

// TestServiceTargetsAndFreshConfirmation covers a confirmed service call that
// observes its state and one that times out. The bare-seconds row locks that
// the confirmation durations read a number without a unit as seconds, the way
// every other duration field does; they used to fail the node with a missing
// unit error before the call went out.
func TestServiceTargetsAndFreshConfirmation(t *testing.T) {
	for _, tc := range []struct{ name, state, port, timeout, interval string }{
		{"observed", "on", "out", "100ms", "5ms"},
		{"timeout", "off", "timeout", "100ms", "5ms"},
		{"observed with bare seconds", "on", "out", "0.1", "0.005"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := hatest.New(testToken)
			defer srv.Close()
			srv.SetState("light.kitchen", "off", nil)
			eng, id, _ := observationEngine(t, srv, nil)
			cfg, _ := json.Marshal(map[string]any{"connection": id.String(), "domain": "light", "service": "turn_on", "area_id": "kitchen,lounge", "device_id": "device-a", "label_id": "night", "entity_id": "light.kitchen", "confirm": true, "confirmation_entity_id": "light.kitchen", "confirmation_value": "on", "confirmation_timeout": tc.timeout, "confirmation_interval": tc.interval})
			if tc.state == "on" {
				go func() {
					for len(srv.ServiceCalls()) == 0 {
						time.Sleep(time.Millisecond)
					}
					srv.SetState("light.kitchen", "on", nil)
				}()
			}
			result, err := eng.TestNode(context.Background(), engine.RunOptions{Graph: &flow.Graph{Nodes: []flow.Node{{ID: "call", Type: "ha-call-service", Config: cfg, Retry: &flow.RetryPolicy{MaxAttempts: 3, DelayMs: 1}}}}, StartNodeID: "call", SeedInputs: map[string]*engine.Msg{"in": {Payload: "original", Topic: "room", Meta: map[string]any{"trace": "kept"}}}})
			require.NoError(t, err)
			require.Equal(t, engine.RunStatusSucceeded, result.Status, result.Error)
			require.Len(t, result.Outputs[tc.port], 1)
			var msg engine.Msg
			require.NoError(t, json.Unmarshal(result.Outputs[tc.port][0], &msg))
			assert.Equal(t, "original", msg.Payload)
			assert.Equal(t, "kept", msg.Meta["trace"])
			calls := srv.ServiceCalls()
			require.Len(t, calls, 1, "confirmation must not resend an accepted action")
			assert.Equal(t, []any{"kitchen", "lounge"}, calls[0].Target["area_id"])
			assert.Equal(t, []any{"device-a"}, calls[0].Target["device_id"])
			assert.Equal(t, []any{"night"}, calls[0].Target["label_id"])
		})
	}
}

// TestHeldStateIsolationAndConnectionGap covers held state changes: each
// entity has its own timer, a contrary change cancels it, and a connection gap
// invalidates a pending hold. The hold is written as a bare "1", which arming
// must read as one second like every other duration field.
func TestHeldStateIsolationAndConnectionGap(t *testing.T) {
	srv := hatest.New(testToken)
	defer srv.Close()
	setupDB(t)
	integrations.RegisterKind(homeassistant.KindDef())
	conn := createConnection(t, srv.URL)
	mgr := connmgr.New(nil, nil)
	require.NoError(t, mgr.Start())
	t.Cleanup(mgr.Stop)
	driver, ok := mgr.Get(conn.ID)
	require.True(t, ok)
	waitFor(t, 5*time.Second, func() bool { return driver.Status().State == integrations.StateConnected }, "HA peer must connect")
	f := createDeployedFlow(t, conn.ID)
	var version struct{ Graph string }
	require.NoError(t, db.DB.Table("flow_versions").Select("graph").Where("id = ?", *f.DeployedVersionID).Scan(&version).Error)
	var graph flow.Graph
	require.NoError(t, json.Unmarshal([]byte(version.Graph), &graph))
	graph.Nodes[0].Config = json.RawMessage(fmt.Sprintf(`{"connection":%q,"entity_id":"light.kitchen,light.lounge","from":"off","to":"on","held_for":"1"}`, conn.ID.String()))
	raw, err := json.Marshal(graph)
	require.NoError(t, err)
	require.NoError(t, db.DB.Table("flow_versions").Where("id = ?", *f.DeployedVersionID).Update("graph", string(raw)).Error)
	clock := engine.NewFakeClock(time.Now())
	reg := engine.NewRegistry()
	core.RegisterAll(reg)
	hanodes.RegisterAll(reg)
	eng := engine.New(engine.Options{Registry: reg, Clock: clock, ConnResolver: mgr.Resolve})
	eng.Start()
	t.Cleanup(eng.Stop)
	runner := engine.NewRunner(eng, nil)
	require.NoError(t, runner.Start())
	t.Cleanup(runner.Stop)
	waitFor(t, 5*time.Second, func() bool { return srv.SubscribeCount("state_changed") >= 1 }, "state subscription must arm")
	push := func(entity, old, next string) {
		srv.PushStateChanged(entity, hatest.StateObj(entity, old, nil), hatest.StateObj(entity, next, nil))
	}
	push("light.kitchen", "off", "on")
	require.True(t, clock.BlockUntilAfterCalls(1, time.Second))
	push("light.kitchen", "on", "off") // Cancel before the from/to filter rejects this event.
	push("light.lounge", "off", "on")
	push("light.kitchen", "off", "on")
	require.True(t, clock.BlockUntilAfterCalls(3, time.Second))
	clock.Advance(time.Second)
	waitFor(t, 5*time.Second, func() bool { return zigbeeCalls(srv) == 2 }, "only the two independent matching holds may fire")
	push("light.kitchen", "on", "off")
	push("light.kitchen", "off", "on")
	require.True(t, clock.BlockUntilAfterCalls(4, time.Second))
	continuity := driver.(*homeassistant.Driver)
	generation, _ := continuity.ConnectionGeneration()
	srv.CloseClients()
	waitFor(t, 5*time.Second, func() bool {
		next, connected := continuity.ConnectionGeneration()
		return !connected || next != generation
	}, "disconnect must break event continuity")
	clock.Advance(time.Second)
	runner.Disarm(f.ID)
	assert.Equal(t, 2, zigbeeCalls(srv), "a connection gap must invalidate the pending hold")
}

func TestAcceptedServiceDoesNotRetryFailedObservation(t *testing.T) {
	srv := hatest.New(testToken)
	defer srv.Close()
	eng, id, _ := observationEngine(t, srv, nil)
	srv.FailStateReads("state database unavailable")
	cfg, _ := json.Marshal(map[string]any{"connection": id.String(), "domain": "light", "service": "turn_on", "confirm": true, "confirmation_entity_id": "light.kitchen", "confirmation_value": "on"})
	result, err := eng.TestNode(context.Background(), engine.RunOptions{Graph: &flow.Graph{Nodes: []flow.Node{{ID: "call", Type: "ha-call-service", Config: cfg, Retry: &flow.RetryPolicy{MaxAttempts: 3, DelayMs: 1}}}}, StartNodeID: "call", SeedInputs: map[string]*engine.Msg{"in": {}}})
	require.NoError(t, err)
	assert.Equal(t, engine.RunStatusFailed, result.Status)
	assert.Contains(t, result.Error, "state database unavailable")
	assert.Len(t, srv.ServiceCalls(), 1)
}
