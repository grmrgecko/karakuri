package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/dashscope"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/plugin"
	"github.com/grmrgecko/karakuri/pkg/pluginhost"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// pluginHost and pluginManager are wired by the server at startup. Both are
// nil in tests that never touch plugins; handlers then report an empty set
// rather than failing.
var (
	pluginHost    *pluginhost.Host
	pluginManager *pluginhost.Manager
)

// SetPluginHost installs the plugin host used by handlers.
func SetPluginHost(h *pluginhost.Host) { pluginHost = h }

// SetPluginManager installs the plugin manager used by handlers.
func SetPluginManager(m *pluginhost.Manager) { pluginManager = m }

// PluginListHandler returns the interactor that lists every plugin: the
// built-ins compiled into the binary, and each one discovered on disk with its
// stored enable choice and last load result.
func PluginListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in struct{}, out *apiutil.Success[[]pluginhost.Item]) error {
		out.Data = pluginhost.Inventory(pluginHost, pluginManager)
		return nil
	})
	u.SetTitle("List Plugins")
	u.SetDescription("Returns every plugin with its load state and contributions.")
	u.SetTags("Plugins")
	return u
}

// PluginNameInput identifies a plugin by name.
type PluginNameInput struct {
	Name string `path:"name" description:"Plugin name."`
}

// PluginSettingsData carries a plugin's settings schema alongside the stored
// values, so the settings form renders and populates in one request.
type PluginSettingsData struct {
	Schema string            `json:"schema" description:"JSON Schema describing the settings."`
	Values map[string]string `json:"values" description:"Stored values by setting key."`
}

// PluginGetSettingsHandler returns the interactor that reads a plugin's
// settings schema and current values.
func PluginGetSettingsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in PluginNameInput, out *apiutil.Success[PluginSettingsData]) error {
		if pluginManager == nil {
			return apperrors.ErrNotFound.WithMessage("plugin not found")
		}

		// Read the schema from the running plugin, and the values from the
		// store — a plugin that is not loaded still has settings to edit.
		prefix := "plugins." + in.Name + "."
		settings, err := models.ListSettingsByPrefix(prefix)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		values := make(map[string]string, len(settings))
		for _, s := range settings {
			values[strings.TrimPrefix(s.Key, prefix)] = s.Value
		}

		out.Data = PluginSettingsData{
			Schema: pluginManager.SettingsSchema(in.Name),
			Values: values,
		}
		return nil
	})
	u.SetTitle("Get Plugin Settings")
	u.SetDescription("Returns a plugin's settings schema and stored values.")
	u.SetTags("Plugins")
	return u
}

// PluginUpdateSettingsInput carries the values to store for one plugin.
type PluginUpdateSettingsInput struct {
	Name   string            `path:"name" description:"Plugin name."`
	Values map[string]string `json:"values" required:"true" description:"Setting values by key."`
}

// PluginUpdateSettingsHandler returns the interactor that stores a plugin's
// settings and reloads it. A plugin reads its configuration once, at Init, so
// saving without reloading would leave the running process on stale values.
func PluginUpdateSettingsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in PluginUpdateSettingsInput, out *apiutil.Success[struct{}]) error {
		// Persist the values.
		for key, value := range in.Values {
			if err := models.SetSetting("plugins."+in.Name+"."+key, value); err != nil {
				return apperrors.ErrInternal.WithError(err)
			}
		}

		// Reload so the plugin picks them up.
		if pluginManager != nil && pluginManager.IsLoaded(in.Name) {
			if err := pluginManager.ReloadPlugin(in.Name); err != nil {
				return apperrors.ErrInternal.WithMessage("settings saved but reload failed: " + err.Error())
			}
		}

		out.Message = "Settings saved"
		return nil
	})
	u.SetTitle("Update Plugin Settings")
	u.SetDescription("Stores a plugin's settings and reloads it so they take effect.")
	u.SetTags("Plugins")
	return u
}

// pluginAction builds an interactor around one lifecycle operation, since load,
// unload, reload, enable, and disable differ only in what they call and say.
func pluginAction(title, description, message string, act func(name string) error) usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in PluginNameInput, out *apiutil.Success[struct{}]) error {
		if pluginManager == nil {
			return apperrors.ErrNotFound.WithMessage("plugin not found")
		}
		if err := act(in.Name); err != nil {
			return apperrors.ErrValidation.WithMessage(err.Error())
		}
		out.Message = message
		return nil
	})
	u.SetTitle(title)
	u.SetDescription(description)
	u.SetTags("Plugins")
	return u
}

// PluginProxyHandler forwards a request to a loaded plugin's own API. The
// path suffix after /api/plugins/{name} is what the plugin routes against, so
// it never has to know where it was mounted.
// maxPluginBody bounds a request body forwarded to a plugin route.
const maxPluginBody = 16 << 20

func PluginProxyHandler(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if pluginManager == nil {
		apiutil.WriteErrorResponse(w, apperrors.ErrServiceUnavailable.WithMessage("plugin not loaded"))
		return
	}

	// The route past here is whatever the plugin wrote, reachable by
	// anonymous callers, so the body is bounded here rather than trusting each
	// plugin to bound it.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPluginBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			apiutil.WriteErrorResponse(w, apperrors.ErrValidation.WithMessage("request body exceeds 16 MiB"))
			return
		}
		apiutil.WriteErrorResponse(w, apperrors.ErrInternal.WithMessage("failed to read request body"))
		return
	}

	headers := make(map[string]string, len(r.Header))
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	// Identity is resolved here so the plugin can trust it; anonymous
	// requests arrive with both fields empty.
	var userID, username string
	if uc := middleware.GetUser(r.Context()); uc != nil {
		userID = uc.UserID.String()
		username = uc.Username
	}

	keyPermissions, keyLimited := auth.KeyLimit(r.Context())

	path := strings.TrimPrefix(r.URL.Path, "/api/plugins/"+name)
	if path == "" {
		path = "/"
	}

	// A dashboard viewer holds no plugin permission, so the host decides here
	// whether this request is one the dashboard authorizes: the plugin's own
	// manifest names the routes its widgets call, and the caller has to be able
	// to view a dashboard that holds one of them.
	var grant string
	if scope := dashscope.FromContext(r.Context()); scope.AllowsPluginAPI(name, r.Method, path) {
		grant = scope.DashboardID
	}

	resp, loaded, err := pluginManager.HandleHTTP(r.Context(), name, &plugin.HTTPRequestParams{
		Method:         r.Method,
		Path:           path,
		Headers:        headers,
		Body:           body,
		QueryString:    r.URL.RawQuery,
		RemoteAddr:     r.RemoteAddr,
		UserID:         userID,
		Username:       username,
		DashboardGrant: grant,
		KeyLimited:     keyLimited,
		KeyPermissions: keyPermissions,
	})
	if !loaded {
		apiutil.WriteErrorResponse(w, apperrors.ErrServiceUnavailable.WithMessage("plugin not loaded"))
		return
	}
	if err != nil {
		apiutil.WriteErrorResponse(w, apperrors.ErrBadGateway.WithMessage("plugin request failed"))
		return
	}

	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(resp.Body)
}

// PortTypeListHandler returns the interactor that lists registered port
// types. The editor derives labels, tooltips, value-parameter eligibility,
// and rotating-hue colors from this list, so a plugin's type renders without
// any frontend change. Order is registration order and is load-bearing for
// the colors.
func PortTypeListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in struct{}, out *apiutil.Success[[]flow.PortTypeDef]) error {
		out.Data = flow.PortTypes()
		return nil
	})
	u.SetTitle("List Port Types")
	u.SetDescription("Returns the registered payload port types in registration order.")
	u.SetTags("NodeTypes")
	return u
}

// FieldControlListHandler returns the interactor that lists registered config
// field controls, so the editor knows each control's fallback base type.
func FieldControlListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in struct{}, out *apiutil.Success[[]flow.FieldControlDef]) error {
		out.Data = flow.FieldControls()
		return nil
	})
	u.SetTitle("List Field Controls")
	u.SetDescription("Returns the registered config field controls.")
	u.SetTags("NodeTypes")
	return u
}

// RegisterPluginRoutes registers plugin management, the plugin API proxy, and
// the editor catalog endpoints.
func RegisterPluginRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	// The editor catalogs are gated on flow read, not plugin read: a flow
	// author needs port types and controls without any say over plugins.
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsRead), bearerDoc).
		Method(http.MethodGet, "/nodetypes/porttypes",
			nethttp.NewHandler(PortTypeListHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsRead), bearerDoc).
		Method(http.MethodGet, "/nodetypes/controls",
			nethttp.NewHandler(FieldControlListHandler()))

	// Plugin management.
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermPluginsRead), bearerDoc).
		Method(http.MethodGet, "/plugins",
			nethttp.NewHandler(PluginListHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermPluginsRead), bearerDoc).
		Method(http.MethodGet, "/plugins/{name}/settings",
			nethttp.NewHandler(PluginGetSettingsHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermPluginsWrite), bearerDoc).
		Method(http.MethodPut, "/plugins/{name}/settings",
			nethttp.NewHandler(PluginUpdateSettingsHandler()))

	write := s.With(middleware.Auth(), middleware.RequirePermission(perm.PermPluginsWrite), bearerDoc)
	write.Method(http.MethodPost, "/plugins/{name}/load",
		nethttp.NewHandler(pluginAction("Load Plugin",
			"Applies a plugin's contributions.", "Plugin loaded",
			func(name string) error { return pluginManager.LoadPlugin(name) })))
	write.Method(http.MethodPost, "/plugins/{name}/unload",
		nethttp.NewHandler(pluginAction("Unload Plugin",
			"Withdraws a plugin's contributions.", "Plugin unloaded",
			func(name string) error { return pluginManager.UnloadPlugin(name) })))
	write.Method(http.MethodPost, "/plugins/{name}/reload",
		nethttp.NewHandler(pluginAction("Reload Plugin",
			"Unloads and loads a plugin.", "Plugin reloaded",
			func(name string) error { return pluginManager.ReloadPlugin(name) })))
	write.Method(http.MethodPost, "/plugins/{name}/enable",
		nethttp.NewHandler(pluginAction("Enable Plugin",
			"Loads a plugin and remembers it for the next start.", "Plugin enabled",
			func(name string) error { return pluginManager.SetEnabled(name, true) })))
	write.Method(http.MethodPost, "/plugins/{name}/disable",
		nethttp.NewHandler(pluginAction("Disable Plugin",
			"Unloads a plugin and keeps it unloaded at the next start.", "Plugin disabled",
			func(name string) error { return pluginManager.SetEnabled(name, false) })))

	// The plugin's own API, mounted after the management routes so those win
	// over this catch-all. Authentication is optional: a plugin decides for
	// itself which of its routes anonymous viewers may reach.
	s.Route("/plugins/{name}", func(pr chi.Router) {
		pr.Use(middleware.OptionalAuth())
		pr.HandleFunc("/*", PluginProxyHandler)
	})
}
