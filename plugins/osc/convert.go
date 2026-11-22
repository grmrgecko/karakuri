package main

import (
	"encoding/base64"
	"fmt"
	"math"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

// Number formats for outgoing arguments: how a message payload number becomes
// a typed OSC argument. OSC receivers are picky about argument type tags, so
// the node declares the conversion instead of every number guessing alone.
const (
	numberAuto   = "auto"
	numberFloat  = "float"
	numberDouble = "double"
	numberInt    = "int"
)

// fromOSCArgs converts received OSC arguments into message payload values:
// integers widen to int64, floats to float64, and time tags become RFC3339
// strings. Blobs become the base64 text scripts already read binary as.
// Booleans, strings, and nil pass through.
func fromOSCArgs(args []any) []any {
	out := make([]any, 0, len(args))
	for _, a := range args {
		switch v := a.(type) {
		case int32:
			out = append(out, int64(v))
		case float32:
			out = append(out, float64(v))
		case []byte:
			out = append(out, base64.StdEncoding.EncodeToString(v))
		case osc.Timetag:
			out = append(out, v.Time().Format(time.RFC3339Nano))
		default:
			out = append(out, v)
		}
	}
	return out
}

// toOSCArgs converts a message payload into OSC arguments: a list sends one
// argument per element, any other value sends a single argument, and nil
// sends none; an address-only message is OSC's bang.
func toOSCArgs(payload any, numberFormat string) ([]any, error) {
	if payload == nil {
		return nil, nil
	}
	list, ok := payload.([]any)
	if !ok {
		list = []any{payload}
	}
	out := make([]any, 0, len(list))
	for i, v := range list {
		arg, err := toOSCArg(v, numberFormat)
		if err != nil {
			return nil, fmt.Errorf("argument %d: %w", i, err)
		}
		out = append(out, arg)
	}
	return out, nil
}

// toOSCArg converts one payload value into a typed OSC argument.
func toOSCArg(v any, numberFormat string) (any, error) {
	switch t := v.(type) {
	case nil, bool, string:
		return t, nil
	case []byte:
		// A binary payload sends as an OSC blob.
		return t, nil
	case float64:
		return convertNumber(t, numberFormat)
	case float32:
		return convertNumber(float64(t), numberFormat)
	case int:
		return convertInt(int64(t), numberFormat), nil
	case int32:
		return convertInt(int64(t), numberFormat), nil
	case int64:
		return convertInt(t, numberFormat), nil
	default:
		return nil, fmt.Errorf("cannot send a %T as an OSC argument; arguments are numbers, strings, booleans, or null", v)
	}
}

// convertNumber applies the configured number format to a float value.
func convertNumber(f float64, numberFormat string) (any, error) {
	switch numberFormat {
	case numberFloat:
		return float32(f), nil
	case numberDouble:
		return f, nil
	case numberInt:
		if f != math.Trunc(f) {
			return nil, fmt.Errorf("%v is not an integer", f)
		}
		return convertInt(int64(f), numberFormat), nil
	default: // numberAuto
		if f == math.Trunc(f) && f >= math.MinInt32 && f <= math.MaxInt32 {
			return int32(f), nil
		}
		return float32(f), nil
	}
}

// convertInt applies the configured number format to an already-integral
// value, keeping int32 where it fits since that is OSC's standard integer.
func convertInt(n int64, numberFormat string) any {
	switch numberFormat {
	case numberFloat:
		return float32(n)
	case numberDouble:
		return float64(n)
	default: // numberInt, numberAuto
		if n >= math.MinInt32 && n <= math.MaxInt32 {
			return int32(n)
		}
		return n
	}
}
