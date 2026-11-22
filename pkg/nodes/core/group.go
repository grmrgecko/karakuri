package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
)

// This file holds the machinery the fan-in nodes (join, batch, aggregate)
// share: collecting the messages that belong together and deciding which run
// gets to emit the combined result.
//
// Messages produced by a split carry a group frame naming the group and its
// size, so the run that delivers the last part completes the group and emits.
// Messages arriving as a plain stream — one run each, from a trigger firing
// repeatedly — have no frame, so they accumulate in a single bucket that a
// configured count or timeout closes instead.

// groupedPart is one collected message, kept with its index so the combined
// result follows the original order rather than arrival order.
type groupedPart struct {
	index   int
	payload any
	topic   string
	meta    map[string]any
	group   []engine.GroupFrame
}

// fanInResult is the outcome of collecting one message.
type fanInResult struct {
	// parts are the collected messages in original order.
	parts []groupedPart
	// complete reports whether the group reached its expected size, as opposed
	// to being released early by the timeout.
	complete bool
	// rest is the group stack with this group's frame removed, which the
	// combined message inherits so nesting works.
	rest []engine.GroupFrame
}

// groupState is a fan-in group as stored in the wait store.
type groupState struct {
	Parts []groupPartState `json:"parts"`
	Want  int              `json:"want,omitempty"`
	// Owned is set once a run is waiting out the group's timeout.
	Owned bool `json:"owned,omitempty"`
	// Topic and Rest are the waiting run's message topic and remaining
	// group stack, which a resumed timeout emits with.
	Topic string              `json:"topic,omitempty"`
	Rest  []engine.GroupFrame `json:"rest,omitempty"`
}

// groupPartState is one stored part.
type groupPartState struct {
	Index         int                 `json:"index"`
	Payload       any                 `json:"payload"`
	PayloadBinary []byte              `json:"payload_binary,omitempty"`
	Topic         string              `json:"topic,omitempty"`
	Meta          map[string]any      `json:"meta,omitempty"`
	Group         []engine.GroupFrame `json:"group,omitempty"`
}

// sortedParts returns the stored parts in index order. Ungrouped parts all
// carry index -1, so a stable sort leaves them in arrival order.
func (g *groupState) sortedParts() []groupedPart {
	out := make([]groupedPart, 0, len(g.Parts))
	for _, p := range g.Parts {
		var payload any = p.Payload
		if p.PayloadBinary != nil {
			payload = p.PayloadBinary
		}
		out = append(out, groupedPart{index: p.Index, payload: payload, topic: p.Topic, meta: p.Meta, group: p.Group})
	}
	return sortParts(out)
}

// sortParts orders parts by index. Ungrouped parts all carry index -1, so a
// stable sort leaves them in arrival order.
func sortParts(parts []groupedPart) []groupedPart {
	out := make([]groupedPart, len(parts))
	copy(out, parts)
	sort.SliceStable(out, func(i, j int) bool { return out[i].index < out[j].index })
	return out
}

// groupPartsKey names a group's parts as they arrived, kept in the memory
// store so the running server emits exactly what it collected. The stored
// copy is for a restart.
func groupPartsKey(token string) string { return "group_parts:" + token }

// groupParts returns the group's parts: the ones collected by the running
// server when it has them all, the stored ones otherwise.
func groupParts(ec engine.ExecContext, token string, g *groupState) []groupedPart {
	store := ec.NodeStore()
	defer store.Delete(groupPartsKey(token))
	if live, ok := store.Get(groupPartsKey(token)); ok {
		if parts, isParts := live.([]groupedPart); isParts && len(parts) == len(g.Parts) {
			return sortParts(parts)
		}
	}
	return g.sortedParts()
}

// fanInResumeKey carries a stored group's resume through Execute to collect.
type fanInResumeKey struct{}

// fanInResume is a stored group a resumed run waits out.
type fanInResume struct {
	key  string
	wait engine.Wait
}

// resumeFanIn continues a stored fan-in group after a restart or a redeploy
// by running the node's Execute with a stand-in message carrying the group's
// topic: collect sees the resume in ctx and waits out the stored timeout, and
// the node builds its output exactly as it would have.
func resumeFanIn(ctx context.Context, ec engine.ExecContext, key string, w engine.Wait, execute func(context.Context, engine.ExecContext, map[string]*engine.Msg) (map[string][]*engine.Msg, error)) (map[string][]*engine.Msg, error) {
	var state groupState
	if err := decodeWaitState(w, &state); err != nil {
		return nil, err
	}
	stand := &engine.Msg{Topic: state.Topic, Group: state.Rest}
	out, err := execute(context.WithValue(ctx, fanInResumeKey{}, &fanInResume{key: key, wait: w}), ec, map[string]*engine.Msg{"in": stand})
	if err == nil {
		// A redeploy may have moved the node to a mode that never reaches
		// collect; the group must not outlive the run that was its owner.
		waits, _ := engine.WaitsFor(ec)
		releaseWait(waits, key, w.Token)
	}
	return out, err
}

// collect adds a message to its group and reports whether this run is the one
// that emits the combined result. A nil result means another run will emit, or
// the group is still waiting for more messages.
//
// Groups live in the wait store, so they survive restarts and redeploys. When
// a timeout is set, the run that opens a group waits out the deadline and
// then emits whatever arrived; whichever of it and the completing run claims
// the group first is the one that emits. Without a timeout, an incomplete
// group simply waits for its remaining parts.
func collect(ctx context.Context, ec engine.ExecContext, msg *engine.Msg, count int, timeout time.Duration) (*fanInResult, error) {
	waits, _ := engine.WaitsFor(ec)
	if resume, ok := ctx.Value(fanInResumeKey{}).(*fanInResume); ok {
		return finishGroup(ctx, ec, waits, resume.key, resume.wait, msg.Group)
	}
	frame, rest, grouped := msg.PopGroup()

	key := "group:stream"
	index := -1
	want := count
	if grouped {
		key = "group:g:" + frame.ID
		index = frame.Index
		if frame.Count > 0 {
			want = frame.Count
		}
	}

	now := ec.Clock().Now()
	part := groupPartState{Index: index, Payload: msg.Payload, Topic: msg.Topic, Meta: msg.Meta, Group: rest}
	if b, isBinary := msg.Payload.([]byte); isBinary {
		part.Payload, part.PayloadBinary = nil, b
	}
	live := groupedPart{index: index, payload: msg.Payload, topic: msg.Topic, meta: msg.Meta, group: rest}

	// Add the part and decide, in one step: concurrent runs of the same flow
	// share the store, and two of them completing a group would otherwise both
	// emit.
	var encodeErr error
	iAmWaiter := false
	var mine engine.Wait
	taken, complete, err := waits.Take(key, func(cur engine.Wait, ok bool) (*engine.Wait, bool) {
		var g groupState
		next := &engine.Wait{}
		if ok {
			if err := decodeWaitState(cur, &g); err != nil {
				encodeErr = err
				return &cur, false
			}
			next = &cur
		} else {
			next.Token = engine.NewWaitToken()
			if timeout > 0 {
				next.Deadline = now.Add(timeout)
			}
		}
		if want > 0 {
			g.Want = want
		}
		g.Parts = append(g.Parts, part)
		take := g.Want > 0 && len(g.Parts) >= g.Want
		if !take && timeout > 0 && !g.Owned {
			g.Owned = true
			g.Topic = msg.Topic
			g.Rest = rest
			iAmWaiter = true
		}
		raw, err := json.Marshal(g)
		if err != nil {
			encodeErr = err
			if ok {
				return &cur, false
			}
			return nil, false
		}
		next.State = raw
		mine = *next
		ec.NodeStore().Update(groupPartsKey(next.Token), func(cur any, _ bool) any {
			parts, _ := cur.([]groupedPart)
			return append(parts, live)
		})
		return next, take
	})
	if encodeErr != nil {
		return nil, fmt.Errorf("fan-in group cannot be stored: %w", encodeErr)
	}
	if err != nil {
		return nil, fmt.Errorf("fan-in group could not be claimed: %w", err)
	}
	if complete {
		var g groupState
		if err := decodeWaitState(taken, &g); err != nil {
			return nil, err
		}
		return &fanInResult{parts: groupParts(ec, taken.Token, &g), complete: true, rest: rest}, nil
	}
	if !iAmWaiter {
		return nil, nil
	}
	return finishGroup(ctx, ec, waits, key, mine, rest)
}

// finishGroup waits out a group's timeout and emits whatever arrived, unless
// the completing run claimed it first.
func finishGroup(ctx context.Context, ec engine.ExecContext, waits engine.WaitStore, key string, mine engine.Wait, rest []engine.GroupFrame) (*fanInResult, error) {
	claimed, ok, err := parkClaim(ctx, ec, waits, key, mine)
	if err != nil || !ok {
		return nil, err
	}
	var g groupState
	if err := decodeWaitState(claimed, &g); err != nil {
		return nil, err
	}
	if len(g.Parts) == 0 {
		return nil, nil
	}
	return &fanInResult{parts: groupParts(ec, claimed.Token, &g), complete: false, rest: rest}, nil
}

// combinedMeta builds the metadata of a combined message: the first part's
// metadata plus how many parts went in and whether the group completed.
func combinedMeta(res *fanInResult) map[string]any {
	var base map[string]any
	if len(res.parts) > 0 {
		base = res.parts[0].meta
	}
	meta := nodeutil.CloneMeta(base)
	meta["parts"] = len(res.parts)
	if !res.complete {
		meta["incomplete"] = true
	}
	return meta
}

// payloads returns the collected payloads in order.
func payloads(parts []groupedPart) []any {
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		out = append(out, p.payload)
	}
	return out
}

// parseGroupCount reads a fan-in node's count field, rejecting values that
// could never complete. An empty value means "however many the group says",
// which only works for messages that carry a group frame.
func parseGroupCount(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("count %q is not a number", raw)
	}
	if n < 1 {
		return 0, fmt.Errorf("count must be at least 1, got %d", n)
	}
	return n, nil
}

// requireCompletion rejects a configuration that can never emit: an ungrouped
// stream with neither a count nor a timeout would accumulate forever.
func requireCompletion(msg *engine.Msg, count int, timeout time.Duration) error {
	if len(msg.Group) > 0 || count > 0 || timeout > 0 {
		return nil
	}
	return errors.New("messages carry no split group, so a count or a timeout is required")
}
