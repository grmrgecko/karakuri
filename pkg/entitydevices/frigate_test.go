package entitydevices

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/frigate"
	"github.com/grmrgecko/karakuri/pkg/integrations/frigate/frigatetest"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestFrigateSwitches covers a Frigate server's camera switches as devices:
// listing, adding with camera and feature names, a toggle that changes state
// only when Frigate reports it, the device offline while its camera receives
// no frames, and unknown while the server is unreachable.
func TestFrigateSwitches(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())
	t.Cleanup(frigate.SetTimingForTest(20*time.Millisecond, time.Second))

	server := frigatetest.New()
	t.Cleanup(server.Close)
	raw, _ := json.Marshal(frigate.Config{URL: server.URL})
	connID := uuid.New()
	feed := &statusFeed{first: integrations.Status{State: integrations.StateConnecting}, updates: make(chan integrations.Status, 64)}
	built, err := frigate.New(integrations.ConnModel{ID: connID, Name: "NVR", Kind: frigate.Kind, Config: raw},
		integrations.Deps{PublishStatus: func(s integrations.Status) { feed.updates <- s }})
	require.NoError(t, err)
	driver := built.(*frigate.Driver)

	runner := mqttrelay.NewDevices(func(id uuid.UUID) (any, bool) { return driver, id == connID }, zap.NewNop())
	runner.SetStatusWatcher(feed)
	prev := mqttrelay.D
	mqttrelay.D = runner
	t.Cleanup(func() { mqttrelay.D = prev; runner.Stop() })
	require.NoError(t, driver.Start(context.Background()))
	t.Cleanup(driver.Stop)
	eventually(t, func() bool { return driver.Status().State == integrations.StateConnected && server.Sockets() == 1 }, "connected")

	src := Frigate(driver)
	listed := Discover(src, nil, connID)
	require.Len(t, listed, 8, "four switches on each of two cameras")
	assert.Equal(t, "doorbell/detect", listed[0].ObjectID)
	assert.Equal(t, "doorbell detect", listed[0].Name)
	assert.True(t, listed[0].Commandable)

	result, err := Add(src, connID, []string{"garage/record", "garage/detect", "garage/zoom"})
	require.NoError(t, err)
	require.Len(t, result.Added, 2)
	require.Len(t, result.Failed, 1)
	assert.Equal(t, "garage record", result.Added[0].Name)
	assert.Equal(t, "frigate", result.Added[0].Transport)

	runner.Reload()
	var record, detect = mustDevices(t)[0], mustDevices(t)[1]
	if record.EntityKey != "garage/record" {
		record, detect = detect, record
	}
	state := func() map[string]any { s, _ := runner.State(record.ID); return s }
	availability := func() string { a, _, _ := runner.Availability(detect.ID); return a }
	eventually(t, func() bool { return state()["on"] == false }, "the switch's state reaches the device")
	eventually(t, func() bool { return availability() == mqttrelay.AvailabilityOnline }, "online while the camera has frames")

	_, err = runner.Command(context.Background(), record, "", true, nil, nil)
	require.NoError(t, err)
	eventually(t, func() bool { return state()["on"] == true }, "the state comes back from Frigate")
	assert.Equal(t, []frigatetest.Command{{Topic: "garage/record/set", Payload: "ON"}}, server.Commands())

	server.SetFPS("garage", 0)
	eventually(t, func() bool { return availability() == mqttrelay.AvailabilityOffline }, "offline when the camera stops")
	server.SetFPS("garage", 5)
	eventually(t, func() bool { return availability() == mqttrelay.AvailabilityOnline }, "online when frames return")

	server.DropSockets()
	server.Close()
	eventually(t, func() bool { return availability() == mqttrelay.AvailabilityUnknown }, "unknown while the server is unreachable")
}
