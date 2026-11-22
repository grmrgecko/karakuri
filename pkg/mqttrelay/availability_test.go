package mqttrelay

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// availabilityRunner returns a runner on a fixed clock and one running device
// built from model, started the way start starts it minus the broker.
func availabilityRunner(t *testing.T, model models.MQTTDevice, w StatusWatcher) (*Devices, *device, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	runner := NewDevices(func(uuid.UUID) (any, bool) { return nil, false }, zap.NewNop())
	runner.clock = func() time.Time { return now }
	if w != nil {
		runner.SetStatusWatcher(w)
	}
	if model.ID == uuid.Nil {
		model.ID = uuid.New()
	}
	if model.Name == "" {
		model.Name = "sensor"
	}
	dev := &device{model: model, state: map[string]any{}}
	runner.initialAvailability(dev)
	runner.running[model.ID] = dev
	return runner, dev, &now
}

// fakeStatus is a connection status source a test drives by hand.
type fakeStatus struct {
	state   string
	updates chan integrations.Status
}

func (f *fakeStatus) WatchStatus(uuid.UUID) (integrations.Status, bool, <-chan integrations.Status, func()) {
	return integrations.Status{State: f.state}, true, f.updates, func() {}
}

// TestMatchAvailability pins the payload forms: the configured strings, and a
// JSON object whose state is one of them, which Zigbee2MQTT publishes since
// 1.30. Matching is exact; anything else is neither.
func TestMatchAvailability(t *testing.T) {
	tests := []struct {
		payload, online, offline, want string
	}{
		{"online", "online", "offline", AvailabilityOnline},
		{"offline", "online", "offline", AvailabilityOffline},
		{` {"state":"online"} `, "online", "offline", AvailabilityOnline},
		{`{"state":"offline"}`, "online", "offline", AvailabilityOffline},
		{"Online", "Online", "Offline", AvailabilityOnline},
		{"ONLINE", "online", "offline", ""},
		{`{"state":"asleep"}`, "online", "offline", ""},
		{"garbage", "online", "offline", ""},
	}
	for _, tc := range tests {
		t.Run(tc.payload, func(t *testing.T) {
			assert.Equal(t, tc.want, matchAvailability([]byte(tc.payload), tc.online, tc.offline))
		})
	}
}

// TestAvailabilityTopic covers a device with an availability topic: unknown
// until the topic speaks, then whatever it says, with an unrecognised payload
// kept as a note and changing nothing. Each change reaches the watchers once.
func TestAvailabilityTopic(t *testing.T) {
	runner, dev, _ := availabilityRunner(t, models.MQTTDevice{AvailabilityTopic: "z/sensor/availability",
		AvailabilityOnline: "online", AvailabilityOffline: "offline"}, nil)
	var changes []AvailabilityChange
	runner.WatchAvailability(func(c AvailabilityChange) { changes = append(changes, c) })

	status, _ := runner.Status(dev.model.ID)
	assert.Equal(t, AvailabilityUnknown, status.Availability, "a topic device waits for its topic")

	runner.receiveAvailability(dev, []byte(`{"state":"online"}`))
	runner.receiveAvailability(dev, []byte("sleeping"))
	status, _ = runner.Status(dev.model.ID)
	assert.Equal(t, AvailabilityOnline, status.Availability)
	assert.Equal(t, "sleeping", status.AvailabilityNote)

	runner.receiveAvailability(dev, []byte("offline"))
	status, _ = runner.Status(dev.model.ID)
	assert.Equal(t, AvailabilityOffline, status.Availability)
	assert.Equal(t, SourceTopic, status.AvailabilitySource)

	runner.receive(dev, "z/sensor", []byte(`{}`), false)
	status, _ = runner.Status(dev.model.ID)
	assert.Equal(t, AvailabilityOffline, status.Availability, "a state message does not overrule the topic")

	require.Len(t, changes, 2)
	assert.Equal(t, AvailabilityUnknown, changes[0].Previous)
	assert.Equal(t, AvailabilityOffline, changes[1].Current)
}

// TestAvailabilityInterval covers the expected reporting interval: quiet past
// it is offline, the next message is online again, and the clock starts at
// the device's start rather than at a message that never came.
func TestAvailabilityInterval(t *testing.T) {
	runner, dev, now := availabilityRunner(t, models.MQTTDevice{ExpectEveryS: 60}, nil)
	start := *now

	runner.sweepAvailability(start.Add(59 * time.Second))
	status, _ := runner.Status(dev.model.ID)
	assert.Equal(t, AvailabilityOnline, status.Availability, "inside the interval")

	runner.sweepAvailability(start.Add(61 * time.Second))
	status, _ = runner.Status(dev.model.ID)
	assert.Equal(t, AvailabilityOffline, status.Availability, "past the interval with nothing heard")
	assert.Equal(t, SourceInterval, status.AvailabilitySource)

	runner.receive(dev, "t", []byte(`{}`), false)
	status, _ = runner.Status(dev.model.ID)
	assert.Equal(t, AvailabilityOnline, status.Availability, "a message brings it back")
}

// TestAvailabilityConnection covers the broker going down and coming back: every
// device on it is unknown while it is down; a device with no topic is online
// again when it returns, and one with a topic waits for its topic.
func TestAvailabilityConnection(t *testing.T) {
	status := &fakeStatus{state: integrations.StateConnected, updates: make(chan integrations.Status, 4)}
	conn := uuid.New()
	runner, plain, _ := availabilityRunner(t, models.MQTTDevice{ConnectionID: conn}, status)
	topicDev := &device{model: models.MQTTDevice{ID: uuid.New(), Name: "lwt", ConnectionID: conn, AvailabilityTopic: "lwt"}, state: map[string]any{}}
	runner.initialAvailability(topicDev)
	runner.running[topicDev.model.ID] = topicDev
	runner.receiveAvailability(topicDev, []byte("online"))

	read := func(dev *device) string {
		s, _ := runner.Status(dev.model.ID)
		return s.Availability
	}
	assert.Equal(t, AvailabilityOnline, read(plain))
	assert.Equal(t, AvailabilityOnline, read(topicDev))

	runner.connectionChanged(conn, false)
	assert.Equal(t, AvailabilityUnknown, read(plain))
	assert.Equal(t, AvailabilityUnknown, read(topicDev))

	runner.connectionChanged(conn, true)
	assert.Equal(t, AvailabilityOnline, read(plain), "no topic: online with the connection")
	assert.Equal(t, AvailabilityUnknown, read(topicDev), "a topic device waits for its retained message")
}

// TestAvailabilitySurvivesRestart covers persistence: an offline device comes
// back offline and marked stale, and the next availability message confirms it.
func TestAvailabilitySurvivesRestart(t *testing.T) {
	runner, dev := stateTestDevice(t, "boolean")
	dev.model.AvailabilityTopic = "home/plug/availability"
	runner.availWatchers = map[int]func(AvailabilityChange){}
	runner.connUp = map[uuid.UUID]bool{}
	runner.receiveAvailability(dev, []byte("offline"))
	runner.saveDirty()

	restored := restart(runner, dev.model)
	runner.initialAvailability(restored)
	status, _ := runner.Status(dev.model.ID)
	assert.Equal(t, AvailabilityOffline, status.Availability, "an offline device stays offline across a restart")
	assert.True(t, status.AvailabilityStale)

	runner.receiveAvailability(restored, []byte("offline"))
	status, _ = runner.Status(dev.model.ID)
	assert.False(t, status.AvailabilityStale, "the topic confirms it")
}

// TestRestoredOutageDoesNotOutliveIt locks that an availability stored while
// the connection was down is not kept once the connection is up. A device
// that restarted with it after its connection had already connected saw no
// transition to clear it, and stayed unknown however many messages arrived:
// a message never overrules an outage the connection itself reports.
func TestRestoredOutageDoesNotOutliveIt(t *testing.T) {
	tests := []struct {
		name  string
		model models.MQTTDevice
		want  string
	}{
		{name: "no topic", model: models.MQTTDevice{}, want: AvailabilityOnline},
		{name: "topic", model: models.MQTTDevice{AvailabilityTopic: "lwt"}, want: AvailabilityUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &fakeStatus{state: integrations.StateConnected, updates: make(chan integrations.Status, 1)}
			runner := NewDevices(func(uuid.UUID) (any, bool) { return nil, false }, zap.NewNop())
			runner.SetStatusWatcher(status)
			tt.model.ID, tt.model.Name, tt.model.ConnectionID = uuid.New(), "sensor", uuid.New()
			dev := &device{model: tt.model, state: map[string]any{},
				availability: AvailabilityUnknown, availabilitySource: SourceConnection, availabilityStale: true}
			runner.initialAvailability(dev)
			runner.running[tt.model.ID] = dev

			runner.receive(dev, "t", []byte(`{}`), false)
			got, _ := runner.Status(tt.model.ID)
			assert.Equal(t, tt.want, got.Availability, "the outage ended before the device started")
			assert.NotEqual(t, SourceConnection, got.AvailabilitySource, "the connection is up and says nothing")
			assert.False(t, got.AvailabilityStale, "a value worked out at start is not a restored one")
		})
	}
}

// TestCommandRefusedWhileOffline covers the refusal and its override.
func TestCommandRefusedWhileOffline(t *testing.T) {
	runner, dev, _ := availabilityRunner(t, models.MQTTDevice{Name: "Hallway motion", AvailabilityTopic: "a"}, nil)
	runner.receiveAvailability(dev, []byte("offline"))

	err := runner.PublishCommand(context.Background(), &dev.model, CommandMessage{Topic: "x", Payload: "1"})
	var offline *OfflineError
	require.True(t, errors.As(err, &offline), "an offline device refuses: %v", err)
	assert.Contains(t, err.Error(), `device "Hallway motion" is offline since 2026-10-31T12:00:00Z`)

	model := dev.model
	model.OfflineCommands = true
	err = runner.PublishCommand(context.Background(), &model, CommandMessage{Topic: "x", Payload: "1"})
	assert.False(t, errors.As(err, &offline), "allowed while offline, the command goes on to the broker")
}

// TestAvailabilityFollowsRemovedRule locks that an availability decided by a
// rule the device no longer has does not outlive the rule. A device taken
// offline by its expected interval stayed offline after the interval was
// removed, and one taken offline by its availability topic stayed offline
// after the topic was removed, until the device next spoke. Commands to it
// were refused as offline meanwhile, although a device with neither rule is
// online whenever its connection is up.
func TestAvailabilityFollowsRemovedRule(t *testing.T) {
	t.Run("interval removed while running", func(t *testing.T) {
		runner, dev := stateTestDevice(t, "boolean")
		dev.model.ExpectEveryS = 60
		require.NoError(t, models.SaveMQTTDevice(&dev.model))
		runner.initialAvailability(dev)
		runner.sweepAvailability(time.Now().Add(2 * time.Minute))
		status, _ := runner.Status(dev.model.ID)
		require.Equal(t, AvailabilityOffline, status.Availability, "quiet past the interval is offline")

		edited := dev.model
		edited.ExpectEveryS = 0
		require.NoError(t, models.SaveMQTTDevice(&edited))
		runner.Reload()

		status, _ = runner.Status(dev.model.ID)
		assert.Equal(t, AvailabilityOnline, status.Availability, "no rule is left to hold it offline")
		assert.Equal(t, SourceDefault, status.AvailabilitySource)
	})

	t.Run("interval removed before a restart", func(t *testing.T) {
		runner, dev := stateTestDevice(t, "boolean")
		dev.model.ExpectEveryS = 60
		runner.initialAvailability(dev)
		runner.sweepAvailability(time.Now().Add(2 * time.Minute))
		runner.saveDirty()

		edited := dev.model
		edited.ExpectEveryS = 0
		restored := restart(runner, edited)
		runner.initialAvailability(restored)

		status, _ := runner.Status(dev.model.ID)
		assert.Equal(t, AvailabilityOnline, status.Availability, "the stored offline came from an interval that is gone")
		assert.False(t, status.AvailabilityStale, "a value worked out at start is not a restored one")
	})

	t.Run("topic removed", func(t *testing.T) {
		runner, dev := stateTestDevice(t, "boolean")
		dev.model.AvailabilityTopic = "home/plug/availability"
		runner.initialAvailability(dev)
		runner.receiveAvailability(dev, []byte("offline"))
		runner.saveDirty()

		edited := dev.model
		edited.AvailabilityTopic = ""
		restored := restart(runner, edited)
		runner.initialAvailability(restored)

		status, _ := runner.Status(dev.model.ID)
		assert.Equal(t, AvailabilityOnline, status.Availability, "the stored offline came from a topic that is gone")
		assert.Equal(t, SourceDefault, status.AvailabilitySource)
	})
}
