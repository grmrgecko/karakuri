package homeassistant

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant/hatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// recorder collects state changes.
type recorder struct {
	mu      sync.Mutex
	changes []StateChange
}

func (r *recorder) add(c StateChange) {
	r.mu.Lock()
	r.changes = append(r.changes, c)
	r.mu.Unlock()
}

func (r *recorder) caughtUp() map[string]StateChange {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]StateChange{}
	for _, c := range r.changes {
		if c.CaughtUp {
			out[c.EntityID] = c
		}
	}
	return out
}

func stateOf(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	if string(raw) == "null" || len(raw) == 0 {
		return "<none>"
	}
	var s struct {
		State string `json:"state"`
	}
	require.NoError(t, json.Unmarshal(raw, &s))
	return s.State
}

// TestCatchUpAfterReconnect covers FR-023: the first connection only seeds;
// after an outage one entity changed three times, one appeared, one went
// away, and one stayed put, and the reconnect reports exactly the first three
// once, caught up, with the latest states.
func TestCatchUpAfterReconnect(t *testing.T) {
	srv := hatest.New(testToken)
	t.Cleanup(srv.Close)
	srv.SetState("binary_sensor.door", "off", nil)
	srv.SetState("light.hall", "off", nil)
	srv.SetState("sensor.old", "1", nil)

	d := newTestDriver(t, srv.URL, srv.Token)
	rec := &recorder{}
	_, err := d.SubscribeStateChangesDetailed(rec.add)
	require.NoError(t, err)
	require.NoError(t, d.Start(t.Context()))
	t.Cleanup(d.Stop)
	waitFor(t, 5*time.Second, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.conn != nil
	}, "never connected")
	waitFor(t, 5*time.Second, func() bool {
		done := make(chan bool)
		d.queue(func() { d.memory.mu.Lock(); done <- d.memory.seeded; d.memory.mu.Unlock() })
		return <-done
	}, "the first connection never seeded the known states")
	assert.Empty(t, rec.caughtUp(), "starting is not an outage")

	srv.CloseClients()
	time.Sleep(2 * time.Millisecond)
	for _, s := range []string{"on", "off", "on"} {
		srv.SetState("binary_sensor.door", s, nil)
		time.Sleep(2 * time.Millisecond)
	}
	srv.SetState("sensor.new", "5", nil)
	srv.RemoveState("sensor.old")

	waitFor(t, 5*time.Second, func() bool { return len(rec.caughtUp()) == 3 }, "the reconnect never caught up")
	got := rec.caughtUp()
	assert.Equal(t, "off", stateOf(t, got["binary_sensor.door"].Old))
	assert.Equal(t, "on", stateOf(t, got["binary_sensor.door"].New), "the latest state, not each transition")
	assert.Equal(t, "<none>", stateOf(t, got["sensor.new"].Old))
	assert.Equal(t, "5", stateOf(t, got["sensor.new"].New))
	assert.Equal(t, "<none>", stateOf(t, got["sensor.old"].New))
	assert.NotContains(t, got, "light.hall", "an unchanged entity is not reported")

	// Nothing more arrives for the same outage.
	time.Sleep(100 * time.Millisecond)
	assert.Len(t, rec.caughtUp(), 3)
}

// TestCatchUpWaitsForAGoodRead covers a reconnect whose state read fails: it
// reports nothing, and the next connection still finds the change.
func TestCatchUpWaitsForAGoodRead(t *testing.T) {
	srv := hatest.New(testToken)
	t.Cleanup(srv.Close)
	srv.SetState("light.hall", "off", nil)
	d := newTestDriver(t, srv.URL, srv.Token)
	rec := &recorder{}
	_, err := d.SubscribeStateChangesDetailed(rec.add)
	require.NoError(t, err)
	require.NoError(t, d.Start(t.Context()))
	t.Cleanup(d.Stop)
	waitFor(t, 5*time.Second, func() bool {
		done := make(chan bool)
		d.queue(func() { d.memory.mu.Lock(); done <- d.memory.seeded; d.memory.mu.Unlock() })
		return <-done
	}, "never seeded")

	srv.FailStateReads("unavailable")
	srv.CloseClients()
	time.Sleep(2 * time.Millisecond)
	srv.SetState("light.hall", "on", nil)
	waitFor(t, 5*time.Second, func() bool { return srv.ConnCount() == 1 }, "never reconnected")
	time.Sleep(200 * time.Millisecond)
	assert.Empty(t, rec.caughtUp(), "a failed read reports nothing")

	srv.FailStateReads("")
	srv.CloseClients()
	waitFor(t, 5*time.Second, func() bool { return len(rec.caughtUp()) == 1 }, "the next connection never caught up")
	assert.Equal(t, "on", stateOf(t, rec.caughtUp()["light.hall"].New))
}

// TestCatchUpSkipsOvertakenReads covers a live event newer than the state
// read: the stale read is not reported as a change backwards.
func TestCatchUpSkipsOvertakenReads(t *testing.T) {
	d := &Driver{memory: &stateMemory{known: map[string]knownState{}}, stateHandlers: map[int]func(StateChange){}}
	rec := &recorder{}
	d.stateHandlers[0] = rec.add
	d.log = zap.NewNop()
	old := json.RawMessage(`{"entity_id":"light.hall","state":"off","last_updated":"2026-11-05T10:00:00Z"}`)
	newer := json.RawMessage(`{"entity_id":"light.hall","state":"on","last_updated":"2026-11-05T10:05:00Z"}`)
	d.applySync(map[string]json.RawMessage{"light.hall": old})
	d.memory.remember("light.hall", newer)
	d.applySync(map[string]json.RawMessage{"light.hall": old})
	assert.Empty(t, rec.caughtUp())
}

// TestCatchUpAcrossDriverReplacement covers a connection disabled and enabled
// again, or edited: the new driver for the same server catches up from what
// the old one knew, with its registries already read.
func TestCatchUpAcrossDriverReplacement(t *testing.T) {
	srv := hatest.New(testToken)
	t.Cleanup(srv.Close)
	srv.SetState("light.hall", "off", nil)
	first := newTestDriver(t, srv.URL, srv.Token)
	_, err := first.SubscribeStateChangesDetailed(func(StateChange) {})
	require.NoError(t, err)
	require.NoError(t, first.Start(t.Context()))
	waitFor(t, 5*time.Second, func() bool {
		first.memory.mu.Lock()
		defer first.memory.mu.Unlock()
		return first.memory.seeded
	}, "never seeded")
	first.Stop()

	time.Sleep(2 * time.Millisecond)
	srv.SetState("light.hall", "on", nil)
	cfg, _ := json.Marshal(Config{BaseURL: srv.URL, Token: srv.Token})
	built, err := New(integrations.ConnModel{ID: first.id, Name: "test", Kind: Kind, Config: cfg}, integrations.Deps{})
	require.NoError(t, err)
	second := built.(*Driver)
	second.initialBackoff = 50 * time.Millisecond
	rec := &recorder{}
	var registryAtCatchUp string
	_, err = second.SubscribeStateChangesDetailed(func(c StateChange) {
		if c.CaughtUp {
			// Area and label triggers match caught-up changes against it.
			registryAtCatchUp = second.Registry().Status[RegistryEntity]
		}
		rec.add(c)
	})
	require.NoError(t, err)
	require.NoError(t, second.Start(t.Context()))
	t.Cleanup(second.Stop)
	waitFor(t, 5*time.Second, func() bool { return len(rec.caughtUp()) == 1 }, "the new driver never caught up")
	assert.Equal(t, "on", stateOf(t, rec.caughtUp()["light.hall"].New))
	assert.Equal(t, "loaded", registryAtCatchUp, "the registries are read before the caught-up changes go out")
}

// TestAvailabilityAfterReconnect covers an entity that did not change while
// the socket was down. The device relay marks every device on a dropped
// connection unknown and leaves entity devices to the driver once it is back,
// and the catch-up only reports entities whose last_updated moved, so the
// driver has to tell availability watchers every entity's state after each
// full read or an unchanged entity's device stays unknown.
func TestAvailabilityAfterReconnect(t *testing.T) {
	srv := hatest.New(testToken)
	t.Cleanup(srv.Close)
	srv.SetState("light.hall", "off", nil)
	d := newTestDriver(t, srv.URL, srv.Token)

	var mu sync.Mutex
	var reports []bool
	_, err := d.WatchEntityAvailability("light.hall", func(available bool) {
		mu.Lock()
		reports = append(reports, available)
		mu.Unlock()
	})
	require.NoError(t, err)
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(reports)
	}
	require.NoError(t, d.Start(t.Context()))
	t.Cleanup(d.Stop)
	waitFor(t, 5*time.Second, func() bool { return count() == 1 }, "the first read never reported availability")

	srv.CloseClients()
	waitFor(t, 5*time.Second, func() bool { return count() == 2 }, "the reconnect never re-reported an unchanged entity")
	mu.Lock()
	assert.Equal(t, []bool{true, true}, reports)
	mu.Unlock()
}

// TestConcurrentFirstStateSubscribersShareOneSubscription locks that the
// state handlers share a single state_changed subscription however they
// arrive. A flow trigger, the device relay, and the entity tracker all
// subscribe as a connection starts, and two of them each finding no
// subscription yet used to open one apiece, so every state change reached
// every handler twice and one subscription could never be dropped.
func TestConcurrentFirstStateSubscribersShareOneSubscription(t *testing.T) {
	const subscribers = 8
	for round := 0; round < 300; round++ {
		d := newTestDriver(t, "http://ha.invalid:8123", testToken)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < subscribers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := d.SubscribeStateChangesDetailed(func(StateChange) {})
				assert.NoError(t, err, "subscribing to state changes needs no connection")
			}()
		}
		close(start)
		wg.Wait()

		d.mu.Lock()
		shared := 0
		for _, s := range d.subs {
			if s.eventType == "state_changed" {
				shared++
			}
		}
		d.mu.Unlock()
		if !assert.Equalf(t, 1, shared,
			"round %d: %d handlers must share one state_changed subscription, or each change is delivered once per extra subscription", round, subscribers) {
			return
		}
	}
}

// TestReplacedDriverWaitsForItsOwnSession pins a connection saved with a bad
// token while its server was down. The state memory outlives the driver, so
// the new driver found it seeded and reported the old states at once: entity
// devices showed online, with stale values, for a server it never reached.
func TestReplacedDriverWaitsForItsOwnSession(t *testing.T) {
	srv := hatest.New(testToken)
	t.Cleanup(srv.Close)
	srv.SetState("light.hall", "on", nil)
	first := newTestDriver(t, srv.URL, srv.Token)
	_, err := first.SubscribeStateChangesDetailed(func(StateChange) {})
	require.NoError(t, err)
	require.NoError(t, first.Start(t.Context()))
	waitFor(t, 5*time.Second, first.isSeeded, "never seeded")
	first.Stop()

	cfg, _ := json.Marshal(Config{BaseURL: srv.URL, Token: "wrong-token"})
	built, err := New(integrations.ConnModel{ID: first.id, Name: "test", Kind: Kind, Config: cfg}, integrations.Deps{})
	require.NoError(t, err)
	second := built.(*Driver)
	second.initialBackoff = 50 * time.Millisecond
	require.NoError(t, second.Start(t.Context()))
	t.Cleanup(second.Stop)

	var reports, values atomic.Int32
	_, err = second.WatchEntityAvailability("light.hall", func(bool) { reports.Add(1) })
	require.NoError(t, err)
	_, err = second.WatchEntity("light.hall", func(map[string]any) { values.Add(1) })
	require.NoError(t, err)
	time.Sleep(300 * time.Millisecond)
	assert.Zero(t, reports.Load(), "a driver that never authenticated must not report availability")
	assert.Zero(t, values.Load(), "a driver that never authenticated must not hand over the old driver's values")
}
