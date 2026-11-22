package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// FlowListInput holds pagination and filter parameters for listing flows.
type FlowListInput struct {
	Page      int64 `query:"page" default:"1" minimum:"1" description:"Page number."`
	Limit     int64 `query:"limit" default:"20" minimum:"1" maximum:"100" description:"Number of items per page."`
	IsSubflow *bool `query:"is_subflow" description:"Filter by subflow flag."`
}

// FlowListHandler returns the interactor that lists flows with pagination.
func FlowListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowListInput, out *apiutil.Success[[]flowops.FlowItem]) error {
		// Query the flows the caller may read.
		uid := callerID(ctx)
		flows, total, err := flowops.ListFor(ctx, uid, int(in.Page), int(in.Limit), in.IsSubflow)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Derive deployment state and the caller's level per flow.
		items := make([]flowops.FlowItem, 0, len(flows))
		for _, f := range flows {
			item, _ := flowops.Item(f)
			item.AccessLevel = flowops.Level(ctx, uid, f)
			flowops.MaskFolder(ctx, uid, &item)
			items = append(items, item)
		}

		// Build response.
		out.Data = items
		out.Meta = apiutil.MakePageMeta(in.Limit, total, in.Page)
		return nil
	})
	u.SetTitle("List Flows")
	u.SetDescription("Returns a paginated list of the flows the caller owns or was shared, with deployment state.")
	u.SetTags("Flows")
	return u
}

// FlowCreateInput holds fields for creating a new flow.
type FlowCreateInput struct {
	Name        string `json:"name" required:"true" minLength:"1" description:"Display name."`
	Description string `json:"description,omitempty" description:"Optional summary."`
	IsSubflow   bool   `json:"is_subflow,omitempty" description:"Whether this flow is a reusable subflow."`
	FolderID    *uint  `json:"folder_id,omitempty" description:"Folder to create the flow in; omit for the root."`
}

// FlowCreateHandler returns the interactor that creates a flow with an empty draft.
func FlowCreateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowCreateInput, out *apiutil.Success[*flowops.FlowDetail]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Create the flow with an empty draft.
		f, err := flowops.Create(ctx, in.Name, in.Description, in.IsSubflow, in.FolderID, uc.UserID)
		if err != nil {
			return err
		}

		// Build response.
		detail, err := flowDetail(ctx, f)
		if err != nil {
			return err
		}
		out.Data = detail
		return nil
	})
	u.SetTitle("Create Flow")
	u.SetDescription("Creates a new flow with an empty draft graph.")
	u.SetTags("Flows")
	return u
}

// FlowGetInput identifies a flow to retrieve by UUID.
type FlowGetInput struct {
	ID string `path:"id" format:"uuid" description:"Flow UUID."`
}

// FlowGetHandler returns the interactor that retrieves a flow with its draft graph.
func FlowGetHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowGetInput, out *apiutil.Success[*flowops.FlowDetail]) error {
		// Load flow from database.
		f, err := loadFlow(ctx, in.ID, models.FlowAccessView)
		if err != nil {
			return err
		}

		// Build response.
		detail, err := flowDetail(ctx, f)
		if err != nil {
			return err
		}
		out.Data = detail
		return nil
	})
	u.SetTitle("Get Flow")
	u.SetDescription("Returns a single flow with its draft graph.")
	u.SetTags("Flows")
	return u
}

// FlowUpdateInput holds the flow ID and optional fields for patching flow metadata.
type FlowUpdateInput struct {
	ID          string  `path:"id" format:"uuid" description:"Flow UUID."`
	Name        *string `json:"name,omitempty" minLength:"1" description:"New display name."`
	Description *string `json:"description,omitempty" description:"New summary."`
	ErrorFlowID *string `json:"error_flow_id,omitempty" description:"Error flow UUID; empty string clears. A new error flow must be one the caller may read."`
	OwnerID     *string `json:"owner_id,omitempty" format:"uuid" description:"Transfers the flow to this user; needs the flow manage permission."`
}

// FlowUpdateHandler returns the interactor that patches flow metadata.
func FlowUpdateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowUpdateInput, out *apiutil.Success[*flowops.FlowDetail]) error {
		// Load flow from database.
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return err
		}

		// Apply the patch.
		if err := flowops.Update(ctx, f, flowops.Patch{
			Name:        in.Name,
			Description: in.Description,
			ErrorFlowID: in.ErrorFlowID,
			OwnerID:     in.OwnerID,
			By:          callerID(ctx),
		}); err != nil {
			return err
		}

		// Build response.
		detail, err := flowDetail(ctx, f)
		if err != nil {
			return err
		}
		out.Data = detail
		return nil
	})
	u.SetTitle("Update Flow")
	u.SetDescription("Patches flow metadata fields.")
	u.SetTags("Flows")
	return u
}

// FlowMoveInput sets a flow's containing folder. The folder is always applied,
// so omitting folder_id moves the flow to the root.
type FlowMoveInput struct {
	ID       string `path:"id" format:"uuid" description:"Flow UUID."`
	FolderID *uint  `json:"folder_id,omitempty" description:"Destination folder ID; omit to move to the root."`
}

// FlowMoveHandler returns the interactor that moves a flow between folders.
func FlowMoveHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowMoveInput, out *apiutil.Success[*flowops.FlowDetail]) error {
		// Load flow from database.
		// Moving a flow changes whose folder shares reach it, so it needs
		// admin on the flow and edit on the destination.
		f, err := loadFlow(ctx, in.ID, models.FlowAccessAdmin)
		if err != nil {
			return err
		}
		if err := flowops.CheckFolderTarget(ctx, callerID(ctx), in.FolderID); err != nil {
			return err
		}

		// Persist the move. A nil folder is written explicitly, so Save is not
		// enough here: the column has to be cleared to reach the root.
		f.FolderID = in.FolderID
		if err := db.DB.Model(f).Update("folder_id", in.FolderID).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		detail, err := flowDetail(ctx, f)
		if err != nil {
			return err
		}
		out.Data = detail
		return nil
	})
	u.SetTitle("Move Flow")
	u.SetDescription("Moves a flow into a folder, or to the root when folder_id is omitted.")
	u.SetTags("Flows")
	return u
}

// FlowDeleteInput identifies a flow to remove by UUID.
type FlowDeleteInput struct {
	ID string `path:"id" format:"uuid" description:"Flow UUID."`
}

// FlowDeleteHandler returns the interactor that removes a flow and its versions.
func FlowDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowDeleteInput, out *apiutil.Success[any]) error {
		// Load flow from database.
		f, err := loadFlow(ctx, in.ID, models.FlowAccessAdmin)
		if err != nil {
			return err
		}

		// Delete the flow with its versions and dependencies.
		if err := flowops.Delete(f); err != nil {
			return err
		}

		// Build response.
		out.Message = "Flow deleted"
		return nil
	})
	u.SetTitle("Delete Flow")
	u.SetDescription("Deletes a flow along with its versions and dependencies.")
	u.SetTags("Flows")
	return u
}

// FlowGraphInput holds the flow ID and the graph document to save as the draft.
type FlowGraphInput struct {
	ExpectedRevision string     `json:"expected_revision,omitempty" description:"Draft revision read before editing; a stale revision returns a conflict."`
	ID               string     `path:"id" format:"uuid" description:"Flow UUID."`
	Graph            flow.Graph `json:"graph" required:"true" description:"Graph document to save."`
}

// FlowSaveGraphHandler returns the interactor that validates and saves the draft graph.
func FlowSaveGraphHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowGraphInput, out *apiutil.Success[*flowops.FlowDetail]) error {
		// Load flow from database.
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return err
		}

		// Validate the graph against the node registry, resolving shared
		// subflow references from the database. Draft saves are lenient about
		// required config fields; deploy enforces them.
		if err := flowops.ValidateDraft(f, &in.Graph); err != nil {
			return err
		}

		// Overwrite the draft graph and rebuild dependency rows.
		if err := flowops.CheckSubflowReferences(ctx, callerID(ctx), f, &in.Graph); err != nil {
			return err
		}
		if err := flowops.SaveDraftChecked(f, &in.Graph, in.ExpectedRevision); err != nil {
			return err
		}

		// Build response.
		detail, err := flowDetail(ctx, f)
		if err != nil {
			return err
		}
		out.Data = detail
		out.Message = "Graph saved"
		return nil
	})
	u.SetTitle("Save Flow Graph")
	u.SetDescription("Validates the graph against the node registry and overwrites the draft version.")
	u.SetTags("Flows")
	return u
}

// FlowDeployInput holds the flow ID and an optional deploy comment.
type FlowDeployInput struct {
	ID      string `path:"id" format:"uuid" description:"Flow UUID."`
	Comment string `json:"comment,omitempty" description:"Deploy comment."`
}

// FlowDeployHandler returns the interactor that freezes the draft as a deployed version.
func FlowDeployHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowDeployInput, out *apiutil.Success[*flowops.FlowDetail]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Load flow from database.
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return err
		}

		// Validate and freeze the draft as a numbered version.
		next, err := flowops.Deploy(ctx, f, in.Comment, uc.UserID)
		if err != nil {
			return err
		}

		// Build response.
		detail, err := flowDetail(ctx, f)
		if err != nil {
			return err
		}
		out.Data = detail
		out.Message = fmt.Sprintf("Deployed version %d", next)
		return nil
	})
	u.SetTitle("Deploy Flow")
	u.SetDescription("Validates the draft and freezes it as an immutable deployed version.")
	u.SetTags("Flows")
	return u
}

// FlowEnableInput identifies a flow to enable or disable by UUID.
type FlowEnableInput struct {
	ID string `path:"id" format:"uuid" description:"Flow UUID."`
}

// FlowEnableHandler returns the interactor that enables a deployed flow.
func FlowEnableHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowEnableInput, out *apiutil.Success[*flowops.FlowDetail]) error {
		// Load flow from database.
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return err
		}

		// Enable and arm the flow's triggers.
		if err := flowops.SetEnabled(ctx, f, true); err != nil {
			return err
		}

		// Build response.
		detail, err := flowDetail(ctx, f)
		if err != nil {
			return err
		}
		out.Data = detail
		out.Message = "Flow enabled"
		return nil
	})
	u.SetTitle("Enable Flow")
	u.SetDescription("Enables a flow. Requires a deployed version.")
	u.SetTags("Flows")
	return u
}

// FlowDisableHandler returns the interactor that disables a flow.
func FlowDisableHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowEnableInput, out *apiutil.Success[*flowops.FlowDetail]) error {
		// Load flow from database.
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return err
		}

		// Disable, disarm triggers, and cancel active runs.
		if err := flowops.SetEnabled(ctx, f, false); err != nil {
			return err
		}

		// Build response.
		detail, err := flowDetail(ctx, f)
		if err != nil {
			return err
		}
		out.Data = detail
		out.Message = "Flow disabled"
		return nil
	})
	u.SetTitle("Disable Flow")
	u.SetDescription("Disables a flow.")
	u.SetTags("Flows")
	return u
}

// FlowVersionsInput identifies a flow whose versions are listed.
type FlowVersionsInput struct {
	ID string `path:"id" format:"uuid" description:"Flow UUID."`
}

// FlowVersionsHandler returns the interactor that lists a flow's deployed versions.
func FlowVersionsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowVersionsInput, out *apiutil.Success[[]*models.FlowVersion]) error {
		// Load flow from database.
		f, err := loadFlow(ctx, in.ID, models.FlowAccessView)
		if err != nil {
			return err
		}

		// Query versions.
		versions, err := models.ListFlowVersions(f.ID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		out.Data = versions
		return nil
	})
	u.SetTitle("List Flow Versions")
	u.SetDescription("Returns a flow's deployed versions, newest first.")
	u.SetTags("Flows")
	return u
}

// FlowVersionGetInput identifies one numbered version of a flow.
type FlowVersionGetInput struct {
	ID      string `path:"id" format:"uuid" description:"Flow UUID."`
	Version int    `path:"version" minimum:"1" description:"Version number."`
}

// FlowVersionDetail is a flow version with its parsed graph.
type FlowVersionDetail struct {
	models.FlowVersion
	Graph *flow.Graph `json:"graph" description:"Graph document."`
}

// FlowVersionGetHandler returns the interactor that retrieves one version with its graph.
func FlowVersionGetHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowVersionGetInput, out *apiutil.Success[*FlowVersionDetail]) error {
		// Load flow from database.
		f, err := loadFlow(ctx, in.ID, models.FlowAccessView)
		if err != nil {
			return err
		}

		// Load the requested version.
		version, err := models.FindFlowVersion(f.ID, in.Version)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("version not found")
		}

		// Build response.
		g, err := flowops.ParseGraph(version.Graph)
		if err != nil {
			return err
		}
		out.Data = &FlowVersionDetail{FlowVersion: *version, Graph: g}
		return nil
	})
	u.SetTitle("Get Flow Version")
	u.SetDescription("Returns one deployed version of a flow including its graph.")
	u.SetTags("Flows")
	return u
}

// FlowVersionRestoreInput identifies one numbered version to restore into the draft.
type FlowVersionRestoreInput struct {
	ID      string `path:"id" format:"uuid" description:"Flow UUID."`
	Version int    `path:"version" minimum:"1" description:"Version number to restore."`
}

// FlowVersionRestoreHandler returns the interactor that copies a deployed
// version's graph back into the flow's editable draft.
func FlowVersionRestoreHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowVersionRestoreInput, out *apiutil.Success[*flowops.FlowDetail]) error {
		// Load flow from database.
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return err
		}

		// Overwrite the draft with the version's graph and rebuild deps.
		if err := flowops.RestoreVersion(f, in.Version); err != nil {
			return err
		}

		// Build response.
		detail, err := flowDetail(ctx, f)
		if err != nil {
			return err
		}
		out.Data = detail
		out.Message = fmt.Sprintf("Version %d restored to draft", in.Version)
		return nil
	})
	u.SetTitle("Restore Flow Version")
	u.SetDescription("Copies a deployed version's graph into the editable draft.")
	u.SetTags("Flows")
	return u
}

// RegisterFlowRoutes registers flow management endpoints.
func RegisterFlowRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	// Listing needs only an account: the per-flow levels decide what is
	// listed, and a faceplate share holds no global flow permission.
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodGet, "/flows",
			nethttp.NewHandler(FlowListHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsWrite), bearerDoc).
		Method(http.MethodPost, "/flows",
			nethttp.NewHandler(FlowCreateHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsRead), bearerDoc).
		Method(http.MethodGet, "/flows/{id}",
			nethttp.NewHandler(FlowGetHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsWrite), bearerDoc).
		Method(http.MethodPut, "/flows/{id}",
			nethttp.NewHandler(FlowUpdateHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsWrite), bearerDoc).
		Method(http.MethodPut, "/flows/{id}/folder",
			nethttp.NewHandler(FlowMoveHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsWrite), bearerDoc).
		Method(http.MethodDelete, "/flows/{id}",
			nethttp.NewHandler(FlowDeleteHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsWrite), bearerDoc).
		Method(http.MethodPut, "/flows/{id}/graph",
			nethttp.NewHandler(FlowSaveGraphHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsDeploy), bearerDoc).
		Method(http.MethodPost, "/flows/{id}/deploy",
			nethttp.NewHandler(FlowDeployHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsDeploy), bearerDoc).
		Method(http.MethodPost, "/flows/{id}/enable",
			nethttp.NewHandler(FlowEnableHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsDeploy), bearerDoc).
		Method(http.MethodPost, "/flows/{id}/disable",
			nethttp.NewHandler(FlowDisableHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsWrite), bearerDoc).
		Method(http.MethodPost, "/flows/{id}/promote_subgraph",
			nethttp.NewHandler(FlowPromoteSubgraphHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsWrite), bearerDoc).
		Method(http.MethodPost, "/flows/{id}/import_subflow",
			nethttp.NewHandler(FlowImportSubflowHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsRead), bearerDoc).
		Method(http.MethodGet, "/flows/{id}/versions",
			nethttp.NewHandler(FlowVersionsHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsRead), bearerDoc).
		Method(http.MethodGet, "/flows/{id}/versions/{version}",
			nethttp.NewHandler(FlowVersionGetHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsWrite), bearerDoc).
		Method(http.MethodPost, "/flows/{id}/versions/{version}/restore",
			nethttp.NewHandler(FlowVersionRestoreHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsRead), bearerDoc).
		Method(http.MethodGet, "/flows/{id}/export",
			nethttp.NewHandler(FlowExportHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsWrite), bearerDoc).
		Method(http.MethodPost, "/flows/import",
			nethttp.NewHandler(FlowImportDocumentHandler(), nethttp.SuccessStatus(http.StatusCreated)))
}
