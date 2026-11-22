package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
	"gorm.io/gorm"
)

// sameFolder reports whether two optional folder IDs name the same place.
func sameFolder(a, b *uint) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// resolveParentFolder validates an optional parent folder reference, returning
// the value to store. Callers pass the folder being moved as self so a folder
// cannot become its own ancestor; self is zero when creating. The parent's
// owner and shares will reach the folder, so the caller needs edit on it.
func resolveParentFolder(ctx context.Context, parentID *uint, self uint) (*uint, error) {
	if parentID == nil {
		return nil, nil
	}
	if err := flowops.CheckFolderTarget(ctx, callerID(ctx), parentID); err != nil {
		return nil, apperrors.ErrValidation.WithMessage("parent folder not found, or you may not add to it")
	}
	if self != 0 {
		if *parentID == self {
			return nil, apperrors.ErrValidation.WithMessage("a folder cannot contain itself")
		}
		descendant, err := models.FolderIsDescendantOf(*parentID, self)
		if err != nil {
			return nil, apperrors.ErrInternal.WithError(err)
		}
		if descendant {
			return nil, apperrors.ErrValidation.WithMessage("a folder cannot be moved into its own subtree")
		}
	}
	return parentID, nil
}

// FolderListHandler returns the interactor that lists the folders the caller
// may see.
func FolderListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]*models.Folder]) error {
		folders, err := flowops.ListFoldersFor(ctx, callerID(ctx))
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Data = folders
		return nil
	})
	u.SetTitle("List Folders")
	u.SetDescription("Returns the folders the caller owns or holds a share on, directly or through a folder above, as a flat list with the caller's access_level; callers assemble the tree from parent_id. A parent the caller cannot see is omitted.")
	u.SetTags("Folders")
	return u
}

// FolderCreateInput holds fields for creating a folder.
type FolderCreateInput struct {
	Name     string `json:"name" required:"true" minLength:"1" description:"Folder name."`
	ParentID *uint  `json:"parent_id,omitempty" description:"Parent folder ID; omit to create at the root."`
}

// FolderCreateHandler returns the interactor that creates a folder.
func FolderCreateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FolderCreateInput, out *apiutil.Success[*models.Folder]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Validate the parent reference.
		parentID, err := resolveParentFolder(ctx, in.ParentID, 0)
		if err != nil {
			return err
		}

		// Persist the folder.
		folder := &models.Folder{Name: in.Name, ParentID: parentID, OwnerID: uc.UserID}
		if err := db.DB.Create(folder).Error; err != nil {
			return apperrors.ErrInternal.WithError(fmt.Errorf("failed to create folder: %w", err))
		}

		folder.AccessLevel = models.FlowAccessAdmin
		out.Data = folder
		return nil
	})
	u.SetTitle("Create Folder")
	u.SetDescription("Creates a folder, optionally nested under a parent folder.")
	u.SetTags("Folders")
	return u
}

// FolderUpdateInput carries the full folder state. Both fields are applied on
// every call, so omitting parent_id moves the folder to the root.
type FolderUpdateInput struct {
	ID       uint   `path:"id" description:"Folder ID."`
	Name     string `json:"name" required:"true" minLength:"1" description:"Folder name."`
	ParentID *uint  `json:"parent_id,omitempty" description:"Parent folder ID; omit to move to the root."`
}

// FolderUpdateHandler returns the interactor that renames or moves a folder.
func FolderUpdateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FolderUpdateInput, out *apiutil.Success[*models.Folder]) error {
		// Load the folder. Renaming needs edit; moving it changes whose shares
		// reach everything inside, so it needs admin, as for a flow.
		uid := callerID(ctx)
		folder, err := flowops.AuthorizeFolder(ctx, uid, in.ID, models.FlowAccessEdit)
		if err != nil {
			return err
		}

		// The list shows a parent the caller cannot see as the root, so the
		// root sent back for such a folder means "where it is", not a move.
		shownParent := folder.ParentID
		if shownParent != nil && flowops.FolderRank(ctx, uid, *shownParent) < models.FlowAccessRank(models.FlowAccessView) {
			shownParent = nil
		}
		parentID := folder.ParentID
		if !sameFolder(shownParent, in.ParentID) {
			if _, err := flowops.AuthorizeFolder(ctx, uid, in.ID, models.FlowAccessAdmin); err != nil {
				return err
			}
			// Validate the new parent, rejecting cycles.
			if parentID, err = resolveParentFolder(ctx, in.ParentID, in.ID); err != nil {
				return err
			}
		}

		// Persist both fields; this endpoint replaces rather than patches.
		folder.Name = in.Name
		folder.ParentID = parentID
		if err := db.DB.Model(folder).Select("name", "parent_id").Updates(map[string]any{
			"name": folder.Name, "parent_id": folder.ParentID,
		}).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Answer as the list does: a parent the caller cannot see is not named.
		if folder.ParentID != nil && flowops.FolderRank(ctx, uid, *folder.ParentID) < models.FlowAccessRank(models.FlowAccessView) {
			folder.ParentID = nil
		}
		folder.AccessLevel = models.FlowAccessLevel(flowops.FolderRank(ctx, uid, folder.ID))
		out.Data = folder
		return nil
	})
	u.SetTitle("Update Folder")
	u.SetDescription("Renames a folder and sets its parent. Moving a folder into its own subtree is rejected.")
	u.SetTags("Folders")
	return u
}

// FolderDeleteInput identifies a folder to remove.
type FolderDeleteInput struct {
	ID uint `path:"id" description:"Folder ID."`
}

// FolderDeleteHandler returns the interactor that removes an empty folder.
func FolderDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FolderDeleteInput, out *apiutil.Success[any]) error {
		// Deleting needs admin on the folder.
		if _, err := flowops.AuthorizeFolder(ctx, callerID(ctx), in.ID, models.FlowAccessAdmin); err != nil {
			return err
		}

		// Refuse to discard contents; the caller moves them out first.
		subfolders, flows, err := models.FolderChildCounts(in.ID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		if subfolders > 0 || flows > 0 {
			return apperrors.ErrConflict.WithMessage(fmt.Sprintf(
				"folder is not empty: it holds %d folder(s) and %d flow(s)", subfolders, flows))
		}

		// Remove the folder and the shares granted on it.
		err = db.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("folder_id = ?", in.ID).Delete(&models.FolderAccess{}).Error; err != nil {
				return err
			}
			return tx.Delete(&models.Folder{}, in.ID).Error
		})
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Message = "Folder deleted"
		return nil
	})
	u.SetTitle("Delete Folder")
	u.SetDescription("Deletes an empty folder. A folder holding subfolders or flows is rejected.")
	u.SetTags("Folders")
	return u
}

// RegisterFolderRoutes registers folder endpoints. Folders organize flows, so
// they share the flow permissions rather than defining their own.
func RegisterFolderRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsRead), bearerDoc).
		Method(http.MethodGet, "/folders",
			nethttp.NewHandler(FolderListHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsWrite), bearerDoc).
		Method(http.MethodPost, "/folders",
			nethttp.NewHandler(FolderCreateHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsWrite), bearerDoc).
		Method(http.MethodPut, "/folders/{id}",
			nethttp.NewHandler(FolderUpdateHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsWrite), bearerDoc).
		Method(http.MethodDelete, "/folders/{id}",
			nethttp.NewHandler(FolderDeleteHandler()))
}
