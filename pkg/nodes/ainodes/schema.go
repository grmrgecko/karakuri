package ainodes

import (
	"fmt"
	"math"
	"reflect"
	"strings"
)

// validateJSONValue checks a decoded JSON value against the subset of JSON
// Schema the AI nodes speak: type, properties, required, items, and enum —
// the same vocabulary the Tool node's argument schemas use. Provider-side
// enforcement is best-effort (Anthropic has none at all), so this is the
// check the error branch actually rests on; anything in the schema beyond
// the subset is ignored rather than rejected, since a schema that asks for
// more than is checked still constrains what it names.
func validateJSONValue(value any, schema map[string]any) error {
	return validateAt(value, schema, "$")
}

func validateAt(value any, schema map[string]any, path string) error {
	if len(schema) == 0 {
		return nil
	}

	// Type check first: everything else assumes the value's shape.
	if declared, ok := schema["type"]; ok {
		if err := checkType(value, declared, path); err != nil {
			return err
		}
	}

	if options := stringOrAnySlice(schema["enum"]); len(options) > 0 {
		matched := false
		for _, option := range options {
			if reflect.DeepEqual(value, option) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s is not one of the allowed values", path)
		}
	}

	if fields, ok := value.(map[string]any); ok {
		if required := stringOrAnySlice(schema["required"]); len(required) > 0 {
			for _, name := range required {
				key, _ := name.(string)
				if _, present := fields[key]; key != "" && !present {
					return fmt.Errorf("%s is missing required property %q", path, key)
				}
			}
		}
		if props, ok := schema["properties"].(map[string]any); ok {
			for key, sub := range props {
				propSchema, ok := sub.(map[string]any)
				if !ok {
					continue
				}
				if field, present := fields[key]; present {
					if err := validateAt(field, propSchema, path+"."+key); err != nil {
						return err
					}
				}
			}
		}
	}

	if elements, ok := value.([]any); ok {
		if items, ok := schema["items"].(map[string]any); ok {
			for i, element := range elements {
				if err := validateAt(element, items, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func stringOrAnySlice(value any) []any {
	switch values := value.(type) {
	case []any:
		return values
	case []string:
		out := make([]any, len(values))
		for i := range values {
			out[i] = values[i]
		}
		return out
	default:
		return nil
	}
}

// checkType matches a decoded value against a schema type, which may be one
// name or a list of alternatives.
func checkType(value, declared any, path string) error {
	var names []string
	switch d := declared.(type) {
	case string:
		names = []string{d}
	case []any:
		for _, n := range d {
			if s, ok := n.(string); ok {
				names = append(names, s)
			}
		}
	case []string:
		names = append(names, d...)
	}
	if len(names) == 0 {
		return nil
	}
	for _, name := range names {
		if valueIsType(value, name) {
			return nil
		}
	}
	return fmt.Errorf("%s is not of type %s", path, strings.Join(names, " or "))
}

// valueIsType reports whether a decoded JSON value has a schema type.
func valueIsType(value any, name string) bool {
	switch name {
	case "null":
		return value == nil
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		f, ok := value.(float64)
		return ok && f == math.Trunc(f)
	case "string":
		_, ok := value.(string)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	}
	return true
}
