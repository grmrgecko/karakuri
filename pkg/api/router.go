package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/browserprompt"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/mcpserver"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/uploads"
	"github.com/swaggest/openapi-go/openapi3"
	"github.com/swaggest/rest"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/swgui"
	swguiemb "github.com/swaggest/swgui/v5emb"
	"go.uber.org/zap"
)

// RegisterRoutes registers all API routes on the provided router.
func RegisterRoutes(r chi.Router, log *zap.Logger) {
	RegisterBrowserPromptRoutes(r, browserprompt.Default)
	// Build the OpenAPI-documented API service.
	reflector := openapi3.NewReflector()
	reflector.Spec.Info.Title = "Karakuri API"
	reflector.Spec.Info.Version = "1.0.0"
	reflector.Spec.Info.WithDescription("Home automation flow engine and dashboard API.")
	reflector.Spec.WithServers(openapi3.Server{URL: "/api"})

	apiService := web.NewService(reflector)

	// Administrative changes are recorded in the security log once they
	// succeed, whichever handler serves them.
	apiService.Use(Audit("/api"))

	// Custom error response handler that encodes AppError directly.
	apiService.Wrap(nethttp.OptionsMiddleware(func(h *nethttp.Handler) {
		h.HandleErrResponse = func(w http.ResponseWriter, r *http.Request, err error) {
			var appErr *apperrors.AppError
			if errors.As(err, &appErr) {
				apiutil.WriteErrorResponse(w, appErr)
				return
			}
			code, errResp := rest.Err(err)
			errCode := "INTERNAL_ERROR"
			if code == http.StatusBadRequest {
				errCode = "VALIDATION_ERROR"
			}
			validationErr := &apperrors.AppError{
				Code:       errCode,
				Message:    err.Error(),
				StatusCode: code,
			}
			// Carry the request validator's per-field context through, so a
			// rejected body names the offending fields instead of reporting a
			// bare "validation failed".
			if len(errResp.Context) > 0 {
				details := make(map[string]string, len(errResp.Context))
				for field, reason := range errResp.Context {
					details[field] = fmt.Sprint(reason)
				}
				validationErr.Details = details
			}
			apiutil.WriteErrorResponse(w, validationErr)
		}
	}))

	// Security scheme annotation for authenticated endpoints.
	bearerDoc := nethttp.HTTPBearerSecurityMiddleware(
		apiService.OpenAPICollector, "bearerAuth", "JWT access token", "JWT",
	)

	// Per-resource route registration.
	RegisterHealthRoutes(apiService, bearerDoc)
	RegisterAuthRoutes(apiService, bearerDoc, log)
	RegisterUserRoutes(apiService, bearerDoc)
	RegisterGroupRoutes(apiService, bearerDoc)
	RegisterRoleRoutes(apiService, bearerDoc)
	RegisterAPIKeyRoutes(apiService, bearerDoc)
	RegisterFolderRoutes(apiService, bearerDoc)
	RegisterFlowRoutes(apiService, bearerDoc)
	RegisterFlowShareRoutes(apiService, bearerDoc)
	RegisterResourceShareRoutes(apiService, bearerDoc)
	RegisterSlugRoutes(apiService, bearerDoc)
	RegisterFaceplateRoutes(apiService, bearerDoc)
	RegisterRunRoutes(apiService, bearerDoc)
	RegisterFlowWaitRoutes(apiService, bearerDoc)
	RegisterHomeRoutes(apiService, bearerDoc)
	RegisterNodeChatRoutes(apiService, bearerDoc)
	RegisterWidgetTriggerRoutes(apiService, bearerDoc)
	RegisterNodeTypeRoutes(apiService, bearerDoc)
	RegisterPluginRoutes(apiService, bearerDoc)
	RegisterAgentRoutes(apiService, bearerDoc)
	RegisterProviderRoutes(apiService, bearerDoc)
	RegisterGuideRoutes(apiService, bearerDoc)
	RegisterCronRoutes(apiService, bearerDoc)
	RegisterConnectionRoutes(apiService, bearerDoc)
	RegisterTimeSeriesRoutes(apiService, bearerDoc)
	RegisterNotificationRoutes(apiService, bearerDoc)
	RegisterControlRoutes(apiService, bearerDoc)
	RegisterMemoryRoutes(apiService, bearerDoc)
	RegisterMQTTDeviceRoutes(apiService, bearerDoc)
	RegisterScriptRoutes(apiService, bearerDoc)
	RegisterDashboardRoutes(apiService, bearerDoc)
	RegisterDashboardDisplayRoutes(apiService, bearerDoc)
	RegisterDashboardGroupRoutes(apiService, bearerDoc)
	RegisterShareRoutes(apiService, bearerDoc)
	RegisterWidgetRoutes(apiService, bearerDoc)
	RegisterRenderRoutes(apiService, bearerDoc)
	RegisterThemeRoutes(apiService, bearerDoc)
	RegisterBackupRoutes(apiService, bearerDoc)
	RegisterEventRoutes(apiService, bearerDoc)

	apiService.With(middleware.Auth()).Method(http.MethodGet, "/assistant/connections", mcpserver.AssistantModelsHandler())
	apiService.With(middleware.Auth()).Method(http.MethodPost, "/assistant/chat", mcpserver.AssistantHandler())

	// Catch-all for unmatched API routes.
	apiService.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		apiutil.WriteErrorResponse(w, apperrors.ErrNotFound)
	})

	// Mount the API service under /api. Every request carries the dashboard it
	// was made from, if any: a widget holds no permissions and asks for the
	// data its config names, so the claim has to reach the handlers that
	// resolve it (see pkg/dashscope). It rides here rather than in the outer
	// server so mounting the API is enough to have it.
	r.Mount("/api", middleware.DashboardContext()(apiService))

	// OpenAPI spec and swagger UI.
	r.Get("/api/docs/openapi.json", OpenAPIHandler(apiService, log))
	docsHandler := swguiemb.NewWithConfig(swgui.Config{
		SettingsUI: map[string]string{
			"requestInterceptor": `function(req) {
				var token = localStorage.getItem("access_token");
				if (token) { req.headers["Authorization"] = "Bearer " + token; }
				return req;
			}`,
		},
	})
	r.Mount("/api/docs", docsHandler("Karakuri API", "/api/docs/openapi.json", "/api/docs"))

	// Public render URLs. They live outside /api because what points at them is
	// an <img> tag, a wiki page, or a photo frame, none of which can set a
	// header: the dashboard's own access decides, and a token rides the query
	// string. Optional authentication is what lets an API key work there, and
	// these are the only routes a dashboard render key opens.
	r.Group(func(rr chi.Router) {
		rr.Use(middleware.RenderURLAuth())
		rr.Get("/dashboard/{id}/render/job/{jobSpec}", RenderJobResultHandler)
		rr.Get("/dashboard/{id}/render/schedule/{schedSpec}", RenderScheduleResultHandler)
		rr.Get("/dashboard/{id}/render/{spec}", RenderDirectHandler)
	})

	// Home Assistant and NVR images and camera streams are binary, so they are plain
	// handlers. They carry the same dashboard claim the API service reads.
	r.Group(func(hr chi.Router) {
		hr.Use(middleware.DashboardContext(), middleware.OptionalAuth())
		hr.Get("/api/connections/{id}/ha/camera/{entity}/snapshot", HACameraSnapshotHandler)
		hr.Get("/api/connections/{id}/ha/camera/{entity}/stream", HACameraStreamHandler)
		hr.Get("/api/connections/{id}/ha/media/{entity}/art", HAMediaArtHandler)
		hr.Get("/api/connections/{id}/nvr/cameras/{camera}/snapshot", NVRSnapshotHandler)
		hr.Get("/api/connections/{id}/nvr/cameras/{camera}/stream", NVRStreamHandler)
		hr.Get("/api/connections/{id}/nvr/events/{event}/{media}", NVREventMediaHandler)
	})

	// Archive downloads are the file itself, so they are a plain handler.
	r.With(middleware.Auth(), middleware.RequirePermission(perm.PermBackupsDownload)).
		Get("/api/backups/{name}/download", BackupDownloadHandler)

	// Widget file upload stays as a plain handler (multipart form processing).
	r.Group(func(ur chi.Router) {
		ur.Use(middleware.Auth())
		ur.Post("/api/dashboards/{id}/widgets/{widgetId}/uploads", uploads.Upload)
	})
}

// OpenAPIHandler returns an HTTP handler that serializes the OpenAPI spec as JSON.
func OpenAPIHandler(apiService *web.Service, log *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		refl, ok := apiService.OpenAPIReflector().(*openapi3.Reflector)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		specJSON, err := json.MarshalIndent(refl.Spec, "", "  ")
		if err != nil {
			log.Error("failed to marshal OpenAPI spec", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(specJSON)
	}
}
