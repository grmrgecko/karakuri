package flowops

import (
	"encoding/json"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// PendingWait is one pending wait of a flow as REST, MCP, and the editor show
// it.
type PendingWait struct {
	NodeID         string    `json:"node_id" description:"Node holding the wait."`
	NodeType       string    `json:"node_type" description:"Type of that node."`
	Key            string    `json:"key" description:"The node's key for the wait; pass it back to cancel."`
	Deadline       time.Time `json:"deadline" description:"When the wait comes due."`
	MissedDeadline string    `json:"missed_deadline" description:"What happens if the server is down at the deadline: deliver or drop."`
	RunID          string    `json:"run_id" description:"Run that armed or last restarted the wait."`
	Durable        bool      `json:"durable" description:"Whether the wait is stored and survives a restart."`
}

// ListWaits returns a flow's pending waits ordered by deadline. Waits that
// hold only state, with no deadline, are not listed.
func ListWaits(f *models.Flow) ([]PendingWait, error) {
	if err := RequireEngine(); err != nil {
		return nil, err
	}
	settings := missedSettings(f)
	out := []PendingWait{}
	for _, w := range eng.PendingWaits(f.ID) {
		missed := settings[w.NodeID]
		if missed == "" {
			missed = "deliver"
		}
		out = append(out, PendingWait{
			NodeID: w.NodeID, NodeType: w.NodeType, Key: w.Key, Deadline: w.Deadline,
			MissedDeadline: missed, RunID: w.RunID, Durable: w.Durable,
		})
	}
	return out, nil
}

// CancelWait ends one pending wait without delivering it, recording who
// cancelled it as a canceled run. A parked waiter finds its claim refused at
// the deadline and emits nothing.
func CancelWait(f *models.Flow, nodeID, key, username string) error {
	if err := RequireEngine(); err != nil {
		return err
	}
	var found *engine.PendingWaitInfo
	for _, w := range eng.PendingWaits(f.ID) {
		if w.NodeID == nodeID && w.Key == key {
			found = &w
			break
		}
	}
	if found == nil || !eng.CancelWait(f.ID, nodeID, key) {
		return apperrors.ErrNotFound.WithMessage("no pending wait with this node and key")
	}
	var versionID uint
	if f.DeployedVersionID != nil {
		versionID = *f.DeployedVersionID
	}
	eng.RecordWaitCancel(f.ID, versionID, *found, "wait cancelled by "+username)
	NotifyChanged(f.ID)
	return nil
}

// missedSettings reads each deployed node's missed_deadline setting. Waits
// are keyed by the node ID the engine runs, so the graph is flattened first:
// a node inside a subflow is listed under its placed ID, such as "sub/d".
func missedSettings(f *models.Flow) map[string]string {
	out := map[string]string{}
	if f.DeployedVersionID == nil {
		return out
	}
	version, err := models.FindFlowVersionByID(*f.DeployedVersionID)
	if err != nil {
		return out
	}
	var g flow.Graph
	if err := json.Unmarshal([]byte(version.Graph), &g); err != nil {
		return out
	}
	nodes := g.Nodes
	if flat, err := engine.FlattenGraph(&g, engine.ResolveSharedSubflowFromDB, engine.Default()); err == nil {
		nodes = flat.Nodes
	}
	for _, n := range nodes {
		var cfg struct {
			MissedDeadline string `json:"missed_deadline"`
		}
		if len(n.Config) > 0 && json.Unmarshal(n.Config, &cfg) == nil && cfg.MissedDeadline != "" {
			out[n.ID] = cfg.MissedDeadline
		}
	}
	return out
}
