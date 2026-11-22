package homeassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// StateChange is one entity's change, live or caught up after a reconnect.
// Old is null for an added entity and New for a removed one.
type StateChange struct {
	EntityID string
	Old      json.RawMessage
	New      json.RawMessage
	// CaughtUp marks a change found by comparing states after a reconnect,
	// carrying the latest state rather than each transition the outage hid.
	CaughtUp bool
}

// stateMemory is the last state seen of every entity of one connection to one
// server. It outlives a driver, so disabling and enabling a connection or
// editing its settings is caught up like a dropped socket; a new address
// starts a new memory, since another server's states are no baseline.
type stateMemory struct {
	mu    sync.Mutex
	known map[string]knownState
	// seeded says known holds a full read, so the next read is a catch-up
	// rather than a seed.
	seeded bool
}

var (
	memoriesMu sync.Mutex
	memories   = map[string]*stateMemory{}
)

// memoryFor returns the memory of a connection and address.
func memoryFor(key string) *stateMemory {
	memoriesMu.Lock()
	defer memoriesMu.Unlock()
	m := memories[key]
	if m == nil {
		m = &stateMemory{known: map[string]knownState{}}
		memories[key] = m
	}
	return m
}

// knownState is the last state seen of one entity.
type knownState struct {
	raw     json.RawMessage
	updated time.Time
}

// lastUpdated reads a state object's last_updated time.
func lastUpdated(raw json.RawMessage) time.Time {
	var s struct {
		LastUpdated string `json:"last_updated"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, s.LastUpdated)
	return t
}

// SubscribeStateChangesDetailed registers a handler for state changes. The
// first handler opens the state_changed subscription the driver shares among
// them, and the driver keeps every entity's last state from then on, so a
// reconnect can report what changed while it was away.
func (d *Driver) SubscribeStateChangesDetailed(handler func(StateChange)) (func(), error) {
	if handler == nil {
		return nil, fmt.Errorf("nil state change handler")
	}
	// Two handlers arriving together must not each find no subscription and
	// open one apiece.
	d.stateSubMu.Lock()
	defer d.stateSubMu.Unlock()
	d.mu.Lock()
	id := d.nextState
	d.nextState++
	d.stateHandlers[id] = handler
	first := d.stateUnsub == nil
	d.mu.Unlock()
	if first {
		unsub, err := d.SubscribeEvents("state_changed", d.onStateChanged)
		if err != nil {
			d.mu.Lock()
			delete(d.stateHandlers, id)
			d.mu.Unlock()
			return nil, err
		}
		d.mu.Lock()
		d.stateUnsub = unsub
		connected, ctx := d.conn != nil, d.ctx
		d.mu.Unlock()
		// A driver already connected gets no resubscribe to seed from, so
		// the first subscriber seeds it.
		if connected && ctx != nil {
			go func() {
				if err := d.subscribeEventType(ctx, "state_changed"); err != nil {
					return
				}
				d.syncStates(ctx)
			}()
		}
	}
	return func() {
		d.stateSubMu.Lock()
		defer d.stateSubMu.Unlock()
		d.mu.Lock()
		delete(d.stateHandlers, id)
		var unsub func()
		if len(d.stateHandlers) == 0 && d.stateUnsub != nil {
			unsub, d.stateUnsub = d.stateUnsub, nil
		}
		d.mu.Unlock()
		if unsub != nil {
			unsub()
			// Without a subscription the known states go stale; the next
			// subscriber starts from a fresh read.
			d.memory.mu.Lock()
			d.memory.known, d.memory.seeded = map[string]knownState{}, false
			d.memory.mu.Unlock()
		}
	}, nil
}

// onStateChanged records a live change and hands it on. It runs on the
// dispatch goroutine.
func (d *Driver) onStateChanged(event json.RawMessage) {
	var sc stateChangedEvent
	if err := json.Unmarshal(event, &sc); err != nil || sc.Data.EntityID == "" {
		return
	}
	d.memory.mu.Lock()
	d.memory.remember(sc.Data.EntityID, sc.Data.NewState)
	d.memory.mu.Unlock()
	d.fanOut(StateChange{EntityID: sc.Data.EntityID, Old: sc.Data.OldState, New: sc.Data.NewState})
}

// remember records an entity's latest state; a null state removes it. The
// caller holds m.mu.
func (m *stateMemory) remember(entityID string, raw json.RawMessage) {
	if len(raw) == 0 || string(raw) == "null" {
		delete(m.known, entityID)
		return
	}
	m.known[entityID] = knownState{raw: raw, updated: lastUpdated(raw)}
}

func (d *Driver) fanOut(c StateChange) {
	d.mu.Lock()
	handlers := make([]func(StateChange), 0, len(d.stateHandlers))
	for _, h := range d.stateHandlers {
		handlers = append(handlers, h)
	}
	d.mu.Unlock()
	for _, h := range handlers {
		h(c)
	}
}

// queue runs fn on the dispatch goroutine in order with events.
func (d *Driver) queue(fn func()) {
	d.mu.Lock()
	generation := d.generation
	d.mu.Unlock()
	select {
	case d.deliveries <- delivery{generation: generation, run: fn}:
	default:
		d.log.Warn("homeassistant state sync dropped, queue full")
	}
}

// syncStates reads every entity's state after the state_changed subscription
// is live. The first read only seeds the known states; a read after a
// reconnect reports each entity that changed, appeared, or disappeared while
// the connection was down. A failed read leaves the catch-up for the next
// session.
func (d *Driver) syncStates(ctx context.Context) {
	d.mu.Lock()
	d.statesCache = nil
	d.mu.Unlock()
	entities, err := d.GetStates(ctx)
	if err != nil {
		// The known states stay as they were, so the next connection's
		// read still finds everything this one missed.
		d.log.Warn("homeassistant state read after connect failed; catch-up waits for the next connection", zap.Error(err))
		return
	}
	fresh := make(map[string]json.RawMessage, len(entities))
	for _, e := range entities {
		raw, err := json.Marshal(e)
		if err == nil {
			fresh[e.EntityID] = raw
		}
	}
	d.queue(func() { d.applySync(fresh) })
}

// applySync compares a full read with the known states on the dispatch
// goroutine, after any live event that arrived before it.
func (d *Driver) applySync(fresh map[string]json.RawMessage) {
	m := d.memory
	m.mu.Lock()
	if !m.seeded {
		for id, raw := range fresh {
			if k, ok := m.known[id]; !ok || lastUpdated(raw).After(k.updated) {
				m.remember(id, raw)
			}
		}
		m.seeded = true
		m.mu.Unlock()
		d.runSyncWatchers()
		return
	}
	var changes []StateChange
	for id, raw := range fresh {
		k, ok := m.known[id]
		switch {
		case !ok:
			changes = append(changes, StateChange{EntityID: id, Old: json.RawMessage("null"), New: raw, CaughtUp: true})
		case lastUpdated(raw).After(k.updated):
			changes = append(changes, StateChange{EntityID: id, Old: k.raw, New: raw, CaughtUp: true})
		}
	}
	for id, k := range m.known {
		if _, ok := fresh[id]; !ok {
			changes = append(changes, StateChange{EntityID: id, Old: k.raw, New: json.RawMessage("null"), CaughtUp: true})
		}
	}
	for _, c := range changes {
		m.remember(c.EntityID, c.New)
	}
	m.mu.Unlock()
	for _, c := range changes {
		d.fanOut(c)
	}
	d.runSyncWatchers()
	d.log.Info("homeassistant caught up after reconnect", zap.Int("changed", len(changes)))
}

// onSync registers fn to run after every full state read. The returned
// function removes it.
func (d *Driver) onSync(fn func()) func() {
	d.mu.Lock()
	id := d.nextSync
	d.nextSync++
	d.syncWatchers[id] = fn
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		delete(d.syncWatchers, id)
		d.mu.Unlock()
	}
}

func (d *Driver) runSyncWatchers() {
	d.mu.Lock()
	watchers := make([]func(), 0, len(d.syncWatchers))
	for _, fn := range d.syncWatchers {
		watchers = append(watchers, fn)
	}
	d.mu.Unlock()
	for _, fn := range watchers {
		fn()
	}
}
