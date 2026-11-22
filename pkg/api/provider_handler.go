package api

import (
	"context"
	"net/http"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/providers"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// ProviderListInput names the provider category to list.
type ProviderListInput struct {
	Type string `path:"type" description:"Provider category, e.g. weather."`
}

// ProviderListHandler returns the interactor that lists the providers a type
// currently offers. A widget's config panel reads this to populate its backend
// picker, so the answer must reflect which plugins are loaded right now.
func ProviderListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ProviderListInput, out *apiutil.Success[[]providers.Provider]) error {
		list := providers.R.List(in.Type)
		if list == nil {
			list = []providers.Provider{}
		}
		out.Data = list
		return nil
	})
	u.SetTitle("List Providers")
	u.SetDescription("Returns the registered providers of a type.")
	u.SetTags("Providers")
	return u
}

// ProviderTypeListHandler returns the interactor that lists the provider
// categories anything has registered under.
func ProviderTypeListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in struct{}, out *apiutil.Success[[]string]) error {
		types := providers.R.Types()
		if types == nil {
			types = []string{}
		}
		out.Data = types
		return nil
	})
	u.SetTitle("List Provider Types")
	u.SetDescription("Returns the registered provider categories.")
	u.SetTags("Providers")
	return u
}

// RegisterProviderRoutes registers the provider catalog endpoints. Reads are
// open to anyone authenticated or not: a public dashboard's widget config
// needs the list, and an endpoint name is not a secret.
func RegisterProviderRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/providers",
			nethttp.NewHandler(ProviderTypeListHandler()))
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/providers/{type}",
			nethttp.NewHandler(ProviderListHandler()))
}
