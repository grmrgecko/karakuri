package ainodes

import (
	"testing"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
	"github.com/grmrgecko/karakuri/pkg/memories"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemoryTranscriptStartsOnUserTurn pins that the replayed window never
// opens on an assistant turn. The window counts turns, so an odd window, or a
// session holding an exchange whose answer was empty, cut the oldest exchange
// in half and replayed its answer first; Anthropic rejects a conversation
// whose first message is not the user's, which failed every later run of the
// session.
func TestMemoryTranscriptStartsOnUserTurn(t *testing.T) {
	require.NoError(t, db.Init(db.Options{
		Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1,
	}))
	require.NoError(t, migrate.AutoMigrate())
	memories.S = memories.NewStore()
	require.NoError(t, memories.S.Load())
	store, err := memories.S.CreateStore(memories.StoreParams{Name: "House"})
	require.NoError(t, err)
	require.NoError(t, memories.S.AppendExchange(store.ID, "kitchen", "first question", "first answer"))
	require.NoError(t, memories.S.AppendExchange(store.ID, "kitchen", "second question", "second answer"))

	mem := &memoryContext{store: store, session: "kitchen", handle: memoryHandle{Window: 3}}
	got := mem.transcript()

	assert.Equal(t, []ai.Message{
		{Role: ai.RoleUser, Text: "second question"},
		{Role: ai.RoleAssistant, Text: "second answer"},
	}, got, "a window that cuts an exchange in half must drop the orphaned answer and start on the next user turn")
}
