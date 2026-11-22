// Package hanodes provides the Home Assistant trigger and action node types.
// All nodes resolve their connection through ExecContext.Connection and talk
// to the shared Home Assistant WebSocket driver, never to the instance
// directly.
package hanodes

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/expr"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"go.uber.org/zap"
)

// RegisterAll registers the Home Assistant node types on the given registry.
func RegisterAll(reg engine.NodeRegistrar) {
	reg.Register(AssistNode{})
	reg.Register(EventNode{})
	reg.Register(StateNode{})
	reg.Register(CallServiceNode{})
	reg.Register(GetStateNode{})
}

// connectRetryDelay is how long an armed trigger waits before retrying when
// its connection is not running yet.
const connectRetryDelay = 5 * time.Second

// conn is the driver surface the Home Assistant nodes need. Satisfied by
// *homeassistant.Driver; the compile-time assertion below keeps the two in
// sync.
type conn interface {
	CallService(ctx context.Context, domain, service string, serviceData map[string]any, target map[string]any) (any, error)
	GetStates(ctx context.Context) ([]homeassistant.Entity, error)
	SubscribeEvents(eventType string, handler func(event json.RawMessage)) (func(), error)
	SubscribeStateChanges(handler func(entityID string, oldState, newState json.RawMessage)) (func(), error)
}

var _ conn = (*homeassistant.Driver)(nil)

// resolveConn resolves a node's configured connection ID to the HA driver.
func resolveConn(ec engine.ExecContext, id string) (conn, error) {
	raw, err := ec.Connection(id)
	if err != nil {
		return nil, err
	}
	c, ok := raw.(conn)
	if !ok {
		return nil, fmt.Errorf("connection %s is not a Home Assistant connection", id)
	}
	return c, nil
}

// registryOf returns a connection's registry copy, or nil for a connection
// that keeps none.
func registryOf(c conn) *homeassistant.Registry {
	if r, ok := c.(interface {
		Registry() *homeassistant.Registry
	}); ok {
		return r.Registry()
	}
	return nil
}

// targetFields are the config fields naming entities by where they sit, as
// ha-state and ha-get-state take them.
func targetFields() []engine.FieldSpec {
	return []engine.FieldSpec{
		{ID: "area_id", Label: "Areas", Type: flow.FieldHATarget, Control: "ha-area",
			Help: "Entities in these areas. An entity with no area of its own sits in its device's area."},
		{ID: "floor_id", Label: "Floors", Type: flow.FieldHATarget, Control: "ha-floor", Help: "Entities in the areas on these floors."},
		{ID: "device_id", Label: "Devices", Type: flow.FieldHATarget, Control: "ha-device", Help: "Entities of these devices."},
		{ID: "label_id", Label: "Labels", Type: flow.FieldHATarget, Control: "ha-label", Help: "Entities with these labels, or whose device has them."},
		{ID: "domain", Label: "Only domain", Type: flow.FieldString, Placeholder: "binary_sensor",
			Help: "With an area, floor, device, or label: only entities of this domain."},
		{ID: "device_class", Label: "Only device class", Type: flow.FieldString, Placeholder: "motion",
			Help: "With an area, floor, device, or label: only entities of this device class."},
	}
}

// targetConfig is the decoded group target of ha-state and ha-get-state.
type targetConfig struct {
	AreaID      string `json:"area_id"`
	FloorID     string `json:"floor_id"`
	DeviceID    string `json:"device_id"`
	LabelID     string `json:"label_id"`
	Domain      string `json:"domain"`
	DeviceClass string `json:"device_class"`
}

// target reads the configured group target.
func (t targetConfig) target() homeassistant.Target {
	return homeassistant.Target{
		AreaIDs:     homeassistant.SplitIDs(t.AreaID),
		FloorIDs:    homeassistant.SplitIDs(t.FloorID),
		DeviceIDs:   homeassistant.SplitIDs(t.DeviceID),
		LabelIDs:    homeassistant.SplitIDs(t.LabelID),
		Domain:      strings.TrimSpace(t.Domain),
		DeviceClass: strings.TrimSpace(t.DeviceClass),
	}
}

// attributesOf reads a decoded state's attributes.
func attributesOf(state map[string]any) map[string]any {
	attrs, _ := state["attributes"].(map[string]any)
	return attrs
}

// waitForConn resolves the connection for an armed trigger, retrying on the
// injectable clock while the connection is missing or not running. Returns
// false when the context is canceled first.
func waitForConn(ctx context.Context, ec engine.ExecContext, id string) (conn, bool) {
	for {
		c, err := resolveConn(ec, id)
		if err == nil {
			return c, true
		}
		ec.Logger().Debug("waiting for Home Assistant connection", zap.Error(err))
		select {
		case <-ec.Clock().After(connectRetryDelay):
		case <-ctx.Done():
			return nil, false
		}
	}
}

// connectionField is the shared connection FieldSpec for all HA nodes.
func connectionField() engine.FieldSpec {
	return engine.FieldSpec{
		ID:             "connection",
		Label:          "Connection",
		Type:           flow.FieldConnection,
		Required:       true,
		ConnectionKind: homeassistant.Kind,
		Help:           "Home Assistant connection to use.",
	}
}

// splitEntityList splits a comma-separated entity_id config value.
func splitEntityList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// pickInput returns any connected input message, or an empty message.
func pickInput(inputs map[string]*engine.Msg) *engine.Msg {
	for _, m := range inputs {
		if m != nil {
			return m
		}
	}
	return &engine.Msg{}
}

// EventNode is the Home Assistant event trigger: while its flow is deployed
// and enabled it subscribes to an event type and starts a run per event.
type EventNode struct{}

func (EventNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:           "ha-event",
		Label:          "HA Event",
		Category:       flow.CategoryTrigger,
		Folder:         flow.FolderHomeAssistant,
		Description:    "Starts a run for every Home Assistant event of a type",
		Icon:           "las la-bolt",
		ConnectionKind: homeassistant.Kind,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []engine.FieldSpec{
			connectionField(),
			{
				ID:          "event_type",
				Label:       "Event type",
				Type:        flow.FieldString,
				Default:     "state_changed",
				Placeholder: "state_changed",
				Help:        "Home Assistant event type. Common types: state_changed, call_service, automation_triggered, script_started, homeassistant_start.",
			},
			{
				ID:    "test_params",
				Label: "Test parameters",
				Type:  flow.FieldJSON,
				Help: "Payload the Test Run button seeds this trigger with instead of waiting for a " +
					"real event. No Home Assistant traffic is involved.",
			},
		},
	}
}

// eventConfig is the decoded ha-event node configuration.
type eventConfig struct {
	Connection string `json:"connection"`
	EventType  string `json:"event_type"`
}

// Execute passes the run's initial message through: runs seeded by the armed
// trigger carry the received event, test runs carry test_params.
func (EventNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs[engine.TriggerInput]
	if msg == nil {
		msg = &engine.Msg{}
	}
	return engine.One("out", msg), nil
}

// Start implements engine.TriggerType: it subscribes to the configured event
// type and emits a message per event until the context is canceled.
func (EventNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg eventConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	if cfg.EventType == "" {
		cfg.EventType = "state_changed"
	}

	c, ok := waitForConn(ctx, ec, cfg.Connection)
	if !ok {
		return nil
	}

	unsubscribe, err := c.SubscribeEvents(cfg.EventType, func(event json.RawMessage) {
		// The port declares an object, so an event that will not decode is
		// dropped rather than emitted as something else — a trigger has no
		// error branch, same as mqtt-in.
		var payload any
		if err := json.Unmarshal(event, &payload); err != nil {
			ec.Logger().Warn("ha-event dropped an undecodable event",
				zap.String("event_type", cfg.EventType), zap.Error(err))
			return
		}
		emit(&engine.Msg{
			Payload: payload,
			Meta: map[string]any{
				"connection_id": cfg.Connection,
				"event_type":    cfg.EventType,
			},
		})
	})
	if err != nil {
		return fmt.Errorf("home assistant event subscribe failed: %w", err)
	}
	ec.Logger().Info("ha-event trigger armed", zap.String("event_type", cfg.EventType))

	<-ctx.Done()
	unsubscribe()
	return nil
}

// StateNode is the Home Assistant state-change trigger: it watches one or
// more entities and starts a run when their state (or one attribute) changes
// through the configured from/to filters.
type StateNode struct{}

func (StateNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:           "ha-state",
		Label:          "HA State",
		Category:       flow.CategoryTrigger,
		Folder:         flow.FolderHomeAssistant,
		Description:    "Starts a run when an entity's state or attribute changes",
		Icon:           "las la-exchange-alt",
		ConnectionKind: homeassistant.Kind,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject, PayloadSchema: statePayloadSchema()},
		},
		ConfigSpec: append([]engine.FieldSpec{
			connectionField(),
			{ID: "held_for", Label: "Held for", Type: flow.FieldDuration, Help: "Fire only after this continuous observed interval. Each entity has its own timer; a contrary change or connection gap cancels it. Empty fires immediately."},
			{
				ID:          "entity_id",
				Label:       "Entity",
				Type:        flow.FieldEntity,
				Placeholder: "light.kitchen",
				Help: "Entity to watch. A comma-separated list watches multiple entities. Leave empty to watch " +
					"by area, floor, device, or label instead; both together watch either.",
			},
		}, append(targetFields(), []engine.FieldSpec{
			{
				ID:          "attribute",
				Label:       "Attribute",
				Type:        flow.FieldString,
				Placeholder: "brightness",
				Help:        "Watch this attribute instead of the entity state.",
			},
			{
				ID:          "from",
				Label:       "From",
				Type:        flow.FieldString,
				Placeholder: "on",
				Help:        "Only fire when the previous value matches. Empty matches any value.",
			},
			{
				ID:          "to",
				Label:       "To",
				Type:        flow.FieldString,
				Placeholder: "off",
				Help:        "Only fire when the new value matches. Empty matches any value.",
			},
			{
				ID:    "ignore_caught_up",
				Label: "Ignore changes caught up after a reconnect",
				Type:  flow.FieldBoolean,
				Help: "After Home Assistant reconnects, each entity that changed while the connection was down is " +
					"reported once with its latest state. Turn this on to fire only for changes seen as they happened.",
			},
			{
				ID:    "test_params",
				Label: "Test parameters",
				Type:  flow.FieldJSON,
				Help: "Payload the Test Run button seeds this trigger with instead of waiting for a " +
					"real event. No Home Assistant traffic is involved.",
			},
		}...)...),
	}
}

// stateConfig is the decoded ha-state node configuration.
type stateConfig struct {
	targetConfig
	IgnoreCaughtUp bool   `json:"ignore_caught_up"`
	HeldFor        string `json:"held_for"`
	Connection     string `json:"connection"`
	EntityID       string `json:"entity_id"`
	Attribute      string `json:"attribute"`
	From           string `json:"from"`
	To             string `json:"to"`
}

// Execute passes the run's initial message through, like all triggers.
func (StateNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs[engine.TriggerInput]
	if msg == nil {
		msg = &engine.Msg{}
	}
	return engine.One("out", msg), nil
}

// Start implements engine.TriggerType: it subscribes to state_changed events
// and emits a message for every change passing the entity and from/to filters.
func (StateNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg stateConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	entities := splitEntityList(cfg.EntityID)
	group := cfg.target()
	if len(entities) == 0 && group.Empty() {
		return fmt.Errorf("ha-state node needs an entity, or an area, floor, device, or label to watch")
	}

	c, ok := waitForConn(ctx, ec, cfg.Connection)
	if !ok {
		return nil
	}
	// Membership is read from the registry at each change, so an entity moved
	// into a watched area counts from its next change with no re-arming.
	watches := func(entityID string, attrs map[string]any) bool {
		for _, id := range entities {
			if id == entityID {
				return true
			}
		}
		if group.Empty() {
			return false
		}
		reg := registryOf(c)
		return reg != nil && reg.Matches(group, entityID, attrs)
	}

	var held time.Duration
	var err error
	if strings.TrimSpace(cfg.HeldFor) != "" {
		held, err = parseDuration(cfg.HeldFor)
	}
	if err != nil || held < 0 {
		return fmt.Errorf("invalid held duration %q", cfg.HeldFor)
	}
	var mu sync.Mutex
	var waits sync.WaitGroup
	type hold struct{ cancel context.CancelFunc }
	pending := map[string]*hold{}
	stopping := false
	continuity, hasContinuity := c.(interface{ ConnectionGeneration() (uint64, bool) })
	if held > 0 && !hasContinuity {
		return fmt.Errorf("Home Assistant connection does not expose event continuity")
	}
	handle := func(entityID string, oldState, newState json.RawMessage, caughtUp bool) {
		if caughtUp && cfg.IgnoreCaughtUp {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if stopping || ctx.Err() != nil {
			return
		}
		attrs := attributesOf(decodeState(newState))
		if attrs == nil {
			attrs = attributesOf(decodeState(oldState))
		}
		if !watches(entityID, attrs) {
			return
		}
		if valueString(comparedValue(decodeState(oldState), cfg.Attribute)) == valueString(comparedValue(decodeState(newState), cfg.Attribute)) {
			return
		}
		if arm := pending[entityID]; arm != nil {
			arm.cancel()
			delete(pending, entityID)
		}
		msg, fire := evalStateChange(&cfg, []string{entityID}, entityID, oldState, newState)
		if !fire {
			return
		}
		if reg := registryOf(c); reg != nil {
			if info := reg.Describe(entityID); info.AreaID != "" || info.DeviceID != "" {
				payload := msg.Payload.(map[string]any)
				payload["area_id"] = info.AreaID
				payload["area"] = info.Area
				payload["device_id"] = info.DeviceID
			}
		}
		if caughtUp {
			msg.Payload.(map[string]any)["caught_up"] = true
			msg.Meta["caught_up"] = true
		}
		if held == 0 {
			emit(msg)
			return
		}
		generation, connected := continuity.ConnectionGeneration()
		if !connected {
			return
		}
		holdCtx, cancel := context.WithCancel(ctx)
		arm := &hold{cancel: cancel}
		pending[entityID] = arm
		deadline := ec.Clock().Now().Add(held)
		waits.Add(1)
		go func() {
			defer waits.Done()
			defer cancel()
			select {
			case <-holdCtx.Done():
				return
			case <-ec.Clock().After(deadline.Sub(ec.Clock().Now())):
			}
			mu.Lock()
			defer mu.Unlock()
			if pending[entityID] != arm {
				return
			}
			delete(pending, entityID)
			current, connected := continuity.ConnectionGeneration()
			if stopping || holdCtx.Err() != nil || !connected || current != generation {
				return
			}
			emit(msg)
		}()
	}
	// A connection that tells caught-up changes apart passes the mark on;
	// any other reports every change as live.
	var unsubscribe func()
	if detailed, ok := c.(interface {
		SubscribeStateChangesDetailed(func(homeassistant.StateChange)) (func(), error)
	}); ok {
		unsubscribe, err = detailed.SubscribeStateChangesDetailed(func(sc homeassistant.StateChange) {
			handle(sc.EntityID, sc.Old, sc.New, sc.CaughtUp)
		})
	} else {
		unsubscribe, err = c.SubscribeStateChanges(func(entityID string, oldState, newState json.RawMessage) {
			handle(entityID, oldState, newState, false)
		})
	}
	if err != nil {
		return fmt.Errorf("home assistant state subscribe failed: %w", err)
	}
	ec.Logger().Info("ha-state trigger armed", zap.Strings("entities", entities))
	if !group.Empty() {
		logGroupTarget(ctx, ec, c, group)
	}

	<-ctx.Done()
	mu.Lock()
	stopping = true
	for _, arm := range pending {
		arm.cancel()
	}
	mu.Unlock()
	unsubscribe()
	waits.Wait()
	return nil
}

// checkGroupTarget refuses a call that targets only by area, floor, device,
// or label and reaches nothing, which Home Assistant would accept and do
// nothing with. The service's domain narrows the match when entities of that
// domain exist, so turning off the lights in a room with only a speaker
// fails; a cross-domain service such as homeassistant.turn_off does not
// narrow. Without a loaded registry the call goes ahead unchecked.
func checkGroupTarget(ctx context.Context, c conn, domain string, target map[string]any) error {
	if _, named := target["entity_id"]; named {
		return nil
	}
	ids := func(key string) []string {
		list, _ := target[key].([]string)
		return list
	}
	group := homeassistant.Target{AreaIDs: ids("area_id"), FloorIDs: ids("floor_id"),
		DeviceIDs: ids("device_id"), LabelIDs: ids("label_id")}
	if group.Empty() {
		return nil
	}
	reg := registryOf(c)
	if reg == nil || !reg.Loaded(homeassistant.RegistryEntity) {
		return nil
	}
	states, err := c.GetStates(ctx)
	if err != nil {
		return nil
	}
	for _, e := range states {
		if strings.HasPrefix(e.EntityID, domain+".") {
			group.Domain = domain
			break
		}
	}
	if len(reg.Resolve(group, states)) == 0 {
		what := "entities"
		if group.Domain != "" {
			what = group.Domain + " entities"
		}
		return fmt.Errorf("no %s %s", what, reg.DescribeTarget(group))
	}
	return nil
}

// logGroupTarget says what an armed trigger's group target matches now, so a
// trigger watching an empty area says so rather than waiting silently.
func logGroupTarget(ctx context.Context, ec engine.ExecContext, c conn, group homeassistant.Target) {
	reg := registryOf(c)
	if reg == nil {
		ec.Logger().Warn("ha-state trigger watches an area, floor, device, or label, but this connection keeps no registry")
		return
	}
	states, err := c.GetStates(ctx)
	if err != nil {
		return
	}
	matched := reg.Resolve(group, states)
	if len(matched) == 0 {
		ec.Logger().Warn("watching " + reg.DescribeTarget(group) + ": no matching entities yet")
		return
	}
	ec.Logger().Info("watching "+reg.DescribeTarget(group), zap.Int("entities", len(matched)))
}

// evalStateChange applies the ha-state filters to one state_changed delivery
// and builds the trigger message when it should fire: the entity must be
// watched, the compared value (state, or one attribute) must actually change,
// and non-empty from/to filters must match the old and new values.
func evalStateChange(cfg *stateConfig, entities []string, entityID string, oldState, newState json.RawMessage) (*engine.Msg, bool) {
	watched := false
	for _, e := range entities {
		if e == entityID {
			watched = true
			break
		}
	}
	if !watched {
		return nil, false
	}

	oldObj := decodeState(oldState)
	newObj := decodeState(newState)
	oldVal := comparedValue(oldObj, cfg.Attribute)
	newVal := comparedValue(newObj, cfg.Attribute)

	// Skip no-op deliveries where the compared value did not change.
	oldStr := valueString(oldVal)
	newStr := valueString(newVal)
	if oldStr == newStr {
		return nil, false
	}
	if cfg.From != "" && oldStr != cfg.From {
		return nil, false
	}
	if cfg.To != "" && newStr != cfg.To {
		return nil, false
	}

	return &engine.Msg{
		Payload: map[string]any{
			"entity_id": entityID,
			"old_state": oldObj,
			"new_state": newObj,
			"old":       oldVal,
			"new":       newVal,
		},
		Topic: entityID,
		Meta: map[string]any{
			"entity_id":     entityID,
			"connection_id": cfg.Connection,
			"attribute":     cfg.Attribute,
		},
	}, true
}

// decodeState parses a state object; nil states (new or removed entities)
// decode to nil.
func decodeState(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	return obj
}

// comparedValue extracts the value the trigger compares: the state string, or
// one attribute when configured.
func comparedValue(state map[string]any, attribute string) any {
	if state == nil {
		return nil
	}
	if attribute == "" {
		return state["state"]
	}
	attrs, _ := state["attributes"].(map[string]any)
	if attrs == nil {
		return nil
	}
	return attrs[attribute]
}

// valueString renders a compared value for filter matching; nil becomes "".
func valueString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case float64:
		// fmt prints large floats in exponent form ("1e+06"), which no
		// filter typed as a plain number matches.
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

// CallServiceNode is the Home Assistant service call action.
type CallServiceNode struct{}

func (CallServiceNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:           "ha-call-service",
		Label:          "HA Call Service",
		Folder:         flow.FolderHomeAssistant,
		Description:    "Calls a Home Assistant service",
		Icon:           "las la-play-circle",
		ConnectionKind: homeassistant.Kind,
		Retryable:      true,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			{ID: "timeout", Label: "Confirmation timeout", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []engine.FieldSpec{
			connectionField(),
			{ID: "area_id", Label: "Target areas", Type: flow.FieldHATarget, Control: "ha-area", Placeholder: "kitchen,living_room", Help: "Act on every entity in these areas. Supports {{ }} expressions."},
			{ID: "floor_id", Label: "Target floors", Type: flow.FieldHATarget, Control: "ha-floor", Help: "Act on every entity on these floors. Supports {{ }} expressions."},
			{ID: "device_id", Label: "Target devices", Type: flow.FieldHATarget, Control: "ha-device", Help: "Act on these devices' entities. Supports {{ }} expressions."},
			{ID: "label_id", Label: "Target labels", Type: flow.FieldHATarget, Control: "ha-label", Help: "Act on every entity with these labels. Supports {{ }} expressions."},
			{ID: "confirm", Label: "Confirm observed state", Type: flow.FieldBoolean, Default: false, Help: "After an accepted call, wait for one explicit entity to report the expected state or attribute. A timeout does not resend the call."},
			{ID: "confirmation_entity_id", Label: "Confirmation entity", Type: flow.FieldEntity, Required: true, ShowIf: &flow.ShowIf{Field: "confirm", Equals: true}, Help: "Exactly one entity to observe, even when the command targets a group."},
			{ID: "confirmation_attribute", Label: "Confirmation attribute", Type: flow.FieldString, ShowIf: &flow.ShowIf{Field: "confirm", Equals: true}, Help: "Empty compares the entity state. Otherwise compare this attribute."},
			{ID: "confirmation_value", Label: "Expected value", Type: flow.FieldString, Required: true, ShowIf: &flow.ShowIf{Field: "confirm", Equals: true}, Help: "Expected state or attribute rendered as text. Supports {{ }} expressions."},
			{ID: "confirmation_timeout", Label: "Confirmation timeout", Type: flow.FieldDuration, Default: "30s", ShowIf: &flow.ShowIf{Field: "confirm", Equals: true}},
			{ID: "confirmation_interval", Label: "Confirmation poll interval", Type: flow.FieldDuration, Default: "1s", ShowIf: &flow.ShowIf{Field: "confirm", Equals: true}},
			{ID: "confirmation_stable_for", Label: "Confirmation held for", Type: flow.FieldDuration, ShowIf: &flow.ShowIf{Field: "confirm", Equals: true}, Help: "Require matching samples for this duration. Empty matches immediately."},
			{
				ID:          "domain",
				Label:       "Domain",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "switch",
				Help:        "Service domain, e.g. switch, light, scene. {{ }} expressions are interpolated.",
			},
			{
				ID:          "service",
				Label:       "Service",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "turn_off",
				Help:        "Service name within the domain, e.g. turn_on, turn_off, toggle. {{ }} expressions are interpolated.",
			},
			{
				ID:          "entity_id",
				Label:       "Entity",
				Type:        flow.FieldEntity,
				Placeholder: "switch.zigbee_plug",
				Help:        "Target entity. A comma-separated list targets multiple entities. {{ }} expressions are interpolated.",
			},
			{
				ID:          "data",
				Label:       "Service data",
				Type:        flow.FieldJSON,
				Placeholder: `{"brightness": 128}`,
				Help:        "Additional service data merged into the call. String values support {{ }} expressions; a value that is exactly one {{ }} expression keeps its evaluated type.",
			},
		},
	}
}

// callServiceConfig is the decoded ha-call-service node configuration.
type callServiceConfig struct {
	AreaID                string          `json:"area_id"`
	FloorID               string          `json:"floor_id"`
	DeviceID              string          `json:"device_id"`
	LabelID               string          `json:"label_id"`
	Confirm               bool            `json:"confirm"`
	ConfirmationEntityID  string          `json:"confirmation_entity_id"`
	ConfirmationAttribute string          `json:"confirmation_attribute"`
	ConfirmationValue     string          `json:"confirmation_value"`
	ConfirmationTimeout   string          `json:"confirmation_timeout"`
	ConfirmationInterval  string          `json:"confirmation_interval"`
	ConfirmationStableFor string          `json:"confirmation_stable_for"`
	Connection            string          `json:"connection"`
	Domain                string          `json:"domain"`
	Service               string          `json:"service"`
	EntityID              string          `json:"entity_id"`
	Data                  json.RawMessage `json:"data"`
}

// Execute calls the configured service and passes the input message through
// with the call result attached to Meta.ha_result. Home Assistant failures
// (success:false) surface as errors so the engine's retry policy and error
// branch apply.
func (CallServiceNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg callServiceConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if cfg.Domain == "" || cfg.Service == "" {
		return nil, fmt.Errorf("ha-call-service node needs a domain and service")
	}
	c, err := resolveConn(ec, cfg.Connection)
	if err != nil {
		return nil, err
	}
	in := pickInput(inputs)

	// Interpolate {{ }} expressions in the scalar call fields.
	domain, err := ec.Interpolate(cfg.Domain, in)
	if err != nil {
		return nil, fmt.Errorf("invalid domain template: %w", err)
	}
	service, err := ec.Interpolate(cfg.Service, in)
	if err != nil {
		return nil, fmt.Errorf("invalid service template: %w", err)
	}

	// Build service data from the config, interpolating string values.
	var serviceData map[string]any
	if len(cfg.Data) > 0 {
		if err := json.Unmarshal(cfg.Data, &serviceData); err != nil {
			return nil, fmt.Errorf("invalid service data: %w", err)
		}
		interpolated, err := interpolateData(ec, in, serviceData)
		if err != nil {
			return nil, fmt.Errorf("invalid service data template: %w", err)
		}
		serviceData, _ = interpolated.(map[string]any)
	}
	// Each target field is rendered exactly once. A second pass would evaluate
	// {{ }} text the first pass brought in from the message.
	target := map[string]any{}
	for _, field := range []struct{ id, value string }{{"entity_id", cfg.EntityID}, {"area_id", cfg.AreaID},
		{"floor_id", cfg.FloorID}, {"device_id", cfg.DeviceID}, {"label_id", cfg.LabelID}} {
		value, err := ec.Interpolate(field.value, in)
		if err != nil {
			return nil, fmt.Errorf("invalid %s template: %w", field.id, err)
		}
		if ids := splitEntityList(value); len(ids) > 0 {
			target[field.id] = ids
		}
	}
	if err := checkGroupTarget(ctx, c, domain, target); err != nil {
		return nil, err
	}
	var confirmationEntity, expected string
	var timeout, interval, stable time.Duration
	if cfg.Confirm {
		confirmationEntity, err = ec.Interpolate(cfg.ConfirmationEntityID, in)
		if err != nil {
			return nil, err
		}
		confirmationEntity = strings.TrimSpace(confirmationEntity)
		if confirmationEntity == "" || strings.Contains(confirmationEntity, ",") {
			return nil, fmt.Errorf("confirmation needs exactly one entity")
		}
		expected, err = ec.Interpolate(cfg.ConfirmationValue, in)
		if err != nil {
			return nil, err
		}
		timeout, err = confirmationDuration(cfg.ConfirmationTimeout, 30*time.Second)
		if err != nil {
			return nil, err
		}
		interval, err = confirmationDuration(cfg.ConfirmationInterval, time.Second)
		if err != nil {
			return nil, err
		}
		stable, err = confirmationDuration(cfg.ConfirmationStableFor, 0)
		if err != nil {
			return nil, err
		}
		if timeout <= 0 || interval <= 0 || stable < 0 || stable > timeout {
			return nil, fmt.Errorf("confirmation needs positive timeout and interval, and held duration between zero and timeout")
		}
	}

	result, err := c.CallService(ctx, domain, service, serviceData, target)
	if err != nil {
		return nil, err
	}

	// The result rides on a copy of the metadata: the input message belongs to
	// the run history, and mutating its Meta in place would rewrite what the
	// upstream node was recorded as emitting.
	out := &engine.Msg{
		Payload: in.Payload,
		Topic:   in.Topic,
		Meta:    nodeutil.CloneMeta(in.Meta),
		Group:   in.Group,
	}
	out.Meta["ha_result"] = result
	if cfg.Confirm {
		value, matched, err := nodeutil.WaitCondition(ctx, ec, timeout, interval, stable, func(ctx context.Context) (any, bool, error) {
			entities, err := c.GetStates(ctx)
			if err != nil {
				return nil, false, err
			}
			for _, entity := range entities {
				if entity.EntityID == confirmationEntity {
					var value any = entity.State
					if cfg.ConfirmationAttribute != "" {
						value = entity.Attributes[cfg.ConfirmationAttribute]
					}
					return value, value != nil && valueString(value) == expected, nil
				}
			}
			return nil, false, nil
		})
		if err != nil {
			return nil, &engine.ExecutionError{Message: fmt.Sprintf("confirming entity %q: %s", confirmationEntity, err), Cause: err, Retryable: false, Details: map[string]any{"action_accepted": true, "confirmation_entity": confirmationEntity}}
		}
		out.Meta["confirmation_entity"] = confirmationEntity
		out.Meta["confirmation_value"] = value
		out.Meta["confirmed"] = matched
		if !matched {
			return engine.One("timeout", out), nil
		}
	}
	return engine.One("out", out), nil
}

// interpolateData walks a decoded JSON value and interpolates {{ }} segments
// in every string. A string that is exactly one {{ }} expression is replaced
// by its evaluated value so numbers and booleans keep their type; all other
// strings interpolate textually.
func interpolateData(ec engine.ExecContext, msg *engine.Msg, v any) (any, error) {
	switch t := v.(type) {
	case string:
		if inner, ok := expr.SingleExpression(t); ok {
			return ec.Eval(inner, msg)
		}
		return ec.Interpolate(t, msg)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			res, err := interpolateData(ec, msg, val)
			if err != nil {
				return nil, err
			}
			out[k] = res
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			res, err := interpolateData(ec, msg, val)
			if err != nil {
				return nil, err
			}
			out[i] = res
		}
		return out, nil
	}
	return v, nil
}

// GetStateNode reads a Home Assistant entity's current state. It is an
// output-only value source: it never fires on its own, and it is pulled when a
// consuming node needs the state — memoized per run, so several consumers
// share one lookup. A failure surfaces at the node that pulled it.
type GetStateNode struct{}

func (GetStateNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "ha-get-state",
		Label:             "HA Get State",
		Folder:            flow.FolderHomeAssistant,
		Description:       "Reads the current state of a Home Assistant entity, pulled when a consumer needs it",
		Icon:              "las la-search",
		ConnectionKind:    homeassistant.Kind,
		Retryable:         true,
		Pure:              true,
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			// The lookup always emits the entity's state object.
			{ID: "out", Label: "Out", Type: flow.PortObject, PayloadSchema: getStatePayloadSchema()},
		},
		ConfigSpec: append([]engine.FieldSpec{
			connectionField(),
			{
				ID:          "entity_id",
				Label:       "Entity",
				Type:        flow.FieldEntity,
				Placeholder: "light.kitchen",
				Help: "Single entity to read. Home Assistant has no single-state WebSocket command, so all states are fetched and filtered. " +
					"Reads return the state Home Assistant holds right now: a service call is accepted before the device acts, so reading straight after " +
					"one usually returns the previous state. Add a Delay, or trigger on HA State, to observe the change. " +
					"Leave empty to read every entity in an area, floor, device, or label instead.",
			},
		}, targetFields()...),
	}
}

// getStateConfig is the decoded ha-get-state node configuration.
type getStateConfig struct {
	targetConfig
	Connection string `json:"connection"`
	EntityID   string `json:"entity_id"`
}

// Execute fetches all states and emits the configured entity's state object
// as the message payload. A missing entity is an error (retryable). The node
// takes no message inputs: it runs when pulled over a value edge, or as the
// seed of a test run.
func (GetStateNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg getStateConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	entityID := strings.TrimSpace(cfg.EntityID)
	group := cfg.target()
	if entityID == "" && group.Empty() {
		return nil, fmt.Errorf("ha-get-state node needs an entity, or an area, floor, device, or label to read")
	}
	c, err := resolveConn(ec, cfg.Connection)
	if err != nil {
		return nil, err
	}

	entities, err := c.GetStates(ctx)
	if err != nil {
		return nil, err
	}
	if entityID == "" {
		reg := registryOf(c)
		if reg == nil || !reg.Loaded(homeassistant.RegistryEntity) {
			return nil, fmt.Errorf("the Home Assistant registries are not loaded, so an area, floor, device, or label cannot be read")
		}
		states := []any{}
		for _, e := range reg.Resolve(group, entities) {
			states = append(states, stateObject(e))
		}
		return engine.One("out", &engine.Msg{
			Payload: map[string]any{"states": states, "count": len(states)},
			Topic:   reg.DescribeTarget(group),
		}), nil
	}
	for _, e := range entities {
		if e.EntityID == entityID {
			return engine.One("out", &engine.Msg{Payload: stateObject(e), Topic: e.EntityID}), nil
		}
	}
	return nil, fmt.Errorf("entity %q not found", entityID)
}

// stateObject is one entity's state as the get state node emits it.
func stateObject(e homeassistant.Entity) map[string]any {
	return map[string]any{
		"entity_id":    e.EntityID,
		"state":        e.State,
		"attributes":   e.Attributes,
		"last_changed": e.LastChanged,
		"last_updated": e.LastUpdated,
	}
}

// getStatePayloadSchema documents the closed entity envelope emitted by
// GetStateNode. Attribute names remain open because Home Assistant integrations
// define them per entity.
func getStatePayloadSchema() map[string]any {
	one := map[string]any{
		"type":     "object",
		"required": []string{"entity_id", "state", "attributes", "last_changed", "last_updated"},
		"properties": map[string]any{
			"entity_id":    map[string]any{"type": "string", "description": "Full Home Assistant entity ID."},
			"state":        map[string]any{"type": "string", "description": "Current state string reported by Home Assistant."},
			"attributes":   map[string]any{"type": []string{"object", "null"}, "additionalProperties": true, "description": "Integration-defined entity attributes."},
			"last_changed": map[string]any{"type": "string", "description": "Timestamp of the last state change."},
			"last_updated": map[string]any{"type": "string", "description": "Timestamp of the last state or attribute update."},
		},
		"additionalProperties": false,
		"description":          "Current Home Assistant entity state.",
	}
	return map[string]any{
		"anyOf": []any{one, map[string]any{
			"type":     "object",
			"required": []string{"states", "count"},
			"properties": map[string]any{
				"states": map[string]any{"type": "array", "items": one, "description": "Every entity the area, floor, device, or label target matches, by entity ID."},
				"count":  map[string]any{"type": "integer"},
			},
			"additionalProperties": false,
			"description":          "States of a group target.",
		}},
		"description": "One entity's state, or with an area, floor, device, or label target, every matching entity's state.",
	}
}

// statePayloadSchema documents the envelope emitted by evalStateChange.
func statePayloadSchema() map[string]any {
	state := map[string]any{
		"type": []string{"object", "null"},
		"properties": map[string]any{
			"state":      map[string]any{"type": "string", "description": "Entity state such as on, off, unknown, or unavailable."},
			"attributes": map[string]any{"type": "object", "additionalProperties": true},
		},
		"additionalProperties": true,
	}
	return map[string]any{
		"type": "object", "required": []string{"entity_id", "old_state", "new_state", "old", "new"},
		"properties": map[string]any{
			"entity_id": map[string]any{"type": "string"},
			"old_state": state, "new_state": state,
			"old":       map[string]any{"description": "Previous state string, or the selected attribute value. May be null."},
			"new":       map[string]any{"description": "New state string, or the selected attribute value. May be null. Compare payload.new for state routing; payload.new_state is the full object."},
			"caught_up": map[string]any{"type": "boolean", "description": "Present and true when the change was found after a reconnect: the latest state, not each transition the outage hid."},
			"area_id":   map[string]any{"type": "string", "description": "Area the entity sits in (its own, else its device's), when the registry knows it."},
			"area":      map[string]any{"type": "string", "description": "That area's name."},
			"device_id": map[string]any{"type": "string", "description": "Device the entity belongs to, when the registry knows it."},
		},
		"description": "HA state change. The to/from config filters match new/old. Topic is the entity ID. Use explicit on and off checks so unknown/unavailable states do not operate devices.",
	}
}

// confirmationDuration reads optional confirmation timing before issuing an action.
func confirmationDuration(raw string, fallback time.Duration) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	d, err := parseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid confirmation duration %q: %w", raw, err)
	}
	return d, nil
}

// parseDuration reads a duration field. A bare number is seconds, as the core
// nodes read their duration fields, so "30" means the same on every node.
func parseDuration(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if n, err := strconv.ParseFloat(raw, 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
		return time.Duration(n * float64(time.Second)), nil
	}
	return time.ParseDuration(raw)
}
