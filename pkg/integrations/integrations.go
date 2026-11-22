// Package integrations manages long-lived connections to external systems
// (MQTT brokers, Home Assistant, ...). Drivers implement the per-kind
// protocol; the Manager owns driver lifecycle, resolves connections for the
// engine, and relays status transitions to the in-process message broker.
package integrations

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"go.uber.org/zap"
)

// Connection state constants reported through Status.
const (
	StateConnecting = "connecting"
	StateConnected  = "connected"
	StateError      = "error"
	StateDisabled   = "disabled"
)

// Status is a connection's current lifecycle state.
type Status struct {
	State string    `json:"state" description:"Connection state: connecting, connected, error, or disabled."`
	Error string    `json:"error,omitempty" description:"Last error message when in the error state."`
	Since time.Time `json:"since" description:"When the state was entered."`
}

// Conn is the read-only view of a running connection.
type Conn interface {
	// ID returns the connection's database identifier.
	ID() uuid.UUID
	// Kind returns the driver kind, e.g. "mqtt".
	Kind() string
	// Status returns the connection's current lifecycle state.
	Status() Status
}

// Driver is a running connection instance. Start establishes the session and
// supervises reconnects internally until Stop or context cancellation.
type Driver interface {
	Conn
	// Start begins connecting. It returns quickly; reconnect supervision runs
	// in the background. An error means the configuration is unusable.
	Start(ctx context.Context) error
	// Stop tears the connection down and releases its resources.
	Stop()
}

// ConnModel carries a connection's identity and decrypted configuration to a
// driver factory.
type ConnModel struct {
	ID     uuid.UUID
	Name   string
	Kind   string
	Config []byte
}

// Deps supplies shared services to a driver: a logger and a callback for
// publishing status transitions.
type Deps struct {
	Logger        *zap.Logger
	PublishStatus func(status Status)
	// PersistSecrets replaces encrypted credential fields after a provider
	// rotates them without restarting the driver.
	PersistSecrets func(secrets map[string]string) error
}

// DriverFactory builds a driver for one connection model.
type DriverFactory func(model ConnModel, deps Deps) (Driver, error)

// ConnectionKind bundles everything the host needs to offer a connection
// driver: identity for the UI, the config schema its settings form renders,
// which fields are secrets, the factory, and optional validation and test
// hooks. Kinds are registered by plugins (the built-in MQTT and Home
// Assistant integrations register through the same path), so nothing else in
// the host carries per-kind knowledge.
type ConnectionKind struct {
	// Kind is the identifier connections store, e.g. "mqtt".
	Kind string
	// Label is the display name shown in the connection settings UI.
	Label string
	// ConfigSpec drives the settings form and required-field validation, the
	// same FieldSpec schema node configs use.
	ConfigSpec []flow.FieldSpec
	// Secrets lists config fields masked in API responses and round-tripped
	// through the password sentinel rather than re-sent to the browser.
	Secrets []string
	// MaskConfig redacts structured secrets in the decoded client view.
	MaskConfig func(cfg map[string]any) error
	// RestoreConfig restores structured secret sentinels from readable stored config.
	RestoreConfig func(cfg, stored map[string]any) error
	// Factory builds a driver for one connection.
	Factory DriverFactory
	// Validate checks a decoded config beyond required fields. Optional.
	Validate func(cfg []byte) error
	// Test performs a short-lived connectivity check with a throwaway client
	// against the given config. Optional; a kind without one has no Test
	// button behavior.
	Test func(ctx context.Context, id uuid.UUID, cfg []byte) error
	// Probe tests a configuration like Test and also reports what it found,
	// such as the calendars an account holds. Optional; when set, connection
	// tests call it instead of Test.
	Probe func(ctx context.Context, id uuid.UUID, cfg []byte) (detail string, err error)
}

// kindRegistry is the process-wide connection kind registry, ordered by
// registration so the settings UI lists kinds stably.
var (
	kindMu    sync.RWMutex
	kindOrder []string
	kindByID  = make(map[string]ConnectionKind)
)

// RegisterKind registers a connection kind, replacing any existing
// registration for the same kind name.
func RegisterKind(k ConnectionKind) {
	kindMu.Lock()
	defer kindMu.Unlock()
	if _, ok := kindByID[k.Kind]; !ok {
		kindOrder = append(kindOrder, k.Kind)
	}
	kindByID[k.Kind] = k
}

// UnregisterKind removes a connection kind. Unloading the plugin that brought
// a kind takes it out of the catalog; stored connections of that kind remain
// but cannot start until the plugin comes back.
func UnregisterKind(kind string) {
	kindMu.Lock()
	defer kindMu.Unlock()
	if _, ok := kindByID[kind]; !ok {
		return
	}
	delete(kindByID, kind)
	for i, id := range kindOrder {
		if id == kind {
			kindOrder = append(kindOrder[:i], kindOrder[i+1:]...)
			break
		}
	}
}

// KindFor returns the registered kind definition.
func KindFor(kind string) (ConnectionKind, bool) {
	kindMu.RLock()
	defer kindMu.RUnlock()
	k, ok := kindByID[kind]
	return k, ok
}

// KindDefs returns every registered kind in registration order.
func KindDefs() []ConnectionKind {
	kindMu.RLock()
	defer kindMu.RUnlock()
	out := make([]ConnectionKind, 0, len(kindOrder))
	for _, id := range kindOrder {
		out = append(out, kindByID[id])
	}
	return out
}

// DriverFor returns the factory registered for a kind.
func DriverFor(kind string) (DriverFactory, bool) {
	k, ok := KindFor(kind)
	if !ok || k.Factory == nil {
		return nil, false
	}
	return k.Factory, true
}

// Kinds returns the registered driver kinds in registration order.
func Kinds() []string {
	kindMu.RLock()
	defer kindMu.RUnlock()
	out := make([]string, len(kindOrder))
	copy(out, kindOrder)
	return out
}
