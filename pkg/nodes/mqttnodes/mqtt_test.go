package mqttnodes_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/connmgr"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/nodes/core"
	"github.com/grmrgecko/karakuri/pkg/nodes/mqttnodes"
	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testEncryptionKey is a fixed 32-byte hex key for connection config crypto.
const testEncryptionKey = "6368616e676520746869732070617373776f726420746f206120736563726574"

// startBroker runs an embedded MQTT broker on a random port.
func startBroker(t *testing.T) (*mochi.Server, string) {
	t.Helper()
	server := mochi.New(&mochi.Options{
		InlineClient: true,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, server.AddHook(new(auth.AllowHook), nil), "the test broker accepts any client")
	tcp := listeners.NewTCP(listeners.Config{ID: "test", Address: "127.0.0.1:0"})
	require.NoError(t, server.AddListener(tcp), "the test broker needs a loopback listener")
	go func() { _ = server.Serve() }()
	t.Cleanup(func() { _ = server.Close() })
	return server, tcp.Address()
}

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

// createConnection stores an enabled MQTT connection pointing at the broker.
func createConnection(t *testing.T, addr string) *models.Connection {
	t.Helper()
	conn := &models.Connection{Name: "Test Broker", Kind: mqtt.Kind, Enabled: true}
	require.NoError(t, conn.SetConfig(mqtt.Config{BrokerURL: "mqtt://" + addr}),
		"the connection config is encrypted on the way in and must round-trip")
	require.NoError(t, db.DB.Create(conn).Error, "the connection must be stored for the manager to find it")
	return conn
}

// createDeployedFlow stores an enabled flow whose deployed graph is
// mqtt-in(zigbee/+/state) -> mqtt-out(republished/state) -> debug.
func createDeployedFlow(t *testing.T, connID uuid.UUID) *models.Flow {
	t.Helper()
	user := &models.User{ID: uuid.New(), Username: "tester", Email: "t@example.com", PasswordHash: "x"}
	require.NoError(t, db.DB.Create(user).Error, "a flow needs an owner")

	graph := map[string]any{
		"nodes": []map[string]any{
			{"id": "in", "type": "mqtt-in", "position": map[string]any{"x": 0, "y": 0},
				"config": map[string]any{
					"connection":  connID.String(),
					"topic":       "zigbee/+/state",
					"qos":         "0",
					"parse_json":  true,
					"test_params": map[string]any{"state": "ON"},
				}},
			{"id": "out", "type": "mqtt-out", "position": map[string]any{"x": 200, "y": 0},
				"config": map[string]any{
					"connection":     connID.String(),
					"topic":          "republished/state",
					"qos":            "0",
					"payload_source": "input",
				}},
			{"id": "dbg", "type": "debug", "position": map[string]any{"x": 400, "y": 0}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "in", "source_handle": "out", "target": "out", "target_handle": "in"},
			{"id": "e2", "source": "out", "source_handle": "out", "target": "dbg", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
	raw, err := json.Marshal(graph)
	require.NoError(t, err, "the graph fixture must encode")

	f := &models.Flow{Name: "MQTT Test Flow", OwnerID: user.ID, Enabled: true}
	require.NoError(t, db.DB.Create(f).Error, "the flow must be stored")

	version := &models.FlowVersion{FlowID: f.ID, Version: 1, Graph: string(raw)}
	require.NoError(t, db.DB.Create(version).Error, "the runner arms the deployed version, so one must exist")

	f.DeployedVersionID = &version.ID
	require.NoError(t, db.DB.Model(f).Update("deployed_version_id", version.ID).Error,
		"the flow must point at the version, or the runner finds nothing to arm")
	return f
}

// TestMQTTFlowThroughRunner assembles the whole arming path against a real
// broker: the connection manager, the driver, the runner, and the engine. What
// only appears here is whether a *deployed* flow actually arms — the trigger's
// topic filter is read from the stored graph — and that the trigger and the
// publish share one broker session rather than opening a connection each.
//
// Disarming is checked because a flow disabled in the UI that keeps publishing
// is the worst kind of stale state: it acts on the house with nothing in the
// interface to show for it.
func TestMQTTFlowThroughRunner(t *testing.T) {
	server, addr := startBroker(t)
	setupDB(t)

	// Fresh registry so the test does not pollute the process default.
	reg := engine.NewRegistry()
	core.RegisterAll(reg)
	mqttnodes.RegisterAll(reg)
	integrations.RegisterKind(mqtt.KindDef())

	conn := createConnection(t, addr)
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

	// Observe the republished topic directly on the driver session.
	driver, ok := mgr.Get(conn.ID)
	require.True(t, ok, "the enabled connection must have a running driver")
	mqttDriver, ok := driver.(*mqtt.Driver)
	require.True(t, ok, "the mqtt kind's factory must produce an mqtt driver")

	var mu sync.Mutex
	var republished []string
	_, err := mqttDriver.Subscribe("republished/state", 0, func(topic string, payload []byte) {
		mu.Lock()
		republished = append(republished, string(payload))
		mu.Unlock()
	})
	require.NoError(t, err, "the observer subscribes on the same session the flow publishes through")

	// Publish into the trigger's filter until the flow fires. The retry loop
	// covers subscription establishment racing the first publish.
	deadline := time.Now().Add(10 * time.Second)
	fired := false
	for time.Now().Before(deadline) {
		_ = server.Publish("zigbee/kitchen/state", []byte(`{"state":"ON","brightness":128}`), false, 0)
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		fired = len(republished) > 0
		mu.Unlock()
		if fired {
			break
		}
	}
	require.True(t, fired,
		"a broker message on the trigger's filter never ran the deployed flow through to its publish")

	mu.Lock()
	got := republished[0]
	mu.Unlock()

	var payload map[string]any
	require.NoErrorf(t, json.Unmarshal([]byte(got), &payload), "the republished payload must be JSON: %s", got)
	assert.Equal(t, "ON", payload["state"], "the parsed payload must survive the trip through the graph")
	assert.Equal(t, float64(128), payload["brightness"],
		"the whole payload must be republished, not just the field a rule happened to read")

	// Disarming stops the trigger: new broker messages must not fire runs.
	runner.Disarm(f.ID)
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	before := len(republished)
	mu.Unlock()
	_ = server.Publish("zigbee/kitchen/state", []byte(`{"state":"OFF"}`), false, 0)
	time.Sleep(500 * time.Millisecond)
	mu.Lock()
	after := len(republished)
	mu.Unlock()
	assert.Equal(t, before, after,
		"a flow disabled in the UI must stop acting on the broker, which is the whole meaning of the switch")
}
