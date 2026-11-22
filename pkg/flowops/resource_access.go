package flowops

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/engine"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// CheckResourceAccess refuses a graph that names a connection or contains an
// agent node type the user cannot use, so a deploy fails with the node named
// instead of the deployed flow failing at run time. It covers local subgraphs
// and the shared subflows the graph calls, which run as the same user. The
// engine checks again at run time, which is what catches a connection that
// arrives over a value edge or a node a script calls.
func CheckResourceAccess(ctx context.Context, userID uuid.UUID, g *flow.Graph) error {
	if g == nil || eng == nil {
		return nil
	}
	exec, err := engine.FlattenGraph(g, engine.ResolveSharedSubflowFromDB, engine.Default())
	if err != nil {
		exec = g
	}
	connections := map[string]bool{}
	agents := map[string]bool{}
	for _, n := range exec.Nodes {
		label := nodeLabel(n)
		if agentID, ok := access.AgentIDFromType(n.Type); ok {
			if !agents[agentID] {
				if !access.CanUseAgent(ctx, userID, agentID) {
					return apperrors.ErrValidation.WithMessage(fmt.Sprintf("node %q runs on agent %s, which is not shared with you", label, agentID))
				}
				agents[agentID] = true
			}
		}
		nt, ok := engine.Default().Get(n.Type)
		if !ok {
			continue
		}
		for _, id := range literalConnectionIDs(nt.Meta(), n.Config) {
			if connections[id] {
				continue
			}
			if !access.CanUseConnection(ctx, userID, id) {
				return apperrors.ErrValidation.WithMessage(fmt.Sprintf("node %q uses connection %s, which is not shared with you", label, id))
			}
			connections[id] = true
		}
	}
	return nil
}

// literalConnectionIDs returns the connection IDs a node's stored config names
// in its connection-typed fields.
func literalConnectionIDs(meta flow.NodeMeta, cfg json.RawMessage) []string {
	if len(cfg) == 0 {
		return nil
	}
	var values map[string]any
	if err := json.Unmarshal(cfg, &values); err != nil {
		return nil
	}
	var ids []string
	for _, spec := range meta.ConfigSpec {
		if spec.Type != flow.FieldConnection {
			continue
		}
		if id, ok := values[spec.ID].(string); ok && id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// checkFaceplateConnections refuses faceplate values naming a connection the
// applying user cannot use. An entity control's value carries its connection,
// and the faceplate readings show that entity's state to whoever applied it.
func checkFaceplateConnections(ctx context.Context, userID uuid.UUID, values FaceplateValues) error {
	for _, controls := range values {
		for control, value := range controls {
			m, ok := value.(map[string]any)
			if !ok {
				continue
			}
			id, _ := m["connection_id"].(string)
			if id == "" {
				continue
			}
			if !access.CanUseConnection(ctx, userID, id) {
				return apperrors.ErrValidation.WithMessage(fmt.Sprintf("faceplate value %q names connection %s, which is not shared with you", control, id))
			}
		}
	}
	return nil
}
