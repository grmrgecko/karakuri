package nodeapi

import (
	"sort"
	"sync"

	"github.com/grmrgecko/karakuri/pkg/flow"
)

// NodeRegistrar is the registration surface a node package needs: satisfied
// by *Registry and by the plugin host's adapter, so RegisterAll helpers work
// for both direct registration in tests and plugin loading.
type NodeRegistrar interface {
	Register(nt NodeType)
}

// Registry holds registered node types keyed by type name.
type Registry struct {
	mu    sync.RWMutex
	types map[string]NodeType
}

// NewRegistry returns an empty node type registry.
func NewRegistry() *Registry {
	return &Registry{types: make(map[string]NodeType)}
}

// Register adds a node type to the registry, replacing any existing
// registration for the same type name.
func (r *Registry) Register(nt NodeType) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.types[nt.Meta().Type] = nt
}

// Unregister removes a node type. Unloading a plugin takes its node types out
// of the palette; flows already referencing one fail validation, which is the
// honest report that the type is gone.
func (r *Registry) Unregister(nodeType string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.types, nodeType)
}

// Get returns the node type registered under the given name.
func (r *Registry) Get(nodeType string) (NodeType, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	nt, ok := r.types[nodeType]
	return nt, ok
}

// List returns metadata for all registered node types sorted by type name.
func (r *Registry) List() []NodeMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	metas := make([]NodeMeta, 0, len(r.types))
	for _, nt := range r.types {
		metas = append(metas, nt.Meta())
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Type < metas[j].Type })
	return metas
}

// MetaFor returns the metadata for a node type. Implements flow.MetaRegistry
// so the registry can be passed directly to flow.Validate.
func (r *Registry) MetaFor(nodeType string) (flow.NodeMeta, bool) {
	nt, ok := r.Get(nodeType)
	if !ok {
		return flow.NodeMeta{}, false
	}
	return nt.Meta(), true
}

// defaultRegistry is the process-wide registry used by the server.
var defaultRegistry = NewRegistry()

// Default returns the process-wide default registry.
func Default() *Registry {
	return defaultRegistry
}

// Register adds a node type to the default registry.
func Register(nt NodeType) {
	defaultRegistry.Register(nt)
}
