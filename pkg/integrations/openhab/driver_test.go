package openhab

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/openhab/openhabtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startDriver connects a driver to a fake house and waits for it.
func startDriver(t *testing.T, srv *openhabtest.Server, token string) *Driver {
	t.Helper()
	cfg, _ := json.Marshal(Config{BaseURL: srv.URL + "/rest/", Token: token})
	built, err := New(integrations.ConnModel{ID: uuid.New(), Kind: Kind, Config: cfg}, integrations.Deps{})
	require.NoError(t, err)
	d := built.(*Driver)
	d.initialBackoff = 20 * time.Millisecond
	d.rereadDebounce = 20 * time.Millisecond
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	waitFor(t, func() bool { return d.Connected() }, "the driver never connected")
	return d
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		require.True(t, time.Now().Before(deadline), msg)
		time.Sleep(10 * time.Millisecond)
	}
}

// changes collects item changes.
type changes struct {
	mu   sync.Mutex
	list []ItemChange
}

func (c *changes) add(ch ItemChange) { c.mu.Lock(); c.list = append(c.list, ch); c.mu.Unlock() }
func (c *changes) all() []ItemChange {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ItemChange(nil), c.list...)
}

// TestConnectAndLocate covers the first connect: the version, the item copy,
// and where each item sits, through its own location, its equipment's, or
// its groups, with items in no location last.
func TestConnectAndLocate(t *testing.T) {
	srv := openhabtest.New()
	t.Cleanup(srv.Close)
	srv.House()
	d := startDriver(t, srv, "")

	assert.Equal(t, "5.2.3", d.Version())
	items := d.Items()
	assert.Len(t, items, 18)
	for name, want := range map[string]Place{
		"KitchenSwitch": {Location: "gKitchen", LocationLabel: "Kitchen", Floor: "gGround", FloorLabel: "Ground floor"},
		"LivingDimmer":  {Location: "gLiving", LocationLabel: "Living room", Floor: "gGround", FloorLabel: "Ground floor"},
		"Volume":        {Location: "gLiving", LocationLabel: "Living room", Floor: "gGround", FloorLabel: "Ground floor"},
		"FrontDoor":     {Location: "gGround", LocationLabel: "Ground floor", Floor: "gGround", FloorLabel: "Ground floor"},
		"NoLocation":    {},
	} {
		assert.Equal(t, want, items.Locate(name), name)
	}
	assert.True(t, items.InLocation("KitchenSwitch", "gGround"), "a room's items are on its floor")
	assert.True(t, items.MemberOf("LivingDimmer", "gLiving"), "membership counts at any depth")
	sorted := items.Sorted()
	assert.Empty(t, items.Locate(sorted[len(sorted)-1].Name).Location, "items in no location come last")
	assert.Equal(t, "Ground floor", items.Locate(sorted[0].Name).FloorLabel)
}

// TestFollowsTheStream covers live changes from the event stream, a group's
// change, the reread after an item is added or added back, and a keepalive
// keeping a quiet stream up.
func TestFollowsTheStream(t *testing.T) {
	srv := openhabtest.New()
	t.Cleanup(srv.Close)
	srv.House()
	d := startDriver(t, srv, "")
	got := &changes{}
	defer d.SubscribeChanges(got.add)()

	srv.SetState("KitchenSwitch", "ON")
	waitFor(t, func() bool { return len(got.all()) == 1 }, "the change never arrived")
	c := got.all()[0]
	assert.Equal(t, ItemChange{Name: "KitchenSwitch", Old: "OFF", New: "ON", Item: c.Item}, c)
	it, _ := d.Item("KitchenSwitch")
	assert.Equal(t, "ON", it.State)

	// openHAB reports a group's own change only on the group/member topic.
	srv.Push("openhab/items/gLights/KitchenSwitch/statechanged", "GroupItemStateChangedEvent",
		map[string]any{"type": "OnOff", "value": "ON", "oldType": "OnOff", "oldValue": "OFF"})
	waitFor(t, func() bool { g, _ := d.Item("gLights"); return g.State == "ON" }, "a group's change never reached the copy")

	srv.SetItem(map[string]any{"name": "NewSwitch", "label": "New", "type": "Switch", "state": "OFF"})
	srv.Announce("NewSwitch", "added")
	waitFor(t, func() bool { _, ok := d.Item("NewSwitch"); return ok }, "an added item never reached the copy")

	// An item removed and added back with another state reports the change
	// its dropped events carried.
	srv.RemoveItem("KitchenSwitch")
	srv.Announce("KitchenSwitch", "removed")
	waitFor(t, func() bool { _, ok := d.Item("KitchenSwitch"); return !ok }, "a removed item stayed in the copy")
	srv.SetItem(map[string]any{"name": "KitchenSwitch", "label": "Kitchen switch", "type": "Switch", "state": "OFF"})
	srv.Announce("KitchenSwitch", "added")
	waitFor(t, func() bool {
		all := got.all()
		last := all[len(all)-1]
		return last.Name == "KitchenSwitch" && last.Old == "ON" && last.New == "OFF"
	}, "an item added back never reported its new state")
}

// TestReconnectsAndCatchesUp covers a dropped stream: the driver reconnects
// and reports each item that changed, appeared, or disappeared while it was
// down, once and caught up, and nothing for the rest.
func TestReconnectsAndCatchesUp(t *testing.T) {
	srv := openhabtest.New()
	t.Cleanup(srv.Close)
	srv.House()
	d := startDriver(t, srv, "")
	got := &changes{}
	defer d.SubscribeChanges(got.add)()
	assert.Empty(t, got.all(), "the first connect is not an outage")

	srv.Drop()
	srv.SetStateQuietly("KitchenSwitch", "ON")
	srv.SetStateQuietly("Volume", "20")
	srv.RemoveItem("Phone")
	srv.SetItem(map[string]any{"name": "Fresh", "label": "Fresh", "type": "Switch", "state": "ON"})
	waitFor(t, func() bool { return len(got.all()) == 4 }, "the catch-up never arrived")
	time.Sleep(100 * time.Millisecond)
	byName := map[string]ItemChange{}
	for _, c := range got.all() {
		assert.True(t, c.CaughtUp)
		byName[c.Name] = c
	}
	assert.Len(t, byName, 4)
	assert.Equal(t, "ON", byName["KitchenSwitch"].New)
	assert.Equal(t, "10", byName["Volume"].Old)
	assert.Equal(t, "", byName["Phone"].New)
	assert.Equal(t, "", byName["Fresh"].Old)
	waitFor(t, d.Connected, "the driver never reconnected")
}

// TestSilentStreamIsDead covers a stream that stops sending even keepalives:
// the driver gives up on it and reconnects.
func TestSilentStreamIsDead(t *testing.T) {
	srv := openhabtest.New()
	t.Cleanup(srv.Close)
	srv.House()
	cfg, _ := json.Marshal(Config{BaseURL: srv.URL})
	built, err := New(integrations.ConnModel{ID: uuid.New(), Kind: Kind, Config: cfg}, integrations.Deps{})
	require.NoError(t, err)
	d := built.(*Driver)
	d.aliveTimeout = 150 * time.Millisecond
	d.initialBackoff = 20 * time.Millisecond
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	waitFor(t, d.Connected, "the driver never connected")
	waitFor(t, func() bool { return d.Status().State == integrations.StateError }, "a silent stream was never given up")
	assert.Contains(t, d.Status().Error, "silent")
	waitFor(t, d.Connected, "the driver never reconnected")
}

// TestCommandsAndAuth covers commands, openHAB's refusal with its reason, and
// a wrong token reported as authorization.
func TestCommandsAndAuth(t *testing.T) {
	srv := openhabtest.New()
	t.Cleanup(srv.Close)
	srv.House()
	srv.Token = "right"
	d := startDriver(t, srv, "right")

	require.NoError(t, d.SendCommand(context.Background(), "KitchenSwitch", "ON"))
	assert.Equal(t, []openhabtest.Command{{Item: "KitchenSwitch", Command: "ON"}}, srv.Commands())
	srv.RefuseCommands("FrontDoor")
	err := d.SendCommand(context.Background(), "FrontDoor", "OPEN")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "openHAB answered 400: Cannot parse command OPEN")
	err = d.SendCommand(context.Background(), "Nothing", "ON")
	assert.Contains(t, err.Error(), "Item Nothing does not exist!")

	cfg, _ := json.Marshal(Config{BaseURL: srv.URL, Token: "wrong"})
	_, err = Probe(context.Background(), uuid.New(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unauthorized: check the API token")
	cfg, _ = json.Marshal(Config{BaseURL: srv.URL, Token: "right"})
	detail, err := Probe(context.Background(), uuid.New(), cfg)
	require.NoError(t, err)
	assert.Equal(t, "openHAB 5.2.3, 18 items, authenticated", detail)
}

// TestWatchEntityBeforeFirstRead pins that a device watching an item gets the
// item's value once the first item read lands, when the watch was placed
// before it. Devices are bound as soon as the driver starts, which is before
// the read, and the first read reports no changes, so such a device stayed
// empty until its item next changed. A watch placed on a connected driver
// still gets the value at once.
func TestWatchEntityBeforeFirstRead(t *testing.T) {
	srv := openhabtest.New()
	t.Cleanup(srv.Close)
	srv.House()
	cfg, _ := json.Marshal(Config{BaseURL: srv.URL})
	built, err := New(integrations.ConnModel{ID: uuid.New(), Kind: Kind, Config: cfg}, integrations.Deps{})
	require.NoError(t, err)
	d := built.(*Driver)

	var mu sync.Mutex
	var early []map[string]any
	stop, err := d.WatchEntity("Volume", func(v map[string]any) {
		mu.Lock()
		early = append(early, v)
		mu.Unlock()
	})
	require.NoError(t, err)
	defer stop()
	seen := func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), early...)
	}
	assert.Empty(t, seen(), "nothing is known before the driver starts")

	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	waitFor(t, d.Connected, "the driver never connected")
	waitFor(t, func() bool { return len(seen()) > 0 },
		"a watch placed before the first item read never received the item's value")
	assert.Equal(t, map[string]any{"value": 10.0}, seen()[0], "the first value is the state the read found")

	var late []map[string]any
	stopLate, err := d.WatchEntity("Volume", func(v map[string]any) { late = append(late, v) })
	require.NoError(t, err)
	stopLate()
	assert.Equal(t, []map[string]any{{"value": 10.0}}, late, "a watch on a connected driver gets the value at once")

	srv.SetState("Volume", "20")
	waitFor(t, func() bool {
		all := seen()
		return all[len(all)-1]["value"] == 20.0
	}, "a later change never reached the early watch")
	assert.Len(t, seen(), 2, "the value read at connect is handed over once")
}
