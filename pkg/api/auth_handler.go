package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/auth"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// LoginInput holds credentials for authenticating a user.
type LoginInput struct {
	Username string `json:"username" required:"true" minLength:"1" description:"Login username."`
	Password string `json:"password" required:"true" minLength:"1" description:"Login password."`
}

// MeResponse extends the user model with the effective permission set.
type MeResponse struct {
	*models.User
	Permissions []string `json:"permissions" description:"Effective permission strings."`
}

// LoginData holds the JWT token pair and authenticated user profile.
type LoginData struct {
	auth.TokenPair
	User MeResponse `json:"user" description:"Authenticated user with permissions."`
}

// AuthLoginHandler returns the interactor that authenticates a user with credentials and issues JWT tokens.
func AuthLoginHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in LoginInput, out *apiutil.Success[LoginData]) error {
		// Resolve client metadata for session tracking and throttling.
		meta := middleware.GetRequestMeta(ctx)
		ip := ""
		ua := ""
		if meta != nil {
			ip = meta.IPAddress
			ua = meta.UserAgent
		}

		// Authenticate credentials, counting failures against the username
		// and the address so neither a spray across accounts nor a dictionary
		// against one runs unbounded.
		keys := []string{"user:" + strings.ToLower(strings.TrimSpace(in.Username))}
		if ip != "" {
			keys = append(keys, "ip:"+ip)
		}
		attempt := events.Actor{Kind: events.ActorAnonymous, Name: in.Username, Via: events.ViaAPI, Address: ip}
		client := map[string]any{"username": in.Username, "client": ua}
		if !auth.Logins.Allow(keys...) {
			events.Record(events.Entry{Category: models.EventSecurity, Level: models.EventWarning,
				Subject: events.Subject{Kind: "user", Name: in.Username},
				Message: "Sign-in refused for " + in.Username + " after repeated failures", Details: client, Actor: attempt})
			return apperrors.ErrRateLimit.WithMessage("too many failed sign-ins; try again later")
		}
		user, err := auth.Authenticate(in.Username, in.Password)
		if err != nil {
			auth.Logins.Fail(keys...)
			// The username is recorded as typed; the password never is.
			events.Record(events.Entry{Category: models.EventSecurity, Level: models.EventWarning,
				Subject: events.Subject{Kind: "user", Name: in.Username},
				Message: "Failed sign-in for " + in.Username, Details: client, Actor: attempt})
			return apperrors.ErrUnauthorized.WithMessage("invalid credentials")
		}
		auth.Logins.Reset(keys...)
		events.Record(events.Entry{Category: models.EventSecurity,
			Subject: events.Subject{Kind: "user", ID: user.ID.String(), Name: user.Username},
			Message: user.Username + " signed in", Details: map[string]any{"client": ua},
			Actor: events.Actor{Kind: events.ActorUser, ID: user.ID.String(), Name: user.Username, Via: events.ViaAPI, Address: ip}})

		// Create session and generate JWT tokens.
		tokens, err := auth.CreateSession(user, ip, ua)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Opportunistic cleanup of expired sessions.
		go auth.CleanupExpiredSessions()

		// Load effective permissions.
		perms, _ := auth.GetEffectivePermissions(user.ID)
		if perms == nil {
			perms = []string{}
		}

		// Build response.
		out.Data = LoginData{
			TokenPair: *tokens,
			User: MeResponse{
				User:        user,
				Permissions: perms,
			},
		}
		return nil
	})
	u.SetTitle("Login")
	u.SetDescription("Authenticates with username and password, returns JWT tokens.")
	u.SetTags("Auth")
	return u
}

// AuthLogoutHandler returns the interactor that invalidates the current session.
func AuthLogoutHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[any]) error {
		// Delete the session tied to this token.
		uc := middleware.GetUser(ctx)
		if uc != nil && uc.SessionID != uuid.Nil {
			_ = auth.DeleteSession(uc.SessionID)
		}

		out.Message = "Logged out successfully"
		return nil
	})
	u.SetTitle("Logout")
	u.SetDescription("Invalidates the current session, revoking the JWT.")
	u.SetTags("Auth")
	return u
}

// RefreshInput holds the refresh token for obtaining new JWT tokens.
type RefreshInput struct {
	RefreshToken string `json:"refresh_token" required:"true" minLength:"1" description:"Refresh token from a previous login."`
}

// AuthRefreshHandler returns the interactor that exchanges a refresh token for new access and refresh tokens.
func AuthRefreshHandler(log *zap.Logger) usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in RefreshInput, out *apiutil.Success[auth.TokenPair]) error {
		tokens, err := auth.RefreshToken(in.RefreshToken)
		if errors.Is(err, auth.ErrRefreshReused) {
			meta := middleware.GetRequestMeta(ctx)
			fields := []zap.Field{}
			if meta != nil {
				fields = append(fields, zap.String("ip", meta.IPAddress))
			}
			log.Error("replayed refresh token; session revoked", fields...)
			address := ""
			if meta != nil {
				address = meta.IPAddress
			}
			events.Record(events.Entry{Category: models.EventSecurity, Level: models.EventError,
				Subject: events.Subject{Kind: "session"},
				Message: "A refresh token was used twice; the session was revoked",
				Actor:   events.Actor{Kind: events.ActorAnonymous, Via: events.ViaAPI, Address: address}})
			return apperrors.ErrUnauthorized.WithMessage("refresh token was already used; sign in again")
		}
		if err != nil {
			return apperrors.ErrUnauthorized.WithMessage("invalid refresh token")
		}

		out.Data = *tokens
		return nil
	})
	u.SetTitle("Refresh Token")
	u.SetDescription("Exchanges a refresh token for new access and refresh tokens.")
	u.SetTags("Auth")
	return u
}

// AuthMeHandler returns the interactor that retrieves the authenticated user's profile and permissions.
func AuthMeHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[MeResponse]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Load user profile.
		user, err := auth.GetUserByID(uc.UserID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("user not found")
		}

		// Load effective permissions.
		perms, _ := auth.GetEffectivePermissions(uc.UserID)
		if perms == nil {
			perms = []string{}
		}

		// Build response.
		out.Data = MeResponse{
			User:        user,
			Permissions: perms,
		}
		return nil
	})
	u.SetTitle("Current User")
	u.SetDescription("Returns the authenticated user's profile and effective permissions.")
	u.SetTags("Auth")
	return u
}

// PreferencesInput carries a partial preferences patch for the current user.
type PreferencesInput struct {
	Preferences map[string]any `json:"preferences" required:"true" description:"Preference keys to set; absent keys keep their stored value."`
}

// AuthPreferencesHandler returns the interactor that merges UI preferences into the authenticated user's profile.
func AuthPreferencesHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in PreferencesInput, out *apiutil.Success[models.Preferences]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Merge the patch into the stored blob.
		merged, err := models.UpdateUserPreferences(uc.UserID, models.Preferences(in.Preferences))
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Data = merged
		return nil
	})
	u.SetTitle("Update Preferences")
	u.SetDescription("Merges client UI preferences into the authenticated user's profile.")
	u.SetTags("Auth")
	return u
}

// SessionItem extends a session record with a flag indicating the current session.
type SessionItem struct {
	*models.Session
	Current bool `json:"current" description:"Whether this is the requesting session."`
}

// SessionListHandler returns the interactor that lists the authenticated user's active sessions.
func SessionListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]SessionItem]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Query sessions for the user.
		sessions, err := models.ListUserSessions(uc.UserID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response with current-session flag.
		items := make([]SessionItem, len(sessions))
		for i, s := range sessions {
			items[i] = SessionItem{
				Session: s,
				Current: s.ID == uc.SessionID,
			}
		}

		out.Data = items
		return nil
	})
	u.SetTitle("List Sessions")
	u.SetDescription("Returns active sessions for the authenticated user.")
	u.SetTags("Sessions")
	return u
}

// SessionDeleteInput identifies a session to revoke by ID.
type SessionDeleteInput struct {
	ID string `path:"id" format:"uuid" description:"Session ID to revoke."`
}

// SessionDeleteHandler returns the interactor that revokes a specific session.
func SessionDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in SessionDeleteInput, out *apiutil.Success[any]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Parse session ID.
		sessionID, err := uuid.Parse(in.ID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid session ID")
		}

		// Delete the session scoped to the current user.
		err = models.DeleteUserSession(uc.UserID, sessionID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperrors.ErrNotFound.WithMessage("session not found")
		}
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Message = "Session revoked"
		return nil
	})
	u.SetTitle("Revoke Session")
	u.SetDescription("Revokes a specific session by ID, scoped to the authenticated user.")
	u.SetTags("Sessions")
	return u
}

// SessionDeleteOthersData holds the confirmation and count of revoked sessions.
type SessionDeleteOthersData struct {
	Message string `json:"message" description:"Confirmation message."`
	Revoked int64  `json:"revoked" description:"Number of sessions revoked."`
}

// SessionDeleteOthersHandler returns the interactor that revokes all sessions except the current one.
func SessionDeleteOthersHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[SessionDeleteOthersData]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Delete all other sessions.
		count, err := models.DeleteOtherUserSessions(uc.UserID, uc.SessionID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Data = SessionDeleteOthersData{
			Message: "Other sessions revoked",
			Revoked: count,
		}
		return nil
	})
	u.SetTitle("Revoke Other Sessions")
	u.SetDescription("Revokes all sessions except the current one for the authenticated user.")
	u.SetTags("Sessions")
	return u
}

// RegisterAuthRoutes registers authentication and session management endpoints.
func RegisterAuthRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler, log *zap.Logger) {
	// Public auth.
	s.Method(http.MethodPost, "/auth/login",
		nethttp.NewHandler(AuthLoginHandler()))
	s.Method(http.MethodPost, "/auth/refresh",
		nethttp.NewHandler(AuthRefreshHandler(log)))

	// Authenticated auth.
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodPost, "/auth/logout",
			nethttp.NewHandler(AuthLogoutHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodGet, "/auth/me",
			nethttp.NewHandler(AuthMeHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodPut, "/auth/preferences",
			nethttp.NewHandler(AuthPreferencesHandler()))

	// Session management (user-scoped).
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodGet, "/auth/sessions",
			nethttp.NewHandler(SessionListHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodDelete, "/auth/sessions/{id}",
			nethttp.NewHandler(SessionDeleteHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodPost, "/auth/sessions/revoke_others",
			nethttp.NewHandler(SessionDeleteOthersHandler()))
}
