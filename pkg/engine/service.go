package engine

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
)

// EnableDBErrorFlows wires the database-backed error handler runner: when a
// run fails, the flow's configured error flow is loaded from the database and
// triggered if it is deployed and enabled.
func (e *Engine) EnableDBErrorFlows() {
	e.errorFlowRunner = e.runErrorFlowFromDB
}

// EnableDBSubflows wires the database-backed shared subflow resolver used to
// flatten graphs at run creation: references execute the referenced subflow's
// deployed version.
func (e *Engine) EnableDBSubflows() {
	e.subflowResolver = ResolveSharedSubflowFromDB
}

// ResolveSharedSubflowFromDB loads a shared subflow's deployed graph from the
// database. Missing, non-subflow, or undeployed flows return a clear error so
// run creation and validation can surface it.
func ResolveSharedSubflowFromDB(id uuid.UUID) (*flow.Graph, error) {
	f, err := models.FindFlowByID(id)
	if err != nil {
		return nil, fmt.Errorf("shared subflow %s not found", id)
	}
	if !f.IsSubflow {
		return nil, fmt.Errorf("flow %q is not a subflow", f.Name)
	}
	if f.DeployedVersionID == nil {
		return nil, fmt.Errorf("shared subflow %q is not deployed", f.Name)
	}
	version, err := models.FindFlowVersionByID(*f.DeployedVersionID)
	if err != nil {
		return nil, fmt.Errorf("shared subflow %q deployed version missing", f.Name)
	}
	var g flow.Graph
	if err := json.Unmarshal([]byte(version.Graph), &g); err != nil {
		return nil, fmt.Errorf("shared subflow %q deployed graph is invalid", f.Name)
	}
	return &g, nil
}

// runErrorFlowFromDB loads the error handler flow's deployed graph and starts
// it with the failure message. The run is marked as an error handler so it
// can never trigger another handler.
func (e *Engine) runErrorFlowFromDB(flowID uuid.UUID, msg *Msg) {
	log := e.log.With(zap.String("error_flow_id", flowID.String()))

	f, err := models.FindFlowByID(flowID)
	if err != nil {
		log.Warn("error flow not found", zap.Error(err))
		return
	}
	if !f.Enabled || f.DeployedVersionID == nil {
		log.Debug("error flow not deployed and enabled; skipping")
		return
	}
	version, err := models.FindFlowVersionByID(*f.DeployedVersionID)
	if err != nil {
		log.Error("failed to load error flow deployed version", zap.Error(err))
		return
	}

	var g flow.Graph
	if err := json.Unmarshal([]byte(version.Graph), &g); err != nil {
		log.Error("failed to parse error flow graph", zap.Error(err))
		return
	}

	// Seed the error flow through its trigger node.
	triggerID := findTriggerNode(&g, e.reg)
	if triggerID == "" {
		log.Warn("error flow has no trigger node; skipping")
		return
	}

	if _, err := e.RunFlow(RunOptions{
		FlowID:         f.ID,
		FlowName:       f.Name,
		FlowSlug:       f.Slug,
		FlowVersionID:  version.ID,
		TriggerNodeID:  triggerID,
		Graph:          &g,
		InitialMsg:     msg,
		IsErrorHandler: true,
		DurableWaits:   true,
		RunAs:          RunAsFor(f, version),
	}); err != nil {
		log.Warn("failed to start error flow run", zap.Error(err))
	}
}

// findTriggerNode returns the first trigger node in a graph, or "" when the
// graph has none.
func findTriggerNode(g *flow.Graph, reg *Registry) string {
	for i := range g.Nodes {
		meta, ok := reg.MetaFor(g.Nodes[i].Type)
		if ok && meta.IsTrigger() {
			return g.Nodes[i].ID
		}
	}
	return ""
}
