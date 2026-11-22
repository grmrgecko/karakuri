package mqttrelay

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// blockingTransport is an entity transport whose first watch waits on a
// channel, holding one Reload inside start while another runs.
type blockingTransport struct {
	entered chan struct{}
	release chan struct{}
	watches atomic.Int32
	drops   atomic.Int32
}

func (b *blockingTransport) WatchEntity(string, func(map[string]any)) (func(), error) {
	if b.watches.Add(1) == 1 {
		close(b.entered)
		<-b.release
	}
	return func() { b.drops.Add(1) }, nil
}

func (b *blockingTransport) CommandEntity(context.Context, string, string, any) error {
	return nil
}

// TestConcurrentReloadStartsADeviceOnce pins the race where two reloads both
// saw a device as not running and each started it, so the first instance
// stayed subscribed with nothing left to drop it. A Zigbee2MQTT device list
// published twice in quick succession reloads from two goroutines this way.
func TestConcurrentReloadStartsADeviceOnce(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())
	m := models.MQTTDevice{ID: uuid.New(), Name: "plug", ConnectionID: uuid.New(), Control: "switch",
		Transport: TransportESPHome, EntityKey: "plug_relay"}
	require.NoError(t, models.SaveMQTTDevice(&m))

	transport := &blockingTransport{entered: make(chan struct{}), release: make(chan struct{})}
	d := NewDevices(func(uuid.UUID) (any, bool) { return transport, true }, zap.NewNop())

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); d.Reload() }()
	<-transport.entered
	go func() { defer wg.Done(); d.Reload() }()
	// The second reload needs time to reach the running check while the
	// first is still inside start.
	time.Sleep(100 * time.Millisecond)
	close(transport.release)
	wg.Wait()

	assert.Equal(t, int32(1), transport.watches.Load(), "one stored device must be watched once")
	d.Stop()
	assert.Equal(t, transport.watches.Load(), transport.drops.Load(), "every watch must be dropped when the devices stop")
}
