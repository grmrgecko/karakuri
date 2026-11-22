package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/dashops"
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

// DashboardGroupListHandler returns the interactor that lists accessible dashboard groups.
func DashboardGroupListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]*models.DashboardGroup]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Resolve user's group memberships.
		groupIDs, err := models.GetUserGroupIDs(uc.UserID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Query accessible dashboard groups.
		groups, err := models.ListDashboardGroupsByUser(uc.UserID, groupIDs)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		out.Data = groups
		return nil
	})
	u.SetTitle("List Dashboard Groups")
	u.SetDescription("Returns all dashboard groups accessible to the authenticated user.")
	u.SetTags("Dashboard Groups")
	return u
}

// DashboardGroupCreateInput holds fields for creating a new dashboard group.
type DashboardGroupCreateInput struct {
	Name        string `json:"name" required:"true" minLength:"1" description:"Dashboard group name."`
	Description string `json:"description" description:"Optional description of the group."`
	ParentID    *uint  `json:"parent_id,omitempty" description:"Parent group ID; absent creates it at the root."`
}

// DashboardGroupCreateHandler returns the interactor that creates a new dashboard group.
func DashboardGroupCreateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardGroupCreateInput, out *apiutil.Success[*models.DashboardGroup]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Validate the parent before creating beneath it.
		if in.ParentID != nil && *in.ParentID != 0 {
			if _, err := models.FindDashboardGroupByID(*in.ParentID); err != nil {
				return apperrors.ErrValidation.WithMessage("parent dashboard group not found")
			}
		}

		// Create dashboard group.
		group := &models.DashboardGroup{
			Name:        in.Name,
			Description: in.Description,
			OwnerID:     uc.UserID,
		}
		if in.ParentID != nil && *in.ParentID != 0 {
			group.ParentID = in.ParentID
		}

		if err := db.DB.Create(group).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		out.Data = group
		return nil
	})
	u.SetTitle("Create Dashboard Group")
	u.SetDescription("Creates a new dashboard group owned by the authenticated user.")
	u.SetTags("Dashboard Groups")
	return u
}

// DashboardGroupUpdateInput holds the group ID and optional fields for patching a dashboard group.
type DashboardGroupUpdateInput struct {
	ID          uint    `path:"id" description:"Dashboard group ID."`
	Name        *string `json:"name,omitempty" minLength:"1" description:"Dashboard group name."`
	Description *string `json:"description,omitempty" description:"Group description."`
	ParentID    *uint   `json:"parent_id,omitempty" description:"Parent group ID; set to 0 to move it to the root."`
}

// DashboardGroupUpdateHandler returns the interactor that patches dashboard group fields.
func DashboardGroupUpdateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardGroupUpdateInput, out *apiutil.Success[*models.DashboardGroup]) error {
		// Load existing group.
		group, err := models.FindDashboardGroupByID(in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard group not found")
		}

		// Enforce ownership: a share at the needed level or the manage permission passes too.
		uc := middleware.GetUser(ctx)
		if uc == nil || !dashops.CanEditGroup(ctx, uc.UserID, group) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		// Apply requested field updates.
		if in.Name != nil {
			group.Name = *in.Name
		}
		if in.Description != nil {
			group.Description = *in.Description
		}

		// Re-parent, refusing a move that would put the group inside its own
		// subtree and detach that subtree from the root.
		if in.ParentID != nil {
			if *in.ParentID == 0 {
				group.ParentID = nil
			} else {
				if *in.ParentID == group.ID {
					return apperrors.ErrValidation.WithMessage("a group cannot be its own parent")
				}
				if _, err := models.FindDashboardGroupByID(*in.ParentID); err != nil {
					return apperrors.ErrValidation.WithMessage("parent dashboard group not found")
				}
				descendant, err := models.DashboardGroupIsDescendantOf(*in.ParentID, group.ID)
				if err != nil {
					return apperrors.ErrInternal.WithError(err)
				}
				if descendant {
					return apperrors.ErrValidation.WithMessage("cannot move a group into its own subtree")
				}
				group.ParentID = in.ParentID
			}
		}

		// Persist changes.
		if err := db.DB.Save(group).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		out.Data = group
		return nil
	})
	u.SetTitle("Update Dashboard Group")
	u.SetDescription("Updates an existing dashboard group. Only the owner or users with api:dashboard-group:write permission may update.")
	u.SetTags("Dashboard Groups")
	return u
}

// DashboardGroupDeleteInput identifies a dashboard group to remove by ID.
type DashboardGroupDeleteInput struct {
	ID uint `path:"id" description:"Dashboard group ID."`
}

// DashboardGroupDeleteHandler returns the interactor that removes a dashboard group.
func DashboardGroupDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardGroupDeleteInput, out *apiutil.Success[any]) error {
		// Load existing group.
		group, err := models.FindDashboardGroupByID(in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard group not found")
		}

		// Enforce ownership: a share at the needed level or the manage permission passes too.
		uc := middleware.GetUser(ctx)
		if uc == nil || !dashops.CanManageGroup(ctx, uc.UserID, group) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		// Delete group.
		if err := group.Delete(); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		out.Message = "Dashboard group deleted"
		return nil
	})
	u.SetTitle("Delete Dashboard Group")
	u.SetDescription("Deletes a dashboard group. Only the owner or users with api:dashboard-group:delete permission may delete.")
	u.SetTags("Dashboard Groups")
	return u
}

// DashboardGroupListAccessInput identifies a dashboard group whose access entries to list.
type DashboardGroupListAccessInput struct {
	ID uint `path:"id" description:"Dashboard group ID."`
}

// DashboardGroupListAccessHandler returns the interactor that lists sharing entries for a dashboard group.
func DashboardGroupListAccessHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardGroupListAccessInput, out *apiutil.Success[[]models.DashboardGroupAccess]) error {
		// Load dashboard group.
		group, err := models.FindDashboardGroupByID(in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard group not found")
		}

		// Shares are listed to the same people who may edit the group.
		uc := middleware.GetUser(ctx)
		if uc == nil || !dashops.CanEditGroup(ctx, uc.UserID, group) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		// Query access entries.
		entries, err := models.ListDashboardGroupAccess(in.ID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Name each grantee without their account fields.
		for i := range entries {
			entries[i].User = shareGrantee(entries[i].User)
		}

		// Build response.
		out.Data = entries
		return nil
	})
	u.SetTitle("List Dashboard Group Access")
	u.SetDescription("Returns all sharing/access entries for a dashboard group.")
	u.SetTags("Dashboard Groups")
	return u
}

// DashboardGroupAddAccessInput holds the group ID and share details for adding access.
type DashboardGroupAddAccessInput struct {
	ID         uint   `path:"id" description:"Dashboard group ID."`
	Username   string `json:"username,omitempty" description:"Username to share with."`
	GroupName  string `json:"group_name,omitempty" description:"User group name to share with."`
	Permission string `json:"permission" enum:"read,view,edit,admin" default:"view" description:"Access level: read shows the dashboard without operating it, view also operates it, edit changes and deploys it, admin shares and deletes it."`
}

// DashboardGroupAddAccessHandler returns the interactor that shares a dashboard group with a user or group.
func DashboardGroupAddAccessHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardGroupAddAccessInput, out *apiutil.Success[*models.DashboardGroupAccess]) error {
		// Load dashboard group.
		group, err := models.FindDashboardGroupByID(in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard group not found")
		}

		// Enforce ownership.
		uc := middleware.GetUser(ctx)
		if uc == nil || !dashops.CanManageGroup(ctx, uc.UserID, group) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		// Validate input.
		// Exactly one grantee: a row naming both would be one share that two
		// removals each take away from the other.
		if (in.Username == "") == (in.GroupName == "") {
			return apperrors.ErrValidation.WithMessage("give either username or group_name")
		}

		// Set default permission level.
		permission := in.Permission
		if permission == "" {
			permission = models.DashboardAccessView
		}
		if !models.ValidDashboardAccess(permission) {
			return apperrors.ErrValidation.WithMessage("permission must be read, view, edit, or admin")
		}

		// Build access record.
		access := &models.DashboardGroupAccess{
			DashboardGroupID: in.ID,
			Permission:       permission,
		}

		// Resolve user or group by name.
		if in.Username != "" {
			var user models.User
			if err := db.DB.Where("username = ?", in.Username).First(&user).Error; err != nil {
				return apperrors.ErrValidation.WithMessage("user not found")
			}
			access.UserID = &user.ID
		}

		if in.GroupName != "" {
			var ug models.UserGroup
			if err := db.DB.Where("name = ?", in.GroupName).First(&ug).Error; err != nil {
				return apperrors.ErrValidation.WithMessage("group not found")
			}
			access.GroupID = &ug.ID
		}

		// Persist to database.
		if err := db.DB.Create(access).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		out.Data = access
		return nil
	})
	u.SetTitle("Add Dashboard Group Access")
	u.SetDescription("Shares a dashboard group with a user or group by name.")
	u.SetTags("Dashboard Groups")
	return u
}

// DashboardGroupRemoveAccessInput identifies an access entry to remove from a dashboard group.
type DashboardGroupRemoveAccessInput struct {
	ID      uint `path:"id" description:"Dashboard group ID."`
	ShareID uint `path:"shareId" description:"Share entry ID."`
}

// DashboardGroupRemoveAccessHandler returns the interactor that revokes a sharing entry from a dashboard group.
func DashboardGroupRemoveAccessHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardGroupRemoveAccessInput, out *apiutil.Success[any]) error {
		// Load dashboard group.
		group, err := models.FindDashboardGroupByID(in.ID)
		if err != nil {
			return apperrors.ErrNotFound.WithMessage("dashboard group not found")
		}

		// Enforce ownership.
		uc := middleware.GetUser(ctx)
		if uc == nil || !dashops.CanManageGroup(ctx, uc.UserID, group) {
			return apperrors.ErrForbidden.WithMessage("forbidden")
		}

		// Delete the entry, and only if it belongs to this group.
		tx := db.DB.Where("dashboard_group_id = ?", group.ID).Delete(&models.DashboardGroupAccess{}, in.ShareID)
		if tx.Error != nil {
			return apperrors.ErrInternal.WithError(tx.Error)
		}
		if tx.RowsAffected == 0 {
			return apperrors.ErrNotFound.WithMessage("share not found")
		}

		// Build response.
		out.Message = "Share removed"
		return nil
	})
	u.SetTitle("Remove Dashboard Group Access")
	u.SetDescription("Removes a sharing/access entry from a dashboard group.")
	u.SetTags("Dashboard Groups")
	return u
}

// ReorderGroupItem specifies the new sort position for a dashboard group.
type ReorderGroupItem struct {
	ID        uint `json:"id" description:"Dashboard group ID."`
	SortOrder int  `json:"sort_order" description:"New sort position."`
}

// DashboardGroupReorderInput holds the list of group reorder entries.
type DashboardGroupReorderInput struct {
	Items []ReorderGroupItem `json:"items" required:"true" minItems:"1" description:"List of group reorder entries."`
}

// DashboardGroupReorderHandler returns the interactor that reorders dashboard groups in a single transaction.
func DashboardGroupReorderHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardGroupReorderInput, out *apiutil.Success[any]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Authorize every item first. The checks read through db.DB, which
		// must not wait on the connection the transaction below holds.
		for _, item := range in.Items {
			var group models.DashboardGroup
			if err := db.DB.Select("id", "owner_id").First(&group, item.ID).Error; err != nil {
				return apperrors.ErrNotFound.WithMessage(fmt.Sprintf("dashboard group not found: %d", item.ID))
			}
			if !dashops.CanEditGroup(ctx, uc.UserID, &group) {
				return apperrors.ErrForbidden.WithMessage(fmt.Sprintf("forbidden: cannot reorder group %d", item.ID))
			}
		}

		// Execute reorder in a single transaction.
		err := db.DB.Transaction(func(tx *gorm.DB) error {
			for _, item := range in.Items {
				if err := tx.Model(&models.DashboardGroup{}).Where("id = ?", item.ID).
					Update("sort_order", item.SortOrder).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Build response.
		out.Message = "Dashboard groups reordered"
		return nil
	})
	u.SetTitle("Reorder Dashboard Groups")
	u.SetDescription("Updates the sort order of multiple dashboard groups in a single transaction.")
	u.SetTags("Dashboard Groups")
	return u
}

// RegisterDashboardGroupRoutes registers dashboard group CRUD and sharing endpoints.
func RegisterDashboardGroupRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	// Dashboard group reads.
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardGroupsRead), bearerDoc).
		Method(http.MethodGet, "/dashboard_groups",
			nethttp.NewHandler(DashboardGroupListHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardGroupsRead), bearerDoc).
		Method(http.MethodGet, "/dashboard_groups/{id}/shares",
			nethttp.NewHandler(DashboardGroupListAccessHandler()))

	// Dashboard group reorder.
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardGroupsWrite), bearerDoc).
		Method(http.MethodPut, "/dashboard_groups/reorder",
			nethttp.NewHandler(DashboardGroupReorderHandler()))

	// Dashboard group writes.
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardGroupsWrite), bearerDoc).
		Method(http.MethodPost, "/dashboard_groups",
			nethttp.NewHandler(DashboardGroupCreateHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardGroupsWrite), bearerDoc).
		Method(http.MethodPut, "/dashboard_groups/{id}",
			nethttp.NewHandler(DashboardGroupUpdateHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardGroupsWrite), bearerDoc).
		Method(http.MethodPost, "/dashboard_groups/{id}/shares",
			nethttp.NewHandler(DashboardGroupAddAccessHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardGroupsWrite), bearerDoc).
		Method(http.MethodDelete, "/dashboard_groups/{id}/shares/{shareId}",
			nethttp.NewHandler(DashboardGroupRemoveAccessHandler()))

	// Dashboard group deletes.
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardGroupsDelete), bearerDoc).
		Method(http.MethodDelete, "/dashboard_groups/{id}",
			nethttp.NewHandler(DashboardGroupDeleteHandler()))
}
