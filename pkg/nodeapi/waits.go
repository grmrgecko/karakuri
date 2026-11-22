package nodeapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// ErrSuspended is the cancellation cause for shutdown and redeploy. A wait
// whose run is cancelled with it stays stored and is resumed.
var ErrSuspended = errors.New("suspended; pending waits are carried over")

// Suspended reports whether ctx was cancelled because its run is suspended
// rather than stopped. A node whose park fails with Suspended true returns the
// context error and leaves its wait stored.
func Suspended(ctx context.Context) bool {
	return ctx.Err() != nil && errors.Is(context.Cause(ctx), ErrSuspended)
}

// Wait is one pending wait held by a node.
type Wait struct {
	// Token identifies the execution that owns the wait. Update assigns a
	// fresh one when it is empty.
	Token string `json:"token"`
	// Deadline is when the wait comes due. Zero marks a state-only entry that
	// has no waiter to resume.
	Deadline time.Time `json:"deadline"`
	// RunID is the run that armed or last restarted the wait. The store sets
	// it.
	RunID string `json:"run_id,omitempty"`
	// State is the node's own JSON state: the held message, collected parts,
	// or whatever the node needs to finish the wait.
	State json.RawMessage `json:"state,omitempty"`
}

// WaitStore is a node's pending waits, keyed by a node-chosen key. Stores
// from deployed runs persist their waits across restarts and redeploys;
// stores from tests and previews keep them in memory.
type WaitStore interface {
	// Get returns the wait stored under key.
	Get(key string) (Wait, bool)
	// Update applies fn under the store lock and stores its result. A nil
	// result deletes the entry. durable is false when the wait could not be
	// stored and will not survive a restart. fn must not call back into the
	// store.
	Update(key string, fn func(cur Wait, ok bool) *Wait) (next Wait, durable bool)
	// Claim deletes the entry when its token matches and returns once the
	// delete is stored, along with the wait as it stood when claimed. It
	// returns false when the wait was reset, restarted by another owner,
	// cancelled, or dropped. A node emits a wait's result only after a true
	// Claim, so a crash can lose a delivery but never repeat one.
	Claim(key, token string) (Wait, bool, error)
	// Take applies fn under the store lock like Update. When fn reports take,
	// the entry it returned is claimed instead of stored: deleted, with the
	// delete stored before Take returns it. It lets a node finish a wait in
	// the same step that decides it is finished.
	Take(key string, fn func(cur Wait, ok bool) (next *Wait, take bool)) (taken Wait, took bool, err error)
}

// WaitParker is implemented by execution contexts that provide a wait store.
type WaitParker interface {
	// WaitStore returns this node's pending-wait store.
	WaitStore() WaitStore
	// ExecutionID identifies this node execution within its run, so a wait
	// owned by one execution has a key no other execution shares.
	ExecutionID() string
}

// Resumer is implemented by node types whose stored waits can be resumed in a
// new run after a restart or a redeploy.
type Resumer interface {
	// Resume continues one stored wait. It parks until the wait's deadline
	// through the same code Execute uses, claims the wait, and returns the
	// outputs to emit, or nil outputs when the claim fails.
	Resume(ctx context.Context, ec ExecContext, key string, w Wait) (map[string][]*Msg, error)
}

// TriggerRecorder is implemented by trigger contexts that can record an event
// the trigger decided not to start a run for, such as an occurrence missed
// while the server was down and dropped by its missed-deadline setting.
type TriggerRecorder interface {
	// Skipped records a run that settles at once as canceled, with reason as
	// its error and msg's payload as its trigger payload.
	Skipped(msg *Msg, reason string)
}

// memWaitsKey is the node store key holding the fallback wait store.
const memWaitsKey = "__waits__"

// WaitsFor returns the execution context's wait store and execution ID. A
// context without one gets a memory-only store kept in its node store, so
// executions of one node still share their waits, and a fresh execution ID.
func WaitsFor(ec ExecContext) (WaitStore, string) {
	if wp, ok := ec.(WaitParker); ok {
		return wp.WaitStore(), wp.ExecutionID()
	}
	return MemWaitsIn(ec.NodeStore()), NewWaitToken()
}

// MemWaitsIn returns the memory-only wait store kept in a node store,
// creating it on first use.
func MemWaitsIn(store NodeStore) WaitStore {
	ws, _ := store.Update(memWaitsKey, func(cur any, _ bool) any {
		if s, ok := cur.(*MemWaitStore); ok {
			return s
		}
		return NewMemWaitStore()
	}).(*MemWaitStore)
	if ws == nil {
		// A store that normalizes values cannot hold the wait store itself.
		return NewMemWaitStore()
	}
	return ws
}

// NewWaitToken returns a random token for a wait owner.
func NewWaitToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// MemWaitStore is a WaitStore that keeps its waits in memory only.
type MemWaitStore struct {
	mu    sync.Mutex
	waits map[string]Wait
}

// NewMemWaitStore returns an empty memory-only wait store.
func NewMemWaitStore() *MemWaitStore {
	return &MemWaitStore{waits: make(map[string]Wait)}
}

// Get returns the wait stored under key.
func (s *MemWaitStore) Get(key string) (Wait, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.waits[key]
	return w, ok
}

// Update applies fn under the lock. Memory waits are never durable.
func (s *MemWaitStore) Update(key string, fn func(cur Wait, ok bool) *Wait) (Wait, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.waits[key]
	next := fn(cur, ok)
	if next == nil {
		delete(s.waits, key)
		return Wait{}, false
	}
	if next.Token == "" {
		next.Token = NewWaitToken()
	}
	s.waits[key] = *next
	return *next, false
}

// Take applies fn and deletes the entry when fn takes it.
func (s *MemWaitStore) Take(key string, fn func(cur Wait, ok bool) (*Wait, bool)) (Wait, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.waits[key]
	next, take := fn(cur, ok)
	if take {
		delete(s.waits, key)
		if next == nil {
			return Wait{}, false, nil
		}
		return *next, true, nil
	}
	if next == nil {
		delete(s.waits, key)
		return Wait{}, false, nil
	}
	if next.Token == "" {
		next.Token = NewWaitToken()
	}
	s.waits[key] = *next
	return Wait{}, false, nil
}

// Claim deletes the wait when its token matches.
func (s *MemWaitStore) Claim(key, token string) (Wait, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.waits[key]
	if !ok || cur.Token != token {
		return Wait{}, false, nil
	}
	delete(s.waits, key)
	return cur, true, nil
}
