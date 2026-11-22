package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"go.uber.org/zap"
)

// Controls are the values karakuri owns rather than observes: a toggle, a
// number, a dropdown, a button. A dashboard row sets one and any number of
// flows read or trigger on it, which is what a dashboard button that only
// presses a flow cannot express — it leaves nothing behind to show or to read.
//
// Three nodes cover the whole of it. Get pulls the current value, Set writes
// one, and Changed starts a run when one moves.

// controlField is the shared picker naming which control a node acts on.
func controlField(help string) engine.FieldSpec {
	return engine.FieldSpec{
		ID:       "control",
		Label:    "Control",
		Type:     flow.FieldString,
		Control:  "control-picker",
		Required: true,
		Help:     help,
	}
}

// controlKindOptions lists the value types a control can carry, for the field
// that decides what a port declares.
func controlKindOptions() []engine.Option {
	kinds := controls.Kinds()
	out := make([]engine.Option, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, engine.Option{Value: k.Name, Label: k.Label})
	}
	return out
}

// attributeField is the picker naming which part of a composite control a node
// acts on. It reads the sibling kind field the same way the control picker
// does, so a light offers brightness and colour while a toggle offers nothing
// to choose.
func attributeField(help string) engine.FieldSpec {
	return engine.FieldSpec{
		ID:      "attribute",
		Label:   "Attribute",
		Type:    flow.FieldString,
		Control: "control-attribute",
		Help:    help,
	}
}

// controlValueTypeRule maps the chosen kind and attribute onto the payload type
// the port carries. It is generated from the catalog rather than written out,
// so a kind that gains an attribute gains a case with it, and it is a rule
// rather than per-node code because the editor derives the same answer from the
// same config: the value edge binding a light's brightness to a config field is
// only as good as the type it reports.
func controlValueTypeRule() *flow.PortTypeRule {
	var cases []flow.PortTypeCase
	for _, k := range controls.Kinds() {
		for _, a := range k.Attributes {
			cases = append(cases, flow.PortTypeCase{
				Match: map[string][]string{"kind": {k.Name}, "attribute": {a.ID}},
				Type:  a.Type,
			})
		}
		// No attribute named is the whole value, which for a composite kind is
		// the object and for everything else is the one value it holds.
		cases = append(cases, flow.PortTypeCase{
			Match: map[string][]string{"kind": {k.Name}, "attribute": {""}},
			Type:  k.ValueType,
		})
	}
	return &flow.PortTypeRule{Cases: cases, Default: flow.PortAny}
}

// attributeValue pulls one attribute out of a control's value. A composite
// control that has not reported an attribute yet reads as nothing, which is a
// value that has not arrived rather than a binding that is wrong.
func attributeValue(snap controls.Snapshot, attribute string) (any, error) {
	attribute = strings.TrimSpace(attribute)
	if attribute == "" {
		return snap.Value, nil
	}
	def, ok := controls.FindKind(snap.Kind)
	if !ok {
		return nil, fmt.Errorf("control %q has an unknown kind %q", snap.Name, snap.Kind)
	}
	if _, ok := def.Attribute(attribute); !ok {
		return nil, fmt.Errorf("control %q is a %s and has no %q attribute", snap.Name, def.Label, attribute)
	}
	if !def.Composite() {
		return snap.Value, nil
	}
	fields, ok := snap.Value.(map[string]any)
	if !ok {
		return nil, nil
	}
	return fields[attribute], nil
}

// resolveControl looks up the configured control and reports a usable error
// when it is missing, which is what an author sees after deleting one out from
// under a graph.
func resolveControl(ref string) (controls.Snapshot, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return controls.Snapshot{}, fmt.Errorf("no control selected")
	}
	snap, ok := controls.S.Resolve(ref)
	if !ok {
		return controls.Snapshot{}, fmt.Errorf("control %q not found", ref)
	}
	return snap, nil
}

// ControlGetNode reads a control's current value. It is an output-only pure
// source: no input port and no failure branch, so it runs when a value edge
// pulls it or when a test run seeds from it, and a failure surfaces at the node
// that asked for the value.
type ControlGetNode struct{}

func (ControlGetNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "control-get",
		Label:             "Control Value",
		Folder:            flow.FolderControls,
		Description:       "Reads a control's current value, pulled when a consumer needs it",
		Icon:              "las la-sliders-h",
		Pure:              true,
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			// The payload is the value itself, so the port declares what the
			// chosen kind holds and a config field can bind straight to it.
			{ID: "out", Label: "Value", Type: flow.PortAny, TypeRule: controlValueTypeRule()},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:      "kind",
				Label:   "Value type",
				Type:    flow.FieldSelect,
				Default: controls.KindBoolean,
				Options: controlKindOptions(),
				Help: "What the control holds. It decides what this node's output port declares and " +
					"narrows the picker below to matching controls, so a value edge binding this to a " +
					"config field is checked before anything runs.",
			},
			controlField("Control to read. Its value becomes the payload, and its name the topic."),
			attributeField("Which part of the control to read. Leave it empty for the whole value, " +
				"which for a light or a thermostat is the object of everything it holds."),
		},
	}
}

// controlGetConfig is the decoded control-get node configuration.
type controlGetConfig struct {
	Kind      string `json:"kind"`
	Control   string `json:"control"`
	Attribute string `json:"attribute"`
}

// Execute emits the control's current value. A control whose kind no longer
// matches the declared one fails rather than emitting the wrong type: the port
// is a promise, and a control redefined under a graph has broken it.
func (ControlGetNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg controlGetConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	snap, err := resolveControl(cfg.Control)
	if err != nil {
		return nil, err
	}
	if cfg.Kind != "" && snap.Kind != cfg.Kind {
		return nil, fmt.Errorf("control %q is a %s but this node declares %s; reopen the node to fix it",
			snap.Name, snap.Kind, cfg.Kind)
	}

	value, err := attributeValue(snap, cfg.Attribute)
	if err != nil {
		return nil, err
	}

	meta := map[string]any{
		"control_id":   snap.ID.String(),
		"control_name": snap.Name,
		"control_kind": snap.Kind,
	}
	if attr := strings.TrimSpace(cfg.Attribute); attr != "" {
		meta["control_attribute"] = attr
	}
	return engine.One("out", &engine.Msg{Payload: value, Topic: snap.Name, Meta: meta}), nil
}

// ControlSetNode writes a control's value. Its input takes whatever arrives
// because the store coerces to the control's kind and refuses what the kind
// cannot hold — the check belongs where the kind is known, not on the wire.
type ControlSetNode struct{}

func (ControlSetNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "control-set",
		Label:       "Set Control",
		Folder:      flow.FolderControls,
		Description: "Writes a control's value, which every dashboard row showing it sees at once",
		Icon:        "las la-pen",
		Retryable:   true,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			// The message passes through untouched, so it reports whatever was
			// wired in rather than shrugging.
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []engine.FieldSpec{
			controlField("Control to write. A buttons control takes the value of the button being pressed."),
			attributeField("Which part of the control to write. Leave it empty for the whole value. " +
				"Setting one attribute leaves the rest where they were, so a flow can dim a light " +
				"without changing its colour."),
			{
				ID:      "source",
				Label:   "Value from",
				Type:    flow.FieldSelect,
				Default: "message",
				Options: []engine.Option{
					{Value: "message", Label: "Message property"},
					{Value: "value", Label: "Fixed value"},
				},
			},
			{
				ID:          "property",
				Label:       "Property",
				Type:        flow.FieldString,
				Default:     "payload",
				Placeholder: "payload",
				ShowIf:      &flow.ShowIf{Field: "source", Equals: "message"},
				Help:        "Dot path the value is read from, such as payload.brightness.",
			},
			{
				ID:          "value",
				Label:       "Value",
				Type:        flow.FieldString,
				Placeholder: "on",
				ShowIf:      &flow.ShowIf{Field: "source", Equals: "value"},
				Help:        "Value to write. {{ }} expressions are evaluated against the message first.",
			},
		},
	}
}

// controlSetConfig is the decoded control-set node configuration.
type controlSetConfig struct {
	Control   string `json:"control"`
	Attribute string `json:"attribute"`
	Source    string `json:"source"`
	Property  string `json:"property"`
	Value     string `json:"value"`
}

// runInfo is what the engine tells a node about its run beyond the
// ExecContext every node sees.
type runInfo interface {
	RunID() string
	FlowName() string
	ControlChain() (int, []string)
}

// flowOf is the flow a context runs in, empty where the engine does not say.
func flowOf(ec engine.ExecContext) string {
	if id, ok := ec.(engine.FlowIdentity); ok {
		return id.FlowID()
	}
	return ""
}

// flowWriter attributes a write to the running flow, carrying the run's place
// in a chain of control handoffs extended by this write.
func flowWriter(ec engine.ExecContext, control string) controls.Writer {
	w := controls.Writer{Source: controls.SourceFlow, FlowID: flowOf(ec)}
	if info, ok := ec.(runInfo); ok {
		w.RunID = info.RunID()
		depth, chain := info.ControlChain()
		w.Depth = depth
		w.Chain = append(chain, info.FlowName()+" set "+control)
	}
	return w
}

// Execute writes the value and passes the message through. The write is
// attributed to the flow, so a control trigger elsewhere can ignore what its
// own graph wrote and avoid chasing its tail.
func (ControlSetNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg controlSetConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)
	snap, err := resolveControl(cfg.Control)
	if err != nil {
		return nil, err
	}

	// Resolve the value to write.
	var value any
	if cfg.Source == "value" {
		rendered, err := ec.Interpolate(cfg.Value, msg)
		if err != nil {
			return nil, err
		}
		value = rendered
	} else {
		found := false
		value, found = resolveProperty(msg, normalizeProperty(cfg.Property))
		if !found {
			return nil, fmt.Errorf("message has no property %q to write", normalizeProperty(cfg.Property))
		}
	}

	// Persist it. A refusal is the control's kind rejecting the value, which is
	// a node failure and belongs on the error branch.
	attribute := strings.TrimSpace(cfg.Attribute)
	w := flowWriter(ec, snap.Name)
	w.Actor = events.ActorFrom(ctx)
	next, err := controls.S.SetAttribute(snap.ID, attribute, value, w)
	if err != nil {
		return nil, fmt.Errorf("cannot set control %q: %w", snap.Name, err)
	}

	out := passThrough(msg)
	if out.Meta == nil {
		out.Meta = map[string]any{}
	}
	out.Meta["control_id"] = next.ID.String()
	out.Meta["control_name"] = next.Name
	out.Meta["control_value"] = next.Value
	if attribute != "" {
		out.Meta["control_attribute"] = attribute
	}
	return engine.One("out", out), nil
}

// ControlChangedNode starts a run when a control moves. It is the other half of
// what a dashboard button used to do on its own: the dashboard writes a value
// and this decides which flows care, so one press can reach any number of them
// and the value stays visible after the run ends.
type ControlChangedNode struct{}

func (ControlChangedNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "control-changed",
		Label:             "Control Changed",
		Category:          flow.CategoryTrigger,
		Folder:            flow.FolderControls,
		Description:       "Starts a run when a control's value changes or a button control is pressed",
		Icon:              "las la-bolt",
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			// The payload carries both sides of the transition, the same shape
			// the Home Assistant state trigger emits, so a flow can act on the
			// edge rather than only on the new value.
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []engine.FieldSpec{
			controlField("Control to watch."),
			{
				ID:      "also_watch",
				Label:   "Also watch",
				Type:    flow.FieldString,
				Control: "control-list",
				Help: "More controls that start this trigger, each change carrying its own control. " +
					"One decision fed by several sources, such as presence from a phone and a Wi-Fi check, " +
					"needs one trigger, because an input takes one wire. The attribute, To, and From filters " +
					"apply to every control.",
			},
			attributeField("Only fire when this part of the control moves. Leave it empty to fire on " +
				"any change, which for a light includes its brightness and its colour."),
			{
				ID:          "to",
				Label:       "To",
				Type:        flow.FieldString,
				Placeholder: "true",
				Help:        "Only fire when the new value equals this. Empty fires on any change.",
			},
			{
				ID:          "from",
				Label:       "From",
				Type:        flow.FieldString,
				Placeholder: "false",
				Help:        "Only fire when the previous value equals this. Empty fires whatever it was.",
			},
			{
				ID:      "ignore_writes",
				Label:   "Ignore writes from",
				Type:    flow.FieldSelect,
				Default: ignoreOwnFlow,
				Options: []engine.Option{
					{Value: ignoreOwnFlow, Label: "This flow"},
					{Value: ignoreAnyFlow, Label: "Any flow"},
					{Value: ignoreNone, Label: "Nothing"},
				},
				Help: "This flow skips changes this flow's own Set Control nodes made, so another flow can hand " +
					"the control over. Any flow skips every flow's writes. Dashboard, API, and MCP writes always fire.",
			},
			{
				ID:      "loop_limit",
				Label:   "Loop limit",
				Type:    flow.FieldNumber,
				Default: defaultLoopLimit,
				Help: "Most handoffs in a chain of flows setting controls that start each other. A run past it " +
					"is skipped and logged with the chain, so two flows writing each other's controls stop.",
			},
			{
				ID:    "test_params",
				Label: "Test parameters",
				Type:  flow.FieldJSON,
				Help: "Payload the Test Run button seeds this trigger with. Left empty, a test run " +
					"reports the control's current value with no previous one.",
			},
		},
	}
}

// controlChangedConfig is the decoded control-changed node configuration.
type controlChangedConfig struct {
	Control      string `json:"control"`
	AlsoWatch    string `json:"also_watch"`
	Attribute    string `json:"attribute"`
	To           string `json:"to"`
	From         string `json:"from"`
	IgnoreWrites string `json:"ignore_writes"`
	LoopLimit    int    `json:"loop_limit"`
	// IgnoreFlowWrites is the setting before ignore_writes: true read as
	// this flow, false as nothing.
	IgnoreFlowWrites *bool `json:"ignore_flow_writes"`
}

// Ignore settings and loop limits of Control Changed.
const (
	ignoreOwnFlow    = "own_flow"
	ignoreAnyFlow    = "any_flow"
	ignoreNone       = "none"
	defaultLoopLimit = 8
	maxLoopLimit     = 32
)

// ignoring resolves the ignore setting, reading the older boolean when the
// new one is unset.
func (c controlChangedConfig) ignoring() string {
	switch c.IgnoreWrites {
	case ignoreOwnFlow, ignoreAnyFlow, ignoreNone:
		return c.IgnoreWrites
	}
	if c.IgnoreFlowWrites != nil && !*c.IgnoreFlowWrites {
		return ignoreNone
	}
	return ignoreOwnFlow
}

// loopLimit is the configured limit within bounds.
func (c controlChangedConfig) loopLimit() int {
	switch {
	case c.LoopLimit <= 0:
		return defaultLoopLimit
	case c.LoopLimit > maxLoopLimit:
		return maxLoopLimit
	}
	return c.LoopLimit
}

// Execute passes the run's initial message through: runs started by the armed
// trigger carry the change, and a test run without parameters reports the
// control's current value so the graph downstream has something real to work
// with.
func (ControlChangedNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs[engine.TriggerInput]
	if msg != nil && msg.Payload != nil {
		return engine.One("out", msg), nil
	}

	var cfg controlChangedConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	snap, err := resolveControl(cfg.Control)
	if err != nil {
		return nil, err
	}
	return engine.One("out", changeMsg(controls.Change{
		Control: snap,
		Old:     nil,
		Source:  controls.SourceAPI,
		At:      ec.Clock().Now(),
	})), nil
}

// Start implements engine.TriggerType: it watches the control store and emits
// a message per matching change until the context is canceled.
func (ControlChangedNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg controlChangedConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	snap, err := resolveControl(cfg.Control)
	if err != nil {
		return err
	}
	ignoring := cfg.ignoring()
	limit := cfg.loopLimit()
	ownFlow := flowOf(ec)

	// The From and To filters compare the part being watched, so a light's
	// brightness can be filtered on its own number rather than on the printed
	// form of the whole object. A scalar kind's only attribute is the value
	// itself and its changes carry no attribute, so the filter is dropped for
	// that control rather than left to skip every change.
	watched := map[uuid.UUID]string{}
	add := func(snap controls.Snapshot) {
		attribute := strings.TrimSpace(cfg.Attribute)
		if def, ok := controls.FindKind(snap.Kind); ok && !def.Composite() {
			attribute = ""
		}
		watched[snap.ID] = attribute
	}
	add(snap)
	for _, ref := range strings.Split(cfg.AlsoWatch, ",") {
		if ref = strings.TrimSpace(ref); ref == "" {
			continue
		}
		other, err := resolveControl(ref)
		if err != nil {
			return err
		}
		add(other)
	}
	unwatch := controls.S.Watch(func(c controls.Change) {
		attribute, ok := watched[c.Control.ID]
		if !ok {
			return
		}
		// An edit of the control's settings is announced with its value
		// unmoved, which is not a write to act on.
		if c.Definition && controls.SameValue(c.Old, c.Control.Value) {
			return
		}
		if c.Source == controls.SourceFlow {
			if ignoring == ignoreAnyFlow || (ignoring == ignoreOwnFlow && c.FlowID == ownFlow) {
				return
			}
		}
		// The watched part has to have moved, whichever part the write named:
		// a whole-value write that changes a location's state names none.
		if attribute != "" && controls.SameValue(changedValue(c.Old, attribute), changedValue(c.Control.Value, attribute)) {
			return
		}
		if cfg.To != "" && valueText(changedValue(c.Control.Value, attribute)) != cfg.To {
			return
		}
		if cfg.From != "" && valueText(changedValue(c.Old, attribute)) != cfg.From {
			return
		}
		if attribute != "" && c.Attribute == "" {
			c.Attribute = attribute
		}
		msg := changeMsg(c)
		if c.Source == controls.SourceFlow {
			// One more handoff in the chain; past the limit the chain is a
			// loop, reported instead of run.
			depth := c.Depth + 1
			msg.Meta["control_depth"] = depth
			msg.Meta["control_chain"] = c.Chain
			if depth > limit {
				reason := fmt.Sprintf("control loop: %s", strings.Join(c.Chain, " → "))
				ec.Logger().Warn("control loop stopped", zap.Int("depth", depth), zap.Strings("chain", c.Chain))
				if rec, ok := ec.(engine.TriggerRecorder); ok {
					rec.Skipped(msg, reason)
				}
				return
			}
		}
		emit(msg)
	})
	defer unwatch()

	<-ctx.Done()
	return nil
}

// changedValue narrows a control value to the attribute being watched, so a
// filter reads the number that moved rather than the object holding it.
func changedValue(value any, attribute string) any {
	if attribute == "" {
		return value
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return value
	}
	return fields[attribute]
}

// changeMsg renders a control change as the message the trigger emits.
func changeMsg(c controls.Change) *engine.Msg {
	payload := map[string]any{
		"control_id": c.Control.ID.String(),
		"name":       c.Control.Name,
		"kind":       c.Control.Kind,
		"value":      c.Control.Value,
		"old":        c.Old,
		"source":     c.Source,
	}
	if c.FlowID != "" {
		payload["flow_id"], payload["run_id"] = c.FlowID, c.RunID
	}
	meta := map[string]any{
		"control_id":   c.Control.ID.String(),
		"control_name": c.Control.Name,
		"control_kind": c.Control.Kind,
		"source":       c.Source,
	}
	// A composite control also reports which part moved and what that part now
	// reads, so a flow watching a light does not have to diff two objects.
	if c.Attribute != "" {
		payload["attribute"] = c.Attribute
		payload["attribute_value"] = changedValue(c.Control.Value, c.Attribute)
		payload["attribute_old"] = changedValue(c.Old, c.Attribute)
		meta["control_attribute"] = c.Attribute
	}
	return &engine.Msg{Payload: payload, Topic: c.Control.Name, Meta: meta}
}

// valueText renders a control value the way the From and To filters compare it,
// matching how a select field's literal reads in the editor.
func valueText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}
