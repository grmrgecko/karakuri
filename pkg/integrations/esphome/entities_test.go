package esphome

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/mycontroller-org/esphome_api/pkg/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// TestNumberEntityKeepsDecimalBounds locks how the float32 values of the
// ESPHome native API become float64. Widening a float32 directly exposes its
// representation error: 0.1 becomes 0.10000000149011612 and 0.7 becomes
// 0.699999988079071. As bounds, those refuse the very values the node
// declared, and as readings they show up on dashboards with 15 digits.
func TestNumberEntityKeepsDecimalBounds(t *testing.T) {
	e, ok := entityFrom(&api.ListEntitiesNumberResponse{
		ObjectId: "gain", Key: 1, MinValue: 0.1, MaxValue: 0.7, Step: 0.1,
	})
	require.True(t, ok, "a number entity is one the mapping knows")

	opts := e.Propose().Options
	assert.Equal(t, 0.1, opts["min"], "the minimum is the decimal the node declared")
	assert.Equal(t, 0.7, opts["max"], "the maximum is the decimal the node declared")
	for _, v := range []float64{0.1, 0.7} {
		_, err := controls.CoerceAttribute(controls.KindNumber, opts, "value", v)
		assert.NoErrorf(t, err, "%v is inside the declared range", v)
	}

	values, ok := e.Values(&api.NumberStateResponse{Key: 1, State: 0.7})
	require.True(t, ok, "a number state carries a value")
	assert.Equal(t, 0.7, values["value"], "the reading is the decimal the node sent")
}

// TestClimateStateOmitsMissingTemperatures locks that a thermostat whose
// sensor has no reading yet still reports its mode. ESPHome sends NaN for a
// temperature it does not have, and one NaN in the values makes the whole map
// impossible to encode, so nothing about the device was published at all.
func TestClimateStateOmitsMissingTemperatures(t *testing.T) {
	e, ok := entityFrom(&api.ListEntitiesClimateResponse{
		ObjectId: "thermostat", Key: 2, SupportsCurrentTemperature: true,
	})
	require.True(t, ok, "a climate entity is one the mapping knows")

	values, ok := e.Values(&api.ClimateStateResponse{
		Key: 2, Mode: api.ClimateMode_CLIMATE_MODE_HEAT,
		CurrentTemperature: float32(math.NaN()), TargetTemperature: 21.5,
	})
	require.True(t, ok, "a climate state carries values")
	assert.NotContains(t, values, "current", "a temperature with no reading is left out")
	assert.Equal(t, 21.5, values["target"], "the setpoint the node does have is kept")
	assert.Equal(t, "heat", values["mode"], "the mode is reported either way")
	_, err := json.Marshal(values)
	assert.NoError(t, err, "the values must be publishable")
}

// TestStatesOmitNonFiniteReadings locks that a reading JSON cannot carry never
// reaches a device's state. A template sensor dividing by zero reports an
// infinity, and a number can report NaN without flagging its state missing;
// either one made the device's whole state impossible to store or publish.
func TestStatesOmitNonFiniteReadings(t *testing.T) {
	inf := float32(math.Inf(1))
	tests := []struct {
		name  string
		info  proto.Message
		state proto.Message
	}{
		{
			name:  "sensor at positive infinity",
			info:  &api.ListEntitiesSensorResponse{ObjectId: "ratio", Key: 3},
			state: &api.SensorStateResponse{Key: 3, State: inf},
		},
		{
			name:  "sensor at negative infinity",
			info:  &api.ListEntitiesSensorResponse{ObjectId: "ratio", Key: 3},
			state: &api.SensorStateResponse{Key: 3, State: float32(math.Inf(-1))},
		},
		{
			name:  "number at NaN without the missing flag",
			info:  &api.ListEntitiesNumberResponse{ObjectId: "gain", Key: 4},
			state: &api.NumberStateResponse{Key: 4, State: float32(math.NaN())},
		},
		{
			name:  "thermostat with an infinite setpoint",
			info:  &api.ListEntitiesClimateResponse{ObjectId: "thermostat", Key: 5},
			state: &api.ClimateStateResponse{Key: 5, TargetTemperature: inf, CurrentTemperature: 20},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, ok := entityFrom(tc.info)
			require.True(t, ok, "the entity is one the mapping knows")
			values, _ := e.Values(tc.state)
			_, err := json.Marshal(values)
			assert.NoError(t, err, "a state the node reports must stay publishable, with the unusable reading left out")
		})
	}
}

// TestSensorKeepsZeroDecimalPrecision locks that a sensor the node declares
// with no decimal places is shown with none. Zero used to read as no
// precision at all, so a carbon dioxide reading of 612.4 ppm was drawn with
// its decimals where the node asked for 612.
func TestSensorKeepsZeroDecimalPrecision(t *testing.T) {
	e, ok := entityFrom(&api.ListEntitiesSensorResponse{ObjectId: "co2", Key: 6, UnitOfMeasurement: "ppm", AccuracyDecimals: 0})
	require.True(t, ok, "a sensor entity is one the mapping knows")
	assert.Equal(t, float64(0), e.Propose().Options["precision"],
		"zero decimal places is a precision the node declared, not a missing one")
}
