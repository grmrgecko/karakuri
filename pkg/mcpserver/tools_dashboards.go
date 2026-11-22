package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	widgetpkg "github.com/grmrgecko/karakuri/pkg/widgets"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type listDashboardsInput struct {
	Page  int `json:"page,omitempty" jsonschema:"Page number, starting at 1."`
	Limit int `json:"limit,omitempty" jsonschema:"Items per page, up to 100; defaults to 50."`
}

type getDashboardInput struct {
	ID   string `json:"id" jsonschema:"Dashboard UUID or slug."`
	View string `json:"view,omitempty" jsonschema:"Which layout to return: deployed (default) or draft. The draft needs edit rights."`
}

type createDashboardInput struct {
	Name          string                   `json:"name" jsonschema:"Display name."`
	Slug          string                   `json:"slug,omitempty" jsonschema:"URL-safe slug; derived from the name when omitted."`
	Description   string                   `json:"description,omitempty" jsonschema:"Optional summary."`
	IsPublic      bool                     `json:"is_public,omitempty" jsonschema:"Whether the dashboard is publicly visible."`
	PublicControl bool                     `json:"public_control,omitempty" jsonschema:"Whether anonymous viewers of a public dashboard may operate its controls."`
	LayoutMode    string                   `json:"layout_mode,omitempty" jsonschema:"fit scales the grid to the viewport; scroll fixes the row height and lets the grid overflow; regions stacks placements in named screen regions. Default fit."`
	GridRows      int                      `json:"grid_rows,omitempty" jsonschema:"Number of grid rows; fit layout only. Default 12."`
	GridCols      int                      `json:"grid_cols,omitempty" jsonschema:"Number of grid columns. Default 12."`
	RowHeight     int                      `json:"row_height,omitempty" jsonschema:"Height of one grid row in pixels; scroll and region layouts. Default 80."`
	Padding       int                      `json:"padding,omitempty" jsonschema:"Outer padding in pixels."`
	Gap           int                      `json:"gap,omitempty" jsonschema:"Gap between widgets in pixels."`
	WidgetPadding int                      `json:"widget_padding,omitempty" jsonschema:"Inner widget padding in pixels."`
	Display       *models.DashboardDisplay `json:"display,omitempty" jsonschema:"Page rotation interval, night and idle dimming, burn-in pixel shift, and variables."`
}

type updateDashboardInput struct {
	ThemeID       *uint                    `json:"theme_id,omitempty" jsonschema:"Theme ID from list_themes; zero clears the theme."`
	ID            string                   `json:"id" jsonschema:"Dashboard UUID or slug."`
	Name          *string                  `json:"name,omitempty" jsonschema:"New display name."`
	Slug          *string                  `json:"slug,omitempty" jsonschema:"New URL-safe slug."`
	Description   *string                  `json:"description,omitempty" jsonschema:"New summary."`
	IsPublic      *bool                    `json:"is_public,omitempty" jsonschema:"Whether the dashboard is publicly visible."`
	PublicControl *bool                    `json:"public_control,omitempty" jsonschema:"Whether anonymous viewers may operate its controls."`
	LayoutMode    *string                  `json:"layout_mode,omitempty" jsonschema:"fit, scroll, or regions. Switching to fit merges sections into the sectionless region."`
	GridRows      *int                     `json:"grid_rows,omitempty" jsonschema:"Number of grid rows; fit layout only."`
	GridCols      *int                     `json:"grid_cols,omitempty" jsonschema:"Number of grid columns."`
	RowHeight     *int                     `json:"row_height,omitempty" jsonschema:"Height of one grid row in pixels; scroll and region layouts."`
	Padding       *int                     `json:"padding,omitempty" jsonschema:"Outer padding in pixels."`
	Gap           *int                     `json:"gap,omitempty" jsonschema:"Gap between widgets in pixels."`
	WidgetPadding *int                     `json:"widget_padding,omitempty" jsonschema:"Inner widget padding in pixels."`
	Display       *models.DashboardDisplay `json:"display,omitempty" jsonschema:"Page rotation, dimming, burn-in, and variable settings; replaces the stored settings, and an empty object clears them."`
	// ExpectedDisplayRevision guards a display replacement.
	ExpectedDisplayRevision *int `json:"expected_display_revision,omitempty" jsonschema:"With display, the display_revision you read; if the display was replaced since, the write fails so you can read it again and merge."`
}

type dashboardIDInput struct {
	ID string `json:"id" jsonschema:"Dashboard UUID or slug."`
}

type addDashboardWidgetInput struct {
	DashboardID string                `json:"dashboard_id" jsonschema:"Dashboard UUID or slug."`
	Package     string                `json:"package" jsonschema:"Widget package name, from list_widget_types."`
	SectionID   string                `json:"section_id,omitempty" jsonschema:"Section UUID to place the widget in; omit for the sectionless region."`
	X           int                   `json:"x" jsonschema:"Grid column position."`
	Y           int                   `json:"y" jsonschema:"Grid row position, relative to the top of its section."`
	Width       int                   `json:"width" jsonschema:"Width in grid units."`
	Height      int                   `json:"height" jsonschema:"Height in grid units."`
	Config      any                   `json:"config,omitempty" jsonschema:"Widget instance configuration object; each widget documents its own shape."`
	PageID      string                `json:"page_id,omitempty" jsonschema:"Page UUID to show the widget on; omit for every page."`
	Region      string                `json:"region,omitempty" jsonschema:"Region name for the region layout: top_bar, top_left, top_center, top_right, upper_third, middle_center, lower_third, bottom_left, bottom_center, bottom_right, bottom_bar, fullscreen_below, or fullscreen_above."`
	Display     *models.WidgetDisplay `json:"display,omitempty" jsonschema:"Placement name, hidden-on-start flag, visibility conditions, animations, tile style, and status cues."`
}

type updateDashboardWidgetInput struct {
	DashboardID string                `json:"dashboard_id" jsonschema:"Dashboard UUID or slug."`
	WidgetID    string                `json:"widget_id" jsonschema:"Widget placement UUID."`
	SectionID   *string               `json:"section_id,omitempty" jsonschema:"Section UUID to move the widget into; an empty string moves it to the sectionless region."`
	X           *int                  `json:"x,omitempty" jsonschema:"Grid column position."`
	Y           *int                  `json:"y,omitempty" jsonschema:"Grid row position, relative to the top of its section."`
	Width       *int                  `json:"width,omitempty" jsonschema:"Width in grid units."`
	Height      *int                  `json:"height,omitempty" jsonschema:"Height in grid units."`
	Config      any                   `json:"config,omitempty" jsonschema:"Widget instance configuration object, replacing the stored one."`
	PageID      *string               `json:"page_id,omitempty" jsonschema:"Page UUID to show the widget on; an empty string shows it on every page."`
	Region      *string               `json:"region,omitempty" jsonschema:"Region name for the region layout."`
	Display     *models.WidgetDisplay `json:"display,omitempty" jsonschema:"Placement display settings, replacing the stored ones; an empty object clears them."`
	// ExpectedConfigRevision guards a config replacement.
	ExpectedConfigRevision *int `json:"expected_config_revision,omitempty" jsonschema:"With config, the config_revision you read; if the config was replaced since, the write fails so you can read it again and merge."`
}

type removeDashboardWidgetInput struct {
	DashboardID string `json:"dashboard_id" jsonschema:"Dashboard UUID or slug."`
	WidgetID    string `json:"widget_id" jsonschema:"Widget placement UUID."`
}

type deployDashboardInput struct {
	ID      string `json:"id" jsonschema:"Dashboard UUID or slug."`
	Comment string `json:"comment,omitempty" jsonschema:"Deploy comment, shown in the version history."`
}

type importDashboardInput struct {
	Document any `json:"document" jsonschema:"An exported dashboard document, as export_dashboard returns."`
}

// editableDashboard resolves a dashboard and checks the caller may change it:
// the owner, or anyone holding the blanket write permission, the same rule the
// REST handlers apply.
func editableDashboard(ctx context.Context, idOrSlug string) (*models.Dashboard, error) {
	uc := middleware.GetUser(ctx)
	if uc == nil {
		return nil, opErr(apperrors.ErrUnauthorized.WithMessage("unauthorized"))
	}
	dashboard, err := dashops.ResolveFor(ctx, callerID(ctx), idOrSlug)
	if err != nil {
		return nil, opErr(err)
	}
	if !dashops.CanEdit(ctx, uc.UserID, dashboard) {
		return nil, opErr(apperrors.ErrForbidden.WithMessage("you do not own this dashboard, hold no edit share on it, and hold no " + perm.PermDashboardsManage))
	}
	return dashboard, nil
}

// redactDashboardOwner strips the account fields off a dashboard's embedded
// owner for a caller who is neither the owner nor a holder of
// api:dashboards:read, as the REST reads do. A share shows the dashboard, not
// its owner's email address or preferences; the ID and username stay.
func redactDashboardOwner(uid uuid.UUID, canRead bool, d *models.Dashboard) {
	if uid == d.OwnerID || canRead {
		return
	}
	d.Owner = models.User{ID: d.Owner.ID, Username: d.Owner.Username}
	if d.Group != nil {
		d.Group.Owner = models.User{ID: d.Group.Owner.ID, Username: d.Group.Owner.Username}
	}
}

// widgetConfigJSON encodes a widget config argument the way placements store
// it: an opaque JSON string, empty when absent.
func widgetConfigJSON(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// addDashboardTools registers the dashboard tools.
func addDashboardTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_dashboards",
		Description: "List the dashboards the calling user may access.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listDashboardsInput) (*mcp.CallToolResult, any, error) {
		uid, err := userID(ctx)
		if err != nil {
			return nil, nil, err
		}
		page, limit := in.Page, in.Limit
		if page < 1 {
			page = 1
		}
		if limit < 1 || limit > 100 {
			limit = 50
		}
		groupIDs, err := models.GetUserGroupIDs(uid)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrInternal.WithError(err))
		}
		dashboards, total, err := models.ListAccessibleDashboards(uid, groupIDs, page, limit)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrInternal.WithError(err))
		}
		// One permission lookup covers the page: HasPermission walks the role
		// tables on every call.
		canRead := auth.Can(ctx, uid, perm.PermDashboardsRead)
		for _, d := range dashboards {
			redactDashboardOwner(uid, canRead, d)
		}
		return nil, map[string]any{"dashboards": dashboards, "total": total}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_dashboard",
		Description: "Get one dashboard with its sections and widget placements: the deployed layout by default, or the editable draft for a caller who may edit.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getDashboardInput) (*mcp.CallToolResult, any, error) {
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return nil, nil, opErr(apperrors.ErrUnauthorized.WithMessage("unauthorized"))
		}
		dashboard, err := dashops.ResolveFor(ctx, callerID(ctx), in.ID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		// Reported as not found so a private slug cannot be confirmed by probing.
		if !dashops.CanView(ctx, uc.UserID, dashboard) {
			return nil, nil, opErr(apperrors.ErrNotFound.WithMessage("dashboard not found"))
		}
		if in.View == "draft" && dashops.CanEdit(ctx, uc.UserID, dashboard) {
			dashops.AnnotateDraftState(dashboard, dashboard.Widgets, dashboard.Sections, dashboard.Pages)
		} else if err := dashops.ApplyDeployedLayout(dashboard); err != nil {
			return nil, nil, opErr(apperrors.ErrInternal.WithError(err))
		}
		redactDashboardOwner(uc.UserID, auth.Can(ctx, uc.UserID, perm.PermDashboardsRead), dashboard)
		return nil, dashboard, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_dashboard",
		Description: "Create a dashboard owned by the calling user.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in createDashboardInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermDashboardsWrite); err != nil {
			return nil, nil, err
		}
		uid, err := userID(ctx)
		if err != nil {
			return nil, nil, err
		}
		rows, cols := in.GridRows, in.GridCols
		if rows <= 0 {
			rows = 12
		}
		if cols <= 0 {
			cols = 12
		}
		dashboard, err := dashops.Create(ctx, dashops.CreateSpec{
			Name:          in.Name,
			Slug:          in.Slug,
			Description:   in.Description,
			IsPublic:      in.IsPublic,
			PublicControl: in.PublicControl,
			LayoutMode:    in.LayoutMode,
			GridRows:      rows,
			GridCols:      cols,
			RowHeight:     in.RowHeight,
			Padding:       in.Padding,
			Gap:           in.Gap,
			WidgetPadding: in.WidgetPadding,
			Display:       in.Display,
		}, uid)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, dashboard, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_dashboard",
		Description: "Patch a dashboard's identity, sharing flags, grid geometry, or display settings. Owner or dashboard write permission required.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updateDashboardInput) (*mcp.CallToolResult, any, error) {
		dashboard, err := editableDashboard(ctx, in.ID)
		if err != nil {
			return nil, nil, err
		}
		uid, err := userID(ctx)
		if err != nil {
			return nil, nil, err
		}
		if err := dashops.Update(ctx, dashboard, dashops.Patch{
			By:            callerID(ctx),
			ThemeID:       in.ThemeID,
			Name:          in.Name,
			Slug:          in.Slug,
			Description:   in.Description,
			IsPublic:      in.IsPublic,
			PublicControl: in.PublicControl,
			LayoutMode:    in.LayoutMode,
			GridRows:      in.GridRows,
			GridCols:      in.GridCols,
			RowHeight:     in.RowHeight,
			Padding:       in.Padding,
			Gap:           in.Gap,
			WidgetPadding: in.WidgetPadding,
			Display:       in.Display,

			ExpectedDisplayRevision: in.ExpectedDisplayRevision,
			UserID:                  uid,
		}); err != nil {
			return nil, nil, opErr(err)
		}
		redactDashboardOwner(uid, auth.Can(ctx, uid, perm.PermDashboardsRead), dashboard)
		return nil, dashboard, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_dashboard",
		Description: "Delete a dashboard with its placements, sections, and versions. Owner or dashboard delete permission required.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in dashboardIDInput) (*mcp.CallToolResult, any, error) {
		uid, err := userID(ctx)
		if err != nil {
			return nil, nil, err
		}
		dashboard, err := dashops.ResolveFor(ctx, callerID(ctx), in.ID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		if !dashops.CanManage(ctx, uid, dashboard) {
			return nil, nil, opErr(apperrors.ErrForbidden.WithMessage("you do not own this dashboard, hold no admin share on it, and hold no " + perm.PermDashboardsManage))
		}
		if err := dashops.Delete(dashboard); err != nil {
			return nil, nil, opErr(err)
		}
		return nil, map[string]any{"deleted": true}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_widget_types",
		Description: "List the widget packages currently available to place on a dashboard.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		if _, err := userID(ctx); err != nil {
			return nil, nil, err
		}
		rows, _, err := models.ListWidgets(1, 500, widgetpkg.AvailablePackages())
		if err != nil {
			return nil, nil, opErr(apperrors.ErrInternal.WithError(err))
		}
		return nil, map[string]any{"widgets": rows}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_dashboard_widget",
		Description: "Place a widget on a dashboard's draft by package name at a grid position.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in addDashboardWidgetInput) (*mcp.CallToolResult, any, error) {
		dashboard, err := editableDashboard(ctx, in.DashboardID)
		if err != nil {
			return nil, nil, err
		}
		var widget models.Widget
		if err := db.DB.Where("package_name = ?", in.Package).First(&widget).Error; err != nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("no installed widget package named " + in.Package))
		}
		config, err := widgetConfigJSON(in.Config)
		if err != nil {
			return nil, nil, err
		}
		dw, err := dashops.AddWidget(ctx, dashboard, dashops.WidgetSpec{
			WidgetID:  widget.ID,
			SectionID: in.SectionID,
			X:         in.X,
			Y:         in.Y,
			Width:     in.Width,
			Height:    in.Height,
			Config:    config,
			PageID:    in.PageID,
			Region:    in.Region,
			Display:   in.Display,
			UserID:    callerID(ctx),
		})
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, dw, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_dashboard_widget",
		Description: "Move, resize, re-section, change the page, region, or display settings of, or reconfigure a widget placement on a dashboard's draft.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updateDashboardWidgetInput) (*mcp.CallToolResult, any, error) {
		dashboard, err := editableDashboard(ctx, in.DashboardID)
		if err != nil {
			return nil, nil, err
		}
		patch := dashops.WidgetPatch{
			SectionID: in.SectionID,
			X:         in.X,
			Y:         in.Y,
			Width:     in.Width,
			Height:    in.Height,
			PageID:    in.PageID,
			Region:    in.Region,
			Display:   in.Display,

			ExpectedConfigRevision: in.ExpectedConfigRevision,
			UserID:                 callerID(ctx),
		}
		if in.Config != nil {
			config, err := widgetConfigJSON(in.Config)
			if err != nil {
				return nil, nil, err
			}
			patch.Config = &config
		}
		dw, err := dashops.UpdateWidget(ctx, dashboard, in.WidgetID, patch)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, dw, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "remove_dashboard_widget",
		Description: "Remove a widget placement from a dashboard's draft.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in removeDashboardWidgetInput) (*mcp.CallToolResult, any, error) {
		dashboard, err := editableDashboard(ctx, in.DashboardID)
		if err != nil {
			return nil, nil, err
		}
		if err := dashops.RemoveWidget(dashboard, in.WidgetID); err != nil {
			return nil, nil, opErr(err)
		}
		return nil, map[string]any{"removed": true}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "deploy_dashboard",
		Description: "Freeze the dashboard's draft layout as a numbered version and publish it to viewers.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in deployDashboardInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermDashboardsDeploy); err != nil {
			return nil, nil, err
		}
		uid, err := userID(ctx)
		if err != nil {
			return nil, nil, err
		}
		dashboard, err := dashops.ResolveFor(ctx, callerID(ctx), in.ID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		if !dashops.CanEdit(ctx, uid, dashboard) {
			return nil, nil, opErr(apperrors.ErrForbidden.WithMessage("you do not own this dashboard, hold no edit share on it, and hold no " + perm.PermDashboardsManage))
		}
		version, err := dashops.Deploy(ctx, dashboard, in.Comment, uid)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, map[string]any{"version": version}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "export_dashboard",
		Description: "Export a dashboard's draft layout as a portable document with widgets named by package.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in dashboardIDInput) (*mcp.CallToolResult, any, error) {
		dashboard, err := editableDashboard(ctx, in.ID)
		if err != nil {
			return nil, nil, err
		}
		doc, err := dashops.Export(dashboard)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, doc, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "import_dashboard",
		Description: "Create a dashboard from an exported document. It lands as a private, undeployed draft owned by the caller.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in importDashboardInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermDashboardsWrite); err != nil {
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
		var doc dashops.ExportDoc
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("document does not parse as an exported dashboard: " + err.Error()))
		}
		dashboard, err := dashops.Import(ctx, &doc, uid)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, dashboard, nil
	})
}
