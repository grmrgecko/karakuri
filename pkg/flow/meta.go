package flow

// Node category constants naming the behavioral kinds. Category carries
// behavior only — where a node sits in the palette is its Folder, which is
// free-form so plugins can bring their own. An ordinary node leaves Category
// empty.
const (
	// CategoryTrigger marks nodes that arm against the outside world and start
	// runs. Equivalent to the Trigger flag; IsTrigger answers for both.
	CategoryTrigger = "trigger"
	// CategoryAnnotation marks canvas decoration (note, group): no edges, no
	// execution.
	CategoryAnnotation = "annotation"
	// CategoryPort marks the subflow boundary pseudo-nodes (subflow-input and
	// subflow-output). They are only valid inside subflow graphs and never
	// execute: the engine splices them out when flattening subflows.
	CategoryPort = "port"
)

// Folder names for the built-in nodes. Folders are display taxonomy only —
// plain strings the palette groups by — so these constants
// exist for consistency across the built-in registrations, not as a closed
// set: a plugin registers whatever folder name fits its domain.
const (
	FolderValues        = "Values"
	FolderLogic         = "Logic"
	FolderTime          = "Time"
	FolderSequence      = "Sequence"
	FolderParsers       = "Parsers"
	FolderEncoders      = "Encoders"
	FolderStorage       = "Storage"
	FolderHTTP          = "HTTP"
	FolderNetwork       = "Network"
	FolderMQTT          = "MQTT"
	FolderDevices       = "Devices"
	FolderHomeAssistant = "Home Assistant"
	FolderESPHome       = "ESPHome"
	FolderOpenHAB       = "openHAB"
	FolderNVR           = "NVR"
	FolderEnergy        = "Energy"
	FolderAI            = "AI"
	FolderDashboard     = "Dashboard"
	FolderControls      = "Controls"
	FolderUtility       = "Utility"
	FolderSubflowPorts  = "Subflow Ports"
	FolderAnnotations   = "Annotations"
)

// Config field type constants describing how a field is edited and validated.
const (
	FieldString     = "string"
	FieldNumber     = "number"
	FieldBoolean    = "boolean"
	FieldSelect     = "select"
	FieldConnection = "connection"
	FieldEntity     = "entity"
	// FieldHATarget is a list of Home Assistant registry IDs, stored as
	// comma-separated text. Control names the registry: area, floor, device,
	// or label.
	FieldHATarget   = "ha-target"
	FieldExpression = "expression"
	FieldJSON       = "json"
	FieldDuration   = "duration"
	FieldCron       = "cron"
	FieldText       = "text"
	FieldTimezone   = "timezone"
	// FieldScript is source code edited in a full-screen editor. The editor
	// shows a short preview inline and opens the highlighted editor on click.
	FieldScript = "script"
	// FieldRules is the switch node's routing rule list: an ordered set of
	// conditions, each owning one output port. Options carries the operator
	// choices offered per rule.
	FieldRules = "rules"
	// FieldPorts is a script node's port declaration: a list of {id, label,
	// type} the author edits to give the node its own signature.
	FieldPorts = "ports"
	// FieldChangeRules is the change node's rewrite list: an ordered set of
	// set/change/delete/move/copy operations applied to the message. Options
	// carries the operation choices offered per rule.
	FieldChangeRules = "change_rules"
	// FieldToolArgs is the Tool node's argument declaration: a list of {id,
	// label, type, description, required} the author edits, which becomes both
	// the JSON Schema the model fills in and one typed value-output port per
	// argument.
	FieldToolArgs = "tool_args"
	// FieldImages is a set of images attached to the node, keyed by name and
	// held as data URIs. A note's markdown refers to one by "image:<name>",
	// which the editor resolves when it renders. Carrying the bytes in the
	// config is what makes an export self-contained: a flow file opened on
	// another instance shows the same pictures without anything to fetch.
	FieldImages = "images"
	// FieldMarkdown is Markdown with a preview and portable attachments.
	FieldMarkdown = "markdown"
	// FieldAttachments holds named base64 files referenced from Markdown.
	FieldAttachments = "attachments"
	// FieldButtons declares stable button output IDs and visible labels.
	FieldButtons = "buttons"
)

// Coordinate roles for the Geo hint on a number config field. A spec that
// carries both lets the editor fill the pair from the browser's location.
const (
	GeoLatitude  = "latitude"
	GeoLongitude = "longitude"
)

// Port kinds. A normal port is on the message path; an error port is a
// failure branch; a param port supplies one config field from another node's
// output and carries no message; a return port brings the result of a branch
// back to the request node that started it. A signal input controls active work
// without pulling parameters or claiming execute-once on signal-only executions.
const (
	PortKindError  = "error"
	PortKindParam  = "param"
	PortKindReturn = "return"
	PortKindSignal = "signal"
)

// PortSpec describes one input or output port of a node type.
// Kind is "" for normal ports, "error" for failure-branch outputs, "param"
// for a config field driven by a value edge, "return" for request replies,
// or "signal" for optional control inputs.
type PortSpec struct {
	// PayloadSchema describes the emitted payload for authors; runtime typing
	// still uses Type and TypeRule.
	PayloadSchema map[string]any `json:"payload_schema,omitempty" description:"JSON Schema describing the payload, excluding the message envelope."`
	ID            string         `json:"id" description:"Port identifier used as an edge handle."`
	Label         string         `json:"label" description:"Display name."`
	Type          string         `json:"type" description:"Port value type."`
	Kind          string         `json:"kind,omitempty" description:"Port kind: empty for normal, \"error\" for failure branch, \"param\" for a value parameter, \"return\" for a request reply, or \"signal\" for an optional control input."`
	// TypeRule derives the port's type from the placed node's config, for the
	// ports whose payload depends on how the node is set up: a JSON node
	// decoding text emits an object and encoding emits a string. Type is the
	// type an unconfigured node advertises, and the rule refines it.
	TypeRule *PortTypeRule `json:"type_rule,omitempty" description:"Derives the port's type from the node's config."`
	// TypeFrom names an input port on the same node whose payload this output
	// carries onward. A node that only routes, delays, or annotates a message
	// does not change what is in it, so declaring "any" would throw away what
	// the graph already knows: the type is whatever was wired in.
	//
	// It only applies once the port's own type resolves to "any" — a node that
	// knows what it emits says so, and a delay in front of a JSON parser
	// reports the parser's object rather than shrugging.
	TypeFrom string `json:"type_from,omitempty" description:"Input port whose payload type this output carries."`
	// Optional marks an input port that does not gate readiness: a message on
	// it runs the node on its own, and its absence never holds the node back.
	// Reset and control inputs use it, since they arrive independently of the
	// node's main input — often from a different branch or run entirely.
	Optional bool `json:"optional,omitempty" description:"Whether an input port dispatches the node on its own instead of gating readiness."`
	// Multi marks a parameter port that accepts several value edges at once.
	// The pulled values reach the config field as an array in graph edge
	// order; the single-source rule does not apply to the port. Only set by
	// port fields (FieldSpec.Port with Multi).
	Multi bool `json:"multi,omitempty" description:"Whether a parameter port accepts several value edges, delivered as an array."`
}

// Option is one selectable value for a select-type config field.
type Option struct {
	Value string `json:"value" description:"Stored value."`
	Label string `json:"label" description:"Display name."`
}

// ShowIf conditionally shows a config field based on another field's value.
// Equals holds one value, or a list the field may match any of — a mode select
// with three values often splits its dependent fields two against one. "*"
// matches any non-empty value (the PortTypeRule convention), and against a
// comma-separated string (the tokens control) a value matches when any one
// token does. On a port field, ShowIf gates the socket itself: the port
// exists only while the condition holds, so a required one is enforced
// exactly when it is shown.
type ShowIf struct {
	Field  string `json:"field" description:"Config field ID to compare."`
	Equals any    `json:"equals" description:"Value the field must equal for this field to show; a list matches any of its values, \"*\" any non-empty value, and any one token of a comma-separated value counts."`
}

// FieldSpec describes one config field of a node type: how it is edited,
// its default, and its validation constraints.
type FieldSpec struct {
	// ValueSchema describes structured values that the editor datatype alone
	// cannot express. Node validation remains the executable contract.
	ValueSchema map[string]any `json:"value_schema,omitempty" description:"JSON Schema for this config value, including nested fields."`
	ID          string         `json:"id" description:"Config key."`
	Label       string         `json:"label" description:"Display name."`
	// Type is the field's semantic datatype: what validation checks and what
	// a value edge can bind to. It also names the default editor.
	Type string `json:"type" description:"Field datatype, and the default editor."`
	// Control overrides the editor the field renders with, referencing a
	// registered FieldControlDef ("password", a plugin's own). A frontend
	// without the control falls back to the editor for the control's Base
	// type, so the field stays editable.
	Control     string   `json:"control,omitempty" description:"Editor control override; falls back to the type's editor."`
	Required    bool     `json:"required" description:"Whether a value must be provided."`
	Default     any      `json:"default,omitempty" description:"Default value."`
	Options     []Option `json:"options,omitempty" description:"Choices for select fields."`
	ShowIf      *ShowIf  `json:"show_if,omitempty" description:"Conditional display rule."`
	Help        string   `json:"help,omitempty" description:"Help text shown in the editor."`
	Placeholder string   `json:"placeholder,omitempty" description:"Input placeholder text."`
	// AttachmentField names the config field storing this Markdown's files.
	AttachmentField string `json:"attachment_field,omitempty" description:"Attachments config field for a Markdown editor."`
	// HideLabel lets a field sit directly below a selector that already names
	// it, without repeating the same heading in the form.
	HideLabel bool `json:"hide_label,omitempty" description:"Whether the editor omits this field's label."`
	// ConnectionKind narrows connection-type fields to matching driver kinds so
	// the editor lists only usable connections. It holds one kind, or several
	// comma-separated for a field any of a family satisfies (the AI model node
	// takes every AI provider kind).
	ConnectionKind string `json:"connection_kind,omitempty" description:"Connection driver kind(s) for connection fields, comma-separated."`
	// ConnectionMatch requires exact values in the connection's public config.
	// ConnectionExclude rejects a connection when every listed value matches.
	ConnectionMatch   map[string]string `json:"connection_match,omitempty" description:"Public connection config values that must match."`
	ConnectionExclude map[string]string `json:"connection_exclude,omitempty" description:"Public connection config values that must not all match."`
	// LanguageField names the config field that says how this field's text is
	// read: the language of a script field, or the output type of a text field
	// holding a payload. The editor picks the matching syntax highlighting from
	// it, so a payload written as JSON reads as JSON in the document editor.
	LanguageField string `json:"language_field,omitempty" description:"Config field holding the language or output type this field's text is read as."`
	// Examples maps a language to starter source for a script field. The
	// editor prefills an empty field with the entry for the chosen language.
	Examples map[string]string `json:"examples,omitempty" description:"Starter source per language for script fields."`
	// Geo marks a number field as one half of a coordinate pair, either
	// GeoLatitude or GeoLongitude. It changes nothing about how the value is
	// stored or validated; it only tells the editor which two fields the
	// browser's location can fill in together.
	Geo string `json:"geo,omitempty" description:"Coordinate role of a number field: latitude or longitude."`
	// Port makes the field a permanent value input port instead of an editable
	// setting, naming the port type it accepts. Its value only ever arrives
	// over value edges — the editor renders a socket rather than an input, the
	// field cannot be toggled through Node.Params, and validateConfig skips it.
	// The AI agent's model and tool inputs are port fields: what they hold is
	// another node's output, which nobody could type in.
	Port string `json:"port,omitempty" description:"Port type that makes this field a permanent value input port instead of an editable setting."`
	// Multi lets several value edges feed a port field at once; the pulled
	// values reach the config as an array in graph edge order, and an unwired
	// field decodes as absent. Only meaningful with Port.
	Multi bool `json:"multi,omitempty" description:"Whether a port field accepts several value edges, delivered as an array."`
	// NoParam keeps an editable field from being exposed as an optional value
	// input when the node already receives that data on its message path.
	NoParam bool `json:"no_param,omitempty" description:"Whether this editable field is excluded from optional value-input ports."`
}

// InvocationState declares whether one node execution can retain state through
// its node stores. It controls the stable identity required for script calls.
type InvocationState string

const (
	// InvocationStateStateless marks a node audited to keep no state in its
	// transient or durable node stores.
	InvocationStateStateless InvocationState = "stateless"
	// InvocationStateStateful marks a node whose stores must be scoped to an
	// explicit stable invocation identity.
	InvocationStateStateful InvocationState = "stateful"
	// InvocationStateUnknown marks a node whose store use has not been audited.
	// It needs the same stable identity as a stateful node.
	InvocationStateUnknown InvocationState = "unknown"
)

// NodeMeta is the full metadata describing a node type: identity, ports,
// config schema, and execution hints. Served to the editor via /nodetypes.
type NodeMeta struct {
	Type  string `json:"type" description:"Unique node type identifier."`
	Label string `json:"label" description:"Display name."`
	// Category is the behavioral kind (trigger, annotation, or port) and is
	// empty for ordinary nodes. Palette placement is Folder's job.
	Category string `json:"category,omitempty" description:"Behavioral kind: trigger, annotation, or port; empty for ordinary nodes."`
	// Folder is the palette folder the node is listed under: a free-form
	// display string, so a plugin can register its own domain.
	// It carries no behavior and never gates anything.
	Folder string `json:"folder,omitempty" description:"Palette folder the node is listed under."`
	// Group is the palette group the folder nests under, naming where the node
	// runs: the host stamps an agent's name onto every node it contributes, and
	// an empty group is the server's own, which the editor labels itself. Like
	// Folder it is display taxonomy only.
	Group          string       `json:"group,omitempty" description:"Palette group the folder nests under; empty means the server's own group."`
	Description    string       `json:"description" description:"Short summary of what the node does."`
	Icon           string       `json:"icon" description:"Line-awesome icon class."`
	Inputs         []PortSpec   `json:"inputs" description:"Input ports."`
	Outputs        []PortSpec   `json:"outputs" description:"Output ports."`
	ConfigSpec     []FieldSpec  `json:"config_spec" description:"Config field schema."`
	DefaultRetry   *RetryPolicy `json:"default_retry,omitempty" description:"Default retry policy applied when the node has none."`
	ConnectionKind string       `json:"connection_kind,omitempty" description:"Connection driver kind this node requires."`
	Retryable      bool         `json:"retryable" description:"Whether the node supports retry on failure."`
	// TestWithoutParams marks trigger types that can seed editor test runs
	// without a test_params config (manual, schedule): their Execute builds a
	// realistic message on its own when the initial payload is empty.
	TestWithoutParams bool `json:"test_without_params,omitempty" description:"Whether the trigger can test-run without test_params config."`
	// Pure marks a node type whose Execute has no side effects and can be run
	// on demand to produce a value, rather than only when a message reaches it.
	// Value edges pull from pure nodes; asking an impure one for a value is a
	// validation error, because pulling it would mean calling the world twice.
	Pure bool `json:"pure,omitempty" description:"Whether the node can be evaluated on demand to supply a value."`
	// InvocationState declares whether execution uses transient or durable node
	// stores. Built-in nodes deliberately use the zero value for audited
	// stateless implementations; contributed metadata is normalized to unknown
	// when the declaration is absent.
	InvocationState InvocationState `json:"invocation_state,omitempty" description:"Node-store use: stateless, stateful, or unknown."`
	// DynamicPorts marks node types whose ports are derived per instance from
	// a referenced graph (the subflow node) rather than from static metadata.
	DynamicPorts bool `json:"dynamic_ports,omitempty" description:"Whether ports are derived per instance instead of from static metadata."`
	// Trigger marks a node that starts runs on its own but sits outside the
	// trigger category, such as the file watcher under storage. Everything that
	// looks for a flow's entry point asks IsTrigger rather than the category.
	Trigger bool `json:"trigger,omitempty" description:"Whether the node starts runs despite sitting outside the trigger category."`
}

// EffectiveInvocationState resolves the built-in zero value to audited
// stateless and treats unrecognized declarations conservatively as unknown.
func (m NodeMeta) EffectiveInvocationState() InvocationState {
	switch m.InvocationState {
	case "", InvocationStateStateless:
		return InvocationStateStateless
	case InvocationStateStateful:
		return InvocationStateStateful
	default:
		return InvocationStateUnknown
	}
}

// InvocationIdentityRequired reports whether a script call needs an explicit
// stable ID for transient and durable node stores.
func (m NodeMeta) InvocationIdentityRequired() bool {
	return m.EffectiveInvocationState() != InvocationStateStateless
}

// NormalizeContributedNodeMeta makes an omitted invocation-state declaration
// unknown instead of giving contributed code the built-in stateless default.
// Invalid declarations are also normalized to unknown.
func NormalizeContributedNodeMeta(m *NodeMeta) {
	if m == nil {
		return
	}
	if m.InvocationState == "" {
		m.InvocationState = InvocationStateUnknown
		return
	}
	m.InvocationState = m.EffectiveInvocationState()
}

// IsTrigger reports whether the node type starts runs on its own, which is
// what makes it a flow's entry point for arming, test runs, and re-runs.
func (m *NodeMeta) IsTrigger() bool {
	return m.Category == CategoryTrigger || m.Trigger
}

// Port lookups deliberately live on PortSet rather than here: a node type's
// static ports are only the starting point, and looking one up without
// resolving the placed node's config misses everything a script, switch, or
// manual node declared for itself. Use NodePorts.
