package scriptsdk

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// ValidateCall validates one invocation against catalog metadata without
// executing the node, resolving a connection, or calling an integration. Config
// and parameter values are combined for conditional fields and dynamic ports.
func ValidateCall(meta flow.NodeMeta, call nodeapi.NodeCall, pure bool) error {
	if meta.Type == "" || call.Type == "" {
		return fmt.Errorf("node type is required")
	}
	if call.Type != meta.Type {
		return fmt.Errorf("node call type %q does not match catalog type %q", call.Type, meta.Type)
	}
	if !Eligible(meta) {
		return fmt.Errorf("node type %q is not eligible for script calls", meta.Type)
	}
	if pure && !meta.Pure {
		return fmt.Errorf("node type %q has side effects and cannot be called during pure evaluation", meta.Type)
	}
	if meta.InvocationIdentityRequired() && strings.TrimSpace(call.Options.ID) == "" {
		return fmt.Errorf("node type %q requires a non-empty options.id because its invocation state is %s",
			meta.Type, meta.EffectiveInvocationState())
	}
	if call.Options.ID != "" && strings.TrimSpace(call.Options.ID) == "" {
		return fmt.Errorf("options.id must not be blank")
	}
	if call.Options.Retry != nil {
		if pure {
			return fmt.Errorf("retry options are not available during pure evaluation")
		}
		if !meta.Retryable {
			return fmt.Errorf("node type %q does not accept a retry policy", meta.Type)
		}
		if err := call.Options.Retry.Validate(); err != nil {
			return fmt.Errorf("invalid retry policy: %w", err)
		}
	}

	values, err := decodeConfig(call.Config)
	if err != nil {
		return err
	}
	fields := make(map[string]flow.FieldSpec, len(meta.ConfigSpec))
	for _, field := range meta.ConfigSpec {
		if _, exists := fields[field.ID]; !exists {
			fields[field.ID] = field
		}
	}
	for _, id := range sortedKeys(values) {
		field, ok := fields[id]
		if !ok {
			return fmt.Errorf("unknown config field %q for node type %q", id, meta.Type)
		}
		if field.Port != "" {
			return fmt.Errorf("config field %q must be supplied through options.params", id)
		}
	}

	effective := make(map[string]any, len(values)+len(call.Options.Params))
	for id, value := range values {
		effective[id] = value
	}
	for _, id := range sortedKeys(call.Options.Params) {
		value := call.Options.Params[id]
		field, ok := fields[id]
		if !ok {
			return fmt.Errorf("unknown parameter %q for node type %q", id, meta.Type)
		}
		if field.Port == "" && (field.NoParam || !flow.FieldBindable(field.Type)) {
			return fmt.Errorf("config field %q cannot be supplied through options.params", id)
		}
		if err := validateParameter(field, value); err != nil {
			return fmt.Errorf("parameter %q: %w", id, err)
		}
		effective[id] = value
	}
	for _, field := range meta.ConfigSpec {
		if field.Port == "" {
			continue
		}
		value, supplied := call.Options.Params[field.ID]
		if !flow.FieldShown(meta.ConfigSpec, field, effective) {
			if supplied {
				return fmt.Errorf("parameter %q is not available in this configuration", field.ID)
			}
			continue
		}
		if field.Required && (!supplied || requiredValueMissing(value)) {
			return fmt.Errorf("required parameter %q is missing", field.ID)
		}
	}

	raw, err := json.Marshal(effective)
	if err != nil {
		return fmt.Errorf("config and parameters must contain only JSON/base64 data: %w", err)
	}
	if err := flow.ValidateNodeConfig(meta, raw); err != nil {
		return fmt.Errorf("invalid config for node type %q: %w", meta.Type, err)
	}

	params := sortedKeys(call.Options.Params)
	ports := flow.NodePorts(meta, &flow.Node{Type: meta.Type, Config: raw, Params: params}, nil)
	if !ports.Known {
		return fmt.Errorf("node type %q has unresolved ports", meta.Type)
	}
	return validateInputs(ports, call.Inputs)
}

func decodeConfig(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("config is required, but may be an empty object")
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("config must be a valid JSON object: %w", err)
	}
	if values == nil {
		return nil, fmt.Errorf("config must be a JSON object, not null")
	}
	return values, nil
}

func validateParameter(field flow.FieldSpec, value any) error {
	portType := field.Port
	if portType == "" {
		var ok bool
		portType, ok = flow.FieldPortType(field.Type)
		if !ok {
			return fmt.Errorf("has no single-value parameter contract")
		}
	}
	if field.Multi {
		rv := reflect.ValueOf(value)
		if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
			return fmt.Errorf("must be an array of %s values", portType)
		}
		for i := 0; i < rv.Len(); i++ {
			if err := validatePayloadValue(portType, rv.Index(i).Interface()); err != nil {
				return fmt.Errorf("element %d: %w", i, err)
			}
		}
	} else if err := validatePayloadValue(portType, value); err != nil {
		return err
	}
	if _, err := json.Marshal(value); err != nil {
		return fmt.Errorf("must contain only JSON/base64 data: %w", err)
	}
	return nil
}

func requiredValueMissing(value any) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok && text == "" {
		return true
	}
	rv := reflect.ValueOf(value)
	return rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) && rv.Len() == 0
}

func validateInputs(ports flow.PortSet, inputs map[string]*nodeapi.Msg) error {
	for _, port := range ports.Inputs {
		if port.Optional || port.Kind == flow.PortKindParam ||
			port.Kind == flow.PortKindReturn || port.Kind == flow.PortKindSignal {
			continue
		}
		if msg, ok := inputs[port.ID]; !ok || msg == nil {
			return fmt.Errorf("required input %q is missing", port.ID)
		}
	}
	for _, id := range sortedKeys(inputs) {
		msg := inputs[id]
		port, ok := ports.Input(id)
		if !ok {
			return fmt.Errorf("unknown input port %q", id)
		}
		switch port.Kind {
		case flow.PortKindParam:
			return fmt.Errorf("input %q is a value parameter; supply it through options.params", id)
		case flow.PortKindReturn:
			return fmt.Errorf("input %q is graph return routing and cannot be called directly", id)
		case flow.PortKindError:
			return fmt.Errorf("input %q is not an ordinary message input", id)
		}
		if msg == nil {
			return fmt.Errorf("input %q must be a complete message object", id)
		}
		if _, err := json.Marshal(msg); err != nil {
			return fmt.Errorf("input %q must contain only JSON/base64 data: %w", id, err)
		}
		if err := validatePayloadValue(port.Type, msg.Payload); err != nil {
			return fmt.Errorf("input %q payload: %w", id, err)
		}
	}
	return nil
}

func validatePayloadValue(portType string, value any) error {
	switch portType {
	case "", flow.PortAny, flow.PortObject:
		return nil
	case flow.PortString:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("must be a string")
		}
		return nil
	case flow.PortBool:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("must be a boolean")
		}
		return nil
	case flow.PortNumber, flow.PortFloat, flow.PortInteger:
		number, ok := numericValue(value)
		if !ok {
			return fmt.Errorf("must be a number")
		}
		if portType == flow.PortInteger && math.Trunc(number) != number {
			return fmt.Errorf("must be an integer")
		}
		return nil
	case flow.PortBinary:
		switch data := value.(type) {
		case []byte:
			return nil
		case string:
			if _, err := base64.StdEncoding.DecodeString(data); err != nil {
				return fmt.Errorf("must be standard base64 binary data: %w", err)
			}
			return nil
		default:
			return fmt.Errorf("must be standard base64 binary data")
		}
	case flow.PortImage:
		return validateImageValue(value)
	default:
		if !portableType(portType) {
			return fmt.Errorf("uses opaque port type %q, which cannot cross the script boundary", portType)
		}
		return nil
	}
}

func numericValue(value any) (float64, bool) {
	if text, ok := value.(json.Number); ok {
		number, err := text.Float64()
		return number, err == nil && !math.IsNaN(number) && !math.IsInf(number, 0)
	}
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return 0, false
	}
	var number float64
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		number = float64(rv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		number = float64(rv.Uint())
	case reflect.Float32, reflect.Float64:
		number = rv.Float()
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0)
}

func validateImageValue(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("must be an image object: %w", err)
	}
	var image flow.ImageValue
	if err := json.Unmarshal(raw, &image); err != nil {
		return fmt.Errorf("must be an image object with mime and base64 data")
	}
	if image.MIME == "" || image.Data == "" {
		return fmt.Errorf("must be an image object with non-empty mime and data")
	}
	if _, err := base64.StdEncoding.DecodeString(image.Data); err != nil {
		return fmt.Errorf("image data must be standard base64: %w", err)
	}
	return nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
