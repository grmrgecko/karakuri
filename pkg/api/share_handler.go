package api

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// SuggestedUser represents a user candidate for share autocomplete.
type SuggestedUser struct {
	ID       uuid.UUID `json:"id" description:"User ID."`
	Username string    `json:"username" description:"Username."`
}

// SuggestedGroup represents a group candidate for share autocomplete.
type SuggestedGroup struct {
	ID   uint   `json:"id" description:"Group ID."`
	Name string `json:"name" description:"Group name."`
}

// ShareSuggestionGetInput holds the optional search query for share suggestions.
type ShareSuggestionGetInput struct {
	Query string `query:"q" description:"Optional search query to filter users and groups."`
}

// ShareSuggestionData holds the suggested users and groups for sharing.
type ShareSuggestionData struct {
	Users  []SuggestedUser  `json:"users" description:"Suggested users for sharing."`
	Groups []SuggestedGroup `json:"groups" description:"Suggested groups for sharing."`
}

// ShareSuggestionGetHandler returns suggested users and groups for the share autocomplete.
// Without a query, it returns the current user's co-group members and groups.
// With a query, it also searches all users and groups by name.
func ShareSuggestionGetHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ShareSuggestionGetInput, out *apiutil.Success[ShareSuggestionData]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Initialize result maps.
		userMap := make(map[uuid.UUID]SuggestedUser)
		groupMap := make(map[uint]SuggestedGroup)

		// Gather the user's groups and co-group members as default suggestions.
		groupIDs, err := models.GetUserGroupIDs(uc.UserID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		for _, gid := range groupIDs {
			var g models.UserGroup
			if gErr := db.DB.First(&g, gid).Error; gErr == nil {
				groupMap[g.ID] = SuggestedGroup{ID: g.ID, Name: g.Name}
			}
		}

		coUsers, err := models.GetMemberUsers(groupIDs)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		for _, u := range coUsers {
			if u.ID != uc.UserID {
				userMap[u.ID] = SuggestedUser{ID: u.ID, Username: u.Username}
			}
		}

		// If a search query is provided, also search all users and groups.
		if in.Query != "" {
			users, uErr := models.SearchUsers(in.Query, 20)
			if uErr == nil {
				for _, u := range users {
					if u.ID != uc.UserID {
						userMap[u.ID] = SuggestedUser{ID: u.ID, Username: u.Username}
					}
				}
			}

			groups, gErr := models.SearchUserGroups(in.Query, 20)
			if gErr == nil {
				for _, g := range groups {
					groupMap[g.ID] = SuggestedGroup{ID: g.ID, Name: g.Name}
				}
			}
		}

		// Build response slices from maps.
		users := make([]SuggestedUser, 0, len(userMap))
		groups := make([]SuggestedGroup, 0, len(groupMap))
		for _, u := range userMap {
			users = append(users, u)
		}
		for _, g := range groupMap {
			groups = append(groups, g)
		}

		out.Data = ShareSuggestionData{Users: users, Groups: groups}

		return nil
	})
	u.SetTitle("Share Suggestions")
	u.SetDescription("Returns suggested users and groups for the share autocomplete, including co-group members and optional search results.")
	u.SetTags("Sharing")
	return u
}

// RegisterShareRoutes registers share suggestion endpoints.
func RegisterShareRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodGet, "/share_suggestions",
			nethttp.NewHandler(ShareSuggestionGetHandler()))
}
