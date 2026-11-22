package api

import (
	"context"
	"net/http"

	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/connops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	aiintegration "github.com/grmrgecko/karakuri/pkg/integrations/ai"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// ConnectionListHandler returns the interactor that lists connections with
// masked configs and live statuses.
func ConnectionListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in struct{}, out *apiutil.Success[[]*connops.ConnectionItem]) error {
		items, err := connops.ListFor(ctx, callerID(ctx))
		if err != nil {
			return err
		}
		out.Data = items
		return nil
	})
	u.SetTitle("List Connections")
	u.SetDescription("Returns the connections the caller may use, with masked configuration, live status, and the caller's access level.")
	u.SetTags("Connections")
	return u
}

// ConnectionGetInput identifies a connection by UUID.
type ConnectionGetInput struct {
	ID string `path:"id" format:"uuid" description:"Connection UUID."`
}

// ConnectionGetHandler returns the interactor that retrieves one connection.
func ConnectionGetHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ConnectionGetInput, out *apiutil.Success[*connops.ConnectionItem]) error {
		c, err := access.Connection(ctx, callerID(ctx), in.ID, access.Use)
		if err != nil {
			return err
		}
		item, err := connops.ItemFor(ctx, callerID(ctx), c)
		if err != nil {
			return err
		}
		out.Data = item
		return nil
	})
	u.SetTitle("Get Connection")
	u.SetDescription("Returns a single connection with masked configuration and live status.")
	u.SetTags("Connections")
	return u
}

// ConnectionCreateInput holds fields for creating a connection.
type ConnectionCreateInput struct {
	Name    string         `json:"name" required:"true" minLength:"1" description:"Display name."`
	Kind    string         `json:"kind" required:"true" description:"Driver kind, e.g. mqtt."`
	Config  map[string]any `json:"config" description:"Driver configuration."`
	Enabled *bool          `json:"enabled,omitempty" description:"Whether to start the connection; defaults to true."`
}

// ConnectionCreateHandler returns the interactor that creates a connection
// and starts its driver when enabled.
func ConnectionCreateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ConnectionCreateInput, out *apiutil.Success[*connops.ConnectionItem]) error {
		// Verify authentication.
		if middleware.GetUser(ctx) == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Validate, persist, and start when enabled.
		c, err := connops.Create(ctx, callerID(ctx), in.Name, in.Kind, in.Config, in.Enabled == nil || *in.Enabled)
		if err != nil {
			return err
		}

		item, err := connops.ItemFor(ctx, callerID(ctx), c)
		if err != nil {
			return err
		}
		out.Data = item
		out.Message = "Connection created"
		return nil
	})
	u.SetTitle("Create Connection")
	u.SetDescription("Creates a connection with encrypted configuration and starts it when enabled.")
	u.SetTags("Connections")
	return u
}

// ConnectionUpdateInput holds the connection ID and optional patch fields.
type ConnectionUpdateInput struct {
	ID      string          `path:"id" format:"uuid" description:"Connection UUID."`
	Name    *string         `json:"name,omitempty" minLength:"1" description:"New display name."`
	Config  *map[string]any `json:"config,omitempty" description:"New driver configuration; masked secrets keep the stored value."`
	Enabled *bool           `json:"enabled,omitempty" description:"Whether the connection should be running."`
}

// ConnectionUpdateHandler returns the interactor that patches a connection
// and hot-applies the change to its driver.
func ConnectionUpdateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ConnectionUpdateInput, out *apiutil.Success[*connops.ConnectionItem]) error {
		c, err := access.Connection(ctx, callerID(ctx), in.ID, access.Edit)
		if err != nil {
			return err
		}
		if in.Config != nil {
			if err := connops.CheckConfigConnections(ctx, callerID(ctx), c.Kind, *in.Config); err != nil {
				return err
			}
		}

		// Apply the patch and hot-apply the driver change, then rearm
		// dependent flow triggers so they bind to the new driver instance.
		if err := connops.Update(ctx, c, connops.Patch{Name: in.Name, Config: in.Config, Enabled: in.Enabled}); err != nil {
			return err
		}
		flowops.RearmDependents(c.ID)

		item, err := connops.ItemFor(ctx, callerID(ctx), c)
		if err != nil {
			return err
		}
		out.Data = item
		out.Message = "Connection updated"
		return nil
	})
	u.SetTitle("Update Connection")
	u.SetDescription("Patches a connection and hot-applies the change. Masked secret values keep the stored secret.")
	u.SetTags("Connections")
	return u
}

// ConnectionDeleteHandler returns the interactor that removes a connection.
// Deletion is blocked while any flow graph references the connection.
func ConnectionDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ConnectionGetInput, out *apiutil.Success[any]) error {
		c, err := access.Connection(ctx, callerID(ctx), in.ID, access.Admin)
		if err != nil {
			return err
		}
		if err := connops.Delete(ctx, c); err != nil {
			return err
		}
		out.Message = "Connection deleted"
		return nil
	})
	u.SetTitle("Delete Connection")
	u.SetDescription("Deletes a connection. Blocked while any flow references it.")
	u.SetTags("Connections")
	return u
}

// ConnectionTestHandler returns the interactor that performs a kind-specific
// connectivity test with a throwaway client against the stored configuration.
func ConnectionTestHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ConnectionGetInput, out *apiutil.Success[connops.TestResult]) error {
		c, err := access.Connection(ctx, callerID(ctx), in.ID, access.Edit)
		if err != nil {
			return err
		}
		result, err := connops.Test(ctx, c)
		if err != nil {
			return err
		}
		out.Data = result
		return nil
	})
	u.SetTitle("Test Connection")
	u.SetDescription("Attempts a short-lived connect/disconnect with a throwaway client and reports the result.")
	u.SetTags("Connections")
	return u
}

// ConnectionKindsHandler returns the interactor that lists registered
// connection kinds. The settings UI builds its kind picker and config forms
// from this, so a plugin's kind appears without any frontend change.
func ConnectionKindsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in struct{}, out *apiutil.Success[[]connops.KindItem]) error {
		out.Data = connops.Kinds()
		return nil
	})
	u.SetTitle("List Connection Kinds")
	u.SetDescription("Returns the registered connection kinds with their config schemas.")
	u.SetTags("Connections")
	return u
}

// CodexLoginStartHandler starts OpenAI's browser-based device login.
func CodexLoginStartHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in struct{}, out *apiutil.Success[aiintegration.CodexDeviceLogin]) error {
		login, err := aiintegration.StartCodexDeviceLogin(ctx)
		if err != nil {
			return apperrors.ErrBadGateway.WithMessage(err.Error())
		}
		out.Data = login
		return nil
	})
	u.SetTitle("Start Codex Login")
	u.SetDescription("Starts OpenAI device authorization for a ChatGPT-backed Codex connection.")
	u.SetTags("Connections")
	return u
}

// CodexLoginPollInput identifies an OpenAI device authorization in progress.
type CodexLoginPollInput struct {
	DeviceAuthID string `json:"device_auth_id" required:"true" description:"OpenAI device authorization identifier."`
	UserCode     string `json:"user_code" required:"true" description:"Short code shown on the OpenAI login page."`
}

// CodexLoginPollHandler finishes an approved OpenAI device login.
func CodexLoginPollHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in CodexLoginPollInput, out *apiutil.Success[aiintegration.CodexDeviceLoginResult]) error {
		result, err := aiintegration.PollCodexDeviceLogin(ctx, in.DeviceAuthID, in.UserCode)
		if err != nil {
			return apperrors.ErrBadGateway.WithMessage(err.Error())
		}
		out.Data = result
		return nil
	})
	u.SetTitle("Poll Codex Login")
	u.SetDescription("Checks an OpenAI device authorization and returns the approved credentials.")
	u.SetTags("Connections")
	return u
}

// RegisterConnectionRoutes registers connection management endpoints.
func RegisterConnectionRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsRead), bearerDoc).
		Method(http.MethodGet, "/connections/kinds",
			nethttp.NewHandler(ConnectionKindsHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsRead), bearerDoc).
		Method(http.MethodGet, "/connections",
			nethttp.NewHandler(ConnectionListHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodPost, "/connections",
			nethttp.NewHandler(ConnectionCreateHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsRead), bearerDoc).
		Method(http.MethodGet, "/connections/{id}",
			nethttp.NewHandler(ConnectionGetHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodPut, "/connections/{id}",
			nethttp.NewHandler(ConnectionUpdateHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodDelete, "/connections/{id}",
			nethttp.NewHandler(ConnectionDeleteHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodPost, "/connections/{id}/test",
			nethttp.NewHandler(ConnectionTestHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodPost, "/connections/ai/codex/auth/start",
			nethttp.NewHandler(CodexLoginStartHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodPost, "/connections/ai/codex/auth/poll",
			nethttp.NewHandler(CodexLoginPollHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsRead), requireConnection(access.Use), bearerDoc).
		Method(http.MethodGet, "/connections/{id}/ha/entities",
			nethttp.NewHandler(HAEntitiesHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsRead), requireConnection(access.Use), bearerDoc).
		Method(http.MethodGet, "/connections/{id}/ha/registry",
			nethttp.NewHandler(HARegistryHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsRead), requireConnection(access.Use), bearerDoc).
		Method(http.MethodGet, "/connections/{id}/openhab/items",
			nethttp.NewHandler(OpenHABItemsHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsRead), requireConnection(access.Use), bearerDoc).
		Method(http.MethodGet, "/connections/{id}/ai/models",
			nethttp.NewHandler(AIModelsHandler()))
	// Entity state and service calls are the dashboard's read and control
	// paths, so they are optionally authenticated and the handler decides:
	// connection read or invoke covers the connection, and anything else has
	// to be exposed by a dashboard the caller may view.
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/connections/{id}/ha/states",
			nethttp.NewHandler(HAStatesHandler()))
	s.With(middleware.OptionalAuth()).
		Method(http.MethodPost, "/connections/{id}/ha/service",
			nethttp.NewHandler(HACallServiceHandler()))
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/connections/{id}/ha/history/{entity}",
			nethttp.NewHandler(HAHistoryHandler()))
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/connections/{id}/ha/todo/{entity}",
			nethttp.NewHandler(HATodoHandler()))
	s.With(middleware.Auth(), requireConnection(access.Use), bearerDoc).
		Method(http.MethodGet, "/connections/{id}/nvr/cameras",
			nethttp.NewHandler(NVRCamerasHandler()))
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/connections/{id}/nvr/events",
			nethttp.NewHandler(NVREventsHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsInvoke), requireConnection(access.Use), bearerDoc).
		Method(http.MethodPost, "/connections/{id}/mqtt/publish",
			nethttp.NewHandler(MQTTPublishHandler()))
}
