package esphome

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/mycontroller-org/esphome_api/pkg/api"
)

// argTypeName renders an action argument type: bool, int, float, string, or
// one of those followed by [] for an array.
func argTypeName(t api.ServiceArgType) string {
	name := strings.ToLower(strings.TrimPrefix(api.ServiceArgType_name[int32(t)], "SERVICE_ARG_TYPE_"))
	if base, ok := strings.CutSuffix(name, "_array"); ok {
		return base + "[]"
	}
	return name
}

// CallAction runs one of the node's user-defined actions. Arguments are
// checked against the action's declared types before anything is sent.
func (d *Driver) CallAction(ctx context.Context, name string, args map[string]any) error {
	d.mu.Lock()
	c := d.client
	action, ok := d.actions[name]
	node := d.nodeLabel(d.info.Name)
	d.mu.Unlock()
	if c == nil {
		return fmt.Errorf("ESPHome node %s is not connected", node)
	}
	if !ok {
		return fmt.Errorf("ESPHome node %s has no action %q", node, name)
	}
	req, err := buildExecute(action, args)
	if err != nil {
		return err
	}
	return c.Send(req)
}

// buildExecute checks arguments and builds the request, in declared order.
func buildExecute(action Action, args map[string]any) (*api.ExecuteServiceRequest, error) {
	declared := map[string]bool{}
	req := &api.ExecuteServiceRequest{Key: action.key}
	for _, a := range action.Args {
		declared[a.Name] = true
		raw, ok := args[a.Name]
		if !ok {
			return nil, fmt.Errorf("action %q needs argument %q (%s)", action.Name, a.Name, a.Type)
		}
		arg, err := convertArg(a, raw)
		if err != nil {
			return nil, fmt.Errorf("action %q argument %q: %w", action.Name, a.Name, err)
		}
		req.Args = append(req.Args, arg)
	}
	for name := range args {
		if !declared[name] {
			return nil, fmt.Errorf("action %q has no argument %q", action.Name, name)
		}
	}
	return req, nil
}

// convertArg holds one argument to its declared type.
func convertArg(a ActionArg, raw any) (*api.ExecuteServiceArgument, error) {
	out := &api.ExecuteServiceArgument{}
	if base, isArray := strings.CutSuffix(a.Type, "[]"); isArray {
		list, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("want a list of %s", base)
		}
		for i, item := range list {
			switch base {
			case "bool":
				b, ok := item.(bool)
				if !ok {
					return nil, fmt.Errorf("item %d is not a bool", i)
				}
				out.BoolArray = append(out.BoolArray, b)
			case "int":
				n, err := wholeNumber(item)
				if err != nil {
					return nil, fmt.Errorf("item %d: %w", i, err)
				}
				out.IntArray = append(out.IntArray, n)
			case "float":
				f, ok := toFloat(item)
				if !ok {
					return nil, fmt.Errorf("item %d is not a number", i)
				}
				out.FloatArray = append(out.FloatArray, float32(f))
			case "string":
				s, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("item %d is not a string", i)
				}
				out.StringArray = append(out.StringArray, s)
			}
		}
		return out, nil
	}
	switch a.Type {
	case "bool":
		b, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("want a bool, got %v", raw)
		}
		out.Bool_ = b
	case "int":
		n, err := wholeNumber(raw)
		if err != nil {
			return nil, err
		}
		out.Int_, out.LegacyInt = n, n
	case "float":
		f, ok := toFloat(raw)
		if !ok {
			return nil, fmt.Errorf("want a number, got %v", raw)
		}
		out.Float_ = float32(f)
	case "string":
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("want a string, got %v", raw)
		}
		out.String_ = s
	default:
		return nil, fmt.Errorf("unknown argument type %s", a.Type)
	}
	return out, nil
}

// toFloat reads a JSON number.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// wholeNumber reads a JSON number that must be an int32.
func wholeNumber(v any) (int32, error) {
	f, ok := toFloat(v)
	if !ok {
		return 0, fmt.Errorf("want a whole number, got %v", v)
	}
	if f != math.Trunc(f) || f < math.MinInt32 || f > math.MaxInt32 {
		return 0, fmt.Errorf("want a whole number, got %v", v)
	}
	return int32(f), nil
}
