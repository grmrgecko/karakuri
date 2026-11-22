// Package memories implements the memory stores behind the AI nodes: named
// banks of conversation transcripts an agent resumes and facts it recalls,
// defined in settings and shared by name across flows the way controls are.
// The whole bank lives in memory backed by the database — recall runs on
// every agent prompt, so it is scored over the cache in microseconds, while
// writes go through the database first so a restart cannot lose what an agent
// was told it would remember. Embeddings are filled in by a background worker
// (see embed.go), so remembering never waits on a provider.
package memories

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// Scoring and retention defaults, applied when a store is created without
// explicit values.
const (
	DefaultHalfLifeDays    = 30.0
	DefaultRecencyWeight   = 0.25
	DefaultMaxSessionTurns = 200
	// DefaultImportance is the middle of the scale, the weight a memory gets
	// when nobody said how much it matters.
	DefaultImportance = 0.5
)

// S is the package-level store, wired by serve.go at boot and read by the
// memory nodes, the AI agent, and the API handlers.
var S = NewStore()

// StoreDef is a memory store's definition as everything outside the package
// sees it.
type StoreDef struct {
	ID              uuid.UUID `json:"id" description:"Unique identifier."`
	Name            string    `json:"name" description:"Display name, unique across memory stores."`
	Description     string    `json:"description" description:"What the store remembers."`
	EmbedConnection string    `json:"embed_connection" description:"AI provider connection UUID used for embeddings; empty disables semantic recall."`
	EmbedModel      string    `json:"embed_model" description:"Embedding model identifier."`
	HalfLifeDays    float64   `json:"half_life_days" description:"Recency decay halflife in days."`
	RecencyWeight   float64   `json:"recency_weight" description:"How much recency counts in recall scoring, 0 to 1."`
	MaxSessionTurns int       `json:"max_session_turns" description:"Conversation turns kept per session before the oldest are dropped."`
	MaxMemories     int       `json:"max_memories" description:"Fact cap; 0 is unlimited."`
	CreatedAt       time.Time `json:"created_at" description:"Creation timestamp."`
	UpdatedAt       time.Time `json:"updated_at" description:"Last update timestamp."`
}

// StoreSnapshot is a definition plus what the store currently holds.
type StoreSnapshot struct {
	StoreDef
	Facts    int `json:"facts" description:"Stored facts."`
	Turns    int `json:"turns" description:"Stored conversation turns."`
	Sessions int `json:"sessions" description:"Distinct sessions with turns."`
	// Pending counts memories awaiting an embedding, which is how the settings
	// page shows semantic recall still catching up after a config change.
	Pending int `json:"pending" description:"Memories not yet embedded."`
}

// Item is one memory as everything outside the store sees it.
type Item struct {
	ID         uuid.UUID  `json:"id" description:"Unique identifier."`
	StoreID    uuid.UUID  `json:"store_id" description:"Memory store this belongs to."`
	Kind       string     `json:"kind" description:"Memory kind: fact or turn."`
	Session    string     `json:"session,omitempty" description:"Session key; empty is global to the store."`
	Role       string     `json:"role,omitempty" description:"Turn role: user or assistant."`
	Text       string     `json:"text" description:"The remembered text."`
	Source     string     `json:"source,omitempty" description:"Document the fact was ingested from; empty for remembered facts."`
	Tags       []string   `json:"tags,omitempty" description:"Tags matched alongside the text in recall."`
	Importance float64    `json:"importance" description:"Recall weight, 0 to 1."`
	Embedded   bool       `json:"embedded" description:"Whether a vector is stored for semantic recall."`
	ExpiresAt  *time.Time `json:"expires_at,omitempty" description:"Optional expiry."`
	CreatedAt  time.Time  `json:"created_at" description:"Creation timestamp."`
}

// Result is one recalled memory with its score.
type Result struct {
	Item
	Score float64 `json:"score" description:"Recall score, 0 to 1."`
}

// Turn is one side of a conversation exchange being appended.
type Turn struct {
	Role string
	Text string
}

// SessionInfo summarizes one session's transcript for the settings page.
type SessionInfo struct {
	Session string    `json:"session" description:"Session key."`
	Turns   int       `json:"turns" description:"Stored turns."`
	LastAt  time.Time `json:"last_at" description:"When the newest turn landed."`
}

// StoreParams carries a store definition being created or updated.
type StoreParams struct {
	Name            string
	Description     string
	EmbedConnection string
	EmbedModel      string
	HalfLifeDays    float64
	RecencyWeight   float64
	MaxSessionTurns int
	MaxMemories     int
}

// MemoryParams carries a memory being created or edited.
type MemoryParams struct {
	Kind       string
	Session    string
	Role       string
	Text       string
	Source     string
	Tags       []string
	Importance float64
	ExpiresAt  *time.Time
}

// item is the cached form of one memory: the exported view plus what scoring
// needs precomputed — the decoded vector and the token set — and a sequence
// number that keeps transcript order stable inside one clock tick.
type item struct {
	Item
	vector []float32
	tokens map[string]struct{}
	seq    int64
}

// storeEntry is one bank's cache.
type storeEntry struct {
	def   StoreDef
	items map[uuid.UUID]*item
}

// Store holds every memory store in memory, backed by the database.
type Store struct {
	mu      sync.RWMutex
	stores  map[uuid.UUID]*storeEntry
	nextSeq int64

	embed embedWorker
	// sourceLocks holds one mutex per store and source, so replacing a
	// source is never interleaved with another replacement of it.
	sourceLocks sync.Map
}

// NewStore returns an empty store. Production code uses the package-level S;
// tests build their own so the embed worker can be pointed at a fixture.
func NewStore() *Store {
	s := &Store{stores: make(map[uuid.UUID]*storeEntry)}
	s.embed.init()
	return s
}

// Load reads every store and memory into the cache, replacing what is there,
// and queues embedding for anything missing a vector — which is how a store
// that gained an embedding connection backfills itself.
func (s *Store) Load() error {
	storeRows, err := models.ListMemoryStores()
	if err != nil {
		return err
	}
	memoryRows, err := models.ListMemories()
	if err != nil {
		return err
	}

	next := make(map[uuid.UUID]*storeEntry, len(storeRows))
	for _, row := range storeRows {
		next[row.ID] = &storeEntry{def: defOf(row), items: make(map[uuid.UUID]*item)}
	}

	s.mu.Lock()
	var seq int64
	var pending []uuid.UUID
	for _, row := range memoryRows {
		entry, ok := next[row.StoreID]
		if !ok {
			continue
		}
		seq++
		it := itemOf(row, seq)
		entry.items[row.ID] = it
		if !it.Embedded && entry.def.EmbedConnection != "" {
			pending = append(pending, row.ID)
		}
	}
	s.stores = next
	s.nextSeq = seq
	s.mu.Unlock()

	s.embed.enqueue(pending...)
	return nil
}

// ListStores returns every store ordered by name.
func (s *Store) ListStores() []StoreSnapshot {
	s.mu.RLock()
	out := make([]StoreSnapshot, 0, len(s.stores))
	for _, e := range s.stores {
		out = append(out, snapshotOf(e))
	}
	s.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// GetStore returns one store by ID.
func (s *Store) GetStore(id uuid.UUID) (StoreSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.stores[id]
	if !ok {
		return StoreSnapshot{}, false
	}
	return snapshotOf(e), true
}

// ResolveStore looks a store up by ID or, failing that, by name — flows hold
// IDs so a rename cannot break a graph, and a name is what an author types.
func (s *Store) ResolveStore(ref string) (StoreSnapshot, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return StoreSnapshot{}, false
	}
	if id, err := uuid.Parse(ref); err == nil {
		if snap, ok := s.GetStore(id); ok {
			return snap, true
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.stores {
		if strings.EqualFold(e.def.Name, ref) {
			return snapshotOf(e), true
		}
	}
	return StoreSnapshot{}, false
}

// CreateStore defines a memory store, filling scoring defaults where the
// params leave zero values.
func (s *Store) CreateStore(p StoreParams) (StoreSnapshot, error) {
	p, err := normalizeStoreParams(p)
	if err != nil {
		return StoreSnapshot{}, err
	}
	row := &models.MemoryStore{
		Name:            p.Name,
		Description:     p.Description,
		EmbedConnection: p.EmbedConnection,
		EmbedModel:      p.EmbedModel,
		HalfLifeDays:    p.HalfLifeDays,
		RecencyWeight:   p.RecencyWeight,
		MaxSessionTurns: p.MaxSessionTurns,
		MaxMemories:     p.MaxMemories,
	}
	if err := models.CreateMemoryStore(row); err != nil {
		return StoreSnapshot{}, err
	}

	entry := &storeEntry{def: defOf(row), items: make(map[uuid.UUID]*item)}
	s.mu.Lock()
	s.stores[row.ID] = entry
	s.mu.Unlock()
	return snapshotOf(entry), nil
}

// UpdateStore rewrites a store's definition. Changing the embedding
// connection or model invalidates every stored vector: they were made in
// another model's space, so they are cleared and re-queued rather than
// compared against queries they cannot match.
func (s *Store) UpdateStore(id uuid.UUID, p StoreParams) (StoreSnapshot, error) {
	p, err := normalizeStoreParams(p)
	if err != nil {
		return StoreSnapshot{}, err
	}

	s.mu.RLock()
	e, ok := s.stores[id]
	var old StoreDef
	if ok {
		old = e.def
	}
	s.mu.RUnlock()
	if !ok {
		return StoreSnapshot{}, fmt.Errorf("memory store not found")
	}

	row := &models.MemoryStore{
		ID:              id,
		Name:            p.Name,
		Description:     p.Description,
		EmbedConnection: p.EmbedConnection,
		EmbedModel:      p.EmbedModel,
		HalfLifeDays:    p.HalfLifeDays,
		RecencyWeight:   p.RecencyWeight,
		MaxSessionTurns: p.MaxSessionTurns,
		MaxMemories:     p.MaxMemories,
		CreatedAt:       old.CreatedAt,
	}
	if err := models.SaveMemoryStore(row); err != nil {
		return StoreSnapshot{}, err
	}

	embeddingChanged := old.EmbedConnection != p.EmbedConnection || old.EmbedModel != p.EmbedModel

	s.mu.Lock()
	e.def = defOf(row)
	var requeue []uuid.UUID
	if embeddingChanged {
		for _, it := range e.items {
			it.vector = nil
			it.Embedded = false
			if p.EmbedConnection != "" {
				requeue = append(requeue, it.ID)
			}
		}
	}
	snap := snapshotOf(e)
	s.mu.Unlock()

	if embeddingChanged {
		// Vectors are cleared in the table too, or a restart would resurrect
		// them into the wrong space.
		for _, mid := range requeue {
			_ = models.UpdateMemoryEmbedding(mid, nil)
		}
		s.embed.enqueue(requeue...)
	}
	return snap, nil
}

// DeleteStore removes a store and everything it remembers.
func (s *Store) DeleteStore(id uuid.UUID) error {
	if err := models.DeleteMemoryStore(id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.stores, id)
	s.mu.Unlock()
	return nil
}

// Remember stores one memory and queues its embedding. It is the single write
// path facts take, whether from a node, an agent's remember tool, or the
// settings page.
func (s *Store) Remember(storeID uuid.UUID, p MemoryParams) (Item, error) {
	p, err := normalizeMemoryParams(p)
	if err != nil {
		return Item{}, err
	}
	s.mu.RLock()
	e, ok := s.stores[storeID]
	s.mu.RUnlock()
	if !ok {
		return Item{}, fmt.Errorf("memory store not found")
	}

	row := &models.Memory{
		StoreID:    storeID,
		Kind:       p.Kind,
		Session:    p.Session,
		Role:       p.Role,
		Text:       p.Text,
		Source:     p.Source,
		Tags:       encodeTags(p.Tags),
		Importance: p.Importance,
		ExpiresAt:  p.ExpiresAt,
	}
	if err := models.CreateMemory(row); err != nil {
		return Item{}, err
	}

	s.mu.Lock()
	s.nextSeq++
	it := itemOf(row, s.nextSeq)
	e.items[row.ID] = it
	evicted := s.enforceCapsLocked(e, p.Kind, p.Session)
	embedNeeded := e.def.EmbedConnection != ""
	s.mu.Unlock()

	if len(evicted) > 0 {
		_ = models.DeleteMemoriesByID(evicted)
	}
	if embedNeeded {
		s.embed.enqueue(row.ID)
	}
	return it.Item, nil
}

// AppendExchange stores one conversation exchange — the user's prompt and the
// assistant's answer — as two turns in a session's transcript.
func (s *Store) AppendExchange(storeID uuid.UUID, session, userText, assistantText string) error {
	for _, t := range []Turn{{Role: "user", Text: userText}, {Role: "assistant", Text: assistantText}} {
		if strings.TrimSpace(t.Text) == "" {
			continue
		}
		if _, err := s.Remember(storeID, MemoryParams{
			Kind: models.MemoryKindTurn, Session: session, Role: t.Role, Text: t.Text,
		}); err != nil {
			return err
		}
	}
	return nil
}

// Transcript returns a session's newest turns in chronological order, at most
// max (0 means every stored turn).
func (s *Store) Transcript(storeID uuid.UUID, session string, max int) []Item {
	now := time.Now()
	s.mu.RLock()
	e, ok := s.stores[storeID]
	if !ok {
		s.mu.RUnlock()
		return nil
	}
	var turns []*item
	for _, it := range e.items {
		if it.Kind == models.MemoryKindTurn && it.Session == session && !expired(it, now) {
			turns = append(turns, it)
		}
	}
	s.mu.RUnlock()

	sort.Slice(turns, func(i, j int) bool { return turns[i].seq < turns[j].seq })
	if max > 0 && len(turns) > max {
		turns = turns[len(turns)-max:]
	}
	out := make([]Item, 0, len(turns))
	for _, it := range turns {
		out = append(out, it.Item)
	}
	return out
}

// Sessions lists a store's sessions with turn counts, newest activity first.
func (s *Store) Sessions(storeID uuid.UUID) []SessionInfo {
	now := time.Now()
	s.mu.RLock()
	e, ok := s.stores[storeID]
	if !ok {
		s.mu.RUnlock()
		return nil
	}
	bySession := map[string]*SessionInfo{}
	for _, it := range e.items {
		if it.Kind != models.MemoryKindTurn || expired(it, now) {
			continue
		}
		info := bySession[it.Session]
		if info == nil {
			info = &SessionInfo{Session: it.Session}
			bySession[it.Session] = info
		}
		info.Turns++
		if it.CreatedAt.After(info.LastAt) {
			info.LastAt = it.CreatedAt
		}
	}
	s.mu.RUnlock()

	out := make([]SessionInfo, 0, len(bySession))
	for _, info := range bySession {
		out = append(out, *info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	return out
}

// ReplaceSource replaces every memory a source contributed with new ones, as
// re-ingesting a document does. The table swap is one transaction, so a failed
// write leaves the previous chunks in place, and replacements of the same
// source run one at a time, so two ingests cannot interleave their chunks. It
// returns how many memories were replaced and the new ones.
func (s *Store) ReplaceSource(storeID uuid.UUID, source string, params []MemoryParams) (int, []Item, error) {
	unlock := s.lockSource(storeID, source)
	defer unlock()

	s.mu.RLock()
	e, ok := s.stores[storeID]
	s.mu.RUnlock()
	if !ok {
		return 0, nil, fmt.Errorf("memory store not found")
	}
	rows := make([]*models.Memory, len(params))
	for i, p := range params {
		p.Source = source
		p, err := normalizeMemoryParams(p)
		if err != nil {
			return 0, nil, fmt.Errorf("memory %d of %d: %w", i+1, len(params), err)
		}
		rows[i] = &models.Memory{
			ID: uuid.New(), StoreID: storeID, Kind: p.Kind, Session: p.Session, Role: p.Role,
			Text: p.Text, Source: p.Source, Tags: encodeTags(p.Tags),
			Importance: p.Importance, ExpiresAt: p.ExpiresAt,
		}
	}
	if err := models.ReplaceMemoriesBySource(storeID, source, rows); err != nil {
		return 0, nil, err
	}

	s.mu.Lock()
	removed := 0
	for id, it := range e.items {
		if it.Source == source {
			delete(e.items, id)
			removed++
		}
	}
	items := make([]Item, 0, len(rows))
	var evicted []uuid.UUID
	for _, row := range rows {
		s.nextSeq++
		it := itemOf(row, s.nextSeq)
		e.items[row.ID] = it
		items = append(items, it.Item)
		evicted = append(evicted, s.enforceCapsLocked(e, row.Kind, row.Session)...)
	}
	embedNeeded := e.def.EmbedConnection != ""
	s.mu.Unlock()

	if len(evicted) > 0 {
		_ = models.DeleteMemoriesByID(evicted)
	}
	if embedNeeded {
		ids := make([]uuid.UUID, len(rows))
		for i, row := range rows {
			ids[i] = row.ID
		}
		s.embed.enqueue(ids...)
	}
	return removed, items, nil
}

// lockSource serializes work on one store's source and returns the unlock.
func (s *Store) lockSource(storeID uuid.UUID, source string) func() {
	key := storeID.String() + "\x00" + source
	v, _ := s.sourceLocks.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// ClearSession forgets one session's memories — its transcript and any facts
// scoped to it. The empty session is the shared conversation: its key is also
// the key of every global fact, so clearing it forgets the turns alone.
func (s *Store) ClearSession(storeID uuid.UUID, session string) error {
	if session == "" {
		return s.clearSharedTranscript(storeID)
	}
	if err := models.DeleteMemoriesBySession(storeID, session); err != nil {
		return err
	}
	s.mu.Lock()
	if e, ok := s.stores[storeID]; ok {
		for id, it := range e.items {
			if it.Session == session {
				delete(e.items, id)
			}
		}
	}
	s.mu.Unlock()
	return nil
}

// clearSharedTranscript deletes the turns stored under the empty session and
// leaves the global facts that share its key.
func (s *Store) clearSharedTranscript(storeID uuid.UUID) error {
	s.mu.RLock()
	var ids []uuid.UUID
	if e, ok := s.stores[storeID]; ok {
		for id, it := range e.items {
			if it.Kind == models.MemoryKindTurn && it.Session == "" {
				ids = append(ids, id)
			}
		}
	}
	s.mu.RUnlock()

	if err := models.DeleteMemoriesByID(ids); err != nil {
		return err
	}
	s.mu.Lock()
	if e, ok := s.stores[storeID]; ok {
		for _, id := range ids {
			delete(e.items, id)
		}
	}
	s.mu.Unlock()
	return nil
}

// GetMemory returns one memory by ID.
func (s *Store) GetMemory(storeID, id uuid.UUID) (Item, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.stores[storeID]
	if !ok {
		return Item{}, false
	}
	it, ok := e.items[id]
	if !ok {
		return Item{}, false
	}
	return it.Item, true
}

// UpdateMemory edits a memory. A text change invalidates the vector, so it is
// cleared and re-queued. The read, the row write, and the cache swap hold the
// store lock: an edit that keeps the text saves the current vector, and an
// embedding landing between that read and the write would be overwritten in
// the table while the cache kept it.
func (s *Store) UpdateMemory(storeID, id uuid.UUID, p MemoryParams) (Item, error) {
	p, err := normalizeMemoryParams(p)
	if err != nil {
		return Item{}, err
	}
	s.mu.Lock()
	e, ok := s.stores[storeID]
	var cur *item
	if ok {
		cur = e.items[id]
	}
	if cur == nil {
		s.mu.Unlock()
		return Item{}, fmt.Errorf("memory not found")
	}

	// The embedded text folds in the tags, so a tag edit needs a new vector
	// as much as a text edit does.
	textChanged := cur.Text != p.Text || encodeTags(cur.Tags) != encodeTags(p.Tags)
	// Source is provenance, not content: an edit keeps where the fact came
	// from, so re-ingesting the document still replaces it.
	row := &models.Memory{
		ID: id, StoreID: storeID,
		Kind: p.Kind, Session: p.Session, Role: p.Role,
		Text: p.Text, Source: cur.Source, Tags: encodeTags(p.Tags),
		Importance: p.Importance, ExpiresAt: p.ExpiresAt,
		CreatedAt: cur.CreatedAt,
	}
	if !textChanged {
		row.Embedding = vectorBytes(cur.vector)
	}
	if err := models.SaveMemory(row); err != nil {
		s.mu.Unlock()
		return Item{}, err
	}

	next := itemOf(row, cur.seq)
	if !textChanged {
		next.vector = cur.vector
		next.Embedded = cur.Embedded
	}
	e.items[id] = next
	embedNeeded := textChanged && e.def.EmbedConnection != ""
	s.mu.Unlock()

	if embedNeeded {
		s.embed.enqueue(id)
	}
	return next.Item, nil
}

// DeleteMemory removes one memory.
func (s *Store) DeleteMemory(storeID, id uuid.UUID) error {
	if err := models.DeleteMemory(id); err != nil {
		return err
	}
	s.mu.Lock()
	if e, ok := s.stores[storeID]; ok {
		delete(e.items, id)
	}
	s.mu.Unlock()
	return nil
}

// Sweep deletes expired memories from the table and the cache. Reads already
// skip them, so this is hygiene rather than correctness, run at load and on
// the maintenance ticker.
func (s *Store) Sweep() error {
	now := time.Now()
	if err := models.DeleteExpiredMemories(now); err != nil {
		return err
	}
	s.mu.Lock()
	for _, e := range s.stores {
		for id, it := range e.items {
			if expired(it, now) {
				delete(e.items, id)
			}
		}
	}
	s.mu.Unlock()
	return nil
}

// enforceCapsLocked trims what the write pushed over a limit and returns the
// evicted IDs for the caller to delete from the table (the cache is trimmed
// here). Turns are capped per session, oldest first; facts by the store cap,
// least important first with age as the tiebreak.
func (s *Store) enforceCapsLocked(e *storeEntry, kind, session string) []uuid.UUID {
	var evict []*item
	switch kind {
	case models.MemoryKindTurn:
		if e.def.MaxSessionTurns <= 0 {
			return nil
		}
		var turns []*item
		for _, it := range e.items {
			if it.Kind == models.MemoryKindTurn && it.Session == session {
				turns = append(turns, it)
			}
		}
		if len(turns) <= e.def.MaxSessionTurns {
			return nil
		}
		sort.Slice(turns, func(i, j int) bool { return turns[i].seq < turns[j].seq })
		evict = turns[:len(turns)-e.def.MaxSessionTurns]

	default:
		if e.def.MaxMemories <= 0 {
			return nil
		}
		var facts []*item
		for _, it := range e.items {
			if it.Kind == models.MemoryKindFact {
				facts = append(facts, it)
			}
		}
		if len(facts) <= e.def.MaxMemories {
			return nil
		}
		sort.Slice(facts, func(i, j int) bool {
			if facts[i].Importance != facts[j].Importance {
				return facts[i].Importance < facts[j].Importance
			}
			return facts[i].seq < facts[j].seq
		})
		evict = facts[:len(facts)-e.def.MaxMemories]
	}

	ids := make([]uuid.UUID, 0, len(evict))
	for _, it := range evict {
		delete(e.items, it.ID)
		ids = append(ids, it.ID)
	}
	return ids
}

// normalizeStoreParams validates a store definition and fills defaults.
func normalizeStoreParams(p StoreParams) (StoreParams, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return p, fmt.Errorf("name is required")
	}
	p.EmbedConnection = strings.TrimSpace(p.EmbedConnection)
	p.EmbedModel = strings.TrimSpace(p.EmbedModel)
	if p.EmbedConnection != "" && p.EmbedModel == "" {
		return p, fmt.Errorf("an embedding connection needs an embedding model")
	}
	if p.HalfLifeDays < 0 {
		return p, fmt.Errorf("half-life must be positive")
	}
	if p.HalfLifeDays == 0 {
		p.HalfLifeDays = DefaultHalfLifeDays
	}
	if p.RecencyWeight < 0 || p.RecencyWeight > 1 {
		return p, fmt.Errorf("recency weight must be between 0 and 1")
	}
	if p.MaxSessionTurns < 0 || p.MaxMemories < 0 {
		return p, fmt.Errorf("caps cannot be negative")
	}
	if p.MaxSessionTurns == 0 {
		p.MaxSessionTurns = DefaultMaxSessionTurns
	}
	return p, nil
}

// normalizeMemoryParams validates a memory and fills defaults.
func normalizeMemoryParams(p MemoryParams) (MemoryParams, error) {
	p.Text = strings.TrimSpace(p.Text)
	if p.Text == "" {
		return p, fmt.Errorf("text is required")
	}
	if p.Kind == "" {
		p.Kind = models.MemoryKindFact
	}
	if p.Kind != models.MemoryKindFact && p.Kind != models.MemoryKindTurn {
		return p, fmt.Errorf("kind must be fact or turn")
	}
	if p.Importance == 0 {
		p.Importance = DefaultImportance
	}
	if p.Importance < 0 || p.Importance > 1 {
		return p, fmt.Errorf("importance must be between 0 and 1")
	}
	clean := p.Tags[:0]
	for _, tag := range p.Tags {
		if tag = strings.TrimSpace(tag); tag != "" {
			clean = append(clean, tag)
		}
	}
	p.Tags = clean
	return p, nil
}

// defOf decodes a stored store row into its definition.
func defOf(row *models.MemoryStore) StoreDef {
	return StoreDef{
		ID:              row.ID,
		Name:            row.Name,
		Description:     row.Description,
		EmbedConnection: row.EmbedConnection,
		EmbedModel:      row.EmbedModel,
		HalfLifeDays:    row.HalfLifeDays,
		RecencyWeight:   row.RecencyWeight,
		MaxSessionTurns: row.MaxSessionTurns,
		MaxMemories:     row.MaxMemories,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

// snapshotOf counts what a store holds. Callers hold at least a read lock.
func snapshotOf(e *storeEntry) StoreSnapshot {
	snap := StoreSnapshot{StoreDef: e.def}
	sessions := map[string]struct{}{}
	for _, it := range e.items {
		switch it.Kind {
		case models.MemoryKindTurn:
			snap.Turns++
			sessions[it.Session] = struct{}{}
		default:
			snap.Facts++
		}
		if !it.Embedded && e.def.EmbedConnection != "" {
			snap.Pending++
		}
	}
	snap.Sessions = len(sessions)
	return snap
}

// itemOf decodes a stored memory row into its cached form, tokens and vector
// included.
func itemOf(row *models.Memory, seq int64) *item {
	tags := decodeTags(row.Tags)
	it := &item{
		Item: Item{
			ID:         row.ID,
			StoreID:    row.StoreID,
			Kind:       row.Kind,
			Session:    row.Session,
			Role:       row.Role,
			Text:       row.Text,
			Source:     row.Source,
			Tags:       tags,
			Importance: row.Importance,
			Embedded:   len(row.Embedding) > 0,
			ExpiresAt:  row.ExpiresAt,
			CreatedAt:  row.CreatedAt,
		},
		vector: bytesVector(row.Embedding),
		seq:    seq,
	}
	it.tokens = tokenize(row.Text)
	for _, tag := range tags {
		for tok := range tokenize(tag) {
			it.tokens[tok] = struct{}{}
		}
	}
	return it
}

// expired reports whether a memory's TTL has passed.
func expired(it *item, now time.Time) bool {
	return it.ExpiresAt != nil && !it.ExpiresAt.After(now)
}

// encodeTags serializes a tag list, empty as the empty string so an untagged
// memory stores nothing.
func encodeTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return ""
	}
	return string(raw)
}

// decodeTags reads a stored tag list, tolerating anything unreadable as no
// tags.
func decodeTags(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil
	}
	return tags
}
