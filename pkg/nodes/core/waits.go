package core

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
)

// armExecWait stores a wait owned by this execution alone, under a key no
// other execution shares, and returns the store, key, and armed wait.
func armExecWait(ec engine.ExecContext, deadline time.Time, state any) (engine.WaitStore, string, engine.Wait, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, "", engine.Wait{}, fmt.Errorf("wait cannot be stored: %w", err)
	}
	waits, id := engine.WaitsFor(ec)
	key := "exec:" + id
	mine, durable := waits.Update(key, func(engine.Wait, bool) *engine.Wait {
		return &engine.Wait{Deadline: deadline, State: raw}
	})
	noteNotDurable(ec, durable)
	return waits, key, mine, nil
}

// noteNotDurable records on the run that a deployed run's wait could not be
// stored and will not survive a restart.
func noteNotDurable(ec engine.ExecContext, durable bool) {
	if !durable && engine.IsDurableRun(ec) {
		ec.Debug(nil, "wait could not be stored; it will not survive a restart")
	}
}

// parkClaim parks until the wait owned by mine comes due, following deadline
// changes, then claims it. It returns the wait as claimed, or false when the
// wait was reset, superseded, or cancelled. A suspended park leaves the wait
// stored for its resume; any other cancellation releases it.
func parkClaim(ctx context.Context, ec engine.ExecContext, waits engine.WaitStore, key string, mine engine.Wait) (engine.Wait, bool, error) {
	reached, err := awaitDeadline(ctx, ec, mine.Deadline, func() (time.Time, bool) {
		cur, ok := waits.Get(key)
		if !ok || cur.Token != mine.Token {
			return time.Time{}, false
		}
		if !cur.Deadline.After(ec.Clock().Now()) {
			return time.Time{}, true
		}
		return cur.Deadline, true
	})
	if err != nil {
		if !engine.Suspended(ctx) {
			releaseWait(waits, key, mine.Token)
		}
		return engine.Wait{}, false, err
	}
	if !reached {
		return engine.Wait{}, false, nil
	}
	claimed, ok, err := waits.Claim(key, mine.Token)
	if err != nil {
		return engine.Wait{}, false, fmt.Errorf("wait could not be claimed: %w", err)
	}
	return claimed, ok, nil
}

// decodeWaitState reads a stored wait's state into out.
func decodeWaitState(w engine.Wait, out any) error {
	if err := json.Unmarshal(w.State, out); err != nil {
		return fmt.Errorf("stored wait is unreadable: %w", err)
	}
	return nil
}

// storedMsg is a message as a wait stores it. A binary payload is kept apart
// from the JSON payload so a restored message gets its bytes back rather
// than their base64 text.
type storedMsg struct {
	ID            string              `json:"id,omitempty"`
	Payload       any                 `json:"payload,omitempty"`
	PayloadBinary []byte              `json:"payload_binary,omitempty"`
	Topic         string              `json:"topic,omitempty"`
	Meta          map[string]any      `json:"meta,omitempty"`
	Group         []engine.GroupFrame `json:"group,omitempty"`
	Continuation  bool                `json:"continuation,omitempty"`
}

// storeMsg converts a message to its stored form.
func storeMsg(m *engine.Msg) *storedMsg {
	if m == nil {
		return nil
	}
	s := &storedMsg{ID: m.ID, Payload: m.Payload, Topic: m.Topic, Meta: m.Meta, Group: m.Group, Continuation: m.Continuation}
	if b, ok := m.Payload.([]byte); ok {
		s.Payload, s.PayloadBinary = nil, b
	}
	return s
}

// msg converts a stored message back.
func (s *storedMsg) msg() *engine.Msg {
	if s == nil {
		return nil
	}
	m := &engine.Msg{ID: s.ID, Payload: s.Payload, Topic: s.Topic, Meta: s.Meta, Group: s.Group, Continuation: s.Continuation}
	if s.PayloadBinary != nil {
		m.Payload = s.PayloadBinary
	}
	return m
}
