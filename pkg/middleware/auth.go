package middleware

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/auth"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
)

type contextKeyType string

const userContextKey contextKeyType = "user"
const renderContextKey contextKeyType = "render"

// RenderContext names the dashboard a headless render is photographing. A
// render holds no account: its token is a viewer grant for one dashboard, for
// as long as the job runs. The job selects the deployed layout or an editor's
// draft preview. Nothing else on the instance answers it.
type RenderContext struct {
	Draft       bool
	DashboardID uuid.UUID
}

// UserContext carries the authenticated user's identity through the request.
type UserContext struct {
	UserID    uuid.UUID
	Username  string
	SessionID uuid.UUID
	// APIKeyID and APIKeyName name the key a request authenticated with, so
	// the event log says which key acted rather than only whose.
	APIKeyID   string
	APIKeyName string
	// RenderDashboardID is set when the request authenticated with a render
	// key, which opens that dashboard's render URLs and nothing else.
	RenderDashboardID *uuid.UUID
}

// bearerToken reads the credential from the Authorization header, which is
// where an API client and the SPA put it. Authenticated routes take nothing
// from the query string: a token in a URL lands in browser history, referers,
// and proxy logs, and only the render URLs and the WebSocket, which cannot set
// a header, are read that way.
func bearerToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
		return parts[1]
	}
	return ""
}

// extractToken is bearerToken with the "token" query parameter as a fallback,
// for the routes a browser navigates to directly.
func extractToken(r *http.Request) string {
	if token := bearerToken(r); token != "" {
		return token
	}
	return r.URL.Query().Get("token")
}

// tryAPIKeyAuth looks up API key candidates by prefix and verifies the token
// hash. On success it updates last-used and returns the authenticated user
// context with the key's permission limit applied to ctx.
func tryAPIKeyAuth(ctx context.Context, token string) (context.Context, *UserContext) {
	uc, key := findAPIKey(token)
	if uc == nil {
		return ctx, nil
	}
	// A render key holds no permissions at all; a limited key holds the
	// ones it lists that its user's roles also grant.
	if key.IsRenderKey() {
		ctx = auth.WithKeyLimit(ctx, nil)
	} else if key.Limited {
		ctx = auth.WithKeyLimit(ctx, key.Permissions)
	}
	return ctx, uc
}

// findAPIKey resolves a bearer token to the key it is and its user.
func findAPIKey(token string) (*UserContext, *models.APIKey) {
	if len(token) < 8 {
		return nil, nil
	}
	prefix := token[:8]

	keys, err := models.FindAPIKeyByPrefix(prefix)
	if err != nil || len(keys) == 0 {
		return nil, nil
	}

	for i := range keys {
		key := &keys[i]
		if !models.CheckTokenHash(key.KeyHash, token) {
			continue
		}

		// Reject expired keys.
		if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
			return nil, nil
		}

		var user models.User
		if err := db.DB.Where("id = ?", key.UserID).First(&user).Error; err != nil {
			return nil, nil
		}

		// Best-effort last-used update; failure is non-fatal.
		_ = key.UpdateLastUsed()

		return &UserContext{
			UserID:            user.ID,
			Username:          user.Username,
			APIKeyID:          strconv.FormatUint(uint64(key.ID), 10),
			APIKeyName:        key.Name,
			RenderDashboardID: key.DashboardID,
		}, key
	}

	return nil, nil
}

// tryRenderTokenAuth resolves a render job's token into the dashboard it
// grants a view of.
func tryRenderTokenAuth(token string) *RenderContext {
	job, err := models.FindRenderJobByRenderToken(token)
	if err != nil {
		return nil
	}
	return &RenderContext{DashboardID: job.DashboardID, Draft: job.Draft}
}

// GetRenderContext returns the dashboard a render token names, or nil.
func GetRenderContext(ctx context.Context) *RenderContext {
	if rc, ok := ctx.Value(renderContextKey).(*RenderContext); ok {
		return rc
	}
	return nil
}

// RendersDashboard reports whether this request is the headless render of the
// given dashboard. It is the only thing a render token grants, which is why
// every caller names the dashboard it is asking about.
func RendersDashboard(ctx context.Context, dashboardID uuid.UUID) bool {
	rc := GetRenderContext(ctx)
	return rc != nil && rc.DashboardID == dashboardID
}

// Auth validates the Bearer token and injects UserContext into the request.
// Rejects the request with 401 if the token is missing or invalid.
// Tries JWT first, then API key.
func Auth() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerToken(r)
			if token == "" {
				apiutil.WriteErrorResponse(w, apperrors.ErrUnauthorized)
				return
			}

			// Try JWT first.
			claims, session, err := auth.ValidateToken(token)
			if err == nil {
				uc := &UserContext{
					UserID:   claims.UserID,
					Username: claims.Username,
				}
				if session != nil {
					uc.SessionID = session.ID
					throttleSessionActivity(session)
				}
				ctx := context.WithValue(r.Context(), userContextKey, uc)
				next.ServeHTTP(w, withActor(r.WithContext(ctx), uc))
				return
			}

			// Fall back to API key authentication. A render key opens render
			// URLs and nothing here.
			if ctx, uc := tryAPIKeyAuth(r.Context(), token); uc != nil && uc.RenderDashboardID == nil {
				ctx = context.WithValue(ctx, userContextKey, uc)
				next.ServeHTTP(w, withActor(r.WithContext(ctx), uc))
				return
			}

			apiutil.WriteErrorResponse(w, apperrors.ErrUnauthorized.WithMessage("Invalid or expired token"))
		})
	}
}

// throttleSessionActivity updates the session's last-active timestamp at most once every 5 minutes.
func throttleSessionActivity(session *models.Session) {
	if time.Since(session.LastActiveAt) > 5*time.Minute {
		db.DB.Model(session).Update("last_active_at", time.Now())
	}
}

// OptionalAuth attempts authentication but allows unauthenticated requests through.
// Checks the Authorization header first, then falls back to a "token"
// query parameter. This enables direct browser navigation to protected
// resources where JavaScript cannot inject headers. A render key leaves the
// request anonymous.
func OptionalAuth() func(http.Handler) http.Handler {
	return optionalAuth(false)
}

// RenderURLAuth is OptionalAuth for the render URLs, which also accept a
// render key. The handler still has to check the key names the dashboard the
// URL renders; RenderKeyAllows answers that.
func RenderURLAuth() func(http.Handler) http.Handler {
	return optionalAuth(true)
}

// RenderKeyAllows reports whether a request may render a dashboard as far as
// its key is concerned: any request that did not authenticate with a render
// key, or one whose render key names this dashboard.
func RenderKeyAllows(ctx context.Context, dashboardID uuid.UUID) bool {
	uc := GetUser(ctx)
	return uc == nil || uc.RenderDashboardID == nil || *uc.RenderDashboardID == dashboardID
}

func optionalAuth(renderKeys bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractToken(r)

			if token != "" {
				// Try JWT first.
				claims, session, err := auth.ValidateToken(token)
				if err == nil {
					uc := &UserContext{
						UserID:   claims.UserID,
						Username: claims.Username,
					}
					if session != nil {
						uc.SessionID = session.ID
						throttleSessionActivity(session)
					}
					ctx := context.WithValue(r.Context(), userContextKey, uc)
					r = r.WithContext(ctx)
				} else if ctx, uc := tryAPIKeyAuth(r.Context(), token); uc != nil {
					// Fall back to API key authentication. A render key is
					// honoured only where the route asked for it.
					if uc.RenderDashboardID == nil || renderKeys {
						ctx = context.WithValue(ctx, userContextKey, uc)
						r = r.WithContext(ctx)
					}
				} else if rc := tryRenderTokenAuth(token); rc != nil {
					// Last, a render in progress. It stays anonymous: the
					// grant is a view of one dashboard, never an account.
					ctx := context.WithValue(r.Context(), renderContextKey, rc)
					r = r.WithContext(ctx)
				}
			}

			next.ServeHTTP(w, withActor(r, GetUser(r.Context())))
		})
	}
}

// RequirePermission rejects the request with 403 if the authenticated user lacks the
// specified permission string.
func RequirePermission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uc := GetUser(r.Context())
			if uc == nil {
				apiutil.WriteErrorResponse(w, apperrors.ErrUnauthorized)
				return
			}

			if !auth.Can(r.Context(), uc.UserID, permission) {
				apiutil.WriteErrorResponse(w, apperrors.ErrForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// GetUser extracts the authenticated user from the request context, or nil.
func GetUser(ctx context.Context) *UserContext {
	if uc, ok := ctx.Value(userContextKey).(*UserContext); ok {
		return uc
	}
	return nil
}
