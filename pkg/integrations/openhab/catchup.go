package openhab

import (
	"sync"

	"go.uber.org/zap"
)

// stateMemory is the last state seen of every item of one connection to one
// server. It outlives a driver, so disabling and enabling a connection or
// editing it is caught up like a dropped stream; a new address starts over,
// since another server's states are no baseline.
type stateMemory struct {
	mu     sync.Mutex
	known  map[string]string
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
		m = &stateMemory{known: map[string]string{}}
		memories[key] = m
	}
	return m
}

func (m *stateMemory) remember(name, state string) {
	m.mu.Lock()
	m.known[name] = state
	m.mu.Unlock()
}

// swap records an item's state and returns the one it replaced, if any.
func (m *stateMemory) swap(name, state string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.known[name]
	m.known[name] = state
	return old, ok
}

// catchUp compares a full read with the known states. The first read only
// seeds them; a read after a reconnect reports each item whose state
// changed, appeared, or disappeared while the stream was down, once, with
// its latest state.
func (d *Driver) catchUp(items Catalog) {
	m := d.memory
	m.mu.Lock()
	if !m.seeded {
		for name, it := range items {
			m.known[name] = it.State
		}
		m.seeded = true
		m.mu.Unlock()
		return
	}
	var changes []ItemChange
	for name, it := range items {
		old, ok := m.known[name]
		switch {
		case !ok:
			changes = append(changes, ItemChange{Name: name, New: it.State, Item: it, CaughtUp: true})
		case old != it.State:
			changes = append(changes, ItemChange{Name: name, Old: old, New: it.State, Item: it, CaughtUp: true})
		}
	}
	for name, old := range m.known {
		if _, ok := items[name]; !ok {
			changes = append(changes, ItemChange{Name: name, Old: old, Item: Item{Name: name}, CaughtUp: true})
		}
	}
	for _, c := range changes {
		if c.New == "" && c.Item.Type == "" {
			delete(m.known, c.Name)
		} else {
			m.known[c.Name] = c.New
		}
	}
	m.mu.Unlock()
	for _, c := range changes {
		d.fanOut(c)
	}
	if len(changes) > 0 {
		d.log.Info("openhab caught up after reconnect", zap.Int("changed", len(changes)))
	}
}
