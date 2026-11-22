// Package access decides who may see and use a connection or an agent. Each
// has an owner and shares to users and groups at use, edit, or admin, and a
// manage permission reaches every one. REST handlers, MCP tools, the flow
// engine, and the plugin host all ask here, so a connection or agent answers
// the same way however it is reached.
package access

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/auth"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
)

// Levels re-exported so callers need not import models for them.
const (
	Use   = models.ResourceAccessUse
	Edit  = models.ResourceAccessEdit
	Admin = models.ResourceAccessAdmin
)

// AgentTypePrefix is the namespace every agent node type is registered under,
// agent.<agent id>.<name>.
const AgentTypePrefix = "agent."

// ConnectionRank returns the rank a user holds on a connection: admin for its
// owner and for a holder of the connections manage permission, otherwise the
// highest share held directly or through a group, and 0 for none.
func ConnectionRank(ctx context.Context, userID uuid.UUID, c *models.Connection) int {
	if userID == uuid.Nil {
		return 0
	}
	if auth.Can(ctx, userID, perm.PermConnectionsManage) {
		return models.ResourceAccessRank(Admin)
	}
	groupIDs, _ := models.GetUserGroupIDs(userID)
	rank, err := models.ConnectionGrantRank(c, userID, groupIDs)
	if err != nil {
		return 0
	}
	return rank
}

// AgentRank returns the rank a user holds on an agent, as ConnectionRank does
// for connections.
func AgentRank(ctx context.Context, userID uuid.UUID, a *models.Agent) int {
	if userID == uuid.Nil {
		return 0
	}
	if auth.Can(ctx, userID, perm.PermAgentsManage) {
		return models.ResourceAccessRank(Admin)
	}
	groupIDs, _ := models.GetUserGroupIDs(userID)
	rank, err := models.AgentGrantRank(a, userID, groupIDs)
	if err != nil {
		return 0
	}
	return rank
}

// authorize turns a rank into an answer. A user holding nothing is told the
// resource does not exist, so private IDs cannot be confirmed by probing; one
// holding too little is refused.
func authorize(rank int, level, noun string) error {
	if rank == 0 {
		return apperrors.ErrNotFound.WithMessage(noun + " not found")
	}
	if rank < models.ResourceAccessRank(level) {
		return apperrors.ErrForbidden.WithMessage("your access to this " + noun + " does not allow that")
	}
	return nil
}

// Connection loads a connection by ID and authorizes the user on it at level.
func Connection(ctx context.Context, userID uuid.UUID, id string, level string) (*models.Connection, error) {
	cid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return nil, apperrors.ErrNotFound.WithMessage("connection not found")
	}
	c, err := models.FindConnectionByID(cid)
	if err != nil {
		return nil, apperrors.ErrNotFound.WithMessage("connection not found")
	}
	if err := authorize(ConnectionRank(ctx, userID, c), level, "connection"); err != nil {
		return nil, err
	}
	return c, nil
}

// AuthorizeConnection authorizes the user on an already loaded connection.
func AuthorizeConnection(ctx context.Context, userID uuid.UUID, c *models.Connection, level string) error {
	return authorize(ConnectionRank(ctx, userID, c), level, "connection")
}

// Agent loads an agent by ID and authorizes the user on it at level.
func Agent(ctx context.Context, userID uuid.UUID, id string, level string) (*models.Agent, error) {
	aid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return nil, apperrors.ErrNotFound.WithMessage("agent not found")
	}
	a, err := models.FindAgentByID(aid)
	if err != nil {
		return nil, apperrors.ErrNotFound.WithMessage("agent not found")
	}
	if err := authorize(AgentRank(ctx, userID, a), level, "agent"); err != nil {
		return nil, err
	}
	return a, nil
}

// AuthorizeAgent authorizes the user on an already loaded agent.
func AuthorizeAgent(ctx context.Context, userID uuid.UUID, a *models.Agent, level string) error {
	return authorize(AgentRank(ctx, userID, a), level, "agent")
}

// CanUseConnection reports whether the user may use the connection with the
// given ID. A manager may use any ID, so a record pointing at a connection
// that was removed stays theirs to clean up; for anyone else an ID naming no
// connection reports false.
func CanUseConnection(ctx context.Context, userID uuid.UUID, id string) bool {
	if userID != uuid.Nil && auth.Can(ctx, userID, perm.PermConnectionsManage) {
		return true
	}
	_, err := Connection(ctx, userID, id, Use)
	return err == nil
}

// CanUseAgent reports whether the user may use the agent with the given ID,
// with the manager rule CanUseConnection follows.
func CanUseAgent(ctx context.Context, userID uuid.UUID, id string) bool {
	if userID != uuid.Nil && auth.Can(ctx, userID, perm.PermAgentsManage) {
		return true
	}
	_, err := Agent(ctx, userID, id, Use)
	return err == nil
}

// ConnectionIDs returns the set of connection IDs a user holds at least level
// on, or nil when they hold the manage permission and reach every connection.
func ConnectionIDs(ctx context.Context, userID uuid.UUID, level string) (map[uuid.UUID]bool, error) {
	if auth.Can(ctx, userID, perm.PermConnectionsManage) {
		return nil, nil
	}
	groupIDs, _ := models.GetUserGroupIDs(userID)
	ids, err := models.AccessibleConnectionIDs(userID, groupIDs, level)
	if err != nil {
		return nil, err
	}
	return toSet(ids), nil
}

// AgentIDs returns the set of agent IDs a user holds at least level on, or nil
// when they hold the manage permission and reach every agent.
func AgentIDs(ctx context.Context, userID uuid.UUID, level string) (map[uuid.UUID]bool, error) {
	if auth.Can(ctx, userID, perm.PermAgentsManage) {
		return nil, nil
	}
	groupIDs, _ := models.GetUserGroupIDs(userID)
	ids, err := models.AccessibleAgentIDs(userID, groupIDs, level)
	if err != nil {
		return nil, err
	}
	return toSet(ids), nil
}

// Allowed reports whether an ID is in a set ConnectionIDs or AgentIDs
// returned, where nil admits every ID.
func Allowed(set map[uuid.UUID]bool, id uuid.UUID) bool {
	return set == nil || set[id]
}

// AllowedString is Allowed for an ID in text form; text that is not a UUID is
// allowed only by a nil set.
func AllowedString(set map[uuid.UUID]bool, id string) bool {
	if set == nil {
		return true
	}
	parsed, err := uuid.Parse(strings.TrimSpace(id))
	return err == nil && set[parsed]
}

func toSet(ids []uuid.UUID) map[uuid.UUID]bool {
	set := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

// AgentIDFromType returns the agent ID a node type belongs to, and false for
// a node type no agent contributed.
func AgentIDFromType(nodeType string) (string, bool) {
	if !strings.HasPrefix(nodeType, AgentTypePrefix) {
		return "", false
	}
	rest := strings.TrimPrefix(nodeType, AgentTypePrefix)
	id, _, ok := strings.Cut(rest, ".")
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

// runChecks turns on the run-time connection and agent checks. The server
// enables it at startup; tests that drive the engine against fixture
// connections and agents, which have no rows to share, leave it off. Deploy,
// REST, and MCP checks do not depend on it.
var runChecks atomic.Bool

// EnableRunChecks turns on the run-time checks for the life of the process.
func EnableRunChecks() { runChecks.Store(true) }

// SetRunChecks sets the run-time checks and returns the previous setting, so
// a test that needs them on can restore the process-wide switch.
func SetRunChecks(on bool) bool { return runChecks.Swap(on) }

// runUserKey carries the user a run acts as through the context a node runs
// in.
type runUserKey struct{}

// WithRunUser returns ctx carrying the user a run acts as.
func WithRunUser(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, runUserKey{}, userID)
}

// RunUser returns the user a run acts as, and false outside a run.
func RunUser(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(runUserKey{}).(uuid.UUID)
	return id, ok
}

// CheckRunAgentType refuses running a node type contributed by an agent the
// run's user cannot use. Outside a run, or for a run with no user, every
// agent node type is refused, so a path that forgets to set the run user
// fails closed. Node types no agent contributed pass.
func CheckRunAgentType(ctx context.Context, nodeType string) error {
	agentID, ok := AgentIDFromType(nodeType)
	if !ok || !runChecks.Load() {
		return nil
	}
	userID, _ := RunUser(ctx)
	if userID == uuid.Nil {
		return fmt.Errorf("agent %s cannot be used: this run acts as no user; redeploy the flow", agentID)
	}
	if !CanUseAgent(context.Background(), userID, agentID) {
		return fmt.Errorf("agent %s not found or not shared with the user this run acts as", agentID)
	}
	return nil
}

// CheckRunConnection refuses a connection the given run user cannot use. An
// empty ID passes, since a node with no connection reports that itself.
func CheckRunConnection(userID uuid.UUID, id string) error {
	if strings.TrimSpace(id) == "" || !runChecks.Load() {
		return nil
	}
	if userID == uuid.Nil {
		return fmt.Errorf("connection %s cannot be used: this run acts as no user; redeploy the flow", id)
	}
	if !CanUseConnection(context.Background(), userID, id) {
		return fmt.Errorf("connection %s not found or not shared with the user this run acts as", id)
	}
	return nil
}

// VisibleNodeTypes drops the node types of agents the user cannot use, so the
// palette, the script function catalog, and node type reads never offer them.
func VisibleNodeTypes(ctx context.Context, userID uuid.UUID, metas []flow.NodeMeta) []flow.NodeMeta {
	allowed, err := AgentIDs(ctx, userID, Use)
	if err != nil {
		allowed = map[uuid.UUID]bool{}
	}
	if allowed == nil {
		return metas
	}
	out := make([]flow.NodeMeta, 0, len(metas))
	for _, meta := range metas {
		if agentID, ok := AgentIDFromType(meta.Type); ok && !AllowedString(allowed, agentID) {
			continue
		}
		out = append(out, meta)
	}
	return out
}

// NodeTypeVisible reports whether the user may see one node type.
func NodeTypeVisible(ctx context.Context, userID uuid.UUID, nodeType string) bool {
	agentID, ok := AgentIDFromType(nodeType)
	return !ok || CanUseAgent(ctx, userID, agentID)
}

// LimitEvents narrows an event log query to the connection, device, entity,
// and agent entries a user may see: those on resources they may use. A holder
// of a manage permission sees every entry of that kind.
func LimitEvents(ctx context.Context, userID uuid.UUID, f *events.Filter) error {
	conns, err := ConnectionIDs(ctx, userID, Use)
	if err != nil {
		return err
	}
	if conns != nil {
		f.LimitConnections = true
		ids := make([]uuid.UUID, 0, len(conns))
		for id := range conns {
			ids = append(ids, id)
			f.UsableConnections = append(f.UsableConnections, id.String())
		}
		if len(ids) > 0 {
			devices, err := models.MQTTDeviceIDsOnConnections(ids)
			if err != nil {
				return err
			}
			for _, id := range devices {
				f.UsableDevices = append(f.UsableDevices, id.String())
			}
		}
	}
	agents, err := AgentIDs(ctx, userID, Use)
	if err != nil {
		return err
	}
	if agents != nil {
		f.LimitAgents = true
		for id := range agents {
			f.UsableAgents = append(f.UsableAgents, id.String())
		}
	}
	return nil
}

// EventVisible reports whether a user may see one connection, device,
// entity, or agent entry. Entries about anything else pass; flows and folders
// are decided by their own shares.
func EventVisible(ctx context.Context, userID uuid.UUID, subjectKind, subjectID string) bool {
	switch subjectKind {
	case "connection":
		return CanUseConnection(ctx, userID, subjectID)
	case "ha_entity":
		conn, _, _ := strings.Cut(subjectID, "/")
		return CanUseConnection(ctx, userID, conn)
	case "device":
		id, err := uuid.Parse(subjectID)
		if err != nil {
			return false
		}
		device, err := models.FindMQTTDeviceByID(id)
		if err != nil {
			return auth.Can(ctx, userID, perm.PermConnectionsManage)
		}
		return CanUseConnection(ctx, userID, device.ConnectionID.String())
	case "agent":
		return CanUseAgent(ctx, userID, subjectID)
	}
	return true
}

// UseConnections refuses the first connection ID the user cannot use, for a
// record that references connections, such as a history forward or a memory
// store. Empty IDs are skipped.
func UseConnections(ctx context.Context, userID uuid.UUID, ids ...string) error {
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if _, err := Connection(ctx, userID, id, Use); err != nil {
			return apperrors.ErrValidation.WithMessage(fmt.Sprintf("connection %s not found or not shared with you", id))
		}
	}
	return nil
}

// ForwardConnectionIDs returns the connections a history forward reads from
// and writes to.
func ForwardConnectionIDs(f *models.HistoryForward) []string {
	var ids []string
	if f.ConnectionID != nil {
		ids = append(ids, f.ConnectionID.String())
	}
	for _, d := range f.Destinations.V {
		ids = append(ids, d.String())
	}
	return ids
}

// SourceConnectionID returns the connection an energy source reads through:
// the Home Assistant connection of an ha source, or a device's connection,
// and "" for a control.
func SourceConnectionID(kind, sourceID string) string {
	switch kind {
	case "ha":
		return sourceID
	case "device":
		id, err := uuid.Parse(sourceID)
		if err != nil {
			return ""
		}
		device, err := models.FindMQTTDeviceByID(id)
		if err != nil {
			return ""
		}
		return device.ConnectionID.String()
	}
	return ""
}
