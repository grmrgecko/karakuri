package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/dashlive"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	"github.com/grmrgecko/karakuri/pkg/dashscope"
	"github.com/grmrgecko/karakuri/pkg/dashvars"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/newsfeed"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// editableFor resolves a dashboard the request may edit.
func editableFor(ctx context.Context, ref string) (*models.Dashboard, error) {
	dashboard, err := resolveDashboard(ctx, ref)
	if err != nil {
		return nil, apperrors.ErrNotFound.WithMessage("dashboard not found")
	}
	if !canWriteDashboard(ctx, dashboard) {
		return nil, apperrors.ErrForbidden.WithMessage("forbidden")
	}
	return dashboard, nil
}

// DashboardPageInput identifies a dashboard and carries a new page's title.
type DashboardPageInput struct {
	ID    string `path:"id" description:"Dashboard UUID or slug."`
	Title string `json:"title" description:"Page title."`
}

// DashboardAddPageHandler returns the interactor that appends a page.
func DashboardAddPageHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardPageInput, out *apiutil.Success[*models.DashboardPage]) error {
		dashboard, err := editableFor(ctx, in.ID)
		if err != nil {
			return err
		}
		page, err := dashops.AddPage(dashboard, in.Title)
		if err != nil {
			return err
		}
		out.Data = page
		return nil
	})
	u.SetTitle("Add Dashboard Page")
	u.SetDescription("Appends a page to a dashboard's draft. Placements without a page appear on every page.")
	u.SetTags("Dashboards")
	return u
}

// DashboardUpdatePageInput holds the page to patch and its new fields.
type DashboardUpdatePageInput struct {
	ID        string  `path:"id" description:"Dashboard UUID or slug."`
	PageID    string  `path:"pageId" format:"uuid" description:"Page UUID."`
	Title     *string `json:"title,omitempty" description:"Page title."`
	SortOrder *int    `json:"sort_order,omitempty" description:"Position among the dashboard's pages."`
}

// DashboardUpdatePageHandler returns the interactor that renames or moves a page.
func DashboardUpdatePageHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardUpdatePageInput, out *apiutil.Success[*models.DashboardPage]) error {
		dashboard, err := editableFor(ctx, in.ID)
		if err != nil {
			return err
		}
		page, err := dashops.UpdatePage(dashboard, in.PageID, dashops.PagePatch{Title: in.Title, SortOrder: in.SortOrder})
		if err != nil {
			return err
		}
		out.Data = page
		return nil
	})
	u.SetTitle("Update Dashboard Page")
	u.SetDescription("Renames a page or changes its position.")
	u.SetTags("Dashboards")
	return u
}

// DashboardReorderPagesInput lists every page's new position.
type DashboardReorderPagesInput struct {
	ID    string `path:"id" description:"Dashboard UUID or slug."`
	Pages []struct {
		ID        string `json:"id" description:"Page UUID."`
		SortOrder int    `json:"sort_order" description:"New position."`
	} `json:"pages" description:"Pages and their positions."`
}

// DashboardReorderPagesHandler returns the interactor that renumbers pages.
func DashboardReorderPagesHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardReorderPagesInput, out *apiutil.Success[any]) error {
		dashboard, err := editableFor(ctx, in.ID)
		if err != nil {
			return err
		}
		order := make(map[uuid.UUID]int, len(in.Pages))
		for _, p := range in.Pages {
			id, err := uuid.Parse(p.ID)
			if err != nil {
				return apperrors.ErrValidation.WithMessage("invalid page ID")
			}
			order[id] = p.SortOrder
		}
		if err := dashops.ReorderPages(dashboard, order); err != nil {
			return err
		}
		out.Message = "Pages reordered"
		return nil
	})
	u.SetTitle("Reorder Dashboard Pages")
	u.SetDescription("Sets the position of several pages at once.")
	u.SetTags("Dashboards")
	return u
}

// DashboardRemovePageInput identifies a page to delete.
type DashboardRemovePageInput struct {
	ID     string `path:"id" description:"Dashboard UUID or slug."`
	PageID string `path:"pageId" format:"uuid" description:"Page UUID."`
}

// DashboardRemovePageHandler returns the interactor that deletes a page.
func DashboardRemovePageHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardRemovePageInput, out *apiutil.Success[any]) error {
		dashboard, err := editableFor(ctx, in.ID)
		if err != nil {
			return err
		}
		if err := dashops.RemovePage(dashboard, in.PageID); err != nil {
			return err
		}
		out.Message = "Page removed; its widgets now appear on every page"
		return nil
	})
	u.SetTitle("Remove Dashboard Page")
	u.SetDescription("Deletes a page. Its placements move to every page rather than being deleted.")
	u.SetTags("Dashboards")
	return u
}

// DashboardLiveInput identifies a dashboard whose runtime state to read.
type DashboardLiveInput struct {
	ID string `path:"id" description:"Dashboard UUID or slug."`
}

// DashboardLiveHandler returns the interactor that reads a dashboard's
// runtime display state. Anyone who may view the dashboard may read it,
// because it decides what they are shown.
func DashboardLiveHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardLiveInput, out *apiutil.Success[*dashlive.State]) error {
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil || !canReadDashboard(ctx, dashboard) {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}
		out.Data = dashlive.S.Get(dashboard.ID)
		return nil
	})
	u.SetTitle("Get Dashboard Live State")
	u.SetDescription("Returns a dashboard's runtime display state: placement locks, the commanded page, active alerts, the dim override, and pushed content. Follow dashboard:{id}:live for changes.")
	u.SetTags("Dashboards")
	return u
}

// DashboardCommandInput carries one display command.
type DashboardCommandInput struct {
	ID string `path:"id" description:"Dashboard UUID or slug."`
	dashlive.Command
}

// DashboardCommandHandler returns the interactor that applies a display
// command to every open viewer of a dashboard.
func DashboardCommandHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardCommandInput, out *apiutil.Success[*dashlive.Result]) error {
		dashboard, err := editableFor(ctx, in.ID)
		if err != nil {
			return err
		}
		res, err := dashlive.S.Apply(dashboard.ID, in.Command)
		if err != nil {
			return err
		}
		out.Data = res
		return nil
	})
	u.SetTitle("Command Dashboard")
	u.SetDescription("Shows or hides placements under named locks, changes page, shows or clears alerts, dims, reloads viewers, pushes content into a placement, or sets a variable. The runtime state is not persisted across restarts.")
	u.SetTags("Dashboards")
	return u
}

// DashboardFeedInput identifies the news placement whose feeds to read.
type DashboardFeedInput struct {
	ID       string `path:"id" description:"Dashboard UUID or slug."`
	WidgetID string `path:"widgetId" format:"uuid" description:"Placement UUID."`
	// Vars carries the viewer's variable values as a JSON object, so feed URLs
	// that name a variable follow the viewer. Each must be an option.
	Vars string `query:"vars" description:"Viewer variable values as a JSON object of name to option value."`
}

// DashboardFeed is the merged headlines of a news placement's feeds.
type DashboardFeed struct {
	Items  []newsfeed.Item `json:"items"`
	Errors []FeedError     `json:"errors"`
}

// FeedError names one feed that could not be read.
type FeedError struct {
	Feed  string `json:"feed"`
	Error string `json:"error"`
}

// DashboardFeedHandler returns the interactor that fetches a news placement's
// feeds. The request names a placement, never a URL: the URLs come from the
// placement's served configuration, so a viewer cannot point the server at an
// address the author did not choose.
func DashboardFeedHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardFeedInput, out *apiutil.Success[DashboardFeed]) error {
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil || !canReadDashboard(ctx, dashboard) {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}

		// Read the placement the caller is shown: an editor or a draft
		// render sees the draft, everyone else the served layout.
		rc := middleware.GetRenderContext(ctx)
		draft := canEditDashboard(ctx, dashboard) || (rc != nil && rc.DashboardID == dashboard.ID && rc.Draft)
		if !draft {
			if err := dashops.ApplyDeployedLayout(dashboard); err != nil {
				return apperrors.ErrInternal.WithError(err)
			}
		}
		var placement *models.DashboardWidget
		for i := range dashboard.Widgets {
			if dashboard.Widgets[i].ID.String() == in.WidgetID {
				placement = &dashboard.Widgets[i]
			}
		}
		if placement == nil || placement.Widget.PackageName != "news-feed" {
			return apperrors.ErrNotFound.WithMessage("news placement not found on this dashboard")
		}

		// Feed URLs may name variables. A viewer's values are substituted only
		// when they are options, so the server still fetches only URLs the
		// author's options produce.
		served, err := dashops.ServedVariables(dashboard, draft)
		if err != nil {
			return err
		}
		resolved, _ := dashvars.Resolve(served)
		values := dashscope.DefaultValues(resolved)
		if in.Vars != "" {
			var requested map[string]string
			if err := json.Unmarshal([]byte(in.Vars), &requested); err != nil {
				return apperrors.ErrValidation.WithMessage("vars must be a JSON object of variable values")
			}
			display := &models.DashboardDisplay{Variables: resolved}
			for name, ref := range requested {
				variable, ok := display.FindVariable(name)
				if !ok {
					continue
				}
				option, ok := variable.Resolve(ref)
				if !ok {
					return apperrors.ErrValidation.WithMessage(fmt.Sprintf("%q is not an option of variable %q", ref, name))
				}
				values[name] = option
			}
		}
		config := dashscope.SubstituteConfig(placement.Config, values)

		var cfg struct {
			Feeds []newsfeed.Feed `json:"feeds"`
		}
		if config != "" {
			if err := json.Unmarshal([]byte(config), &cfg); err != nil {
				return apperrors.ErrValidation.WithMessage("news placement configuration is not valid JSON")
			}
		}
		if len(cfg.Feeds) > newsfeed.MaxFeeds {
			cfg.Feeds = cfg.Feeds[:newsfeed.MaxFeeds]
		}

		result := DashboardFeed{Items: []newsfeed.Item{}, Errors: []FeedError{}}
		for _, feed := range cfg.Feeds {
			if feed.URL == "" {
				continue
			}
			items, err := newsfeed.Fetch(ctx, feed)
			if err != nil {
				result.Errors = append(result.Errors, FeedError{Feed: feed.Title, Error: err.Error()})
			}
			result.Items = append(result.Items, items...)
		}
		// Newest first across every feed; undated items keep their order
		// at the end.
		sort.SliceStable(result.Items, func(i, j int) bool {
			a, b := result.Items[i].Published, result.Items[j].Published
			if a == "" || b == "" {
				return a != "" && b == ""
			}
			return a > b
		})
		out.Data = result
		return nil
	})
	u.SetTitle("Get Dashboard News Feed")
	u.SetDescription("Returns the merged headlines of a news-feed placement's configured feeds.")
	u.SetTags("Dashboards")
	return u
}

// RegisterDashboardDisplayRoutes registers pages, runtime display state,
// commands, variables, and the news feed route.
func RegisterDashboardDisplayRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	registerDashboardVariableRoutes(s, bearerDoc)
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/dashboards/{id}/live",
			nethttp.NewHandler(DashboardLiveHandler()))
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/dashboards/{id}/widgets/{widgetId}/feed",
			nethttp.NewHandler(DashboardFeedHandler()))
	// Chart data is read by viewers of every kind, including anonymous and
	// render viewers, so the handler checks dashboard access itself.
	s.With(middleware.OptionalAuth()).
		Method(http.MethodPost, "/dashboards/{id}/widgets/{widgetId}/chart",
			nethttp.NewHandler(DashboardChartHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodPost, "/dashboards/{id}/chart/preview",
			nethttp.NewHandler(DashboardChartPreviewHandler()))

	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodPost, "/dashboards/{id}/commands",
			nethttp.NewHandler(DashboardCommandHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodPut, "/dashboards/{id}/pages/reorder",
			nethttp.NewHandler(DashboardReorderPagesHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodPost, "/dashboards/{id}/pages",
			nethttp.NewHandler(DashboardAddPageHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodPut, "/dashboards/{id}/pages/{pageId}",
			nethttp.NewHandler(DashboardUpdatePageHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodDelete, "/dashboards/{id}/pages/{pageId}",
			nethttp.NewHandler(DashboardRemovePageHandler()))
}
