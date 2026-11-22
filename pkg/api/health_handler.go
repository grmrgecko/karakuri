package api

import (
	"context"
	"net/http"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// HealthData holds the payload fields for a health check response.
type HealthData struct {
	HealthStatus string `json:"health_status" description:"Health check result."`
	Reason       string `json:"reason,omitempty" description:"Reason if not ready."`
}

// HealthLiveHandler returns the liveness check interactor.
func HealthLiveHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[HealthData]) error {
		out.Data = HealthData{HealthStatus: "ok"}
		return nil
	})
	u.SetTitle("Liveness Check")
	u.SetDescription("Returns ok if the service is alive.")
	u.SetTags("Health")
	return u
}

// HealthReadyHandler returns the readiness check interactor that verifies database connectivity.
func HealthReadyHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[HealthData]) error {
		sqlDB, err := db.DB.DB()
		if err != nil || sqlDB.Ping() != nil {
			return apperrors.ErrServiceUnavailable.WithError(err)
		}
		out.Data = HealthData{HealthStatus: "ready"}
		return nil
	})
	u.SetTitle("Readiness Check")
	u.SetDescription("Returns ready if the database is reachable.")
	u.SetTags("Health")
	return u
}

// RegisterHealthRoutes registers health check endpoints.
func RegisterHealthRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.Method(http.MethodGet, "/health",
		nethttp.NewHandler(HealthLiveHandler()))
	s.Method(http.MethodGet, "/health/ready",
		nethttp.NewHandler(HealthReadyHandler()))
}

// HealthHandler is a plain HTTP handler for the top-level /health endpoint,
// served outside the /api prefix for load balancers and uptime checks.
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	apiutil.WriteJSON(w, http.StatusOK, apiutil.Success[HealthData]{
		Data: HealthData{HealthStatus: "ok"},
	})
}
