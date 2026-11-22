package models

import (
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// MemoryStore is a named bank of memories: conversation transcripts an AI
// agent resumes and facts it recalls. Stores are defined in settings and
// referenced by name or ID from the ai-memory node, so two flows naming the
// same store share one memory the way two flows naming one control share its
// value. Scoring settings live on the store rather than the node because what
// "relevant" means is a property of the bank, not of whoever is asking.
type MemoryStore struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	Name        string    `gorm:"uniqueIndex;not null" json:"name" description:"Display name, unique across memory stores."`
	Description string    `json:"description" description:"What the store remembers, for the settings page."`
	// EmbedConnection and EmbedModel name an AI provider connection and its
	// embedding model. Set, recall scores by vector similarity blended with
	// keywords; empty, recall is keyword scoring alone.
	EmbedConnection string `json:"embed_connection" description:"AI provider connection UUID used for embeddings; empty disables semantic recall."`
	EmbedModel      string `json:"embed_model" description:"Embedding model identifier as the provider spells it."`
	// HalfLifeDays is the recency decay halflife: a memory this many days old
	// scores half the recency of one made now.
	HalfLifeDays float64 `json:"half_life_days" description:"Recency decay halflife in days."`
	// RecencyWeight blends recency into the recall score, 0 to 1. Zero ranks
	// purely by relevance; one ranks purely by age.
	RecencyWeight float64 `json:"recency_weight" description:"How much recency counts in recall scoring, 0 to 1."`
	// MaxSessionTurns caps one session's stored transcript; the oldest turns
	// are dropped as new ones land.
	MaxSessionTurns int `json:"max_session_turns" description:"Conversation turns kept per session before the oldest are dropped."`
	// MaxMemories caps the store's fact count; 0 is unlimited. When the cap
	// trips, the lowest-scoring facts are evicted first.
	MaxMemories int       `json:"max_memories" description:"Fact cap for the store; 0 is unlimited."`
	CreatedAt   time.Time `json:"created_at" description:"Creation timestamp."`
	UpdatedAt   time.Time `json:"updated_at" description:"Last update timestamp."`
}

// BeforeCreate assigns a UUID when none is set.
func (m *MemoryStore) BeforeCreate(tx *gorm.DB) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	return nil
}

// Memory kinds.
const (
	MemoryKindFact = "fact"
	MemoryKindTurn = "turn"
)

// Memory is one remembered item in a store: a fact an agent (or an operator)
// saved, or one turn of a conversation transcript. Facts are what recall
// searches; turns are what the agent replays as context, and recall can be
// asked to search them too.
type Memory struct {
	ID      uuid.UUID `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	StoreID uuid.UUID `gorm:"type:uuid;index;not null" json:"store_id" description:"Memory store this belongs to."`
	// Kind is "fact" or "turn".
	Kind string `gorm:"index;not null" json:"kind" description:"Memory kind: fact or turn."`
	// Session scopes the memory to one conversation; empty is store-global.
	// Turns always carry one, facts may.
	Session string `gorm:"index" json:"session" description:"Session key; empty is global to the store."`
	// Role is which side of the conversation a turn came from, user or
	// assistant; empty on facts.
	Role string `json:"role,omitempty" description:"Turn role: user or assistant."`
	Text string `gorm:"type:text;not null" json:"text" description:"The remembered text."`
	// Tags is a JSON array of strings, matched alongside the text in keyword
	// scoring.
	Tags string `gorm:"type:text" json:"-"`
	// Source names the document a fact was ingested from. Re-ingesting a
	// source replaces every fact carrying its name, which is what keeps a
	// re-read file from piling up beside its old chunks. Empty on memories
	// that were remembered rather than ingested.
	Source string `gorm:"index" json:"source,omitempty" description:"Document the fact was ingested from; empty for remembered facts."`
	// Importance weights recall, 0 to 1.
	Importance float64 `json:"importance" description:"Recall weight, 0 to 1."`
	// Embedding is the stored vector as little-endian float32 bytes; empty
	// means not embedded yet (or the store has no embedding connection).
	Embedding []byte `json:"-"`
	// ExpiresAt is an optional TTL; an expired memory is skipped by every read
	// and swept from the table.
	ExpiresAt *time.Time `json:"expires_at,omitempty" description:"Optional expiry; the memory is dropped after this time."`
	CreatedAt time.Time  `json:"created_at" description:"Creation timestamp."`
	UpdatedAt time.Time  `json:"updated_at" description:"Last update timestamp."`
}

// BeforeCreate assigns a UUID when none is set.
func (m *Memory) BeforeCreate(tx *gorm.DB) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	return nil
}

// ListMemoryStores returns every memory store ordered by name.
func ListMemoryStores() ([]*MemoryStore, error) {
	var stores []*MemoryStore
	err := db.DB.Order("name").Find(&stores).Error
	return stores, err
}

// CreateMemoryStore inserts a memory store.
func CreateMemoryStore(m *MemoryStore) error {
	return db.DB.Create(m).Error
}

// SaveMemoryStore persists every field of a memory store.
func SaveMemoryStore(m *MemoryStore) error {
	return db.DB.Save(m).Error
}

// DeleteMemoryStore removes a store and everything it remembers.
func DeleteMemoryStore(id uuid.UUID) error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&Memory{}, "store_id = ?", id).Error; err != nil {
			return err
		}
		return tx.Delete(&MemoryStore{}, "id = ?", id).Error
	})
}

// ListMemories returns every memory across all stores, oldest first, which is
// the load order that keeps transcript replay chronological.
func ListMemories() ([]*Memory, error) {
	var memories []*Memory
	err := db.DB.Order("created_at, id").Find(&memories).Error
	return memories, err
}

// CreateMemory inserts a memory.
func CreateMemory(m *Memory) error {
	return db.DB.Create(m).Error
}

// ReplaceMemoriesBySource swaps a source's memories for new rows in one
// transaction, so a failure part way leaves the old source whole rather than
// gone with only some of its replacement written.
func ReplaceMemoriesBySource(storeID uuid.UUID, source string, rows []*Memory) error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&Memory{}, "store_id = ? AND source = ?", storeID, source).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// SaveMemory persists every field of a memory.
func SaveMemory(m *Memory) error {
	return db.DB.Save(m).Error
}

// DeleteMemory removes one memory.
func DeleteMemory(id uuid.UUID) error {
	return db.DB.Delete(&Memory{}, "id = ?", id).Error
}

// DeleteMemoriesByID removes a batch of memories in one statement, which is
// what trimming a transcript and evicting over-cap facts do.
func DeleteMemoriesByID(ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	return db.DB.Delete(&Memory{}, "id IN ?", ids).Error
}

// DeleteMemoriesBySession removes one session's memories from a store.
func DeleteMemoriesBySession(storeID uuid.UUID, session string) error {
	return db.DB.Delete(&Memory{}, "store_id = ? AND session = ?", storeID, session).Error
}

// UpdateMemoryEmbeddingIf stores a vector only while the row still holds the
// text and tags it was computed from, and reports whether it did. An edit that
// lands while the provider is answering changes either, so the stale vector is
// never written over the invalidation.
func UpdateMemoryEmbeddingIf(id uuid.UUID, embedding []byte, text, tags string) (bool, error) {
	tx := db.DB.Model(&Memory{}).Where("id = ? AND text = ? AND COALESCE(tags, '') = ?", id, text, tags).
		Update("embedding", embedding)
	return tx.RowsAffected > 0, tx.Error
}

// UpdateMemoryEmbedding writes only the vector, unconditionally, which is what
// clearing vectors after an embedding model change needs.
func UpdateMemoryEmbedding(id uuid.UUID, embedding []byte) error {
	return db.DB.Model(&Memory{}).Where("id = ?", id).
		Update("embedding", embedding).Error
}

// DeleteExpiredMemories sweeps every memory whose TTL has passed.
func DeleteExpiredMemories(now time.Time) error {
	return db.DB.Delete(&Memory{}, "expires_at IS NOT NULL AND expires_at <= ?", now).Error
}
