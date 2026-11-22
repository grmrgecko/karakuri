package core

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// NumericTransformNode calibrates numeric readings and converts physical units.
type NumericTransformNode struct{}

func (NumericTransformNode) Meta() engine.NodeMeta {
	fields := []engine.FieldSpec{
		{ID: "source", Label: "Source property", Type: flow.FieldString, Default: "payload", Help: "Numeric message property, such as payload.temperature."},
		{ID: "target", Label: "Destination property", Type: flow.FieldString, Default: "payload", Help: "Write the result to payload, a nested payload property, or metadata."},
		{ID: "operation", Label: "Operation", Type: flow.FieldSelect, Default: "scale", Options: []engine.Option{{Value: "scale", Label: "Scale and offset"}, {Value: "map", Label: "Map range"}, {Value: "clamp", Label: "Clamp"}, {Value: "round", Label: "Round"}, {Value: "convert", Label: "Convert units"}}},
	}
	for _, f := range []struct {
		id, label, operation string
		value                float64
	}{
		{"factor", "Factor", "scale", 1}, {"offset", "Offset", "scale", 0},
		{"input_min", "Input start", "map", 0}, {"input_max", "Input end", "map", 100},
		{"output_min", "Output start", "map", 0}, {"output_max", "Output end", "map", 1},
		{"min", "Minimum", "clamp", 0}, {"max", "Maximum", "clamp", 100},
		{"decimals", "Decimal places", "round", 0},
	} {
		fields = append(fields, engine.FieldSpec{ID: f.id, Label: f.label, Type: flow.FieldNumber, Default: f.value, ShowIf: &flow.ShowIf{Field: "operation", Equals: f.operation}})
	}
	fields = append(fields,
		engine.FieldSpec{ID: "clamp", Label: "Clamp mapped result", Type: flow.FieldBoolean, Default: false, ShowIf: &flow.ShowIf{Field: "operation", Equals: "map"}, Help: "Keep the result between the output endpoints, including reversed ranges."},
		engine.FieldSpec{ID: "rounding", Label: "Rounding", Type: flow.FieldSelect, Default: "nearest", Options: []engine.Option{{Value: "nearest", Label: "Nearest, ties away from zero"}, {Value: "floor", Label: "Floor"}, {Value: "ceil", Label: "Ceiling"}, {Value: "truncate", Label: "Toward zero"}}, ShowIf: &flow.ShowIf{Field: "operation", Equals: "round"}, Help: "Decimal places must be an integer from -12 to 12. Negative places round tens, hundreds, and larger values."},
		engine.FieldSpec{ID: "from_unit", Label: "From unit", Type: flow.FieldSelect, Default: "celsius", Options: numericUnitOptions(), ShowIf: &flow.ShowIf{Field: "operation", Equals: "convert"}},
		engine.FieldSpec{ID: "to_unit", Label: "To unit", Type: flow.FieldSelect, Default: "fahrenheit", Options: numericUnitOptions(), ShowIf: &flow.ShowIf{Field: "operation", Equals: "convert"}},
	)
	return engine.NodeMeta{
		Type: "numeric-transform", Label: "Numeric transform", Folder: flow.FolderLogic, Icon: "las la-calculator",
		Description: "Scales, maps, clamps, rounds, or converts a numeric message property", Pure: true,
		Inputs: []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortNumber, TypeRule: &flow.PortTypeRule{PayloadPath: []string{"source"}}}},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortNumber, TypeFrom: "in", TypeRule: &flow.PortTypeRule{PayloadPath: []string{"target"}}},
			flow.ErrorPort(),
		},
		ConfigSpec: fields,
	}
}

// numericTransformConfig keeps absent numeric settings distinct from zero.
type numericTransformConfig struct {
	Source    string   `json:"source"`
	Target    string   `json:"target"`
	Operation string   `json:"operation"`
	Factor    *float64 `json:"factor"`
	Offset    *float64 `json:"offset"`
	InputMin  *float64 `json:"input_min"`
	InputMax  *float64 `json:"input_max"`
	OutputMin *float64 `json:"output_min"`
	OutputMax *float64 `json:"output_max"`
	Min       *float64 `json:"min"`
	Max       *float64 `json:"max"`
	Clamp     bool     `json:"clamp"`
	Decimals  float64  `json:"decimals"`
	Rounding  string   `json:"rounding"`
	FromUnit  string   `json:"from_unit"`
	ToUnit    string   `json:"to_unit"`
}

// numericSetting reads an optional finite number with its documented default.
func numericSetting(value *float64, fallback float64) (float64, error) {
	if value == nil {
		return fallback, nil
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) {
		return 0, fmt.Errorf("numeric settings must be finite")
	}
	return *value, nil
}

// Execute copies the message before writing a finite transformed result.
func (NumericTransformNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg numericTransformConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)
	source, target := normalizeProperty(cfg.Source), normalizeProperty(cfg.Target)
	if target != "payload" && !strings.HasPrefix(target, "payload.") && !strings.HasPrefix(target, "meta.") {
		return nil, fmt.Errorf("numeric destination must be payload, a payload property, or a metadata property")
	}
	value, found := resolveProperty(msg, source)
	if !found {
		return nil, fmt.Errorf("numeric property %q is not present on the message", source)
	}
	number, ok := toNumber(value)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
		return nil, fmt.Errorf("property %q must be a finite number", source)
	}
	result, err := cfg.transform(number)
	if err != nil {
		return nil, err
	}
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return nil, fmt.Errorf("numeric transform result is not finite")
	}
	out := passThrough(msg)
	if err := setProperty(out, target, result); err != nil {
		return nil, fmt.Errorf("writing numeric result to %q: %w", target, err)
	}
	return engine.One("out", out), nil
}

// transform applies only the selected operation's settings.
func (cfg numericTransformConfig) transform(value float64) (float64, error) {
	switch cfg.Operation {
	case "", "scale":
		factor, err := numericSetting(cfg.Factor, 1)
		if err != nil {
			return 0, err
		}
		offset, err := numericSetting(cfg.Offset, 0)
		if err != nil {
			return 0, err
		}
		return value*factor + offset, nil
	case "map", "clamp":
		lowerField, upperField, lowerDefault, upperDefault := cfg.InputMin, cfg.InputMax, 0.0, 100.0
		if cfg.Operation == "clamp" {
			lowerField, upperField = cfg.Min, cfg.Max
		}
		lower, err := numericSetting(lowerField, lowerDefault)
		if err != nil {
			return 0, err
		}
		upper, err := numericSetting(upperField, upperDefault)
		if err != nil {
			return 0, err
		}
		if cfg.Operation == "clamp" {
			if lower > upper {
				return 0, fmt.Errorf("clamp minimum must not exceed maximum")
			}
			return math.Max(lower, math.Min(upper, value)), nil
		}
		if lower == upper {
			return 0, fmt.Errorf("mapping input endpoints must differ")
		}
		outLower, err := numericSetting(cfg.OutputMin, 0)
		if err != nil {
			return 0, err
		}
		outUpper, err := numericSetting(cfg.OutputMax, 1)
		if err != nil {
			return 0, err
		}
		if cfg.Clamp {
			value = math.Max(math.Min(lower, upper), math.Min(math.Max(lower, upper), value))
		}
		width, outWidth := upper-lower, outUpper-outLower
		if math.IsInf(width, 0) || math.IsInf(outWidth, 0) {
			return 0, fmt.Errorf("mapping range is not finite")
		}
		return outLower + (value-lower)/width*outWidth, nil
	case "round":
		if math.IsNaN(cfg.Decimals) || math.IsInf(cfg.Decimals, 0) || cfg.Decimals != math.Trunc(cfg.Decimals) || cfg.Decimals < -12 || cfg.Decimals > 12 {
			return 0, fmt.Errorf("decimal places must be an integer from -12 to 12")
		}
		// Tens and larger places divide by a whole power of ten, which float64
		// holds exactly; the fractional scale 1e-05 is not exact, and dividing
		// by it turned 100000 into 99999.99999999999.
		places := int(cfg.Decimals)
		whole := places < 0
		if whole {
			places = -places
		}
		scale := math.Pow10(places)
		scaled := value * scale
		if whole {
			scaled = value / scale
		}
		if math.IsInf(scaled, 0) {
			return 0, fmt.Errorf("scaled rounding value is not finite")
		}
		switch cfg.Rounding {
		case "", "nearest":
			scaled = math.Round(scaled)
		case "floor":
			scaled = math.Floor(scaled)
		case "ceil":
			scaled = math.Ceil(scaled)
		case "truncate":
			scaled = math.Trunc(scaled)
		default:
			return 0, fmt.Errorf("unknown rounding mode %q", cfg.Rounding)
		}
		if whole {
			return scaled * scale, nil
		}
		return scaled / scale, nil
	case "convert":
		from, to := cfg.FromUnit, cfg.ToUnit
		if from == "" {
			from = "celsius"
		}
		if to == "" {
			to = "fahrenheit"
		}
		var source, target *numericUnit
		for i := range numericUnits {
			u := &numericUnits[i]
			if u.id == from {
				source = u
			}
			if u.id == to {
				target = u
			}
		}
		if source == nil || target == nil || source.dimension != target.dimension {
			return 0, fmt.Errorf("units %q and %q must exist and measure the same dimension", from, to)
		}
		// Combine scales before multiplication to avoid overflowing the base
		// value when the converted value is still representable.
		return value*(source.scale/target.scale) + (source.offset-target.offset)/target.scale, nil
	default:
		return 0, fmt.Errorf("unknown numeric operation %q", cfg.Operation)
	}
}

// numericUnit maps a unit to its dimension's base value using scale and offset.
type numericUnit struct {
	id, label, dimension string
	scale, offset        float64
}

// numericUnits uses Celsius, meters, meters/second, pascals, watts, joules and
// radians as base units. Celsius avoids subtracting Kelvin offsets for ordinary
// Fahrenheit conversions. International inch/mile definitions are exact.
var numericUnits = []numericUnit{
	{"celsius", "Celsius", "temperature", 1, 0}, {"fahrenheit", "Fahrenheit", "temperature", 5.0 / 9, -32 * 5.0 / 9}, {"kelvin", "Kelvin", "temperature", 1, -273.15},
	{"meter", "Meter", "length", 1, 0}, {"centimeter", "Centimeter", "length", .01, 0}, {"millimeter", "Millimeter", "length", .001, 0}, {"kilometer", "Kilometer", "length", 1000, 0},
	{"inch", "Inch", "length", .0254, 0}, {"foot", "Foot", "length", .3048, 0}, {"yard", "Yard", "length", .9144, 0}, {"mile", "Mile", "length", 1609.344, 0},
	{"meter_per_second", "Meters per second", "speed", 1, 0}, {"kilometer_per_hour", "Kilometers per hour", "speed", 1.0 / 3.6, 0}, {"mile_per_hour", "Miles per hour", "speed", .44704, 0}, {"knot", "Knots", "speed", 1852.0 / 3600, 0},
	{"pascal", "Pascal", "pressure", 1, 0}, {"kilopascal", "Kilopascal", "pressure", 1000, 0}, {"hectopascal", "Hectopascal", "pressure", 100, 0}, {"bar", "Bar", "pressure", 100000, 0}, {"psi", "Pounds per square inch", "pressure", 6894.757293168, 0},
	{"watt", "Watt", "power", 1, 0}, {"kilowatt", "Kilowatt", "power", 1000, 0}, {"megawatt", "Megawatt", "power", 1000000, 0},
	{"joule", "Joule", "energy", 1, 0}, {"kilojoule", "Kilojoule", "energy", 1000, 0}, {"watt_hour", "Watt hour", "energy", 3600, 0}, {"kilowatt_hour", "Kilowatt hour", "energy", 3600000, 0}, {"megawatt_hour", "Megawatt hour", "energy", 3600000000, 0},
	{"radian", "Radian", "angle", 1, 0}, {"degree", "Degree", "angle", math.Pi / 180, 0},
}

// numericUnitOptions keeps catalog labels and execution unit IDs together.
func numericUnitOptions() []engine.Option {
	out := make([]engine.Option, 0, len(numericUnits))
	for _, unit := range numericUnits {
		out = append(out, engine.Option{Value: unit.id, Label: unit.label})
	}
	return out
}
