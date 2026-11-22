package engine

import (
	"encoding/json"
	"fmt"

	"github.com/grmrgecko/karakuri/pkg/models"
)

// Topic helpers matching the WS topic conventions consumed by the frontend.
func flowRunsTopic(r *Run) string { return fmt.Sprintf("flow:%s:runs", r.flowID) }
func runTopic(r *Run) string      { return fmt.Sprintf("run:%s", r.id) }

// RunsTopic carries every flow's run_started and run_finished messages, the
// same ones each flow's own topic carries, for pages that list many flows.
// The hub filters it per message, so a socket hears only flows it may read.
const RunsTopic = "runs"

// runPayload builds the run object embedded in run_started/run_finished
// messages on the flow topic.
func runPayload(snap RunSnapshot) map[string]any {
	p := map[string]any{
		"id":              snap.ID.String(),
		"flow_id":         snap.FlowID.String(),
		"status":          snap.Status,
		"started_at":      snap.StartedAt,
		"ended_at":        snap.EndedAt,
		"error":           snap.Error,
		"trigger_node_id": snap.TriggerNodeID,
		"had_failures":    snap.HadFailures,
	}
	return p
}

// publishRunStarted announces a new run on the flow topic.
func (e *Engine) publishRunStarted(r *Run) {
	if e.pub == nil {
		return
	}
	msg := map[string]any{
		"event": "run_started",
		"run":   runPayload(r.snapshot()),
	}
	e.pub.Publish(flowRunsTopic(r), "run_started", msg)
	e.pub.Publish(RunsTopic, "run_started", msg)
}

// publishRunFinished announces run completion on both the flow topic and the
// run topic.
func (e *Engine) publishRunFinished(r *Run, snap RunSnapshot) {
	if e.pub == nil {
		return
	}
	msg := map[string]any{
		"event": "run_finished",
		"run":   runPayload(snap),
	}
	e.pub.Publish(flowRunsTopic(r), "run_finished", msg)
	e.pub.Publish(RunsTopic, "run_finished", msg)
	e.pub.Publish(runTopic(r), "run_finished", map[string]any{
		"event":  "run_finished",
		"status": snap.Status,
		"error":  snap.Error,
	})
}

// emitNodeState records one node state transition: an async history event and
// a WebSocket message on the run topic.
func (e *Engine) emitNodeState(r *Run, nodeID, state string, attempt int, handled bool, errStr string) {
	e.emitNodeStateOutputs(r, nodeID, state, attempt, handled, errStr, nil)
}

// emitNodeStateOutputs is emitNodeState for terminal node states that carry
// emitted outputs. Outputs (port → {payload, topic, meta}) are persisted in
// the history event payload only — not on the WebSocket message — so
// run-from-node can later seed downstream nodes from the last run. Oversized
// payloads are truncated by the history sink.
func (e *Engine) emitNodeStateOutputs(r *Run, nodeID, state string, attempt int, handled bool, errStr string, outputs map[string][]*Msg) {
	at := e.clock.Now()
	invocation, invoked := r.invocationFor(nodeID)

	if e.history != nil {
		payload := ""
		body := map[string]any{}
		if invoked {
			invocation.addMetadata(body)
		}
		if errStr != "" || handled {
			body["error"] = errStr
			body["handled"] = handled
		}
		if out := outputsPayload(outputs); len(out) > 0 {
			body["outputs"] = out
		}
		if len(body) > 0 {
			raw, err := json.Marshal(body)
			if err == nil {
				payload = string(raw)
			}
		}
		e.history.Event(&models.RunEvent{
			RunID:   r.id,
			NodeID:  nodeID,
			Attempt: attempt,
			Event:   "node_state",
			State:   state,
			Payload: payload,
			At:      at,
		})
	}

	if e.pub != nil {
		msg := map[string]any{
			"event":   "node_state",
			"node_id": nodeID,
			"state":   state,
			"attempt": attempt,
			"at":      at,
		}
		if invoked {
			invocation.addMetadata(msg)
		}
		if handled {
			msg["handled"] = true
		}
		if errStr != "" {
			msg["error"] = errStr
		}
		e.pub.Publish(runTopic(r), "node_state", msg)
	}
}

// outputsPayload converts emitted messages to their persisted event form:
// port ID → {payload, topic, meta}. Ports with no message (untaken branches)
// are omitted. A port that fanned out records its first message plus a count,
// since run-from-node seeds one message per port.
func outputsPayload(outputs map[string][]*Msg) map[string]any {
	if len(outputs) == 0 {
		return nil
	}
	out := make(map[string]any, len(outputs))
	for port, msgs := range outputs {
		m := firstNonNil(msgs)
		if m == nil {
			continue
		}
		entry := map[string]any{"payload": m.Payload}
		if m.Topic != "" {
			entry["topic"] = m.Topic
		}
		if len(m.Meta) > 0 {
			entry["meta"] = m.Meta
		}
		if n := countNonNil(msgs); n > 1 {
			entry["count"] = n
		}
		out[port] = entry
	}
	return out
}

// countNonNil counts the messages actually emitted on a port.
func countNonNil(msgs []*Msg) int {
	n := 0
	for _, m := range msgs {
		if m != nil {
			n++
		}
	}
	return n
}

// emitDebug records a debug emission from a node (via ExecContext.Debug).
func (e *Engine) emitDebug(r *Run, nodeID string, payload any, note string) {
	at := e.clock.Now()
	invocation, invoked := r.invocationFor(nodeID)

	if e.history != nil {
		body := ""
		fields := map[string]any{"payload": payload, "note": note}
		if invoked {
			invocation.addMetadata(fields)
		}
		raw, err := json.Marshal(fields)
		if err == nil {
			body = string(raw)
		}
		e.history.Event(&models.RunEvent{
			RunID:   r.id,
			NodeID:  nodeID,
			Event:   "debug",
			Payload: body,
			At:      at,
		})
	}

	if e.pub != nil {
		msg := map[string]any{
			"event":   "debug",
			"node_id": nodeID,
			"payload": payload,
			"note":    note,
			"at":      at,
		}
		if invoked {
			invocation.addMetadata(msg)
		}
		e.pub.Publish(runTopic(r), "debug", msg)
	}
}

// historyRunStarted persists the new running row, replacing the flow's
// previous run and events.
func (e *Engine) historyRunStarted(r *Run) {
	if e.history == nil {
		return
	}
	payload := ""
	if r.initial != nil {
		if raw, err := json.Marshal(r.initial.Payload); err == nil {
			payload = string(raw)
		}
	}
	snap := r.snapshot()
	e.history.RunStarted(&models.Run{
		ID:               r.id,
		FlowID:           r.flowID,
		FlowVersionID:    r.flowVersionID,
		TriggerNodeID:    r.triggerNodeID,
		TriggerPayload:   payload,
		Status:           snap.Status,
		StartedAt:        snap.StartedAt,
		ResumedFromRunID: snap.ResumedFrom,
	})
}

// historyRunFinished persists the run's terminal state.
func (e *Engine) historyRunFinished(r *Run, snap RunSnapshot) {
	if e.history == nil || snap.EndedAt == nil {
		return
	}
	e.history.RunFinished(r.id, snap.Status, *snap.EndedAt, snap.Error, snap.HadFailures)
}
