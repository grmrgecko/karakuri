package dashscope

import (
	"context"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// For resolves the scope a caller holds through one dashboard. It is the whole
// authorization decision in one place: the dashboard has to exist, the caller
// has to be allowed to view it, and what comes back is only what that
// dashboard's layout exposes.
//
// userID is uuid.Nil for an anonymous caller.
func For(dashboardRef string, userID uuid.UUID) *Scope {
	return resolve(dashboardRef, userID, false, false)
}

// ForRender is the same question asked by a headless render. The render token
// already established that this dashboard is the one being photographed, so it
// stands in for the view check. The job selects draft or deployed bindings. The scope
// never carries control, because a photograph presses nothing.
func ForRender(dashboardRef string, draft bool) *Scope {
	return resolve(dashboardRef, uuid.Nil, true, draft)
}

func resolve(dashboardRef string, userID uuid.UUID, rendering, draft bool) *Scope {
	if dashboardRef == "" {
		return nil
	}

	// Resolve the dashboard. Widgets are served the ID, but a hand-written
	// request naming the slug resolves the same way the view route does.
	// Viewing the dashboard is the precondition for anything it exposes, so a
	// slug resolves only among the dashboards this caller may view.
	var groupIDs []uint
	if userID != uuid.Nil {
		groupIDs, _ = models.GetUserGroupIDs(userID)
	}
	dashboard, err := models.LookupDashboardVisible(dashboardRef, func(d *models.Dashboard) bool {
		if rendering {
			return true
		}
		ok, err := models.CanAccessDashboard(d.ID, userID, groupIDs, models.DashboardAccessRead)
		return err == nil && ok
	})
	if err != nil {
		return nil
	}
	if !rendering {
		canView, err := models.CanAccessDashboard(dashboard.ID, userID, groupIDs, models.DashboardAccessRead)
		if err != nil || !canView {
			return nil
		}
	}

	var scope *Scope
	if draft {
		scope, err = draftScope(dashboard)
	} else {
		scope, err = deployedScope(dashboard)
	}
	if err != nil {
		return nil
	}

	// An editor is rendering the draft they are changing, so their own draft
	// grants them too. Nobody else is served the draft, so nobody else's grant
	// moves when it does.
	resolved := newScope()
	resolved.DashboardID = dashboard.ID.String()
	resolved.merge(scope)
	if userID != uuid.Nil {
		if canEdit, eerr := models.CanAccessDashboard(dashboard.ID, userID, groupIDs, models.DashboardAccessEdit); eerr == nil && canEdit {
			if draft, derr := draftScope(dashboard); derr == nil {
				resolved.merge(draft)
			}
		}
	}

	resolved.Control = !rendering && canOperate(dashboard, userID, groupIDs)
	return resolved
}

// CanOperate reports whether a caller may operate what a dashboard shows,
// the control policy the scope carries. userID is uuid.Nil for an anonymous
// caller.
func CanOperate(dashboard *models.Dashboard, userID uuid.UUID) bool {
	var groupIDs []uint
	if userID != uuid.Nil {
		groupIDs, _ = models.GetUserGroupIDs(userID)
	}
	return canOperate(dashboard, userID, groupIDs)
}

// canOperate is the control policy. A viewer the owner deliberately shared
// with at view level or above, directly or through a group, may operate what
// the dashboard shows. A read share, an anonymous visitor, or an account that
// can see it only because it is public may only if the author said the
// dashboard is for operating rather than only for watching. Callers that are
// rendering check that themselves: a photograph presses nothing, and a page
// that pressed something on its way past would change the house to take it.
func canOperate(dashboard *models.Dashboard, userID uuid.UUID, groupIDs []uint) bool {
	if dashboard.PublicControl {
		return true
	}
	if userID == uuid.Nil {
		return false
	}
	shared, err := models.HasDashboardOperateGrant(dashboard.ID, userID, groupIDs)
	return err == nil && shared
}

// FromContext resolves the scope for the dashboard a request named, if any.
// Handlers call this after their own permission check fails, so a caller who
// holds the real permission never pays for it.
func FromContext(ctx context.Context) *Scope {
	ref := middleware.GetDashboardContext(ctx)
	if ref == "" {
		return nil
	}
	var userID uuid.UUID
	if uc := middleware.GetUser(ctx); uc != nil {
		userID = uc.UserID
	}
	// A render carries the same header every widget does; what it does not
	// carry is an account, so its own grant answers the view check.
	if rc := middleware.GetRenderContext(ctx); rc != nil && userID == uuid.Nil {
		if d, err := models.LookupDashboard(ref); err == nil && d.ID == rc.DashboardID {
			return ForRender(ref, rc.Draft)
		}
		return nil
	}
	return For(ref, userID)
}

// Allows is the short form handlers use: does the dashboard this request names
// expose this capability?
func Allows(ctx context.Context, c Capability) bool {
	return FromContext(ctx).Allows(c)
}

// AllowsTopic answers the same question for a WebSocket subscription. The
// client's own user ID is passed rather than read from a context, because a
// socket authenticates once and subscribes many times.
func AllowsTopic(userID, dashboardRef, topic string) bool {
	var uid uuid.UUID
	if userID != "" {
		parsed, err := uuid.Parse(userID)
		if err != nil {
			return false
		}
		uid = parsed
	}
	return For(dashboardRef, uid).AllowsTopic(topic)
}

// AllowsRenderTopic answers the subscription question for a headless render's
// socket. It authenticated with a render token rather than a session, so the
// dashboard it named is the one its token granted, and what it may watch is
// what the job's selected draft or deployed layout binds to.
func AllowsRenderTopic(dashboardRef, topic string, draft bool) bool {
	return ForRender(dashboardRef, draft).AllowsTopic(topic)
}
