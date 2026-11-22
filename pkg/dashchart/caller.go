package dashchart

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// canRead reports whether the request may be served the dashboard: the
// same rule as every dashboard read, including the render of this
// dashboard.
func canRead(ctx context.Context, d *models.Dashboard) bool {
	if middleware.RendersDashboard(ctx, d.ID) {
		return true
	}
	var userID uuid.UUID
	if uc := middleware.GetUser(ctx); uc != nil {
		userID = uc.UserID
	}
	return dashops.CanView(ctx, userID, d)
}

func canEdit(ctx context.Context, d *models.Dashboard) bool {
	uc := middleware.GetUser(ctx)
	return uc != nil && dashops.CanEdit(ctx, uc.UserID, d)
}

// servedPlacement loads a dashboard and the placement the caller is shown:
// an editor or a draft render reads the draft, everyone else the served
// layout. It returns whether the draft was used.
func servedPlacement(ctx context.Context, id, widgetID, pkg string) (*models.Dashboard, *models.DashboardWidget, bool, error) {
	var userID uuid.UUID
	if uc := middleware.GetUser(ctx); uc != nil {
		userID = uc.UserID
	}
	dashboard, err := dashops.ResolveVisible(id, func(d *models.Dashboard) bool {
		return dashops.CanView(ctx, userID, d) || middleware.RendersDashboard(ctx, d.ID)
	})
	if err != nil || !canRead(ctx, dashboard) {
		return nil, nil, false, apperrors.ErrNotFound.WithMessage("dashboard not found")
	}
	rc := middleware.GetRenderContext(ctx)
	draft := canEdit(ctx, dashboard) || (rc != nil && rc.DashboardID == dashboard.ID && rc.Draft)
	if !draft {
		if err := dashops.ApplyDeployedLayout(dashboard); err != nil {
			return nil, nil, false, apperrors.ErrInternal.WithError(err)
		}
	}
	for i := range dashboard.Widgets {
		if dashboard.Widgets[i].ID.String() == widgetID && dashboard.Widgets[i].Widget.PackageName == pkg {
			return dashboard, &dashboard.Widgets[i], draft, nil
		}
	}
	return nil, nil, false, apperrors.ErrNotFound.WithMessage("chart placement not found on this dashboard")
}

// ForCaller runs a chart placement's queries for one caller. Shared by REST
// and MCP.
func ForCaller(ctx context.Context, id, widgetID string, vars map[string]string, from, to string, width int, refs []string) (*Response, error) {
	dashboard, placement, draft, err := servedPlacement(ctx, id, widgetID, PackageName)
	if err != nil {
		return nil, err
	}
	cfg, err := ParseConfig(placement.Config)
	if err != nil {
		return nil, apperrors.ErrValidation.WithMessage(err.Error())
	}
	served, err := dashops.ServedVariables(dashboard, draft)
	if err != nil {
		return nil, err
	}
	values, err := Values(served, vars)
	if err != nil {
		return nil, apperrors.ErrValidation.WithMessage(err.Error())
	}
	resp, err := Run(ctx, cfg, values, Request{Values: vars, From: from, To: to, Width: width, Refs: refs})
	if err != nil {
		var ve *ValueError
		if errors.As(err, &ve) {
			return nil, apperrors.ErrValidation.WithMessage(ve.Message)
		}
		return nil, err
	}
	return resp, nil
}
