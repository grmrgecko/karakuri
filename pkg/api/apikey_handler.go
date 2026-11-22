package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// APIKeyItem is one API key as its owner lists it, with the name of the
// dashboard a render key opens.
type APIKeyItem struct {
	*models.APIKey
	DashboardName string `json:"dashboard_name,omitempty" description:"Name of the dashboard a render key opens."`
}

// APIKeyListInput optionally narrows the list to one dashboard's render keys.
type APIKeyListInput struct {
	DashboardID string `query:"dashboard_id" description:"List only the render keys of this dashboard UUID."`
}

// APIKeyListHandler returns the interactor that lists the authenticated user's API keys.
func APIKeyListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in APIKeyListInput, out *apiutil.Success[[]APIKeyItem]) error {
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		query := db.DB.Where("user_id = ?", uc.UserID)
		if in.DashboardID != "" {
			id, err := uuid.Parse(in.DashboardID)
			if err != nil {
				return apperrors.ErrValidation.WithMessage("invalid dashboard ID")
			}
			query = query.Where("dashboard_id = ?", id)
		}
		var keys []*models.APIKey
		if err := query.Order("created_at DESC").Find(&keys).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Name the dashboards render keys open, in one query.
		names := map[uuid.UUID]string{}
		var ids []uuid.UUID
		for _, k := range keys {
			if k.DashboardID != nil {
				ids = append(ids, *k.DashboardID)
			}
		}
		if len(ids) > 0 {
			var rows []models.Dashboard
			if err := db.DB.Select("id", "name").Where("id IN ?", ids).Find(&rows).Error; err != nil {
				return apperrors.ErrInternal.WithError(err)
			}
			for _, d := range rows {
				names[d.ID] = d.Name
			}
		}

		out.Data = make([]APIKeyItem, 0, len(keys))
		for _, k := range keys {
			item := APIKeyItem{APIKey: k}
			if k.DashboardID != nil {
				item.DashboardName = names[*k.DashboardID]
			}
			out.Data = append(out.Data, item)
		}
		return nil
	})
	u.SetTitle("List API Keys")
	u.SetDescription("Returns API keys belonging to the authenticated user, optionally only one dashboard's render keys.")
	u.SetTags("API Keys")
	return u
}

// APIKeyCreateInput holds fields for generating a new API key.
type APIKeyCreateInput struct {
	Name      string  `json:"name" required:"true" minLength:"1" description:"Display name for the key."`
	ExpiresAt *string `json:"expires_at,omitempty" format:"date-time" description:"Expiration time in RFC3339 format."`
	// Permissions limits the key when present, even when empty: a key with an
	// empty list passes no permission check.
	Permissions *[]string `json:"permissions,omitempty" description:"Limit the key to these permission strings, in the role syntax; omit for a key holding every permission its user holds."`
	DashboardID string    `json:"dashboard_id,omitempty" description:"Make a render key that opens only this dashboard's render URLs; it holds no permissions."`
}

// APIKeyCreateData holds the created API key record and its raw bearer value.
type APIKeyCreateData struct {
	*models.APIKey
	Key string `json:"key" description:"Raw API key."`
}

// APIKeyCreateHandler returns the interactor that generates a new API key.
func APIKeyCreateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in APIKeyCreateInput, out *apiutil.Success[APIKeyCreateData]) error {
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		key := &models.APIKey{
			UserID: uc.UserID,
			Name:   strings.TrimSpace(in.Name),
		}
		if key.Name == "" {
			return apperrors.ErrValidation.WithMessage("name is required")
		}
		if err := limitNewKey(ctx, uc.UserID, key, in); err != nil {
			return err
		}

		// Parse optional expiration (format validated by framework).
		if in.ExpiresAt != nil && *in.ExpiresAt != "" {
			t, err := time.Parse(time.RFC3339, *in.ExpiresAt)
			if err != nil {
				return apperrors.ErrValidation.WithMessage("expires_at must be an RFC 3339 time such as 2027-01-01T00:00:00Z")
			}
			key.ExpiresAt = &t
		}
		rawKey, err := key.Generate()
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		if err := db.DB.Create(key).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Data = APIKeyCreateData{APIKey: key, Key: rawKey}
		return nil
	})
	u.SetTitle("Create API Key")
	u.SetDescription("Generates a new API key for the authenticated user and returns its raw value. A key may be limited to permissions its creator holds, or be a render key for one dashboard.")
	u.SetTags("API Keys")
	return u
}

// limitNewKey applies a new key's limit or dashboard. A key never holds what
// its creator does not: each listed permission must be covered by the
// creator's roles and by the limit of the key the request authenticated
// with, and a limited key cannot mint an unlimited one.
func limitNewKey(ctx context.Context, userID uuid.UUID, key *models.APIKey, in APIKeyCreateInput) error {
	_, callerLimited := auth.KeyLimit(ctx)

	if in.DashboardID != "" {
		if in.Permissions != nil && len(*in.Permissions) > 0 {
			return apperrors.ErrValidation.WithMessage("a render key holds no permissions")
		}
		dashboard, err := dashops.ResolveFor(ctx, userID, in.DashboardID)
		if err != nil || !dashops.CanView(ctx, userID, dashboard) {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}
		// The key holds no permissions, so it opens the dashboard only when
		// ownership, a share, or publishing does. Refusing here keeps a key
		// from being made that could never open anything.
		if !dashops.CanView(auth.WithKeyLimit(ctx, nil), userID, dashboard) {
			return apperrors.ErrValidation.WithMessage("you see this dashboard only through the dashboard manage permission, which a render key does not hold; share it with yourself or make it public first")
		}
		key.DashboardID = &dashboard.ID
		key.Limited = true
		key.Permissions = nil
		return nil
	}

	if in.Permissions == nil {
		if callerLimited {
			return apperrors.ErrForbidden.WithMessage("a limited API key can only create limited keys")
		}
		return nil
	}

	perms := dedupe(*in.Permissions)
	if err := auth.ValidateKeyPermissions(perms); err != nil {
		return apperrors.ErrValidation.WithMessage(err.Error())
	}
	var refused []string
	for _, p := range perms {
		if !auth.Covers(ctx, userID, p) {
			refused = append(refused, p)
		}
	}
	if len(refused) > 0 {
		return apperrors.ErrForbidden.WithMessage(fmt.Sprintf("you do not hold %s, so a key cannot either", strings.Join(refused, ", ")))
	}
	key.Limited = true
	key.Permissions = perms
	return nil
}

// dedupe trims, drops empty entries, removes repeats, and sorts.
func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// APIKeyIDInput identifies an API key by ID.
type APIKeyIDInput struct {
	ID uint `path:"id" description:"API key ID."`
}

// APIKeyValueData is a render key's bearer value.
type APIKeyValueData struct {
	Key string `json:"key" description:"Raw API key."`
}

// APIKeyValueHandler returns the interactor that reveals a render key's value
// to its owner, so the render page can put it into the addresses it copies.
// Only render keys are revealed, because they are made to be written into
// URLs, and only over a signed-in session: a key that could read another
// key's value would be a way out of its own limit.
func APIKeyValueHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in APIKeyIDInput, out *apiutil.Success[APIKeyValueData]) error {
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}
		if uc.APIKeyID != "" {
			return apperrors.ErrForbidden.WithMessage("key values are revealed only to a signed-in session")
		}
		var key models.APIKey
		if err := db.DB.Where("id = ? AND user_id = ?", in.ID, uc.UserID).First(&key).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("API key not found")
		}
		if !key.IsRenderKey() {
			return apperrors.ErrForbidden.WithMessage("only a render key's value can be shown again")
		}
		raw, err := key.Raw()
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		out.Data = APIKeyValueData{Key: raw}
		return nil
	})
	u.SetTitle("Reveal Render Key")
	u.SetDescription("Returns a render key's raw value to its owner over a signed-in session.")
	u.SetTags("API Keys")
	return u
}

// APIKeyPermissionsHandler returns the interactor that lists the registered
// permissions the caller may hand to a limited key.
func APIKeyPermissionsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]string]) error {
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}
		perms := auth.EffectiveFor(ctx, uc.UserID)
		if perms == nil {
			perms = []string{}
		}
		out.Data = perms
		return nil
	})
	u.SetTitle("List Key Permissions")
	u.SetDescription("Lists the registered permissions the caller holds and may give a limited API key.")
	u.SetTags("API Keys")
	return u
}

// APIKeyDeleteHandler returns the interactor that revokes an API key.
func APIKeyDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in APIKeyIDInput, out *apiutil.Success[any]) error {
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Delete the key scoped to the current user.
		tx := db.DB.Where("id = ? AND user_id = ?", in.ID, uc.UserID).Delete(&models.APIKey{})
		if tx.Error != nil {
			return apperrors.ErrInternal.WithError(tx.Error)
		}
		if tx.RowsAffected == 0 {
			return apperrors.ErrNotFound.WithMessage("API key not found")
		}

		out.Message = "API key deleted"
		return nil
	})
	u.SetTitle("Delete API Key")
	u.SetDescription("Deletes an API key by ID, scoped to the authenticated user.")
	u.SetTags("API Keys")
	return u
}

// RegisterAPIKeyRoutes registers API key management endpoints.
func RegisterAPIKeyRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermAPIKeysRead), bearerDoc).
		Method(http.MethodGet, "/api_keys",
			nethttp.NewHandler(APIKeyListHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermAPIKeysWrite), bearerDoc).
		Method(http.MethodGet, "/api_keys/permissions",
			nethttp.NewHandler(APIKeyPermissionsHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermAPIKeysRead), bearerDoc).
		Method(http.MethodGet, "/api_keys/{id}/value",
			nethttp.NewHandler(APIKeyValueHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermAPIKeysWrite), bearerDoc).
		Method(http.MethodPost, "/api_keys",
			nethttp.NewHandler(APIKeyCreateHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermAPIKeysWrite), bearerDoc).
		Method(http.MethodDelete, "/api_keys/{id}",
			nethttp.NewHandler(APIKeyDeleteHandler()))
}
