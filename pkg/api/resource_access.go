package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// requireConnection admits a request to a /connections/{id}/... route only
// when the caller holds level on that connection, answering not found when
// they hold nothing. It runs after Auth. An id that is not a UUID, such as the
// energy pseudo-connection the time-series query accepts, is left to the
// handler.
func requireConnection(level string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := chi.URLParam(r, "id")
			if _, err := uuid.Parse(id); err != nil {
				next.ServeHTTP(w, r)
				return
			}
			if _, err := access.Connection(r.Context(), callerID(r.Context()), id, level); err != nil {
				var appErr *apperrors.AppError
				if !errors.As(err, &appErr) {
					appErr = apperrors.ErrInternal
				}
				apiutil.WriteErrorResponse(w, appErr)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// useConnectionID authorizes the caller at use on a connection named in a
// request body or query rather than the path.
func useConnectionID(ctx context.Context, id string) error {
	_, err := access.Connection(ctx, callerID(ctx), id, access.Use)
	return err
}

// deviceForCaller loads a device and authorizes the caller on the connection
// it speaks on, answering not found when they cannot use it, so a device on a
// private connection cannot be confirmed by probing.
func deviceForCaller(ctx context.Context, id uuid.UUID) (*models.MQTTDevice, error) {
	device, err := models.FindMQTTDeviceByID(id)
	if err != nil || !access.CanUseConnection(ctx, callerID(ctx), device.ConnectionID.String()) {
		return nil, apperrors.ErrNotFound.WithMessage("device not found")
	}
	return device, nil
}
