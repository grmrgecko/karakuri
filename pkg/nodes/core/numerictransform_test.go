package core

import (
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNumericTransformBoundaries pins reversed ranges, affine temperature
// conversion and physical unit constants rather than individual arithmetic helpers.
func TestNumericTransformBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		value, want  float64
		invalid      bool
	}{
		{"calibration", `{"factor":2,"offset":-1}`, 3, 5, false},
		{"reversed input", `{"operation":"map","input_min":100,"input_max":0,"output_min":0,"output_max":1}`, 25, .75, false},
		{"reversed output clamp", `{"operation":"map","input_min":0,"input_max":100,"output_min":10,"output_max":0,"clamp":true}`, 150, 0, false},
		{"zero width", `{"operation":"map","input_min":1,"input_max":1}`, 1, 0, true},
		{"negative nearest", `{"operation":"round","decimals":1}`, -1.25, -1.3, false},
		{"round tens", `{"operation":"round","decimals":-1,"rounding":"floor"}`, -11, -20, false},
		{"Celsius freezing", `{"operation":"convert","from_unit":"celsius","to_unit":"fahrenheit"}`, 0, 32, false},
		{"Fahrenheit boiling", `{"operation":"convert","from_unit":"fahrenheit","to_unit":"celsius"}`, 212, 100, false},
		{"Kelvin zero", `{"operation":"convert","from_unit":"kelvin","to_unit":"celsius"}`, 0, -273.15, false},
		{"mile length", `{"operation":"convert","from_unit":"mile","to_unit":"meter"}`, 1, 1609.344, false},
		{"energy", `{"operation":"convert","from_unit":"kilowatt_hour","to_unit":"joule"}`, 1, 3600000, false},
		{"large equivalent units", `{"operation":"convert","from_unit":"megawatt","to_unit":"megawatt"}`, 1e308, 1e308, false},
		{"incompatible", `{"operation":"convert","from_unit":"watt","to_unit":"meter"}`, 1, 0, true},
		{"overflow", `{"factor":1e308}`, 1e308, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := &engine.Msg{Payload: tc.value, Topic: "sensor", Meta: map[string]any{"request_id": "r"}}
			out, err := (NumericTransformNode{}).Execute(t.Context(), newTestCtx(tc.config, nil), map[string]*engine.Msg{"in": in})
			if tc.invalid {
				assert.Error(t, err, "undefined numeric operations must not emit successful readings")
				assert.Empty(t, out)
				return
			}
			require.NoError(t, err)
			msg := only(out, "out")
			require.NotNil(t, msg)
			assert.InDelta(t, tc.want, msg.Payload, 1e-8, "the transformed physical value must match %s", tc.name)
			assert.Equal(t, in.Topic, msg.Topic)
			assert.Equal(t, in.Meta, msg.Meta)
			assert.Equal(t, tc.value, in.Payload, "the input remains the recorded observation")
		})
	}
}

// TestNumericTransformPaths checks the message boundary and matching catalog
// types when a reading is nested or written into metadata.
func TestNumericTransformPaths(t *testing.T) {
	for _, tc := range []struct {
		target, inputType, outputType string
	}{
		{"payload", flow.PortObject, flow.PortNumber},
		{"payload.converted", flow.PortObject, flow.PortObject},
		{"meta.converted", flow.PortObject, flow.PortAny},
	} {
		cfg := `{"source":"payload.reading","target":"` + tc.target + `","factor":2}`
		ec := newTestCtx(cfg, nil).usePorts((NumericTransformNode{}).Meta(), cfg)
		in := &engine.Msg{Payload: map[string]any{"reading": 5.0, "unit": "V"}, Meta: map[string]any{"request_id": "r"}, Group: []engine.GroupFrame{{ID: "g", Count: 2}}}
		out, err := (NumericTransformNode{}).Execute(t.Context(), ec, map[string]*engine.Msg{"in": in})
		require.NoError(t, err)
		msg := only(out, "out")
		got, found := resolveProperty(msg, tc.target)
		assert.True(t, found)
		assert.Equal(t, 10.0, got)
		assert.Equal(t, in.Group, msg.Group)
		assert.Equal(t, map[string]any{"reading": 5.0, "unit": "V"}, in.Payload)
		ports := resolvePorts(t, (NumericTransformNode{}).Meta(), cfg)
		input, _ := ports.Input("in")
		output, _ := ports.Output("out")
		assert.Equal(t, tc.inputType, input.Type)
		assert.Equal(t, tc.outputType, output.Type)
	}
}

// TestNumericTransformRoundsWholePlacesExactly locks that rounding to tens,
// hundreds, and larger places yields a whole number. The result was divided by
// a fractional scale such as 1e-05, which float64 cannot hold exactly, so
// 123456 rounded to the hundred-thousands came out 99999.99999999999 and
// printed that way in a notification or an equality rule. Whole powers of ten
// are exact, so the comparison here is exact too.
func TestNumericTransformRoundsWholePlacesExactly(t *testing.T) {
	for _, tc := range []struct {
		config      string
		value, want float64
	}{
		{`{"operation":"round","decimals":-5}`, 123456, 100000},
		{`{"operation":"round","decimals":-5}`, 12345678, 12300000},
		{`{"operation":"round","decimals":-9,"rounding":"floor"}`, 1000000007, 1000000000},
		{`{"operation":"round","decimals":-1,"rounding":"ceil"}`, -11, -10},
		{`{"operation":"round","decimals":1}`, 2.25, 2.3},
	} {
		out, err := (NumericTransformNode{}).Execute(t.Context(), newTestCtx(tc.config, nil),
			map[string]*engine.Msg{"in": {Payload: tc.value}})
		require.NoErrorf(t, err, "rounding %v with %s must succeed", tc.value, tc.config)
		assert.Equalf(t, tc.want, only(out, "out").Payload, "rounding %v with %s", tc.value, tc.config)
	}
}
