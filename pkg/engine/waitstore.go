package engine

import (
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
)

// maxWaitState bounds a stored wait's encoded state. A wait over it stays in
// memory only: an image held in a delay should not rewrite megabytes of
// database row on every extend.
const maxWaitState = 1 << 20

// claimTimeout bounds how long a claim waits for its delete to commit.
const claimTimeout = 10 * time.Second

// WaitSink persists pending waits. Save and Delete are queued and report
// whether the write was accepted; DeleteSync returns once the delete, and
// every write queued before it, has committed.
type WaitSink interface {
	Save(w *models.PendingWait) bool
	Delete(flowID uuid.UUID, nodeID, key string) bool
	DeleteSync(flowID uuid.UUID, nodeID, key string) error
	Close()
}

// PendingWaitInfo describes one pending wait for inspection.
type PendingWaitInfo struct {
	FlowID   uuid.UUID `json:"flow_id"`
	NodeID   string    `json:"node_id"`
	NodeType string    `json:"node_type"`
	Key      string    `json:"key"`
	Deadline time.Time `json:"deadline"`
	RunID    string    `json:"run_id"`
	Durable  bool      `json:"durable"`
	// Wait is the stored wait itself, for the engine's own restore path.
	Wait Wait `json:"-"`
}

// storedWait is a wait in the registry and whether its row is stored.
type storedWait struct {
	wait    Wait
	durable bool
}

// waitRegistry holds the pending waits of deployed runs, keyed by flow and
// node. The registry is authoritative while the engine runs; the sink keeps
// the durable copy.
type waitRegistry struct {
	mu    sync.Mutex
	flows map[uuid.UUID]map[string]*nodeWaits
	sink  WaitSink
	log   *zap.Logger

	// waiters maps each wait token to the run waiting on it, and runTokens
	// indexes the same pairs by run. A token without a live waiter belongs to
	// a wait nobody will finish: one restored from storage, or one whose run
	// was suspended. Only those are resumed, so a rearm while runs are parked
	// does not start a second waiter.
	waiterMu  sync.Mutex
	waiters   map[string]uuid.UUID
	runTokens map[uuid.UUID]map[string]bool
}

// newWaitRegistry returns an empty registry writing through to sink, which
// may be nil when waits should live only as long as the process.
func newWaitRegistry(sink WaitSink, log *zap.Logger) *waitRegistry {
	return &waitRegistry{
		flows:     make(map[uuid.UUID]map[string]*nodeWaits),
		sink:      sink,
		log:       log,
		waiters:   make(map[string]uuid.UUID),
		runTokens: make(map[uuid.UUID]map[string]bool),
	}
}

// attach records runID as the waiter of token.
func (r *waitRegistry) attach(token string, runID uuid.UUID) {
	r.waiterMu.Lock()
	defer r.waiterMu.Unlock()
	if prev, ok := r.waiters[token]; ok {
		delete(r.runTokens[prev], token)
	}
	r.waiters[token] = runID
	if r.runTokens[runID] == nil {
		r.runTokens[runID] = make(map[string]bool)
	}
	r.runTokens[runID][token] = true
}

// reserve marks token as about to be resumed, reporting false when it already
// has a waiter or a pending resume.
func (r *waitRegistry) reserve(token string) bool {
	r.waiterMu.Lock()
	defer r.waiterMu.Unlock()
	if _, ok := r.waiters[token]; ok {
		return false
	}
	r.waiters[token] = uuid.Nil
	return true
}

// unreserve drops a reservation that never became a run.
func (r *waitRegistry) unreserve(token string) {
	r.waiterMu.Lock()
	defer r.waiterMu.Unlock()
	if id, ok := r.waiters[token]; ok && id == uuid.Nil {
		delete(r.waiters, token)
	}
}

// releaseRun forgets every token runID was waiting on. Called when the run
// settles.
func (r *waitRegistry) releaseRun(runID uuid.UUID) {
	r.waiterMu.Lock()
	defer r.waiterMu.Unlock()
	for token := range r.runTokens[runID] {
		if r.waiters[token] == runID {
			delete(r.waiters, token)
		}
	}
	delete(r.runTokens, runID)
}

// nodeWaits is one node's pending waits.
type nodeWaits struct {
	reg      *waitRegistry
	flowID   uuid.UUID
	nodeID   string
	nodeType string

	mu    sync.Mutex
	waits map[string]storedWait
}

// get returns one node's waits, creating them on first use. A non-empty
// nodeType records the type of the node now holding them.
func (r *waitRegistry) get(flowID uuid.UUID, nodeID, nodeType string) *nodeWaits {
	r.mu.Lock()
	defer r.mu.Unlock()
	byNode := r.flows[flowID]
	if byNode == nil {
		byNode = make(map[string]*nodeWaits)
		r.flows[flowID] = byNode
	}
	nw := byNode[nodeID]
	if nw == nil {
		nw = &nodeWaits{reg: r, flowID: flowID, nodeID: nodeID, waits: make(map[string]storedWait)}
		byNode[nodeID] = nw
	}
	if nodeType != "" {
		nw.mu.Lock()
		nw.nodeType = nodeType
		nw.mu.Unlock()
	}
	return nw
}

// load seeds the registry from stored rows. A row whose state no longer
// decodes is skipped rather than failing startup.
func (r *waitRegistry) load(rows []*models.PendingWait) {
	for _, row := range rows {
		if !json.Valid([]byte(row.State)) {
			r.log.Warn("skipping unreadable pending wait",
				zap.String("flow_id", row.FlowID.String()), zap.String("node_id", row.NodeID),
				zap.String("key", row.Key))
			continue
		}
		w := Wait{Token: row.Token, RunID: row.RunID.String(), State: json.RawMessage(row.State)}
		if row.Deadline != nil {
			w.Deadline = *row.Deadline
		}
		nw := r.get(row.FlowID, row.NodeID, row.NodeType)
		nw.mu.Lock()
		nw.waits[row.Key] = storedWait{wait: w, durable: true}
		nw.mu.Unlock()
	}
}

// list returns a flow's pending waits ordered by deadline. State-only entries
// are included only when withState is set.
func (r *waitRegistry) list(flowID uuid.UUID, withState bool) []PendingWaitInfo {
	r.mu.Lock()
	nodes := make([]*nodeWaits, 0, len(r.flows[flowID]))
	for _, nw := range r.flows[flowID] {
		nodes = append(nodes, nw)
	}
	r.mu.Unlock()

	var out []PendingWaitInfo
	for _, nw := range nodes {
		nw.mu.Lock()
		for key, sw := range nw.waits {
			if sw.wait.Deadline.IsZero() && !withState {
				continue
			}
			out = append(out, PendingWaitInfo{
				FlowID: flowID, NodeID: nw.nodeID, NodeType: nw.nodeType, Key: key,
				Deadline: sw.wait.Deadline, RunID: sw.wait.RunID, Durable: sw.durable, Wait: sw.wait,
			})
		}
		nw.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Deadline.Equal(out[j].Deadline) {
			return out[i].Deadline.Before(out[j].Deadline)
		}
		if out[i].NodeID != out[j].NodeID {
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// remove deletes one wait, reporting whether it existed.
func (r *waitRegistry) remove(flowID uuid.UUID, nodeID, key string) bool {
	r.mu.Lock()
	nw := r.flows[flowID][nodeID]
	r.mu.Unlock()
	if nw == nil {
		return false
	}
	nw.mu.Lock()
	sw, ok := nw.waits[key]
	delete(nw.waits, key)
	nw.mu.Unlock()
	if ok && sw.durable && r.sink != nil {
		r.sink.Delete(flowID, nodeID, key)
	}
	return ok
}

// removeNode drops every wait a node holds, including state-only entries.
func (r *waitRegistry) removeNode(flowID uuid.UUID, nodeID string) {
	r.mu.Lock()
	nw := r.flows[flowID][nodeID]
	delete(r.flows[flowID], nodeID)
	r.mu.Unlock()
	if nw == nil {
		return
	}
	nw.mu.Lock()
	keys := make([]string, 0, len(nw.waits))
	for key, sw := range nw.waits {
		if sw.durable {
			keys = append(keys, key)
		}
	}
	nw.waits = make(map[string]storedWait)
	nw.mu.Unlock()
	if r.sink != nil {
		for _, key := range keys {
			r.sink.Delete(flowID, nodeID, key)
		}
	}
}

// purge forgets a flow's waits in memory. The flow's delete transaction owns
// the rows.
func (r *waitRegistry) purge(flowID uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.flows, flowID)
}

// nodeTypes returns the node type recorded for each node holding waits in a
// flow.
func (r *waitRegistry) nodeTypes(flowID uuid.UUID) map[string]string {
	r.mu.Lock()
	nodes := make([]*nodeWaits, 0, len(r.flows[flowID]))
	for _, nw := range r.flows[flowID] {
		nodes = append(nodes, nw)
	}
	r.mu.Unlock()
	out := make(map[string]string, len(nodes))
	for _, nw := range nodes {
		nw.mu.Lock()
		if len(nw.waits) > 0 {
			out[nw.nodeID] = nw.nodeType
		}
		nw.mu.Unlock()
	}
	return out
}

// runWaitStore is the WaitStore one run's execution sees: the node's waits,
// stamped with the run that arms them.
type runWaitStore struct {
	nw    *nodeWaits
	runID uuid.UUID
}

// Get returns the wait stored under key.
func (s *runWaitStore) Get(key string) (Wait, bool) {
	s.nw.mu.Lock()
	defer s.nw.mu.Unlock()
	sw, ok := s.nw.waits[key]
	return sw.wait, ok
}

// Update applies fn under the node's lock, stores the result, and mirrors it
// to the sink. A new token, or a new entry, records this run as the arming
// run.
func (s *runWaitStore) Update(key string, fn func(cur Wait, ok bool) *Wait) (Wait, bool) {
	nw := s.nw
	nw.mu.Lock()
	defer nw.mu.Unlock()
	cur, ok := nw.waits[key]
	next := fn(cur.wait, ok)
	if next == nil {
		delete(nw.waits, key)
		if ok && cur.durable && nw.reg.sink != nil {
			nw.reg.sink.Delete(nw.flowID, nw.nodeID, key)
		}
		return Wait{}, false
	}
	w := *next
	if w.Token == "" {
		w.Token = NewWaitToken()
	}
	if !ok || w.Token != cur.wait.Token || w.RunID == "" {
		w.RunID = s.runID.String()
		nw.reg.attach(w.Token, s.runID)
	}
	durable := nw.store(key, w)
	if !durable && ok && cur.durable && nw.reg.sink != nil {
		// The new value could not be stored, so the old row must not outlive
		// it and be restored in its place.
		nw.reg.sink.Delete(nw.flowID, nw.nodeID, key)
	}
	nw.waits[key] = storedWait{wait: w, durable: durable}
	return w, durable
}

// Take applies fn under the node's lock and, when fn takes the result,
// deletes it and waits for the stored row to go before returning it.
func (s *runWaitStore) Take(key string, fn func(cur Wait, ok bool) (*Wait, bool)) (Wait, bool, error) {
	nw := s.nw
	var taken *Wait
	var wasDurable bool
	s.Update(key, func(cur Wait, ok bool) *Wait {
		next, take := fn(cur, ok)
		if !take {
			return next
		}
		taken = next
		if ok {
			wasDurable = nw.waits[key].durable
		}
		return nil
	})
	if taken == nil {
		return Wait{}, false, nil
	}
	if wasDurable && nw.reg.sink != nil {
		// Update queued the delete; this one waits for it, and for every
		// earlier write, to commit.
		if err := nw.reg.sink.DeleteSync(nw.flowID, nw.nodeID, key); err != nil {
			return Wait{}, false, err
		}
	}
	return *taken, true, nil
}

// store queues a wait's row, reporting whether it will be stored. Callers
// hold nw.mu.
func (nw *nodeWaits) store(key string, w Wait) bool {
	sink := nw.reg.sink
	if sink == nil {
		return false
	}
	if len(w.State) > maxWaitState {
		nw.reg.log.Warn("pending wait state is too large to store; kept in memory only",
			zap.String("flow_id", nw.flowID.String()), zap.String("node_id", nw.nodeID),
			zap.String("key", key), zap.Int("bytes", len(w.State)))
		return false
	}
	state := string(w.State)
	if state == "" {
		state = "null"
	}
	row := &models.PendingWait{
		FlowID: nw.flowID, NodeID: nw.nodeID, NodeType: nw.nodeType, Key: key,
		Token: w.Token, State: state,
	}
	if runID, err := uuid.Parse(w.RunID); err == nil {
		row.RunID = runID
	}
	if !w.Deadline.IsZero() {
		deadline := w.Deadline
		row.Deadline = &deadline
	}
	return sink.Save(row)
}

// Claim deletes the wait when its token matches and waits for the stored row
// to go.
func (s *runWaitStore) Claim(key, token string) (Wait, bool, error) {
	nw := s.nw
	nw.mu.Lock()
	cur, ok := nw.waits[key]
	if !ok || cur.wait.Token != token {
		nw.mu.Unlock()
		return Wait{}, false, nil
	}
	delete(nw.waits, key)
	sink := nw.reg.sink
	nw.mu.Unlock()
	if !cur.durable || sink == nil {
		return cur.wait, true, nil
	}
	// The delete is queued behind every earlier write for this wait, so once
	// it commits no older row can come back.
	if err := sink.DeleteSync(nw.flowID, nw.nodeID, key); err != nil {
		return Wait{}, false, err
	}
	return cur.wait, true, nil
}

// DBWaitSink persists pending waits from a single writer goroutine, so writes
// for one wait commit in the order they were made.
type DBWaitSink struct {
	log    *zap.Logger
	ch     chan func()
	wg     sync.WaitGroup
	mu     sync.RWMutex
	closed bool
}

// NewDBWaitSink creates the sink and starts its writer goroutine.
func NewDBWaitSink(log *zap.Logger) *DBWaitSink {
	s := &DBWaitSink{log: log, ch: make(chan func(), varBuffer)}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for op := range s.ch {
			op()
		}
	}()
	return s
}

// enqueue submits a write without waiting, reporting whether it was accepted.
func (s *DBWaitSink) enqueue(op func()) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return false
	}
	select {
	case s.ch <- op:
		return true
	default:
		s.log.Warn("pending wait write queue full, dropping write")
		return false
	}
}

// Save queues one wait's row.
func (s *DBWaitSink) Save(w *models.PendingWait) bool {
	return s.enqueue(func() {
		if err := models.UpsertPendingWait(w); err != nil {
			s.log.Error("failed to persist pending wait",
				zap.String("node_id", w.NodeID), zap.String("key", w.Key), zap.Error(err))
		}
	})
}

// Delete queues the removal of one wait's row.
func (s *DBWaitSink) Delete(flowID uuid.UUID, nodeID, key string) bool {
	return s.enqueue(func() {
		if err := models.DeletePendingWait(flowID, nodeID, key); err != nil {
			s.log.Error("failed to delete pending wait",
				zap.String("node_id", nodeID), zap.String("key", key), zap.Error(err))
		}
	})
}

// DeleteSync removes one wait's row and returns once it has committed. It
// waits for room in a full queue rather than dropping the delete, because a
// claim that cannot store its delete must not emit.
func (s *DBWaitSink) DeleteSync(flowID uuid.UUID, nodeID, key string) error {
	done := make(chan error, 1)
	op := func() { done <- models.DeletePendingWait(flowID, nodeID, key) }
	timer := time.NewTimer(claimTimeout)
	defer timer.Stop()
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return errors.New("pending wait store is closed")
	}
	select {
	case s.ch <- op:
	case <-timer.C:
		s.mu.RUnlock()
		return errors.New("pending wait store did not accept the claim in time")
	}
	s.mu.RUnlock()
	select {
	case err := <-done:
		if err != nil {
			s.log.Error("failed to claim pending wait",
				zap.String("node_id", nodeID), zap.String("key", key), zap.Error(err))
		}
		return err
	case <-timer.C:
		return errors.New("pending wait claim did not commit in time")
	}
}

// Close flushes queued writes and stops the writer.
func (s *DBWaitSink) Close() {
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
