package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/auth"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
	"gorm.io/gorm"
)

// UserListInput holds pagination parameters for listing users.
type UserListInput struct {
	Page  int64 `query:"page" default:"1" minimum:"1" description:"Page number."`
	Limit int64 `query:"limit" default:"20" minimum:"1" maximum:"100" description:"Number of items per page."`
}

// UserListHandler returns the interactor that lists users with pagination.
func UserListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in UserListInput, out *apiutil.Success[[]*models.User]) error {
		page := in.Page
		limit := in.Limit

		// Query users.
		users, total, err := models.ListUsers(int(page), int(limit))
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		out.Data = users
		out.Meta = apiutil.MakePageMeta(limit, total, page)
		return nil
	})
	u.SetTitle("List Users")
	u.SetDescription("Returns a paginated list of all users.")
	u.SetTags("Users")
	return u
}

// UserGetInput identifies a user to retrieve by UUID.
type UserGetInput struct {
	ID string `path:"id" format:"uuid" description:"User UUID."`
}

// UserGetHandler returns the interactor that retrieves a single user by ID.
func UserGetHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in UserGetInput, out *apiutil.Success[*models.User]) error {
		// Parse and validate user ID.
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid user ID")
		}

		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Non-self access requires api:users:read permission.
		if uc.UserID != id && !auth.Can(ctx, uc.UserID, perm.PermUsersRead) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		// Load user from database.
		var user models.User
		if err := db.DB.Where("id = ?", id).First(&user).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("user not found")
		}

		// Build response.
		out.Data = &user
		return nil
	})
	u.SetTitle("Get User")
	u.SetDescription("Returns a single user by ID. Non-self access requires the api:users:read permission.")
	u.SetTags("Users")
	return u
}

// UserCreateInput holds fields for provisioning a new user account.
type UserCreateInput struct {
	Username string `json:"username" required:"true" minLength:"1" description:"Desired username."`
	Email    string `json:"email" required:"true" format:"email" description:"User email address."`
	Password string `json:"password" required:"true" minLength:"1" description:"Initial password."`
	GroupIDs []uint `json:"group_ids,omitempty" description:"Group IDs to add user to. Defaults to the User group if empty."`
}

// UserCreateHandler returns the interactor that provisions a new user account.
func UserCreateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in UserCreateInput, out *apiutil.Success[*models.User]) error {
		// Hash password.
		hash, err := auth.HashPassword(in.Password)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Resolve group IDs. Default to the "User" group when none are specified.
		// A deleted group is revived under its old ID when one of the same name
		// is created, so a membership written for it would come back as one
		// nobody gave the new group.
		groupIDs := in.GroupIDs
		if len(groupIDs) == 0 {
			var userGroup models.UserGroup
			if err := db.DB.Where("name = ?", "User").First(&userGroup).Error; err == nil {
				groupIDs = []uint{userGroup.ID}
			}
		}
		for _, gid := range in.GroupIDs {
			var group models.UserGroup
			if err := db.DB.First(&group, gid).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return apperrors.ErrNotFound.WithMessage(fmt.Sprintf("group %d not found", gid))
				}
				return apperrors.ErrInternal.WithError(err)
			}
		}

		// Create the user with its group memberships, so a membership that
		// cannot be written does not leave an account in no group.
		user := &models.User{
			ID:           uuid.New(),
			Username:     in.Username,
			Email:        in.Email,
			PasswordHash: hash,
		}
		err = db.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(user).Error; err != nil {
				return duplicateOr(fmt.Errorf("failed to create user: %w", err), "a user with this username or email already exists")
			}
			for _, gid := range groupIDs {
				member := &models.UserGroupMember{GroupID: gid, UserID: user.ID}
				if err := tx.Where("group_id = ? AND user_id = ?", gid, user.ID).FirstOrCreate(member).Error; err != nil {
					return apperrors.ErrInternal.WithError(fmt.Errorf("failed to add user to group %d: %w", gid, err))
				}
			}
			return nil
		})
		if err != nil {
			return err
		}

		// Build response.
		out.Data = user
		return nil
	})
	u.SetTitle("Create User")
	u.SetDescription("Provisions a new user account with the given credentials.")
	u.SetTags("Users")
	return u
}

// UserUpdateInput holds the user ID and optional fields for patching user profile data.
type UserUpdateInput struct {
	ID       string  `path:"id" format:"uuid" description:"User UUID."`
	Username *string `json:"username,omitempty" minLength:"1" description:"New username."`
	Email    *string `json:"email,omitempty" format:"email" description:"New email address."`
}

// UserUpdateHandler returns the interactor that patches user profile fields.
func UserUpdateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in UserUpdateInput, out *apiutil.Success[*models.User]) error {
		// Parse and validate user ID.
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid user ID")
		}

		// Verify authentication and check permissions.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Non-self access requires api:users:write permission.
		if uc.UserID != id && !auth.Can(ctx, uc.UserID, perm.PermUsersWrite) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		// Load user from database.
		var user models.User
		if err := db.DB.Where("id = ?", id).First(&user).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("user not found")
		}

		// Apply requested field updates.
		if in.Username != nil {
			user.Username = *in.Username
		}
		if in.Email != nil {
			user.Email = *in.Email
		}

		// Persist changes.
		if err := db.DB.Save(&user).Error; err != nil {
			return duplicateOr(err, "a user with this username or email already exists")
		}

		// Build response.
		out.Data = &user
		return nil
	})
	u.SetTitle("Update User")
	u.SetDescription("Patches user profile fields. Non-self access requires the api:users:write permission.")
	u.SetTags("Users")
	return u
}

// UserDeleteInput identifies a user to remove by UUID.
type UserDeleteInput struct {
	ID string `path:"id" format:"uuid" description:"User UUID."`
}

// UserDeleteHandler returns the interactor that removes a user account.
func UserDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in UserDeleteInput, out *apiutil.Success[any]) error {
		// Parse user ID.
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid user ID")
		}

		// Delete the account with everything that would keep authorizing it:
		// SQLite runs without foreign keys, so a session, an API key, a role
		// assignment, or a group membership left behind would keep working
		// for a user who no longer exists.
		err = db.DB.Transaction(func(tx *gorm.DB) error {
			for _, rows := range []any{&models.Session{}, &models.APIKey{}, &models.RoleAssignment{}, &models.UserGroupMember{}, &models.DashboardAccess{}, &models.DashboardGroupAccess{}, &models.FlowAccess{}, &models.FolderAccess{}, &models.ConnectionAccess{}, &models.AgentAccess{}} {
				if err := tx.Where("user_id = ?", id).Delete(rows).Error; err != nil {
					return err
				}
			}
			res := tx.Delete(&models.User{}, "id = ?", id)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
			return nil
		})
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperrors.ErrNotFound.WithMessage("user not found")
		}
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		out.Message = "User deleted"
		return nil
	})
	u.SetTitle("Delete User")
	u.SetDescription("Deletes a user account by ID.")
	u.SetTags("Users")
	return u
}

// UserChangePasswordInput holds the user ID, old password, and new password for a password change.
type UserChangePasswordInput struct {
	ID          string `path:"id" format:"uuid" description:"User UUID."`
	OldPassword string `json:"old_password" description:"Current password for verification."`
	NewPassword string `json:"new_password" required:"true" minLength:"1" description:"New password to set."`
}

// UserChangePasswordHandler returns the interactor that updates a user's password.
func UserChangePasswordHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in UserChangePasswordInput, out *apiutil.Success[any]) error {
		// Parse and validate user ID.
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid user ID")
		}

		// Verify authentication and check permissions.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Non-self access requires api:users:write permission.
		if uc.UserID != id && !auth.Can(ctx, uc.UserID, perm.PermUsersWrite) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		// Self-service changes must prove knowledge of the current password.
		if uc.UserID == id {
			var user models.User
			if err := db.DB.Where("id = ?", id).First(&user).Error; err != nil {
				return apperrors.ErrNotFound.WithMessage("user not found")
			}
			if !auth.CheckPassword(user.PasswordHash, in.OldPassword) {
				return apperrors.ErrValidation.WithMessage("old password is incorrect")
			}
		}

		// Load user for update.
		var user models.User
		if err := db.DB.Where("id = ?", id).First(&user).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("user not found")
		}

		// Hash new password.
		hash, err := auth.HashPassword(in.NewPassword)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Persist changes.
		user.PasswordHash = hash
		if err := db.DB.Save(&user).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// A new password ends the sessions the old one opened; whoever holds
		// one is who the change is meant to shut out. A self-service change
		// keeps the session that made it.
		if uc.UserID == id {
			_, err = models.DeleteOtherUserSessions(id, uc.SessionID)
		} else {
			err = models.DeleteAllUserSessions(id)
		}
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		out.Message = "Password updated"
		return nil
	})
	u.SetTitle("Change Password")
	u.SetDescription("Updates a user's password. Self-service changes require the old password for verification.")
	u.SetTags("Users")
	return u
}

// RegisterUserRoutes registers user management endpoints.
func RegisterUserRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermUsersRead), bearerDoc).
		Method(http.MethodGet, "/users",
			nethttp.NewHandler(UserListHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodGet, "/users/{id}",
			nethttp.NewHandler(UserGetHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodPut, "/users/{id}",
			nethttp.NewHandler(UserUpdateHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodPut, "/users/{id}/password",
			nethttp.NewHandler(UserChangePasswordHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermUsersWrite), bearerDoc).
		Method(http.MethodPost, "/users",
			nethttp.NewHandler(UserCreateHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermUsersWrite), bearerDoc).
		Method(http.MethodDelete, "/users/{id}",
			nethttp.NewHandler(UserDeleteHandler()))
}
