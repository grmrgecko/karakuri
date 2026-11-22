package api_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/backup"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sessionWith returns a session token for a new user holding only perms.
func sessionWith(t *testing.T, name string, perms ...string) string {
	t.Helper()
	user := &models.User{ID: uuid.New(), Username: name, Email: name + "@example.com", PasswordHash: "unused"}
	require.NoError(t, db.DB.Create(user).Error)
	role := &models.Role{Name: name + " role"}
	require.NoError(t, role.Create())
	for _, p := range perms {
		require.NoError(t, db.DB.Create(&models.RolePermission{RoleID: role.ID, Permission: p}).Error)
	}
	require.NoError(t, db.DB.Create(&models.RoleAssignment{RoleID: role.ID, UserID: &user.ID}).Error)
	tokens, err := auth.CreateSession(user, "127.0.0.1", "test")
	require.NoError(t, err)
	return tokens.AccessToken
}

func rawGet(t *testing.T, ts *httptest.Server, token, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

// TestBackupRoutes covers US4 through REST: the schedule refuses what would
// fail at night, a backup started over the API lands and downloads as the
// encrypted archive, each route needs its own permission with download
// separate from read, and a name that is not an archive never reaches a file.
func TestBackupRoutes(t *testing.T) {
	ts, admin := setupTestServer(t)
	data := t.TempDir()
	backup.S = backup.NewService(func() backup.Source {
		return backup.Source{DB: db.DB, DatabaseType: "sqlite", DataDir: data, Installation: "api", Version: "1.0.0",
			Secrets: backup.Secrets{EncryptionKey: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", JWTSecret: "jwt"}}
	}, nil)
	t.Cleanup(func() { backup.S = nil })
	dir := filepath.Join(t.TempDir(), "archives")

	status, resp := doJSON(t, ts, admin, http.MethodPut, "/api/backups/schedule", map[string]any{"enabled": true, "directory": dir})
	assert.Equalf(t, http.StatusBadRequest, status, "enabling without a passphrase is refused: %v", resp)
	status, resp = doJSON(t, ts, admin, http.MethodPut, "/api/backups/schedule", map[string]any{
		"enabled": true, "directory": dir, "retain": 3, "passphrase": "correct horse battery"})
	require.Equalf(t, http.StatusOK, status, "save schedule: %v", resp)
	view := resp["data"].(map[string]any)
	assert.Equal(t, true, view["passphrase_set"])
	assert.NotContains(t, view, "passphrase", "the passphrase is never returned")
	assert.NotEmpty(t, view["next_at"])

	status, resp = doJSON(t, ts, admin, http.MethodPost, "/api/backups", map[string]any{})
	require.Equalf(t, http.StatusAccepted, status, "create: %v", resp)

	var name string
	require.Eventually(t, func() bool {
		_, resp := doJSON(t, ts, admin, http.MethodGet, "/api/backups", nil)
		archives, _ := resp["data"].(map[string]any)["archives"].([]any)
		if len(archives) == 0 {
			return false
		}
		a := archives[0].(map[string]any)
		name = a["name"].(string)
		return a["verified"] == "ok"
	}, 30*time.Second, 100*time.Millisecond, "the backup lands and verifies")

	status, body := rawGet(t, ts, admin, "/api/backups/"+name+"/download")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body[:40]), "age-encryption.org/v1", "the download is the encrypted archive itself")

	status, resp = doJSON(t, ts, admin, http.MethodPost, "/api/backups/"+name+"/verify", map[string]any{})
	require.Equalf(t, http.StatusOK, status, "verify: %v", resp)
	status, _ = doJSON(t, ts, admin, http.MethodPost, "/api/backups/"+name+"/verify", map[string]any{"passphrase": "the wrong one entirely"})
	assert.Equal(t, http.StatusBadRequest, status)

	reader := sessionWith(t, "reader", permissions.PermBackupsRead)
	status, _ = doJSON(t, ts, reader, http.MethodGet, "/api/backups", nil)
	assert.Equal(t, http.StatusOK, status, "read lists")
	status, _ = rawGet(t, ts, reader, "/api/backups/"+name+"/download")
	assert.Equal(t, http.StatusForbidden, status, "read does not download")
	status, _ = doJSON(t, ts, reader, http.MethodPost, "/api/backups", map[string]any{})
	assert.Equal(t, http.StatusForbidden, status, "read does not create")
	status, _ = doJSON(t, ts, reader, http.MethodDelete, "/api/backups/"+name, nil)
	assert.Equal(t, http.StatusForbidden, status, "read does not delete")
	nobody := sessionWith(t, "nobody")
	status, _ = doJSON(t, ts, nobody, http.MethodGet, "/api/backups", nil)
	assert.Equal(t, http.StatusForbidden, status)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600))
	for _, bad := range []string{"notes.txt", "..%2Fsecret", "karakuri-api-20260101T000000Z.tar.zst.age"} {
		status, _ = rawGet(t, ts, admin, "/api/backups/"+bad+"/download")
		assert.Equal(t, http.StatusNotFound, status, "%s is not an archive here", bad)
		status, _ = doJSON(t, ts, admin, http.MethodDelete, "/api/backups/"+bad, nil)
		assert.Equal(t, http.StatusNotFound, status, "%s is not an archive here", bad)
	}

	status, _ = doJSON(t, ts, admin, http.MethodDelete, "/api/backups/"+name, nil)
	require.Equal(t, http.StatusOK, status)
	_, resp = doJSON(t, ts, admin, http.MethodGet, "/api/backups", nil)
	assert.Empty(t, resp["data"].(map[string]any)["archives"])
	_, err := os.Stat(filepath.Join(dir, "notes.txt"))
	assert.NoError(t, err, "other files are left alone")
}
