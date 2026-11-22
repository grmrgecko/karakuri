package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	"github.com/grmrgecko/karakuri/pkg/dashscope"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
	"gorm.io/gorm"
)

// redactDashboardOwner strips the account fields off a dashboard's embedded
// owner for a caller who is not entitled to them. A public dashboard is read
// by anyone with the link, and the owner's email address, UI preferences, and
// account timestamps are no part of what publishing a dashboard shares. The ID
// and the username stay: they are what a listing names a dashboard's owner by.
func redactDashboardOwner(ctx context.Context, d *models.Dashboard) {
	uc := middleware.GetUser(ctx)
	if uc != nil && (uc.UserID == d.OwnerID || auth.Can(ctx, uc.UserID, perm.PermDashboardsRead)) {
		return
	}
	d.Owner = models.User{ID: d.Owner.ID, Username: d.Owner.Username}
	if d.Group != nil {
		d.Group.Owner = models.User{ID: d.Group.Owner.ID, Username: d.Group.Owner.Username}
	}
}

// canReadDashboard reports whether a request may be served a dashboard's
// layout at all: a public dashboard by anyone, a private one by its owner,
// the people and groups it is shared with, and a manager. The one caller with
// no account that reads a private dashboard is the headless render of that
// very dashboard, which is the whole of what a render token grants.
func canReadDashboard(ctx context.Context, d *models.Dashboard) bool {
	if middleware.RendersDashboard(ctx, d.ID) {
		return true
	}
	var userID uuid.UUID
	if uc := middleware.GetUser(ctx); uc != nil {
		userID = uc.UserID
	}
	return dashops.CanView(ctx, userID, d)
}

// canEditDashboard reports whether the request may read and change a
// dashboard's draft. Viewers are served the deployed layout instead.
func canEditDashboard(ctx context.Context, d *models.Dashboard) bool {
	uc := middleware.GetUser(ctx)
	return uc != nil && dashops.CanEdit(ctx, uc.UserID, d)
}

// canOperateDashboard reports whether the request may operate what a
// dashboard shows, so the viewer draws controls only where a press would be
// allowed. A render presses nothing. An editor holds the dashboard's own
// subsystem permissions or a share that already operates it.
func canOperateDashboard(ctx context.Context, d *models.Dashboard) bool {
	if middleware.RendersDashboard(ctx, d.ID) {
		return false
	}
	var userID uuid.UUID
	if uc := middleware.GetUser(ctx); uc != nil {
		userID = uc.UserID
	}
	return dashscope.CanOperate(d, userID) || (userID != uuid.Nil && dashops.CanEdit(ctx, userID, d))
}

// DashboardListInput holds pagination parameters for listing dashboards.
type DashboardListInput struct {
	Page  int64 `query:"page" default:"1" minimum:"1" description:"Page number for pagination."`
	Limit int64 `query:"limit" default:"20" minimum:"1" maximum:"100" description:"Number of items per page."`
}

// DashboardListHandler returns the interactor that lists accessible dashboards with pagination.
func DashboardListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardListInput, out *apiutil.Success[[]*models.Dashboard]) error {
		page := in.Page
		limit := in.Limit

		// Determine access scope.
		uc := middleware.GetUser(ctx)

		var dashboards []*models.Dashboard
		var total int64
		var err error

		if uc != nil {
			groupIDs, gErr := models.GetUserGroupIDs(uc.UserID)
			if gErr != nil {
				return apperrors.ErrInternal.WithError(gErr)
			}
			dashboards, total, err = models.ListAccessibleDashboards(uc.UserID, groupIDs, int(page), int(limit))
		} else {
			dashboards, total, err = models.ListPublicDashboards(int(page), int(limit))
		}

		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		for _, d := range dashboards {
			redactDashboardOwner(ctx, d)
		}
		out.Data = dashboards
		out.Meta = apiutil.MakePageMeta(limit, total, page)
		return nil
	})
	u.SetTitle("List Dashboards")
	u.SetDescription("Returns a paginated list of dashboards accessible to the authenticated user, or all public dashboards for anonymous requests.")
	u.SetTags("Dashboards")
	return u
}

// DashboardGetInput identifies a dashboard to retrieve by UUID or slug.
type DashboardGetInput struct {
	ID   string `path:"id" description:"Dashboard UUID or slug."`
	View string `query:"view" enum:"deployed,draft" default:"deployed" description:"Which layout to return: the deployed version, or the editable draft."`
}

// DashboardGetHandler returns the interactor that retrieves a dashboard by UUID or slug.
func DashboardGetHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardGetInput, out *apiutil.Success[*models.Dashboard]) error {
		// A slug several visible dashboards share answers AMBIGUOUS with the
		// choices; anything else unresolved is not found.
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return err
		}

		// Anonymous requests may only read public dashboards. Reported as
		// not-found so private slugs are not confirmable by probing.
		if !canReadDashboard(ctx, dashboard) {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}

		// The draft belongs to the editor; everyone else gets what was
		// deployed, so unpublished work never reaches a wall panel.
		rc := middleware.GetRenderContext(ctx)
		draftRender := rc != nil && rc.DashboardID == dashboard.ID && rc.Draft
		if in.View == "draft" && (canEditDashboard(ctx, dashboard) || draftRender) {
			dashops.AnnotateDraftState(dashboard, dashboard.Widgets, dashboard.Sections, dashboard.Pages)
		} else if err := dashops.ApplyDeployedLayout(dashboard); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		dashboard.CanOperate = canOperateDashboard(ctx, dashboard)
		redactDashboardOwner(ctx, dashboard)
		out.Data = dashboard
		return nil
	})
	u.SetTitle("Get Dashboard")
	u.SetDescription("Returns a single dashboard by UUID or slug. Defaults to the deployed layout; view=draft returns the editable draft to users who may edit it.")
	u.SetTags("Dashboards")
	return u
}

// DashboardCreateInput holds fields for creating a new dashboard.
type DashboardCreateInput struct {
	Name          string                   `json:"name" required:"true" minLength:"1" description:"Dashboard display name."`
	Slug          string                   `json:"slug" maxLength:"200" description:"URL-safe slug; auto-generated from name if omitted."`
	Description   string                   `json:"description" description:"Optional description of the dashboard."`
	IsPublic      bool                     `json:"is_public" description:"Whether the dashboard is publicly visible."`
	PublicControl bool                     `json:"public_control" description:"Whether anonymous viewers of a public dashboard may operate its controls."`
	GroupID       *uint                    `json:"group_id,omitempty" description:"Optional dashboard group ID."`
	ThemeID       *uint                    `json:"theme_id,omitempty" description:"Optional theme ID."`
	LayoutMode    string                   `json:"layout_mode" enum:"fit,scroll,regions" default:"fit" description:"Layout mode: fit scales the grid to the viewport, scroll fixes the row height and lets the grid overflow, regions stacks placements in named screen regions."`
	GridRows      int                      `json:"grid_rows" default:"12" description:"Number of grid rows; fit layout only."`
	GridCols      int                      `json:"grid_cols" default:"12" description:"Number of grid columns."`
	RowHeight     int                      `json:"row_height" default:"80" description:"Height of one grid row in pixels; scroll and region layouts."`
	Padding       int                      `json:"padding" default:"8" description:"Outer padding in pixels."`
	Gap           int                      `json:"gap" default:"8" description:"Gap between widgets in pixels."`
	WidgetPadding int                      `json:"widget_padding" description:"Inner widget padding in pixels."`
	Display       *models.DashboardDisplay `json:"display,omitempty" description:"Page rotation, dimming, burn-in, and variable settings."`
}

// DashboardCreateHandler returns the interactor that creates a new dashboard.
func DashboardCreateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardCreateInput, out *apiutil.Success[*models.Dashboard]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// A group's shares reach the dashboards in it.
		if err := dashops.CheckGroupTarget(ctx, uc.UserID, in.GroupID); err != nil {
			return err
		}

		// Validate and persist.
		dashboard, err := dashops.Create(ctx, dashops.CreateSpec{
			Name:          in.Name,
			Slug:          in.Slug,
			Description:   in.Description,
			IsPublic:      in.IsPublic,
			PublicControl: in.PublicControl,
			GroupID:       in.GroupID,
			ThemeID:       in.ThemeID,
			LayoutMode:    in.LayoutMode,
			GridRows:      in.GridRows,
			GridCols:      in.GridCols,
			RowHeight:     in.RowHeight,
			Padding:       in.Padding,
			Gap:           in.Gap,
			WidgetPadding: in.WidgetPadding,
			Display:       in.Display,
		}, uc.UserID)
		if err != nil {
			return err
		}

		// Build response.
		out.Data = dashboard
		return nil
	})
	u.SetTitle("Create Dashboard")
	u.SetDescription("Creates a new dashboard owned by the authenticated user.")
	u.SetTags("Dashboards")
	return u
}

// DashboardUpdateInput holds the dashboard ID and optional fields for patching a dashboard.
type DashboardUpdateInput struct {
	ID            string                   `path:"id" description:"Dashboard UUID or slug."`
	Name          *string                  `json:"name,omitempty" minLength:"1" description:"Dashboard display name."`
	Slug          *string                  `json:"slug,omitempty" description:"URL-safe slug."`
	Description   *string                  `json:"description,omitempty" description:"Dashboard description."`
	IsPublic      *bool                    `json:"is_public,omitempty" description:"Whether the dashboard is publicly visible."`
	PublicControl *bool                    `json:"public_control,omitempty" description:"Whether anonymous viewers of a public dashboard may operate its controls."`
	GroupID       *uint                    `json:"group_id,omitempty" description:"Dashboard group ID; set to 0 to clear."`
	ThemeID       *uint                    `json:"theme_id,omitempty" description:"Theme ID; set to 0 to clear."`
	SortOrder     *int                     `json:"sort_order,omitempty" description:"Manual sort position within a group."`
	LayoutMode    *string                  `json:"layout_mode,omitempty" enum:"fit,scroll,regions" description:"Layout mode: fit, scroll, or regions. Switching to fit merges any sections into the sectionless region."`
	GridRows      *int                     `json:"grid_rows,omitempty" description:"Number of grid rows; fit layout only."`
	GridCols      *int                     `json:"grid_cols,omitempty" description:"Number of grid columns."`
	RowHeight     *int                     `json:"row_height,omitempty" description:"Height of one grid row in pixels; scroll and region layouts."`
	Padding       *int                     `json:"padding,omitempty" description:"Outer padding in pixels."`
	Gap           *int                     `json:"gap,omitempty" description:"Gap between widgets in pixels."`
	WidgetPadding *int                     `json:"widget_padding,omitempty" description:"Inner widget padding in pixels."`
	Display       *models.DashboardDisplay `json:"display,omitempty" description:"Page rotation, dimming, burn-in, and variable settings; replaces the stored settings, and an empty object clears them."`
	// ExpectedDisplayRevision guards a display replacement against one made
	// since the caller read the dashboard.
	ExpectedDisplayRevision *int `json:"expected_display_revision,omitempty" description:"With display, the display_revision read; a later replacement makes the write fail with 409."`
}

// DashboardUpdateHandler returns the interactor that patches dashboard fields.
func DashboardUpdateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardUpdateInput, out *apiutil.Success[*models.Dashboard]) error {
		// Resolve dashboard by UUID or slug.
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}

		// Enforce ownership: a share at the needed level or the manage permission passes too.
		uc := middleware.GetUser(ctx)
		if uc == nil || !dashops.CanEdit(ctx, uc.UserID, dashboard) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}
		if in.GroupID != nil {
			if err := dashops.CheckGroupMove(ctx, uc.UserID, dashboard, in.GroupID); err != nil {
				return err
			}
		}

		// Apply the patch, including section flattening on a layout switch.
		if err := dashops.Update(ctx, dashboard, dashops.Patch{
			By:            callerID(ctx),
			Name:          in.Name,
			Slug:          in.Slug,
			Description:   in.Description,
			IsPublic:      in.IsPublic,
			PublicControl: in.PublicControl,
			GroupID:       in.GroupID,
			ThemeID:       in.ThemeID,
			SortOrder:     in.SortOrder,
			LayoutMode:    in.LayoutMode,
			GridRows:      in.GridRows,
			GridCols:      in.GridCols,
			RowHeight:     in.RowHeight,
			Padding:       in.Padding,
			Gap:           in.Gap,
			WidgetPadding: in.WidgetPadding,
			Display:       in.Display,

			ExpectedDisplayRevision: in.ExpectedDisplayRevision,
			UserID:                  uc.UserID,
		}); err != nil {
			return err
		}

		// Build response. An edit share does not read the owner's account.
		redactDashboardOwner(ctx, dashboard)
		out.Data = dashboard
		return nil
	})
	u.SetTitle("Update Dashboard")
	u.SetDescription("Updates an existing dashboard. Only the owner or users with api:dashboard:write permission may update.")
	u.SetTags("Dashboards")
	return u
}

// DashboardDeleteInput identifies a dashboard to remove by UUID or slug.
type DashboardDeleteInput struct {
	ID string `path:"id" description:"Dashboard UUID or slug."`
}

// DashboardDeleteHandler returns the interactor that removes a dashboard.
func DashboardDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardDeleteInput, out *apiutil.Success[any]) error {
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}

		// Enforce ownership: a share at the needed level or the manage permission passes too.
		uc := middleware.GetUser(ctx)
		if uc == nil || !dashops.CanManage(ctx, uc.UserID, dashboard) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		if err := dashops.Delete(dashboard); err != nil {
			return err
		}

		out.Message = "Dashboard deleted"
		return nil
	})
	u.SetTitle("Delete Dashboard")
	u.SetDescription("Deletes a dashboard. Only the owner or users with api:dashboard:delete permission may delete.")
	u.SetTags("Dashboards")
	return u
}

// DashboardListWidgetsInput identifies a dashboard whose widget placements to list.
type DashboardListWidgetsInput struct {
	ID   string `path:"id" description:"Dashboard UUID or slug."`
	View string `query:"view" enum:"deployed,draft" default:"deployed" description:"Which layout to list: the deployed version, or the editable draft."`
}

// DashboardListWidgetsHandler returns the interactor that lists widget placements on a dashboard.
func DashboardListWidgetsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardListWidgetsInput, out *apiutil.Success[[]models.DashboardWidget]) error {
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}

		// Anonymous requests may only read public dashboards.
		if !canReadDashboard(ctx, dashboard) {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}

		// Same split as the dashboard read: the draft is the editor's, and
		// every other caller sees the deployed layout.
		rc := middleware.GetRenderContext(ctx)
		draftRender := rc != nil && rc.DashboardID == dashboard.ID && rc.Draft
		if in.View == "draft" && (canEditDashboard(ctx, dashboard) || draftRender) {
			widgets, err := models.ListDashboardWidgets(dashboard.ID)
			if err != nil {
				return apperrors.ErrInternal.WithError(err)
			}
			out.Data = widgets
			return nil
		}

		// Preloaded draft rows stand in when nothing has been deployed yet.
		if err := dashops.ApplyDeployedLayout(dashboard); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		out.Data = dashboard.Widgets
		return nil
	})
	u.SetTitle("List Dashboard Widgets")
	u.SetDescription("Returns widget placements for a dashboard. Defaults to the deployed layout; view=draft returns the editable draft to users who may edit it.")
	u.SetTags("Dashboards")
	return u
}

// DashboardAddWidgetInput holds the dashboard ID and widget placement details.
type DashboardAddWidgetInput struct {
	ID        string                `path:"id" description:"Dashboard UUID or slug."`
	WidgetID  uint                  `json:"widget_id" description:"ID of the widget to place."`
	SectionID string                `json:"section_id,omitempty" description:"Section UUID to place the widget in; omit for the sectionless region."`
	X         int                   `json:"x" description:"Grid column position."`
	Y         int                   `json:"y" description:"Grid row position, relative to the top of its section."`
	Width     int                   `json:"width" description:"Widget width in grid units."`
	Height    int                   `json:"height" description:"Widget height in grid units."`
	Config    string                `json:"config" description:"JSON configuration for the widget instance."`
	PageID    string                `json:"page_id,omitempty" description:"Page UUID to show the widget on; omit for every page."`
	Region    string                `json:"region,omitempty" description:"Region name in the region layout."`
	Display   *models.WidgetDisplay `json:"display,omitempty" description:"Name, visibility conditions, animations, tile style, and status cues."`
}

// DashboardAddWidgetHandler returns the interactor that places a widget on a dashboard.
func DashboardAddWidgetHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardAddWidgetInput, out *apiutil.Success[*models.DashboardWidget]) error {
		// Resolve dashboard.
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}

		// Enforce ownership on dashboard widget modifications.
		uc := middleware.GetUser(ctx)
		if uc == nil || !dashops.CanEdit(ctx, uc.UserID, dashboard) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		// Place the widget.
		dw, err := dashops.AddWidget(ctx, dashboard, dashops.WidgetSpec{
			WidgetID:  in.WidgetID,
			SectionID: in.SectionID,
			X:         in.X,
			Y:         in.Y,
			Width:     in.Width,
			Height:    in.Height,
			Config:    in.Config,
			PageID:    in.PageID,
			Region:    in.Region,
			Display:   in.Display,
			UserID:    uc.UserID,
		})
		if err != nil {
			return err
		}

		// Build response.
		out.Data = dw
		return nil
	})
	u.SetTitle("Add Widget to Dashboard")
	u.SetDescription("Places a widget on a dashboard at the specified grid position.")
	u.SetTags("Dashboards")
	return u
}

// DashboardUpdateWidgetInput holds the dashboard and widget IDs with optional fields to patch.
type DashboardUpdateWidgetInput struct {
	ID       string `path:"id" description:"Dashboard UUID or slug."`
	WidgetID string `path:"widgetId" format:"uuid" description:"Dashboard widget UUID."`
	// SectionID moves the placement between regions; an empty string moves it
	// out of every section, and omitting it leaves the region alone.
	SectionID *string               `json:"section_id,omitempty" description:"Section UUID to move the widget into; empty string for the sectionless region."`
	X         *int                  `json:"x,omitempty" description:"Grid column position."`
	Y         *int                  `json:"y,omitempty" description:"Grid row position, relative to the top of its section."`
	Width     *int                  `json:"width,omitempty" description:"Widget width in grid units."`
	Height    *int                  `json:"height,omitempty" description:"Widget height in grid units."`
	Config    *string               `json:"config,omitempty" description:"JSON configuration for the widget instance."`
	PageID    *string               `json:"page_id,omitempty" description:"Page UUID to show the widget on; empty string for every page."`
	Region    *string               `json:"region,omitempty" description:"Region name in the region layout."`
	Display   *models.WidgetDisplay `json:"display,omitempty" description:"Name, visibility conditions, animations, tile style, and status cues; replaces the stored settings, and an empty object clears them."`
	// ExpectedConfigRevision guards a config replacement against one made
	// since the caller read the placement.
	ExpectedConfigRevision *int `json:"expected_config_revision,omitempty" description:"With config, the config_revision read; a later replacement makes the write fail with 409."`
}

// DashboardUpdateWidgetHandler returns the interactor that patches a widget placement.
func DashboardUpdateWidgetHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardUpdateWidgetInput, out *apiutil.Success[*models.DashboardWidget]) error {
		// Resolve dashboard.
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}

		// Enforce ownership on dashboard widget modifications.
		uc := middleware.GetUser(ctx)
		if uc == nil || !dashops.CanEdit(ctx, uc.UserID, dashboard) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		// Patch the placement.
		dw, err := dashops.UpdateWidget(ctx, dashboard, in.WidgetID, dashops.WidgetPatch{
			SectionID: in.SectionID,
			X:         in.X,
			Y:         in.Y,
			Width:     in.Width,
			Height:    in.Height,
			Config:    in.Config,
			PageID:    in.PageID,
			Region:    in.Region,
			Display:   in.Display,

			ExpectedConfigRevision: in.ExpectedConfigRevision,
			UserID:                 uc.UserID,
		})
		if err != nil {
			return err
		}

		// Build response.
		out.Data = dw
		return nil
	})
	u.SetTitle("Update Dashboard Widget")
	u.SetDescription("Updates the position, size, or configuration of a widget placement on a dashboard.")
	u.SetTags("Dashboards")
	return u
}

// DashboardRemoveWidgetInput identifies a widget placement to remove from a dashboard.
type DashboardRemoveWidgetInput struct {
	ID       string `path:"id" description:"Dashboard UUID or slug."`
	WidgetID string `path:"widgetId" format:"uuid" description:"Dashboard widget UUID."`
}

// DashboardRemoveWidgetHandler returns the interactor that removes a widget from a dashboard.
func DashboardRemoveWidgetHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardRemoveWidgetInput, out *apiutil.Success[any]) error {
		// Resolve dashboard.
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}

		// Enforce ownership on dashboard widget modifications.
		uc := middleware.GetUser(ctx)
		if uc == nil || !dashops.CanEdit(ctx, uc.UserID, dashboard) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		// Delete the placement.
		if err := dashops.RemoveWidget(dashboard, in.WidgetID); err != nil {
			return err
		}

		// Build response.
		out.Message = "Widget removed"
		return nil
	})
	u.SetTitle("Remove Widget from Dashboard")
	u.SetDescription("Removes a widget placement from a dashboard.")
	u.SetTags("Dashboards")
	return u
}

// canWriteDashboard reports whether the request may change a dashboard's draft:
// ownership, a share at edit level, or the manage permission. Section routes
// share it with the widget routes.
func canWriteDashboard(ctx context.Context, d *models.Dashboard) bool {
	uc := middleware.GetUser(ctx)
	return uc != nil && dashops.CanEdit(ctx, uc.UserID, d)
}

// canManageDashboard reports whether the request may share or delete a
// dashboard: ownership, a share at admin level, or the manage permission.
func canManageDashboard(ctx context.Context, d *models.Dashboard) bool {
	uc := middleware.GetUser(ctx)
	return uc != nil && dashops.CanManage(ctx, uc.UserID, d)
}

// DashboardListSectionsInput identifies a dashboard whose sections to list.
type DashboardListSectionsInput struct {
	ID   string `path:"id" description:"Dashboard UUID or slug."`
	View string `query:"view" enum:"deployed,draft" default:"deployed" description:"Which layout to list: the deployed version, or the editable draft."`
}

// DashboardListSectionsHandler returns the interactor that lists a dashboard's
// sections. It follows the same draft/deployed split as the widget list, since
// a section is layout like a placement is.
func DashboardListSectionsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardListSectionsInput, out *apiutil.Success[[]models.DashboardSection]) error {
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}
		if !canReadDashboard(ctx, dashboard) {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}

		rc := middleware.GetRenderContext(ctx)
		draftRender := rc != nil && rc.DashboardID == dashboard.ID && rc.Draft
		if in.View == "draft" && (canEditDashboard(ctx, dashboard) || draftRender) {
			sections, err := models.ListDashboardSections(dashboard.ID)
			if err != nil {
				return apperrors.ErrInternal.WithError(err)
			}
			out.Data = sections
			return nil
		}

		if err := dashops.ApplyDeployedLayout(dashboard); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		out.Data = dashboard.Sections
		return nil
	})
	u.SetTitle("List Dashboard Sections")
	u.SetDescription("Returns a dashboard's collapsible sections in display order. Defaults to the deployed layout; view=draft returns the editable draft to users who may edit it.")
	u.SetTags("Dashboards")
	return u
}

// DashboardAddSectionInput holds the dashboard ID and the new section's fields.
type DashboardAddSectionInput struct {
	ID        string `path:"id" description:"Dashboard UUID or slug."`
	Title     string `json:"title" description:"Section heading."`
	Collapsed bool   `json:"collapsed,omitempty" description:"Whether the section starts collapsed."`
}

// DashboardAddSectionHandler returns the interactor that appends a section to
// the bottom of a dashboard.
func DashboardAddSectionHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardAddSectionInput, out *apiutil.Success[*models.DashboardSection]) error {
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}
		if !canWriteDashboard(ctx, dashboard) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		// A section is a band of a scrolling layout; fit has no room for one.
		if dashboard.LayoutMode != models.DashboardLayoutScroll {
			return apperrors.ErrValidation.WithMessage("sections need the scrolling layout")
		}

		title := in.Title
		if title == "" {
			title = "New section"
		}
		section := &models.DashboardSection{
			ID:          uuid.New(),
			DashboardID: dashboard.ID,
			Title:       title,
			SortOrder:   models.NextDashboardSectionOrder(dashboard.ID),
			Collapsed:   in.Collapsed,
		}
		if err := db.DB.Create(section).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		dashops.NotifyChanged(dashboard.ID)
		out.Data = section
		return nil
	})
	u.SetTitle("Add Dashboard Section")
	u.SetDescription("Appends a collapsible section to the bottom of a scrolling dashboard.")
	u.SetTags("Dashboards")
	return u
}

// DashboardUpdateSectionInput holds the section to patch and its new fields.
type DashboardUpdateSectionInput struct {
	ID        string  `path:"id" description:"Dashboard UUID or slug."`
	SectionID string  `path:"sectionId" format:"uuid" description:"Section UUID."`
	Title     *string `json:"title,omitempty" description:"Section heading."`
	Collapsed *bool   `json:"collapsed,omitempty" description:"Whether the section starts collapsed."`
	SortOrder *int    `json:"sort_order,omitempty" description:"Position among the dashboard's sections."`
}

// DashboardUpdateSectionHandler returns the interactor that patches a section.
func DashboardUpdateSectionHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardUpdateSectionInput, out *apiutil.Success[*models.DashboardSection]) error {
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}
		if !canWriteDashboard(ctx, dashboard) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		section, err := findSectionOn(dashboard, in.SectionID)
		if err != nil {
			return err
		}

		if in.Title != nil {
			section.Title = *in.Title
		}
		if in.Collapsed != nil {
			section.Collapsed = *in.Collapsed
		}
		if in.SortOrder != nil {
			section.SortOrder = *in.SortOrder
		}
		if err := db.DB.Save(section).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		dashops.NotifyChanged(dashboard.ID)
		out.Data = section
		return nil
	})
	u.SetTitle("Update Dashboard Section")
	u.SetDescription("Updates a section's title, collapsed default, or position.")
	u.SetTags("Dashboards")
	return u
}

// DashboardRemoveSectionInput identifies a section to delete.
type DashboardRemoveSectionInput struct {
	ID        string `path:"id" description:"Dashboard UUID or slug."`
	SectionID string `path:"sectionId" format:"uuid" description:"Section UUID."`
}

// DashboardRemoveSectionHandler returns the interactor that deletes a section
// together with the widgets placed in it.
func DashboardRemoveSectionHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardRemoveSectionInput, out *apiutil.Success[any]) error {
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}
		if !canWriteDashboard(ctx, dashboard) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		section, err := findSectionOn(dashboard, in.SectionID)
		if err != nil {
			return err
		}
		if err := section.Delete(); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		dashops.NotifyChanged(dashboard.ID)
		out.Message = "Section removed"
		return nil
	})
	u.SetTitle("Remove Dashboard Section")
	u.SetDescription("Deletes a section and the widgets placed in it.")
	u.SetTags("Dashboards")
	return u
}

// ReorderSectionItem gives one section its new position.
type ReorderSectionItem struct {
	ID        string `json:"id" format:"uuid" description:"Section UUID."`
	SortOrder int    `json:"sort_order" description:"New position."`
}

// DashboardReorderSectionsInput holds the full ordering of a dashboard's sections.
type DashboardReorderSectionsInput struct {
	ID    string               `path:"id" description:"Dashboard UUID or slug."`
	Items []ReorderSectionItem `json:"items" required:"true" minItems:"1" description:"Sections with their new positions."`
}

// DashboardReorderSectionsHandler returns the interactor that repositions a
// dashboard's sections in one transaction, so no intermediate order is ever
// read by a viewer.
func DashboardReorderSectionsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardReorderSectionsInput, out *apiutil.Success[any]) error {
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}
		if !canWriteDashboard(ctx, dashboard) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		err = db.DB.Transaction(func(tx *gorm.DB) error {
			for _, item := range in.Items {
				id, err := uuid.Parse(item.ID)
				if err != nil {
					return errors.New("invalid section ID: " + item.ID)
				}
				res := tx.Model(&models.DashboardSection{}).
					Where("id = ? AND dashboard_id = ?", id, dashboard.ID).
					Update("sort_order", item.SortOrder)
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected == 0 {
					return errors.New("section not found on this dashboard: " + item.ID)
				}
			}
			return nil
		})
		if err != nil {
			return apperrors.ErrValidation.WithError(err)
		}

		dashops.NotifyChanged(dashboard.ID)
		out.Message = "Sections reordered"
		return nil
	})
	u.SetTitle("Reorder Dashboard Sections")
	u.SetDescription("Sets the display order of a dashboard's sections in a single transaction.")
	u.SetTags("Dashboards")
	return u
}

// findSectionOn resolves a section ID against one dashboard, so a section of
// another dashboard cannot be reached through this dashboard's routes.
func findSectionOn(d *models.Dashboard, raw string) (*models.DashboardSection, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, apperrors.ErrValidation.WithMessage("invalid section ID")
	}
	section, err := models.FindDashboardSection(id)
	if err != nil || section.DashboardID != d.ID {
		return nil, apperrors.ErrNotFound.WithMessage("section not found")
	}
	return section, nil
}

// DashboardListAccessInput identifies a dashboard whose access entries to list.
type DashboardListAccessInput struct {
	ID string `path:"id" description:"Dashboard UUID or slug."`
}

// DashboardListAccessHandler returns the interactor that lists sharing entries for a dashboard.
func DashboardListAccessHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardListAccessInput, out *apiutil.Success[[]models.DashboardAccess]) error {
		// Resolve dashboard.
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}
		if !canWriteDashboard(ctx, dashboard) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		// Query access entries.
		entries, err := models.ListDashboardAccess(dashboard.ID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Name each grantee without their account fields.
		for i := range entries {
			entries[i].User = shareGrantee(entries[i].User)
		}

		// Build response.
		out.Data = entries
		return nil
	})
	u.SetTitle("List Dashboard Access")
	u.SetDescription("Returns all sharing/access entries for a dashboard.")
	u.SetTags("Dashboards")
	return u
}

// DashboardAddAccessInput holds the dashboard ID and share details for adding access.
type DashboardAddAccessInput struct {
	ID         string `path:"id" description:"Dashboard UUID or slug."`
	Username   string `json:"username,omitempty" description:"Username to share with."`
	GroupName  string `json:"group_name,omitempty" description:"User group name to share with."`
	Permission string `json:"permission" enum:"read,view,edit,admin" default:"view" description:"Access level: read shows the dashboard without operating it, view also operates it, edit changes and deploys it, admin shares and deletes it."`
}

// DashboardAddAccessHandler returns the interactor that shares a dashboard with a user or group.
func DashboardAddAccessHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardAddAccessInput, out *apiutil.Success[*models.DashboardAccess]) error {
		// Resolve dashboard.
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}

		// Enforce ownership.
		uc := middleware.GetUser(ctx)
		if uc == nil || !dashops.CanManage(ctx, uc.UserID, dashboard) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		// Validate input.
		// Exactly one grantee: a row naming both would be one share that two
		// removals each take away from the other.
		if (in.Username == "") == (in.GroupName == "") {
			return apperrors.ErrValidation.WithMessage("give either username or group_name")
		}

		// Set default permission level.
		permission := in.Permission
		if permission == "" {
			permission = models.DashboardAccessView
		}
		if !models.ValidDashboardAccess(permission) {
			return apperrors.ErrValidation.WithMessage("permission must be read, view, edit, or admin")
		}

		// Build access record.
		access := &models.DashboardAccess{
			DashboardID: dashboard.ID,
			Permission:  permission,
		}

		// Resolve user or group by name.
		if in.Username != "" {
			var user models.User
			if err := db.DB.Where("username = ?", in.Username).First(&user).Error; err != nil {
				return apperrors.ErrValidation.WithMessage("user not found")
			}
			access.UserID = &user.ID
		}

		if in.GroupName != "" {
			var group models.UserGroup
			if err := db.DB.Where("name = ?", in.GroupName).First(&group).Error; err != nil {
				return apperrors.ErrValidation.WithMessage("group not found")
			}
			access.GroupID = &group.ID
		}

		// Persist to database.
		if err := db.DB.Create(access).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		out.Data = access
		return nil
	})
	u.SetTitle("Add Dashboard Access")
	u.SetDescription("Shares a dashboard with a user or group by name.")
	u.SetTags("Dashboards")
	return u
}

// DashboardRemoveAccessInput identifies an access entry to remove from a dashboard.
type DashboardRemoveAccessInput struct {
	ID      string `path:"id" description:"Dashboard UUID or slug."`
	ShareID uint   `path:"shareId" description:"Share entry ID."`
}

// DashboardRemoveAccessHandler returns the interactor that revokes a sharing entry.
func DashboardRemoveAccessHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardRemoveAccessInput, out *apiutil.Success[any]) error {
		// Resolve dashboard.
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}

		// Enforce ownership.
		uc := middleware.GetUser(ctx)
		if uc == nil || !dashops.CanManage(ctx, uc.UserID, dashboard) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		// Delete the entry, and only if it belongs to this dashboard: the id in
		// the path must not reach a share on a dashboard the caller cannot manage.
		tx := db.DB.Where("dashboard_id = ?", dashboard.ID).Delete(&models.DashboardAccess{}, in.ShareID)
		if tx.Error != nil {
			return apperrors.ErrInternal.WithError(tx.Error)
		}
		if tx.RowsAffected == 0 {
			return apperrors.ErrNotFound.WithMessage("share not found")
		}

		// Build response.
		out.Message = "Share removed"
		return nil
	})
	u.SetTitle("Remove Dashboard Access")
	u.SetDescription("Removes a sharing/access entry from a dashboard.")
	u.SetTags("Dashboards")
	return u
}

// ReorderDashboardItem specifies the new sort position and optional group assignment for a dashboard.
type ReorderDashboardItem struct {
	ID        string `json:"id" description:"Dashboard UUID."`
	GroupID   *uint  `json:"group_id" description:"Target group ID; null for ungrouped."`
	SortOrder int    `json:"sort_order" description:"New sort position."`
}

// DashboardReorderInput holds the list of dashboard reorder entries.
type DashboardReorderInput struct {
	Items []ReorderDashboardItem `json:"items" required:"true" minItems:"1" description:"List of dashboard reorder entries."`
}

// DashboardReorderHandler returns the interactor that reorders dashboards in a single transaction.
func DashboardReorderHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardReorderInput, out *apiutil.Success[any]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Authorize every item first. The checks read through db.DB, which
		// must not wait on the connection the transaction below holds.
		ids := make([]uuid.UUID, len(in.Items))
		for i, item := range in.Items {
			dashID, err := uuid.Parse(item.ID)
			if err != nil {
				return apperrors.ErrValidation.WithMessage("invalid dashboard ID: " + item.ID)
			}
			var dashboard models.Dashboard
			if err := db.DB.Select("id", "owner_id", "group_id").Where("id = ?", dashID).First(&dashboard).Error; err != nil {
				return apperrors.ErrNotFound.WithMessage("dashboard not found: " + item.ID)
			}
			if !dashops.CanEdit(ctx, uc.UserID, &dashboard) {
				return apperrors.ErrForbidden.WithMessage("forbidden: cannot reorder dashboard " + item.ID)
			}
			if err := dashops.CheckGroupMove(ctx, uc.UserID, &dashboard, item.GroupID); err != nil {
				return err
			}
			ids[i] = dashID
		}

		// Execute reorder in a single transaction.
		err := db.DB.Transaction(func(tx *gorm.DB) error {
			for i, item := range in.Items {
				// A group ID of 0 means ungrouped, which the column holds
				// as NULL.
				groupID := item.GroupID
				if groupID != nil && *groupID == 0 {
					groupID = nil
				}
				updates := map[string]interface{}{
					"sort_order": item.SortOrder,
					"group_id":   groupID,
				}
				if err := tx.Model(&models.Dashboard{}).Where("id = ?", ids[i]).Updates(updates).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		out.Message = "Dashboards reordered"
		return nil
	})
	u.SetTitle("Reorder Dashboards")
	u.SetDescription("Updates the sort order and group assignment of multiple dashboards in a single transaction.")
	u.SetTags("Dashboards")
	return u
}

// DashboardDeployInput holds the dashboard ID and an optional deploy comment.
type DashboardDeployInput struct {
	ID      string `path:"id" description:"Dashboard UUID or slug."`
	Comment string `json:"comment,omitempty" description:"Deploy comment."`
}

// DashboardDeployHandler returns the interactor that freezes the draft layout
// as a numbered version and points viewers at it.
func DashboardDeployHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardDeployInput, out *apiutil.Success[*models.Dashboard]) error {
		// Verify authentication and resolve the dashboard.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}
		if !canEditDashboard(ctx, dashboard) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		// Snapshot, freeze, and publish the draft.
		next, err := dashops.Deploy(ctx, dashboard, in.Comment, uc.UserID)
		if err != nil {
			return err
		}

		// Build response: the draft the author is still editing, now clean.
		redactDashboardOwner(ctx, dashboard)
		out.Data = dashboard
		out.Message = fmt.Sprintf("Deployed version %d", next)
		return nil
	})
	u.SetTitle("Deploy Dashboard")
	u.SetDescription("Freezes the draft layout as a numbered version and serves it to viewers.")
	u.SetTags("Dashboards")
	return u
}

// DashboardVersionsInput identifies a dashboard whose versions are listed.
type DashboardVersionsInput struct {
	ID string `path:"id" description:"Dashboard UUID or slug."`
}

// DashboardVersionsHandler returns the interactor that lists a dashboard's versions.
func DashboardVersionsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardVersionsInput, out *apiutil.Success[[]*models.DashboardVersion]) error {
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}
		if !canEditDashboard(ctx, dashboard) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		versions, err := models.ListDashboardVersions(dashboard.ID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Data = versions
		return nil
	})
	u.SetTitle("List Dashboard Versions")
	u.SetDescription("Returns a dashboard's deployed versions, newest first.")
	u.SetTags("Dashboards")
	return u
}

// DashboardVersionRestoreInput identifies one numbered version to restore into the draft.
type DashboardVersionRestoreInput struct {
	ID      string `path:"id" description:"Dashboard UUID or slug."`
	Version int    `path:"version" minimum:"1" description:"Version number to restore."`
}

// DashboardVersionRestoreHandler returns the interactor that copies a version's
// layout back into the editable draft. What viewers see is unchanged until the
// restored draft is deployed.
func DashboardVersionRestoreHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardVersionRestoreInput, out *apiutil.Success[*models.Dashboard]) error {
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}
		if !canEditDashboard(ctx, dashboard) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		if err := dashops.RestoreVersion(dashboard, in.Version); err != nil {
			return err
		}

		// Build response from the rewritten draft.
		widgets, err := models.ListDashboardWidgets(dashboard.ID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		sections, err := models.ListDashboardSections(dashboard.ID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		pages, err := models.ListDashboardPages(dashboard.ID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		dashboard.Widgets = widgets
		dashboard.Sections = sections
		dashboard.Pages = pages
		dashops.AnnotateDraftState(dashboard, widgets, sections, pages)
		redactDashboardOwner(ctx, dashboard)
		out.Data = dashboard
		out.Message = fmt.Sprintf("Restored version %d into the draft", in.Version)
		return nil
	})
	u.SetTitle("Restore Dashboard Version")
	u.SetDescription("Copies a version's layout into the editable draft. Viewers keep seeing the deployed version until the draft is deployed again.")
	u.SetTags("Dashboards")
	return u
}

// RegisterDashboardRoutes registers dashboard CRUD, widget placement, and share endpoints.
func RegisterDashboardRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	// Public and optionally-authenticated dashboard reads.
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/dashboards",
			nethttp.NewHandler(DashboardListHandler()))
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/dashboards/{id}",
			nethttp.NewHandler(DashboardGetHandler()))
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/dashboards/{id}/widgets",
			nethttp.NewHandler(DashboardListWidgetsHandler()))
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/dashboards/{id}/sections",
			nethttp.NewHandler(DashboardListSectionsHandler()))

	// Dashboard share reads.
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsRead), bearerDoc).
		Method(http.MethodGet, "/dashboards/{id}/shares",
			nethttp.NewHandler(DashboardListAccessHandler()))

	// Dashboard share writes.
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodPost, "/dashboards/{id}/shares",
			nethttp.NewHandler(DashboardAddAccessHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodDelete, "/dashboards/{id}/shares/{shareId}",
			nethttp.NewHandler(DashboardRemoveAccessHandler()))

	// Dashboard reorder.
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodPut, "/dashboards/reorder",
			nethttp.NewHandler(DashboardReorderHandler()))

	// Authenticated dashboard writes with permission gates.
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodPost, "/dashboards",
			nethttp.NewHandler(DashboardCreateHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodPut, "/dashboards/{id}",
			nethttp.NewHandler(DashboardUpdateHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodPost, "/dashboards/{id}/widgets",
			nethttp.NewHandler(DashboardAddWidgetHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodPut, "/dashboards/{id}/widgets/{widgetId}",
			nethttp.NewHandler(DashboardUpdateWidgetHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodDelete, "/dashboards/{id}/widgets/{widgetId}",
			nethttp.NewHandler(DashboardRemoveWidgetHandler()))

	// Section writes. Reordering is registered before the per-section routes so
	// "reorder" is not read as a section ID.
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodPut, "/dashboards/{id}/sections/reorder",
			nethttp.NewHandler(DashboardReorderSectionsHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodPost, "/dashboards/{id}/sections",
			nethttp.NewHandler(DashboardAddSectionHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodPut, "/dashboards/{id}/sections/{sectionId}",
			nethttp.NewHandler(DashboardUpdateSectionHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodDelete, "/dashboards/{id}/sections/{sectionId}",
			nethttp.NewHandler(DashboardRemoveSectionHandler()))

	// Dashboard versioning: deploy publishes a layout, history reads and
	// restores one.
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsDeploy), bearerDoc).
		Method(http.MethodPost, "/dashboards/{id}/deploy",
			nethttp.NewHandler(DashboardDeployHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsRead), bearerDoc).
		Method(http.MethodGet, "/dashboards/{id}/versions",
			nethttp.NewHandler(DashboardVersionsHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsDeploy), bearerDoc).
		Method(http.MethodPost, "/dashboards/{id}/versions/{version}/restore",
			nethttp.NewHandler(DashboardVersionRestoreHandler()))

	// Dashboard portability: export the draft, import a document as a new one.
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsRead), bearerDoc).
		Method(http.MethodGet, "/dashboards/{id}/export",
			nethttp.NewHandler(DashboardExportHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodPost, "/dashboards/import",
			nethttp.NewHandler(DashboardImportDocumentHandler(), nethttp.SuccessStatus(http.StatusCreated)))

	// Dashboard deletes.
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsDelete), bearerDoc).
		Method(http.MethodDelete, "/dashboards/{id}",
			nethttp.NewHandler(DashboardDeleteHandler()))
}
