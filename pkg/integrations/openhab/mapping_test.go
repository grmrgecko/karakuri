package openhab

import (
	"testing"

	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func f(v float64) *float64 { return &v }

// TestMappingStates covers each item type's state as its device kind holds
// it, and NULL and UNDEF as no value.
func TestMappingStates(t *testing.T) {
	tests := []struct {
		item Item
		kind string
		want map[string]any
	}{
		{Item{Type: "Switch", State: "ON"}, controls.KindBoolean, map[string]any{"on": true}},
		{Item{Type: "Contact", State: "OPEN"}, controls.KindBoolean, map[string]any{"on": true}},
		{Item{Type: "Dimmer", State: "40"}, controls.KindLight, map[string]any{"on": true, "brightness": float64(40)}},
		{Item{Type: "Color", State: "120,100,50"}, controls.KindLight, map[string]any{"on": true, "brightness": float64(50), "color": "#00ff00"}},
		{Item{Type: "Rollershutter", State: "30"}, controls.KindCover, map[string]any{"state": "open", "position": float64(70)}},
		{Item{Type: "Rollershutter", State: "100"}, controls.KindCover, map[string]any{"state": "closed", "position": float64(0)}},
		{Item{Type: "Number:Temperature", State: "21.5 °C"}, controls.KindNumber, map[string]any{"value": 21.5}},
		{Item{Type: "String", State: "hello"}, controls.KindText, map[string]any{"value": "hello"}},
		{Item{Type: "DateTime", State: "2026-11-06T20:00:00.000+0000"}, controls.KindDateTime, map[string]any{"value": "2026-11-06T20:00:00Z"}},
		{Item{Type: "Player", State: "PLAY"}, controls.KindMedia, map[string]any{"state": "playing"}},
		{Item{Type: "Location", State: "41.88,-87.63,180"}, controls.KindLocation, map[string]any{"latitude": 41.88, "longitude": -87.63}},
		{Item{Type: "Group", GroupType: "Switch", State: "OFF"}, controls.KindBoolean, map[string]any{"on": false}},
		{Item{Type: "Switch", State: "NULL"}, controls.KindBoolean, nil},
		{Item{Type: "Dimmer", State: "UNDEF"}, controls.KindLight, nil},
	}
	for _, tc := range tests {
		t.Run(tc.item.Type+" "+tc.item.State, func(t *testing.T) {
			assert.Equal(t, tc.kind, Propose(tc.item).Kind)
			assert.Equal(t, tc.want, Values(tc.item))
		})
	}
}

// TestMappingOptions covers options from the state description and the
// unit, read-only items, and unsupported types.
func TestMappingOptions(t *testing.T) {
	vol := Propose(Item{Type: "Number", State: "10", StateDescription: &StateDescription{Minimum: f(0), Maximum: f(50), Step: f(5)}})
	assert.Equal(t, 0.0, vol.Options["min"])
	assert.Equal(t, 50.0, vol.Options["max"])
	assert.Equal(t, "slider", vol.Options["display"])
	assert.Equal(t, " °C", Propose(Item{Type: "Number:Temperature", State: "21.5 °C"}).Options["unit"])
	assert.Equal(t, " °C", Propose(Item{Type: "Number:Temperature", State: "NULL", UnitSymbol: "°C"}).Options["unit"],
		"the unit symbol gives the unit while the state is NULL")

	mode := Propose(Item{Type: "String", StateDescription: &StateDescription{Options: []Option{{Value: "day", Label: "Day"}}}})
	assert.Equal(t, controls.KindSelect, mode.Kind)
	assert.False(t, Propose(Item{Type: "Contact"}).Writable)
	assert.False(t, Propose(Item{Type: "Switch", StateDescription: &StateDescription{ReadOnly: true}}).Writable)
	assert.NotEmpty(t, Propose(Item{Type: "Image"}).Reason)
	assert.NotEmpty(t, Propose(Item{Type: "Group"}).Reason)

	// The kind's own checks hold a command to the description.
	_, err := controls.CoerceAttribute(vol.Kind, vol.Options, "value", 70.0)
	require.Error(t, err, "a volume past the maximum is refused before openHAB sees it")
	_, err = controls.CoerceAttribute(mode.Kind, mode.Options, "value", "night")
	require.Error(t, err, "a mode outside the options is refused")
}

// TestMappingCommands covers each write's openHAB command.
func TestMappingCommands(t *testing.T) {
	tests := []struct {
		item  Item
		attr  string
		value any
		want  string
	}{
		{Item{Type: "Switch"}, "on", true, "ON"},
		{Item{Type: "Dimmer"}, "brightness", 40.0, "40"},
		{Item{Type: "Dimmer"}, "on", false, "OFF"},
		{Item{Type: "Color", State: "0,0,30"}, "color", "#00ff00", "120,100,30"},
		{Item{Type: "Color", State: "0,0,0"}, "color", "#ff0000", "0,100,100"},
		// #ff0001 has a hue of 359.76. openHAB's HSBType takes 0 up to but
		// not including 360, so the hue wraps to 0 instead of rounding to 360.
		{Item{Type: "Color", State: "359.8,100,100"}, "color", "#ff0001", "0,100,100"},
		{Item{Type: "Color", State: "0,0,100"}, "color", "#ff0003", "359,100,100"},
		{Item{Type: "Color"}, "brightness", 55.0, "55"},
		{Item{Type: "Rollershutter"}, "position", 30.0, "70"},
		{Item{Type: "Rollershutter"}, "state", "open", "UP"},
		{Item{Type: "Rollershutter"}, "state", "stopped", "STOP"},
		{Item{Type: "Number"}, "value", 21.5, "21.5"},
		{Item{Type: "String"}, "value", "night", "night"},
		{Item{Type: "Player"}, "state", "paused", "PAUSE"},
		{Item{Type: "Player"}, "command", "next", "NEXT"},
		{Item{Type: "Group", GroupType: "Switch"}, "on", false, "OFF"},
	}
	for _, tc := range tests {
		got, err := Command(tc.item, tc.attr, tc.value)
		require.NoError(t, err, "%s %s %v", tc.item.Type, tc.attr, tc.value)
		assert.Equal(t, tc.want, got, "%s %s %v", tc.item.Type, tc.attr, tc.value)
	}
	_, err := Command(Item{Name: "T", Type: "Number", StateDescription: &StateDescription{ReadOnly: true}}, "value", 1.0)
	assert.EqualError(t, err, "openHAB marks T read only")
	_, err = Command(Item{Type: "Contact"}, "on", true)
	assert.Error(t, err)
}
