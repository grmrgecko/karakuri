package uploads

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/resources"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/auth"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/usecase"
)

// authorizeWidget verifies the caller may act on the dashboard owning the
// widget, at the given access level. Files belong to a dashboard, so they
// follow the dashboard's own sharing rather than a global permission: holding
// api:dashboards:write says a user may author dashboards, not that they may
// write into someone else's, and a user shared with at edit level may.
func authorizeWidget(ctx context.Context, dashboardWidgetID uuid.UUID, level string) error {
	widget, err := models.FindDashboardWidget(dashboardWidgetID)
	if err != nil {
		return apperrors.ErrNotFound.WithMessage("widget not found")
	}
	return authorizeDashboard(ctx, widget.DashboardID, level)
}

// authorizeDashboard checks one dashboard at one access level. An anonymous
// caller carries no user, which CanAccessDashboard admits only for viewing a
// public dashboard.
func authorizeDashboard(ctx context.Context, dashboardID uuid.UUID, level string) error {
	var userID uuid.UUID
	var groupIDs []uint
	if uc := middleware.GetUser(ctx); uc != nil {
		userID = uc.UserID
		groupIDs, _ = models.GetUserGroupIDs(userID)
	}

	allowed, err := canAccess(ctx, dashboardID, userID, groupIDs, level)
	if err != nil {
		return apperrors.ErrNotFound.WithMessage("dashboard not found")
	}
	if !allowed {
		if userID == uuid.Nil {
			return apperrors.ErrUnauthorized
		}
		return apperrors.ErrForbidden.WithMessage("You do not have access to this dashboard")
	}
	return nil
}

// canAccess answers one access level on a dashboard for a user: what
// ownership and shares grant, or everything for a holder of the dashboard
// manage permission, who may open and edit any dashboard and so reaches its
// files as well.
func canAccess(ctx context.Context, dashboardID, userID uuid.UUID, groupIDs []uint, level string) (bool, error) {
	allowed, err := models.CanAccessDashboard(dashboardID, userID, groupIDs, level)
	if err != nil || allowed || userID == uuid.Nil {
		return allowed, err
	}
	return auth.Can(ctx, userID, perm.PermDashboardsManage), nil
}

var allowedContentTypes = map[string]bool{
	"image/jpeg":       true,
	"image/png":        true,
	"image/gif":        true,
	"image/webp":       true,
	"image/svg+xml":    true,
	"video/mp4":        true,
	"video/quicktime":  true,
	"video/webm":       true,
	"video/ogg":        true,
	"application/pdf":  true,
	"application/json": true,
	"text/csv":         true,
}

// Upload handles multipart file upload for a dashboard widget. Stays as a plain handler
// because swaggest interactors do not support multipart form processing.
func Upload(w http.ResponseWriter, r *http.Request) {
	dashboardWidgetID, err := uuid.Parse(chi.URLParam(r, "widgetId"))
	if err != nil {
		apiutil.WriteErrorResponse(w, apperrors.ErrValidation.WithMessage("Invalid widget ID"))
		return
	}

	if err := authorizeWidget(r.Context(), dashboardWidgetID, models.DashboardAccessEdit); err != nil {
		var appErr *apperrors.AppError
		if !errors.As(err, &appErr) {
			appErr = apperrors.ErrInternal.WithError(err)
		}
		apiutil.WriteErrorResponse(w, appErr)
		return
	}

	maxSize := config.C.Security.MaxUploadSize
	r.Body = http.MaxBytesReader(w, r.Body, maxSize)
	if err := r.ParseMultipartForm(maxSize); err != nil {
		apiutil.WriteErrorResponse(w, apperrors.ErrValidation.WithMessage("File too large"))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		apiutil.WriteErrorResponse(w, apperrors.ErrValidation.WithMessage("No file provided"))
		return
	}
	defer func() { _ = file.Close() }()

	contentType := header.Header.Get("Content-Type")
	if !allowedContentTypes[contentType] {
		apiutil.WriteErrorResponse(w, apperrors.ErrValidation.WithMessage("File type not allowed"))
		return
	}

	uploadID := uuid.New()
	storagePath := storagePath(dashboardWidgetID, uploadID, header.Filename)

	if err := saveFile(file, storagePath); err != nil {
		apiutil.WriteErrorResponse(w, apperrors.ErrInternal.WithError(err))
		return
	}

	maxPos, err := models.MaxUploadPosition(dashboardWidgetID)
	if err != nil {
		_ = os.Remove(storagePath)
		apiutil.WriteErrorResponse(w, apperrors.ErrInternal.WithError(err))
		return
	}

	upload := &models.WidgetUpload{
		ID:                uploadID,
		DashboardWidgetID: dashboardWidgetID,
		FileName:          header.Filename,
		StoragePath:       storagePath,
		ContentType:       contentType,
		Size:              header.Size,
		Position:          maxPos + 1,
	}

	if err := db.DB.Create(upload).Error; err != nil {
		_ = os.Remove(storagePath)
		apiutil.WriteErrorResponse(w, apperrors.ErrInternal.WithError(err))
		return
	}

	apiutil.WriteJSON(w, http.StatusCreated, apiutil.Success[map[string]interface{}]{
		Data: map[string]interface{}{
			"id":        upload.ID,
			"file_name": upload.FileName,
			"size":      upload.Size,
			"position":  upload.Position,
		},
	})
}

// --- Interactors for upload list, reorder, delete ---

type uploadItem struct {
	ID          uuid.UUID `json:"id" description:"Upload UUID."`
	FileName    string    `json:"file_name" description:"Original file name."`
	ContentType string    `json:"content_type" description:"MIME content type."`
	Size        int64     `json:"size" description:"File size in bytes."`
	Position    int       `json:"position" description:"Display position."`
}

type listUploadsInput struct {
	ID       string `path:"id" description:"Dashboard UUID or slug."`
	WidgetID string `path:"widgetId" format:"uuid" description:"Dashboard widget UUID."`
}

type listUploadsOutput = apiutil.Success[[]uploadItem]

// ListUploadsInteractor returns an interactor that lists uploads for a dashboard widget.
func ListUploadsInteractor() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in listUploadsInput, out *listUploadsOutput) error {
		dashboardWidgetID, err := uuid.Parse(in.WidgetID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid widget ID")
		}

		if err := authorizeWidget(ctx, dashboardWidgetID, models.DashboardAccessRead); err != nil {
			return err
		}

		var uploads []*models.WidgetUpload
		if err := db.DB.Where("dashboard_widget_id = ?", dashboardWidgetID).Order("position ASC").Find(&uploads).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		items := make([]uploadItem, len(uploads))
		for i, u := range uploads {
			items[i] = uploadItem{
				ID:          u.ID,
				FileName:    u.FileName,
				ContentType: u.ContentType,
				Size:        u.Size,
				Position:    u.Position,
			}
		}

		out.Data = items
		return nil
	})
	u.SetTitle("List Uploads")
	u.SetDescription("Returns uploads for a dashboard widget, ordered by position.")
	u.SetTags("Uploads")
	return u
}

type reorderUploadsInput struct {
	ID        string      `path:"id" description:"Dashboard UUID or slug."`
	WidgetID  string      `path:"widgetId" format:"uuid" description:"Dashboard widget UUID."`
	UploadIDs []uuid.UUID `json:"upload_ids" required:"true" minItems:"1" description:"Ordered list of upload IDs."`
}

type reorderUploadsOutput = apiutil.Success[[]uploadItem]

// ReorderUploadsInteractor returns an interactor that reorders uploads for a dashboard widget.
func ReorderUploadsInteractor() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in reorderUploadsInput, out *reorderUploadsOutput) error {
		dashboardWidgetID, err := uuid.Parse(in.WidgetID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid widget ID")
		}

		if err := authorizeWidget(ctx, dashboardWidgetID, models.DashboardAccessEdit); err != nil {
			return err
		}

		if err := models.ReorderUploads(dashboardWidgetID, in.UploadIDs); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		var uploads []*models.WidgetUpload
		if err := db.DB.Where("dashboard_widget_id = ?", dashboardWidgetID).Order("position ASC").Find(&uploads).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		items := make([]uploadItem, len(uploads))
		for i, u := range uploads {
			items[i] = uploadItem{
				ID:          u.ID,
				FileName:    u.FileName,
				ContentType: u.ContentType,
				Size:        u.Size,
				Position:    u.Position,
			}
		}

		out.Data = items
		return nil
	})
	u.SetTitle("Reorder Uploads")
	u.SetDescription("Sets display order for widget uploads.")
	u.SetTags("Uploads")
	return u
}

type deleteUploadInput struct {
	ID       string `path:"id" description:"Dashboard UUID or slug."`
	WidgetID string `path:"widgetId" format:"uuid" description:"Dashboard widget UUID."`
	UploadID string `path:"uploadId" format:"uuid" description:"Upload UUID."`
}

type deleteUploadOutput = apiutil.Success[any]

// DeleteUploadInteractor returns an interactor that deletes a single upload.
func DeleteUploadInteractor() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in deleteUploadInput, out *deleteUploadOutput) error {
		uploadID, err := uuid.Parse(in.UploadID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid upload ID")
		}

		var upload models.WidgetUpload
		if err := db.DB.Where("id = ?", uploadID).First(&upload).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("upload not found")
		}

		if err := authorizeWidget(ctx, upload.DashboardWidgetID, models.DashboardAccessEdit); err != nil {
			return err
		}

		_ = os.Remove(filePath(resources.GetDataPath(), &upload))

		if err := db.DB.Delete(&models.WidgetUpload{}, "id = ?", uploadID).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Message = "Upload deleted"
		return nil
	})
	u.SetTitle("Delete Upload")
	u.SetDescription("Deletes an uploaded file from a dashboard widget.")
	u.SetTags("Uploads")
	return u
}

// ServeFile serves an uploaded file after verifying the upload belongs to the
// specified dashboard widget. The name in the URL is only compared with the
// stored one; the file is opened where the upload's own row places it.
func ServeFile(w http.ResponseWriter, r *http.Request) {
	dashboardWidgetID, err := uuid.Parse(chi.URLParam(r, "dashboardWidgetId"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	uploadID, err := uuid.Parse(chi.URLParam(r, "uploadId"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// The router matches on the encoded path whenever the request carries
	// one, which it does for a name mixing an encoded character with one a
	// browser leaves bare, so the segment is decoded before it is compared.
	filename := chi.URLParam(r, "filename")
	if r.URL.RawPath != "" {
		if filename, err = url.PathUnescape(filename); err != nil {
			http.NotFound(w, r)
			return
		}
	}

	var upload models.WidgetUpload
	if err := db.DB.Where("id = ?", uploadID).First(&upload).Error; err != nil || upload.DashboardWidgetID != dashboardWidgetID || upload.FileName != filename {
		http.NotFound(w, r)
		return
	}
	// A file is shown to whoever may view its placement, and refused as not
	// found to anyone else, so a private dashboard's uploads cannot be
	// confirmed by probing.
	if !mayView(r, dashboardWidgetID) {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", upload.ContentType)
	// Uploads are served on the app origin, where the session tokens live in
	// local storage. A raster image or a video cannot run script; an SVG can,
	// and opened by its URL it would run with the viewer's session at hand.
	// The policy bars script wherever the file renders inline, and a direct
	// visit downloads it. An <img> ignores the disposition, so widgets still
	// draw it.
	if upload.ContentType == "image/svg+xml" {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		w.Header().Set("Content-Disposition", "attachment")
	}
	http.ServeFile(w, r, filePath(resources.GetDataPath(), &upload))
}

// mayView decides whether the request may see an upload of a placement. The
// caller's credential comes from the media cookie or a header, resolved by
// OptionalAuth into a user or a render job. A render job sees its own
// dashboard. A user, or an anonymous visitor of a public dashboard, sees it
// when they may view the dashboard; a placement the deployed version does not
// hold is unpublished and needs edit access, as the draft it belongs to does.
func mayView(r *http.Request, dashboardWidgetID uuid.UUID) bool {
	var placement models.DashboardWidget
	if err := db.DB.Where("id = ?", dashboardWidgetID).First(&placement).Error; err != nil {
		return false
	}
	dashboard, err := models.FindDashboardByID(placement.DashboardID)
	if err != nil {
		return false
	}
	published := placementDeployed(dashboard, dashboardWidgetID)

	if rc := middleware.GetRenderContext(r.Context()); rc != nil {
		return rc.DashboardID == dashboard.ID && (published || rc.Draft)
	}

	userID := uuid.Nil
	var groups []uint
	if uc := middleware.GetUser(r.Context()); uc != nil {
		userID = uc.UserID
		groups, _ = models.GetUserGroupIDs(userID)
	}
	level := models.DashboardAccessRead
	if !published {
		level = models.DashboardAccessEdit
	}
	ok, err := canAccess(r.Context(), dashboard.ID, userID, groups, level)
	return err == nil && ok
}

// placementDeployed reports whether viewers are served the placement: always
// for a dashboard never deployed, otherwise when the deployed version holds
// it. A version that cannot be read serves nothing.
func placementDeployed(d *models.Dashboard, placementID uuid.UUID) bool {
	if d.DeployedVersion == 0 {
		return true
	}
	version, err := models.FindDashboardVersion(d.ID, d.DeployedVersion)
	if err != nil {
		return false
	}
	snap, err := models.ParseDashboardSnapshot(version.Snapshot)
	if err != nil {
		return false
	}
	for _, w := range snap.Widgets {
		if w.ID == placementID {
			return true
		}
	}
	return false
}

// MediaCookie is the cookie the browser mirrors its access or render token
// into for /widget/ requests, which an <img> or <video> cannot attach a
// header to.
const MediaCookie = "karakuri_media"

// CookieCredential presents the media cookie as a bearer token when the
// request carries no Authorization header, so OptionalAuth resolves it.
func CookieCredential(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			if c, err := r.Cookie(MediaCookie); err == nil && c.Value != "" {
				r.Header.Set("Authorization", "Bearer "+c.Value)
			}
		}
		next.ServeHTTP(w, r)
	})
}

func storagePath(dashboardWidgetID, uploadID uuid.UUID, filename string) string {
	return storagePathIn(resources.GetDataPath(), dashboardWidgetID, uploadID, filename)
}

// storagePathIn is where an upload's file is kept under a data directory.
// Multipart keeps a backslash in a file name outside Windows, and a backup
// refuses an entry name holding one, so the file on disk takes an underscore
// in its place. The row keeps the name as uploaded.
func storagePathIn(dataDir string, dashboardWidgetID, uploadID uuid.UUID, filename string) string {
	return filepath.Join(dataDir, "uploads", "widgets",
		dashboardWidgetID.String(), uploadID.String(), strings.ReplaceAll(filepath.Base(filename), `\`, "_"))
}

// filePath returns where an upload's file is now. The row records the
// absolute path the file had when it was uploaded, which is stale once a
// backup is restored onto a machine that keeps its data elsewhere: the files
// come back under that machine's data directory. The place under dataDir is
// built from the row alone and wins when a file is there; otherwise the
// recorded path stands.
func filePath(dataDir string, upload *models.WidgetUpload) string {
	local := storagePathIn(dataDir, upload.DashboardWidgetID, upload.ID, upload.FileName)
	if info, err := os.Stat(local); err == nil && info.Mode().IsRegular() {
		return local
	}
	return upload.StoragePath
}

func saveFile(file multipart.File, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	dst, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer func() { _ = dst.Close() }()

	if _, err := io.Copy(dst, file); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}
