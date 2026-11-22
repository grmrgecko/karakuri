package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/plugin/sdk/httpsdk"
)

// ConnectionView is a calendar connection as the widget panel lists it: what
// it is called and what kind it is, nothing about how it signs in.
type ConnectionView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// handleConnections lists the running calendar connections a dashboard
// author can point a widget at.
func (p *CalendarPlugin) handleConnections(w http.ResponseWriter, r *http.Request) {
	userID := apiutil.PluginUserID(r)
	if userID == "" {
		apiutil.WriteErrorResponse(w, apperrors.ErrUnauthorized.WithMessage("authentication required"))
		return
	}
	if !httpsdk.Allowed(p.host, r, "api:dashboards:write") {
		apiutil.WriteErrorResponse(w, apperrors.ErrForbidden.WithMessage("insufficient permissions"))
		return
	}
	// Only the connections shared with the author are offered.
	views := []ConnectionView{}
	for _, d := range p.connections() {
		if ok, err := p.host.CheckConnectionAccess(r.Context(), userID, d.id.String()); err != nil || !ok {
			continue
		}
		views = append(views, ConnectionView{ID: d.id.String(), Name: d.name, Kind: d.kind})
	}
	sort.Slice(views, func(i, j int) bool { return strings.ToLower(views[i].Name) < strings.ToLower(views[j].Name) })
	apiutil.WriteJSON(w, http.StatusOK, apiutil.Success[[]ConnectionView]{Data: views})
}

// handleSetSource stores the calendars a dashboard widget shows. Each source
// names a calendar connection, so no credential is stored with the widget.
func (p *CalendarPlugin) handleSetSource(w http.ResponseWriter, r *http.Request) {
	dashboardID := chi.URLParam(r, "dashboardID")
	widgetID := chi.URLParam(r, "widgetID")

	userID := apiutil.PluginUserID(r)
	if userID == "" {
		apiutil.WriteErrorResponse(w, apperrors.ErrUnauthorized.WithMessage("authentication required"))
		return
	}
	allowed, err := p.host.CheckDashboardAccess(r.Context(), userID, dashboardID, "edit")
	if err != nil || !allowed {
		apiutil.WriteErrorResponse(w, apperrors.ErrForbidden.WithMessage("insufficient permissions"))
		return
	}

	var sources []SourceConfig
	if err := json.NewDecoder(r.Body).Decode(&sources); err != nil {
		apiutil.WriteErrorResponse(w, apperrors.ErrValidation.WithMessage("invalid request body"))
		return
	}
	for i := range sources {
		sources[i].ConnectionID = strings.TrimSpace(sources[i].ConnectionID)
		// Every viewer reads the calendar through the source, so the author
		// must be able to use its connection.
		if ok, err := p.host.CheckConnectionAccess(r.Context(), userID, sources[i].ConnectionID); err != nil || !ok {
			apiutil.WriteErrorResponse(w, apperrors.ErrValidation.WithMessage(
				"source "+sources[i].Name+" names a calendar connection that is not shared with you; choose one from the list"))
			return
		}
		if _, err := p.driverFor(sources[i].ConnectionID); err != nil {
			apiutil.WriteErrorResponse(w, apperrors.ErrValidation.WithMessage(
				"source "+sources[i].Name+" names a calendar connection that is not running; choose one from the list"))
			return
		}
	}

	data, _ := json.Marshal(sources)
	if err := p.host.SetConfig(r.Context(), sourcesKey(dashboardID, widgetID), string(data)); err != nil {
		apiutil.WriteErrorResponse(w, apperrors.ErrInternal.WithMessage("failed to save sources"))
		return
	}
	apiutil.WriteJSON(w, http.StatusOK, apiutil.Success[any]{Message: "Sources saved"})
}

// handleGetSources lists a widget's sources for its settings panel. Sources
// live only in the plugin's store, never in the placement's config, because
// every viewer of a dashboard is served that config.
func (p *CalendarPlugin) handleGetSources(w http.ResponseWriter, r *http.Request) {
	dashboardID := chi.URLParam(r, "dashboardID")
	widgetID := chi.URLParam(r, "widgetID")

	userID := apiutil.PluginUserID(r)
	if userID == "" {
		apiutil.WriteErrorResponse(w, apperrors.ErrUnauthorized.WithMessage("authentication required"))
		return
	}
	allowed, err := p.host.CheckDashboardAccess(r.Context(), userID, dashboardID, "edit")
	if err != nil || !allowed {
		apiutil.WriteErrorResponse(w, apperrors.ErrForbidden.WithMessage("insufficient permissions"))
		return
	}

	sources := p.loadSources(r.Context(), dashboardID, widgetID)
	if sources == nil {
		sources = []SourceConfig{}
	}
	apiutil.WriteJSON(w, http.StatusOK, apiutil.Success[[]SourceConfig]{Data: sources})
}

// handleGetData returns merged event data for all sources of a dashboard widget.
func (p *CalendarPlugin) handleGetData(w http.ResponseWriter, r *http.Request) {
	p.serveData(w, r, "read", p.pollInterval)
}

// handleRefresh reads every source again, bypassing the drivers' caches.
func (p *CalendarPlugin) handleRefresh(w http.ResponseWriter, r *http.Request) {
	p.serveData(w, r, "edit", 0)
}

// serveData answers a widget's event request at the given access level,
// reusing calendar reads younger than maxAge.
func (p *CalendarPlugin) serveData(w http.ResponseWriter, r *http.Request, level string, maxAge time.Duration) {
	dashboardID := chi.URLParam(r, "dashboardID")
	widgetID := chi.URLParam(r, "widgetID")

	// A viewer the host authorized through this dashboard, such as a render
	// or an anonymous viewer of a public dashboard, may read its events. The
	// manifest names this placement in the route, so the grant only has to
	// match the dashboard in the path.
	granted := level == "read" && apiutil.PluginDashboardGrant(r) == dashboardID
	if !granted {
		userID := apiutil.PluginUserID(r)
		if userID == "" {
			apiutil.WriteErrorResponse(w, apperrors.ErrUnauthorized.WithMessage("authentication required"))
			return
		}
		allowed, err := p.host.CheckDashboardAccess(r.Context(), userID, dashboardID, level)
		if err != nil || !allowed {
			apiutil.WriteErrorResponse(w, apperrors.ErrForbidden.WithMessage("insufficient permissions"))
			return
		}
	}

	events := p.fetchAllSources(r.Context(), p.loadSources(r.Context(), dashboardID, widgetID), maxAge)
	if events == nil {
		events = []CalendarEvent{}
	}
	apiutil.WriteJSON(w, http.StatusOK, apiutil.Success[CalendarData]{Data: CalendarData{
		Events:    events,
		FetchedAt: time.Now().Unix(),
	}})
}
