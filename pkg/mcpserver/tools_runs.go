package mcpserver

import (
	"context"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"

	"github.com/google/uuid"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type runFlowTestInput struct {
	ID            string `json:"id" jsonschema:"Flow UUID."`
	TriggerNodeID string `json:"trigger_node_id,omitempty" jsonschema:"Node to start from; required when the graph has more than one trigger or value source."`
	StartNodeID   string `json:"start_node_id,omitempty" jsonschema:"Run only the subgraph reachable from this node, seeding its inputs from the last persisted run."`
	Payload       any    `json:"payload,omitempty" jsonschema:"Overrides the seed message payload for this run."`
	Topic         string `json:"topic,omitempty" jsonschema:"Overrides the seed message topic for this run."`
}

type getFlowRunInput struct {
	RunID         string `json:"run_id,omitempty" jsonschema:"Exact run UUID returned by run_flow_test; omit for the latest run."`
	NodeID        string `json:"node_id,omitempty" jsonschema:"Return states and events for only this node."`
	AfterEventID  uint   `json:"after_event_id,omitempty" jsonschema:"Return events after this event ID."`
	Limit         int    `json:"limit,omitempty" jsonschema:"Maximum events to return, default 100, maximum 500."`
	ID            string `json:"id" jsonschema:"Flow UUID."`
	IncludeEvents bool   `json:"include_events,omitempty" jsonschema:"Also return the raw run events; node_states already answers what failed, and events are large."`
}

type cancelRunInput struct {
	RunID string `json:"run_id" jsonschema:"Run UUID, as run_flow_test returned."`
}

// addRunTools registers the run execution and inspection tools.
func addRunTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "list_flow_runs", Description: "List retained executions of one flow, newest first. Filter by status or node failures to diagnose intermittent problems."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			ID          string `json:"id" jsonschema:"Flow UUID."`
			Status      string `json:"status,omitempty" jsonschema:"Optional status: running, succeeded, failed, canceled."`
			HadFailures *bool  `json:"had_failures,omitempty" jsonschema:"Filter runs that encountered node failures, including handled failures."`
			Limit       int    `json:"limit,omitempty" jsonschema:"Page size, default 20, maximum 100."`
			Offset      int    `json:"offset,omitempty" jsonschema:"Number of runs to skip."`
		}) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermRunsRead); err != nil {
				return nil, nil, err
			}
			f, err := loadFlow(ctx, in.ID, models.FlowAccessView)
			if err != nil {
				return nil, nil, err
			}
			query := db.DB.Model(&models.Run{}).Where("flow_id = ?", f.ID)
			if in.Status != "" {
				query = query.Where("status = ?", in.Status)
			}
			if in.HadFailures != nil {
				query = query.Where("had_failures = ?", *in.HadFailures)
			}
			if in.Limit < 1 || in.Limit > 100 {
				in.Limit = 20
			}
			if in.Offset < 0 {
				in.Offset = 0
			}
			var total int64
			if err := query.Count(&total).Error; err != nil {
				return nil, nil, opErr(err)
			}
			runs := []*models.Run{}
			if err := query.Order("started_at DESC, id DESC").Limit(in.Limit).Offset(in.Offset).Find(&runs).Error; err != nil {
				return nil, nil, opErr(err)
			}
			return nil, map[string]any{"runs": runs, "total": total}, nil
		})
	mcp.AddTool(s, &mcp.Tool{
		Name:        "run_flow_test",
		Description: "Validate the draft strictly and start a test run of it, returning the run ID. Poll get_flow_run with the returned run_id for the result. This executes real actions, not a simulation.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in runFlowTestInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsWrite); err != nil {
			return nil, nil, err
		}
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return nil, nil, err
		}
		payload, err := rawJSON(in.Payload)
		if err != nil {
			return nil, nil, err
		}
		runID, err := flowops.TestRun(f, flowops.TestOptions{
			RunAs:         callerID(ctx),
			TriggerNodeID: in.TriggerNodeID,
			StartNodeID:   in.StartNodeID,
			Payload:       payload,
			Topic:         in.Topic,
		})
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, map[string]any{"run_id": runID.String()}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_flow_run",
		Description: "Inspect an exact run or the latest run, including per-node states and optional filtered events.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getFlowRunInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermRunsRead); err != nil {
			return nil, nil, err
		}
		f, err := loadFlow(ctx, in.ID, models.FlowAccessView)
		if err != nil {
			return nil, nil, err
		}
		var detail *flowops.RunDetail
		if in.RunID == "" {
			detail, err = flowops.LastRunDetail(f.ID)
		} else {
			runID, parseErr := uuid.Parse(in.RunID)
			if parseErr != nil {
				return nil, nil, apperrors.ErrValidation.WithMessage("invalid run ID")
			}
			detail, err = flowops.RunDetailByID(f.ID, runID)
		}
		if err != nil {
			return nil, nil, opErr(err)
		}
		if in.NodeID != "" {
			states := map[string]flowops.NodeState{}
			if state, ok := detail.NodeStates[in.NodeID]; ok {
				states[in.NodeID] = state
			}
			detail.NodeStates = states
		}
		out := map[string]any{"run": detail.Run, "node_states": detail.NodeStates}
		if in.IncludeEvents {
			limit := in.Limit
			if limit < 1 || limit > 500 {
				limit = 100
			}
			events := []*models.RunEvent{}
			for _, event := range detail.Events {
				if event.ID <= in.AfterEventID || (in.NodeID != "" && event.NodeID != in.NodeID) {
					continue
				}
				if len(events) == limit {
					out["has_more_events"] = true
					break
				}
				events = append(events, event)
			}
			out["events"] = events
			if len(events) > 0 {
				out["next_event_id"] = events[len(events)-1].ID
			}
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_active_runs",
		Description: "Snapshot the runs currently executing in memory, across the flows you may read.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermRunsRead); err != nil {
			return nil, nil, err
		}
		runs, err := flowops.ActiveRunsFor(ctx, callerID(ctx))
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, map[string]any{"runs": runs}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "cancel_run",
		Description: "Cancel an active run.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in cancelRunInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermRunsCancel); err != nil {
			return nil, nil, err
		}
		id, err := uuid.Parse(in.RunID)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid run ID"))
		}
		if err := flowops.CancelRunFor(ctx, callerID(ctx), id); err != nil {
			return nil, nil, opErr(err)
		}
		return nil, map[string]any{"canceled": true}, nil
	})
}
