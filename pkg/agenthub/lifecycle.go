package agenthub

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/plugin"
)

const (
	agentConnected    = "connected"
	agentDisconnected = "disconnected"
)

// AgentEvent is one authenticated agent session changing state.
type AgentEvent struct {
	AgentID   string
	AgentName string
	State     string
	At        time.Time
}

// subscribe returns a buffered event stream and its release function.
func (h *Hub) subscribe() (<-chan AgentEvent, func()) {
	h.eventMu.Lock()
	h.nextSub++
	id := h.nextSub
	ch := make(chan AgentEvent, 16)
	h.eventSub[id] = ch
	h.eventMu.Unlock()
	return ch, func() {
		h.eventMu.Lock()
		delete(h.eventSub, id)
		h.eventMu.Unlock()
	}
}

// publish fans an event out without allowing a slow flow to stall connection
// handling. The trigger's buffer covers bursts of reconnecting agents.
func (h *Hub) publish(event AgentEvent) {
	level, message := models.EventInfo, event.AgentName+" connected"
	if event.State == agentDisconnected {
		level, message = models.EventWarning, event.AgentName+" disconnected"
	}
	events.Record(events.Entry{
		Category: models.EventAgent, Level: level,
		Subject: events.Subject{Kind: "agent", ID: event.AgentID, Name: event.AgentName},
		Message: message,
		Actor:   events.Actor{Kind: events.ActorAgent, ID: event.AgentID, Name: event.AgentName},
		At:      event.At,
	})
	h.eventMu.Lock()
	defer h.eventMu.Unlock()
	for _, ch := range h.eventSub {
		select {
		case ch <- event:
		default:
			h.log.Warn("agent lifecycle subscriber fell behind", zap.String("agent", event.AgentID))
		}
	}
}

type lifecyclePlugin struct{ hub *Hub }

// Plugin contributes the server-side agent connection trigger.
func Plugin(hub *Hub) plugin.Plugin { return lifecyclePlugin{hub: hub} }

func (lifecyclePlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{Name: "agent-lifecycle", Description: "Agent connection lifecycle triggers.", Author: "Karakuri", Builtin: true}
}

func (p lifecyclePlugin) Register(r plugin.Registrar) error {
	r.RegisterNode(&agentConnectionNode{hub: p.hub})
	return nil
}

type agentConnectionNode struct{ hub *Hub }

func (agentConnectionNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type: "agent-connection", Label: "Agent connection", Folder: flow.FolderNetwork,
		Category: flow.CategoryTrigger, Description: "Starts a run when an agent connects or disconnects",
		Icon: "las la-plug", TestWithoutParams: true,
		Outputs: []engine.PortSpec{{ID: "out", Label: "Out", Type: flow.PortObject}},
		ConfigSpec: []engine.FieldSpec{
			{ID: "agent", Label: "Agent", Type: flow.FieldString, Placeholder: "kitchen-pi",
				Help: "Exact agent name or id. Empty watches every agent."},
			{ID: "on_connect", Label: "On connect", Type: flow.FieldBoolean, Default: true},
			{ID: "on_disconnect", Label: "On disconnect", Type: flow.FieldBoolean, Default: true},
		},
	}
}

type agentConnectionConfig struct {
	Agent        string `json:"agent"`
	OnConnect    *bool  `json:"on_connect"`
	OnDisconnect *bool  `json:"on_disconnect"`
}

func (agentConnectionNode) Execute(_ context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs[engine.TriggerInput]
	if msg != nil && msg.Payload != nil {
		return engine.One("out", msg), nil
	}
	var cfg agentConnectionConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(cfg.Agent)
	if name == "" {
		name = "example-agent"
	}
	out := agentEventMsg(AgentEvent{AgentID: "00000000-0000-0000-0000-000000000000", AgentName: name, State: agentConnected, At: ec.Clock().Now()})
	if msg != nil {
		out.ID, out.Group = msg.ID, msg.Group
	}
	return engine.One("out", out), nil
}

func (n *agentConnectionNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	if n.hub == nil {
		return fmt.Errorf("agent listener is disabled")
	}
	var cfg agentConnectionConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	events, cancel := n.hub.subscribe()
	defer cancel()
	wanted := strings.TrimSpace(cfg.Agent)
	for {
		select {
		case <-ctx.Done():
			return nil
		case event := <-events:
			if wanted != "" && !strings.EqualFold(wanted, event.AgentID) && !strings.EqualFold(wanted, event.AgentName) {
				continue
			}
			if event.State == agentConnected && cfg.OnConnect != nil && !*cfg.OnConnect {
				continue
			}
			if event.State == agentDisconnected && cfg.OnDisconnect != nil && !*cfg.OnDisconnect {
				continue
			}
			emit(agentEventMsg(event))
		}
	}
}

func agentEventMsg(event AgentEvent) *engine.Msg {
	payload := map[string]any{
		"agent_id": event.AgentID, "agent_name": event.AgentName, "state": event.State,
		"at": event.At.UTC().Format(time.RFC3339Nano),
	}
	return &engine.Msg{Payload: payload, Topic: event.AgentID, Meta: map[string]any{"source": "agent-connection", "state": event.State}}
}
