package middleware

import (
	"net/http"

	"github.com/grmrgecko/karakuri/pkg/events"
)

// actorFor describes who is making a request, for the event log: the user or
// API key, how the request arrived, the dashboard it came from, and the
// client address.
func actorFor(r *http.Request, uc *UserContext) events.Actor {
	a := events.Actor{Kind: events.ActorAnonymous, Via: events.ViaAPI}
	if uc != nil {
		a.Kind, a.ID, a.Name = events.ActorUser, uc.UserID.String(), uc.Username
		if uc.APIKeyID != "" {
			a.Kind, a.ID = events.ActorAPIKey, uc.APIKeyID
			a.Name = uc.APIKeyName + " (" + uc.Username + ")"
		}
	}
	if ref := GetDashboardContext(r.Context()); ref != "" {
		a.Via, a.DashboardID = events.ViaDashboard, ref
	}
	if meta := GetRequestMeta(r.Context()); meta != nil {
		a.Address = meta.IPAddress
	}
	return a
}

// withActor returns r carrying the actor for uc, which may be nil for an
// anonymous request.
func withActor(r *http.Request, uc *UserContext) *http.Request {
	a := actorFor(r, uc)
	events.FillSlot(r.Context(), a)
	return r.WithContext(events.WithActor(r.Context(), a))
}

// Via marks every request through the handler as arriving by via, such as
// MCP, so the event log says how an action was made.
func Via(via string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a := events.ActorFrom(r.Context())
			if a.Kind != events.ActorSystem {
				a.Via = via
				r = r.WithContext(events.WithActor(r.Context(), a))
			}
			next.ServeHTTP(w, r)
		})
	}
}
