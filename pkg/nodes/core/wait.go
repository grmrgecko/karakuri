package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// WaitUntilNode holds a message until an instant the message itself names. The
// delay node waits a duration the author configured; this one waits for a time
// that is not known until the data arrives, which is what "turn the heating on
// forty minutes before the calendar event" needs.
//
// The wait uses ExecContext.Park, so the run keeps its place without holding a
// worker slot and without spending its timeout. That is what makes waiting
// until tomorrow reasonable rather than a run that dies at the flow's limit.
//
// An instant already past emits immediately and says so in the metadata. A
// schedule that fires late is still a schedule, and failing on it would turn
// every restart into a run of errors for events that merely happened while the
// server was down.
type WaitUntilNode struct{}

func (WaitUntilNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "wait-until",
		Label:       "Wait Until",
		Folder:      flow.FolderTime,
		Description: "Holds a message until an instant the message names",
		Icon:        "las la-calendar-check",
		Retryable:   true,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: append([]engine.FieldSpec{
			{
				ID:          "time",
				Label:       "Wait until",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "payload.starts_at",
				Help: "The instant to wait for. A dot path into the message, or a literal " +
					"timestamp. Reads RFC 3339, an HTTP date, \"2006-01-02 15:04:05\", a plain " +
					"date, and a Unix time in seconds or milliseconds.",
			},
			{
				ID:          "offset",
				Label:       "Offset",
				Type:        flow.FieldDuration,
				Placeholder: "-40m",
				Help:        "Added to that instant. Negative waits until before it.",
			},
			{
				ID:    "location",
				Label: "Time zone",
				Type:  flow.FieldTimezone,
				Help: "IANA time zone used to read timestamps that carry no zone of their own. " +
					"Empty uses the server's local time.",
			},
			{
				ID:          "max_wait",
				Label:       "Longest wait",
				Type:        flow.FieldDuration,
				Placeholder: "24h",
				Help: "Fail rather than wait longer than this. Empty waits however long the " +
					"instant is away, which a wrong timestamp upstream can make years.",
			},
		}, engine.MissedDeadlineFields()...),
	}
}

// waitUntilConfig is the decoded wait-until node configuration.
type waitUntilConfig struct {
	Time     string `json:"time"`
	Offset   string `json:"offset"`
	Location string `json:"location"`
	MaxWait  string `json:"max_wait"`
}

// Execute waits for the configured instant and then passes the message on.
func (WaitUntilNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg waitUntilConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	// Resolve the target instant.
	loc, err := waitLocation(cfg.Location)
	if err != nil {
		return nil, err
	}
	target, err := waitTarget(cfg, msg, loc)
	if err != nil {
		return nil, err
	}
	offset, err := parseDurationDefault(cfg.Offset, 0)
	if err != nil {
		return nil, fmt.Errorf("invalid offset %q: %w", cfg.Offset, err)
	}
	target = target.Add(offset)

	// Wait, unless the instant has already gone by.
	now := ec.Clock().Now()
	wait := target.Sub(now)
	if wait > 0 {
		max, err := parseDurationDefault(cfg.MaxWait, 0)
		if err != nil {
			return nil, fmt.Errorf("invalid longest wait %q: %w", cfg.MaxWait, err)
		}
		if max > 0 && wait > max {
			return nil, fmt.Errorf("%s is %s away, longer than the %s allowed",
				target.Format(time.RFC3339), wait.Round(time.Second), max)
		}
		waits, key, mine, err := armExecWait(ec, target, waitUntilState{Msg: storeMsg(msg)})
		if err != nil {
			return nil, err
		}
		_, ok, err := parkClaim(ctx, ec, waits, key, mine)
		if err != nil || !ok {
			return nil, err
		}
	}
	return waitUntilOutput(msg, target, wait <= 0), nil
}

// waitUntilState is a stored wait-until wait.
type waitUntilState struct {
	Msg *storedMsg `json:"msg"`
}

// Resume finishes a wait stored before a restart or a redeploy.
func (WaitUntilNode) Resume(ctx context.Context, ec engine.ExecContext, key string, w engine.Wait) (map[string][]*engine.Msg, error) {
	var state waitUntilState
	if err := decodeWaitState(w, &state); err != nil {
		return nil, err
	}
	waits, _ := engine.WaitsFor(ec)
	_, ok, err := parkClaim(ctx, ec, waits, key, w)
	if err != nil || !ok {
		return nil, err
	}
	return waitUntilOutput(state.Msg.msg(), w.Deadline, false), nil
}

// waitUntilOutput reports what happened: a flow that acted late usually wants
// to know it was late, and the run history is where it can be read
// afterwards.
func waitUntilOutput(msg *engine.Msg, target time.Time, late bool) map[string][]*engine.Msg {
	out := &engine.Msg{
		Payload: msg.Payload,
		Topic:   msg.Topic,
		Meta:    nodeutil.CloneMeta(msg.Meta),
		Group:   msg.Group,
	}
	out.Meta["waited_until"] = target.Format(time.RFC3339)
	if late {
		out.Meta["late"] = true
	}
	return engine.One("out", out)
}

// waitTarget reads the instant to wait for. The field is a dot path when the
// message has one at that path, and a literal timestamp otherwise, so a fixed
// deadline needs no separate mode and a value edge feeding the field works
// either way.
func waitTarget(cfg waitUntilConfig, msg *engine.Msg, loc *time.Location) (time.Time, error) {
	raw := strings.TrimSpace(cfg.Time)
	if raw == "" {
		return time.Time{}, fmt.Errorf("wait-until needs an instant to wait for")
	}
	if value, found := resolveProperty(msg, raw); found {
		t, err := parseTimeValue(value, loc)
		if err != nil {
			return time.Time{}, fmt.Errorf("%s: %w", raw, err)
		}
		return t, nil
	}
	return parseTimeValue(raw, loc)
}

// waitLocation resolves the configured time zone, which only matters for
// timestamps that carry none of their own.
func waitLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown time zone %q: %w", name, err)
	}
	return loc, nil
}
