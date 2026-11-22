// Package core provides the built-in node types that ship with karakuri: the
// manual, webhook, and schedule triggers; the message-shaping nodes (change,
// template, script, switch, filter, dedupe); the timing nodes (delay,
// wait-until, trigger); the sequence nodes (split, join, batch, sort, limit,
// aggregate); the utility nodes (debug, fail, cryptography, JWT, image-edit); the
// decoders (base64, csv, html, json, markdown, rss, spreadsheet, xml, yaml,
// decompress) and their matching encoders; the storage nodes (file-read,
// file-write, file-watch); the outbound actions (http-request,
// wol, ping, socket sends and requests); and the subflow, note, and group pseudo-nodes.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"go.uber.org/zap"
)

// RegisterAll registers every core node type on the given registry.
func RegisterAll(reg engine.NodeRegistrar) {
	reg.Register(ManualNode{})
	reg.Register(FaceplateNode{})
	reg.Register(WebhookNode{})
	reg.Register(WidgetTriggerNode{})
	reg.Register(DashboardCommandNode{})
	reg.Register(ControlGetNode{})
	reg.Register(ControlSetNode{})
	reg.Register(ControlChangedNode{})
	reg.Register(ScheduleNode{})
	reg.Register(TimeWindowNode{})
	reg.Register(NumericTransformNode{})
	reg.Register(ConnectionStatusNode{})
	reg.Register(BackupCreateNode{})
	reg.Register(BackupResultNode{})
	reg.Register(LogEventNode{})
	reg.Register(EventLoggedNode{})
	reg.Register(NotificationNode{})
	reg.Register(DelayNode{})
	reg.Register(WaitUntilNode{})
	reg.Register(ConditionWaitNode{})
	reg.Register(BrowserPromptNode{})
	reg.Register(TriggerNode{})
	reg.Register(DebugNode{})
	reg.Register(FailNode{})
	reg.Register(CryptoNode{})
	reg.Register(JWTNode{})
	reg.Register(SwitchNode{})
	reg.Register(FilterNode{})
	reg.Register(HysteresisNode{})
	reg.Register(DedupeNode{})
	reg.Register(ChangeNode{})
	reg.Register(TemplateNode{})
	reg.Register(ScriptNode{})
	reg.Register(SplitNode{})
	reg.Register(JoinNode{})
	reg.Register(BatchNode{})
	reg.Register(SortNode{})
	reg.Register(LimitNode{})
	reg.Register(AggregateNode{})
	reg.Register(Base64Node{})
	reg.Register(DocumentNode{})
	reg.Register(ImageNode{})
	reg.Register(ImageEditNode{})
	reg.Register(CSVNode{})
	reg.Register(HTMLNode{})
	reg.Register(JSONNode{})
	reg.Register(MarkdownNode{})
	reg.Register(RSSNode{})
	reg.Register(SpreadsheetNode{})
	reg.Register(XMLNode{})
	reg.Register(YAMLNode{})
	reg.Register(DecompressNode{})
	reg.Register(Base64EncodeNode{})
	reg.Register(CSVEncodeNode{})
	reg.Register(JSONEncodeNode{})
	reg.Register(SpreadsheetEncodeNode{})
	reg.Register(XMLEncodeNode{})
	reg.Register(YAMLEncodeNode{})
	reg.Register(CompressNode{})
	reg.Register(HTTPRequestNode{})
	for _, protocol := range []string{"tcp", "udp", "websocket"} {
		reg.Register(SocketInNode{Protocol: protocol})
		reg.Register(SocketOutNode{Protocol: protocol})
	}
	reg.Register(TCPRequestNode{})
	reg.Register(WOLNode{})
	reg.Register(PingNode{})
	reg.Register(FileReadNode{})
	reg.Register(SQLQueryNode{})
	reg.Register(FileWriteNode{})
	reg.Register(FileWatchNode{})
	reg.Register(RerouteNode{})
	reg.Register(LinkSetNode{})
	reg.Register(LinkGetNode{})
	reg.Register(NoteNode{})
	reg.Register(GroupNode{})
	reg.Register(SubflowNode{})
	reg.Register(SubflowInputNode{})
	reg.Register(SubflowOutputNode{})
	reg.Register(TimeSeriesQueryNode{})
	reg.Register(TimeSeriesWriteNode{})
}

// inbound is the message a node acts on: the payload, topic, and group stack of
// its main input port, carrying the metadata every other connected port arrived
// with merged in behind it.
//
// The merge is what makes the envelope ambient. A port's type describes its
// payload and nothing else — the topic and metadata ride along on every port
// whatever that type says — so a node with two sockets must not silently drop
// the correlation keys, the webhook request_id, or the topic that came in on
// the one it did not read. The main port wins every conflict; a key it does not
// set is filled from the other ports in declared order.
//
// The group stack is deliberately not merged. It is scheduling state: the
// engine keys a node instance per group stack and fan-in nodes pop frames off
// it, so a stack assembled from two branches would describe a fan-out that
// never happened. It comes from the main port alone.
func inbound(ec engine.ExecContext, inputs map[string]*engine.Msg) *engine.Msg {
	order := inputOrder(ec.Ports(), inputs)
	main := mainInput(ec.Ports(), inputs, order)

	out := &engine.Msg{
		Payload:      main.Payload,
		Topic:        main.Topic,
		Group:        main.Group,
		Continuation: main.Continuation,
	}

	// Merge metadata in declared order with the main port applied last, so it
	// overwrites whatever the others set for the same key.
	for _, id := range order {
		msg := inputs[id]
		if msg == nil || msg == main {
			continue
		}
		out.Meta = mergeMeta(out.Meta, msg.Meta)
	}
	out.Meta = mergeMeta(out.Meta, main.Meta)

	// A main port with no topic takes the first one another port carries,
	// rather than losing it because it arrived on a different socket.
	if out.Topic == "" {
		for _, id := range order {
			if msg := inputs[id]; msg != nil && msg.Topic != "" {
				out.Topic = msg.Topic
				break
			}
		}
	}
	return out
}

// inputOrder lists the ports carrying a message, declared ports first in
// declaration order and anything else sorted by ID. The synthetic trigger input
// is the "anything else" that matters; sorting rather than ranging the map
// keeps the merge reproducible across runs.
func inputOrder(ports flow.PortSet, inputs map[string]*engine.Msg) []string {
	order := make([]string, 0, len(inputs))
	seen := make(map[string]bool, len(inputs))
	for _, p := range ports.Inputs {
		if inputs[p.ID] != nil {
			order = append(order, p.ID)
			seen[p.ID] = true
		}
	}
	rest := make([]string, 0, len(inputs))
	for id, msg := range inputs {
		if msg != nil && !seen[id] {
			rest = append(rest, id)
		}
	}
	sort.Strings(rest)
	return append(order, rest...)
}

// mainInput returns the message on the node's main input port: the first
// declared port that is neither optional nor a parameter. Reading by port
// rather than taking whatever the input map yields is what makes a node with
// more than one input deterministic — Go map order is not.
//
// Two cases have nothing on the main port and fall back to the first port in
// order: a trigger, whose run-seeding message sits on the synthetic trigger
// input rather than a declared port, and a node dispatched by an optional port
// alone.
func mainInput(ports flow.PortSet, inputs map[string]*engine.Msg, order []string) *engine.Msg {
	if port, ok := ports.PrimaryInput(); ok {
		if msg := inputs[port.ID]; msg != nil {
			return msg
		}
	}
	if len(order) > 0 {
		return inputs[order[0]]
	}
	return &engine.Msg{}
}

// mergeMeta folds src into dst, allocating only when there is something to
// carry, so a node whose inputs have no metadata still emits none.
func mergeMeta(dst, src map[string]any) map[string]any {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string]any, len(src))
	}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// namedInputs collects the payload delivered to each connected input port,
// keyed by port ID, for nodes that read their sockets by name rather than
// taking one message. The synthetic trigger input is left out: it is not a
// port an author can wire or name.
func namedInputs(inputs map[string]*engine.Msg) map[string]any {
	named := make(map[string]any, len(inputs))
	for port, msg := range inputs {
		if msg == nil || port == engine.TriggerInput {
			continue
		}
		named[port] = msg.Payload
	}
	return named
}

// ManualNode holds a typed value. It is not a trigger: it never fires on its
// own, and a flow built only from value nodes simply never runs by itself. It
// earns its place two ways — pulled over a value edge to supply a parameter,
// and, when a graph has no trigger, chosen as the start of a test run so an
// author can drive a flow by hand from the value it holds.
//
// The registered type stays "manual", the name it had when it was a trigger, so
// graphs that already reference it keep working.
type ManualNode struct{}

func (ManualNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "manual",
		Label:             "Value",
		Folder:            flow.FolderValues,
		Description:       "Holds a typed value: wire it into a parameter, or start a test run from it",
		Icon:              "las la-hashtag",
		TestWithoutParams: true,
		// The output port's type is the payload type the node is configured to
		// emit, so a downstream node can see what it will receive. The flag
		// keeps the palette offering the node whatever type a drag is looking
		// for, since the port only takes a type once the config exists.
		DynamicPorts: true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeRule: payloadTypeRule()},
		},
		Pure: true,
		ConfigSpec: []engine.FieldSpec{
			{
				ID:            "payload",
				Label:         "Payload",
				Type:          flow.FieldText,
				Placeholder:   "{}",
				LanguageField: "output_format",
				Help:          "Message payload emitted when the trigger fires, read as the type below.",
			},
			outputTypeField(outputJSON),
		},
	}
}

// Execute emits the configured value. A run seeded on this node (a test run, or
// a rerun replaying a stored payload) passes its initial message through
// instead, so replaying a run reproduces what it actually carried.
func (ManualNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	if msg := inputs[engine.TriggerInput]; msg != nil {
		return engine.One("out", msg), nil
	}
	var cfg manualConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	payload, err := manualPayload(cfg)
	if err != nil {
		return nil, err
	}
	return engine.One("out", &engine.Msg{Payload: payload}), nil
}

// manualConfig is the decoded manual trigger configuration.
type manualConfig struct {
	Payload      json.RawMessage `json:"payload"`
	OutputFormat string          `json:"output_format"`
}

// manualPayload resolves the configured payload. The format is passed through
// unset rather than defaulted, so a config written before the setting existed
// is read the way it was written. An unconfigured trigger emits an empty
// object, the payload shape a flow author most often builds on from here.
func manualPayload(cfg manualConfig) (any, error) {
	payload, err := coerceConfigValue(cfg.Payload, cfg.OutputFormat)
	if err != nil {
		return nil, fmt.Errorf("invalid payload config: %w", err)
	}
	if payload == nil && len(cfg.Payload) == 0 {
		return map[string]any{}, nil
	}
	return payload, nil
}

// ManualTriggerPayload resolves a manual trigger node's payload from its raw
// config. The test-run API seeds its initial message from the same config the
// node reads, so both go through here rather than each interpreting the
// payload and its type setting separately.
func ManualTriggerPayload(config json.RawMessage) (any, error) {
	var cfg manualConfig
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil, fmt.Errorf("invalid manual trigger config: %w", err)
		}
	}
	return manualPayload(cfg)
}

// DebugNode logs incoming messages and emits them as debug events so flow
// behavior can be inspected live and in run history.
type DebugNode struct{}

func (DebugNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "debug",
		Label:       "Debug",
		Folder:      flow.FolderUtility,
		Description: "Logs incoming messages for inspection",
		Icon:        "las la-bug",
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:          "note",
				Label:       "Note",
				Type:        flow.FieldText,
				Placeholder: "Optional label for log output",
			},
		},
	}
}

func (DebugNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg struct {
		Note string `json:"note"`
	}
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)
	ec.Logger().Info("debug node", zap.Any("payload", msg.Payload), zap.String("note", cfg.Note))
	ec.Debug(msg.Payload, cfg.Note)
	return nil, nil
}

// SwitchNode routes a message down one branch per rule: each configured rule
// owns an output port, so a value can pick its own path instead of a single
// true/false split. Ports are derived per instance from the rule list.
type SwitchNode struct{}

func (SwitchNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:         "switch",
		Label:        "Switch",
		Folder:       flow.FolderLogic,
		Description:  "Routes a message down a branch per rule, with an otherwise branch for no match",
		Icon:         "las la-code-branch",
		DynamicPorts: true,
		// Testing a property inside the payload means the payload has to be
		// an object; topic, meta, and the payload itself leave it undeclared.
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny,
				TypeRule: &flow.PortTypeRule{PayloadPath: []string{"property"}}},
		},
		// Outputs are derived from the rules; see flow.SwitchPorts.
		ConfigSpec: []engine.FieldSpec{
			{
				ID:          "property",
				Label:       "Property",
				Type:        flow.FieldString,
				Default:     "payload",
				Placeholder: "payload.temperature",
				Help:        "Dot path every value rule tests: payload.*, topic, or meta.*. Expression rules ignore it.",
			},
			{
				ID:          "rules",
				Label:       "Rules",
				Type:        flow.FieldRules,
				ValueSchema: flow.SwitchRulesSchema(),
				Default: []any{
					map[string]any{"id": "r1", "operator": flow.SwitchOpEq},
					map[string]any{"id": "r2", "operator": flow.SwitchOpOtherwise},
				},
				Options: []engine.Option{
					{Value: flow.SwitchOpEq, Label: "=="},
					{Value: flow.SwitchOpNeq, Label: "!="},
					{Value: flow.SwitchOpLt, Label: "<"},
					{Value: flow.SwitchOpLte, Label: "<="},
					{Value: flow.SwitchOpGt, Label: ">"},
					{Value: flow.SwitchOpGte, Label: ">="},
					{Value: flow.SwitchOpContains, Label: "contains"},
					{Value: flow.SwitchOpRegex, Label: "matches regex"},
					{Value: flow.SwitchOpExists, Label: "exists"},
					{Value: flow.SwitchOpExpression, Label: "expression"},
					{Value: flow.SwitchOpOtherwise, Label: "otherwise"},
				},
				Help: "Each rule gets its own output. Expression rules evaluate against payload, topic, meta, msg, vars, and nodes.<id>; otherwise fires when no other rule matched.",
			},
			{
				ID:      "match",
				Label:   "Matching",
				Type:    flow.FieldSelect,
				Default: flow.SwitchMatchFirst,
				Options: []engine.Option{
					{Value: flow.SwitchMatchFirst, Label: "Stop after the first match"},
					{Value: flow.SwitchMatchAll, Label: "Send to every matching rule"},
				},
			},
		},
	}
}

func (SwitchNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg flow.SwitchConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	rules := cfg.EffectiveRules()
	if len(rules) == 0 {
		return nil, errors.New("switch node has no routing rules")
	}
	msg := inbound(ec, inputs)

	property := strings.TrimSpace(cfg.Property)
	if property == "" {
		property = "payload"
	}
	actual, found := resolveProperty(msg, property)

	// Evaluate rules in order. Otherwise rules are held back: they only fire
	// when no ordinary rule matched, wherever they sit in the list.
	out := make(map[string][]*engine.Msg)
	matched := false
	var otherwise []string
	for _, r := range rules {
		if r.Operator == flow.SwitchOpOtherwise {
			otherwise = append(otherwise, r.ID)
			continue
		}
		if matched && cfg.Match != flow.SwitchMatchAll {
			break
		}
		ok, err := matchSwitchRule(ec, msg, actual, found, r)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		matched = true
		out[r.ID] = []*engine.Msg{branchMsg(msg, len(out))}
	}
	if !matched {
		for _, id := range otherwise {
			out[id] = []*engine.Msg{branchMsg(msg, len(out))}
		}
	}
	return out, nil
}

// matchSwitchRule evaluates one rule against the message. Expression rules run
// the full expression environment; every other operator compares the resolved
// property, with {{ }} interpolation on the comparison value so rules can be
// written against other parts of the message.
func matchSwitchRule(ec engine.ExecContext, msg *engine.Msg, actual any, found bool, r flow.SwitchRule) (bool, error) {
	if r.Operator == flow.SwitchOpExpression {
		if strings.TrimSpace(r.Value) == "" {
			return false, errors.New("switch expression rule has no expression")
		}
		result, err := ec.Eval(r.Value, msg)
		if err != nil {
			return false, err
		}
		return truthy(result), nil
	}
	value := r.Value
	if strings.Contains(value, "{{") {
		interpolated, err := ec.Interpolate(value, msg)
		if err != nil {
			return false, err
		}
		value = interpolated
	}
	return evaluateRule(actual, found, r.Operator, value)
}

// branchMsg returns the message to send down one branch. The first branch
// passes the message through; further branches get their own shallow copy so
// two parallel branches never share mutable meta.
func branchMsg(msg *engine.Msg, index int) *engine.Msg {
	if index == 0 {
		return msg
	}
	copied := *msg
	copied.Meta = nodeutil.CloneMeta(msg.Meta)
	return &copied
}

// truthy converts an expression result to a branch decision: true booleans,
// non-zero numbers, and non-empty strings, lists, and maps are truthy.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	}
	if n, ok := toNumber(v); ok {
		return n != 0
	}
	return true
}

// resolveProperty walks a dot path into a message. The first segment selects
// payload, topic, or meta; a bare path is resolved against the payload.
func resolveProperty(msg *engine.Msg, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any
	switch parts[0] {
	case "topic":
		cur = msg.Topic
		parts = parts[1:]
	case "meta":
		m := make(map[string]any, len(msg.Meta))
		for k, v := range msg.Meta {
			m[k] = v
		}
		cur = m
		parts = parts[1:]
	case "payload":
		cur = msg.Payload
		parts = parts[1:]
	default:
		cur = msg.Payload
	}
	for _, part := range parts {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// evaluateRule applies a switch operator to the resolved property value.
func evaluateRule(actual any, found bool, operator, value string) (bool, error) {
	switch operator {
	case "exists":
		return found, nil
	case "eq":
		return found && looseEquals(actual, value), nil
	case "neq":
		return !found || !looseEquals(actual, value), nil
	case "lt", "lte", "gt", "gte":
		if !found {
			return false, nil
		}
		af, ok := toNumber(actual)
		if !ok {
			return false, fmt.Errorf("property value %v is not numeric", actual)
		}
		vf, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return false, fmt.Errorf("comparison value %q is not numeric", value)
		}
		switch operator {
		case "lt":
			return af < vf, nil
		case "lte":
			return af <= vf, nil
		case "gt":
			return af > vf, nil
		default:
			return af >= vf, nil
		}
	case "contains":
		if !found {
			return false, nil
		}
		return strings.Contains(stringify(actual), value), nil
	case "regex":
		if !found {
			return false, nil
		}
		re, err := regexp.Compile(value)
		if err != nil {
			return false, fmt.Errorf("invalid regular expression %q: %w", value, err)
		}
		return re.MatchString(stringify(actual)), nil
	default:
		return false, fmt.Errorf("unknown operator %q", operator)
	}
}

// looseEquals compares a resolved value against a config string with numeric
// tolerance so 3 matches "3".
func looseEquals(actual any, value string) bool {
	if af, ok := toNumber(actual); ok {
		if vf, err := strconv.ParseFloat(value, 64); err == nil {
			return af == vf
		}
	}
	return stringify(actual) == value
}

// toNumber converts common numeric types to float64.
func toNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// stringify renders a value for string comparison; non-scalars use JSON.
func stringify(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		// Bytes read as the text they hold. Marshaling them would produce a
		// base64 document, which is an encoding decision a base64 node makes.
		return string(s)
	case nil:
		return ""
	}
	if raw, err := json.Marshal(v); err == nil {
		return string(raw)
	}
	return fmt.Sprint(v)
}

// timeLayouts are the layouts a timestamp arriving in a message is read with,
// tried in order. RFC 3339 covers what an API returns, the SQL-style pair
// covers what a database or a device firmware writes, and the RFC 1123 pair
// covers an HTTP date header. Layouts carrying no zone are read in the caller's
// location, which is why the naive forms sit beside the zoned ones rather than
// being normalized away.
var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	time.RFC1123Z,
	time.RFC1123,
}

// parseTimeValue reads a value as an instant, for the nodes that compare or
// wait on a timestamp that arrived in a message. It accepts a time.Time, a Unix
// timestamp as a number or a numeric string, and the layouts in timeLayouts,
// with the zoneless ones read in loc.
//
// A Unix value is seconds or milliseconds by magnitude: 1e12 seconds lands in
// the year 33658, so anything at or above it is milliseconds. Either may carry
// a fraction.
func parseTimeValue(v any, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.Local
	}
	if t, ok := v.(time.Time); ok {
		return t, nil
	}

	// A bare number is Unix time; so is a string holding nothing but digits,
	// which is how a JSON payload that quoted its timestamps arrives.
	raw := strings.TrimSpace(stringify(v))
	if raw == "" {
		return time.Time{}, fmt.Errorf("timestamp is empty")
	}
	if n, ok := sortNumber(v); ok && !strings.ContainsAny(raw, "-:TZ") {
		// The fraction is kept: two readings half a second apart are two
		// instants, and whole seconds would make the later one a repeat.
		whole, frac := math.Modf(n)
		if n >= 1e12 {
			return time.UnixMilli(int64(whole)).Add(time.Duration(frac * float64(time.Millisecond))).In(loc), nil
		}
		return time.Unix(int64(whole), int64(math.Round(frac*float64(time.Second)))).In(loc), nil
	}

	for _, layout := range timeLayouts {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("timestamp %q is not a Unix time or a recognized date layout", raw)
}

// storedTime reads a timestamp back out of a durable store value, where times
// are kept as RFC 3339 strings because the store holds JSON shapes only.
func storedTime(v any) (time.Time, bool) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// NoteNode is a canvas annotation with no ports and no runtime behavior.
// The engine never schedules annotation nodes. Its text is markdown, rendered
// by the server through the same pipeline the built-in reference pages use.
type NoteNode struct{}

func (NoteNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "note",
		Label:       "Note",
		Category:    flow.CategoryAnnotation,
		Folder:      flow.FolderAnnotations,
		Description: "Canvas annotation for documenting a flow",
		Icon:        "las la-sticky-note",
		ConfigSpec: []engine.FieldSpec{
			{
				ID:       "text",
				Label:    "Markdown",
				Type:     flow.FieldText,
				Help:     "Rendered as markdown on the canvas. Drag the note's edge to resize it; longer text scrolls.",
				Required: true,
			},
			{
				ID:    "images",
				Label: "Images",
				Type:  flow.FieldImages,
				Help:  "Attached images travel with the flow, so an export shows them too. Refer to one in the markdown as ![alt](image:name).",
			},
			{
				ID:      "color",
				Label:   "Color",
				Type:    flow.FieldSelect,
				Default: "yellow",
				Options: []engine.Option{
					{Value: "yellow", Label: "Yellow"},
					{Value: "blue", Label: "Blue"},
					{Value: "green", Label: "Green"},
					{Value: "pink", Label: "Pink"},
					{Value: "gray", Label: "Gray"},
				},
			},
		},
	}
}

func (NoteNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	return nil, nil
}

// GroupNode is a titled frame drawn behind the graph, gathering the nodes that
// stand inside it so they can be labelled and moved together. Like the note it
// has no ports and never runs; membership is the geometry on the canvas rather
// than anything stored, so a node joins a group by being dragged into it.
type GroupNode struct{}

func (GroupNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "group",
		Label:       "Group",
		Category:    flow.CategoryAnnotation,
		Folder:      flow.FolderAnnotations,
		Description: "Titled frame that gathers and moves a set of nodes together",
		Icon:        "las la-object-group",
		ConfigSpec: []engine.FieldSpec{
			{
				ID:          "title",
				Label:       "Title",
				Type:        flow.FieldString,
				Placeholder: "Group",
				Help:        "Shown on the frame's header, which is also its drag handle.",
			},
			{
				ID:      "color",
				Label:   "Color",
				Type:    flow.FieldSelect,
				Default: "blue",
				Options: []engine.Option{
					{Value: "yellow", Label: "Yellow"},
					{Value: "blue", Label: "Blue"},
					{Value: "green", Label: "Green"},
					{Value: "pink", Label: "Pink"},
					{Value: "gray", Label: "Gray"},
				},
			},
		},
	}
}

func (GroupNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	return nil, nil
}
