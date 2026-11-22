package engine

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// nodeStateRecorder captures durable-store writes the way the DB sink would
// persist them, so a test can replay them into a fresh registry.
type nodeStateRecorder struct {
	rows    map[[3]string]string
	saves   int
	deletes int
}

func newNodeStateRecorder() *nodeStateRecorder {
	return &nodeStateRecorder{rows: make(map[[3]string]string)}
}

func (s *nodeStateRecorder) Save(flowID uuid.UUID, nodeID, key, value string) {
	s.rows[[3]string{flowID.String(), nodeID, key}] = value
	s.saves++
}

func (s *nodeStateRecorder) Delete(flowID uuid.UUID, nodeID, key string) {
	delete(s.rows, [3]string{flowID.String(), nodeID, key})
	s.deletes++
}

func (s *nodeStateRecorder) Close() {}

// entries renders the captured rows as the model rows LoadNodeState takes.
func (s *nodeStateRecorder) entries() []*models.NodeStoreEntry {
	out := make([]*models.NodeStoreEntry, 0, len(s.rows))
	for k, v := range s.rows {
		out = append(out, &models.NodeStoreEntry{
			FlowID: uuid.MustParse(k[0]), NodeID: k[1], Key: k[2], Value: v,
		})
	}
	return out
}

// TestDurableStoreRoundTrip locks the on-disk invariant the durable store
// stands on: a value a node writes is normalized to its JSON shape
// immediately, so what the node reads back before a restart is byte-for-byte
// the shape it reads after the persisted rows are loaded into a fresh
// registry. It also pins that a write changing nothing skips the sink — a
// dedupe node's common case is a duplicate message re-storing an identical
// history — and that purging a flow forgets its state.
func TestDurableStoreRoundTrip(t *testing.T) {
	sink := newNodeStateRecorder()
	stores := newDurableStores(sink, zap.NewNop())
	flowID := uuid.New()
	st := stores.get(flowID, "n1")

	// Writes in the Go shapes nodes use: a string list via Update, a plain
	// number, a nested map via Set.
	st.Update("seen", func(cur any, ok bool) any {
		require.False(t, ok, "a fresh store has no history")
		return []string{"a", "b"}
	})
	st.Set("max", 21.5)
	st.Set("state", map[string]any{"kitchen": 20.0, "hall": true})

	// The Update result and later reads are already the normalized JSON
	// shapes: []string came back as []any.
	seen, ok := st.Get("seen")
	require.True(t, ok, "the written key must be readable")
	assert.Equal(t, []any{"a", "b"}, seen, "a string slice must normalize to the JSON list shape on write")

	// A write that changes nothing must not reach the sink.
	before := sink.saves
	st.Update("seen", func(cur any, ok bool) any { return cur })
	assert.Equal(t, before, sink.saves, "re-storing an identical value must skip the sink")

	// Restart: load the persisted rows into a fresh registry and compare
	// every key against the pre-restart reads.
	reloaded := newDurableStores(nil, zap.NewNop())
	reloaded.load(sink.entries(), zap.NewNop())
	st2 := reloaded.get(flowID, "n1")
	for _, key := range []string{"seen", "max", "state"} {
		want, _ := st.Get(key)
		got, ok := st2.Get(key)
		require.Truef(t, ok, "key %s must survive the restart", key)
		assert.Equalf(t, want, got, "key %s must read identically before and after a restart", key)
	}

	// Deleting a flow forgets its state.
	reloaded.purge(flowID)
	_, ok = reloaded.get(flowID, "n1").Get("seen")
	assert.False(t, ok, "a purged flow's durable state must be gone")
}

// gatedNodeSink keeps the rows a durable store asked for and holds its first
// Delete until released, so a test can stop a removal between memory and the
// queue.
type gatedNodeSink struct {
	mu      sync.Mutex
	rows    map[string]string
	deletes int
	entered chan struct{}
	release chan struct{}
	saved   chan struct{}
}

func (s *gatedNodeSink) Save(flowID uuid.UUID, nodeID, key, value string) {
	s.mu.Lock()
	s.rows[key] = value
	s.mu.Unlock()
	select {
	case s.saved <- struct{}{}:
	default:
	}
}

func (s *gatedNodeSink) Delete(flowID uuid.UUID, nodeID, key string) {
	s.mu.Lock()
	s.deletes++
	first := s.deletes == 1
	s.mu.Unlock()
	if first {
		close(s.entered)
		<-s.release
	}
	s.mu.Lock()
	delete(s.rows, key)
	s.mu.Unlock()
}

func (s *gatedNodeSink) Close() {}

// TestDurableStoreDeleteFollowsMemoryOrder covers a Delete racing a Set of the
// same key. A removal queued after the lock is released can land after the
// newer save, leaving the database without a row memory holds. The store's
// encoded copy still matches, so later identical writes skip the sink and the
// row never comes back.
func TestDurableStoreDeleteFollowsMemoryOrder(t *testing.T) {
	sink := &gatedNodeSink{rows: make(map[string]string), entered: make(chan struct{}),
		release: make(chan struct{}), saved: make(chan struct{}, 1)}
	stores := newDurableStores(sink, zap.NewNop())
	st := stores.get(uuid.New(), "n1")
	st.Set("seen", "a")
	<-sink.saved

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); st.Delete("seen") }()
	<-sink.entered
	go func() { defer wg.Done(); st.Set("seen", "b") }()
	// The Set cannot reach the sink while the Delete holds the store; give it
	// the chance to try before letting the Delete finish.
	select {
	case <-sink.saved:
	case <-time.After(100 * time.Millisecond):
	}
	close(sink.release)
	wg.Wait()

	value, ok := st.Get("seen")
	require.True(t, ok, "the Set came after the Delete in memory")
	assert.Equal(t, "b", value)
	assert.Equal(t, `"b"`, sink.rows["seen"], "the database must hold the row memory holds, or a restart loses it")
}

// TestDurableStoreUnencodableValueDropsStoredRow locks the rule that a restart
// never brings back a value the node has since replaced. A value JSON cannot
// carry, such as a NaN reading, stays in memory only, and its predecessor's
// row was left in place, so the next start restored the older value as though
// it were current.
func TestDurableStoreUnencodableValueDropsStoredRow(t *testing.T) {
	sink := newNodeStateRecorder()
	stores := newDurableStores(sink, zap.NewNop())
	flowID := uuid.New()
	st := stores.get(flowID, "n1")

	st.Set("last", 21.5)
	require.Len(t, sink.rows, 1, "the encodable value must be stored")
	st.Set("last", math.NaN())

	got, ok := st.Get("last")
	require.True(t, ok, "the replacement must stay readable in memory")
	assert.True(t, math.IsNaN(got.(float64)), "memory must hold the value the node wrote last")
	assert.Empty(t, sink.rows,
		"the replaced value's row must go, or a restart restores it over the value that replaced it")
}
