package api

import (
	"context"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// callerID returns the authenticated user's ID, or uuid.Nil.
func callerID(ctx context.Context) uuid.UUID {
	if uc := middleware.GetUser(ctx); uc != nil {
		return uc.UserID
	}
	return uuid.Nil
}

// loadFlow loads a flow the caller holds at least level on. A caller holding
// nothing on it is told it does not exist.
func loadFlow(ctx context.Context, id string, level string) (*models.Flow, error) {
	uid := callerID(ctx)
	if uid == uuid.Nil {
		return nil, apperrors.ErrUnauthorized.WithMessage("unauthorized")
	}
	return flowops.LoadFor(ctx, uid, id, level)
}

// flowDetail builds a flow response carrying the caller's access level.
func flowDetail(ctx context.Context, f *models.Flow) (*flowops.FlowDetail, error) {
	detail, err := flowops.Detail(f)
	if err != nil {
		return nil, err
	}
	detail.AccessLevel = flowops.Level(ctx, callerID(ctx), f)
	flowops.MaskFolder(ctx, callerID(ctx), &detail.FlowItem)
	return detail, nil
}

// resolveDashboard looks up a dashboard by ID, or by a slug among the
// dashboards this request may view: the caller's own view, plus the one
// dashboard a render token names.
func resolveDashboard(ctx context.Context, ref string) (*models.Dashboard, error) {
	uid := callerID(ctx)
	return dashops.ResolveVisible(ref, func(d *models.Dashboard) bool {
		return dashops.CanView(ctx, uid, d) || middleware.RendersDashboard(ctx, d.ID)
	})
}
