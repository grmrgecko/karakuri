package core

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/controls"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupControls runs the real migrations against a real SQLite database held in
// memory, then empties the controls table. The nodes below read and write
// through the same store the server uses, so the coercion and persistence they
// depend on are the production ones rather than a stand-in.
func setupControls(t *testing.T) {
	t.Helper()

	require.NoError(t, db.Init(db.Options{
		Type:         "sqlite",
		DSN:          memdb.TestDB(t),
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}), "the tests need a database before a control can exist")
	require.NoError(t, migrate.AutoMigrate(), "the controls table is created by the real migrations")
	require.NoError(t, db.DB.Exec("DELETE FROM controls").Error,
		"each test starts from an empty set, since the in-memory database is shared")
	require.NoError(t, controls.S.Load(), "the store reads the table into memory at startup")
}

// TestControlGetEmitsValue covers reading a control. The node is an output-only
// pure source, so it runs with no input at all — this is what a value edge
// pulls when a config field is bound to a control. The topic carries the
// control's name so a flow reading several can tell them apart.
func TestControlGetEmitsValue(t *testing.T) {
	setupControls(t)

	snap, err := controls.S.Create("Brightness", controls.KindNumber, "", map[string]any{
		"min": float64(0), "max": float64(100),
	}, false)
	require.NoError(t, err, "the test needs a control to read")
	_, err = controls.S.Set(snap.ID, 42, controls.By(controls.SourceAPI))
	require.NoError(t, err, "the control needs a value to read back")

	cfg := `{"kind":"number","control":"` + snap.ID.String() + `"}`
	out, err := ControlGetNode{}.Execute(context.Background(), newTestCtx(cfg, nil), nil)
	require.NoError(t, err, "a pure source runs with no input, which is how a value edge pulls it")

	require.NotEmpty(t, out["out"], "reading a control must emit its value")
	msg := out["out"][0]
	assert.Equal(t, float64(42), msg.Payload, "the stored value is what the node emits")
	assert.Equal(t, "Brightness", msg.Topic,
		"the topic names the control, so a flow reading several can tell them apart")
}

// TestControlGetRefusesKindMismatch covers a control redefined underneath a
// graph that reads it. The node's port declared a number because that is the
// kind it was configured against; emitting the boolean the control now holds
// would break the promise the port made to everything downstream, so it fails
// instead.
func TestControlGetRefusesKindMismatch(t *testing.T) {
	setupControls(t)

	snap, err := controls.S.Create("Away", controls.KindBoolean, "", nil, false)
	require.NoError(t, err, "the test needs a control of the wrong kind")

	cfg := `{"kind":"number","control":"` + snap.ID.String() + `"}`
	_, err = ControlGetNode{}.Execute(context.Background(), newTestCtx(cfg, nil), nil)
	assert.Error(t, err,
		"the port promised a number, so a control redefined as a boolean must fail rather than emit the wrong type")
}

// TestControlGetPortTypeFollowsKind covers the port type derived from the
// chosen kind. It is what a value edge is checked against at save time, so a
// node that declared "any" would let a control be bound to a field it can never
// satisfy — and the failure would only appear at run time.
func TestControlGetPortTypeFollowsKind(t *testing.T) {
	meta := ControlGetNode{}.Meta()
	cases := map[string]string{
		`{"kind":"boolean"}`:  flow.PortBool,
		`{"kind":"number"}`:   flow.PortNumber,
		`{"kind":"select"}`:   flow.PortString,
		`{"kind":"datetime"}`: flow.PortString,
		`{}`:                  flow.PortAny,
	}
	for cfg, want := range cases {
		ports := flow.NodePorts(meta, &flow.Node{Type: meta.Type, Config: json.RawMessage(cfg)}, nil)
		port, ok := ports.Output("out")
		require.Truef(t, ok, "config %s: the node always has an output", cfg)
		assert.Equalf(t, want, port.Type, "config %s: the port type must follow the chosen kind", cfg)
	}
}

// TestControlSetWritesFromMessage covers writing a control from a value the
// message carried, which is how a flow records what it decided so a dashboard
// can show it. The write is re-read from the store rather than trusted from the
// return value, and the message passes through so the flow can continue.
func TestControlSetWritesFromMessage(t *testing.T) {
	setupControls(t)

	snap, err := controls.S.Create("House Mode", controls.KindSelect, "", map[string]any{
		"options": []any{"Home", "Away", "Night"},
	}, false)
	require.NoError(t, err, "the test needs a control to write")

	cfg := `{"control":"` + snap.ID.String() + `","source":"message","property":"payload.mode"}`
	in := map[string]*engine.Msg{"in": {Payload: map[string]any{"mode": "Night"}}}
	out, err := ControlSetNode{}.Execute(context.Background(), newTestCtx(cfg, nil), in)
	require.NoError(t, err, "Night is one of the declared options and must be accepted")

	stored, ok := controls.S.Get(snap.ID)
	require.True(t, ok, "the control must still exist after the write")
	assert.Equal(t, "Night", stored.Value, "the value is read back from the store, not taken on trust")

	require.NotEmpty(t, out["out"], "a set node passes the message on rather than ending the branch")
	payload, ok := out["out"][0].Payload.(map[string]any)
	require.True(t, ok, "the message passes through as it arrived")
	assert.Equal(t, "Night", payload["mode"], "writing a control must not consume or rewrite the message")
}

// TestControlSetRefusesValueTheKindCannotHold covers the kind's own bounds. A
// control declared 0..10 refuses 99 rather than storing it: coercion lives with
// the kind, so a value it cannot hold fails the node onto its error branch
// instead of being written and read back wrong by every dashboard showing it.
func TestControlSetRefusesValueTheKindCannotHold(t *testing.T) {
	setupControls(t)

	snap, err := controls.S.Create("Level", controls.KindNumber, "", map[string]any{
		"min": float64(0), "max": float64(10),
	}, false)
	require.NoError(t, err, "the test needs a bounded control")

	cfg := `{"control":"` + snap.ID.String() + `","source":"value","value":"99"}`
	_, err = ControlSetNode{}.Execute(context.Background(), newTestCtx(cfg, nil), map[string]*engine.Msg{"in": {}})
	require.Error(t, err, "99 is outside the declared 0..10 and must be refused rather than stored")

	stored, ok := controls.S.Get(snap.ID)
	require.True(t, ok, "the control must still exist after a refused write")
	assert.NotEqual(t, float64(99), stored.Value, "a refused write must leave nothing behind")
}

// TestControlChangedFiresOnMatchingEdge covers the trigger's two filters at
// once, because they can only be told apart by the writes that do *not* fire
// it. The node asks for the edge into true, and skips writes a flow made:
//
//   - a flow write to true is skipped, which is what stops two flows chasing
//     each other around one control
//   - a dashboard write to false is the wrong edge
//   - a dashboard write to true is the one it asked for
//
// Only the third may produce a message, and the payload has to carry both the
// old and new value so a flow can tell which direction it moved.
func TestControlChangedFiresOnMatchingEdge(t *testing.T) {
	setupControls(t)

	snap, err := controls.S.Create("Porch Light", controls.KindBoolean, "", nil, false)
	require.NoError(t, err, "the test needs a control to watch")

	cfg := `{"control":"` + snap.ID.String() + `","to":"true","ignore_flow_writes":true}`
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msgs := make(chan *engine.Msg, 4)
	started := make(chan struct{})
	go func() {
		close(started)
		_ = ControlChangedNode{}.Start(ctx, newTestCtx(cfg, nil), func(m *engine.Msg) { msgs <- m })
	}()
	<-started
	// Start registers its watcher before blocking; give it the moment it needs
	// so the writes below are observed.
	time.Sleep(50 * time.Millisecond)

	// A flow's own write is skipped, so two flows cannot chase each other.
	_, err = controls.S.Set(snap.ID, true, controls.By(controls.SourceFlow))
	require.NoError(t, err, "the flow write must be accepted by the store, whatever the trigger does with it")

	_, err = controls.S.Set(snap.ID, false, controls.By(controls.SourceDashboard))
	require.NoError(t, err, "the reset must be accepted")

	// This is the edge the node asked for.
	_, err = controls.S.Set(snap.ID, true, controls.By(controls.SourceDashboard))
	require.NoError(t, err, "the matching write must be accepted")

	select {
	case msg := <-msgs:
		payload, ok := msg.Payload.(map[string]any)
		require.Truef(t, ok, "payload = %T, want an object a flow can read fields off", msg.Payload)

		assert.Equal(t, true, payload["value"], "the new value is what the flow acts on")
		assert.Equal(t, false, payload["old"],
			"the previous value must be carried too, or a flow cannot tell which direction the control moved")
		assert.Equal(t, controls.SourceDashboard, payload["source"],
			"the source names who wrote it, which is how a flow can ignore its own writes")
	case <-time.After(2 * time.Second):
		require.FailNow(t, "the trigger never fired on the edge it asked for")
	}

	select {
	case extra := <-msgs:
		assert.Fail(t, "the trigger fired on a write it should have skipped",
			"only the dashboard write to true matches; the flow write and the reset to false do not: %v", extra.Payload)
	default:
	}
}

// TestControlChangedAttributeFiresWhenThatPartMoves covers the attribute
// filter on a composite control. It fires when the watched part's value moves,
// whichever part the write named: a whole-value write that changes a
// location's state names no attribute, and a presence flow watching state must
// still see it. A fix that only moves coordinates must not fire it.
func TestControlChangedAttributeFiresWhenThatPartMoves(t *testing.T) {
	setupControls(t)

	person, err := controls.S.Create("Mikinzi", controls.KindLocation, "", nil, false)
	require.NoError(t, err, "the test needs a location to watch")
	lamp, err := controls.S.Create("Desk Lamp", controls.KindLight, "", nil, false)
	require.NoError(t, err, "the test needs a light to watch")

	watch := func(id, attribute string) chan *engine.Msg {
		cfg := `{"control":"` + id + `","attribute":"` + attribute + `","ignore_flow_writes":false}`
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		msgs := make(chan *engine.Msg, 8)
		go func() {
			_ = ControlChangedNode{}.Start(ctx, newTestCtx(cfg, nil), func(m *engine.Msg) { msgs <- m })
		}()
		return msgs
	}
	states := watch(person.ID.String(), "state")
	brightness := watch(lamp.ID.String(), "brightness")
	time.Sleep(50 * time.Millisecond)

	writes := []any{
		map[string]any{"state": "work", "latitude": 41.9},
		map[string]any{"latitude": 41.91},
		map[string]any{"state": "home", "latitude": 41.88},
	}
	for _, w := range writes {
		_, err := controls.S.Set(person.ID, w, controls.By(controls.SourceAPI))
		require.NoError(t, err)
	}
	_, err = controls.S.Set(lamp.ID, map[string]any{"on": true, "brightness": 40}, controls.By(controls.SourceAPI))
	require.NoError(t, err)

	read := func(ch chan *engine.Msg) map[string]any {
		select {
		case m := <-ch:
			return m.Payload.(map[string]any)
		case <-time.After(2 * time.Second):
			require.FailNow(t, "the trigger did not fire")
			return nil
		}
	}
	first := read(states)
	assert.Equal(t, "state", first["attribute"], "the watched part is named even when the write named none")
	assert.Equal(t, "work", first["attribute_value"])
	assert.Nil(t, first["attribute_old"], "a new location had no state")
	second := read(states)
	assert.Equal(t, "home", second["attribute_value"])
	assert.Equal(t, "work", second["attribute_old"])
	select {
	case extra := <-states:
		assert.Fail(t, "a coordinate-only write fired a state watcher", "%v", extra.Payload)
	default:
	}

	lit := read(brightness)
	assert.Equal(t, float64(40), lit["attribute_value"], "a whole-value write that moves brightness fires a brightness watcher")
}

// TestControlChangedWatchesSeveralControls covers Also watch. A decision fed
// by several sources needs one trigger, since an input takes one wire, so the
// trigger fires for a change of any listed control, carrying that control,
// and nothing else.
func TestControlChangedWatchesSeveralControls(t *testing.T) {
	setupControls(t)

	phone, err := controls.S.Create("Phone home", controls.KindBoolean, "", nil, false)
	require.NoError(t, err)
	wifi, err := controls.S.Create("Wifi home", controls.KindBoolean, "", nil, false)
	require.NoError(t, err)
	other, err := controls.S.Create("Porch light", controls.KindBoolean, "", nil, false)
	require.NoError(t, err)

	cfg := `{"control":"` + phone.ID.String() + `","also_watch":"` + wifi.ID.String() + `","ignore_flow_writes":false}`
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msgs := make(chan *engine.Msg, 8)
	go func() {
		_ = ControlChangedNode{}.Start(ctx, newTestCtx(cfg, nil), func(m *engine.Msg) { msgs <- m })
	}()
	time.Sleep(50 * time.Millisecond)

	for _, id := range []uuid.UUID{wifi.ID, other.ID, phone.ID} {
		_, err := controls.S.Set(id, true, controls.By(controls.SourceAPI))
		require.NoError(t, err)
	}
	var names []string
	for i := 0; i < 2; i++ {
		select {
		case m := <-msgs:
			names = append(names, m.Payload.(map[string]any)["name"].(string))
		case <-time.After(2 * time.Second):
			require.FailNow(t, "the trigger did not fire for a watched control")
		}
	}
	assert.Equal(t, []string{"Wifi home", "Phone home"}, names, "each change carries the control that moved")
	select {
	case extra := <-msgs:
		assert.Fail(t, "an unwatched control fired the trigger", "%v", extra.Payload)
	default:
	}
}

// TestControlChangedIgnoresDefinitionEdits covers renaming a watched control.
// The store announces an edit of a control's settings with its value unmoved,
// and for a buttons control that announcement is indistinguishable from a
// repeated press unless the trigger reads the definition flag. A rename must
// start no run; the next real write still must.
func TestControlChangedIgnoresDefinitionEdits(t *testing.T) {
	setupControls(t)

	snap, err := controls.S.Create("Porch Light", controls.KindBoolean, "", nil, false)
	require.NoError(t, err, "the test needs a control to watch")

	cfg := `{"control":"` + snap.ID.String() + `"}`
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msgs := make(chan *engine.Msg, 4)
	go func() {
		_ = ControlChangedNode{}.Start(ctx, newTestCtx(cfg, nil), func(m *engine.Msg) { msgs <- m })
	}()
	// Start registers its watcher before blocking; give it the moment it needs.
	time.Sleep(50 * time.Millisecond)

	_, err = controls.S.Update(snap.ID, "Front Porch Light", controls.KindBoolean, "", nil, false)
	require.NoError(t, err, "the rename must be accepted by the store")
	_, err = controls.S.Set(snap.ID, true, controls.By(controls.SourceDashboard))
	require.NoError(t, err, "the write must be accepted")

	select {
	case msg := <-msgs:
		payload, ok := msg.Payload.(map[string]any)
		require.Truef(t, ok, "payload = %T, want an object", msg.Payload)
		assert.Equal(t, true, payload["value"],
			"the first run must come from the write to true, not from the rename that moved nothing")
	case <-time.After(2 * time.Second):
		require.FailNow(t, "the trigger never fired on the write that followed the rename")
	}
	select {
	case extra := <-msgs:
		assert.Fail(t, "the trigger fired twice", "one write was made after the rename: %v", extra.Payload)
	default:
	}
}
