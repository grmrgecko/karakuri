package engine

import (
	"sync"

	"github.com/google/uuid"
)

// nodeStores holds the flow-scoped node stores of one engine, keyed by flow
// and node ID. Stores are created on first use and dropped when a flow is
// rearmed or disarmed, so a redeploy starts nodes from a clean slate.
type nodeStores struct {
	mu     sync.Mutex
	stores map[uuid.UUID]map[string]*memNodeStore
}

// newNodeStores returns an empty node store registry.
func newNodeStores() *nodeStores {
	return &nodeStores{stores: make(map[uuid.UUID]map[string]*memNodeStore)}
}

// get returns the store for one node of one flow, creating it on first use.
func (s *nodeStores) get(flowID uuid.UUID, nodeID string) *memNodeStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	byNode := s.stores[flowID]
	if byNode == nil {
		byNode = make(map[string]*memNodeStore)
		s.stores[flowID] = byNode
	}
	st := byNode[nodeID]
	if st == nil {
		st = &memNodeStore{values: make(map[string]any)}
		byNode[nodeID] = st
	}
	return st
}

// purge drops every store belonging to a flow.
func (s *nodeStores) purge(flowID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.stores, flowID)
}

// NewMemNodeStore returns a standalone in-memory NodeStore. Node tests use it
// to exercise nodes that reason across messages without standing up a run.
func NewMemNodeStore() NodeStore {
	return &memNodeStore{values: make(map[string]any)}
}

// memNodeStore is the in-memory NodeStore implementation. Concurrent runs of
// the same flow share one instance per node, so every operation takes the lock.
type memNodeStore struct {
	mu     sync.Mutex
	values map[string]any
}

// Get returns the value stored under key.
func (s *memNodeStore) Get(key string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[key]
	return v, ok
}

// Set stores a value under key.
func (s *memNodeStore) Set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
}

// Delete removes a key.
func (s *memNodeStore) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
}

// Update applies fn to the current value under the lock and stores the result.
// A nil result deletes the key. Nodes that read, decide, and write in one step
// (rate limits, change detection, group accumulation) must use this so
// concurrent runs of the same flow cannot interleave.
func (s *memNodeStore) Update(key string, fn func(cur any, ok bool) any) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.values[key]
	next := fn(cur, ok)
	if next == nil {
		delete(s.values, key)
		return nil
	}
	s.values[key] = next
	return next
}
