package zigbee2mqtt

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// retained is a subscriber holding retained messages by topic, delivered on
// subscribe as a broker does.
type retained map[string][]byte

func (r retained) Subscribe(filter string, _ byte, handler func(string, []byte)) (func(), error) {
	if payload, ok := r[filter]; ok {
		go handler(filter, payload)
	}
	return func() {}, nil
}

// TestReadDevices covers reading the retained list on the base topic given,
// with the default applied, and the error naming the topic when nothing is
// retained there.
func TestReadDevices(t *testing.T) {
	raw, err := os.ReadFile("testdata/synthetic.json")
	require.NoError(t, err)
	broker := retained{"zigbee2mqtt/bridge/devices": raw}

	devices, err := ReadDevices(context.Background(), broker, " /zigbee2mqtt/ ")
	require.NoError(t, err)
	assert.Len(t, devices, 9)

	devices, err = ReadDevices(context.Background(), broker, "")
	require.NoError(t, err, "an empty base topic is the default")
	assert.Len(t, devices, 9)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ReadDevices(ctx, broker, "nothing-here")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Zigbee2MQTT not found on nothing-here/bridge/devices")
}

// TestParseDevicesNumericEnum locks that an enum expose listing numbers as its
// values is read, with each value as its text. Zigbee2MQTT allows numbers
// there, and one such device made the whole list unreadable, so discovery
// found nothing and every added device stopped following renames.
func TestParseDevicesNumericEnum(t *testing.T) {
	devices, err := ParseDevices([]byte(`[
		{"ieee_address": "0x01", "friendly_name": "Plug", "type": "Router", "supported": true,
		 "interview_completed": true, "definition": {"model": "P1", "vendor": "V", "exposes": [
			{"type": "switch", "features": [{"type": "binary", "name": "state", "property": "state",
			 "access": 7, "value_on": "ON", "value_off": "OFF"}]}]}},
		{"ieee_address": "0x02", "friendly_name": "Fan", "type": "Router", "supported": true,
		 "interview_completed": true, "definition": {"model": "F1", "vendor": "V", "exposes": [
			{"type": "fan", "features": [
				{"type": "binary", "name": "state", "property": "fan_state", "access": 3, "value_on": "ON", "value_off": "OFF"},
				{"type": "enum", "name": "mode", "property": "fan_mode", "access": 3, "values": [1, 2.5, "auto"]}]}]}}
	]`))
	require.NoError(t, err, "one numeric enum must not make the list unreadable")
	require.Len(t, devices, 2)

	fan := Propose(devices[1])
	assert.Equal(t, []any{
		map[string]any{"value": "1", "label": "1"},
		map[string]any{"value": "2.5", "label": "2.5"},
		map[string]any{"value": "auto", "label": "auto"},
	}, fan.Options["preset_list"], "numeric values become the choices' text")
}
