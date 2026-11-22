// Package httpsdk carries the boilerplate for a plugin that serves its own
// HTTP API: an OpenAPI-documented service wired to the host's error envelope,
// the adapter that answers a proxied request, and the permission middleware.
// It is separate from pkg/plugin/sdk because it pulls the whole OpenAPI
// toolchain in, and a plugin that only contributes nodes, a remote agent in
// particular, has no API to serve.
package httpsdk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/swaggest/openapi-go/openapi3"
	"github.com/swaggest/rest"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/plugin"
)

// NewService builds an OpenAPI-documented HTTP service for a plugin's own
// API, wired to the same error envelope the host uses so a plugin's failures
// read like every other endpoint's.
func NewService(title, version, description string) *web.Service {
	reflector := openapi3.NewReflector()
	reflector.Spec.Info.Title = title
	reflector.Spec.Info.Version = version
	reflector.Spec.Info.WithDescription(description)

	s := web.NewService(reflector)
	s.Wrap(nethttp.OptionsMiddleware(func(h *nethttp.Handler) {
		h.HandleErrResponse = func(w http.ResponseWriter, r *http.Request, err error) {
			var appErr *apperrors.AppError
			if errors.As(err, &appErr) {
				apiutil.WriteErrorResponse(w, appErr)
				return
			}
			code, _ := rest.Err(err)
			apiutil.WriteErrorResponse(w, &apperrors.AppError{
				Code:       "INTERNAL_ERROR",
				Message:    err.Error(),
				StatusCode: code,
			})
		}
	}))
	return s
}

// ServeHTTP routes one proxied request through a plugin's service. Every
// HTTP plugin needs the same three steps — rebuild the request, give chi a
// fresh routing context, capture the response — so they live here rather than
// in each plugin.
func ServeHTTP(svc *web.Service, req *plugin.HTTPRequestParams) (*plugin.HTTPResponseResult, error) {
	r, err := apiutil.PluginHTTPRequest(req.Method, req.Path, req.QueryString,
		req.RemoteAddr, req.UserID, req.Username, req.Headers, req.Body, req.DashboardGrant)
	if err != nil {
		return nil, err
	}
	if req.KeyLimited {
		r = apiutil.WithPluginKeyLimit(r, req.KeyPermissions)
	}

	// chi stores routing state on the context and requires a fresh one per
	// request; reusing it silently misroutes.
	rctx := chi.NewRouteContext()
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	w := apiutil.NewPluginResponseWriter()
	svc.ServeHTTP(w, r)
	code, headers, body := w.Result()

	return &plugin.HTTPResponseResult{StatusCode: code, Headers: headers, Body: body}, nil
}

// OpenAPISpec serializes a service's OpenAPI document, empty when the service
// does not expose a reflector the host can read.
func OpenAPISpec(svc *web.Service) string {
	if svc == nil {
		return ""
	}
	refl, ok := svc.OpenAPIReflector().(*openapi3.Reflector)
	if !ok {
		return ""
	}
	spec, err := json.Marshal(refl.Spec)
	if err != nil {
		return ""
	}
	return string(spec)
}

// RequirePermission gates a plugin route behind a host permission. The host
// already authenticated the caller, so this asks it whether that identity
// holds the permission; an anonymous request never does.
func RequirePermission(host plugin.HostAPI, permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// A request the host authorized through a dashboard is already
			// checked: it reached a route this plugin's manifest declares its
			// widgets call, from someone who may view a dashboard holding one.
			// Asking for a permission on top of that would refuse every viewer,
			// which is the whole reason the grant exists. A route keyed by
			// dashboard needs more than this and should read the grant itself.
			if apiutil.PluginDashboardGrant(r) != "" {
				next.ServeHTTP(w, r)
				return
			}
			userID := apiutil.PluginUserID(r)
			if userID == "" {
				apiutil.WriteErrorResponse(w,
					apperrors.ErrUnauthorized.WithMessage("authentication required"))
				return
			}
			if !Allowed(host, r, permission) {
				apiutil.WriteErrorResponse(w,
					apperrors.ErrForbidden.WithMessage("insufficient permissions"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Allowed reports whether a proxied request's caller may use a host
// permission: the host answers for the user, and an API key limited to a set
// of permissions must also list it. A route checking a permission inside its
// handler asks this rather than the host alone.
func Allowed(host plugin.HostAPI, r *http.Request, permission string) bool {
	userID := apiutil.PluginUserID(r)
	if userID == "" {
		return false
	}
	if limit, ok := apiutil.PluginKeyLimit(r); ok && !keyAllows(limit, permission) {
		return false
	}
	allowed, err := host.CheckPermission(r.Context(), userID, permission)
	return err == nil && allowed
}

// keyAllows reports whether a limited key's permissions cover one required
// permission.
func keyAllows(limit []string, required string) bool {
	for _, held := range limit {
		if permissions.MatchPermission(held, required) {
			return true
		}
	}
	return false
}
