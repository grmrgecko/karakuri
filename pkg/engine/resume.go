package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
)

// defaultMissedGrace is how late a restored wait may be and still count as on
// time. It covers the seconds a restart or a redeploy takes without hiding a
// real outage.
const defaultMissedGrace = 30 * time.Second

// Resume admission backoff bounds, used while a flow is at its run limit.
const (
	resumeBackoffMin = time.Second
	resumeBackoffMax = 30 * time.Second
)

// resumeOrderWait bounds how long one resume may hold up the next while it
// settles into its wait. It is wall-clock time: a resume that neither parks
// nor finishes in it is doing real work, not ordering.
const resumeOrderWait = 2 * time.Second

// MissedDeadlineFields returns the config fields every resumable node
// declares: what to do with a deadline that came due while the server was
// down, and how late still counts as on time.
func MissedDeadlineFields() []FieldSpec {
	return []FieldSpec{
		{
			ID:      "missed_deadline",
			Label:   "If the server was down at the deadline",
			Type:    flow.FieldSelect,
			Default: "deliver",
			Options: []Option{
				{Value: "deliver", Label: "Send it late"},
				{Value: "drop", Label: "Drop it"},
			},
			Help: "Pending waits survive restarts and redeploys. This decides what happens to one whose time passed while the server was stopped.",
		},
		{
			ID:          "missed_grace",
			Label:       "Late by up to",
			Type:        flow.FieldDuration,
			Default:     "30s",
			Placeholder: "30s",
			ShowIf:      &flow.ShowIf{Field: "missed_deadline", Equals: "drop"},
			Help:        "A deadline missed by less than this is sent as if on time.",
		},
	}
}

// missedPolicy is a node's missed-deadline setting.
type missedPolicy struct {
	drop  bool
	grace time.Duration
}

// readMissedPolicy decodes the missed-deadline fields from a node's config.
// An unreadable or negative grace falls back to the default, since dropping a
// wait over a typo would lose it.
func readMissedPolicy(raw json.RawMessage, log *zap.Logger) missedPolicy {
	var cfg struct {
		MissedDeadline string `json:"missed_deadline"`
		MissedGrace    string `json:"missed_grace"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &cfg)
	}
	p := missedPolicy{drop: cfg.MissedDeadline == "drop", grace: defaultMissedGrace}
	if cfg.MissedGrace != "" {
		d, err := time.ParseDuration(cfg.MissedGrace)
		if err != nil || d < 0 {
			log.Warn("invalid missed_grace; using the default",
				zap.String("missed_grace", cfg.MissedGrace))
		} else {
			p.grace = d
		}
	}
	return p
}

// restoreTarget is one deployed node that can hold resumable waits.
type restoreTarget struct {
	nodeType string
	cfg      json.RawMessage
	resumer  bool
}

// restoreWaits reconciles a flow's stored waits with its deployed graph and
// resumes those nobody is waiting on. Waits on nodes the deployment no longer
// has, or has with a different type, are dropped and recorded. Called by
// Rearm before triggers are armed, so a message arriving right after a start
// or a redeploy sees the restored waits.
func (r *Runner) restoreWaits(f *models.Flow, version *models.FlowVersion, g *flow.Graph) {
	e := r.eng
	log := r.log.With(zap.String("flow_id", f.ID.String()))
	// Each waiting node is looked up in the part of the graph its resume run
	// would execute, so a draft fragment elsewhere on the canvas that cannot
	// flatten does not strand the waits of a branch that can.
	nodeTypes := e.waits.nodeTypes(f.ID)
	targets := make(map[string]restoreTarget, len(nodeTypes))
	unrunnable := make(map[string]bool)
	for nodeID := range nodeTypes {
		exec, err := e.executionGraph(g, nodeID)
		if err != nil {
			// The resume run cannot start either; keep the node's waits for a
			// deployment that can run it.
			log.Error("cannot restore pending waits", zap.String("node_id", nodeID), zap.Error(err))
			unrunnable[nodeID] = true
			continue
		}
		for i := range exec.Nodes {
			n := &exec.Nodes[i]
			if n.ID != nodeID {
				continue
			}
			nt, ok := e.reg.Get(n.Type)
			if !ok {
				break
			}
			_, resumer := nt.(Resumer)
			targets[n.ID] = restoreTarget{nodeType: n.Type, cfg: n.Config, resumer: resumer}
			break
		}
	}

	for nodeID, nodeType := range nodeTypes {
		if unrunnable[nodeID] {
			continue
		}
		t, ok := targets[nodeID]
		var reason string
		switch {
		case !ok:
			reason = fmt.Sprintf("node %s removed by redeploy; wait dropped", nodeID)
		case nodeType != "" && t.nodeType != nodeType:
			reason = fmt.Sprintf("node %s changed type by redeploy; wait dropped", nodeID)
		case !t.resumer:
			reason = fmt.Sprintf("node %s cannot resume waits; wait dropped", nodeID)
		default:
			continue
		}
		for _, w := range e.waits.list(f.ID, false) {
			if w.NodeID == nodeID {
				e.recordWaitOutcome(f.ID, version.ID, w, reason)
			}
		}
		e.waits.removeNode(f.ID, nodeID)
		log.Info("pending waits dropped", zap.String("node_id", nodeID), zap.String("reason", reason))
	}

	var queue []PendingWaitInfo
	now := e.clock.Now()
	for _, w := range e.waits.list(f.ID, false) {
		if unrunnable[w.NodeID] || !e.waits.reserve(w.Wait.Token) {
			continue
		}
		policy := readMissedPolicy(targets[w.NodeID].cfg, log)
		late := now.Sub(w.Deadline)
		if policy.drop && late > policy.grace {
			e.waits.unreserve(w.Wait.Token)
			if e.waits.remove(f.ID, w.NodeID, w.Key) {
				reason := fmt.Sprintf("missed deadline %s by %s; dropped",
					w.Deadline.UTC().Format(time.RFC3339), late.Round(time.Second))
				e.recordWaitOutcome(f.ID, version.ID, w, reason)
			}
			continue
		}
		queue = append(queue, w)
	}
	if len(queue) == 0 {
		return
	}
	log.Info("resuming pending waits", zap.Int("waits", len(queue)))
	ctx, cancel := context.WithCancel(r.baseCtx)
	handle := &resumeQueueHandle{cancel: cancel, done: make(chan struct{})}
	r.mu.Lock()
	r.resumes[f.ID] = handle
	r.mu.Unlock()
	go func() {
		defer close(handle.done)
		r.resumeQueue(ctx, f, version, g, queue)
	}()
}

// resumeQueueHandle stops one flow's resume queue. done closes once the queue
// has returned and released every wait it did not start.
type resumeQueueHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// resumeQueue starts one run per restored wait, in deadline order. A flow at
// its run limit is retried with backoff rather than losing the wait; a wait
// that is cancelled or dropped in the meantime is skipped. Cancelling ctx, as
// a disarm or shutdown does, releases the waits not yet started.
func (r *Runner) resumeQueue(ctx context.Context, f *models.Flow, version *models.FlowVersion, g *flow.Graph, queue []PendingWaitInfo) {
	e := r.eng
	version.ApplySettings(f)
	for i, w := range queue {
		backoff := resumeBackoffMin
		for {
			if ctx.Err() != nil {
				for _, rest := range queue[i:] {
					e.waits.unreserve(rest.Wait.Token)
				}
				return
			}
			cur, ok := e.waits.get(f.ID, w.NodeID, "").peek(w.Key)
			if !ok || cur.Token != w.Wait.Token {
				e.waits.unreserve(w.Wait.Token)
				break
			}
			var from *uuid.UUID
			if id, err := uuid.Parse(cur.RunID); err == nil {
				from = &id
			}
			handle, err := e.RunFlow(RunOptions{
				FlowID:           f.ID,
				FlowName:         f.Name,
				FlowSlug:         f.Slug,
				FlowVersionID:    version.ID,
				TriggerNodeID:    w.NodeID,
				Graph:            g,
				ErrorFlowID:      f.ErrorFlowID,
				DurableWaits:     true,
				ResumeNodeID:     w.NodeID,
				ResumeKey:        w.Key,
				ResumeWait:       cur,
				ResumedFromRunID: from,
				RunAs:            RunAsFor(f, version),
			})
			if err == nil {
				// Let this wait take its place before the next one starts, so
				// waits that compete for something, such as a rate limit's
				// slots, are restored in deadline order.
				select {
				case <-handle.Done():
				case <-handle.Parked():
				case <-ctx.Done():
				case <-time.After(resumeOrderWait):
				}
				break
			}
			if err != ErrRunLimit {
				e.waits.unreserve(w.Wait.Token)
				if err == ErrStopped {
					for _, rest := range queue[i+1:] {
						e.waits.unreserve(rest.Wait.Token)
					}
					return
				}
				r.log.Error("cannot resume pending wait",
					zap.String("flow_id", f.ID.String()), zap.String("node_id", w.NodeID),
					zap.String("key", w.Key), zap.Error(err))
				break
			}
			select {
			case <-e.clock.After(backoff):
			case <-ctx.Done():
				for _, rest := range queue[i:] {
					e.waits.unreserve(rest.Wait.Token)
				}
				return
			}
			backoff *= 2
			if backoff > resumeBackoffMax {
				backoff = resumeBackoffMax
			}
		}
	}
}

// peek returns the wait stored under key without a run.
func (nw *nodeWaits) peek(key string) (Wait, bool) {
	nw.mu.Lock()
	defer nw.mu.Unlock()
	sw, ok := nw.waits[key]
	return sw.wait, ok
}

// DropFlowWaits removes every pending wait of a flow, recording each one with
// a deadline as a canceled run giving reason. Called when a flow is disabled.
func (e *Engine) DropFlowWaits(flowID uuid.UUID, versionID uint, reason string) int {
	waits := e.waits.list(flowID, false)
	for _, w := range waits {
		e.recordWaitOutcome(flowID, versionID, w, reason)
	}
	for nodeID := range e.waits.nodeTypes(flowID) {
		e.waits.removeNode(flowID, nodeID)
	}
	return len(waits)
}

// RecordWaitCancel records an operator's cancellation of one pending wait.
func (e *Engine) RecordWaitCancel(flowID uuid.UUID, versionID uint, w PendingWaitInfo, reason string) {
	e.recordWaitOutcome(flowID, versionID, w, reason)
}

// recordSkippedTrigger stores a settled canceled run for an event an armed
// trigger skipped, carrying the event's payload.
func (e *Engine) recordSkippedTrigger(flowID uuid.UUID, versionID uint, nodeID string, msg *Msg, reason string) {
	if e.history == nil {
		return
	}
	now := e.clock.Now()
	run := &models.Run{
		ID:            uuid.New(),
		FlowID:        flowID,
		FlowVersionID: versionID,
		TriggerNodeID: nodeID,
		Status:        RunStatusCanceled,
		StartedAt:     now,
	}
	if msg != nil {
		if raw, err := json.Marshal(msg.Payload); err == nil {
			run.TriggerPayload = string(raw)
		}
	}
	e.history.RunStarted(run)
	e.history.RunFinished(run.ID, RunStatusCanceled, now, reason, false)
	e.log.Info("trigger skipped an event",
		zap.String("flow_id", flowID.String()), zap.String("node_id", nodeID), zap.String("reason", reason))
}

// recordWaitOutcome stores a settled canceled run for a wait that ended
// without being delivered, so run history says what happened to it.
func (e *Engine) recordWaitOutcome(flowID uuid.UUID, versionID uint, w PendingWaitInfo, reason string) {
	if e.history == nil {
		return
	}
	now := e.clock.Now()
	run := &models.Run{
		ID:            uuid.New(),
		FlowID:        flowID,
		FlowVersionID: versionID,
		TriggerNodeID: w.NodeID,
		Status:        RunStatusCanceled,
		StartedAt:     now,
	}
	if id, err := uuid.Parse(w.RunID); err == nil {
		run.ResumedFromRunID = &id
	}
	e.history.RunStarted(run)
	e.history.RunFinished(run.ID, RunStatusCanceled, now, reason, false)
}
