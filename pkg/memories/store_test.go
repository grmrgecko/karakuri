package memories_test

import (
	"context"
	"sync"
	"testing"
	"time"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/memories"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setup runs the real migrations against an in-memory SQLite database and
// returns a fresh store loaded from it, so persistence and cache behaviour
// are the production ones.
func setup(t *testing.T) *memories.Store {
	t.Helper()

	require.NoError(t, db.Init(db.Options{
		Type:         "sqlite",
		DSN:          memdb.TestDB(t),
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}), "the tests need a database before a memory can exist")
	require.NoError(t, migrate.AutoMigrate(), "the memory tables are created by the real migrations")
	require.NoError(t, db.DB.Exec("DELETE FROM memories").Error)
	require.NoError(t, db.DB.Exec("DELETE FROM memory_stores").Error)

	s := memories.NewStore()
	require.NoError(t, s.Load())
	return s
}

// TestRememberAndRecallByKeyword covers the no-embedding path end to end: a
// stored fact is found by its words, ranked above an unrelated one, and the
// ranking honours importance between equal matches.
func TestRememberAndRecallByKeyword(t *testing.T) {
	s := setup(t)
	store, err := s.CreateStore(memories.StoreParams{Name: "House"})
	require.NoError(t, err)

	_, err = s.Remember(store.ID, memories.MemoryParams{Text: "The boiler pressure valve sticks in winter"})
	require.NoError(t, err)
	_, err = s.Remember(store.ID, memories.MemoryParams{Text: "The cat is fed at seven"})
	require.NoError(t, err)

	got := s.Recall(context.Background(), store.ID, "why does the boiler valve stick?", memories.RecallOpts{})
	require.NotEmpty(t, got, "a keyword match must be recalled without any embedding configured")
	assert.Contains(t, got[0].Text, "boiler", "the matching fact ranks first")
	for _, r := range got {
		assert.NotContains(t, r.Text, "cat", "an unrelated fact has no relevance signal and is dropped")
	}
}

// TestRecallScopesSessions pins the leak boundary: a session-scoped fact is
// recalled inside its session and never outside it, while global facts reach
// every session.
func TestRecallScopesSessions(t *testing.T) {
	s := setup(t)
	store, err := s.CreateStore(memories.StoreParams{Name: "House"})
	require.NoError(t, err)

	_, err = s.Remember(store.ID, memories.MemoryParams{Text: "front door code is stored in the safe", Session: "alice"})
	require.NoError(t, err)
	_, err = s.Remember(store.ID, memories.MemoryParams{Text: "front garden gate squeaks"})
	require.NoError(t, err)

	inSession := s.Recall(context.Background(), store.ID, "front door code", memories.RecallOpts{Session: "alice"})
	require.Len(t, inSession, 2, "a session sees its own facts and the global ones")

	outside := s.Recall(context.Background(), store.ID, "front door code", memories.RecallOpts{Session: "bob"})
	for _, r := range outside {
		assert.NotEqual(t, "alice", r.Session, "another session's facts never surface")
	}
}

// TestTranscriptWindowAndTrim covers the conversation path: exchanges append
// in order, the transcript replays chronologically, and the per-session cap
// drops the oldest turns rather than refusing new ones.
func TestTranscriptWindowAndTrim(t *testing.T) {
	s := setup(t)
	store, err := s.CreateStore(memories.StoreParams{Name: "Chat", MaxSessionTurns: 4})
	require.NoError(t, err)

	require.NoError(t, s.AppendExchange(store.ID, "kitchen", "turn on the lights", "done"))
	require.NoError(t, s.AppendExchange(store.ID, "kitchen", "and the fan", "fan is on"))
	require.NoError(t, s.AppendExchange(store.ID, "kitchen", "thanks", "any time"))

	turns := s.Transcript(store.ID, "kitchen", 0)
	require.Len(t, turns, 4, "the cap keeps the newest four turns of six")
	assert.Equal(t, "and the fan", turns[0].Text, "the oldest exchange was trimmed away")
	assert.Equal(t, "user", turns[0].Role)
	assert.Equal(t, "any time", turns[3].Text, "replay is chronological")

	windowed := s.Transcript(store.ID, "kitchen", 2)
	require.Len(t, windowed, 2)
	assert.Equal(t, "thanks", windowed[0].Text, "a window takes the newest turns, still in order")

	assert.Empty(t, s.Transcript(store.ID, "bedroom", 0), "sessions do not share transcripts")
}

// TestFactCapEvictsLeastImportant pins eviction order: pushing past the cap
// drops the least important fact, not the newest or an arbitrary one.
func TestFactCapEvictsLeastImportant(t *testing.T) {
	s := setup(t)
	store, err := s.CreateStore(memories.StoreParams{Name: "Small", MaxMemories: 2})
	require.NoError(t, err)

	_, err = s.Remember(store.ID, memories.MemoryParams{Text: "trivia", Importance: 0.1})
	require.NoError(t, err)
	_, err = s.Remember(store.ID, memories.MemoryParams{Text: "vital", Importance: 0.9})
	require.NoError(t, err)
	_, err = s.Remember(store.ID, memories.MemoryParams{Text: "routine", Importance: 0.5})
	require.NoError(t, err)

	snap, ok := s.GetStore(store.ID)
	require.True(t, ok)
	assert.Equal(t, 2, snap.Facts)

	got := s.Browse(context.Background(), store.ID, "", "", "", 0)
	texts := []string{}
	for _, r := range got {
		texts = append(texts, r.Text)
	}
	assert.NotContains(t, texts, "trivia", "the least important fact is the one evicted")
	assert.Contains(t, texts, "vital")
}

// TestExpiredMemoriesAreInvisible covers TTL: an expired memory is skipped by
// recall before any sweep runs, and Sweep removes it from the table.
func TestExpiredMemoriesAreInvisible(t *testing.T) {
	s := setup(t)
	store, err := s.CreateStore(memories.StoreParams{Name: "TTL"})
	require.NoError(t, err)

	past := time.Now().Add(-time.Minute)
	_, err = s.Remember(store.ID, memories.MemoryParams{Text: "guest wifi voucher code alpha", ExpiresAt: &past})
	require.NoError(t, err)

	got := s.Recall(context.Background(), store.ID, "guest wifi voucher", memories.RecallOpts{})
	assert.Empty(t, got, "an expired memory never surfaces, sweep or no sweep")

	require.NoError(t, s.Sweep())
	var count int64
	require.NoError(t, db.DB.Model(&models.Memory{}).Count(&count).Error)
	assert.EqualValues(t, 0, count, "the sweep deletes the expired row")
}

// TestVectorRecallRanksBySimilarity wires a fixture embedder and checks the
// hybrid path: vectors dominate the ranking, and the query is embedded through
// the same function. The fixture space puts "heating" near the boiler memory
// and far from the pets one, with no keyword overlap to help.
func TestVectorRecallRanksBySimilarity(t *testing.T) {
	s := setup(t)
	vectors := map[string][]float32{
		"radiators warm the rooms": {1, 0},
		"the dog naps at noon":     {0, 1},
		"heating schedule":         {0.9, 0.1},
	}
	s.SetEmbedFunc(func(ctx context.Context, conn, model string, texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i, txt := range texts {
			out[i] = vectors[txt]
		}
		return out, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx, nil)

	store, err := s.CreateStore(memories.StoreParams{
		Name: "Vec", EmbedConnection: "11111111-1111-1111-1111-111111111111", EmbedModel: "fixture",
	})
	require.NoError(t, err)
	_, err = s.Remember(store.ID, memories.MemoryParams{Text: "radiators warm the rooms"})
	require.NoError(t, err)
	_, err = s.Remember(store.ID, memories.MemoryParams{Text: "the dog naps at noon"})
	require.NoError(t, err)

	// The worker embeds in the background; wait for both vectors to land.
	require.Eventually(t, func() bool {
		snap, ok := s.GetStore(store.ID)
		return ok && snap.Pending == 0
	}, 5*time.Second, 10*time.Millisecond, "the background embedder must fill both vectors")

	got := s.Recall(context.Background(), store.ID, "heating schedule", memories.RecallOpts{})
	require.NotEmpty(t, got, "a semantic match must be recalled with zero keyword overlap")
	assert.Equal(t, "radiators warm the rooms", got[0].Text, "similarity ranks the related memory first")
}

// TestRecallMMRSuppressesNearDuplicates pins the user-visible MMR invariant:
// after the best hybrid match, a slightly lower-scored distinct memory beats a
// near duplicate, while each result still reports its original hybrid score.
func TestRecallMMRSuppressesNearDuplicates(t *testing.T) {
	s := setup(t)
	store, err := s.CreateStore(memories.StoreParams{Name: "Maintenance", RecencyWeight: 0})
	require.NoError(t, err)

	fixtures := []memories.MemoryParams{
		{Text: "boiler heating maintenance requires annual pressure valve inspection", Importance: 0.9},
		{Text: "annual boiler heating maintenance requires pressure valve inspection", Importance: 0.89},
		{Text: "boiler heating maintenance includes cleaning the burner and checking exhaust", Importance: 0.88},
	}
	for _, fixture := range fixtures {
		_, err = s.Remember(store.ID, fixture)
		require.NoError(t, err)
	}

	plain := s.Recall(context.Background(), store.ID, "boiler heating maintenance", memories.RecallOpts{Limit: 3})
	require.Len(t, plain, 3)
	assert.Contains(t, plain[1].Text, "pressure valve", "hybrid ranking alone returns the near duplicate second")
	assert.Contains(t, plain[2].Text, "burner", "the distinct memory starts below the near duplicate")

	diverse := s.Recall(context.Background(), store.ID, "boiler heating maintenance", memories.RecallOpts{
		Limit: 2, Rerank: memories.RerankMMR,
	})
	require.Len(t, diverse, 2)
	assert.Equal(t, plain[0].ID, diverse[0].ID, "MMR keeps the strongest hybrid match first")
	assert.Contains(t, diverse[1].Text, "burner", "MMR uses the larger candidate pool to select distinct context")
	assert.Equal(t, plain[2].Score, diverse[1].Score, "the public score remains hybrid relevance rather than the MMR selection value")
}

// TestLoadBackfillsMissingVectors pins the self-healing path: memories stored
// before embedding was configured get vectors on the next Load.
func TestLoadBackfillsMissingVectors(t *testing.T) {
	s := setup(t)
	store, err := s.CreateStore(memories.StoreParams{Name: "Backfill"})
	require.NoError(t, err)
	_, err = s.Remember(store.ID, memories.MemoryParams{Text: "stored before embeddings"})
	require.NoError(t, err)

	_, err = s.UpdateStore(store.ID, memories.StoreParams{
		Name: "Backfill", EmbedConnection: "11111111-1111-1111-1111-111111111111", EmbedModel: "fixture",
	})
	require.NoError(t, err)

	s2 := memories.NewStore()
	s2.SetEmbedFunc(func(ctx context.Context, conn, model string, texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i := range texts {
			out[i] = []float32{1}
		}
		return out, nil
	})
	require.NoError(t, s2.Load())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s2.Start(ctx, nil)

	require.Eventually(t, func() bool {
		snap, ok := s2.GetStore(store.ID)
		return ok && snap.Pending == 0
	}, 5*time.Second, 10*time.Millisecond, "Load must queue the unembedded memory for the worker")
}

// TestUpdateMemoryTextInvalidatesVector pins re-embedding: editing the text
// clears the stored vector so recall cannot match against words that are no
// longer there.
func TestUpdateMemoryTextInvalidatesVector(t *testing.T) {
	s := setup(t)
	store, err := s.CreateStore(memories.StoreParams{Name: "Edit"})
	require.NoError(t, err)
	it, err := s.Remember(store.ID, memories.MemoryParams{Text: "original", Importance: 0.5})
	require.NoError(t, err)

	edited, err := s.UpdateMemory(store.ID, it.ID, memories.MemoryParams{Text: "rewritten", Importance: 0.9})
	require.NoError(t, err)
	assert.Equal(t, "rewritten", edited.Text)
	assert.False(t, edited.Embedded)
	assert.Equal(t, 0.9, edited.Importance)
}

// TestResolveStoreByName mirrors the controls bargain: flows hold IDs, authors
// type names, both work.
func TestResolveStoreByName(t *testing.T) {
	s := setup(t)
	store, err := s.CreateStore(memories.StoreParams{Name: "Front Door"})
	require.NoError(t, err)

	byName, ok := s.ResolveStore("front door")
	require.True(t, ok, "resolution by name is case-insensitive")
	assert.Equal(t, store.ID, byName.ID)

	byID, ok := s.ResolveStore(store.ID.String())
	require.True(t, ok)
	assert.Equal(t, store.ID, byID.ID)
}

// TestClearSessionForgetsTranscriptAndScopedFacts covers ending a
// conversation: the session's turns and its scoped facts go, global facts
// stay.
func TestClearSessionForgetsTranscriptAndScopedFacts(t *testing.T) {
	s := setup(t)
	store, err := s.CreateStore(memories.StoreParams{Name: "Clear"})
	require.NoError(t, err)

	require.NoError(t, s.AppendExchange(store.ID, "hall", "hello", "hi"))
	_, err = s.Remember(store.ID, memories.MemoryParams{Text: "session fact", Session: "hall"})
	require.NoError(t, err)
	_, err = s.Remember(store.ID, memories.MemoryParams{Text: "global fact"})
	require.NoError(t, err)

	require.NoError(t, s.ClearSession(store.ID, "hall"))
	assert.Empty(t, s.Transcript(store.ID, "hall", 0))
	snap, _ := s.GetStore(store.ID)
	assert.Equal(t, 1, snap.Facts, "only the global fact survives")
}

// TestClearSharedSessionKeepsGlobalFacts locks that clearing the shared
// conversation, whose session key is empty, forgets only its transcript. Global
// facts carry the same empty key, so the clear used to wipe every fact and
// ingested document in the store along with the turns.
func TestClearSharedSessionKeepsGlobalFacts(t *testing.T) {
	s := setup(t)
	store, err := s.CreateStore(memories.StoreParams{Name: "Shared"})
	require.NoError(t, err)

	require.NoError(t, s.AppendExchange(store.ID, "", "hello", "hi"))
	require.NoError(t, s.AppendExchange(store.ID, "hall", "evening", "good evening"))
	_, err = s.Remember(store.ID, memories.MemoryParams{Text: "the boiler is serviced in March"})
	require.NoError(t, err)

	require.NoError(t, s.ClearSession(store.ID, ""))
	assert.Empty(t, s.Transcript(store.ID, "", 0), "the shared transcript is what the clear forgets")
	assert.Len(t, s.Transcript(store.ID, "hall", 0), 2, "another session's transcript is untouched")
	snap, _ := s.GetStore(store.ID)
	assert.Equal(t, 1, snap.Facts, "a global fact belongs to no session, so no session clear removes it")

	reloaded := memories.NewStore()
	require.NoError(t, reloaded.Load())
	snap, _ = reloaded.GetStore(store.ID)
	assert.Equal(t, 1, snap.Facts, "the fact must survive in the table too, or a restart loses it")
	assert.Equal(t, 2, snap.Turns, "only the shared turns are deleted from the table")
}

// TestLateEmbeddingDoesNotOverwriteAnEdit pins the race the audit reproduced
// against a real embeddings peer: a provider answer for the old text arriving
// after the memory was edited was stored and marked current, so a reload
// trusted a vector for words no longer there. Here the provider holds its
// answer for "original" until the edit lands; that answer must be discarded and
// the table must end with the vector for "rewritten".
func TestLateEmbeddingDoesNotOverwriteAnEdit(t *testing.T) {
	s := setup(t)
	called := make(chan struct{})
	release := make(chan struct{})
	vectors := map[string][]float32{"original": {1, 0}, "rewritten": {0, 1}}
	s.SetEmbedFunc(func(ctx context.Context, conn, model string, texts []string) ([][]float32, error) {
		if len(texts) == 1 && texts[0] == "original" {
			close(called)
			<-release
		}
		out := make([][]float32, len(texts))
		for i, txt := range texts {
			out[i] = vectors[txt]
		}
		return out, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx, nil)

	store, err := s.CreateStore(memories.StoreParams{
		Name: "Race", EmbedConnection: "11111111-1111-1111-1111-111111111111", EmbedModel: "fixture",
	})
	require.NoError(t, err)
	it, err := s.Remember(store.ID, memories.MemoryParams{Text: "original"})
	require.NoError(t, err)
	<-called
	_, err = s.UpdateMemory(store.ID, it.ID, memories.MemoryParams{Text: "rewritten"})
	require.NoError(t, err)
	close(release)

	// rewritten is 0.0 then 1.0 as little-endian float32: 00 00 00 00, 00 00 80 3f.
	want := []byte{0, 0, 0, 0, 0, 0, 0x80, 0x3f}
	require.Eventually(t, func() bool {
		var row models.Memory
		return db.DB.Where("id = ?", it.ID).First(&row).Error == nil && string(row.Embedding) == string(want)
	}, 5*time.Second, 10*time.Millisecond, "the stored vector must be the one for the edited text, not the late one")
}

// TestMetadataEditKeepsALandingEmbedding pins the lost vector from an edit that
// changes only importance while the provider answers. The edit read the
// memory's vector before the embedding landed and saved the whole row after it,
// so the table went back to no vector while the cache said embedded, and a
// reload queued the memory again. A gorm callback in front of the edit's save
// releases the provider and waits up to 500ms for the vector to reach the row,
// which is the window the edit used to leave open.
func TestMetadataEditKeepsALandingEmbedding(t *testing.T) {
	s := setup(t)
	release := make(chan struct{})
	s.SetEmbedFunc(func(ctx context.Context, conn, model string, texts []string) ([][]float32, error) {
		<-release
		out := make([][]float32, len(texts))
		for i := range texts {
			out[i] = []float32{0, 1}
		}
		return out, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx, nil)

	store, err := s.CreateStore(memories.StoreParams{
		Name: "Landing", EmbedConnection: "11111111-1111-1111-1111-111111111111", EmbedModel: "fixture",
	})
	require.NoError(t, err)
	it, err := s.Remember(store.ID, memories.MemoryParams{Text: "original", Importance: 0.5})
	require.NoError(t, err)

	// 0.0 then 1.0 as little-endian float32: 00 00 00 00, 00 00 80 3f.
	want := []byte{0, 0, 0, 0, 0, 0, 0x80, 0x3f}
	stored := func() bool {
		var row models.Memory
		return db.DB.Where("id = ?", it.ID).First(&row).Error == nil && string(row.Embedding) == string(want)
	}
	var once sync.Once
	require.NoError(t, db.DB.Callback().Update().Before("gorm:begin_transaction").Register("test:land", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*models.Memory); !ok {
			return
		}
		once.Do(func() {
			close(release)
			deadline := time.Now().Add(500 * time.Millisecond)
			for time.Now().Before(deadline) && !stored() {
				time.Sleep(10 * time.Millisecond)
			}
		})
	}))
	t.Cleanup(func() { _ = db.DB.Callback().Update().Remove("test:land") })

	_, err = s.UpdateMemory(store.ID, it.ID, memories.MemoryParams{Text: "original", Importance: 0.9})
	require.NoError(t, err)

	assert.Eventually(t, stored, 5*time.Second, 10*time.Millisecond,
		"a metadata edit must not erase the vector that landed while it ran")
	got, ok := s.GetMemory(store.ID, it.ID)
	require.True(t, ok, "the edited memory must still exist")
	assert.True(t, got.Embedded, "the cache must agree with the table that the memory is embedded")
	assert.Equal(t, 0.9, got.Importance, "the edit itself must land")
}

// TestReplaceSourceIsAllOrNothing pins the partial write the audit reproduced
// with an insert trigger: re-ingesting deleted the old chunks, wrote the first
// new one, and failed on the second, leaving the source neither old nor new.
// The same trigger now makes the whole replacement fail and the previous
// version stay, in the table and in recall.
func TestReplaceSourceIsAllOrNothing(t *testing.T) {
	s := setup(t)
	store, err := s.CreateStore(memories.StoreParams{Name: "Docs"})
	require.NoError(t, err)
	_, _, err = s.ReplaceSource(store.ID, "manual.pdf", []memories.MemoryParams{{Text: "version one of the manual"}})
	require.NoError(t, err)

	require.NoError(t, db.DB.Exec(`CREATE TRIGGER reject_bad BEFORE INSERT ON memories
		WHEN NEW.text = 'second chunk fails' BEGIN SELECT RAISE(ABORT, 'rejected'); END`).Error)
	t.Cleanup(func() { db.DB.Exec("DROP TRIGGER IF EXISTS reject_bad") })

	_, _, err = s.ReplaceSource(store.ID, "manual.pdf", []memories.MemoryParams{
		{Text: "first chunk of version two"}, {Text: "second chunk fails"},
	})
	require.Error(t, err, "a rejected chunk must fail the replacement")

	var texts []string
	require.NoError(t, db.DB.Model(&models.Memory{}).Where("store_id = ?", store.ID).Pluck("text", &texts).Error)
	assert.Equal(t, []string{"version one of the manual"}, texts, "the table must still hold the previous version and nothing of the failed one")
	got := s.Recall(context.Background(), store.ID, "manual version", memories.RecallOpts{})
	require.Len(t, got, 1, "recall must see the previous version alone")
	assert.Equal(t, "version one of the manual", got[0].Text, "the cache keeps the previous version too")
}
