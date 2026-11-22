package mcpserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// flowIDInput names one flow by UUID.
type flowIDInput struct {
	ID string `json:"id" jsonschema:"Flow UUID."`
}

type listFlowsInput struct {
	Page      int   `json:"page,omitempty" jsonschema:"Page number, starting at 1."`
	Limit     int   `json:"limit,omitempty" jsonschema:"Items per page, up to 100; defaults to 50."`
	IsSubflow *bool `json:"is_subflow,omitempty" jsonschema:"Filter by whether the flow is a reusable subflow."`
}

type createFlowInput struct {
	Name        string `json:"name" jsonschema:"Display name; the slug is derived from it and must be unique."`
	Description string `json:"description,omitempty" jsonschema:"Optional summary."`
	IsSubflow   bool   `json:"is_subflow,omitempty" jsonschema:"Whether this flow is a reusable subflow."`
}

type updateFlowInput struct {
	ID          string  `json:"id" jsonschema:"Flow UUID."`
	Name        *string `json:"name,omitempty" jsonschema:"New display name."`
	Description *string `json:"description,omitempty" jsonschema:"New summary."`
	ErrorFlowID *string `json:"error_flow_id,omitempty" jsonschema:"Error handler flow UUID; an empty string clears it."`
}

type saveFlowGraphInput struct {
	ExpectedRevision string `json:"expected_revision,omitempty" jsonschema:"Pass draft_revision from get_flow to reject a stale write. Read the latest draft before editing."`
	ID               string `json:"id" jsonschema:"Flow UUID."`
	Graph            any    `json:"graph" jsonschema:"The graph document to store as the draft, in the flow graph JSON format (see karakuri://guides/flow-authoring)."`
}

type validateFlowGraphInput struct {
	Graph     any    `json:"graph,omitempty" jsonschema:"Graph document to check. Omit to validate the current draft identified by flow_id."`
	FlowID    string `json:"flow_id,omitempty" jsonschema:"Flow the graph belongs to, so self-references are caught; omit for a graph with no flow yet."`
	IsSubflow bool   `json:"is_subflow,omitempty" jsonschema:"Whether the graph is a subflow definition."`
	Strict    bool   `json:"strict,omitempty" jsonschema:"Use the deploy-level check, which enforces required config; default is the lenient draft check."`
}

type validateResult struct {
	Valid   bool              `json:"valid"`
	Error   string            `json:"error,omitempty"`
	Details map[string]string `json:"details,omitempty"`
}

type deployFlowInput struct {
	ID      string `json:"id" jsonschema:"Flow UUID."`
	Comment string `json:"comment,omitempty" jsonschema:"Deploy comment, shown in the version history."`
}

type setFlowEnabledInput struct {
	ID      string `json:"id" jsonschema:"Flow UUID."`
	Enabled bool   `json:"enabled" jsonschema:"true arms the flow's triggers; false disarms them and cancels active runs."`
}

type restoreFlowVersionInput struct {
	ID      string `json:"id" jsonschema:"Flow UUID."`
	Version int    `json:"version,omitempty" jsonschema:"Version to restore; omit to discard all changes since the current deployment."`
}

type importFlowInput struct {
	Document any `json:"document" jsonschema:"An exported flow document, as export_flow returns."`
}

// addFlowTools registers the flow lifecycle tools.
func addFlowTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_flows",
		Description: "List the flows you own or were shared, with their deployment state (deployed version, draft changes, enabled) and your access level.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listFlowsInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsRead); err != nil {
			return nil, nil, err
		}
		page, limit := in.Page, in.Limit
		if page < 1 {
			page = 1
		}
		if limit < 1 || limit > 100 {
			limit = 50
		}
		uid := callerID(ctx)
		flows, total, err := flowops.ListFor(ctx, uid, page, limit, in.IsSubflow)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrInternal.WithError(err))
		}
		items := make([]flowops.FlowItem, 0, len(flows))
		for _, f := range flows {
			item, _ := flowops.Item(f)
			item.AccessLevel = flowops.Level(ctx, uid, f)
			flowops.MaskFolder(ctx, uid, &item)
			items = append(items, item)
		}
		return nil, map[string]any{"flows": items, "total": total}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_flow",
		Description: "Get one flow with its draft graph document.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in flowIDInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsRead); err != nil {
			return nil, nil, err
		}
		f, err := loadFlow(ctx, in.ID, models.FlowAccessView)
		if err != nil {
			return nil, nil, err
		}
		detail, err := flowDetail(ctx, f)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, detail, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_flow",
		Description: "Create a flow with an empty draft graph.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in createFlowInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsWrite); err != nil {
			return nil, nil, err
		}
		uid, err := userID(ctx)
		if err != nil {
			return nil, nil, err
		}
		f, err := flowops.Create(ctx, in.Name, in.Description, in.IsSubflow, nil, uid)
		if err != nil {
			return nil, nil, opErr(err)
		}
		detail, err := flowDetail(ctx, f)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, detail, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_flow",
		Description: "Patch a flow's name, description, or error-handler flow.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updateFlowInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsWrite); err != nil {
			return nil, nil, err
		}
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return nil, nil, err
		}
		if in.Name != nil && *in.Name == "" {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("name must not be empty"))
		}
		if err := flowops.Update(ctx, f, flowops.Patch{
			Name: in.Name, Description: in.Description, ErrorFlowID: in.ErrorFlowID, By: callerID(ctx),
		}); err != nil {
			return nil, nil, opErr(err)
		}
		detail, err := flowDetail(ctx, f)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, detail, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_flow",
		Description: "Delete a flow with its versions and dependencies. A shared subflow with dependents is refused.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in flowIDInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsWrite); err != nil {
			return nil, nil, err
		}
		f, err := loadFlow(ctx, in.ID, models.FlowAccessAdmin)
		if err != nil {
			return nil, nil, err
		}
		if err := flowops.Delete(f); err != nil {
			return nil, nil, opErr(err)
		}
		return nil, map[string]any{"deleted": true}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "save_flow_graph",
		Description: "Validate a graph leniently and store it as the flow's draft. Required config may still be empty. Returns strict validation diagnostics alongside the saved draft; invalid diagnostics do not undo the save.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in saveFlowGraphInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsWrite); err != nil {
			return nil, nil, err
		}
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return nil, nil, err
		}
		g, err := toGraph(in.Graph)
		if err != nil {
			return nil, nil, err
		}
		if err := flowops.ValidateDraft(f, g); err != nil {
			return nil, nil, opErr(err)
		}
		if err := flowops.CheckSubflowReferences(ctx, callerID(ctx), f, g); err != nil {
			return nil, nil, opErr(err)
		}
		if err := flowops.SaveDraftChecked(f, g, in.ExpectedRevision); err != nil {
			return nil, nil, opErr(err)
		}
		detail, err := flowDetail(ctx, f)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, draftValidationDetail(f, detail), nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "validate_flow_graph",
		Description: "Check a supplied graph or the current draft named by flow_id without saving. Set strict=true to check all nodes for executable configuration. Returns all validation details keyed by node or edge.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in validateFlowGraphInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsRead); err != nil {
			return nil, nil, err
		}
		graph := in.Graph
		if graph == nil {
			if in.FlowID == "" {
				return nil, nil, errors.New("provide graph or flow_id")
			}
			f, err := loadFlow(ctx, in.FlowID, models.FlowAccessView)
			if err != nil {
				return nil, nil, err
			}
			draft, err := flowops.Detail(f)
			if err != nil {
				return nil, nil, opErr(err)
			}
			graph = draft.Graph
			in.IsSubflow = f.IsSubflow
		}
		g, err := toGraph(graph)
		if err != nil {
			return nil, nil, err
		}

		selfID := uuid.New()
		if in.FlowID != "" {
			parsed, err := uuid.Parse(in.FlowID)
			if err != nil {
				return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid flow ID"))
			}
			selfID = parsed
		}
		if err := flowops.Validate(g, selfID, in.IsSubflow, in.Strict); err != nil {
			return nil, graphValidationResult(err), nil
		}
		return nil, validateResult{Valid: true}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "deploy_flow",
		Description: "Validate the draft strictly and freeze it as an immutable numbered version. An enabled flow is rearmed on the new version.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in deployFlowInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsDeploy); err != nil {
			return nil, nil, err
		}
		uid, err := userID(ctx)
		if err != nil {
			return nil, nil, err
		}
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return nil, nil, err
		}
		version, err := flowops.Deploy(ctx, f, in.Comment, uid)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, map[string]any{"version": version}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "set_flow_enabled",
		Description: "Enable a deployed flow (arming its triggers) or disable it (disarming and canceling active runs).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in setFlowEnabledInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsDeploy); err != nil {
			return nil, nil, err
		}
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return nil, nil, err
		}
		if err := flowops.SetEnabled(ctx, f, in.Enabled); err != nil {
			return nil, nil, opErr(err)
		}
		detail, err := flowDetail(ctx, f)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, detail, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_flow_versions",
		Description: "List a flow's deployed versions, newest first.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in flowIDInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsRead); err != nil {
			return nil, nil, err
		}
		f, err := loadFlow(ctx, in.ID, models.FlowAccessView)
		if err != nil {
			return nil, nil, err
		}
		versions, err := models.ListFlowVersions(f.ID)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrInternal.WithError(err))
		}
		return nil, map[string]any{"versions": versions}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "restore_flow_version",
		Description: "Copy a deployed version's graph into the editable draft. The deployed version itself is untouched.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in restoreFlowVersionInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsWrite); err != nil {
			return nil, nil, err
		}
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return nil, nil, err
		}
		if err := flowops.RestoreVersion(f, in.Version); err != nil {
			return nil, nil, opErr(err)
		}
		detail, err := flowDetail(ctx, f)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, detail, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "export_flow",
		Description: "Export a flow's draft as a self-contained portable document: shared subflows embedded, connection values blanked.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in flowIDInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsRead); err != nil {
			return nil, nil, err
		}
		f, err := loadFlow(ctx, in.ID, models.FlowAccessView)
		if err != nil {
			return nil, nil, err
		}
		doc, err := flowops.Export(ctx, f, callerID(ctx))
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, doc, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "import_flow",
		Description: "Create a new flow from an exported flow document. The graph lands as the draft; blanked connections must be configured before deploy.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in importFlowInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsWrite); err != nil {
			return nil, nil, err
		}
		uid, err := userID(ctx)
		if err != nil {
			return nil, nil, err
		}
		raw, err := docJSON(in.Document)
		if err != nil || raw == nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("document is required"))
		}
		var doc flowops.ExportDoc
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("document does not parse as an exported flow: " + err.Error()))
		}
		f, err := flowops.Import(ctx, &doc, uid)
		if err != nil {
			return nil, nil, opErr(err)
		}
		detail, err := flowDetail(ctx, f)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, detail, nil
	})
}

// graphValidationResult preserves public diagnostics without internal causes.
func graphValidationResult(err error) validateResult {
	if err == nil {
		return validateResult{Valid: true}
	}
	result := validateResult{Error: opErr(err).Error()}
	var app *apperrors.AppError
	if errors.As(err, &app) {
		result.Details = app.Details
	}
	return result
}

// draftValidationDetail reports strict problems without rejecting a saved draft.
func draftValidationDetail(f *models.Flow, detail *flowops.FlowDetail) any {
	err := flowops.Validate(detail.Graph, f.ID, f.IsSubflow, true)
	return struct {
		*flowops.FlowDetail
		Validation validateResult `json:"validation"`
	}{detail, graphValidationResult(err)}
}
