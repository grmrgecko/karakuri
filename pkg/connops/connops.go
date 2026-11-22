// Package connops holds the connection operations shared by the REST handlers
// and the MCP tools: listing with masked secrets, create and update with the
// sentinel round-trip, delete, the connectivity test, and typed driver
// resolution. Callers apply their own authentication and permission checks
// first. Errors are AppErrors so both layers report the same codes.
package connops

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/connmgr"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/models"
	"gorm.io/gorm"
)

// PasswordSentinel is returned in place of stored secrets. Update requests
// that send the sentinel back keep the stored value unchanged, so clients can
// round-trip a config without ever seeing the secret.
const PasswordSentinel = "••••••"

// mgr is the package-level connection manager wired by the server at startup.
// Status lookups degrade gracefully when it is unset.
var mgr *connmgr.Manager

// SetManager installs the connection manager.
func SetManager(m *connmgr.Manager) { mgr = m }

// Manager returns the installed connection manager, or nil before startup.
func Manager() *connmgr.Manager { return mgr }

// ConnectionItem is a connection with masked config and live status.
type ConnectionItem struct {
	models.Connection
	Config map[string]any      `json:"config" description:"Driver configuration with secrets masked."`
	Status integrations.Status `json:"status" description:"Live connection status."`
	// Writer is the history writer's state for a writable time-series
	// connection, from the WriterStatus hook.
	Writer any `json:"writer,omitempty" description:"History writer state for writable time-series connections."`
	// Access is the level the caller holds, filled in by ListFor and ItemFor.
	Access string `json:"access,omitempty" description:"The caller's access level: use, edit, or admin."`
}

// WriterStatus reports a connection's history writer state, or nil for
// connections without one. Set at startup by the time-series service, which
// connops cannot import.
var WriterStatus func(id uuid.UUID) any

// deleteHooks run after a connection row is deleted.
var deleteHooks []func(id uuid.UUID)

// OnDelete registers a function called with the ID of every deleted
// connection, after its driver has stopped and its row is gone.
func OnDelete(fn func(id uuid.UUID)) {
	deleteHooks = append(deleteHooks, fn)
}

// KindItem is one registered connection kind, with the config schema the
// settings form renders.
type KindItem struct {
	Kind       string           `json:"kind" description:"Kind identifier, e.g. mqtt."`
	Label      string           `json:"label" description:"Display name."`
	ConfigSpec []flow.FieldSpec `json:"config_spec" description:"Config field schema for the settings form."`
	Testable   bool             `json:"testable" description:"Whether the kind supports a connectivity test."`
}

// Kinds lists the registered connection kinds with their config schemas.
func Kinds() []KindItem {
	defs := integrations.KindDefs()
	items := make([]KindItem, 0, len(defs))
	for _, d := range defs {
		items = append(items, KindItem{
			Kind: d.Kind, Label: d.Label, ConfigSpec: d.ConfigSpec, Testable: d.Test != nil || d.Probe != nil,
		})
	}
	return items
}

// secretKeys returns the config fields masked in responses for a kind, from
// its registered kind definition.
func secretKeys(kind string) []string {
	k, ok := integrations.KindFor(kind)
	if !ok {
		return nil
	}
	return k.Secrets
}

// Load parses a connection ID and loads the row.
func Load(id string) (*models.Connection, error) {
	cid, err := uuid.Parse(id)
	if err != nil {
		return nil, apperrors.ErrValidation.WithMessage("invalid connection ID")
	}
	c, err := models.FindConnectionByID(cid)
	if err != nil {
		return nil, apperrors.ErrNotFound.WithMessage("connection not found")
	}
	return c, nil
}

// maskedConfig decrypts a connection's config and replaces secret values with
// the sentinel so they never reach clients.
func maskedConfig(c *models.Connection) (map[string]any, error) {
	var cfg map[string]any
	if err := c.GetConfig(&cfg); err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	for _, key := range secretKeys(c.Kind) {
		if s, ok := cfg[key].(string); ok && s != "" {
			cfg[key] = PasswordSentinel
		}
	}
	if def, ok := integrations.KindFor(c.Kind); ok && def.MaskConfig != nil {
		if err := def.MaskConfig(cfg); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// status returns retained lifecycle status, including startup failures with no driver.
func status(c *models.Connection) integrations.Status {
	if mgr != nil {
		if status, ok := mgr.Status(c.ID); ok {
			return status
		}
	}
	return integrations.Status{State: integrations.StateDisabled}
}

// Item assembles the client view of one connection.
func Item(c *models.Connection) (*ConnectionItem, error) {
	cfg, err := maskedConfig(c)
	if err != nil {
		// A config that cannot be decrypted (typically a rotated encryption
		// key) must not fail the whole request: report it on the connection so
		// the operator can still see it and overwrite its settings.
		return &ConnectionItem{
			Connection: *c,
			Config:     map[string]any{},
			Status: integrations.Status{
				State: integrations.StateError,
				Error: "config could not be decrypted; re-enter its settings to replace it",
			},
		}, nil
	}
	item := &ConnectionItem{Connection: *c, Config: cfg, Status: status(c)}
	if WriterStatus != nil {
		item.Writer = WriterStatus(c.ID)
	}
	return item, nil
}

// List returns every connection with masked config and live status.
func List() ([]*ConnectionItem, error) {
	conns, err := models.ListConnections()
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	items := make([]*ConnectionItem, 0, len(conns))
	for _, c := range conns {
		item, err := Item(c)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// validateConfig checks a config against its registered kind: required fields
// from the kind's ConfigSpec, then the kind's own Validate hook. No per-kind
// knowledge lives here.
func validateConfig(kind string, cfg map[string]any) error {
	def, ok := integrations.KindFor(kind)
	if !ok {
		return apperrors.ErrValidation.WithMessage(fmt.Sprintf("unknown connection kind %q", kind))
	}
	for _, field := range def.ConfigSpec {
		if !field.Required {
			continue
		}
		// A required field hidden by its show_if is not required in this
		// mode — the mcp kind's url applies to the HTTP transports only.
		if !flow.FieldShown(def.ConfigSpec, field, cfg) {
			continue
		}
		v, present := cfg[field.ID]
		if !present || v == nil {
			return apperrors.ErrValidation.WithMessage(
				fmt.Sprintf("%s config requires %s", kind, field.ID))
		}
		if s, isStr := v.(string); isStr && s == "" {
			return apperrors.ErrValidation.WithMessage(
				fmt.Sprintf("%s config requires %s", kind, field.ID))
		}
	}
	if err := integrations.ValidateAutoImport(cfg); err != nil {
		return apperrors.ErrValidation.WithMessage(err.Error())
	}
	if def.Validate != nil {
		raw, err := json.Marshal(cfg)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid connection config")
		}
		if err := def.Validate(raw); err != nil {
			return apperrors.ErrValidation.WithMessage(err.Error())
		}
	}
	return nil
}

// Create validates and stores a connection with encrypted config, starting
// its driver when enabled. Driver startup problems surface through the status
// field rather than failing the create: the connection is stored either way.
func Create(ctx context.Context, ownerID uuid.UUID, name, kind string, cfg map[string]any, enabled bool) (*models.Connection, error) {
	if cfg == nil {
		cfg = map[string]any{}
	}
	if err := validateConfig(kind, cfg); err != nil {
		return nil, err
	}
	if err := CheckConfigConnections(ctx, ownerID, kind, cfg); err != nil {
		return nil, err
	}

	// Reject duplicate names up front for a clean conflict error.
	var count int64
	db.DB.Model(&models.Connection{}).Where("name = ?", name).Count(&count)
	if count > 0 {
		return nil, apperrors.ErrConflict.WithMessage("a connection with this name already exists")
	}

	// Persist with encrypted config.
	c := &models.Connection{Name: name, Kind: kind, Enabled: enabled}
	if ownerID != uuid.Nil {
		c.OwnerID = &ownerID
	}
	if err := c.SetConfig(cfg); err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	// The enabled column defaults to true, and an insert leaves a false to
	// that default, so a connection created disabled is switched off in the
	// same transaction rather than stored enabled.
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(c).Error; err != nil {
			return err
		}
		if enabled {
			return nil
		}
		c.Enabled = false
		return tx.Model(c).UpdateColumn("enabled", false).Error
	})
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}

	// Hot-apply: start the driver when enabled.
	if c.Enabled && mgr != nil {
		_ = mgr.StartConnection(c.ID)
	}
	recordChange(ctx, c, "Created connection "+c.Name+" ("+c.Kind+")", nil)
	return c, nil
}

// recordChange records a connection change in the security log: connections
// hold credentials for other systems. Only field names are recorded, never
// their values.
func recordChange(ctx context.Context, c *models.Connection, message string, details map[string]any) {
	events.Record(events.Entry{
		Category: models.EventSecurity,
		Subject:  events.Subject{Kind: "connection", ID: c.ID.String(), Name: c.Name},
		Message:  message,
		Details:  details,
		Actor:    events.ActorFrom(ctx),
	})
}

// Patch holds the optional fields Update applies to a connection. A Config
// value carrying the sentinel for a secret keeps the stored value.
type Patch struct {
	Name    *string
	Config  *map[string]any
	Enabled *bool
}

// Update applies a patch to a connection, persists it, and hot-applies the
// change: the driver is restarted or stopped, and the caller is expected to
// rearm dependent flow triggers (flowops.RearmDependents) afterwards.
func Update(ctx context.Context, c *models.Connection, p Patch) error {
	name := c.Name
	var changed []string
	credentials := false
	// Apply requested field updates.
	if p.Name != nil && *p.Name != c.Name {
		var count int64
		db.DB.Model(&models.Connection{}).Where("name = ? AND id != ?", *p.Name, c.ID).Count(&count)
		if count > 0 {
			return apperrors.ErrConflict.WithMessage("a connection with this name already exists")
		}
		c.Name = *p.Name
		changed = append(changed, "name")
	}
	if p.Config != nil {
		cfg := *p.Config
		var previous map[string]any
		_ = c.GetConfig(&previous)
		// Sentinel handling: masked secrets sent back by the client are
		// replaced with the currently stored values before re-encrypting.
		// An unreadable stored config still has to be replaceable, so in
		// that case the real secret must be supplied instead.
		var stored map[string]any
		readable := c.GetConfig(&stored) == nil
		for _, key := range secretKeys(c.Kind) {
			if s, ok := cfg[key].(string); ok && s == PasswordSentinel {
				if !readable {
					return apperrors.ErrValidation.WithMessage(
						fmt.Sprintf("the stored config could not be decrypted; re-enter %q to replace it", key))
				}
				cfg[key] = stored[key]
			}
		}
		if def, ok := integrations.KindFor(c.Kind); ok && def.RestoreConfig != nil {
			if !readable {
				stored = nil
			}
			if err := def.RestoreConfig(cfg, stored); err != nil {
				return apperrors.ErrValidation.WithMessage(err.Error())
			}
		}
		if err := validateConfig(c.Kind, cfg); err != nil {
			return err
		}
		if err := c.SetConfig(cfg); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		secrets := map[string]bool{}
		for _, key := range secretKeys(c.Kind) {
			secrets[key] = true
		}
		for key, v := range cfg {
			if fmt.Sprint(previous[key]) == fmt.Sprint(v) {
				continue
			}
			if secrets[key] {
				credentials = true
				continue
			}
			changed = append(changed, key)
		}
		for key := range previous {
			if _, ok := cfg[key]; !ok && !secrets[key] {
				changed = append(changed, key)
			}
		}
	}
	if p.Enabled != nil && *p.Enabled != c.Enabled {
		c.Enabled = *p.Enabled
		changed = append(changed, "enabled")
	}

	// Persist changes.
	if err := db.DB.Save(c).Error; err != nil {
		return apperrors.ErrInternal.WithError(err)
	}

	// Hot-apply: restart or stop the driver.
	if mgr != nil {
		if c.Enabled {
			_ = mgr.RestartConnection(c.ID)
		} else {
			mgr.StopConnection(c.ID)
		}
	}

	if len(changed) > 0 || credentials {
		sort.Strings(changed)
		parts := changed
		if credentials {
			parts = append(append([]string{}, changed...), "credentials")
		}
		recordChange(ctx, c, "Changed connection "+name+": "+strings.Join(parts, ", "),
			map[string]any{"fields": changed, "credentials_changed": credentials})
	}
	return nil
}

// Delete stops a connection's driver and removes the row. Deletion is blocked
// while any flow graph references the connection.
func Delete(ctx context.Context, c *models.Connection) error {
	count, err := models.CountConnectionDependencies(c.ID)
	if err != nil {
		return apperrors.ErrInternal.WithError(err)
	}
	if count > 0 {
		return apperrors.ErrConflict.WithMessage(
			fmt.Sprintf("connection is used by %d flow(s); remove it from their graphs first", count))
	}

	if mgr != nil {
		mgr.StopConnection(c.ID)
	}
	err = db.DB.Transaction(func(tx *gorm.DB) error {
		if err := models.DeleteConnectionAccess(tx, c.ID); err != nil {
			return err
		}
		return tx.Delete(&models.Connection{}, "id = ?", c.ID).Error
	})
	if err != nil {
		return apperrors.ErrInternal.WithError(err)
	}
	for _, fn := range deleteHooks {
		fn(c.ID)
	}
	recordChange(ctx, c, "Deleted connection "+c.Name, nil)
	return nil
}

// TestResult reports the outcome of a connection test.
type TestResult struct {
	OK    bool   `json:"ok" description:"Whether the test connection succeeded."`
	Error string `json:"error,omitempty" description:"Failure reason when not ok."`
	// Detail is what a probing kind found, such as the calendars an account
	// holds, shown beside the result.
	Detail string `json:"detail,omitempty" description:"What the test found, for kinds that report it."`
}

// Test performs the kind-specific connectivity test with a throwaway client
// against the stored configuration. A failed test is a result, not an error;
// the error return covers a kind without a test or an unreadable config.
func Test(ctx context.Context, c *models.Connection) (TestResult, error) {
	cfg, err := c.ConfigJSON()
	if err != nil {
		return TestResult{}, apperrors.ErrInternal.WithError(err)
	}
	def, ok := integrations.KindFor(c.Kind)
	if !ok || (def.Test == nil && def.Probe == nil) {
		return TestResult{}, apperrors.ErrValidation.WithMessage(fmt.Sprintf("connection kind %q has no test", c.Kind))
	}
	tctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	if def.Probe != nil {
		detail, probeErr := def.Probe(tctx, c.ID, cfg)
		if probeErr != nil {
			return TestResult{OK: false, Error: probeErr.Error(), Detail: detail}, nil
		}
		return TestResult{OK: true, Detail: detail}, nil
	}
	if testErr := def.Test(tctx, c.ID, cfg); testErr != nil {
		return TestResult{OK: false, Error: testErr.Error()}, nil
	}
	return TestResult{OK: true}, nil
}
