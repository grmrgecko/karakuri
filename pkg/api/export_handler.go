package api

import (
	"context"
	"fmt"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/swaggest/usecase"
)

// FlowExportInput identifies the flow to export.
type FlowExportInput struct {
	ID string `path:"id" format:"uuid" description:"Flow UUID."`
}

// FlowExportHandler returns the interactor that exports a flow's draft graph
// as a self-contained portable document.
func FlowExportHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowExportInput, out *apiutil.Success[*flowops.ExportDoc]) error {
		// Load flow from database.
		f, err := loadFlow(ctx, in.ID, models.FlowAccessView)
		if err != nil {
			return err
		}

		// Build the portable document.
		doc, err := flowops.Export(ctx, f, callerID(ctx))
		if err != nil {
			return err
		}
		out.Data = doc
		return nil
	})
	u.SetTitle("Export Flow")
	u.SetDescription("Exports the flow's draft graph as a portable document with shared subflows embedded and connection values blanked.")
	u.SetTags("Flows")
	return u
}

// FlowImportDocInput carries an exported flow document to import.
type FlowImportDocInput struct {
	Document flowops.ExportDoc `json:"document" required:"true" description:"Exported flow document."`
}

// FlowImportDocumentHandler returns the interactor that creates a new flow
// from an exported document. The graph becomes the new flow's draft; blanked
// connection fields are tolerated (drafts validate leniently) and must be
// configured before deploy.
func FlowImportDocumentHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowImportDocInput, out *apiutil.Success[*flowops.FlowDetail]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Create the flow with the document as its draft graph.
		f, err := flowops.Import(ctx, &in.Document, uc.UserID)
		if err != nil {
			return err
		}

		// Build response.
		detail, err := flowDetail(ctx, f)
		if err != nil {
			return err
		}
		out.Data = detail
		out.Message = fmt.Sprintf("Imported as %q", f.Name)
		return nil
	})
	u.SetTitle("Import Flow")
	u.SetDescription("Creates a new flow from an exported document; the graph becomes the draft.")
	u.SetTags("Flows")
	return u
}

// DashboardExportInput identifies the dashboard to export.
type DashboardExportInput struct {
	ID string `path:"id" description:"Dashboard UUID or slug."`
}

// DashboardExportHandler returns the interactor that exports a dashboard's
// draft layout as a portable document.
func DashboardExportHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardExportInput, out *apiutil.Success[*dashops.ExportDoc]) error {
		// Resolve the dashboard; the draft is the author's copy, so exporting
		// it needs edit rights rather than a view grant.
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}
		if !canEditDashboard(ctx, dashboard) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		// Build the portable document.
		doc, err := dashops.Export(dashboard)
		if err != nil {
			return err
		}
		out.Data = doc
		return nil
	})
	u.SetTitle("Export Dashboard")
	u.SetDescription("Exports the dashboard's draft layout as a portable document. Widgets are named by package; uploads, sharing, and public visibility are not included.")
	u.SetTags("Dashboards")
	return u
}

// DashboardImportDocInput carries an exported dashboard document to import.
type DashboardImportDocInput struct {
	Document dashops.ExportDoc `json:"document" required:"true" description:"Exported dashboard document."`
}

// DashboardImportDocumentHandler returns the interactor that creates a new
// dashboard from an exported document. The layout lands as the draft, private
// and undeployed: what viewers see is the importer's call, not the file's.
func DashboardImportDocumentHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardImportDocInput, out *apiutil.Success[*models.Dashboard]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Create the dashboard, its sections, and its placements together.
		imported, err := dashops.Import(ctx, &in.Document, uc.UserID)
		if err != nil {
			return err
		}

		out.Data = imported
		out.Message = fmt.Sprintf("Imported as %q", imported.Name)
		return nil
	})
	u.SetTitle("Import Dashboard")
	u.SetDescription("Creates a new dashboard from an exported document. The layout lands as an undeployed private draft; widget uploads are not part of the document.")
	u.SetTags("Dashboards")
	return u
}
