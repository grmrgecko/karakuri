package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

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
	"gorm.io/gorm"
)

// RoleListHandler returns the interactor that lists all roles.
func RoleListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]*models.Role]) error {
		var roles []*models.Role
		if err := db.DB.Order("name ASC").Find(&roles).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Data = roles
		return nil
	})
	u.SetTitle("List Roles")
	u.SetDescription("Returns all roles ordered by name.")
	u.SetTags("Roles")
	return u
}

// RoleCreateInput holds fields for creating a new role.
type RoleCreateInput struct {
	Name        string `json:"name" required:"true" minLength:"1" description:"Role display name."`
	Description string `json:"description" description:"Optional role description."`
}

// RoleCreateHandler returns the interactor that creates a new role.
func RoleCreateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in RoleCreateInput, out *apiutil.Success[*models.Role]) error {
		role := &models.Role{
			Name:        in.Name,
			Description: in.Description,
		}

		if err := role.Create(); err != nil {
			return duplicateOr(err, "a role with this name already exists")
		}

		out.Data = role
		return nil
	})
	u.SetTitle("Create Role")
	u.SetDescription("Creates a new role.")
	u.SetTags("Roles")
	return u
}

// RoleUpdateInput holds the role ID and optional fields for patching a role.
type RoleUpdateInput struct {
	ID          uint    `path:"id" description:"Role ID."`
	Name        *string `json:"name,omitempty" minLength:"1" description:"Role display name."`
	Description *string `json:"description,omitempty" description:"Role description."`
}

// RoleUpdateHandler returns the interactor that patches role fields.
func RoleUpdateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in RoleUpdateInput, out *apiutil.Success[*models.Role]) error {
		var role models.Role
		if err := db.DB.First(&role, in.ID).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("role not found")
		}

		if in.Name != nil {
			role.Name = *in.Name
		}
		if in.Description != nil {
			role.Description = *in.Description
		}

		if err := db.DB.Save(&role).Error; err != nil {
			return duplicateOr(err, "a role with this name already exists")
		}

		out.Data = &role
		return nil
	})
	u.SetTitle("Update Role")
	u.SetDescription("Updates an existing role by ID.")
	u.SetTags("Roles")
	return u
}

// RoleDeleteInput identifies a role to remove by ID.
type RoleDeleteInput struct {
	ID uint `path:"id" description:"Role ID."`
}

// RoleDeleteHandler returns the interactor that removes a role and its permissions.
func RoleDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in RoleDeleteInput, out *apiutil.Success[any]) error {
		var r models.Role
		if err := db.DB.First(&r, in.ID).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("role not found")
		}
		if err := r.Delete(); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Message = "Role deleted"
		return nil
	})
	u.SetTitle("Delete Role")
	u.SetDescription("Deletes a role by ID.")
	u.SetTags("Roles")
	return u
}

// RoleListPermissionsInput identifies a role whose permissions to list.
type RoleListPermissionsInput struct {
	ID uint `path:"id" description:"Role ID."`
}

// RoleListPermissionsHandler returns the interactor that lists permissions for a role.
func RoleListPermissionsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in RoleListPermissionsInput, out *apiutil.Success[[]models.RolePermission]) error {
		var perms []models.RolePermission
		if err := db.DB.Where("role_id = ?", in.ID).Find(&perms).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Data = perms
		return nil
	})
	u.SetTitle("List Role Permissions")
	u.SetDescription("Returns all permission grants for a role.")
	u.SetTags("Roles")
	return u
}

// RoleAddPermissionInput holds the role ID and permission string to grant.
type RoleAddPermissionInput struct {
	ID         uint   `path:"id" description:"Role ID."`
	Permission string `json:"permission" required:"true" minLength:"1" description:"Permission string (e.g. api:flows:read)."`
}

// RoleAddPermissionHandler returns the interactor that grants a permission to a role.
func RoleAddPermissionHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in RoleAddPermissionInput, out *apiutil.Success[*models.RolePermission]) error {
		// The role must exist. A deleted role keeps its row and is revived
		// under the same ID by name, so a permission written against it would
		// come back with the new role.
		if err := db.DB.First(&models.Role{}, in.ID).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("role not found")
		}

		rp := &models.RolePermission{
			RoleID:     in.ID,
			Permission: in.Permission,
		}

		if err := rp.Create(); err != nil {
			return duplicateOr(err, "the role already holds this permission")
		}

		out.Data = rp
		return nil
	})
	u.SetTitle("Add Permission to Role")
	u.SetDescription("Adds a permission grant to a role.")
	u.SetTags("Roles")
	return u
}

// RoleRemovePermissionInput identifies a permission entry to revoke from a role.
type RoleRemovePermissionInput struct {
	ID  uint `path:"id" description:"Role ID."`
	PID uint `path:"pid" description:"Permission entry ID."`
}

// RoleRemovePermissionHandler returns the interactor that revokes a permission from a role.
func RoleRemovePermissionHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in RoleRemovePermissionInput, out *apiutil.Success[any]) error {
		tx := db.DB.Where("role_id = ?", in.ID).Delete(&models.RolePermission{}, in.PID)
		if tx.Error != nil {
			return apperrors.ErrInternal.WithError(tx.Error)
		}
		if tx.RowsAffected == 0 {
			return apperrors.ErrNotFound.WithMessage("the role holds no such permission entry")
		}

		out.Message = "Permission removed"
		return nil
	})
	u.SetTitle("Remove Permission from Role")
	u.SetDescription("Removes a permission grant from a role.")
	u.SetTags("Roles")
	return u
}

// RoleListAssignmentsHandler returns the interactor that lists all role assignments.
func RoleListAssignmentsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]models.RoleAssignment]) error {
		assignments, err := models.ListRoleAssignments()
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Data = assignments
		return nil
	})
	u.SetTitle("List Role Assignments")
	u.SetDescription("Returns all role assignments across users and groups.")
	u.SetTags("Roles")
	return u
}

// RoleCreateAssignmentInput holds fields for assigning a role to a user or group.
type RoleCreateAssignmentInput struct {
	RoleID  uint       `json:"role_id" required:"true" minimum:"1" description:"ID of the role to assign."`
	UserID  *uuid.UUID `json:"user_id,omitempty" description:"User ID to assign the role to."`
	GroupID *uint      `json:"group_id,omitempty" description:"Group ID to assign the role to."`
}

// RoleCreateAssignmentHandler returns the interactor that assigns a role to a user or group.
func RoleCreateAssignmentHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in RoleCreateAssignmentInput, out *apiutil.Success[*models.RoleAssignment]) error {
		// Exactly one assignee: a row naming both dies with either of them
		// and takes the other's role along.
		if (in.UserID == nil) == (in.GroupID == nil) {
			return apperrors.ErrValidation.WithMessage("give either user_id or group_id")
		}

		// The role and a group it is assigned to must exist, for the reason a
		// permission's role must.
		if err := db.DB.First(&models.Role{}, in.RoleID).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("role not found")
		}
		if in.GroupID != nil {
			if err := db.DB.First(&models.UserGroup{}, *in.GroupID).Error; err != nil {
				return apperrors.ErrNotFound.WithMessage("group not found")
			}
		}
		if in.UserID != nil {
			if err := userExists(*in.UserID); err != nil {
				return err
			}
		}

		ra := &models.RoleAssignment{
			RoleID:  in.RoleID,
			UserID:  in.UserID,
			GroupID: in.GroupID,
		}

		if err := ra.Create(); err != nil {
			return duplicateOr(err, "the role is already assigned there")
		}

		out.Data = ra
		return nil
	})
	u.SetTitle("Create Role Assignment")
	u.SetDescription("Assigns a role to a user or group.")
	u.SetTags("Roles")
	return u
}

// RoleDeleteAssignmentInput identifies a role assignment to remove by ID.
type RoleDeleteAssignmentInput struct {
	ID uint `path:"id" description:"Assignment ID."`
}

// RoleDeleteAssignmentHandler returns the interactor that removes a role assignment.
func RoleDeleteAssignmentHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in RoleDeleteAssignmentInput, out *apiutil.Success[any]) error {
		tx := db.DB.Delete(&models.RoleAssignment{}, in.ID)
		if tx.Error != nil {
			return apperrors.ErrInternal.WithError(tx.Error)
		}
		if tx.RowsAffected == 0 {
			return apperrors.ErrNotFound.WithMessage("role assignment not found")
		}

		out.Message = "Assignment deleted"
		return nil
	})
	u.SetTitle("Delete Role Assignment")
	u.SetDescription("Removes a role assignment by ID.")
	u.SetTags("Roles")
	return u
}

// RolePermissionTreeHandler returns the nested permission tree for frontend autocomplete.
func RolePermissionTreeHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[map[string]interface{}]) error {
		out.Data = perm.R.Tree()
		return nil
	})
	u.SetTitle("Permission Tree")
	u.SetDescription("Returns the nested permission tree for frontend autocomplete.")
	u.SetTags("Roles")
	return u
}

// userExists answers 404 for a user ID that names no account, which the
// foreign key would otherwise refuse as an internal error.
func userExists(id uuid.UUID) error {
	var count int64
	if err := db.DB.Model(&models.User{}).Where("id = ?", id).Count(&count).Error; err != nil {
		return apperrors.ErrInternal.WithError(err)
	}
	if count == 0 {
		return apperrors.ErrNotFound.WithMessage("user not found")
	}
	return nil
}

// duplicateOr maps a store failure onto a status: a row that already exists is
// a conflict carrying message, and anything else is an internal error. The
// models report a live duplicate as gorm.ErrDuplicatedKey; a unique index
// reports it in the driver's own words.
func duplicateOr(err error, message string) error {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return apperrors.ErrConflict.WithMessage(message)
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "unique") || strings.Contains(text, "duplicate") {
		return apperrors.ErrConflict.WithMessage(message)
	}
	return apperrors.ErrInternal.WithError(err)
}

// RegisterRoleRoutes registers role management and permission endpoints.
func RegisterRoleRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRolesRead), bearerDoc).
		Method(http.MethodGet, "/roles",
			nethttp.NewHandler(RoleListHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRolesRead), bearerDoc).
		Method(http.MethodGet, "/roles/{id}/permissions",
			nethttp.NewHandler(RoleListPermissionsHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRolesRead), bearerDoc).
		Method(http.MethodGet, "/role_assignments",
			nethttp.NewHandler(RoleListAssignmentsHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRolesRead), bearerDoc).
		Method(http.MethodGet, "/permissions/tree",
			nethttp.NewHandler(RolePermissionTreeHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRolesWrite), bearerDoc).
		Method(http.MethodPost, "/roles",
			nethttp.NewHandler(RoleCreateHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRolesWrite), bearerDoc).
		Method(http.MethodPut, "/roles/{id}",
			nethttp.NewHandler(RoleUpdateHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRolesWrite), bearerDoc).
		Method(http.MethodPost, "/roles/{id}/permissions",
			nethttp.NewHandler(RoleAddPermissionHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRolesWrite), bearerDoc).
		Method(http.MethodDelete, "/roles/{id}/permissions/{pid}",
			nethttp.NewHandler(RoleRemovePermissionHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRolesWrite), bearerDoc).
		Method(http.MethodPost, "/role_assignments",
			nethttp.NewHandler(RoleCreateAssignmentHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRolesWrite), bearerDoc).
		Method(http.MethodDelete, "/role_assignments/{id}",
			nethttp.NewHandler(RoleDeleteAssignmentHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRolesWrite), bearerDoc).
		Method(http.MethodDelete, "/roles/{id}",
			nethttp.NewHandler(RoleDeleteHandler()))
}
