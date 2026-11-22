package middleware

import (
	"net/http"
	"runtime/debug"

	"github.com/grmrgecko/karakuri/internal/logger"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"

	"go.uber.org/zap"
)

// Recovery catches panics from downstream handlers, logs the stack trace,
// and returns a 500 JSON error response.
func Recovery(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					l := logger.FromContext(r.Context(), log)
					l.Error("panic recovered",
						zap.Any("panic", rec),
						zap.String("stack", string(debug.Stack())),
					)

					apiutil.WriteErrorResponse(w, apperrors.ErrInternal)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
