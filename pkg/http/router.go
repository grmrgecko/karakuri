package http

import (
	nethttp "net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/resources"
	"github.com/grmrgecko/karakuri/pkg/api"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/mcpserver"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/mq"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/uploads"
	"github.com/grmrgecko/karakuri/pkg/widgets"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

// newRouter builds the top-level HTTP handler with global middleware, API routes,
// and static file serving.
func newRouter(log *zap.Logger) nethttp.Handler {
	r := chi.NewRouter()

	if config.C.Server.TrustProxy {
		nets := parseTrusted(config.C.Server.TrustedProxies, log)
		log.Info("accepting forwarding headers from trusted proxies", zap.String("trusted_proxies", config.C.Server.TrustedProxies))
		r.Use(trustedRealIP(nets))
	}
	r.Use(middleware.RequestID)
	r.Use(middleware.Logging(log))
	r.Use(middleware.Recovery(log))
	r.Use(middleware.SecurityHeaders)
	r.Use(middleware.InjectRequestMeta())
	r.Use(recheckSockets)
	r.Use(corsHandler(config.C.Server.CORSOrigins))

	// API routes.
	api.RegisterRoutes(r, log)

	// Top-level health check for load balancers and uptime probes.
	r.Get("/health", api.HealthHandler)

	// Prometheus metrics: standard Go/process collectors plus the karakuri
	// gauges and counters from pkg/metrics.
	r.Handle("/metrics", promhttp.Handler())

	// WebSocket endpoint.
	r.Get("/ws", mq.WS.HandleWebSocket)

	// The MCP endpoint: the same bearer credentials as /api, gated on
	// api:mcp:use so an administrator decides which groups may drive the
	// server over MCP at all. Each tool then checks its own api:* permission.
	if config.C.MCP.Enabled {
		r.Group(func(mr chi.Router) {
			mr.Use(middleware.Auth())
			mr.Use(middleware.RequirePermission(perm.PermMCPUse))
			mr.Use(middleware.Via(events.ViaMCP))
			mr.Handle("/mcp", mcpserver.Handler())
		})
	}

	// Webhook trigger endpoint. Lives outside /api and carries no auth: the
	// token is the credential. Armed webhook nodes register tokens on the
	// engine's webhook registry.
	mountWebhooks(r, engine.Webhooks())

	// Dashboard widget uploads, served by upload ID to whoever may view the
	// placement. Media elements send the media cookie, not a header.
	r.With(uploads.CookieCredential, middleware.OptionalAuth()).
		Get("/widget/{dashboardWidgetId}/{uploadId}/{filename}", uploads.ServeFile)

	// Theme and widget packages are public static assets: theme CSS and widget
	// bundles must load before authentication on shared dashboard views.
	if paths := resources.GetResourcePaths(resources.Themes); len(paths) > 0 {
		fs := nethttp.FileServer(nethttp.Dir(paths[0]))
		r.Handle("/themes/*", nethttp.StripPrefix("/themes/", fs))
	}
	// Widget bundles live inside their contributing plugin, so the package
	// name resolves through the registry rather than one directory root. A
	// package whose plugin is unloaded is not served.
	r.Handle("/widgets/{package}/*", widgetAssetHandler())

	// The web interface is embedded in the binary.
	r.Handle("/*", frontendHandler(log, embeddedFrontend()))

	return r
}

// corsHandler builds the CORS middleware from server.cors_origins, a
// comma-separated list of origins. A setting that names none allows none: the
// list handed over is never empty, because the CORS layer reads an empty list
// as every origin.
func corsHandler(origins string) func(nethttp.Handler) nethttp.Handler {
	var allowed []string
	for _, origin := range strings.Split(origins, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			allowed = append(allowed, origin)
		}
	}
	if len(allowed) == 0 {
		allowed = []string{""}
	}
	return cors.Handler(cors.Options{
		AllowedOrigins:   allowed,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", middleware.DashboardHeader},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           300,
	})
}

// widgetAssetHandler serves a widget package's built bundle out of whichever
// plugin directory contributed it. An unregistered package — an unknown name,
// or one whose plugin is unloaded — is a 404, so disabling a plugin takes its
// assets off the wire without touching the files.
func widgetAssetHandler() nethttp.HandlerFunc {
	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		pkg := chi.URLParam(r, "package")
		dir, ok := widgets.Available(pkg)
		if !ok {
			nethttp.NotFound(w, r)
			return
		}
		// A widget bundle is rebuilt in place at a stable URL, so heuristic
		// freshness would keep serving the old one long after the file changed.
		// Requiring revalidation costs one conditional request per load and
		// still answers 304 from the file's modification time.
		w.Header().Set("Cache-Control", "no-cache")

		// Join against a cleaned absolute path so no request escapes the
		// package directory.
		rel := chi.URLParam(r, "*")
		nethttp.ServeFile(w, r, filepath.Join(dir, filepath.FromSlash(path.Clean("/"+rel))))
	}
}
