package middleware

import (
	"context"
	"net/http"
)

// DashboardHeader names the dashboard a widget's request is being made from.
// The host's widget bridge stamps it on every call a widget makes, so a widget
// needs no knowledge of authorization at all: the header says which dashboard
// is on screen, and the server decides what that dashboard exposes.
//
// The header is a claim, not a credential. Naming a dashboard grants nothing
// on its own — the request still has to be one the caller may view, and the
// capability still has to be one that dashboard's deployed layout exposes.
const DashboardHeader = "X-Karakuri-Dashboard"

const dashboardContextKey contextKeyType = "dashboard"

// DashboardContext carries the dashboard a request was made from, if any.
func DashboardContext() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ref := r.Header.Get(DashboardHeader); ref != "" {
				ctx := context.WithValue(r.Context(), dashboardContextKey, ref)
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// GetDashboardContext returns the dashboard a request names, empty when none.
func GetDashboardContext(ctx context.Context) string {
	if ref, ok := ctx.Value(dashboardContextKey).(string); ok {
		return ref
	}
	return ""
}
