package homeops

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"go.uber.org/zap"
)

const (
	// DefaultUpcomingWindow is how far ahead the overview looks.
	DefaultUpcomingWindow = 12 * time.Hour
	// MaxUpcomingWindow bounds a caller-chosen window.
	MaxUpcomingWindow = 24 * time.Hour
	// maxUpcoming bounds the list so a minutely schedule cannot fill it.
	maxUpcoming = 20
)

// UpcomingItem is one thing due to happen: a trigger firing or a wait's
// deadline.
type UpcomingItem struct {
	Kind      string    `json:"kind" description:"schedule or wait."`
	At        time.Time `json:"at" description:"When the trigger fires or the wait's deadline passes."`
	FlowID    string    `json:"flow_id" description:"Flow ID."`
	FlowName  string    `json:"flow_name" description:"Flow name."`
	NodeID    string    `json:"node_id" description:"Node that fires or holds the wait."`
	NodeLabel string    `json:"node_label" description:"The node's label."`
	Key       string    `json:"key,omitempty" description:"For waits, the key the cancel route takes."`
	CanCancel bool      `json:"can_cancel,omitempty" description:"For waits, whether the caller may cancel it."`
}

// Upcoming lists the next firings of schedule triggers and the pending waits
// due within window after now, soonest first, for enabled, deployed flows the
// caller may read. Waits need the run read permission as well. A flow whose
// triggers cannot be read is logged and left out.
func Upcoming(ctx context.Context, userID uuid.UUID, now time.Time, window time.Duration) ([]UpcomingItem, error) {
	out := []UpcomingItem{}
	if !auth.Can(ctx, userID, perm.PermFlowsRead) {
		return out, nil
	}
	eng := flowops.Engine()
	if eng == nil {
		return nil, fmt.Errorf("flow engine is not running")
	}
	readable, err := flowops.ReadableFlowIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	until := now.Add(window)

	flows, err := models.ListDeployedEnabledFlows()
	if err != nil {
		return nil, err
	}
	canWaits := auth.Can(ctx, userID, perm.PermRunsRead)
	canCancel := auth.Can(ctx, userID, perm.PermRunsCancel)
	for _, f := range flows {
		if readable != nil && !readable[f.ID] {
			continue
		}
		firings, err := eng.NextFirings(f, now)
		if err != nil {
			log.Debug("upcoming firings skipped", zap.String("flow_id", f.ID.String()), zap.Error(err))
			continue
		}
		for _, fi := range firings {
			if fi.At.After(until) {
				continue
			}
			out = append(out, UpcomingItem{Kind: "schedule", At: fi.At, FlowID: f.ID.String(), FlowName: f.Name,
				NodeID: fi.NodeID, NodeLabel: fi.NodeLabel})
		}
		if !canWaits {
			continue
		}
		// A wait that only holds state has no deadline and is not upcoming.
		var labels map[string]string
		for _, w := range eng.PendingWaits(f.ID) {
			if w.Deadline.IsZero() || w.Deadline.Before(now) || w.Deadline.After(until) {
				continue
			}
			if labels == nil {
				labels = nodeLabels(eng, f)
			}
			label := labels[w.NodeID]
			if label == "" {
				label = w.NodeType
			}
			out = append(out, UpcomingItem{Kind: "wait", At: w.Deadline, FlowID: f.ID.String(), FlowName: f.Name,
				NodeID: w.NodeID, NodeLabel: label, Key: w.Key,
				CanCancel: canCancel && flowops.Holds(ctx, userID, f, models.FlowAccessEdit)})
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	if len(out) > maxUpcoming {
		out = out[:maxUpcoming]
	}
	return out, nil
}

// nodeLabels maps the nodes of a flow's deployed graph to their labels.
func nodeLabels(eng *engine.Engine, f *models.Flow) map[string]string {
	out := map[string]string{}
	if f.DeployedVersionID == nil {
		return out
	}
	v, err := models.FindFlowVersionByID(*f.DeployedVersionID)
	if err != nil {
		return out
	}
	g, err := flowops.ParseGraph(v.Graph)
	if err != nil {
		return out
	}
	for i := range g.Nodes {
		out[g.Nodes[i].ID] = eng.NodeLabel(&g.Nodes[i])
	}
	return out
}
