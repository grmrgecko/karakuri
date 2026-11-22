package flow

// NodeTypeManual is the manual trigger, whose output port type is its
// configured payload type.
const NodeTypeManual = "manual"

// Payload type constants: how a node reads authored text into a value. They
// are the vocabulary behind the "output type" setting, and they decide the
// port type a node advertises, so the node, validation, and the editor all
// read them from here.
const (
	PayloadString  = "string"
	PayloadInteger = "integer"
	PayloadFloat   = "float"
	PayloadBoolean = "boolean"
	PayloadJSON    = "json"
	PayloadYAML    = "yaml"
)

// PayloadPortType maps a payload type to the port type a node emitting it
// advertises. JSON and YAML differ only in how the text is written, so both
// produce an object; an unknown or unset type falls back to the untyped
// message port, which is what a node advertised before types existed.
func PayloadPortType(payloadType string) string {
	switch payloadType {
	case PayloadString:
		return PortString
	case PayloadInteger:
		return PortInteger
	case PayloadFloat:
		return PortFloat
	case PayloadBoolean:
		return PortBool
	case PayloadJSON, PayloadYAML:
		return PortObject
	default:
		return PortAny
	}
}

// The manual node's port type is no longer derived here: its output carries a
// PortTypeRule keyed on output_format, resolved by the same machinery as every
// other config-typed port, so NodePorts needs no special case for it.
