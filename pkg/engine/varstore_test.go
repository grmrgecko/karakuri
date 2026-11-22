package engine

import (
	"encoding/json"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingSink stands in for the database table variables are persisted to.
// It records what the store asked for so the write-through and clear paths can
// be asserted without one.
type recordingSink struct {
	mu      sync.Mutex
	saved   map[string]string
	deleted []string
}

func newRecordingSink() *recordingSink {
	return &recordingSink{saved: make(map[string]string)}
}

func (s *recordingSink) key(scope string, flowID uuid.UUID, key string) string {
	return scope + "|" + flowID.String() + "|" + key
}

func (s *recordingSink) Save(scope string, flowID uuid.UUID, key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saved[s.key(scope, flowID, key)] = value
}

func (s *recordingSink) Delete(scope string, flowID uuid.UUID, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.saved, s.key(scope, flowID, key))
	s.deleted = append(s.deleted, s.key(scope, flowID, key))
}

func (s *recordingSink) Close() {}

// TestVarStoreScopesAreSeparate covers the isolation the scopes promise: two
// flows do not see each other's flow variables, but do share global ones.
func TestVarStoreScopesAreSeparate(t *testing.T) {
	store := newVarStore(nil)
	flowA, flowB := uuid.New(), uuid.New()

	store.set(ScopeFlow, flowA, "count", float64(1))
	store.set(ScopeFlow, flowB, "count", float64(2))
	store.set(ScopeGlobal, flowA, "site", "hq")

	a, _ := store.get(ScopeFlow, flowA, "count")
	assert.Equal(t, float64(1), a, "flow scope is per flow: one flow's counter must not be another's")
	b, _ := store.get(ScopeFlow, flowB, "count")
	assert.Equal(t, float64(2), b, "flow scope is per flow: one flow's counter must not be another's")

	// Global is shared, so the flow asking does not matter.
	site, _ := store.get(ScopeGlobal, flowB, "site")
	assert.Equal(t, "hq", site, "a global written by one flow must be readable by any other, which is the point of the scope")
}

// TestVarStoreNilClears covers clearing a variable by writing nil, the same
// rule run variables follow.
func TestVarStoreNilClears(t *testing.T) {
	sink := newRecordingSink()
	store := newVarStore(sink)
	flowID := uuid.New()

	store.set(ScopeFlow, flowID, "count", float64(1))
	store.set(ScopeFlow, flowID, "count", nil)

	_, ok := store.get(ScopeFlow, flowID, "count")
	assert.False(t, ok, "writing nil is the only way to remove a variable, so it must actually remove it")
	assert.Len(t, sink.deleted, 1, "a clear must reach the database, or the value comes back on the next restart")
}

// TestVarStoreWritesThroughToSink covers persistence: every write is mirrored
// as the JSON the store would reload.
func TestVarStoreWritesThroughToSink(t *testing.T) {
	sink := newRecordingSink()
	store := newVarStore(sink)
	flowID := uuid.New()

	store.set(ScopeFlow, flowID, "reading", map[string]any{"c": float64(21)})
	store.set(ScopeGlobal, flowID, "site", "hq")

	assert.JSONEq(t, `{"c":21}`, sink.saved[sink.key(ScopeFlow, flowID, "reading")],
		"a value is persisted as the JSON it will be reloaded from")
	// Global rows are keyed by the nil UUID, not the writing flow, or a global
	// would come back as that flow's alone.
	assert.JSONEq(t, `"hq"`, sink.saved[sink.key(ScopeGlobal, uuid.Nil, "site")],
		"a global row must be keyed by the nil flow id, not by whichever flow happened to write it")
}

// TestVarStoreLoadRestoresValues covers the restart path: rows written by a
// previous process come back as the same values.
func TestVarStoreLoadRestoresValues(t *testing.T) {
	flowID := uuid.New()
	rows := []*models.Variable{
		{Scope: ScopeFlow, FlowID: flowID, Key: "count", Value: "4"},
		{Scope: ScopeGlobal, FlowID: uuid.Nil, Key: "site", Value: `"hq"`},
		// A row that no longer decodes is skipped rather than failing startup.
		{Scope: ScopeFlow, FlowID: flowID, Key: "broken", Value: "{not json"},
	}

	store := newVarStore(nil)
	store.load(rows, nil)

	count, _ := store.get(ScopeFlow, flowID, "count")
	assert.Equal(t, float64(4), count, "a flow variable written by a previous process must come back as the same value")
	site, _ := store.get(ScopeGlobal, uuid.Nil, "site")
	assert.Equal(t, "hq", site, "a global written by a previous process must come back as the same value")

	_, ok := store.get(ScopeFlow, flowID, "broken")
	assert.False(t, ok, "a row that no longer decodes must be skipped: one bad row cannot be allowed to stop the server booting")
}

// TestVarStoreSnapshotIsACopy covers the snapshot contract: expression
// evaluation and scripts must never hold a map another run is writing.
func TestVarStoreSnapshotIsACopy(t *testing.T) {
	store := newVarStore(nil)
	flowID := uuid.New()
	store.set(ScopeFlow, flowID, "count", float64(1))

	snap := store.snapshot(ScopeFlow, flowID)
	snap["count"] = float64(99)

	stored, _ := store.get(ScopeFlow, flowID, "count")
	assert.Equal(t, float64(1), stored,
		"a snapshot handed to an expression or a script must be a copy, or one run's evaluation rewrites another's variables")
}

// TestVarStoreConcurrentWrites covers concurrent runs of one flow sharing the
// store: every write lands and none corrupts the map.
func TestVarStoreConcurrentWrites(t *testing.T) {
	store := newVarStore(nil)
	flowID := uuid.New()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := "k" + string(rune('a'+n%26))
			store.set(ScopeFlow, flowID, key, float64(n))
			store.snapshot(ScopeFlow, flowID)
			store.get(ScopeGlobal, flowID, key)
		}(i)
	}
	wg.Wait()

	// 50 goroutines write keys ka..kz, so 26 distinct keys survive whichever
	// writer landed last on each.
	snap := store.snapshot(ScopeFlow, flowID)
	require.Len(t, snap, 26, "every distinct key must land: a lost write here is a lost write in production")

	for key, value := range snap {
		_, err := json.Marshal(value)
		assert.NoErrorf(t, err, "%s holds a value the store could not persist, which means the map was corrupted", key)
	}
}

// gatedVarSink records the last value saved per key and holds its first Save
// until released, so a test can stop one write between memory and the queue.
type gatedVarSink struct {
	mu      sync.Mutex
	last    string
	saves   int
	entered chan struct{}
	release chan struct{}
	second  chan struct{}
}

func (s *gatedVarSink) Save(scope string, flowID uuid.UUID, key, value string) {
	s.mu.Lock()
	s.saves++
	first := s.saves == 1
	s.mu.Unlock()
	if first {
		close(s.entered)
		<-s.release
	}
	s.mu.Lock()
	s.last = value
	s.mu.Unlock()
	if !first {
		close(s.second)
	}
}

func (s *gatedVarSink) Delete(scope string, flowID uuid.UUID, key string) {}

func (s *gatedVarSink) Close() {}

// TestVarStoreSinkFollowsMemoryOrder covers two runs setting the same flow
// variable at once. The sink is a FIFO queue, so a write queued after the
// store lock is released can land after a newer one: memory says 2 while the
// database keeps 1 and restores it on the next start.
func TestVarStoreSinkFollowsMemoryOrder(t *testing.T) {
	sink := &gatedVarSink{entered: make(chan struct{}), release: make(chan struct{}), second: make(chan struct{})}
	store := newVarStore(sink)
	flowID := uuid.New()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); store.set(ScopeFlow, flowID, "count", float64(1)) }()
	<-sink.entered
	go func() { defer wg.Done(); store.set(ScopeFlow, flowID, "count", float64(2)) }()
	// The second write cannot reach the sink while the first holds the
	// store; give it the chance to try before letting the first finish.
	select {
	case <-sink.second:
	case <-time.After(100 * time.Millisecond):
	}
	close(sink.release)
	wg.Wait()

	value, _ := store.get(ScopeFlow, flowID, "count")
	assert.Equal(t, float64(2), value, "the later write wins in memory")
	assert.Equal(t, "2", sink.last, "the database must end on the value memory holds, or a restart restores the older one")
}

// TestVarStoreUnencodableValueDropsStoredRow locks the rule that a restart
// never brings back a variable value that has since been replaced. A value
// JSON cannot carry, such as the NaN a division by zero leaves, stays in
// memory only, and the row of the value before it was left in place, so the
// next start loaded the older value as though it were current.
func TestVarStoreUnencodableValueDropsStoredRow(t *testing.T) {
	sink := newRecordingSink()
	store := newVarStore(sink)
	flowID := uuid.New()

	store.set(ScopeFlow, flowID, "ratio", 0.5)
	require.Len(t, sink.saved, 1, "the encodable value must be stored")
	store.set(ScopeFlow, flowID, "ratio", math.NaN())

	got, ok := store.get(ScopeFlow, flowID, "ratio")
	require.True(t, ok, "the replacement must stay readable in memory")
	assert.True(t, math.IsNaN(got.(float64)), "memory must hold the value written last")
	assert.Empty(t, sink.saved,
		"the replaced value's row must go, or a restart restores it over the value that replaced it")
}
