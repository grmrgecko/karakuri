package engine

import (
	"encoding/json"
	"sync"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
)

// NodeStateSink persists durable node store entries. All methods must be
// non-blocking; implementations write asynchronously, so the in-memory value
// is authoritative for the running engine and the store is a durable copy.
type NodeStateSink interface {
	// Save writes one entry's JSON encoding.
	Save(flowID uuid.UUID, nodeID, key, value string)
	// Delete removes one entry.
	Delete(flowID uuid.UUID, nodeID, key string)
	// Close flushes queued writes and stops the writer.
	Close()
}

// durableStores holds the durable node stores of one engine, keyed by flow and
// node ID. Unlike the memory-only registry these are not dropped when a flow
// is rearmed: surviving a redeploy and a restart is their reason to exist.
// They are purged only when the flow itself is deleted.
type durableStores struct {
	mu     sync.Mutex
	stores map[uuid.UUID]map[string]*durableNodeStore
	sink   NodeStateSink
	log    *zap.Logger
}

// newDurableStores returns an empty registry writing through to sink, which
// may be nil when state should live only as long as the process (tests).
func newDurableStores(sink NodeStateSink, log *zap.Logger) *durableStores {
	return &durableStores{
		stores: make(map[uuid.UUID]map[string]*durableNodeStore),
		sink:   sink,
		log:    log,
	}
}

// get returns the durable store for one node of one flow, creating it on
// first use.
func (s *durableStores) get(flowID uuid.UUID, nodeID string) *durableNodeStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	byNode := s.stores[flowID]
	if byNode == nil {
		byNode = make(map[string]*durableNodeStore)
		s.stores[flowID] = byNode
	}
	st := byNode[nodeID]
	if st == nil {
		st = &durableNodeStore{
			values:  make(map[string]any),
			encoded: make(map[string]string),
			flowID:  flowID,
			nodeID:  nodeID,
			sink:    s.sink,
			log:     s.log,
		}
		byNode[nodeID] = st
	}
	return st
}

// load seeds the registry from persisted rows. Values that no longer decode
// are skipped rather than failing startup: a bad row should not keep the
// engine down.
func (s *durableStores) load(rows []*models.NodeStoreEntry, log *zap.Logger) {
	for _, row := range rows {
		var value any
		if err := json.Unmarshal([]byte(row.Value), &value); err != nil {
			if log != nil {
				log.Warn("skipping unreadable node store entry",
					zap.String("flow_id", row.FlowID.String()),
					zap.String("node_id", row.NodeID),
					zap.String("key", row.Key), zap.Error(err))
			}
			continue
		}
		st := s.get(row.FlowID, row.NodeID)
		st.mu.Lock()
		st.values[row.Key] = value
		st.encoded[row.Key] = row.Value
		st.mu.Unlock()
	}
}

// purge drops every durable store belonging to a flow. Called when the flow is
// deleted; the database rows go in the flow's own delete transaction.
func (s *durableStores) purge(flowID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.stores, flowID)
}

// NewMemDurableStore returns a standalone durable-store instance with no sink.
// Node tests use it so nodes see the same JSON normalization a persisted store
// applies, without standing up a database.
func NewMemDurableStore() NodeStore {
	return &durableNodeStore{
		values:  make(map[string]any),
		encoded: make(map[string]string),
		log:     zap.NewNop(),
	}
}

// durableNodeStore is the durable NodeStore implementation. Values are
// normalized through a JSON round trip on every write, so what a node reads
// back is always the persisted shape (map[string]any, []any, float64, string,
// bool) — the same before and after a restart, which is what keeps a
// comparison made today honest against a value stored yesterday. A value that
// cannot be encoded stays in memory and is not persisted, and the row of the
// value it replaced is removed, matching how variable writes treat the same
// failure.
type durableNodeStore struct {
	mu     sync.Mutex
	values map[string]any
	// encoded keeps each key's persisted JSON so a write that changes nothing
	// skips the sink: a dedupe node's common case is a duplicate message that
	// leaves its history exactly as it was.
	encoded map[string]string
	flowID  uuid.UUID
	nodeID  string
	sink    NodeStateSink
	log     *zap.Logger
}

// Get returns the value stored under key.
func (s *durableNodeStore) Get(key string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[key]
	return v, ok
}

// Set stores a value under key, mirroring it to the sink.
func (s *durableNodeStore) Set(key string, value any) {
	s.mu.Lock()
	s.write(key, value)
	s.mu.Unlock()
}

// Delete removes a key, mirroring the removal to the sink. The removal is
// queued under the lock, as every write is, so the sink sees changes in the
// order memory took them.
func (s *durableNodeStore) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	delete(s.encoded, key)
	if s.sink != nil {
		s.sink.Delete(s.flowID, s.nodeID, key)
	}
}

// Update applies fn to the current value under the lock and stores the
// result. A nil result deletes the key. The result fn returns is normalized
// before it is stored, so fn always reads back the JSON shape it wrote.
func (s *durableNodeStore) Update(key string, fn func(cur any, ok bool) any) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.values[key]
	next := fn(cur, ok)
	if next == nil {
		if !ok {
			return nil
		}
		delete(s.values, key)
		delete(s.encoded, key)
		if s.sink != nil {
			s.sink.Delete(s.flowID, s.nodeID, key)
		}
		return nil
	}
	return s.write(key, next)
}

// write normalizes a value through JSON, stores it, and mirrors it to the
// sink. Callers hold the lock. It returns the normalized value, or the raw
// one when encoding failed and the value stayed memory-only.
func (s *durableNodeStore) write(key string, value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		s.log.Warn("durable store value cannot be encoded; kept in memory only",
			zap.String("node_id", s.nodeID), zap.String("key", key), zap.Error(err))
		s.keepInMemory(key, value)
		return value
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		s.keepInMemory(key, value)
		return value
	}
	text := string(encoded)
	unchanged := s.encoded[key] == text
	s.values[key] = normalized
	s.encoded[key] = text
	if s.sink != nil && !unchanged {
		s.sink.Save(s.flowID, s.nodeID, key, text)
	}
	return normalized
}

// keepInMemory holds a value that cannot be persisted and removes the row of
// the value it replaces, so a restart finds the key unset instead of restoring
// a value the node has moved past. Callers hold the lock.
func (s *durableNodeStore) keepInMemory(key string, value any) {
	s.values[key] = value
	if _, stored := s.encoded[key]; stored && s.sink != nil {
		s.sink.Delete(s.flowID, s.nodeID, key)
	}
	delete(s.encoded, key)
}

// DBNodeStateSink persists durable node state from a single writer goroutine
// fed by a buffered channel, the same shape as the variable sink: a flow
// writing state never waits on the database.
type DBNodeStateSink struct {
	log    *zap.Logger
	ch     chan func()
	wg     sync.WaitGroup
	mu     sync.RWMutex
	closed bool
}

// NewDBNodeStateSink creates the sink and starts its writer goroutine.
func NewDBNodeStateSink(log *zap.Logger) *DBNodeStateSink {
	s := &DBNodeStateSink{log: log, ch: make(chan func(), varBuffer)}
	s.wg.Add(1)
	go s.writer()
	return s
}

// writer drains queued write operations until the channel closes.
func (s *DBNodeStateSink) writer() {
	defer s.wg.Done()
	for op := range s.ch {
		op()
	}
}

// enqueue submits a write, dropping it if the queue is full or the sink has
// closed. The in-memory value already reflects the change either way.
func (s *DBNodeStateSink) enqueue(op func()) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- op:
	default:
		s.log.Warn("node state write queue full, dropping write")
	}
}

// Save persists one entry.
func (s *DBNodeStateSink) Save(flowID uuid.UUID, nodeID, key, value string) {
	s.enqueue(func() {
		if err := models.UpsertNodeStoreEntry(flowID, nodeID, key, value); err != nil {
			s.log.Error("failed to persist node state",
				zap.String("node_id", nodeID), zap.String("key", key), zap.Error(err))
		}
	})
}

// Delete removes one persisted entry.
func (s *DBNodeStateSink) Delete(flowID uuid.UUID, nodeID, key string) {
	s.enqueue(func() {
		if err := models.DeleteNodeStoreEntry(flowID, nodeID, key); err != nil {
			s.log.Error("failed to delete node state",
				zap.String("node_id", nodeID), zap.String("key", key), zap.Error(err))
		}
	})
}

// Close flushes queued writes and stops the writer.
func (s *DBNodeStateSink) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.ch)
	s.mu.Unlock()
	s.wg.Wait()
}
