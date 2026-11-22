package main

import "time"

// CalendarEvent is one occurrence of an event: a single event, or one instance
// of a recurring one.
type CalendarEvent struct {
	// ID identifies the occurrence: its event's UID and original start, so a
	// moved occurrence keeps it and each instance of a recurring event has
	// its own.
	ID       string `json:"id"`
	UID      string `json:"uid,omitempty"`
	Calendar string `json:"calendar,omitempty"`
	Title    string `json:"title"`
	Start    string `json:"start"`
	End      string `json:"end"`
	AllDay   bool   `json:"all_day"`
	Color    string `json:"color"`
	Location string `json:"location"`
	// Recurring marks an instance of a recurring event.
	Recurring bool `json:"recurring,omitempty"`
	// Tag is the tag of the source the event came from, for widgets that show
	// only some sources, such as one person's.
	Tag string `json:"tag,omitempty"`
}

// startTime returns the occurrence's start.
func (e CalendarEvent) startTime() time.Time {
	t, _ := time.Parse(time.RFC3339, e.Start)
	return t
}

// endTime returns the occurrence's end.
func (e CalendarEvent) endTime() time.Time {
	t, _ := time.Parse(time.RFC3339, e.End)
	return t
}

// CalendarData holds events and fetch metadata for a widget's merged sources.
type CalendarData struct {
	Events    []CalendarEvent `json:"events"`
	FetchedAt int64           `json:"fetched_at"`
}

// SourceConfig is one calendar a widget shows: a calendar connection, how
// the widget labels and colours it, and optionally which of the connection's
// calendars to show.
type SourceConfig struct {
	ConnectionID string `json:"connection_id"`
	Name         string `json:"name"`
	Color        string `json:"color"`
	// Tag groups sources a widget can show selectively, such as by person.
	Tag string `json:"tag,omitempty"`
	// Calendars narrows a connection that reads several calendars.
	Calendars []string `json:"calendars,omitempty"`
}
