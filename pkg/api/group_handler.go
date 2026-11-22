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
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// GroupListInput holds pagination parameters for listing user groups.
type GroupListInput struct {
	Page  int64 `query:"page" default:"1" minimum:"1" description:"Page number."`
	Limit int64 `query:"limit" default:"20" minimum:"1" maximum:"100" description:"Number of items per page."`
}

// GroupListHandler returns the interactor that lists user groups with pagination.
func GroupListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in GroupListInput, out *apiutil.Success[[]*models.UserGroup]) error {
		// Query groups.
		groups, total, err := models.ListUserGroups(int(in.Page), int(in.Limit))
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		out.Data = groups
		out.Meta = apiutil.MakePageMeta(in.Limit, total, in.Page)
		return nil
	})
	u.SetTitle("List Groups")
	u.SetDescription("Returns a paginated list of all user groups.")
	u.SetTags("Groups")
	return u
}

// GroupCreateInput holds fields for creating a new user group.
type GroupCreateInput struct {
	Name string `json:"name" required:"true" minLength:"1" description:"Group name."`
}

// GroupCreateHandler returns the interactor that creates a new user group.
func GroupCreateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in GroupCreateInput, out *apiutil.Success[*models.UserGroup]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Create the group.
		group := &models.UserGroup{Name: in.Name}
		if err := group.Create(); err != nil {
			return duplicateOr(err, "a group with this name already exists")
		}

		// Assign the creating user as the initial owner, so the group always has
		// someone able to manage it.
		owner := &models.GroupOwner{GroupID: group.ID, UserID: &uc.UserID}
		if err := owner.Create(); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		out.Data = group
		return nil
	})
	u.SetTitle("Create Group")
	u.SetDescription("Creates a new user group and assigns the authenticated user as its initial owner.")
	u.SetTags("Groups")
	return u
}

// GroupUpdateInput holds the group ID and optional fields for patching a user group.
type GroupUpdateInput struct {
	ID   uint    `path:"id" description:"Group ID."`
	Name *string `json:"name,omitempty" minLength:"1" description:"New group name."`
}

// GroupUpdateHandler returns the interactor that patches user group fields.
func GroupUpdateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in GroupUpdateInput, out *apiutil.Success[*models.UserGroup]) error {
		// Load existing group.
		var group models.UserGroup
		if err := db.DB.First(&group, in.ID).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("group not found")
		}

		// Apply requested field updates. Groups are shared with by name and
		// the column has no unique index, so a rename must not take a name
		// another group holds.
		if in.Name != nil && *in.Name != group.Name {
			var taken int64
			if err := db.DB.Model(&models.UserGroup{}).Where("name = ? AND id <> ?", *in.Name, group.ID).Count(&taken).Error; err != nil {
				return apperrors.ErrInternal.WithError(err)
			}
			if taken > 0 {
				return apperrors.ErrConflict.WithMessage("a group with this name already exists")
			}
			group.Name = *in.Name
		}

		// Persist changes.
		if err := db.DB.Save(&group).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		out.Data = &group
		return nil
	})
	u.SetTitle("Update Group")
	u.SetDescription("Patches user group fields by ID.")
	u.SetTags("Groups")
	return u
}

// GroupDeleteInput identifies a group to remove by ID.
type GroupDeleteInput struct {
	ID uint `path:"id" description:"Group ID."`
}

// GroupDeleteHandler returns the interactor that removes a user group.
func GroupDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in GroupDeleteInput, out *apiutil.Success[any]) error {
		var group models.UserGroup
		if err := db.DB.First(&group, in.ID).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("group not found")
		}
		if err := group.Delete(); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Message = "Group deleted"
		return nil
	})
	u.SetTitle("Delete Group")
	u.SetDescription("Deletes a user group along with its members and owners.")
	u.SetTags("Groups")
	return u
}

// GroupGetMembersInput identifies a group whose members to list.
type GroupGetMembersInput struct {
	ID uint `path:"id" description:"Group ID."`
}

// GroupGetMembersHandler returns the interactor that lists members of a group.
func GroupGetMembersHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in GroupGetMembersInput, out *apiutil.Success[[]models.UserGroupMember]) error {
		members, err := models.GetGroupMembers(in.ID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Data = members
		return nil
	})
	u.SetTitle("List Group Members")
	u.SetDescription("Returns all members of the specified group.")
	u.SetTags("Groups")
	return u
}

// GroupAddMemberInput holds the group ID and user ID for adding a group member.
type GroupAddMemberInput struct {
	ID     uint      `path:"id" description:"Group ID."`
	UserID uuid.UUID `json:"user_id" description:"UUID of the user to add."`
}

// GroupAddMemberHandler returns the interactor that adds a user to a group.
func GroupAddMemberHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in GroupAddMemberInput, out *apiutil.Success[*models.UserGroupMember]) error {
		// The group must exist. A deleted group keeps its row and is revived
		// under the same ID by name, so a membership written against it would
		// come back with the new group.
		if err := db.DB.First(&models.UserGroup{}, in.ID).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("group not found")
		}

		// Membership is a composite primary key, so re-adding an existing member
		// is a no-op rather than a conflict.
		if err := userExists(in.UserID); err != nil {
			return err
		}

		member := &models.UserGroupMember{GroupID: in.ID, UserID: in.UserID}
		if err := db.DB.FirstOrCreate(member, "group_id = ? AND user_id = ?", in.ID, in.UserID).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Data = member
		return nil
	})
	u.SetTitle("Add Group Member")
	u.SetDescription("Adds a user as a member of the specified group.")
	u.SetTags("Groups")
	return u
}

// GroupRemoveMemberInput identifies a group member to remove.
type GroupRemoveMemberInput struct {
	ID     uint   `path:"id" description:"Group ID."`
	UserID string `path:"userId" format:"uuid" description:"UUID of the member to remove."`
}

// GroupRemoveMemberHandler returns the interactor that removes a user from a group.
func GroupRemoveMemberHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in GroupRemoveMemberInput, out *apiutil.Success[any]) error {
		// Parse member user ID.
		userID, err := uuid.Parse(in.UserID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid user ID")
		}

		// Delete membership record.
		tx := db.DB.Where("group_id = ? AND user_id = ?", in.ID, userID).
			Delete(&models.UserGroupMember{})
		if tx.Error != nil {
			return apperrors.ErrInternal.WithError(tx.Error)
		}
		if tx.RowsAffected == 0 {
			return apperrors.ErrNotFound.WithMessage("the user is not a member of this group")
		}

		// Build response.
		out.Message = "Member removed"
		return nil
	})
	u.SetTitle("Remove Group Member")
	u.SetDescription("Removes a user from the specified group's membership.")
	u.SetTags("Groups")
	return u
}

// GroupListOwnersInput identifies a group whose owners to list.
type GroupListOwnersInput struct {
	ID uint `path:"id" description:"Group ID."`
}

// GroupListOwnersHandler returns the interactor that lists owners of a group.
func GroupListOwnersHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in GroupListOwnersInput, out *apiutil.Success[[]models.GroupOwner]) error {
		owners, err := models.ListGroupOwners(in.ID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Data = owners
		return nil
	})
	u.SetTitle("List Group Owners")
	u.SetDescription("Returns all owners of the specified group.")
	u.SetTags("Groups")
	return u
}

// GroupAddOwnerInput holds the group ID and user or group to assign as owner.
type GroupAddOwnerInput struct {
	ID           uint       `path:"id" description:"Group ID."`
	UserID       *uuid.UUID `json:"user_id,omitempty" description:"UUID of the user to assign as owner."`
	OwnerGroupID *uint      `json:"owner_group_id,omitempty" description:"ID of the group to assign as owner."`
}

// GroupAddOwnerHandler returns the interactor that assigns a user or group as owner.
func GroupAddOwnerHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in GroupAddOwnerInput, out *apiutil.Success[*models.GroupOwner]) error {
		// Validate input.
		if (in.UserID == nil) == (in.OwnerGroupID == nil) {
			return apperrors.ErrValidation.WithMessage("give either user_id or owner_group_id")
		}

		// The owned group and an owning group must both exist, for the reason
		// a membership's group must.
		if err := db.DB.First(&models.UserGroup{}, in.ID).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("group not found")
		}
		if in.OwnerGroupID != nil {
			if err := db.DB.First(&models.UserGroup{}, *in.OwnerGroupID).Error; err != nil {
				return apperrors.ErrNotFound.WithMessage("owner group not found")
			}
		}

		// Create owner record.
		if in.UserID != nil {
			if err := userExists(*in.UserID); err != nil {
				return err
			}
		}

		owner := &models.GroupOwner{
			GroupID:      in.ID,
			UserID:       in.UserID,
			OwnerGroupID: in.OwnerGroupID,
		}
		if err := owner.Create(); err != nil {
			return duplicateOr(err, "the group already has this owner")
		}

		// Build response.
		out.Data = owner
		return nil
	})
	u.SetTitle("Add Group Owner")
	u.SetDescription("Assigns a user or group as an owner of the specified group.")
	u.SetTags("Groups")
	return u
}

// GroupRemoveOwnerInput identifies a group owner to remove by owner record ID.
type GroupRemoveOwnerInput struct {
	ID      uint `path:"id" description:"Group ID."`
	OwnerID uint `path:"ownerId" description:"Owner record ID."`
}

// GroupRemoveOwnerHandler returns the interactor that removes an owner from a group.
func GroupRemoveOwnerHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in GroupRemoveOwnerInput, out *apiutil.Success[any]) error {
		tx := db.DB.Where("group_id = ?", in.ID).
			Delete(&models.GroupOwner{}, in.OwnerID)
		if tx.Error != nil {
			return apperrors.ErrInternal.WithError(tx.Error)
		}
		if tx.RowsAffected == 0 {
			return apperrors.ErrNotFound.WithMessage("the group has no such owner entry")
		}

		out.Message = "Owner removed"
		return nil
	})
	u.SetTitle("Remove Group Owner")
	u.SetDescription("Removes an owner assignment from the group by owner record ID.")
	u.SetTags("Groups")
	return u
}

// RegisterGroupRoutes registers user group management endpoints.
func RegisterGroupRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermGroupsRead), bearerDoc).
		Method(http.MethodGet, "/groups",
			nethttp.NewHandler(GroupListHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermGroupsRead), bearerDoc).
		Method(http.MethodGet, "/groups/{id}/members",
			nethttp.NewHandler(GroupGetMembersHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermGroupsRead), bearerDoc).
		Method(http.MethodGet, "/groups/{id}/owners",
			nethttp.NewHandler(GroupListOwnersHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermGroupsWrite), bearerDoc).
		Method(http.MethodPost, "/groups",
			nethttp.NewHandler(GroupCreateHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermGroupsWrite), bearerDoc).
		Method(http.MethodPut, "/groups/{id}",
			nethttp.NewHandler(GroupUpdateHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermGroupsWrite), bearerDoc).
		Method(http.MethodPost, "/groups/{id}/members",
			nethttp.NewHandler(GroupAddMemberHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermGroupsWrite), bearerDoc).
		Method(http.MethodDelete, "/groups/{id}/members/{userId}",
			nethttp.NewHandler(GroupRemoveMemberHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermGroupsWrite), bearerDoc).
		Method(http.MethodPost, "/groups/{id}/owners",
			nethttp.NewHandler(GroupAddOwnerHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermGroupsWrite), bearerDoc).
		Method(http.MethodDelete, "/groups/{id}/owners/{ownerId}",
			nethttp.NewHandler(GroupRemoveOwnerHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermGroupsWrite), bearerDoc).
		Method(http.MethodDelete, "/groups/{id}",
			nethttp.NewHandler(GroupDeleteHandler()))
}
