package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/uploads"
	"github.com/grmrgecko/karakuri/pkg/widgets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestWidgetUploadsFollowDashboardAccess pins the fix for uploaded widget
// files being served to anyone holding the URL. A file is now shown to whoever
// may view its placement: refused to an anonymous request on a private
// dashboard, served to its owner through the media cookie an <img> sends,
// served to anyone once the dashboard is public, and refused again for a
// placement the deployed version does not hold, which is unpublished work.
//
// A holder of the dashboard manage permission reaches every dashboard, so they
// reach its files and its renders too. The upload and render routes asked
// only about ownership and shares, which left a manager looking at broken
// images, and told a dashboard was missing, on one they could open and edit.
func TestWidgetUploadsFollowDashboardAccess(t *testing.T) {
	ts, bearer := setupTestServer(t)
	require.NoError(t, widgets.Register(zap.NewNop(), "core", t.TempDir(), []widgets.Spec{{
		Name: "Image", PackageName: "image", TagName: "image-widget", Version: "0.1.0",
	}}), "the widget package must register")
	t.Cleanup(func() { widgets.Unregister("core") })

	// The file route as production mounts it.
	files := chi.NewRouter()
	files.With(uploads.CookieCredential, middleware.OptionalAuth()).
		Get("/widget/{dashboardWidgetId}/{uploadId}/{filename}", uploads.ServeFile)
	fileSrv := httptest.NewServer(files)
	t.Cleanup(fileSrv.Close)

	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards", map[string]any{"name": "Photos"})
	require.Equalf(t, http.StatusCreated, status, "the dashboard must be created: %v", resp)
	dashID, _ := resp["data"].(map[string]any)["id"].(string)
	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/widgets", nil)
	require.Equal(t, http.StatusOK, status, "the widget catalog must load")
	widgetID := resp["data"].([]any)[0].(map[string]any)["id"]

	// upload places an image widget and stores one file for it.
	upload := func() string {
		t.Helper()
		status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards/"+dashID+"/widgets", map[string]any{
			"widget_id": widgetID, "x": 0, "y": 0, "width": 2, "height": 2, "config": `{}`,
		})
		require.Equalf(t, http.StatusCreated, status, "the placement must be created: %v", resp)
		placement := uuid.MustParse(resp["data"].(map[string]any)["id"].(string))
		path := filepath.Join(t.TempDir(), "photo.png")
		require.NoError(t, os.WriteFile(path, []byte("png"), 0o600), "the stored file must exist")
		row := models.WidgetUpload{ID: uuid.New(), DashboardWidgetID: placement, FileName: "photo.png", StoragePath: path, ContentType: "image/png", Size: 3}
		require.NoError(t, db.DB.Create(&row).Error, "the upload row must be stored")
		return "/widget/" + placement.String() + "/" + row.ID.String() + "/photo.png"
	}
	get := func(url string, cookie string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, fileSrv.URL+url, nil)
		require.NoError(t, err, "the file request must build")
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: uploads.MediaCookie, Value: cookie})
		}
		res, err := fileSrv.Client().Do(req)
		require.NoError(t, err, "the file request must reach the server")
		_ = res.Body.Close()
		return res.StatusCode
	}

	published := upload()
	assert.Equal(t, http.StatusNotFound, get(published, ""), "a private dashboard's file must not be served to an anonymous request")
	assert.Equal(t, http.StatusOK, get(published, bearer), "the owner's media cookie must open the file an <img> asks for")

	// A second account owns nothing here and holds only the manage permission.
	manager := sessionWith(t, "manager", permissions.PermDashboardsManage, permissions.PermRendersRead)
	stranger := sessionWith(t, "stranger", permissions.PermDashboardsRead, permissions.PermRendersRead)
	uploadsPath := "/api/dashboards/" + dashID + "/widgets/" + strings.Split(published, "/")[2] + "/uploads"
	assert.Equal(t, http.StatusOK, get(published, manager), "the manage permission opens a private dashboard, so it must open its files")
	assert.Equal(t, http.StatusNotFound, get(published, stranger), "an account holding no grant must still be refused the file")
	status, resp = doJSON(t, ts, manager, http.MethodGet, uploadsPath, nil)
	assert.Equalf(t, http.StatusOK, status, "a manager may edit the dashboard, so they must list its uploads: %v", resp)
	status, _ = doJSON(t, ts, stranger, http.MethodGet, uploadsPath, nil)
	assert.Equal(t, http.StatusForbidden, status, "an account holding no grant must not list the uploads")
	for _, path := range []string{"/renders", "/render_schedules"} {
		status, resp = doJSON(t, ts, manager, http.MethodGet, "/api/dashboards/"+dashID+path, nil)
		assert.Equalf(t, http.StatusOK, status, "%s follows the dashboard's access, which a manager holds: %v", path, resp)
		status, _ = doJSON(t, ts, stranger, http.MethodGet, "/api/dashboards/"+dashID+path, nil)
		assert.Equalf(t, http.StatusNotFound, status, "%s must stay hidden from an account holding no grant", path)
	}

	status, _ = doJSON(t, ts, bearer, http.MethodPut, "/api/dashboards/"+dashID, map[string]any{"is_public": true})
	require.Equal(t, http.StatusOK, status, "the dashboard must become public")
	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards/"+dashID+"/deploy", map[string]any{})
	require.Equalf(t, http.StatusOK, status, "the dashboard must deploy: %v", resp)
	assert.Equal(t, http.StatusOK, get(published, ""), "a public dashboard's published file is open to anyone")

	draftOnly := upload()
	assert.Equal(t, http.StatusNotFound, get(draftOnly, ""), "a file on a placement the deployment does not hold is unpublished")
	assert.Equal(t, http.StatusOK, get(draftOnly, bearer), "the editor still sees their draft's files")
}

// TestWidgetUploadsServeEveryStoredFileName locks the rule that any name an
// upload was stored under can be fetched back. The name in the URL is only
// compared with the stored row and never joined to a path, yet the route
// refused every name holding two dots, and compared the still-encoded path
// segment the router hands over whenever a name mixes an encoded character
// with one browsers leave bare. Both left a successful upload such as
// "image (1).png" as a broken image.
func TestWidgetUploadsServeEveryStoredFileName(t *testing.T) {
	ts, bearer := setupTestServer(t)
	require.NoError(t, widgets.Register(zap.NewNop(), "core", t.TempDir(), []widgets.Spec{{
		Name: "Image", PackageName: "image", TagName: "image-widget", Version: "0.1.0",
	}}), "the widget package must register")
	t.Cleanup(func() { widgets.Unregister("core") })

	files := chi.NewRouter()
	files.With(uploads.CookieCredential, middleware.OptionalAuth()).
		Get("/widget/{dashboardWidgetId}/{uploadId}/{filename}", uploads.ServeFile)
	fileSrv := httptest.NewServer(files)
	t.Cleanup(fileSrv.Close)

	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards", map[string]any{"name": "Photos"})
	require.Equalf(t, http.StatusCreated, status, "the dashboard must be created: %v", resp)
	dashID, _ := resp["data"].(map[string]any)["id"].(string)
	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/widgets", nil)
	require.Equal(t, http.StatusOK, status, "the widget catalog must load")
	widgetID := resp["data"].([]any)[0].(map[string]any)["id"]
	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards/"+dashID+"/widgets", map[string]any{
		"widget_id": widgetID, "x": 0, "y": 0, "width": 2, "height": 2, "config": `{}`,
	})
	require.Equalf(t, http.StatusCreated, status, "the placement must be created: %v", resp)
	placement := uuid.MustParse(resp["data"].(map[string]any)["id"].(string))

	cases := []struct {
		name string
		// stored is the file name on the upload row.
		stored string
		// segment is the last path segment as a browser writes it for an
		// <img> whose src holds the stored name.
		segment string
		why     string
	}{
		{
			name: "two dots inside a name", stored: "holiday..2026.png", segment: "holiday..2026.png",
			why: "two dots inside one segment name a file, not a parent directory",
		},
		{
			name: "a space beside parentheses", stored: "image (1).png", segment: "image%20(1).png",
			why: "a browser encodes the space and leaves the parentheses, so the segment must be decoded before it is compared",
		},
	}
	for _, c := range cases {
		path := filepath.Join(t.TempDir(), "stored")
		require.NoErrorf(t, os.WriteFile(path, []byte("png"), 0o600), "%s: the stored file must exist", c.name)
		row := models.WidgetUpload{ID: uuid.New(), DashboardWidgetID: placement, FileName: c.stored, StoragePath: path, ContentType: "image/png", Size: 3}
		require.NoErrorf(t, db.DB.Create(&row).Error, "%s: the upload row must be stored", c.name)

		req, err := http.NewRequest(http.MethodGet, fileSrv.URL+"/widget/"+placement.String()+"/"+row.ID.String()+"/"+c.segment, nil)
		require.NoErrorf(t, err, "%s: the file request must build", c.name)
		req.AddCookie(&http.Cookie{Name: uploads.MediaCookie, Value: bearer})
		res, err := fileSrv.Client().Do(req)
		require.NoErrorf(t, err, "%s: the file request must reach the server", c.name)
		_ = res.Body.Close()
		assert.Equalf(t, http.StatusOK, res.StatusCode, "%s: %s", c.name, c.why)
	}
}
