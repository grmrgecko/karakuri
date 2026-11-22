package api

import (
	"context"
	"net/http"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/uploads"
	widgetpkg "github.com/grmrgecko/karakuri/pkg/widgets"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// WidgetListInput holds pagination parameters for listing widgets.
type WidgetListInput struct {
	Page  int64 `query:"page" default:"1" minimum:"1" description:"Page number."`
	Limit int64 `query:"limit" default:"20" minimum:"1" maximum:"100" description:"Number of items per page."`
}

// WidgetListHandler returns the interactor that lists the widgets available to
// place. A widget belongs to the plugin that contributes it, so the list is
// the packages whose plugin is currently loaded — disabling a plugin takes its
// widgets out of the picker.
func WidgetListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in WidgetListInput, out *apiutil.Success[[]*models.Widget]) error {
		rows, total, err := models.ListWidgets(int(in.Page), int(in.Limit), widgetpkg.AvailablePackages())
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Data = rows
		out.Meta = apiutil.MakePageMeta(in.Limit, total, in.Page)
		return nil
	})
	u.SetTitle("List Widgets")
	u.SetDescription("Returns the widgets currently available to place on a dashboard.")
	u.SetTags("Widgets")
	return u
}

// WidgetGetInput identifies a widget to retrieve by ID.
type WidgetGetInput struct {
	ID uint `path:"id" description:"Widget ID."`
}

// WidgetGetHandler returns the interactor that retrieves a single widget by ID.
func WidgetGetHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in WidgetGetInput, out *apiutil.Success[*models.Widget]) error {
		var widget models.Widget
		if err := db.DB.First(&widget, in.ID).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("widget not found")
		}

		out.Data = &widget
		return nil
	})
	u.SetTitle("Get Widget")
	u.SetDescription("Returns a single widget by ID.")
	u.SetTags("Widgets")
	return u
}

// RegisterWidgetRoutes registers the widget catalog and upload management
// endpoints. There are no widget mutations: rows are derived from plugin
// manifests, and a widget is enabled or disabled by its plugin.
func RegisterWidgetRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	// Public widget reads.
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/widgets",
			nethttp.NewHandler(WidgetListHandler()))
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/widgets/{id}",
			nethttp.NewHandler(WidgetGetHandler()))

	// Widget file uploads scoped to a dashboard placement.
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodGet, "/dashboards/{id}/widgets/{widgetId}/uploads",
			nethttp.NewHandler(uploads.ListUploadsInteractor()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodPut, "/dashboards/{id}/widgets/{widgetId}/uploads/reorder",
			nethttp.NewHandler(uploads.ReorderUploadsInteractor()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodDelete, "/dashboards/{id}/widgets/{widgetId}/uploads/{uploadId}",
			nethttp.NewHandler(uploads.DeleteUploadInteractor()))
}
