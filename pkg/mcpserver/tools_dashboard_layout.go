package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/dashlive"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	"github.com/grmrgecko/karakuri/pkg/dashvars"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type addDashboardSectionInput struct {
	DashboardID string `json:"dashboard_id" jsonschema:"Dashboard UUID or slug. Must use the scrolling layout."`
	Title       string `json:"title" jsonschema:"Section heading."`
	Collapsed   bool   `json:"collapsed,omitempty" jsonschema:"Whether the section starts collapsed."`
}

type updateDashboardSectionInput struct {
	DashboardID string  `json:"dashboard_id" jsonschema:"Dashboard UUID or slug."`
	SectionID   string  `json:"section_id" jsonschema:"Section UUID on this dashboard."`
	Title       *string `json:"title,omitempty" jsonschema:"Section heading."`
	Collapsed   *bool   `json:"collapsed,omitempty" jsonschema:"Whether the section starts collapsed."`
	SortOrder   *int    `json:"sort_order,omitempty" jsonschema:"Position among sections, starting at zero."`
}

type removeDashboardSectionInput struct {
	DashboardID string `json:"dashboard_id" jsonschema:"Dashboard UUID or slug."`
	SectionID   string `json:"section_id" jsonschema:"Section UUID on this dashboard."`
}

type addDashboardPageInput struct {
	DashboardID string `json:"dashboard_id" jsonschema:"Dashboard UUID or slug."`
	Title       string `json:"title" jsonschema:"Page title."`
}

type updateDashboardPageInput struct {
	DashboardID string  `json:"dashboard_id" jsonschema:"Dashboard UUID or slug."`
	PageID      string  `json:"page_id" jsonschema:"Page UUID on this dashboard."`
	Title       *string `json:"title,omitempty" jsonschema:"Page title."`
	SortOrder   *int    `json:"sort_order,omitempty" jsonschema:"Position among pages, starting at zero."`
}

type removeDashboardPageInput struct {
	DashboardID string `json:"dashboard_id" jsonschema:"Dashboard UUID or slug."`
	PageID      string `json:"page_id" jsonschema:"Page UUID on this dashboard."`
}

type commandDashboardInput struct {
	ID       string   `json:"id" jsonschema:"Dashboard UUID or slug."`
	Action   string   `json:"action" jsonschema:"show, hide, page, next_page, previous_page, alert, clear_alert, dim, undim, reload, content, or variable."`
	Targets  []string `json:"targets,omitempty" jsonschema:"Placement IDs for show, hide, and content."`
	Lock     string   `json:"lock,omitempty" jsonschema:"Lock name for show and hide; defaults to command. A placement authored hidden starts under the lock start."`
	Force    bool     `json:"force,omitempty" jsonschema:"With show, clear every lock on the placement."`
	Page     string   `json:"page,omitempty" jsonschema:"Page ID, title, or 1-based index for page; empty returns to the first page."`
	Title    string   `json:"title,omitempty" jsonschema:"Alert title, up to 200 bytes."`
	Message  string   `json:"message,omitempty" jsonschema:"Alert message, up to 4 KiB."`
	Level    string   `json:"level,omitempty" jsonschema:"Alert level: info (default), success, warning, or error."`
	Style    string   `json:"style,omitempty" jsonschema:"Alert style: toast (default) or fullscreen."`
	Speak    bool     `json:"speak,omitempty" jsonschema:"Read the alert aloud on each open viewer."`
	Duration *int     `json:"duration,omitempty" jsonschema:"Alert seconds from 0 to 86400; 0 keeps it until cleared; default 10."`
	AlertID  string   `json:"alert_id,omitempty" jsonschema:"Alert to clear; empty clears every alert."`
	DimLevel *int     `json:"dim_level,omitempty" jsonschema:"Dim level 0 to 100 for dim; default 100 (black)."`
	Value    any      `json:"value,omitempty" jsonschema:"Content value for content: a string, a list of strings, or any JSON value; null clears it. For variable, a string naming an option value or label; an empty string returns to the default. Constants and variables that follow an entity are refused."`
	Format   string   `json:"format,omitempty" jsonschema:"Content format: text (default), markdown, or json."`
	Variable string   `json:"variable,omitempty" jsonschema:"Variable name for the variable action, from the dashboard's display.variables."`
}

func dashboardSection(dashboard *models.Dashboard, raw string) (*models.DashboardSection, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, opErr(apperrors.ErrValidation.WithMessage("invalid section ID"))
	}
	section, err := models.FindDashboardSection(id)
	if err != nil || section.DashboardID != dashboard.ID {
		return nil, opErr(apperrors.ErrNotFound.WithMessage("section not found on this dashboard"))
	}
	return section, nil
}

func addDashboardLayoutTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "list_dashboard_versions", Description: "List deployed dashboard versions, newest first."},
		func(ctx context.Context, req *mcp.CallToolRequest, in dashboardIDInput) (*mcp.CallToolResult, any, error) {
			d, err := editableDashboard(ctx, in.ID)
			if err != nil {
				return nil, nil, err
			}
			versions, err := models.ListDashboardVersions(d.ID)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, map[string]any{"versions": versions}, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "restore_dashboard_version", Description: "Replace the whole dashboard draft with a deployed version. Omit version to discard all changes since the current deployment. This does not deploy."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			ID      string `json:"id" jsonschema:"Dashboard UUID."`
			Version int    `json:"version,omitempty" jsonschema:"Version to restore; omit for the currently deployed version."`
		}) (*mcp.CallToolResult, any, error) {
			d, err := editableDashboard(ctx, in.ID)
			if err != nil {
				return nil, nil, err
			}
			if err := dashops.RestoreVersion(d, in.Version); err != nil {
				return nil, nil, opErr(err)
			}
			updated, err := dashops.ResolveFor(ctx, callerID(ctx), in.ID)
			if err != nil {
				return nil, nil, opErr(err)
			}
			dashops.AnnotateDraftState(updated, updated.Widgets, updated.Sections, updated.Pages)
			uid := callerID(ctx)
			redactDashboardOwner(uid, auth.Can(ctx, uid, perm.PermDashboardsRead), updated)
			return nil, updated, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "list_themes", Description: "List available dashboard theme IDs, names, and descriptions."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
			if _, err := userID(ctx); err != nil {
				return nil, nil, err
			}
			var themes []struct {
				ID          uint   `json:"id"`
				Name        string `json:"name"`
				Description string `json:"description"`
			}
			if err := db.DB.Model(&models.Theme{}).Select("id", "name", "description").Order("name ASC").Find(&themes).Error; err != nil {
				return nil, nil, opErr(apperrors.ErrInternal.WithError(err))
			}
			return nil, map[string]any{"themes": themes}, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "add_dashboard_section", Description: "Append a section to a scrolling dashboard's draft."},
		func(ctx context.Context, req *mcp.CallToolRequest, in addDashboardSectionInput) (*mcp.CallToolResult, any, error) {
			d, err := editableDashboard(ctx, in.DashboardID)
			if err != nil {
				return nil, nil, err
			}
			if d.LayoutMode != models.DashboardLayoutScroll {
				return nil, nil, opErr(apperrors.ErrValidation.WithMessage("sections need the scrolling layout"))
			}
			section := &models.DashboardSection{DashboardID: d.ID, Title: in.Title, Collapsed: in.Collapsed, SortOrder: models.NextDashboardSectionOrder(d.ID)}
			if err := db.DB.Create(section).Error; err != nil {
				return nil, nil, opErr(apperrors.ErrInternal.WithError(err))
			}
			dashops.NotifyChanged(d.ID)
			return nil, section, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "update_dashboard_section", Description: "Rename, reorder, or change the collapsed default of a dashboard section."},
		func(ctx context.Context, req *mcp.CallToolRequest, in updateDashboardSectionInput) (*mcp.CallToolResult, any, error) {
			d, err := editableDashboard(ctx, in.DashboardID)
			if err != nil {
				return nil, nil, err
			}
			section, err := dashboardSection(d, in.SectionID)
			if err != nil {
				return nil, nil, err
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
				return nil, nil, opErr(apperrors.ErrInternal.WithError(err))
			}
			dashops.NotifyChanged(d.ID)
			return nil, section, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "remove_dashboard_section", Description: "Delete a dashboard section and all widget placements inside it. Move widgets out first to keep them."},
		func(ctx context.Context, req *mcp.CallToolRequest, in removeDashboardSectionInput) (*mcp.CallToolResult, any, error) {
			d, err := editableDashboard(ctx, in.DashboardID)
			if err != nil {
				return nil, nil, err
			}
			section, err := dashboardSection(d, in.SectionID)
			if err != nil {
				return nil, nil, err
			}
			if err := section.Delete(); err != nil {
				return nil, nil, opErr(apperrors.ErrInternal.WithError(err))
			}
			dashops.NotifyChanged(d.ID)
			return nil, map[string]any{"removed": true}, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "add_dashboard_page", Description: "Append a page to a dashboard's draft. Placements without a page_id appear on every page; set a placement's page_id to show it on one."},
		func(ctx context.Context, req *mcp.CallToolRequest, in addDashboardPageInput) (*mcp.CallToolResult, any, error) {
			d, err := editableDashboard(ctx, in.DashboardID)
			if err != nil {
				return nil, nil, err
			}
			page, err := dashops.AddPage(d, in.Title)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, page, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "update_dashboard_page", Description: "Rename or reorder a dashboard page."},
		func(ctx context.Context, req *mcp.CallToolRequest, in updateDashboardPageInput) (*mcp.CallToolResult, any, error) {
			d, err := editableDashboard(ctx, in.DashboardID)
			if err != nil {
				return nil, nil, err
			}
			page, err := dashops.UpdatePage(d, in.PageID, dashops.PagePatch{Title: in.Title, SortOrder: in.SortOrder})
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, page, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "remove_dashboard_page", Description: "Delete a dashboard page. Its placements move to every page; none are deleted."},
		func(ctx context.Context, req *mcp.CallToolRequest, in removeDashboardPageInput) (*mcp.CallToolResult, any, error) {
			d, err := editableDashboard(ctx, in.DashboardID)
			if err != nil {
				return nil, nil, err
			}
			if err := dashops.RemovePage(d, in.PageID); err != nil {
				return nil, nil, opErr(err)
			}
			return nil, map[string]any{"removed": true}, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "get_dashboard_live", Description: "Read a dashboard's runtime display state: placement locks, the commanded page, active alerts, the dim override, and pushed content."},
		func(ctx context.Context, req *mcp.CallToolRequest, in dashboardIDInput) (*mcp.CallToolResult, any, error) {
			d, err := editableDashboard(ctx, in.ID)
			if err != nil {
				return nil, nil, err
			}
			return nil, dashlive.S.Get(d.ID), nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "resolve_dashboard_variables", Description: "Resolve a dashboard's variables: the options of custom, constant, and Home Assistant query variables, any query errors, and the values of variables that follow a select entity. Use it to check a query before deploying and to see which values the variable command accepts."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			ID   string `json:"id" jsonschema:"Dashboard UUID or slug."`
			View string `json:"view,omitempty" jsonschema:"draft (default) or deployed."`
		}) (*mcp.CallToolResult, any, error) {
			d, err := editableDashboard(ctx, in.ID)
			if err != nil {
				return nil, nil, err
			}
			if in.View != "" && in.View != "draft" && in.View != "deployed" {
				return nil, nil, opErr(apperrors.ErrValidation.WithMessage("view must be draft or deployed"))
			}
			served, err := dashops.ServedVariables(d, in.View != "deployed")
			if err != nil {
				return nil, nil, opErr(err)
			}
			resolved, followed := dashvars.Resolve(served)
			errs := map[string]string{}
			for _, v := range served {
				if v.Kind() == models.VariableTypeQuery {
					if r := dashvars.Query(ctx, v.Query); r.Err != nil {
						errs[v.Name] = r.Err.Error()
					}
				}
			}
			return nil, map[string]any{"variables": resolved, "followed": followed, "errors": errs}, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "command_dashboard", Description: "Act on every open viewer of a dashboard: show or hide placements under a named lock (show with force clears all locks), change page by ID, title, or 1-based index, step pages, show or clear alerts, dim or undim, reload viewers, push content (text, markdown, or JSON) into placements, or set a variable's dashboard-wide value. Targets are placement IDs from the served layout. The state is not persisted across restarts and never changes the draft."},
		func(ctx context.Context, req *mcp.CallToolRequest, in commandDashboardInput) (*mcp.CallToolResult, any, error) {
			d, err := editableDashboard(ctx, in.ID)
			if err != nil {
				return nil, nil, err
			}
			cmd := dashlive.Command{
				Action: in.Action, Targets: in.Targets, Lock: in.Lock, Force: in.Force, Page: in.Page,
				Title: in.Title, Message: in.Message, Level: in.Level, Style: in.Style, Speak: in.Speak, Duration: in.Duration,
				AlertID: in.AlertID, DimLevel: in.DimLevel, Format: in.Format, Variable: in.Variable,
			}
			if in.Value != nil {
				raw, err := json.Marshal(in.Value)
				if err != nil {
					return nil, nil, opErr(apperrors.ErrValidation.WithMessage("content value must be JSON"))
				}
				cmd.Value = raw
			}
			res, err := dashlive.S.Apply(d.ID, cmd)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, res, nil
		})
}
