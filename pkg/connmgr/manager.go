// Package connmgr owns the lifecycle of connection drivers: it loads enabled
// connections from the database at startup, hot-applies API changes, and
// relays driver status to the message broker. It is deliberately separate
// from pkg/integrations, which describes what a connection kind is and must
// stay free of the data model so a remote agent or an out-of-process plugin
// can register a kind without linking a database driver.
package connmgr

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/crypto"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/metrics"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// Publisher publishes status events to the in-process message broker.
// Implemented by pkg/mq.Broker.
type Publisher interface {
	Publish(topic, msgType string, payload any)
}

// Manager owns the lifecycle of all connection drivers: it loads enabled
// connections at startup, hot-applies API changes via Start/Stop/Restart, and
// relays driver status transitions to the message broker.
type Manager struct {
	log *zap.Logger
	pub Publisher

	// OnDriverStart, when set, runs after a connection's driver starts
	// successfully. Set before Start; the manager reads it without locking.
	// Used to attach per-kind observers such as the Home Assistant state
	// relay feeding dashboard widgets.
	OnDriverStart func(id uuid.UUID, d integrations.Driver)

	mu      sync.Mutex
	drivers map[uuid.UUID]integrations.Driver
	// connStates records the last published state per connection for the
	// karakuri_connections metric; disabled connections are dropped.
	connStates  map[uuid.UUID]string
	statuses    map[uuid.UUID]integrations.Status
	generations map[uuid.UUID]uint64
	watchers    map[uuid.UUID]map[uint64]chan integrations.Status
	nextWatcher uint64
	ctx         context.Context
	cancel      context.CancelFunc
	stopped     bool
}

// New creates a connection manager. The publisher is optional.
func New(log *zap.Logger, pub Publisher) *Manager {
	if log == nil {
		log = zap.NewNop()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		log:         log,
		pub:         pub,
		drivers:     make(map[uuid.UUID]integrations.Driver),
		connStates:  make(map[uuid.UUID]string),
		statuses:    make(map[uuid.UUID]integrations.Status),
		generations: make(map[uuid.UUID]uint64),
		watchers:    make(map[uuid.UUID]map[uint64]chan integrations.Status),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start loads every enabled connection from the database and starts its
// driver. Individual failures are logged and skipped so one bad connection
// cannot prevent startup.
func (m *Manager) Start() error {
	conns, err := models.ListEnabledConnections()
	if err != nil {
		return fmt.Errorf("failed to load connections: %w", err)
	}
	for _, c := range conns {
		err := m.StartConnection(c.ID)
		switch {
		case err == nil:
		case errors.Is(err, crypto.ErrDecrypt):
			// The stored secrets predate the current security.encryption_key.
			// Nothing is broken in the code and no stack trace helps. Rotation
			// cannot recover these, since it needs the old key to read them.
			m.log.Warn("connection secrets do not decrypt with the current "+
				"security.encryption_key; re-enter its settings in the UI to replace them. "+
				"Restore the previous key first if you still have it, then run "+
				"`karakuri config rotate-key` to migrate them properly",
				zap.String("connection_id", c.ID.String()),
				zap.String("name", c.Name))
		default:
			m.log.Error("failed to start connection",
				zap.String("connection_id", c.ID.String()),
				zap.String("name", c.Name),
				zap.Error(err))
		}
	}
	m.log.Info("connection manager started", zap.Int("connections", len(conns)))
	return nil
}

// Stop tears down every running driver.
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	drivers := make([]integrations.Driver, 0, len(m.drivers))
	for _, d := range m.drivers {
		drivers = append(drivers, d)
	}
	m.drivers = make(map[uuid.UUID]integrations.Driver)
	for _, watchers := range m.watchers {
		for _, updates := range watchers {
			close(updates)
		}
	}
	m.watchers = make(map[uuid.UUID]map[uint64]chan integrations.Status)
	m.mu.Unlock()

	m.cancel()
	for _, d := range drivers {
		d.Stop()
	}
	m.log.Info("connection manager stopped")
}

// StartConnection loads a connection from the database, builds its driver,
// and starts it. A driver already running for the ID is stopped first.
func (m *Manager) StartConnection(id uuid.UUID) error {
	conn, err := models.FindConnectionByID(id)
	if err != nil {
		return fmt.Errorf("connection not found: %w", err)
	}
	if !conn.Enabled {
		return fmt.Errorf("connection %q is disabled", conn.Name)
	}
	// Retire the old generation before stopping it. Its shutdown callbacks
	// cannot replace a new driver's status, even when Stop returns late.
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return fmt.Errorf("connection manager is stopped")
	}
	m.generations[id]++
	generation := m.generations[id]
	old := m.drivers[id]
	delete(m.drivers, id)
	m.mu.Unlock()
	if old != nil {
		old.Stop()
	}
	report := func(status integrations.Status) { m.publishStatusFor(id, generation, status) }
	report(integrations.Status{State: integrations.StateConnecting})
	startError := func(err error) error {
		report(integrations.Status{State: integrations.StateError, Error: err.Error()})
		return err
	}
	factory, ok := integrations.DriverFor(conn.Kind)
	if !ok {
		return startError(fmt.Errorf("no driver registered for kind %q", conn.Kind))
	}
	cfg, err := conn.ConfigJSON()
	if err != nil {
		return startError(err)
	}

	driver, err := factory(integrations.ConnModel{ID: conn.ID, Name: conn.Name, Kind: conn.Kind, Config: cfg}, integrations.Deps{
		Logger: m.log.With(
			zap.String("connection_id", conn.ID.String()),
			zap.String("connection", conn.Name),
			zap.String("kind", conn.Kind)),
		PublishStatus: report,
		PersistSecrets: func(secrets map[string]string) error {
			current, err := models.FindConnectionByID(id)
			if err != nil {
				return fmt.Errorf("loading connection for credential refresh: %w", err)
			}
			var currentConfig map[string]any
			if err := current.GetConfig(&currentConfig); err != nil {
				return err
			}
			for key, value := range secrets {
				currentConfig[key] = value
			}
			if err := current.SetConfig(currentConfig); err != nil {
				return err
			}
			return models.SaveConnection(current)
		},
	})
	if err != nil {
		return startError(fmt.Errorf("failed to build %s driver: %w", conn.Kind, err))
	}

	// A stop or a newer replacement can win while the factory is running.
	m.mu.Lock()
	if m.stopped || m.generations[id] != generation {
		m.mu.Unlock()
		driver.Stop()
		return fmt.Errorf("connection startup was superseded")
	}
	m.drivers[id] = driver
	m.mu.Unlock()

	if err := driver.Start(m.ctx); err != nil {
		m.mu.Lock()
		errorGeneration := generation
		if m.generations[id] == generation {
			// Cleanup callbacks must not overwrite the startup failure.
			m.generations[id]++
			errorGeneration = m.generations[id]
			delete(m.drivers, id)
		}
		m.mu.Unlock()
		driver.Stop()
		m.publishStatusFor(id, errorGeneration, integrations.Status{State: integrations.StateError, Error: err.Error()})
		return err
	}
	m.mu.Lock()
	current := !m.stopped && m.generations[id] == generation
	m.mu.Unlock()
	if !current {
		// Stop may have run before Start finished launching its workers.
		driver.Stop()
		return fmt.Errorf("connection startup was superseded")
	}

	if m.OnDriverStart != nil {
		m.OnDriverStart(id, driver)
	}
	return nil
}

// StopConnection stops and removes the driver for a connection. Safe to call
// when no driver is running.
func (m *Manager) StopConnection(id uuid.UUID) {
	m.mu.Lock()
	m.generations[id]++
	generation := m.generations[id]
	driver := m.drivers[id]
	delete(m.drivers, id)
	m.mu.Unlock()
	if driver != nil {
		driver.Stop()
	}
	m.publishStatusFor(id, generation, integrations.Status{State: integrations.StateDisabled})
}

// RestartConnection replaces the running driver with a fresh one from the
// current database state. Used to hot-apply config changes. StartConnection
// retires the old driver itself, so status watchers see connecting rather than
// a false disabled transition.
func (m *Manager) RestartConnection(id uuid.UUID) error {
	conn, err := models.FindConnectionByID(id)
	if err != nil || !conn.Enabled {
		m.StopConnection(id)
		if err != nil {
			return fmt.Errorf("connection not found: %w", err)
		}
		return fmt.Errorf("connection %q is disabled", conn.Name)
	}
	return m.StartConnection(id)
}

// Get returns the running driver for a connection ID.
func (m *Manager) Get(id uuid.UUID) (integrations.Driver, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.drivers[id]
	return d, ok
}

// Resolve implements the engine's connection resolver: it parses the ID and
// returns the running driver, erroring when the connection is unknown or not
// running. The returned value is the concrete driver for nodes to assert.
func (m *Manager) Resolve(id string) (any, error) {
	cid, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("invalid connection ID %q", id)
	}
	d, ok := m.Get(cid)
	if !ok {
		return nil, fmt.Errorf("connection %s is not running", id)
	}
	return d, nil
}

// Statuses returns the current status of every running driver keyed by
// connection ID.
func (m *Manager) Statuses() map[uuid.UUID]integrations.Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[uuid.UUID]integrations.Status, len(m.drivers))
	for id, d := range m.drivers {
		out[id] = d.Status()
	}
	return out
}

// Status returns a retained lifecycle status even when startup failed or the
// connection was disabled and no driver is running.
func (m *Manager) Status(id uuid.UUID) (integrations.Status, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status, ok := m.statuses[id]
	return status, ok
}

// WatchStatus atomically snapshots a status and registers a bounded queue of
// subsequent reports. A slow consumer loses the oldest report with an error log.
// Unsubscribe is idempotent and closes the queue; Stop closes every queue.
func (m *Manager) WatchStatus(id uuid.UUID) (integrations.Status, bool, <-chan integrations.Status, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status, found := m.statuses[id]
	updates := make(chan integrations.Status, 64)
	if m.stopped {
		close(updates)
		return status, found, updates, func() {}
	}
	m.nextWatcher++
	watcherID := m.nextWatcher
	if m.watchers[id] == nil {
		m.watchers[id] = make(map[uint64]chan integrations.Status)
	}
	m.watchers[id][watcherID] = updates
	return status, found, updates, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if queue, ok := m.watchers[id][watcherID]; ok {
			delete(m.watchers[id], watcherID)
			close(queue)
			if len(m.watchers[id]) == 0 {
				delete(m.watchers, id)
			}
		}
	}
}

// trackState updates the connection state gauges from one status transition.
// Disabled connections are removed so stopped or deleted connections no
// longer count toward any state.
func (m *Manager) trackState(id uuid.UUID, state string) {
	m.mu.Lock()
	if state == integrations.StateDisabled {
		delete(m.connStates, id)
	} else {
		m.connStates[id] = state
	}
	counts := make(map[string]int, 3)
	for _, s := range m.connStates {
		counts[s]++
	}
	m.mu.Unlock()
	for _, s := range []string{integrations.StateConnecting, integrations.StateConnected, integrations.StateError} {
		metrics.Connections.WithLabelValues(s).Set(float64(counts[s]))
	}
}

// publishStatusFor retains reports from the current driver generation before
// publishing them. Watcher queues preserve report order under the manager lock.
func (m *Manager) publishStatusFor(id uuid.UUID, generation uint64, status integrations.Status) {
	m.mu.Lock()
	if m.stopped || m.generations[id] != generation {
		m.mu.Unlock()
		return
	}
	previous, found := m.statuses[id]
	if found && previous.State == status.State {
		status.Since = previous.Since
	} else if status.Since.IsZero() {
		status.Since = time.Now()
	}
	m.statuses[id] = status
	for _, queue := range m.watchers[id] {
		select {
		case queue <- status:
		default:
			select {
			case <-queue:
			default:
			}
			queue <- status
			m.log.Error("connection status queue overflow", zap.String("connection_id", id.String()))
		}
	}
	m.mu.Unlock()
	m.trackState(id, status.State)
	if m.pub == nil {
		return
	}
	m.pub.Publish(fmt.Sprintf("connection:%s:status", id), "connection_status", map[string]any{
		"connection_id": id.String(),
		"state":         status.State,
		"error":         status.Error,
		"since":         status.Since,
	})
}
