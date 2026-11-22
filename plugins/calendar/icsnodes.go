package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	ical "github.com/emersion/go-ical"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// The calendar-event and calendar-events nodes go and get a feed. These two do
// not: they read and write an ICS document that is already on the message,
// which is how one arrives from a mail attachment, an HTTP response, or a file.
// The parsing is the same code the feed provider uses, so a document read here
// expands recurrences exactly the way the trigger does.

// icsPropertyField is the "which value to convert" field both nodes share,
// matching the wording the built-in parsers use.
func icsPropertyField() engine.FieldSpec {
	return engine.FieldSpec{
		ID:          "property",
		Label:       "Property",
		Type:        flow.FieldString,
		Default:     "payload",
		Placeholder: "payload",
		Help:        "The value to convert. Dot path into the message; a bare path is read from the payload.",
	}
}

// icsTargetField is the "where the result goes" field both nodes share.
func icsTargetField() engine.FieldSpec {
	return engine.FieldSpec{
		ID:          "target",
		Label:       "Write the result to",
		Type:        flow.FieldString,
		Placeholder: "payload",
		Help:        "Dot path into the message. Empty overwrites the property that was converted.",
	}
}

// ---- ics ----

// ICSNode parses an iCalendar document that arrived on a message.
// It holds its own provider rather than reaching for the plugin's: parsing is
// stateless, and the map in Init is not built until after Register runs.
type ICSNode struct{ ics ICSProvider }

func (ICSNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "ics",
		Label:       "iCalendar",
		Folder:      flow.FolderParsers,
		Description: "Parses an iCalendar (ICS) document into events",
		Icon:        "las la-calendar-alt",
		Pure:        true,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortString},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []engine.FieldSpec{
			icsPropertyField(),
			icsTargetField(),
			{
				ID: "window_days", Label: "Window (days)", Type: flow.FieldNumber, Default: 365,
				Help: "How far ahead to expand repeating events. A document with none is unaffected, " +
					"but a yearly repeat with no window would otherwise expand forever.",
			},
			{
				ID: "past_days", Label: "Include the past (days)", Type: flow.FieldNumber, Default: 0,
				Help: "How far back to keep events. Zero starts the window now, which is what a " +
					"flow reacting to a calendar wants; an importer reading a whole file wants more.",
			},
		},
	}
}

// icsParseConfig is the decoded ics node configuration.
type icsParseConfig struct {
	Property   string `json:"property"`
	Target     string `json:"target"`
	WindowDays int    `json:"window_days"`
	PastDays   int    `json:"past_days"`
}

// Execute parses the document into the same event shape the feed nodes emit.
func (n *ICSNode) Execute(_ context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg icsParseConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inputs["in"]
	if msg == nil {
		return nil, fmt.Errorf("the ics node needs a message")
	}

	property, value, err := icsInput(msg, cfg.Property)
	if err != nil {
		return nil, err
	}
	text, err := icsText(value, property)
	if err != nil {
		return nil, err
	}

	window := cfg.WindowDays
	if window <= 0 {
		window = 365
	}
	now := ec.Clock().Now()
	events, err := n.ics.ParseEvents(strings.NewReader(text),
		now.AddDate(0, 0, -cfg.PastDays), now.AddDate(0, 0, window))
	if err != nil {
		return nil, err
	}

	items := make([]map[string]any, 0, len(events))
	for _, e := range events {
		items = append(items, eventPayload(e))
	}
	payload := map[string]any{"events": items, "count": len(items)}
	if len(items) > 0 {
		payload["next"] = items[0]
	}

	out, err := icsResult(msg, property, cfg.Target, payload)
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}

// ---- ics-encode ----

// ICSEncodeNode builds an iCalendar document from events on the message, for
// mailing an invite or writing a file something else subscribes to.
type ICSEncodeNode struct{}

func (ICSEncodeNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "ics-encode",
		Label:       "iCalendar Encode",
		Folder:      flow.FolderEncoders,
		Description: "Builds an iCalendar (ICS) document from events",
		Icon:        "las la-calendar-alt",
		Pure:        true,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortObject},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortString},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:          "property",
				Label:       "Events",
				Type:        flow.FieldString,
				Default:     "payload.events",
				Placeholder: "payload.events",
				Help: "An array of events, or one event object. Each takes title, start, end, " +
					"all_day, location, and description; start and end read the usual timestamp layouts.",
			},
			icsTargetField(),
			{
				ID: "calendar_name", Label: "Calendar name", Type: flow.FieldString,
				Placeholder: "House", Help: "Written as X-WR-CALNAME, which is what most clients show as the calendar's title.",
			},
			{
				ID: "product_id", Label: "Product ID", Type: flow.FieldString,
				Placeholder: "-//karakuri//EN",
				Help:        "The PRODID every document has to carry. Empty writes karakuri's own.",
			},
		},
	}
}

// icsEncodeConfig is the decoded ics-encode node configuration.
type icsEncodeConfig struct {
	Property     string `json:"property"`
	Target       string `json:"target"`
	CalendarName string `json:"calendar_name"`
	ProductID    string `json:"product_id"`
}

// Execute writes the events out as an ICS document.
func (ICSEncodeNode) Execute(_ context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg icsEncodeConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inputs["in"]
	if msg == nil {
		return nil, fmt.Errorf("the ics-encode node needs a message")
	}

	property := strings.TrimSpace(cfg.Property)
	if property == "" {
		property = "payload.events"
	}
	_, value, err := icsInput(msg, property)
	if err != nil {
		return nil, err
	}

	// One event or a list of them: a flow that built a single invite should not
	// have to wrap it in an array to encode it.
	var raw []any
	switch t := value.(type) {
	case []any:
		raw = t
	case map[string]any:
		raw = []any{t}
	default:
		return nil, fmt.Errorf("property %q is %T, not an event or a list of events", property, value)
	}

	cal := ical.NewCalendar()
	prodID := strings.TrimSpace(cfg.ProductID)
	if prodID == "" {
		prodID = "-//karakuri//iCalendar//EN"
	}
	cal.Props.SetText(ical.PropProductID, prodID)
	cal.Props.SetText(ical.PropVersion, "2.0")
	if name := strings.TrimSpace(cfg.CalendarName); name != "" {
		cal.Props.SetText("X-WR-CALNAME", name)
	}

	stamp := ec.Clock().Now().UTC()
	for i, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("event %d is %T, not an object", i, item)
		}
		comp, err := icsEvent(obj, i, stamp)
		if err != nil {
			return nil, err
		}
		cal.Children = append(cal.Children, comp)
	}

	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, fmt.Errorf("writing ICS: %w", err)
	}
	out, err := icsResult(msg, property, cfg.Target, buf.String())
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}

// icsEvent builds one VEVENT from a flow's event object.
func icsEvent(obj map[string]any, index int, stamp time.Time) (*ical.Component, error) {
	event := ical.NewEvent()
	comp := event.Component

	// A UID is required by the spec and is how a client recognises an update
	// rather than a second event, so one the flow supplies always wins.
	uid := icsString(obj["id"])
	if uid == "" {
		uid = icsString(obj["uid"])
	}
	if uid == "" {
		uid = fmt.Sprintf("%d-%d@karakuri", stamp.UnixNano(), index)
	}
	comp.Props.SetText(ical.PropUID, uid)
	comp.Props.SetDateTime(ical.PropDateTimeStamp, stamp)

	title := icsString(obj["title"])
	if title == "" {
		title = icsString(obj["summary"])
	}
	if title == "" {
		return nil, fmt.Errorf("event %d has no title", index)
	}
	comp.Props.SetText(ical.PropSummary, title)

	start, err := icsTime(obj["start"])
	if err != nil {
		return nil, fmt.Errorf("event %q start: %w", title, err)
	}
	allDay, _ := obj["all_day"].(bool)
	end, endErr := icsTime(obj["end"])
	if endErr != nil {
		if obj["end"] != nil {
			return nil, fmt.Errorf("event %q end: %w", title, endErr)
		}
		// No end is an hour for a timed event. An all-day one ends on the next
		// date: its DTEND is exclusive, so the start date would be an empty
		// span, and a day is what the parser assumes for one with no DTEND.
		end = start.Add(time.Hour)
		if allDay {
			end = start.AddDate(0, 0, 1)
		}
	}

	// An all-day event is a date rather than an instant, and a client shown a
	// midnight timestamp puts it in whatever zone it is reading in.
	if allDay {
		comp.Props.SetDate(ical.PropDateTimeStart, start)
		comp.Props.SetDate(ical.PropDateTimeEnd, end)
	} else {
		// The encoder labels a time outside UTC with its location's name. A
		// timestamp parsed from an offset has no name and the server's zone
		// is called "Local", neither of which another reader can resolve, so
		// a timed event is written as the UTC instant.
		comp.Props.SetDateTime(ical.PropDateTimeStart, start.UTC())
		comp.Props.SetDateTime(ical.PropDateTimeEnd, end.UTC())
	}

	if location := icsString(obj["location"]); location != "" {
		comp.Props.SetText(ical.PropLocation, location)
	}
	if description := icsString(obj["description"]); description != "" {
		comp.Props.SetText(ical.PropDescription, description)
	}
	return comp, nil
}

// icsInput resolves the property a node reads, reporting a path that is not
// there rather than converting nothing.
func icsInput(msg *engine.Msg, property string) (string, any, error) {
	property = strings.TrimSpace(property)
	if property == "" {
		property = "payload"
	}
	value, found := icsResolve(msg, property)
	if !found {
		return property, nil, fmt.Errorf("property %q is not present on the message", property)
	}
	return property, value, nil
}

// icsText reads a value as the document text to parse.
func icsText(value any, property string) (string, error) {
	switch t := value.(type) {
	case string:
		return t, nil
	case []byte:
		return string(t), nil
	default:
		return "", fmt.Errorf("property %q is %T, not an ICS document", property, value)
	}
}

// icsString renders an event field as text, treating a missing one as empty.
func icsString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// icsTime reads an event's start or end. It takes the layouts the rest of the
// system emits, which is what makes the output of the ics node valid input to
// ics-encode without a conversion in between.
func icsTime(v any) (time.Time, error) {
	if v == nil {
		return time.Time{}, fmt.Errorf("no timestamp")
	}
	if t, ok := v.(time.Time); ok {
		return t, nil
	}
	raw := icsString(v)
	if raw == "" {
		return time.Time{}, fmt.Errorf("no timestamp")
	}
	if n, ok := v.(float64); ok {
		return time.Unix(int64(n), 0).UTC(), nil
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a recognized timestamp", raw)
}

// icsResolve walks a dot path into a message, the same way the built-in nodes
// read a property. The plugin cannot reach the host's copy, so this is the
// small part that has to live on both sides.
func icsResolve(msg *engine.Msg, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any
	switch parts[0] {
	case "topic":
		cur = msg.Topic
		parts = parts[1:]
	case "meta":
		m := make(map[string]any, len(msg.Meta))
		for k, v := range msg.Meta {
			m[k] = v
		}
		cur = m
		parts = parts[1:]
	case "payload":
		cur = msg.Payload
		parts = parts[1:]
	default:
		cur = msg.Payload
	}
	for _, part := range parts {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// icsResult writes the converted value onto a copy of the message.
func icsResult(msg *engine.Msg, source, target string, value any) (*engine.Msg, error) {
	dest := strings.TrimSpace(target)
	if dest == "" {
		dest = source
	}
	out := &engine.Msg{
		Payload: msg.Payload,
		Topic:   msg.Topic,
		Meta:    map[string]any{},
		Group:   msg.Group,
	}
	for k, v := range msg.Meta {
		out.Meta[k] = v
	}

	parts := strings.Split(dest, ".")
	switch parts[0] {
	case "topic":
		out.Topic = fmt.Sprint(value)
		return out, nil
	case "meta":
		if len(parts) < 2 {
			return nil, fmt.Errorf("cannot write to %q: meta needs a key", dest)
		}
		out.Meta[strings.Join(parts[1:], ".")] = value
		return out, nil
	case "payload":
		parts = parts[1:]
	default:
		// A bare path is inside the payload.
	}
	if len(parts) == 0 {
		out.Payload = value
		return out, nil
	}

	obj, ok := out.Payload.(map[string]any)
	if !ok {
		obj = map[string]any{}
	} else {
		copied := make(map[string]any, len(obj))
		for k, v := range obj {
			copied[k] = v
		}
		obj = copied
	}
	out.Payload = obj
	for i, part := range parts {
		if i == len(parts)-1 {
			obj[part] = value
			break
		}
		// Each object on the way down is copied before it is written to: the
		// input message still holds the original, and so does every other
		// branch the message went to.
		next := map[string]any{}
		if existing, ok := obj[part].(map[string]any); ok {
			for k, v := range existing {
				next[k] = v
			}
		}
		obj[part] = next
		obj = next
	}
	return out, nil
}
