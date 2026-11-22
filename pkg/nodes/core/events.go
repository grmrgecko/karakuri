package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/controls"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
)

var eventLevels = []engine.Option{
	{Value: models.EventInfo, Label: "Info"}, {Value: models.EventWarning, Label: "Warning"}, {Value: models.EventError, Label: "Error"},
}

// LogEventNode adds an entry to the event log, attributed to the flow.
type LogEventNode struct{}

func (LogEventNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type: "log-event", Label: "Log event", Folder: flow.FolderUtility, Icon: "las la-stream",
		Description: "Adds an entry to the event log, such as \"Holiday mode on\"",
		Inputs:      []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}},
		Outputs:     []engine.PortSpec{{ID: "out", Label: "Out", Type: flow.PortAny}},
		ConfigSpec: []engine.FieldSpec{
			{ID: "message", Label: "Message", Type: flow.FieldString, Required: true, Help: "What happened. Message properties interpolate, such as {{payload.room}}."},
			{ID: "level", Label: "Level", Type: flow.FieldSelect, Default: models.EventInfo, Options: eventLevels},
			{ID: "control", Label: "About control", Type: flow.FieldString, Control: "control-picker", Help: "Optional: the entry shows with this control's other entries."},
			{ID: "device", Label: "About device", Type: flow.FieldString, Control: "mqtt-device", Help: "Optional: the entry shows with this device's other entries."},
			{ID: "include_payload", Label: "Include payload", Type: flow.FieldBoolean, Default: false, Help: "Store the message payload as the entry's details, up to 8 KiB."},
		},
	}
}

type logEventConfig struct {
	Message        string `json:"message"`
	Level          string `json:"level"`
	Control        string `json:"control"`
	Device         string `json:"device"`
	IncludePayload bool   `json:"include_payload"`
}

func validLevel(level string) (string, error) {
	switch level {
	case "":
		return models.EventInfo, nil
	case models.EventInfo, models.EventWarning, models.EventError:
		return level, nil
	}
	return "", fmt.Errorf("unknown level %q", level)
}

// Execute records the entry and passes the message on. The entry is written
// in the background, so the node never waits on the log.
func (LogEventNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg logEventConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	level, err := validLevel(cfg.Level)
	if err != nil {
		return nil, &engine.ExecutionError{Message: err.Error()}
	}
	msg := inbound(ec, inputs)
	text, err := ec.Interpolate(cfg.Message, msg)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return nil, &engine.ExecutionError{Message: "the event message is empty"}
	}
	entry := events.Entry{Category: models.EventCustom, Level: level, Message: text, Actor: events.ActorFrom(ctx)}
	switch {
	case cfg.Control != "":
		entry.Subject = events.Subject{Kind: "control", ID: cfg.Control, Name: cfg.Control}
		if id, err := uuid.Parse(cfg.Control); err == nil {
			if snap, ok := controls.S.Get(id); ok {
				entry.Subject.Name = snap.Name
			}
		}
	case cfg.Device != "":
		entry.Subject = events.Subject{Kind: "device", ID: cfg.Device, Name: cfg.Device}
		var name string
		db.DB.Table("mqtt_devices").Select("name").Where("id = ?", cfg.Device).Limit(1).Scan(&name)
		if name != "" {
			entry.Subject.Name = name
		}
	}
	if cfg.IncludePayload {
		entry.Details = map[string]any{"payload": msg.Payload}
	}
	events.Record(entry)
	return engine.One("out", msg), nil
}

// EventLoggedNode starts a run when a matching entry is logged.
type EventLoggedNode struct{}

func (EventLoggedNode) Meta() engine.NodeMeta {
	categories := make([]engine.Option, 0, len(models.EventCategories))
	for _, c := range models.EventCategories {
		categories = append(categories, engine.Option{Value: c, Label: strings.ToUpper(c[:1]) + c[1:]})
	}
	props := map[string]any{}
	for _, f := range []string{"category", "level", "message", "subject_kind", "subject_id", "subject_name", "actor_kind", "actor_name", "at"} {
		props[f] = map[string]any{"type": "string"}
	}
	props["count"] = map[string]any{"type": "integer"}
	props["details"] = map[string]any{"type": "object"}
	return engine.NodeMeta{
		Type: "event-logged", Label: "Event logged", Folder: flow.FolderUtility, Icon: "las la-stream",
		Category: flow.CategoryTrigger, TestWithoutParams: true,
		Description: "Starts a run when a matching entry is added to the event log, such as a failed sign-in",
		Outputs:     []engine.PortSpec{{ID: "out", Label: "Entry", Type: flow.PortObject, PayloadSchema: map[string]any{"type": "object", "properties": props}}},
		ConfigSpec: []engine.FieldSpec{
			{ID: "categories", Label: "Categories", Type: flow.FieldString, Control: "tokens", Options: categories, Help: "Empty matches every category except security. Security needs the flow's owner to hold the audit permission."},
			{ID: "levels", Label: "Levels", Type: flow.FieldString, Control: "tokens", Options: eventLevels, Help: "Empty matches every level."},
			{ID: "control", Label: "About control", Type: flow.FieldString, Control: "control-picker", Help: "Only entries about this control."},
			{ID: "device", Label: "About device", Type: flow.FieldString, Control: "mqtt-device", Help: "Only entries about this device."},
			{ID: "match", Label: "Message contains", Type: flow.FieldString, Help: "Only entries whose message contains this text, ignoring case."},
		},
	}
}

type eventLoggedConfig struct {
	Categories string `json:"categories"`
	Levels     string `json:"levels"`
	Control    string `json:"control"`
	Device     string `json:"device"`
	Match      string `json:"match"`
}

// eventMatcher is a parsed Event Logged configuration.
type eventMatcher struct {
	categories map[string]bool
	levels     map[string]bool
	subject    events.Subject
	match      string
	flowID     string
}

func tokens(raw string) map[string]bool {
	out := map[string]bool{}
	for _, t := range strings.Split(raw, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out[t] = true
		}
	}
	return out
}

func (cfg eventLoggedConfig) matcher(flowID string) (eventMatcher, error) {
	m := eventMatcher{categories: tokens(cfg.Categories), levels: tokens(cfg.Levels), match: strings.ToLower(strings.TrimSpace(cfg.Match)), flowID: flowID}
	for c := range m.categories {
		known := false
		for _, k := range models.EventCategories {
			known = known || c == k
		}
		if !known {
			return m, fmt.Errorf("unknown category %q", c)
		}
	}
	for l := range m.levels {
		if _, err := validLevel(l); err != nil {
			return m, err
		}
	}
	switch {
	case cfg.Control != "":
		m.subject = events.Subject{Kind: "control", ID: cfg.Control}
	case cfg.Device != "":
		m.subject = events.Subject{Kind: "device", ID: cfg.Device}
	}
	return m, nil
}

// matches reports whether an entry starts a run. Entries the flow itself
// caused never do: a flow that writes a control and listens for control
// entries would otherwise start itself.
func (m eventMatcher) matches(ev models.Event) bool {
	if m.flowID != "" && ev.ActorKind == events.ActorFlow && ev.ActorID == m.flowID {
		return false
	}
	if len(m.categories) == 0 {
		if ev.Category == models.EventSecurity {
			return false
		}
	} else if !m.categories[ev.Category] {
		return false
	}
	if len(m.levels) > 0 && !m.levels[ev.Level] {
		return false
	}
	if m.subject.Kind != "" && (ev.SubjectKind != m.subject.Kind || ev.SubjectID != m.subject.ID) {
		return false
	}
	if m.match != "" && !strings.Contains(strings.ToLower(ev.Message), m.match) {
		return false
	}
	return true
}

func eventMessage(ev models.Event) *engine.Msg {
	return &engine.Msg{Topic: ev.Category, Payload: map[string]any{
		"id": ev.ID, "category": ev.Category, "level": ev.Level, "message": ev.Message,
		"subject_kind": ev.SubjectKind, "subject_id": ev.SubjectID, "subject_name": ev.SubjectName,
		"actor_kind": ev.ActorKind, "actor_name": ev.ActorName, "count": ev.Count,
		"details": ev.Details, "at": ev.At.Format(time.RFC3339Nano),
	}}
}

// Execute forwards a trigger sample, or for a test the newest recent entry the
// node's settings match.
func (EventLoggedNode) Execute(_ context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg eventLoggedConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	m, err := cfg.matcher(flowOf(ec))
	if err != nil {
		return nil, err
	}
	if msg := inputs[engine.TriggerInput]; msg != nil && msg.Payload != nil {
		return engine.One("out", msg), nil
	}
	// The database narrows by category, level, and subject; the text match
	// and the flow's own entries are checked here, as the trigger checks them,
	// since LIKE is case-sensitive on some engines.
	filter := events.Filter{Limit: events.MaxLimit}
	for c := range m.categories {
		filter.Categories = append(filter.Categories, c)
	}
	for l := range m.levels {
		filter.Levels = append(filter.Levels, l)
	}
	if m.subject.Kind != "" {
		filter.Subjects = []events.Subject{m.subject}
	}
	if m.categories[models.EventSecurity] {
		if !ownerAudits(m.flowID) {
			return nil, errors.New("security entries need the flow's owner to hold the audit permission")
		}
		filter.Audit = true
	}
	page, err := events.List(filter)
	if err != nil {
		return nil, err
	}
	for _, ev := range page.Events {
		if m.matches(ev) {
			return engine.One("out", eventMessage(ev)), nil
		}
	}
	return nil, errors.New("no recent entry in the event log matches")
}

// Start emits each new matching entry until disarmed. A repeat folded into
// an earlier entry does not start another run.
func (EventLoggedNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg eventLoggedConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	m, err := cfg.matcher(flowOf(ec))
	if err != nil {
		return err
	}
	if m.categories[models.EventSecurity] && !ownerAudits(m.flowID) {
		return errors.New("security entries need the flow's owner to hold the audit permission")
	}
	if events.R == nil {
		return errors.New("the event log is not running")
	}
	found := make(chan models.Event, 64)
	stop := events.R.Watch(func(ev models.Event, update bool) {
		if update || !m.matches(ev) {
			return
		}
		select {
		case found <- ev:
		default:
		}
	})
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-found:
			emit(eventMessage(ev))
		}
	}
}

// ownerAudits reports whether a flow's owner holds the audit permission.
func ownerAudits(flowID string) bool {
	var owner string
	db.DB.Table("flows").Select("owner_id").Where("id = ?", flowID).Limit(1).Scan(&owner)
	id, err := uuid.Parse(owner)
	return err == nil && auth.HasPermission(id, perm.PermEventsAudit)
}
