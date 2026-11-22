package api

import (
	"context"
	"net/http"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// SlugInput names the slug to resolve.
type SlugInput struct {
	Slug string `path:"slug" description:"Readable address to resolve."`
}

// FlowBySlugHandler returns the interactor that lists the flows a slug could
// mean to the caller. One match is the flow to open; several are a choice.
func FlowBySlugHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in SlugInput, out *apiutil.Success[[]flowops.SlugMatch]) error {
		matches, err := flowops.SlugMatches(ctx, callerID(ctx), in.Slug)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		out.Data = matches
		return nil
	})
	u.SetTitle("Find Flows by Slug")
	u.SetDescription("Lists the flows using a slug that the caller may read, by ID. Slugs are not unique; the ID is the canonical address.")
	u.SetTags("Flows")
	return u
}

// DashboardBySlugHandler returns the interactor that lists the dashboards a
// slug could mean to the caller, public ones included for anonymous callers.
func DashboardBySlugHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in SlugInput, out *apiutil.Success[[]dashops.SlugMatch]) error {
		matches, err := dashops.SlugMatches(ctx, callerID(ctx), in.Slug)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		out.Data = matches
		return nil
	})
	u.SetTitle("Find Dashboards by Slug")
	u.SetDescription("Lists the dashboards using a slug that the caller may view, by ID; anonymous callers see public dashboards. Slugs are not unique; the ID is the canonical address.")
	u.SetTags("Dashboards")
	return u
}

// RegisterSlugRoutes registers the slug resolution endpoints.
func RegisterSlugRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsRead), bearerDoc).
		Method(http.MethodGet, "/flows/by_slug/{slug}",
			nethttp.NewHandler(FlowBySlugHandler()))
	// Public dashboards answer anonymous callers, as their layout does.
	s.With(middleware.OptionalAuth(), bearerDoc).
		Method(http.MethodGet, "/dashboards/by_slug/{slug}",
			nethttp.NewHandler(DashboardBySlugHandler()))
}
