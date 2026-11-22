// Package events is the event log: what happened in the house and in
// karakuri, to what, and who or what did it.
package events

import (
	"context"
	"encoding/json"
	"fmt"
)

// Actor kinds.
const (
	ActorUser      = "user"
	ActorAPIKey    = "apikey"
	ActorFlow      = "flow"
	ActorAgent     = "agent"
	ActorSystem    = "system"
	ActorAnonymous = "anonymous"
)

// Ways an action arrives.
const (
	ViaDashboard = "dashboard"
	ViaAPI       = "api"
	ViaMCP       = "mcp"
	ViaCLI       = "cli"
	ViaFlow      = "flow"
)

// Actor is who or what performed an action. It travels in the context so the
// place a change happens can record it whichever path reached it: a REST
// handler, an MCP tool, or a flow node.
type Actor struct {
	Kind        string `json:"kind"`
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Via         string `json:"via,omitempty"`
	DashboardID string `json:"dashboard_id,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	Address     string `json:"address,omitempty"`
}

// System is the server acting on its own, such as a schedule or a recovery.
func System() Actor { return Actor{Kind: ActorSystem, Name: "karakuri"} }

type actorKey struct{}

// WithActor returns ctx carrying a.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// ActorFrom returns the actor ctx carries, or the system actor.
func ActorFrom(ctx context.Context) Actor {
	if ctx != nil {
		if a, ok := ctx.Value(actorKey{}).(Actor); ok {
			return a
		}
	}
	return System()
}

// Present reports whether a was set, as opposed to the zero value.
func (a Actor) Present() bool { return a.Kind != "" }

// FormatValue renders a value for a message: on and off for booleans, text
// as is, and anything else as compact JSON, cut to keep messages short.
func FormatValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "nothing"
	case bool:
		if t {
			return "on"
		}
		return "off"
	case string:
		return clip(t)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return clip(fmt.Sprint(v))
	}
	return clip(string(raw))
}

func clip(s string) string {
	r := []rune(s)
	if len(r) > 120 {
		return string(r[:119]) + "…"
	}
	return s
}

type slotKey struct{}

// WithActorSlot returns ctx with an empty slot that WithActor fills further
// down the same request, for middleware that runs before authentication and
// needs the actor after the handler.
func WithActorSlot(ctx context.Context) (context.Context, *Actor) {
	slot := &Actor{}
	return context.WithValue(ctx, slotKey{}, slot), slot
}

// FillSlot records a in the slot ctx carries, if any.
func FillSlot(ctx context.Context, a Actor) {
	if slot, ok := ctx.Value(slotKey{}).(*Actor); ok {
		*slot = a
	}
}
