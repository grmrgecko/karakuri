package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// FlowSharesInput identifies the flow whose shares are listed.
type FlowSharesInput struct {
	ID string `path:"id" format:"uuid" description:"Flow UUID."`
}

// FlowSharesHandler returns the interactor that lists who a flow is shared with.
func FlowSharesHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowSharesInput, out *apiutil.Success[[]models.FlowAccess]) error {
		f, err := loadFlow(ctx, in.ID, models.FlowAccessAdmin)
		if err != nil {
			return err
		}
		entries, err := models.ListFlowAccess(f.ID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		for i := range entries {
			entries[i].User = shareGrantee(entries[i].User)
		}
		out.Data = entries
		return nil
	})
	u.SetTitle("List Flow Shares")
	u.SetDescription("Returns the users and groups a flow is shared with and at which level. Needs admin on the flow.")
	u.SetTags("Flows")
	return u
}

// FlowAddShareInput names the flow, the grantee, and the level to share at.
type FlowAddShareInput struct {
	ID         string `path:"id" format:"uuid" description:"Flow UUID."`
	Username   string `json:"username,omitempty" description:"Username to share with."`
	GroupName  string `json:"group_name,omitempty" description:"User group name to share with."`
	Permission string `json:"permission" enum:"faceplate,view,edit,admin" default:"view" description:"Access level: faceplate reads and applies faceplates only, view reads the graph, edit changes, tests, deploys, and runs it, admin also shares and deletes it."`
}

// FlowAddShareHandler returns the interactor that shares a flow with a user or group.
func FlowAddShareHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowAddShareInput, out *apiutil.Success[*models.FlowAccess]) error {
		f, err := loadFlow(ctx, in.ID, models.FlowAccessAdmin)
		if err != nil {
			return err
		}

		// Exactly one grantee, at a known level.
		if (in.Username == "") == (in.GroupName == "") {
			return apperrors.ErrValidation.WithMessage("give either username or group_name")
		}
		permission := in.Permission
		if permission == "" {
			permission = models.FlowAccessView
		}
		if !models.ValidFlowAccess(permission) {
			return apperrors.ErrValidation.WithMessage("permission must be faceplate, view, edit, or admin")
		}

		access := &models.FlowAccess{FlowID: f.ID, Permission: permission}
		grantee := in.Username
		if in.Username != "" {
			var user models.User
			if err := db.DB.Where("username = ?", in.Username).First(&user).Error; err != nil {
				return apperrors.ErrValidation.WithMessage("user not found")
			}
			access.UserID = &user.ID
		} else {
			var group models.UserGroup
			if err := db.DB.Where("name = ?", in.GroupName).First(&group).Error; err != nil {
				return apperrors.ErrValidation.WithMessage("group not found")
			}
			access.GroupID = &group.ID
			grantee = "group " + group.Name
		}
		if err := db.DB.Create(access).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		recordShare(ctx, f, fmt.Sprintf("Shared %s with %s at %s", f.Name, grantee, permission),
			map[string]any{"grantee": grantee, "permission": permission})
		out.Data = access
		return nil
	})
	u.SetTitle("Share Flow")
	u.SetDescription("Shares a flow with a user or group by name at faceplate, view, edit, or admin level. Needs admin on the flow.")
	u.SetTags("Flows")
	return u
}

// FlowRemoveShareInput identifies one share to remove from a flow.
type FlowRemoveShareInput struct {
	ID      string `path:"id" format:"uuid" description:"Flow UUID."`
	ShareID uint   `path:"shareId" description:"Share entry ID."`
}

// FlowRemoveShareHandler returns the interactor that removes a flow share.
func FlowRemoveShareHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowRemoveShareInput, out *apiutil.Success[any]) error {
		f, err := loadFlow(ctx, in.ID, models.FlowAccessAdmin)
		if err != nil {
			return err
		}

		// The share must belong to this flow: the ID in the path must not reach
		// a share on a flow the caller cannot administer.
		var access models.FlowAccess
		if err := db.DB.Preload("User").Preload("Group").Where("flow_id = ? AND id = ?", f.ID, in.ShareID).First(&access).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("share not found")
		}
		if err := db.DB.Delete(&models.FlowAccess{}, access.ID).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		grantee := "a removed account"
		if access.User != nil {
			grantee = access.User.Username
		} else if access.Group != nil {
			grantee = "group " + access.Group.Name
		}
		recordShare(ctx, f, fmt.Sprintf("Stopped sharing %s with %s", f.Name, grantee),
			map[string]any{"grantee": grantee, "permission": access.Permission})
		out.Message = "Share removed"
		return nil
	})
	u.SetTitle("Remove Flow Share")
	u.SetDescription("Removes one share from a flow. Needs admin on the flow.")
	u.SetTags("Flows")
	return u
}

// shareGrantee reduces a share's user to the ID and username a share dialog
// names them by. Listing shares needs access to the shared item, not
// permission to read users, so the email address, preferences, and account
// timestamps stay out of it.
func shareGrantee(u *models.User) *models.User {
	if u == nil {
		return nil
	}
	return &models.User{ID: u.ID, Username: u.Username}
}

// recordShare records a change to who a flow is shared with.
func recordShare(ctx context.Context, f *models.Flow, message string, details map[string]any) {
	events.Record(events.Entry{
		Category: models.EventFlow,
		Subject:  events.Subject{Kind: "flow", ID: f.ID.String(), Name: f.Name},
		Message:  message,
		Details:  details,
		Actor:    events.ActorFrom(ctx),
	})
}

// RegisterFlowShareRoutes registers the flow and folder sharing endpoints.
// They need no global permission beyond signing in: admin on the flow or
// folder is the check.
func RegisterFlowShareRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodGet, "/flows/{id}/shares",
			nethttp.NewHandler(FlowSharesHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodPost, "/flows/{id}/shares",
			nethttp.NewHandler(FlowAddShareHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodDelete, "/flows/{id}/shares/{shareId}",
			nethttp.NewHandler(FlowRemoveShareHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodGet, "/folders/{id}/shares",
			nethttp.NewHandler(FolderSharesHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodPost, "/folders/{id}/shares",
			nethttp.NewHandler(FolderAddShareHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodDelete, "/folders/{id}/shares/{shareId}",
			nethttp.NewHandler(FolderRemoveShareHandler()))
}

// FolderSharesInput identifies the folder whose shares are listed.
type FolderSharesInput struct {
	ID uint `path:"id" description:"Folder ID."`
}

// FolderSharesHandler returns the interactor that lists who a folder is shared with.
func FolderSharesHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FolderSharesInput, out *apiutil.Success[[]models.FolderAccess]) error {
		if _, err := flowops.AuthorizeFolder(ctx, callerID(ctx), in.ID, models.FlowAccessAdmin); err != nil {
			return err
		}
		entries, err := models.ListFolderAccess(in.ID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		for i := range entries {
			entries[i].User = shareGrantee(entries[i].User)
		}
		out.Data = entries
		return nil
	})
	u.SetTitle("List Folder Shares")
	u.SetDescription("Returns the users and groups a folder is shared with and at which level. Needs admin on the folder.")
	u.SetTags("Folders")
	return u
}

// FolderAddShareInput names the folder, the grantee, and the level to share at.
type FolderAddShareInput struct {
	ID         uint   `path:"id" description:"Folder ID."`
	Username   string `json:"username,omitempty" description:"Username to share with."`
	GroupName  string `json:"group_name,omitempty" description:"User group name to share with."`
	Permission string `json:"permission" enum:"faceplate,view,edit,admin" default:"view" description:"Access level granted on the folder, its subfolders, and every flow inside: faceplate, view, edit, or admin."`
}

// FolderAddShareHandler returns the interactor that shares a folder with a user or group.
func FolderAddShareHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FolderAddShareInput, out *apiutil.Success[*models.FolderAccess]) error {
		folder, err := flowops.AuthorizeFolder(ctx, callerID(ctx), in.ID, models.FlowAccessAdmin)
		if err != nil {
			return err
		}
		if (in.Username == "") == (in.GroupName == "") {
			return apperrors.ErrValidation.WithMessage("give either username or group_name")
		}
		permission := in.Permission
		if permission == "" {
			permission = models.FlowAccessView
		}
		if !models.ValidFlowAccess(permission) {
			return apperrors.ErrValidation.WithMessage("permission must be faceplate, view, edit, or admin")
		}

		access := &models.FolderAccess{FolderID: folder.ID, Permission: permission}
		grantee := in.Username
		if in.Username != "" {
			var user models.User
			if err := db.DB.Where("username = ?", in.Username).First(&user).Error; err != nil {
				return apperrors.ErrValidation.WithMessage("user not found")
			}
			access.UserID = &user.ID
		} else {
			var group models.UserGroup
			if err := db.DB.Where("name = ?", in.GroupName).First(&group).Error; err != nil {
				return apperrors.ErrValidation.WithMessage("group not found")
			}
			access.GroupID = &group.ID
			grantee = "group " + group.Name
		}
		if err := db.DB.Create(access).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		recordFolderShare(ctx, folder, fmt.Sprintf("Shared folder %s with %s at %s", folder.Name, grantee, permission),
			map[string]any{"grantee": grantee, "permission": permission})
		out.Data = access
		return nil
	})
	u.SetTitle("Share Folder")
	u.SetDescription("Shares a folder, its subfolders, and every flow inside with a user or group by name. Needs admin on the folder.")
	u.SetTags("Folders")
	return u
}

// FolderRemoveShareInput identifies one share to remove from a folder.
type FolderRemoveShareInput struct {
	ID      uint `path:"id" description:"Folder ID."`
	ShareID uint `path:"shareId" description:"Share entry ID."`
}

// FolderRemoveShareHandler returns the interactor that removes a folder share.
func FolderRemoveShareHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FolderRemoveShareInput, out *apiutil.Success[any]) error {
		folder, err := flowops.AuthorizeFolder(ctx, callerID(ctx), in.ID, models.FlowAccessAdmin)
		if err != nil {
			return err
		}
		var access models.FolderAccess
		if err := db.DB.Preload("User").Preload("Group").Where("folder_id = ? AND id = ?", folder.ID, in.ShareID).First(&access).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("share not found")
		}
		if err := db.DB.Delete(&models.FolderAccess{}, access.ID).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		grantee := "a removed account"
		if access.User != nil {
			grantee = access.User.Username
		} else if access.Group != nil {
			grantee = "group " + access.Group.Name
		}
		recordFolderShare(ctx, folder, fmt.Sprintf("Stopped sharing folder %s with %s", folder.Name, grantee),
			map[string]any{"grantee": grantee, "permission": access.Permission})
		out.Message = "Share removed"
		return nil
	})
	u.SetTitle("Remove Folder Share")
	u.SetDescription("Removes one share from a folder. Needs admin on the folder.")
	u.SetTags("Folders")
	return u
}

// recordFolderShare records a change to who a folder is shared with. The
// folder is the subject, so the entry is not hidden behind any one flow.
func recordFolderShare(ctx context.Context, folder *models.Folder, message string, details map[string]any) {
	events.Record(events.Entry{
		Category: models.EventFlow,
		Subject:  events.Subject{Kind: "folder", ID: fmt.Sprint(folder.ID), Name: folder.Name},
		Message:  message,
		Details:  details,
		Actor:    events.ActorFrom(ctx),
	})
}
