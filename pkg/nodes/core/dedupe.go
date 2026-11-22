package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// Dedupe logics, naming what makes a message worth passing on.
const (
	dedupeNew    = "new"
	dedupeHigher = "higher"
	dedupeLater  = "later"
)

// dedupeSeenKey, dedupeMaxKey, and dedupeLatestKey name each logic's memory in
// the node store. They are separate keys so switching logic on a running flow
// starts that logic from nothing rather than reading the other one's state.
const (
	dedupeSeenKey   = "seen"
	dedupeMaxKey    = "max"
	dedupeLatestKey = "latest"
)

// DedupeNode drops messages that say nothing new. A sensor reporting "open"
// every thirty seconds should notify once, and a poller that re-reads a feed
// should forward only what it has not forwarded before, so the three logics
// are "this key is new", "this number beats every previous one", and "this
// timestamp is later than every previous one".
//
// State lives in the node's durable store, so it spans runs — each message
// arrives in a run of its own and comparing within a run would compare
// nothing — and survives a redeploy and a restart: "notify once when the door
// opens" must not notify again because the server rebooted. The history is
// removed only when the flow is deleted.
//
// The node is deliberately not retryable. A retry would run the same message
// through the same claim a second time and drop it as a duplicate of itself.
type DedupeNode struct{}

func (DedupeNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:            "dedupe",
		Label:           "Dedupe",
		Folder:          flow.FolderLogic,
		Description:     "Drops messages whose value has been seen, or is no newer than the last",
		Icon:            "las la-fingerprint",
		InvocationState: flow.InvocationStateStateful,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
			{ID: "reset", Label: "Reset", Type: flow.PortAny, Optional: true},
		},
		Outputs: []engine.PortSpec{
			// A message either passes through untouched or does not pass at
			// all, so the output carries whatever the input did.
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			{ID: "duplicate", Label: "Duplicate", Type: flow.PortAny, TypeFrom: "in"},
		},
		ConfigSpec: []engine.FieldSpec{
			{ID: "expiry", Label: "Expiry", Type: flow.FieldDuration, Placeholder: "1h", Help: "Forget an admitted value or watermark after this duration. Duplicates do not extend it. Empty retains history until reset or eviction."},
			{
				ID:          "key",
				Label:       "Value",
				Type:        flow.FieldString,
				Default:     "payload",
				Placeholder: "payload.event_id",
				Help:        "The value to compare. Dot path into the message; a bare path is read from the payload.",
			},
			{
				ID:      "logic",
				Label:   "Pass the message on when",
				Type:    flow.FieldSelect,
				Default: dedupeNew,
				Options: []engine.Option{
					{Value: dedupeNew, Label: "The value has not been seen before"},
					{Value: dedupeHigher, Label: "The value is higher than every previous value"},
					{Value: dedupeLater, Label: "The value is a date later than every previous date"},
				},
			},
			{
				ID:          "history",
				Label:       "Values remembered",
				Type:        flow.FieldString,
				Default:     "1000",
				Placeholder: "1000",
				ShowIf:      &flow.ShowIf{Field: "logic", Equals: dedupeNew},
				Help: "How many values to remember. The oldest is forgotten once the list is " +
					"full, so a value that has not been seen for that many messages passes again.",
			},
		},
	}
}

// dedupeConfig is the decoded dedupe node configuration.
type dedupeConfig struct {
	Expiry  string `json:"expiry"`
	Key     string `json:"key"`
	Logic   string `json:"logic"`
	History string `json:"history"`
}

// Execute passes the message on only when its value is one this node has not
// already accounted for.
func (DedupeNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg dedupeConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	expiry, err := parseDurationDefault(cfg.Expiry, 0)
	if err != nil || expiry < 0 {
		return nil, fmt.Errorf("invalid dedupe expiry %q", cfg.Expiry)
	}
	if inputs["reset"] != nil && inputs["in"] == nil {
		for _, key := range []string{dedupeSeenKey, dedupeMaxKey, dedupeLatestKey} {
			ec.DurableStore().Delete(key)
		}
		return nil, nil
	}
	msg := inbound(ec, inputs)

	// Read the value being compared. A path that is not on the message is a
	// wiring mistake rather than a duplicate, so it fails instead of quietly
	// deduplicating every message against the same missing value.
	path := strings.TrimSpace(cfg.Key)
	if path == "" {
		path = "payload"
	}
	value, found := resolveProperty(msg, path)
	if !found {
		return nil, fmt.Errorf("value %q is not present on the message", path)
	}

	// Claim the value. Every logic decides and records in one Update, because
	// concurrent runs of the same flow share the store and two of them reading
	// before either writes would both pass.
	var pass bool
	switch cfg.Logic {
	case "", dedupeNew:
		pass, err = dedupeClaimNew(ec, cfg, value, expiry)
	case dedupeHigher:
		pass, err = dedupeClaimHigher(ec, value, expiry)
	case dedupeLater:
		pass, err = dedupeClaimLater(ec, value, expiry)
	default:
		return nil, fmt.Errorf("unknown dedupe logic %q", cfg.Logic)
	}
	if err != nil {
		return nil, err
	}
	if !pass {
		ec.Debug(value, "duplicate; message suppressed")
		return engine.One("duplicate", msg), nil
	}
	return engine.One("out", msg), nil
}

// dedupeEntry retains legacy scalar values and attaches admission time only
// when expiry is enabled. Old history starts its clock on first expiry-aware read.
func dedupeEntry(cur any, now time.Time, expiry time.Duration) (value any, admitted time.Time) {
	if record, ok := cur.(map[string]any); ok {
		value = record["value"]
		admitted, _ = storedTime(record["admitted_at"])
	} else {
		value = cur
	}
	if expiry > 0 && admitted.IsZero() {
		admitted = now
	}
	return value, admitted
}

// encodeDedupeEntry preserves scalar history when no admission time is needed.
func encodeDedupeEntry(value any, admitted time.Time) any {
	if admitted.IsZero() {
		return value
	}
	return map[string]any{"value": value, "admitted_at": admitted.Format(time.RFC3339Nano)}
}

// dedupeClaimNew admits unseen or expired values under the history count bound.
func dedupeClaimNew(ec engine.ExecContext, cfg dedupeConfig, value any, expiry time.Duration) (bool, error) {
	limit, err := parseDedupeHistory(cfg.History)
	if err != nil {
		return false, err
	}
	key := storableText(stringify(value))
	now := ec.Clock().Now()
	pass := true
	ec.DurableStore().Update(dedupeSeenKey, func(cur any, _ bool) any {
		list, _ := cur.([]any)
		order := make([]any, 0, len(list)+1)
		for _, entry := range list {
			v, at := dedupeEntry(entry, now, expiry)
			seen, ok := v.(string)
			if !ok {
				continue
			}
			if expiry > 0 && !now.Before(at.Add(expiry)) {
				continue
			}
			if seen == key {
				pass = false
			}
			order = append(order, encodeDedupeEntry(seen, at))
		}
		if pass {
			var at time.Time
			if expiry > 0 {
				at = now
			}
			order = append(order, encodeDedupeEntry(key, at))
		}
		if excess := len(order) - limit; excess > 0 {
			order = order[excess:]
		}
		return order
	})
	return pass, nil
}

// dedupeClaimHigher compares the durable watermark until its admission expires.
func dedupeClaimHigher(ec engine.ExecContext, value any, expiry time.Duration) (bool, error) {
	n, ok := sortNumber(value)
	if !ok {
		return false, fmt.Errorf("value %v is not a number, so it cannot be compared against the highest seen", value)
	}
	now := ec.Clock().Now()
	pass := false
	ec.DurableStore().Update(dedupeMaxKey, func(cur any, exists bool) any {
		v, at := dedupeEntry(cur, now, expiry)
		max, valid := v.(float64)
		if !exists || !valid || n > max || (expiry > 0 && !now.Before(at.Add(expiry))) {
			pass = true
			at = time.Time{}
			if expiry > 0 {
				at = now
			}
			return encodeDedupeEntry(n, at)
		}
		return encodeDedupeEntry(max, at)
	})
	return pass, nil
}

// dedupeClaimLater compares timestamps without changing the admission on repeats.
func dedupeClaimLater(ec engine.ExecContext, value any, expiry time.Duration) (bool, error) {
	t, err := parseTimeValue(value, time.Local)
	if err != nil {
		return false, fmt.Errorf("value cannot be compared against the latest seen: %w", err)
	}
	now := ec.Clock().Now()
	pass := false
	ec.DurableStore().Update(dedupeLatestKey, func(cur any, exists bool) any {
		v, at := dedupeEntry(cur, now, expiry)
		latest, valid := storedTime(v)
		if !exists || !valid || t.After(latest) || (expiry > 0 && !now.Before(at.Add(expiry))) {
			pass = true
			at = time.Time{}
			if expiry > 0 {
				at = now
			}
			return encodeDedupeEntry(t.Format(time.RFC3339Nano), at)
		}
		return encodeDedupeEntry(v, at)
	})
	return pass, nil
}

// parseDedupeHistory reads the history size, rejecting a value that would make
// the node either useless or unbounded.
func parseDedupeHistory(raw string) (int, error) {
	n, err := parseGroupCount(raw)
	if err != nil {
		return 0, fmt.Errorf("values remembered: %w", err)
	}
	if n == 0 {
		return 1000, nil
	}
	return n, nil
}
