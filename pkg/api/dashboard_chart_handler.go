package api

import (
	"context"
	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/dashchart"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/grmrgecko/karakuri/pkg/tsdbops"
	"github.com/swaggest/usecase"
)

// DashboardChartInput names the chart placement whose data to read. It
// carries variable values and a zoom, never query text or a connection.
type DashboardChartInput struct {
	ID       string            `path:"id" description:"Dashboard UUID or slug."`
	WidgetID string            `path:"widgetId" format:"uuid" description:"Placement UUID."`
	Vars     map[string]string `json:"vars,omitempty" description:"Viewer variable values; each must be an option, or for a time range parse."`
	From     string            `json:"from,omitempty" description:"Range start override for a zoomed chart with a fixed range."`
	To       string            `json:"to,omitempty" description:"Range end override."`
	Width    int               `json:"width,omitempty" description:"Drawing width in pixels, which sets the default maximum data points."`
	Refs     []string          `json:"refs,omitempty" description:"Query reference IDs to run; empty runs every query."`
}

// DashboardChartHandler returns a chart placement's data.
func DashboardChartHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardChartInput, out *apiutil.Success[*dashchart.Response]) error {
		resp, err := dashchart.ForCaller(ctx, in.ID, in.WidgetID, in.Vars, in.From, in.To, in.Width, in.Refs)
		if err != nil {
			return err
		}
		out.Data = resp
		return nil
	})
	u.SetTitle("Get Dashboard Chart Data")
	u.SetDescription("Runs a chart placement's saved queries with the viewer's variable values and returns their series.")
	u.SetTags("Dashboards")
	return u
}

// DashboardChartPreviewInput is one unsaved chart query an editor tries.
type DashboardChartPreviewInput struct {
	ID    string            `path:"id" description:"Dashboard UUID or slug."`
	Query dashchart.Query   `json:"query" required:"true" description:"Chart query as the placement would store it."`
	Vars  map[string]string `json:"vars,omitempty" description:"Variable values to substitute."`
	From  string            `json:"from,omitempty" description:"Range start; default now-6h."`
	To    string            `json:"to,omitempty" description:"Range end; default now."`
	Width int               `json:"width,omitempty" description:"Drawing width in pixels."`
}

// DashboardChartPreviewHandler runs an unsaved query for the chart editor.
func DashboardChartPreviewHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardChartPreviewInput, out *apiutil.Success[*tsdb.Result]) error {
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil || !canEditDashboard(ctx, dashboard) {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}
		uc := middleware.GetUser(ctx)
		if uc == nil || !auth.Can(ctx, uc.UserID, perm.PermConnectionsRead) {
			return apperrors.ErrForbidden.WithMessage("previewing a query needs connection read")
		}
		if _, err := uuid.Parse(in.Query.ConnectionID); err == nil {
			if err := access.UseConnections(ctx, uc.UserID, in.Query.ConnectionID); err != nil {
				return err
			}
		}
		served, err := dashops.ServedVariables(dashboard, true)
		if err != nil {
			return err
		}
		values, err := dashchart.Values(served, in.Vars)
		if err != nil {
			return apperrors.ErrValidation.WithMessage(err.Error())
		}
		res, err := dashchart.Preview(ctx, in.Query, values, in.From, in.To, in.Width)
		if err != nil {
			return tsdbops.AppError(err)
		}
		out.Data = res
		return nil
	})
	u.SetTitle("Preview Dashboard Chart Query")
	u.SetDescription("Runs one unsaved chart query for the dashboard editor.")
	u.SetTags("Dashboards")
	return u
}
