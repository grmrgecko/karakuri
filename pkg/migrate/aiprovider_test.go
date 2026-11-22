package migrate

import (
	"testing"

	"github.com/grmrgecko/karakuri/internal/config"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBackfillAIProviderKind pins the upgrade path for connections written
// when every AI provider was its own kind: the row moves to the "ai" kind
// with the old kind carried into the config's provider field, the rest of
// the encrypted config intact, and the connection's ID untouched — which is
// what keeps every node config referencing it working.
func TestBackfillAIProviderKind(t *testing.T) {
	require.NoError(t, db.Init(db.Options{
		Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1,
	}))
	require.NoError(t, AutoMigrate())
	// The config column is encrypted, so the backfill needs the key serve.go
	// would have loaded.
	require.NoError(t, config.Load())
	config.C.Security.EncryptionKey = "0000000000000000000000000000000000000000000000000000000000000000"

	c := &models.Connection{Name: "llama-swap", Kind: "openai-compatible"}
	require.NoError(t, c.SetConfig(map[string]any{"base_url": "http://10.0.10.6:9292/v1"}))
	require.NoError(t, db.DB.Create(c).Error)

	require.NoError(t, BackfillAIProviderKind())
	require.NoError(t, BackfillAIProviderKind(), "the backfill is one-shot and reruns as a no-op")

	var migrated models.Connection
	require.NoError(t, db.DB.First(&migrated, "id = ?", c.ID).Error)
	assert.Equal(t, "ai", migrated.Kind, "the row lands on the single AI kind")
	var cfg map[string]any
	require.NoError(t, migrated.GetConfig(&cfg))
	assert.Equal(t, "openai-compatible", cfg["provider"], "the old kind becomes the provider")
	assert.Equal(t, "http://10.0.10.6:9292/v1", cfg["base_url"], "the rest of the config survives re-encryption")
}
