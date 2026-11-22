package engine

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
)

// Firing is one upcoming firing of an armed trigger node.
type Firing struct {
	NodeID    string    `json:"node_id" description:"Trigger node ID."`
	NodeType  string    `json:"node_type" description:"Trigger node type."`
	NodeLabel string    `json:"node_label" description:"The node's label, or its type's label when it has none."`
	At        time.Time `json:"at" description:"When the trigger next fires."`
}

// NextFirings returns the next firing after after of every trigger the runner
// would arm for a flow: the root nodes of its deployed graph whose type
// implements NextFirer, read with the same wired config arming uses. A flow
// that is disabled, undeployed, or a subflow has none. A node whose config
// cannot be read or has no next firing is left out and logged at debug.
func (e *Engine) NextFirings(f *models.Flow, after time.Time) ([]Firing, error) {
	if f.IsSubflow || !f.Enabled || f.DeployedVersionID == nil {
		return nil, nil
	}
	version, err := models.FindFlowVersionByID(*f.DeployedVersionID)
	if err != nil {
		return nil, fmt.Errorf("deployed version missing: %w", err)
	}
	var g flow.Graph
	if err := json.Unmarshal([]byte(version.Graph), &g); err != nil {
		return nil, fmt.Errorf("deployed graph is invalid: %w", err)
	}

	var out []Firing
	for i := range g.Nodes {
		n := &g.Nodes[i]
		nt, ok := e.reg.Get(n.Type)
		if !ok {
			continue
		}
		nf, ok := nt.(NextFirer)
		if !ok {
			continue
		}
		log := e.log.With(zap.String("flow_id", f.ID.String()), zap.String("node_id", n.ID))
		cfg, err := e.armedConfig(f, &g, n, RunAsFor(f, version))
		if err != nil {
			log.Debug("next firing skipped: config could not be read", zap.Error(err))
			continue
		}
		at, ok, err := nf.NextFire(cfg, after)
		if err != nil {
			log.Debug("next firing skipped", zap.Error(err))
			continue
		}
		if !ok {
			continue
		}
		out = append(out, Firing{NodeID: n.ID, NodeType: n.Type, NodeLabel: e.NodeLabel(n), At: at})
	}
	return out, nil
}

// NodeLabel returns the label an author gave a node, or its type's label, or
// its type name when the type is not registered.
func (e *Engine) NodeLabel(n *flow.Node) string {
	var c struct {
		Label string `json:"_label"`
	}
	if len(n.Config) > 0 && json.Unmarshal(n.Config, &c) == nil && c.Label != "" {
		return c.Label
	}
	if nt, ok := e.reg.Get(n.Type); ok {
		return nt.Meta().Label
	}
	return n.Type
}
