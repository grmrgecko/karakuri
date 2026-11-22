package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// DelayNode holds a message back. Beyond a plain wait it also does the two jobs
// that keep chattery devices from flooding a flow: rate limiting (at most one
// message per interval) and debouncing (emit only after things go quiet).
//
// Waits use ExecContext.Park, so the run stays alive without burning its
// timeout and without holding a worker slot. Every held message is a stored
// wait, so a restart or a redeploy resumes it rather than losing it. Rate and
// debounce state spans runs, which is the point: each message arrives in a
// run of its own. The rate schedule is durable too — a restart must not
// reopen the bucket.
type DelayNode struct{}

func (DelayNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:            "delay",
		Label:           "Delay",
		Folder:          flow.FolderTime,
		Description:     "Delays, rate limits, or debounces messages",
		Icon:            "las la-hourglass-half",
		Retryable:       true,
		InvocationState: flow.InvocationStateStateful,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
			{ID: "reset", Label: "Reset", Type: flow.PortAny, Optional: true},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			{ID: "dropped", Label: "Dropped", Type: flow.PortAny, TypeFrom: "in"},
			{ID: "superseded", Label: "Superseded", Type: flow.PortAny, TypeFrom: "in"},
		},
		ConfigSpec: append([]engine.FieldSpec{
			stateKeyField(),
			{ID: "max_wait", Label: "Maximum wait", Type: flow.FieldDuration, ShowIf: &flow.ShowIf{Field: "mode", Equals: "debounce"}, Help: "Maximum debounce window from its first arrival. Empty allows chatter to extend the quiet period indefinitely."},
			{
				ID:      "mode",
				Label:   "Mode",
				Type:    flow.FieldSelect,
				Default: "fixed",
				Options: []engine.Option{
					{Value: "fixed", Label: "Delay each message"},
					{Value: "random", Label: "Delay by a random amount"},
					{Value: "rate", Label: "Rate limit"},
					{Value: "debounce", Label: "Debounce (wait for quiet)"},
				},
			},
			{
				ID:          "duration",
				Label:       "Duration",
				Type:        flow.FieldDuration,
				Required:    true,
				Default:     "1s",
				Placeholder: "1s",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "fixed"},
				Help:        "How long to hold each message.",
			},
			{
				ID:          "min_duration",
				Label:       "Minimum",
				Type:        flow.FieldDuration,
				Default:     "1s",
				Placeholder: "1s",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "random"},
			},
			{
				ID:          "max_duration",
				Label:       "Maximum",
				Type:        flow.FieldDuration,
				Default:     "5s",
				Placeholder: "5s",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "random"},
			},
			{
				ID:          "interval",
				Label:       "Interval",
				Type:        flow.FieldDuration,
				Default:     "1s",
				Placeholder: "1s",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "rate"},
				Help:        "Minimum spacing between emitted messages.",
			},
			{
				ID:      "on_limit",
				Label:   "When over the limit",
				Type:    flow.FieldSelect,
				Default: "drop",
				Options: []engine.Option{
					{Value: "drop", Label: "Drop the message"},
					{Value: "queue", Label: "Hold it until its turn"},
				},
				ShowIf: &flow.ShowIf{Field: "mode", Equals: "rate"},
				Help: "Queued messages each hold their run open until released, so the " +
					"effective queue depth is the flow's concurrent run limit.",
			},
			{
				ID:          "quiet",
				Label:       "Quiet period",
				Type:        flow.FieldDuration,
				Default:     "1s",
				Placeholder: "1s",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "debounce"},
				Help:        "Emit the most recent message once no new one has arrived for this long.",
			},
		}, engine.MissedDeadlineFields()...),
	}
}

// delayConfig is the decoded delay node configuration.
type delayConfig struct {
	Key         string `json:"key"`
	MaxWait     string `json:"max_wait"`
	Mode        string `json:"mode"`
	Duration    string `json:"duration"`
	MinDuration string `json:"min_duration"`
	MaxDuration string `json:"max_duration"`
	Interval    string `json:"interval"`
	OnLimit     string `json:"on_limit"`
	Quiet       string `json:"quiet"`
}

// Execute holds the message according to the configured mode.
func (DelayNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg delayConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)
	key, err := messageStateKey(msg, cfg.Key)
	if err != nil {
		return nil, err
	}
	waits, _ := engine.WaitsFor(ec)
	if inputs["reset"] != nil && inputs["in"] == nil {
		// A new epoch invalidates every message queued under the old one, and
		// the cleared schedule frees the bucket.
		waits.Update(keyedStoreKey(rateEpochKey, key), func(engine.Wait, bool) *engine.Wait {
			ec.DurableStore().Delete(keyedStoreKey(rateStoreKey, key))
			return &engine.Wait{}
		})
		// The window's owner is still parked and has not seen every arrival,
		// so the latest message is handed to it for its superseded output.
		waits.Update(keyedStoreKey(debounceStoreKey, key), func(cur engine.Wait, ok bool) *engine.Wait {
			var state delayWait
			if ok && decodeWaitState(cur, &state) == nil {
				final := state.Msg.msg()
				if live, found := ec.NodeStore().Get(debounceLatestKey(cur.Token)); found {
					final, _ = live.(*engine.Msg)
				}
				ec.NodeStore().Delete(debounceLatestKey(cur.Token))
				ec.NodeStore().Set(debounceFinalKey(cur.Token), final)
			}
			return nil
		})
		return nil, nil
	}

	switch cfg.Mode {
	case "", "fixed":
		d, err := parseRequiredDuration(cfg.Duration)
		if err != nil {
			return nil, fmt.Errorf("invalid duration %q: %w", cfg.Duration, err)
		}
		return holdDelay(ctx, ec, msg, d)

	case "random":
		d, err := randomDelay(cfg)
		if err != nil {
			return nil, err
		}
		return holdDelay(ctx, ec, msg, d)

	case "rate":
		return rateLimit(ctx, ec, cfg, msg, key)

	case "debounce":
		return debounce(ctx, ec, cfg, msg, key)

	default:
		return nil, fmt.Errorf("unknown delay mode %q", cfg.Mode)
	}
}

// delayWait is the stored state of every delay wait. Kind says which mode
// armed it, so a resume after a mode change still finishes the wait it has.
type delayWait struct {
	Kind string     `json:"kind"`
	Msg  *storedMsg `json:"msg"`
	// Maximum is the end of a debounce window's maximum wait, zero for none.
	Maximum time.Time `json:"maximum,omitempty"`
	// Epoch is the rate-limit epoch a queued message was queued under.
	Epoch string `json:"epoch,omitempty"`
	// RateKey is the keyed bucket a queued message belongs to.
	RateKey string `json:"rate_key,omitempty"`
}

// Kinds of delay wait.
const (
	delayKindHold     = "hold"
	delayKindDebounce = "debounce"
	delayKindQueue    = "queue"
)

// Resume finishes a delay wait stored before a restart or a redeploy.
func (DelayNode) Resume(ctx context.Context, ec engine.ExecContext, key string, w engine.Wait) (map[string][]*engine.Msg, error) {
	var cfg delayConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	var state delayWait
	if err := decodeWaitState(w, &state); err != nil {
		return nil, err
	}
	waits, _ := engine.WaitsFor(ec)
	switch state.Kind {
	case delayKindHold:
		return finishHold(ctx, ec, waits, key, w, state.Msg.msg())
	case delayKindDebounce:
		return finishDebounce(ctx, ec, waits, key, w, state.Msg.msg())
	case delayKindQueue:
		return resumeQueued(ctx, ec, cfg, waits, key, w, state)
	default:
		return nil, fmt.Errorf("unknown stored delay wait %q", state.Kind)
	}
}

// holdDelay holds one message for d in a wait of its own.
func holdDelay(ctx context.Context, ec engine.ExecContext, msg *engine.Msg, d time.Duration) (map[string][]*engine.Msg, error) {
	waits, key, mine, err := armExecWait(ec, ec.Clock().Now().Add(d), delayWait{Kind: delayKindHold, Msg: storeMsg(msg)})
	if err != nil {
		return nil, err
	}
	return finishHold(ctx, ec, waits, key, mine, msg)
}

// finishHold parks a held message to its deadline and emits it.
func finishHold(ctx context.Context, ec engine.ExecContext, waits engine.WaitStore, key string, mine engine.Wait, msg *engine.Msg) (map[string][]*engine.Msg, error) {
	_, ok, err := parkClaim(ctx, ec, waits, key, mine)
	if err != nil || !ok {
		return nil, err
	}
	return engine.One("out", msg), nil
}

// randomDelay picks a wait uniformly between the configured bounds.
func randomDelay(cfg delayConfig) (time.Duration, error) {
	min, err := parseDurationDefault(cfg.MinDuration, time.Second)
	if err != nil {
		return 0, fmt.Errorf("invalid minimum %q: %w", cfg.MinDuration, err)
	}
	max, err := parseDurationDefault(cfg.MaxDuration, 5*time.Second)
	if err != nil {
		return 0, fmt.Errorf("invalid maximum %q: %w", cfg.MaxDuration, err)
	}
	if max <= min {
		return min, nil
	}
	return min + time.Duration(rand.Int63n(int64(max-min))), nil
}

// rateStoreKey names the next-allowed timestamp in the durable store.
const rateStoreKey = "next_at"

// rateEpochKey names the state-only wait whose token is the current rate
// epoch. A reset replaces it, invalidating every message queued before.
const rateEpochKey = "rate_epoch"

// claimRateSlot claims the next free slot of a keyed bucket under the
// durable store's lock, so two runs cannot claim the same one. admitted is
// false when the bucket is full and the node drops rather than queues.
func claimRateSlot(ec engine.ExecContext, key string, interval time.Duration, queue bool) (slot time.Time, admitted bool) {
	now := ec.Clock().Now()
	ec.DurableStore().Update(keyedStoreKey(rateStoreKey, key), func(cur any, ok bool) any {
		next, isTime := storedTime(cur)
		if !ok || !isTime || !next.After(now) {
			// The bucket is free: emit right away.
			slot, admitted = now, true
			return now.Add(interval).Format(time.RFC3339Nano)
		}
		if !queue {
			// Dropping must leave the schedule untouched, otherwise a burst of
			// dropped messages would keep pushing the next slot out.
			return cur
		}
		slot, admitted = next, true
		return next.Add(interval).Format(time.RFC3339Nano)
	})
	return slot, admitted
}

// currentEpoch returns the bucket's rate epoch, creating one on first use.
func currentEpoch(waits engine.WaitStore, key string) string {
	epoch, _ := waits.Update(keyedStoreKey(rateEpochKey, key), func(cur engine.Wait, ok bool) *engine.Wait {
		if ok {
			return &cur
		}
		return &engine.Wait{}
	})
	return epoch.Token
}

// rateLimit spaces emitted messages at least one interval apart. Over the limit
// a message is either dropped or held until its slot, depending on config.
func rateLimit(ctx context.Context, ec engine.ExecContext, cfg delayConfig, msg *engine.Msg, key string) (map[string][]*engine.Msg, error) {
	interval, err := parseDurationDefault(cfg.Interval, time.Second)
	if err != nil {
		return nil, fmt.Errorf("invalid interval %q: %w", cfg.Interval, err)
	}
	if interval <= 0 {
		return engine.One("out", msg), nil
	}
	queue := cfg.OnLimit == "queue"
	waits, _ := engine.WaitsFor(ec)
	epoch := currentEpoch(waits, key)
	slot, admitted := claimRateSlot(ec, key, interval, queue)
	if !admitted {
		ec.Debug(msg.Payload, "rate limit exceeded; message dropped")
		return engine.One("dropped", msg), nil
	}
	if !slot.After(ec.Clock().Now()) {
		return engine.One("out", msg), nil
	}
	waits, qkey, mine, err := armExecWait(ec, slot, delayWait{Kind: delayKindQueue, Msg: storeMsg(msg), Epoch: epoch, RateKey: key})
	if err != nil {
		return nil, err
	}
	return finishQueued(ctx, ec, waits, qkey, mine, msg, epoch, key)
}

// rateRestoreKey names a bucket's restore cursor in the flow-scoped store: the
// earliest slot the next restored message may take. Arming a flow purges that
// store, so each restore pass starts without one.
const rateRestoreKey = "rate_restore"

// resumeQueued finishes a queued message after a restart. Restored messages
// arrive in deadline order and are spaced from a per-bucket cursor: a slot
// that passed while the server was down moves to the restart, and each later
// message keeps its slot unless that would leave less than an interval after
// the message before it.
func resumeQueued(ctx context.Context, ec engine.ExecContext, cfg delayConfig, waits engine.WaitStore, key string, w engine.Wait, state delayWait) (map[string][]*engine.Msg, error) {
	interval, err := parseDurationDefault(cfg.Interval, time.Second)
	if err != nil {
		return nil, fmt.Errorf("invalid interval %q: %w", cfg.Interval, err)
	}
	if interval > 0 {
		slot := w.Deadline
		if now := ec.Clock().Now(); slot.Before(now) {
			slot = now
		}
		ec.NodeStore().Update(keyedStoreKey(rateRestoreKey, state.RateKey), func(cur any, ok bool) any {
			if cursor, isTime := cur.(time.Time); ok && isTime && cursor.After(slot) {
				slot = cursor
			}
			return slot.Add(interval)
		})
		// A message arriving after the restore must queue behind every
		// restored one.
		ec.DurableStore().Update(keyedStoreKey(rateStoreKey, state.RateKey), func(cur any, ok bool) any {
			if next, isTime := storedTime(cur); ok && isTime && !next.Before(slot.Add(interval)) {
				return cur
			}
			return slot.Add(interval).Format(time.RFC3339Nano)
		})
		if !slot.Equal(w.Deadline) {
			moved, _ := waits.Update(key, func(cur engine.Wait, ok bool) *engine.Wait {
				if !ok || cur.Token != w.Token {
					return nil
				}
				cur.Deadline = slot
				return &cur
			})
			if moved.Token == w.Token {
				w = moved
			}
		}
	}
	return finishQueued(ctx, ec, waits, key, w, state.Msg.msg(), state.Epoch, state.RateKey)
}

// finishQueued parks a queued message to its slot and emits it, or routes it
// to superseded when a reset replaced its epoch while it waited.
func finishQueued(ctx context.Context, ec engine.ExecContext, waits engine.WaitStore, key string, mine engine.Wait, msg *engine.Msg, epoch, rateKey string) (map[string][]*engine.Msg, error) {
	_, ok, err := parkClaim(ctx, ec, waits, key, mine)
	if err != nil || !ok {
		return nil, err
	}
	if cur, _ := waits.Get(keyedStoreKey(rateEpochKey, rateKey)); cur.Token != epoch {
		return engine.One("superseded", msg), nil
	}
	return engine.One("out", msg), nil
}

// debounceStoreKey names the debounce window in the wait store.
const debounceStoreKey = "debounce"

// debounceLatestKey names a window's latest message as it arrived, kept in
// the memory store for the running server.
func debounceLatestKey(token string) string { return "debounce_latest:" + token }

// debounceFinalKey names the latest message of a reset window, kept in the
// memory store for the owner that holds token.
func debounceFinalKey(token string) string { return "debounce_final:" + token }

// debounce owns a single keyed window; only its waiter may consume or clear it.
// Later arrivals replace the held message and push the deadline back, and
// emit the message they replaced on superseded.
func debounce(ctx context.Context, ec engine.ExecContext, cfg delayConfig, msg *engine.Msg, key string) (map[string][]*engine.Msg, error) {
	quiet, err := parseDurationDefault(cfg.Quiet, time.Second)
	if err != nil || quiet < 0 {
		return nil, fmt.Errorf("invalid quiet period %q", cfg.Quiet)
	}
	maximum, err := parseDurationDefault(cfg.MaxWait, 0)
	if err != nil || maximum < 0 {
		return nil, fmt.Errorf("invalid maximum wait %q", cfg.MaxWait)
	}
	waits, _ := engine.WaitsFor(ec)
	storeKey := keyedStoreKey(debounceStoreKey, key)
	now := ec.Clock().Now()
	var previous *engine.Msg
	var encodeErr error
	mine := false
	window, durable := waits.Update(storeKey, func(cur engine.Wait, ok bool) *engine.Wait {
		state := delayWait{Kind: delayKindDebounce}
		next := &engine.Wait{}
		if ok {
			if err := decodeWaitState(cur, &state); err != nil {
				encodeErr = err
				return &cur
			}
			previous = state.Msg.msg()
			if live, found := ec.NodeStore().Get(debounceLatestKey(cur.Token)); found {
				previous, _ = live.(*engine.Msg)
			}
			next = &cur
		} else {
			mine = true
			if maximum > 0 {
				state.Maximum = now.Add(maximum)
			}
		}
		state.Msg = storeMsg(msg)
		next.Deadline = now.Add(quiet)
		if !state.Maximum.IsZero() && next.Deadline.After(state.Maximum) {
			next.Deadline = state.Maximum
		}
		raw, err := json.Marshal(state)
		if err != nil {
			encodeErr = err
			if ok {
				return &cur
			}
			return nil
		}
		next.State = raw
		if !ok {
			next.Token = engine.NewWaitToken()
		}
		// The running server keeps the message itself, so a payload it
		// emits is exactly what arrived; the stored copy is for a restart.
		ec.NodeStore().Set(debounceLatestKey(next.Token), msg)
		return next
	})
	if encodeErr != nil {
		return nil, fmt.Errorf("debounce window cannot be stored: %w", encodeErr)
	}
	if !mine {
		return engine.One("superseded", previous), nil
	}
	noteNotDurable(ec, durable)
	return finishDebounce(ctx, ec, waits, storeKey, window, msg)
}

// finishDebounce waits out the window owned by mine and emits its latest
// message. A reset deletes the window, and the latest message the owner saw
// leaves through superseded.
func finishDebounce(ctx context.Context, ec engine.ExecContext, waits engine.WaitStore, storeKey string, mine engine.Wait, latest *engine.Msg) (map[string][]*engine.Msg, error) {
	seen := latest
	reached, err := awaitDeadline(ctx, ec, mine.Deadline, func() (time.Time, bool) {
		cur, ok := waits.Get(storeKey)
		if !ok || cur.Token != mine.Token {
			return time.Time{}, false
		}
		if live, found := ec.NodeStore().Get(debounceLatestKey(cur.Token)); found {
			if m, isMsg := live.(*engine.Msg); isMsg {
				seen = m
			}
		} else {
			var state delayWait
			if decodeWaitState(cur, &state) == nil && state.Msg != nil {
				seen = state.Msg.msg()
			}
		}
		if !cur.Deadline.After(ec.Clock().Now()) {
			return time.Time{}, true
		}
		return cur.Deadline, true
	})
	if err != nil {
		if !engine.Suspended(ctx) {
			releaseWait(waits, storeKey, mine.Token)
			ec.NodeStore().Delete(debounceLatestKey(mine.Token))
		}
		return nil, err
	}
	if !reached {
		if final, ok := ec.NodeStore().Get(debounceFinalKey(mine.Token)); ok {
			ec.NodeStore().Delete(debounceFinalKey(mine.Token))
			if msg, isMsg := final.(*engine.Msg); isMsg && msg != nil {
				seen = msg
			}
		}
		return engine.One("superseded", seen), nil
	}
	claimed, ok, err := waits.Claim(storeKey, mine.Token)
	if err != nil {
		return nil, fmt.Errorf("debounce window could not be claimed: %w", err)
	}
	if !ok {
		return engine.One("superseded", seen), nil
	}
	if live, found := ec.NodeStore().Get(debounceLatestKey(mine.Token)); found {
		ec.NodeStore().Delete(debounceLatestKey(mine.Token))
		if m, isMsg := live.(*engine.Msg); isMsg {
			return engine.One("out", m), nil
		}
	}
	var state delayWait
	if err := decodeWaitState(claimed, &state); err != nil {
		return nil, err
	}
	return engine.One("out", state.Msg.msg()), nil
}

// awaitDeadline refreshes ownership and the deadline before every completion check.
func awaitDeadline(ctx context.Context, ec engine.ExecContext, deadline time.Time, refresh func() (time.Time, bool)) (bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		next, ok := refresh()
		if !ok {
			return false, nil
		}
		deadline = next
		if deadline.IsZero() {
			return true, nil
		}
		remaining := deadline.Sub(ec.Clock().Now())
		if remaining <= 0 {
			continue
		}
		if err := ec.Park(ctx, remaining); err != nil {
			return false, err
		}
	}
}
