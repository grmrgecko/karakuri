package hanodes

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/expr"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeConn stands in for a Home Assistant websocket session. The real driver is
// exercised against a fake server in pkg/integrations/homeassistant; what these
// tests need is control over what a call returns, so the boundary is stubbed at
// the interface the nodes see and the calls are recorded to assert on.
type fakeConn struct {
	entities   []homeassistant.Entity
	callErr    error
	callResult any

	calls []recordedCall
}

type recordedCall struct {
	domain      string
	service     string
	serviceData map[string]any
	target      map[string]any
}

func (f *fakeConn) CallService(ctx context.Context, domain, service string, serviceData map[string]any, target map[string]any) (any, error) {
	f.calls = append(f.calls, recordedCall{domain: domain, service: service, serviceData: serviceData, target: target})
	if f.callErr != nil {
		return nil, f.callErr
	}
	return f.callResult, nil
}

func (f *fakeConn) GetStates(ctx context.Context) ([]homeassistant.Entity, error) {
	return f.entities, nil
}

func (f *fakeConn) SubscribeEvents(eventType string, handler func(event json.RawMessage)) (func(), error) {
	return func() {}, nil
}

func (f *fakeConn) SubscribeStateChanges(handler func(entityID string, oldState, newState json.RawMessage)) (func(), error) {
	return func() {}, nil
}

// fakeEC is the engine's real ExecContext interface with the minimum behind it,
// so a node can be run with no engine: expression evaluation and the node store
// are the production ones, everything else is inert.
type fakeEC struct {
	cfg     string
	conn    *fakeConn
	store   engine.NodeStore
	durable engine.NodeStore
}

func (f *fakeEC) Config(out any) error {
	if f.cfg == "" {
		return nil
	}
	return json.Unmarshal([]byte(f.cfg), out)
}
func (f *fakeEC) Logger() *zap.Logger { return zap.NewNop() }
func (f *fakeEC) Clock() engine.Clock { return engine.RealClock{} }
func (f *fakeEC) Eval(src string, msg *engine.Msg) (any, error) {
	return expr.Eval(src, fakeEnv(msg))
}
func (f *fakeEC) Interpolate(s string, msg *engine.Msg) (string, error) {
	return expr.Interpolate(s, fakeEnv(msg))
}

// fakeEnv mirrors the engine's message environment without a nodes map.
func fakeEnv(msg *engine.Msg) map[string]any {
	if msg == nil {
		msg = &engine.Msg{}
	}
	return expr.BuildEnv(expr.Env{Payload: msg.Payload, Topic: msg.Topic, Meta: msg.Meta})
}
func (f *fakeEC) NodeID() string         { return "n1" }
func (f *fakeEC) Wired(port string) bool { return true }
func (f *fakeEC) Ports() flow.PortSet {
	return flow.PortSet{
		Inputs:  []flow.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}},
		Outputs: []flow.PortSpec{{ID: "out", Label: "Out", Type: flow.PortAny}},
		Known:   true,
	}
}
func (f *fakeEC) Debug(payload any, note string)  {}
func (f *fakeEC) GetState(key string) (any, bool) { return nil, false }
func (f *fakeEC) SetState(key string, value any)  {}
func (f *fakeEC) GetVar(key string) (any, bool)   { return nil, false }
func (f *fakeEC) SetVar(key string, value any)    {}
func (f *fakeEC) Vars() map[string]any            { return map[string]any{} }

func (f *fakeEC) GetScoped(scope, key string) (any, bool)      { return nil, false }
func (f *fakeEC) SetScoped(scope, key string, value any) error { return nil }
func (f *fakeEC) ScopedVars(scope string) map[string]any       { return map[string]any{} }
func (f *fakeEC) Emit(port string, msg *engine.Msg)            {}
func (f *fakeEC) CallTool(ctx context.Context, toolNodeID string, args map[string]any, timeout time.Duration) (*engine.Msg, error) {
	return nil, errors.New("not supported in tests")
}
func (f *fakeEC) Connection(id string) (any, error) { return f.conn, nil }

// NodeStore hands out one store per fake context, created on first use.
func (f *fakeEC) NodeStore() engine.NodeStore {
	if f.store == nil {
		f.store = engine.NewMemNodeStore()
	}
	return f.store
}

func (f *fakeEC) DurableStore() engine.NodeStore {
	if f.durable == nil {
		f.durable = engine.NewMemDurableStore()
	}
	return f.durable
}

func (f *fakeEC) Park(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	select {
	case <-f.Clock().After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// only returns the single message a port emitted, or nil when the branch was
// not taken.
func only(out map[string][]*engine.Msg, port string) *engine.Msg {
	msgs := out[port]
	if len(msgs) == 0 {
		return nil
	}
	return msgs[0]
}

// raw builds a state object JSON for evalStateChange tests.
func raw(t *testing.T, state string, attrs map[string]any) json.RawMessage {
	t.Helper()
	obj := map[string]any{"entity_id": "x", "state": state}
	if attrs != nil {
		obj["attributes"] = attrs
	}
	b, err := json.Marshal(obj)
	require.NoError(t, err, "the state fixture must encode")
	return b
}

// TestEvalStateChange covers what decides whether an ha-state trigger fires.
// Home Assistant pushes every state_changed event on one subscription, so this
// filter runs on all of them and every row here is a way a flow fires when it
// should not — or stays silent when it should not.
//
// The attribute rows carry the case worth the most: watching an attribute means
// the entity's own state changing is irrelevant, so a light going off without
// its brightness moving must not fire a brightness trigger.
func TestEvalStateChange(t *testing.T) {
	schemaJSON, err := json.Marshal((StateNode{}).Meta().Outputs[0].PayloadSchema)
	require.NoError(t, err)
	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(schemaJSON, &schema))
	resolved, err := schema.Resolve(nil)
	require.NoError(t, err)
	entities := []string{"light.kitchen", "switch.plug"}
	base := &stateConfig{Connection: "c1"}

	cases := []struct {
		name     string
		cfg      stateConfig
		entityID string
		old, new json.RawMessage
		fire     bool
	}{
		{
			name:     "unwatched entity is ignored",
			cfg:      *base,
			entityID: "sensor.other",
			old:      raw(t, "on", nil), new: raw(t, "off", nil),
			fire: false,
		},
		{
			name:     "watched entity fires on state change",
			cfg:      *base,
			entityID: "light.kitchen",
			old:      raw(t, "on", nil), new: raw(t, "off", nil),
			fire: true,
		},
		{
			name:     "second entity of the comma list fires",
			cfg:      *base,
			entityID: "switch.plug",
			old:      raw(t, "on", nil), new: raw(t, "off", nil),
			fire: true,
		},
		{
			name:     "unchanged state is skipped",
			cfg:      *base,
			entityID: "light.kitchen",
			old:      raw(t, "on", nil), new: raw(t, "on", nil),
			fire: false,
		},
		{
			name:     "to filter matches",
			cfg:      stateConfig{To: "off"},
			entityID: "light.kitchen",
			old:      raw(t, "on", nil), new: raw(t, "off", nil),
			fire: true,
		},
		{
			name:     "to filter rejects",
			cfg:      stateConfig{To: "off"},
			entityID: "light.kitchen",
			old:      raw(t, "off", nil), new: raw(t, "on", nil),
			fire: false,
		},
		{
			name:     "from filter matches",
			cfg:      stateConfig{From: "on", To: "off"},
			entityID: "light.kitchen",
			old:      raw(t, "on", nil), new: raw(t, "off", nil),
			fire: true,
		},
		{
			name:     "from filter rejects",
			cfg:      stateConfig{From: "unavailable"},
			entityID: "light.kitchen",
			old:      raw(t, "on", nil), new: raw(t, "off", nil),
			fire: false,
		},
		{
			name:     "attribute change fires",
			cfg:      stateConfig{Attribute: "brightness"},
			entityID: "light.kitchen",
			old:      raw(t, "on", map[string]any{"brightness": 100}),
			new:      raw(t, "on", map[string]any{"brightness": 200}),
			fire:     true,
		},
		{
			name:     "unchanged attribute is skipped even when state changes",
			cfg:      stateConfig{Attribute: "brightness"},
			entityID: "light.kitchen",
			old:      raw(t, "on", map[string]any{"brightness": 100}),
			new:      raw(t, "off", map[string]any{"brightness": 100}),
			fire:     false,
		},
		{
			// A float64 of a million prints as "1e+06" through fmt, so the
			// filter the user typed as 1000000 never matched an energy total.
			name:     "attribute to filter matches a value of a million",
			cfg:      stateConfig{Connection: "c1", Attribute: "total", To: "1000000"},
			entityID: "light.kitchen",
			old:      raw(t, "on", map[string]any{"total": 999999}), new: raw(t, "on", map[string]any{"total": 1000000}),
			fire: true,
		},
		{
			name:     "attribute to filter matches numeric value as string",
			cfg:      stateConfig{Attribute: "brightness", To: "200"},
			entityID: "light.kitchen",
			old:      raw(t, "on", map[string]any{"brightness": 100}),
			new:      raw(t, "on", map[string]any{"brightness": 200}),
			fire:     true,
		},
		{
			name:     "new entity with nil old state fires",
			cfg:      *base,
			entityID: "light.kitchen",
			old:      json.RawMessage("null"), new: raw(t, "on", nil),
			fire: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg, fire := evalStateChange(&c.cfg, entities, c.entityID, c.old, c.new)
			require.Equal(t, c.fire, fire, "this event decides whether the flow runs at all")
			if !fire {
				return
			}

			require.NoError(t, resolved.Validate(msg.Payload), "the advertised payload schema must match state and attribute events")
			payload, ok := msg.Payload.(map[string]any)
			require.Truef(t, ok, "the fired payload is an object, got %T", msg.Payload)
			assert.Equal(t, c.entityID, payload["entity_id"],
				"a trigger watching several entities must say which one moved")
			assert.Equal(t, c.entityID, msg.Topic, "the topic names the entity, so one handler can serve several")
			assert.Contains(t, payload, "new_state", "the new state is what the flow acts on")
		})
	}
}

// TestCallServiceNodeExecute covers how a node's config becomes a Home
// Assistant service call. The entity field is free text an author types, so a
// comma-separated list has to become a real array of trimmed ids — Home
// Assistant would take the whole string as one entity name and act on nothing.
//
// The message passes through with the result in metadata rather than replacing
// the payload, so a flow can keep working on whatever it was carrying.
func TestCallServiceNodeExecute(t *testing.T) {
	fc := &fakeConn{callResult: map[string]any{"context": map[string]any{"id": "abc"}}}
	ec := &fakeEC{
		cfg: `{"connection":"c1","domain":"switch","service":"turn_off",
			"entity_id":"switch.plug_a, switch.plug_b","data":{"transition":2}}`,
		conn: fc,
	}
	in := &engine.Msg{Payload: map[string]any{"hello": "world"}}

	outputs, err := CallServiceNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": in})
	require.NoError(t, err, "a well-formed service call must succeed")

	require.Len(t, fc.calls, 1, "exactly one call must be made")
	call := fc.calls[0]
	assert.Equal(t, "switch", call.domain, "the configured domain is what is called")
	assert.Equal(t, "turn_off", call.service, "the configured service is what is called")
	assert.Equal(t, float64(2), call.serviceData["transition"],
		"the configured data must travel as service data, which is a separate field from the target")
	assert.Equal(t, []string{"switch.plug_a", "switch.plug_b"}, call.target["entity_id"],
		"a comma list must become trimmed ids: sent whole, Home Assistant reads it as one entity and acts on nothing")

	out := only(outputs, "out")
	require.NotNil(t, out, "a service call node passes the message on rather than ending the branch")
	assert.Equal(t, "world", out.Payload.(map[string]any)["hello"],
		"the payload must pass through, so the flow keeps whatever it was carrying")
	assert.NotNil(t, out.Meta["ha_result"],
		"the result goes in metadata rather than replacing the payload, so both survive")
}

// TestCallServiceNodeExecuteError covers the two ways the call cannot happen,
// against the working call above. Both fail the node so a retry policy and a
// failure branch apply — a device command that quietly did nothing is the worst
// outcome available, since the flow carries on as though the house changed.
func TestCallServiceNodeExecuteError(t *testing.T) {
	fc := &fakeConn{callErr: context.DeadlineExceeded}
	ec := &fakeEC{cfg: `{"connection":"c1","domain":"switch","service":"turn_off"}`, conn: fc}

	_, err := CallServiceNode{}.Execute(context.Background(), ec, nil)
	assert.Error(t, err, "a call Home Assistant refused must fail the node, or the flow carries on as though it worked")

	ec = &fakeEC{cfg: `{"connection":"c1"}`, conn: fc}
	_, err = CallServiceNode{}.Execute(context.Background(), ec, nil)
	assert.Error(t, err, "with no domain or service there is nothing to call, and there is no sensible default")
}

// TestGetStateNodeExecute covers reading one entity out of the whole state set.
// The node is an output-only pure source, so it runs with no input at all —
// this is what a value edge pulls when a config field is bound to an entity.
//
// An entity that is not there fails rather than emitting an empty state,
// because "off" and "not present" lead a flow to do different things, and the
// error names the entity since a flow may read several.
func TestGetStateNodeExecute(t *testing.T) {
	fc := &fakeConn{entities: []homeassistant.Entity{
		{EntityID: "light.kitchen", State: "on", Attributes: map[string]any{"friendly_name": "Kitchen"}},
		{EntityID: "switch.plug", State: "off"},
	}}
	ec := &fakeEC{cfg: `{"connection":"c1","entity_id":"light.kitchen"}`, conn: fc}

	outputs, err := GetStateNode{}.Execute(context.Background(), ec, nil)
	require.NoError(t, err, "a pure source runs with no input, which is how a value edge pulls it")

	out := only(outputs, "out")
	require.NotNil(t, out, "reading an entity must emit its state")
	payload, ok := out.Payload.(map[string]any)
	require.Truef(t, ok, "the payload is an object, got %T", out.Payload)

	assert.Equal(t, "light.kitchen", payload["entity_id"], "the named entity is the one read, not the first in the set")
	assert.Equal(t, "on", payload["state"], "the state is what the flow acts on")
	assert.Equal(t, "light.kitchen", out.Topic, "the topic names the entity, so a flow reading several can tell them apart")

	ec = &fakeEC{cfg: `{"connection":"c1","entity_id":"sensor.missing"}`, conn: fc}
	_, err = GetStateNode{}.Execute(context.Background(), ec, nil)
	require.Error(t, err,
		`an entity that is not there must fail: "off" and "not present" lead a flow to do different things`)
	assert.Contains(t, err.Error(), "sensor.missing",
		"the error must name the entity, since a flow may read several and the author has to know which")
}

// TestSplitEntityList pins how the entity field's free text is read. Every
// oddity here comes from a person typing into a text box: spaces after commas,
// a stray empty item from a deleted entry, and whitespace at the ends from a
// copy and paste. An id left untrimmed is an id Home Assistant does not
// recognise, and it fails silently — the service call simply acts on nothing.
func TestSplitEntityList(t *testing.T) {
	assert.Equal(t,
		[]string{"light.kitchen", "switch.plug", "sensor.x"},
		splitEntityList(" light.kitchen, switch.plug ,,sensor.x "),
		"ids must be trimmed and empty items dropped: an untrimmed id is one Home Assistant silently ignores")
}

// TestCallServiceRendersMessageTextOnce locks that text arriving in a message
// stays data. The entity field was rendered twice, so a payload value holding
// {{ }} was evaluated as an expression on the second pass: a webhook body could
// read flow state or redirect the call to another entity.
func TestCallServiceRendersMessageTextOnce(t *testing.T) {
	fc := &fakeConn{}
	ec := &fakeEC{
		cfg:  `{"connection":"c1","domain":"light","service":"turn_on","entity_id":"{{ payload.entity }}"}`,
		conn: fc,
	}
	in := &engine.Msg{Payload: map[string]any{"entity": "light.{{ 1 + 1 }}"}}

	_, err := CallServiceNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": in})
	require.NoError(t, err, "a rendered entity must be sent as it is")

	require.Len(t, fc.calls, 1, "exactly one call must be made")
	assert.Equal(t, []string{"light.{{ 1 + 1 }}"}, fc.calls[0].target["entity_id"],
		"the payload's text must reach Home Assistant unevaluated, because only the author's template is an expression")
}
