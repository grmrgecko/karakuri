package uploads

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFilePathFollowsTheDataDirectory locks that an upload is found under
// the installation's data directory after a restore onto a machine that keeps
// its data elsewhere. The row records the absolute path the file had when it
// was uploaded, and a restore puts the uploads directory under the target's
// data directory, so the recorded path named a directory the new machine does
// not have and every restored upload answered not found. An upload whose file
// is not under the data directory keeps its recorded path.
func TestFilePathFollowsTheDataDirectory(t *testing.T) {
	oldData, newData := filepath.Join(t.TempDir(), "old"), filepath.Join(t.TempDir(), "new")
	placement, id := uuid.New(), uuid.New()
	restored := &models.WidgetUpload{ID: id, DashboardWidgetID: placement, FileName: "photo.png",
		StoragePath: storagePathIn(oldData, placement, id, "photo.png")}
	local := storagePathIn(newData, placement, id, "photo.png")
	require.NoError(t, os.MkdirAll(filepath.Dir(local), 0o750))
	require.NoError(t, os.WriteFile(local, []byte("png"), 0o600))

	assert.Equal(t, local, filePath(newData, restored),
		"a restored upload is read from the data directory it was restored into")

	elsewhere := &models.WidgetUpload{ID: uuid.New(), DashboardWidgetID: placement, FileName: "other.png",
		StoragePath: filepath.Join(oldData, "kept", "other.png")}
	assert.Equal(t, elsewhere.StoragePath, filePath(newData, elsewhere),
		"an upload with no file under the data directory keeps the path recorded for it")
}

// TestStoredFileNameHoldsNoBackslash locks that an upload's file on disk is
// named without a backslash. Multipart keeps a backslash in a file name on
// Linux, and a backup refuses any archive entry name holding one, so a single
// upload named a\b.png made every later backup fail. The row keeps the name
// as uploaded, and the file is still found by it.
func TestStoredFileNameHoldsNoBackslash(t *testing.T) {
	data := t.TempDir()
	placement, id := uuid.New(), uuid.New()
	path := storagePathIn(data, placement, id, `a\b.png`)
	rel, err := filepath.Rel(data, path)
	require.NoError(t, err)
	assert.NotContains(t, filepath.ToSlash(rel), `\`, "a backup refuses an entry name holding a backslash")

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("png"), 0o600))
	row := &models.WidgetUpload{ID: id, DashboardWidgetID: placement, FileName: `a\b.png`, StoragePath: "/elsewhere/a.png"}
	assert.Equal(t, path, filePath(data, row), "the file is found under the data directory by the name the row keeps")
}
