package mcpserver_test

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/connmgr"
	"github.com/grmrgecko/karakuri/pkg/connops"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant/hatest"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/nodes/hanodes"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/widgets"
	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestMCPDiscovery reads real protocol replies and bounds MQTT subscriptions without disturbing another consumer.
func TestMCPDiscovery(t *testing.T) {
	srv, token, gated := setupMCPServer(t)
	session := connectMCP(t, srv, token)
	oldKey := config.C.Security.EncryptionKey
	config.C.Security.EncryptionKey = strings.Repeat("0", 64)
	t.Cleanup(func() { config.C.Security.EncryptionKey = oldKey })
	integrations.RegisterKind(homeassistant.KindDef())
	integrations.RegisterKind(mqtt.KindDef())
	mgr := connmgr.New(nil, nil)
	oldMgr := connops.Manager()
	connops.SetManager(mgr)
	t.Cleanup(func() { mgr.Stop(); connops.SetManager(oldMgr) })
	ha := hatest.New("discovery-token")
	t.Cleanup(ha.Close)
	// Synthetic get_services fixture follows Home Assistant's domain/service map, including nested selectors.
	schema := map[string]any{"name": "Turn on", "fields": map[string]any{"brightness": map[string]any{"selector": map[string]any{"number": map[string]any{"min": 0, "max": 255}}}}, "target": map[string]any{"entity": map[string]any{"domain": "light"}}}
	ha.SetServices(map[string]map[string]any{"light": {"turn_on": schema, "turn_off": map[string]any{"name": "Turn off"}}})
	hc, err := connops.Create(t.Context(), uuid.Nil, "Discovery HA", "homeassistant", map[string]any{"base_url": ha.URL, "token": "discovery-token"}, true)
	require.NoError(t, err)
	broker := mochi.New(&mochi.Options{InlineClient: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	require.NoError(t, broker.AddHook(new(auth.AllowHook), nil))
	listener := listeners.NewTCP(listeners.Config{ID: "discovery", Address: "127.0.0.1:0"})
	require.NoError(t, broker.AddListener(listener))
	go func() { _ = broker.Serve() }()
	t.Cleanup(func() { _ = broker.Close() })
	mc, err := connops.Create(t.Context(), uuid.Nil, "Discovery MQTT", "mqtt", map[string]any{"broker_url": "mqtt://" + listener.Address()}, true)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		for _, id := range []uuid.UUID{hc.ID, mc.ID} {
			d, ok := mgr.Get(id)
			if !ok || d.Status().State != integrations.StateConnected {
				return false
			}
		}
		return true
	}, 5*time.Second, 10*time.Millisecond)
	res, services := callTool(t, session, "list_ha_services", map[string]any{"connection_id": hc.ID.String(), "domain": "light", "service": "turn_on"})
	require.False(t, res.IsError)
	items := services["services"].([]any)
	require.Len(t, items, 1)
	item := items[0].(map[string]any)
	assert.Equal(t, "turn_on", item["service"])
	assert.Contains(t, item["schema"], "fields")
	assert.Contains(t, item["schema"], "target")
	assert.Empty(t, ha.ServiceCalls())
	hanodes.RegisterAll(engine.Default())
	_, created := callTool(t, session, "create_flow", map[string]any{"name": "HA references"})
	res, _ = callTool(t, session, "save_flow_graph", map[string]any{"id": created["id"], "graph": map[string]any{"nodes": []map[string]any{{"id": "ha", "type": "ha-call-service", "position": map[string]any{"x": 0, "y": 0}, "config": map[string]any{"connection": hc.ID.String(), "domain": "light", "service": "turn_on", "entity_id": "light.one, light.two"}}}, "edges": []any{}}})
	require.False(t, res.IsError)
	res, refs := callTool(t, session, "find_references", map[string]any{"kind": "ha_entity", "ref": "light.two", "owner": hc.ID.String()})
	require.False(t, res.IsError)
	assert.Len(t, refs["references"], 1)

	driver, err := connops.MQTTDriver(mc.ID.String())
	require.NoError(t, err)
	received := make(chan string, 10)
	unsubscribe, err := driver.Subscribe("checks/+", 0, func(topic string, payload []byte) {
		select {
		case received <- string(payload):
		default:
		}
	})
	require.NoError(t, err)
	defer unsubscribe()
	require.Eventually(t, func() bool { return driver.Subscribed("checks/+") }, 5*time.Second, 10*time.Millisecond)
	// Retained messages make capture setup deterministic without publishing into a subscription race.
	require.NoError(t, broker.Publish("sample/binary", []byte{0xff, 0x00}, true, 0))
	res, captured := callTool(t, session, "capture_mqtt_messages", map[string]any{"connection_id": mc.ID.String(), "topic_filter": "sample/+", "max_messages": 1, "duration_seconds": 3})
	require.False(t, res.IsError)
	samples := captured["messages"].([]any)
	require.Len(t, samples, 1)
	sample := samples[0].(map[string]any)
	assert.Equal(t, "base64", sample["encoding"])
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte{0xff, 0x00}), sample["payload"])
	assert.Equal(t, "message_limit", captured["stop_reason"])
	require.Eventually(t, func() bool { return !driver.Subscribed("sample/+") }, 5*time.Second, 10*time.Millisecond)
	res, captured = callTool(t, session, "capture_mqtt_messages", map[string]any{"connection_id": mc.ID.String(), "topic_filter": "checks/+", "duration_seconds": 1})
	require.False(t, res.IsError)
	assert.Equal(t, "timeout", captured["stop_reason"])
	assert.Empty(t, captured["messages"])
	require.NoError(t, broker.Publish("checks/live", []byte("still subscribed"), false, 0))
	select {
	case payload := <-received:
		assert.Equal(t, "still subscribed", payload)
	case <-time.After(3 * time.Second):
		t.Fatal("capture removed the other consumer's subscription")
	}
	require.NoError(t, broker.Publish("large/data", []byte(strings.Repeat("x", 129<<10)), true, 0))
	res, captured = callTool(t, session, "capture_mqtt_messages", map[string]any{"connection_id": mc.ID.String(), "topic_filter": "large/#", "duration_seconds": 3})
	require.False(t, res.IsError)
	assert.Equal(t, true, captured["truncated"])
	assert.Empty(t, captured["messages"])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	canceled := make(chan struct{})
	go func() {
		_, _ = session.CallTool(ctx, &mcp.CallToolParams{Name: "capture_mqtt_messages", Arguments: map[string]any{"connection_id": mc.ID.String(), "topic_filter": "cancel/#", "duration_seconds": 30}})
		close(canceled)
	}()
	require.Eventually(t, func() bool { return driver.Subscribed("cancel/#") }, 3*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled capture did not return")
	}
	require.Eventually(t, func() bool { return !driver.Subscribed("cancel/#") }, 3*time.Second, 10*time.Millisecond)
	limited := connectMCP(t, srv, gated)
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"list_ha_services", map[string]any{"connection_id": hc.ID.String()}},
		{"capture_mqtt_messages", map[string]any{"connection_id": mc.ID.String(), "topic_filter": "#"}},
	} {
		res, _ := callTool(t, limited, tc.name, tc.args)
		assert.True(t, res.IsError)
	}
}

// TestMCPReferencesAndState pins deployment separation, dashboard access, control aliases, and persisted-state pagination.
func TestMCPReferencesAndState(t *testing.T) {
	srv, token, gated := setupMCPServer(t)
	session := connectMCP(t, srv, token)
	control := models.Control{Name: "Reference control", Kind: "boolean", State: "false", Options: "{}"}
	require.NoError(t, db.DB.Create(&control).Error)
	_, created := callTool(t, session, "create_flow", map[string]any{"name": "Referenced flow"})
	id := created["id"].(string)
	graph := validGraph()
	graph["nodes"] = append(graph["nodes"].([]map[string]any), map[string]any{"id": "control", "type": "control-get", "position": map[string]any{"x": 0, "y": 400}, "config": map[string]any{"control": control.Name}})
	res, _ := callTool(t, session, "save_flow_graph", map[string]any{"id": id, "graph": graph})
	require.False(t, res.IsError)
	res, refs := callTool(t, session, "find_references", map[string]any{"kind": "control", "ref": control.ID.String()})
	require.False(t, res.IsError)
	require.Len(t, refs["references"], 1)
	_, refs = callTool(t, session, "find_references", map[string]any{"kind": "control", "ref": control.Name, "view": "deployed"})
	assert.Empty(t, refs["references"])
	res, _ = callTool(t, session, "deploy_flow", map[string]any{"id": id})
	require.False(t, res.IsError)
	_, refs = callTool(t, session, "find_references", map[string]any{"kind": "control", "ref": control.Name, "view": "deployed"})
	assert.Len(t, refs["references"], 1)
	spec := widgets.Spec{Name: "Reference", PackageName: "reference-check", TagName: "reference-widget", Scope: widgets.WidgetScope{Bindings: "."}}
	require.NoError(t, spec.Validate("reference-check", "1", "", ""))
	require.NoError(t, widgets.Register(zap.NewNop(), "reference-check", t.TempDir(), []widgets.Spec{spec}))
	t.Cleanup(func() { widgets.Unregister("reference-check") })
	_, dashboard := callTool(t, session, "create_dashboard", map[string]any{"name": "Private reference dashboard"})
	did := dashboard["id"]
	res, _ = callTool(t, session, "add_dashboard_widget", map[string]any{"dashboard_id": did, "package": "reference-check", "config": map[string]any{"source": "control", "controlId": control.ID.String()}, "x": 0, "y": 0, "width": 2, "height": 2})
	require.False(t, res.IsError)
	_, refs = callTool(t, session, "find_references", map[string]any{"kind": "control", "ref": control.ID.String()})
	assert.Len(t, refs["references"], 2)
	fid := uuid.MustParse(id)
	require.NoError(t, models.UpsertVariable("flow", fid, "count", "7"))
	require.NoError(t, models.UpsertVariable("flow", fid, "large", `"`+strings.Repeat("x", 65536)+`"`))
	require.NoError(t, models.UpsertVariable("global", uuid.Nil, "private", "9"))
	require.NoError(t, models.UpsertNodeStoreEntry(fid, "counter", "last", `{"value":3}`))
	res, state := callTool(t, session, "inspect_flow_state", map[string]any{"id": id, "limit": 1})
	require.False(t, res.IsError)
	assert.Equal(t, "persisted", state["source"])
	assert.Equal(t, true, state["has_more"])
	entries := state["entries"].([]any)
	require.Len(t, entries, 1)
	assert.Equal(t, float64(7), entries[0].(map[string]any)["value"])
	res, state = callTool(t, session, "inspect_flow_state", map[string]any{"id": id, "after_id": state["next_id"]})
	require.False(t, res.IsError)
	entries = state["entries"].([]any)
	require.Len(t, entries, 1)
	assert.Equal(t, true, entries[0].(map[string]any)["value_omitted"])
	res, state = callTool(t, session, "inspect_flow_state", map[string]any{"id": id, "store": "nodes", "node_id": "counter"})
	require.False(t, res.IsError)
	entries = state["entries"].([]any)
	require.Len(t, entries, 1)
	assert.Equal(t, map[string]any{"value": float64(3)}, entries[0].(map[string]any)["value"])
	limited := connectMCP(t, srv, gated)
	res, _ = callTool(t, limited, "inspect_flow_state", map[string]any{"id": id})
	assert.True(t, res.IsError)
	var role models.Role
	require.NoError(t, db.DB.Where("name = ?", "mcp-gate-only").First(&role).Error)
	require.NoError(t, db.DB.Create(&models.RolePermission{RoleID: role.ID, Permission: perm.PermControlsRead}).Error)
	res, refs = callTool(t, limited, "find_references", map[string]any{"kind": "control", "ref": control.ID.String()})
	require.False(t, res.IsError)
	assert.Empty(t, refs["references"], "control access must not reveal private documents")
}

// TestMCPSchedulePreview uses the engine's timezone rules across the spring DST gap.
func TestMCPSchedulePreview(t *testing.T) {
	srv, token, gated := setupMCPServer(t)
	session := connectMCP(t, srv, token)
	for _, tc := range []struct {
		expr, zone string
		valid      bool
		next       []any
	}{
		{"30 2 * * *", "America/Chicago", true, []any{"2026-03-09T02:30:00-05:00", "2026-03-10T02:30:00-05:00"}},
		{"bad cron", "America/Chicago", false, []any{}},
		{"@daily", "Missing/Zone", false, []any{}},
	} {
		res, result := callTool(t, session, "preview_schedule", map[string]any{"expr": tc.expr, "location": tc.zone, "after": "2026-03-08T00:00:00-06:00", "count": 2})
		require.False(t, res.IsError)
		assert.Equal(t, tc.valid, result["valid"])
		assert.Equal(t, tc.next, result["next"])
	}
	limited := connectMCP(t, srv, gated)
	res, _ := callTool(t, limited, "preview_schedule", map[string]any{"expr": "@hourly"})
	assert.True(t, res.IsError)
}
