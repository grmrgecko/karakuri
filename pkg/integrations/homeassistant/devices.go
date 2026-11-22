package homeassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync/atomic"
)

// KnownStates returns every entity's last seen state, read from the driver's
// state memory once a full read has seeded it. ok is false before then.
func (d *Driver) KnownStates() ([]Entity, bool) {
	d.memory.mu.Lock()
	defer d.memory.mu.Unlock()
	if !d.memory.seeded {
		return nil, false
	}
	out := make([]Entity, 0, len(d.memory.known))
	for _, k := range d.memory.known {
		var e Entity
		if json.Unmarshal(k.raw, &e) == nil && e.EntityID != "" {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EntityID < out[j].EntityID })
	return out, true
}

// knownState returns one entity's last seen state.
func (d *Driver) knownState(entityID string) (Entity, bool) {
	d.memory.mu.Lock()
	defer d.memory.mu.Unlock()
	k, ok := d.memory.known[entityID]
	if !ok {
		return Entity{}, false
	}
	var e Entity
	if json.Unmarshal(k.raw, &e) != nil {
		return Entity{}, false
	}
	return e, true
}

// unanswered reports a state Home Assistant uses for an entity that cannot
// answer.
func unanswered(state string) bool { return state == "unavailable" || state == "unknown" }

// WatchEntity hands a device the entity's value as its kind holds it: the last
// seen state now, then every change. A state of unavailable or unknown is not
// a value; WatchEntityAvailability reports it.
func (d *Driver) WatchEntity(entityID string, fn func(map[string]any)) (func(), error) {
	deliver := func(raw json.RawMessage) {
		var e Entity
		if len(raw) == 0 || json.Unmarshal(raw, &e) != nil || e.EntityID == "" || unanswered(e.State) {
			return
		}
		fn(Values(e))
	}
	unsub, err := d.SubscribeStateChangesDetailed(func(c StateChange) {
		if c.EntityID == entityID {
			deliver(c.New)
		}
	})
	if err != nil {
		return nil, err
	}
	// The last seen state goes out once, after the first full read, however
	// long Home Assistant takes to answer one.
	var seeded atomic.Bool
	seed := func() {
		if !seeded.CompareAndSwap(false, true) {
			return
		}
		if e, ok := d.knownState(entityID); ok && !unanswered(e.State) {
			fn(Values(e))
		}
	}
	stopSync := d.onSync(seed)
	if d.isCurrent() {
		go seed()
	}
	return func() {
		stopSync()
		unsub()
	}, nil
}

// WatchEntityAvailability reports whether the entity can answer: false while
// Home Assistant reports it unavailable or unknown, or no longer has it.
func (d *Driver) WatchEntityAvailability(entityID string, fn func(available bool)) (func(), error) {
	unsub, err := d.SubscribeStateChangesDetailed(func(c StateChange) {
		if c.EntityID != entityID {
			return
		}
		var e Entity
		if len(c.New) == 0 || string(c.New) == "null" || json.Unmarshal(c.New, &e) != nil {
			fn(false)
			return
		}
		fn(!unanswered(e.State))
	})
	if err != nil {
		return nil, err
	}
	// Every full read reports, changed or not: the device relay marks a
	// device unknown while the connection is down, and a reconnect's
	// catch-up only reports entities that changed.
	report := func() {
		e, ok := d.knownState(entityID)
		fn(ok && !unanswered(e.State))
	}
	stopSync := d.onSync(report)
	if d.isCurrent() {
		go report()
	}
	return func() {
		stopSync()
		unsub()
	}, nil
}

// isCurrent reports whether the state memory can be reported as the
// server's states now: it holds a full read and this driver has a session.
// The memory outlives a driver, so a replacement that has not connected, and
// may never connect, finds it seeded with what the old driver last saw. The
// first full read of its session runs every sync watcher instead.
func (d *Driver) isCurrent() bool {
	d.mu.Lock()
	connected := d.conn != nil
	d.mu.Unlock()
	return connected && d.isSeeded()
}

// isSeeded reports whether the state memory holds a full read.
func (d *Driver) isSeeded() bool {
	d.memory.mu.Lock()
	defer d.memory.mu.Unlock()
	return d.memory.seeded
}

// CommandEntity sends one attribute write to an entity as the service the
// shared mapping names for it. The device's value changes when Home
// Assistant reports the new state, not here.
func (d *Driver) CommandEntity(ctx context.Context, entityID, field string, value any) error {
	e, ok := d.knownState(entityID)
	if !ok {
		states, err := d.GetStates(ctx)
		if err != nil {
			return err
		}
		for _, s := range states {
			if s.EntityID == entityID {
				e, ok = s, true
				break
			}
		}
		if !ok {
			return fmt.Errorf("Home Assistant has no entity %s", entityID)
		}
	}
	call, ok := Command(e, field, value)
	if !ok {
		return fmt.Errorf("%s cannot be told %s %v", entityID, field, value)
	}
	_, err := d.CallService(ctx, call.Domain, call.Service, call.Data, map[string]any{"entity_id": []string{entityID}})
	return err
}
