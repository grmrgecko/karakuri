// Package providers is the catalog of typed service endpoints plugins expose.
// A weather plugin registering "openmeteo" and "openweathermap" under type
// "weather" is telling a dashboard widget which backends it can point at and
// where each one answers, so the widget's config panel needs no knowledge of
// what plugins are installed.
package providers

import (
	"sort"
	"sync"
)

// Provider is one typed service endpoint.
type Provider struct {
	Type        string `json:"type" description:"Provider category, e.g. weather."`
	Name        string `json:"name" description:"Provider identifier within its type."`
	Endpoint    string `json:"endpoint" description:"API path the provider answers on."`
	DisplayName string `json:"display_name,omitempty" description:"Human-readable name."`
	// Source names the plugin that registered the entry, so unloading it
	// withdraws the entry too.
	Source string `json:"source,omitempty" description:"Plugin that registered the provider."`
}

// R is the package-level registry plugins populate as they load.
var R = &Registry{}

// Registry is a thread-safe catalog of provider entries.
type Registry struct {
	mu        sync.RWMutex
	providers []Provider
}

// Register adds a provider entry, replacing an existing entry with the same
// type and name so reloading a plugin does not duplicate it.
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, existing := range r.providers {
		if existing.Type == p.Type && existing.Name == p.Name {
			r.providers[i] = p
			return
		}
	}
	r.providers = append(r.providers, p)
}

// Unregister removes one entry by type and name.
func (r *Registry) Unregister(providerType, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.providers[:0]
	for _, p := range r.providers {
		if p.Type != providerType || p.Name != name {
			kept = append(kept, p)
		}
	}
	r.providers = kept
}

// UnregisterBySource removes every entry a source registered.
func (r *Registry) UnregisterBySource(source string) {
	if source == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.providers[:0]
	for _, p := range r.providers {
		if p.Source != source {
			kept = append(kept, p)
		}
	}
	r.providers = kept
}

// List returns every provider of a type, sorted by name so a config panel's
// dropdown has a stable order.
func (r *Registry) List(providerType string) []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		if p.Type == providerType {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Types returns the distinct registered provider types in sorted order.
func (r *Registry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]bool, len(r.providers))
	var out []string
	for _, p := range r.providers {
		if !seen[p.Type] {
			seen[p.Type] = true
			out = append(out, p.Type)
		}
	}
	sort.Strings(out)
	return out
}
