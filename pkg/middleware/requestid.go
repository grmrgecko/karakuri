package middleware

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/logger"
)

// RequestID injects a unique request ID into each request context and response header.
// Reuses an incoming X-Request-ID if present.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.New().String()
		}
		ctx := logger.WithCorrelationID(r.Context(), id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
