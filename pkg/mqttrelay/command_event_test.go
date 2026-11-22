package mqttrelay

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// recordingTransport accepts every command.
type recordingTransport struct{ sent int }

func (r *recordingTransport) WatchEntity(string, func(map[string]any)) (func(), error) {
	return func() {}, nil
}

func (r *recordingTransport) CommandEntity(context.Context, string, string, any) error {
	r.sent++
	return nil
}

// TestCommandRecordsItsSender covers US1 scenario 3: a command sent to a
// device is logged with whoever the context says sent it, a flow here, and a
// refused command is not logged.
func TestCommandRecordsItsSender(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: filepath.Join(t.TempDir(), "e.db"), MaxOpenConns: 2, MaxIdleConns: 2}))
	require.NoError(t, db.DB.AutoMigrate(&models.Event{}))
	events.R = events.NewRecorder(nil, nil)
	events.R.Start()
	t.Cleanup(func() { events.R = nil })

	transport := &recordingTransport{}
	runner := NewDevices(func(uuid.UUID) (any, bool) { return transport, true }, zap.NewNop())
	device := &models.MQTTDevice{ID: uuid.New(), Name: "Garage switch", Transport: TransportESPHome, EntityType: "switch", EntityKey: "garage"}

	flowActor := events.Actor{Kind: events.ActorFlow, ID: "f1", Name: "Close up", RunID: "r1", Via: events.ViaFlow}
	_, err := runner.Command(events.WithActor(context.Background(), flowActor), device, "state", true, nil, nil)
	require.NoError(t, err)
	readonly := &models.MQTTDevice{ID: uuid.New(), Name: "Garage sensor", Transport: TransportESPHome, EntityType: "sensor"}
	_, err = runner.Command(context.Background(), readonly, "state", true, nil, nil)
	require.Error(t, err)
	events.R.Stop()

	var rows []models.Event
	require.NoError(t, db.DB.Find(&rows).Error)
	require.Len(t, rows, 1, "only the sent command is logged")
	assert.Equal(t, 1, transport.sent)
	assert.Equal(t, "Garage switch: state set to on", rows[0].Message)
	assert.Equal(t, events.ActorFlow, rows[0].ActorKind)
	assert.Equal(t, "Close up", rows[0].ActorName)
	assert.Equal(t, "r1", rows[0].RunID)
	assert.Equal(t, device.ID.String(), rows[0].SubjectID)
}
