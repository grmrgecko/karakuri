// Package agentops exposes enrolled host status without issuing commands.
package agentops

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/agenthub"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/models"
)

var hub *agenthub.Hub

// SetHub installs the live session source at server startup.
func SetHub(h *agenthub.Hub) { hub = h }

// Running reports whether the agent hub is serving, so callers can tell an
// offline agent from a hub that cannot say.
func Running() bool { return hub != nil }

// Connected reports whether an agent holds a live session now.
func Connected(id uuid.UUID) bool { return hub != nil && hub.IsConnected(id) }

// Item is one agent as the panel sees it: its record plus whether it is
// connected right now, which is live state the hub holds rather than the DB.
type Item struct {
	ID          string     `json:"id" description:"Agent identifier, baked into its node types."`
	Name        string     `json:"name" description:"Display name and palette folder."`
	Description string     `json:"description,omitempty" description:"Operator notes."`
	Hostname    string     `json:"hostname,omitempty" description:"Hostname reported at enrollment."`
	OS          string     `json:"os,omitempty" description:"Operating system reported by the agent."`
	Arch        string     `json:"arch,omitempty" description:"CPU architecture reported by the agent."`
	Version     string     `json:"version,omitempty" description:"Agent binary version."`
	Enabled     bool       `json:"enabled" description:"Whether the agent may connect."`
	Connected   bool       `json:"connected" description:"Whether the agent holds a live session now."`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty" description:"When the agent was last connected."`
	CertExpires *time.Time `json:"cert_expires_at,omitempty" description:"When the agent's certificate expires; it renews well before."`
	CreatedAt   time.Time  `json:"created_at" description:"When the agent enrolled."`
	// Access is the level the caller holds, filled in by DescribeFor.
	Access string `json:"access,omitempty" description:"The caller's access level: use, edit, or admin."`
}

// DescribeFor is Describe with the level the user holds on the agent.
func DescribeFor(ctx context.Context, userID uuid.UUID, a *models.Agent) Item {
	item := Describe(a)
	item.Access = models.ResourceAccessLevel(access.AgentRank(ctx, userID, a))
	return item
}

// ListFor describes the agents a user may use.
func ListFor(ctx context.Context, userID uuid.UUID) ([]Item, error) {
	allowed, err := access.AgentIDs(ctx, userID, access.Use)
	if err != nil {
		return nil, err
	}
	agents, err := models.ListAgents()
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(agents))
	for _, a := range agents {
		if access.Allowed(allowed, a.ID) {
			items = append(items, DescribeFor(ctx, userID, a))
		}
	}
	return items, nil
}

// Describe combines persisted host metadata with the live session state.
func Describe(a *models.Agent) Item {
	connected := false
	if hub != nil {
		connected = hub.IsConnected(a.ID)
	}
	return Item{
		ID:          a.ID.String(),
		Name:        a.Name,
		Description: a.Description,
		Hostname:    a.Hostname,
		OS:          a.OS,
		Arch:        a.Arch,
		Version:     a.Version,
		Enabled:     a.Enabled,
		Connected:   connected,
		LastSeenAt:  a.LastSeenAt,
		CertExpires: a.CertNotAfter,
		CreatedAt:   a.CreatedAt,
	}
}

// Capabilities returns the registered node types contributed by this host.
func Capabilities(a *models.Agent) []string {
	types := []string{}
	for _, meta := range engine.Default().List() {
		if strings.HasPrefix(meta.Type, "agent."+a.ID.String()+".") {
			types = append(types, meta.Type)
		}
	}
	sort.Strings(types)
	return types
}
