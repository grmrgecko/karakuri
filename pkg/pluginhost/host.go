// Package pluginhost loads plugins and applies what they contribute. It owns
// the catalog, the external-plugin subprocess manager, the agent transport,
// and the host-side gRPC service a plugin calls back into. pkg/plugin holds
// only the surface a plugin is written against, which is why an agent or an
// out-of-process plugin links that and none of this.
package pluginhost

import (
	"fmt"
	"sync"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/grmrgecko/karakuri/pkg/plugin"
)

// Contributions records what one plugin registered, so unloading can reverse
// exactly that and the catalog can say what a plugin brings.
type Contributions struct {
	Nodes []string `json:"nodes,omitempty" description:"Node types registered."`
	Kinds []string `json:"kinds,omitempty" description:"Connection kinds registered."`
	// PortTypes and Controls are recorded but not reversed: both are
	// order-indexed data the palette colors and falls back on, and removing
	// one would reshuffle every later entry.
	PortTypes []string `json:"port_types,omitempty" description:"Port types registered."`
	Controls  []string `json:"controls,omitempty" description:"Field controls registered."`
	// AIProviders are providers added to the shared AI connection kind.
	AIProviders []string `json:"ai_providers,omitempty" description:"AI providers registered."`
	// Widgets come from the plugin's resource directory rather than its Go
	// registration, so they are attached after the fact — including for a
	// compiled-in plugin, whose widgets ship as files beside its manifest.
	Widgets []string `json:"widgets,omitempty" description:"Widget packages contributed."`
}

// loadedEntry is one plugin in the catalog.
type loadedEntry struct {
	manifest plugin.Manifest
	contrib  Contributions
}

// Host loads plugins and is the Registrar they register against. Node types
// land in the node registry it was built with; connection kinds, port types,
// and field controls land in their package registries.
type Host struct {
	nodes *nodeapi.Registry

	mu      sync.Mutex
	order   []string
	plugins map[string]*loadedEntry
}

// NewHost creates a plugin host registering node types into the given engine
// registry.
func NewHost(nodes *nodeapi.Registry) *Host {
	return &Host{nodes: nodes, plugins: make(map[string]*loadedEntry)}
}

// Load registers a plugin's contributions and records what it brought. A
// plugin registers against a recorder rather than the Host directly, so
// unloading knows which node types and kinds to withdraw.
func (h *Host) Load(p plugin.Plugin) error {
	m := p.Manifest()
	if m.Name == "" {
		return fmt.Errorf("plugin has no name")
	}
	rec := &recorder{host: h, plugin: m.Name, contributed: !m.Builtin}
	// A registration that fails partway has already put its earlier
	// contributions in the registries. The plugin never enters the catalog,
	// so Unload could not find them; they are withdrawn here.
	if err := p.Register(rec); err != nil {
		h.withdraw(m.Name, rec.contrib)
		return fmt.Errorf("plugin %q failed to register: %w", m.Name, err)
	}
	if rec.err != nil {
		h.withdraw(m.Name, rec.contrib)
		return fmt.Errorf("plugin %q failed to register: %w", m.Name, rec.err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.plugins[m.Name]; !ok {
		h.order = append(h.order, m.Name)
	}
	h.plugins[m.Name] = &loadedEntry{manifest: m, contrib: rec.contrib}
	return nil
}

// Unload withdraws a plugin's node types and connection kinds and drops it
// from the catalog. Built-ins are compiled in and refuse to unload.
func (h *Host) Unload(name string) error {
	h.mu.Lock()
	entry, ok := h.plugins[name]
	if !ok {
		h.mu.Unlock()
		return fmt.Errorf("plugin %q is not loaded", name)
	}
	if entry.manifest.Builtin {
		h.mu.Unlock()
		return fmt.Errorf("plugin %q is built in and cannot be unloaded", name)
	}
	delete(h.plugins, name)
	for i, n := range h.order {
		if n == name {
			h.order = append(h.order[:i], h.order[i+1:]...)
			break
		}
	}
	h.mu.Unlock()

	h.withdraw(name, entry.contrib)
	return nil
}

// withdraw removes the node types, connection kinds, and AI providers a
// plugin registered. Port types and field controls stay, as Contributions
// explains.
func (h *Host) withdraw(name string, contrib Contributions) {
	for _, nodeType := range contrib.Nodes {
		h.nodes.Unregister(nodeType)
	}
	for _, kind := range contrib.Kinds {
		integrations.UnregisterKind(kind)
	}
	if len(contrib.AIProviders) > 0 {
		ai.UnregisterProviders(name)
	}
}

// IsLoaded reports whether a plugin's contributions are currently applied.
func (h *Host) IsLoaded(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.plugins[name]
	return ok
}

// IsBuiltin reports whether a loaded plugin is compiled into the binary. A
// resource directory naming one is that plugin's own files — its widgets —
// rather than a second plugin that happens to share the name.
func (h *Host) IsBuiltin(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry, ok := h.plugins[name]
	return ok && entry.manifest.Builtin
}

// AttachWidgets records the widget packages a plugin's resource directory
// contributed, so the catalog reports a plugin's whole surface whether it
// registered in Go or was discovered on disk.
func (h *Host) AttachWidgets(name string, packages []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if entry, ok := h.plugins[name]; ok {
		entry.contrib.Widgets = packages
	}
}

// Plugins returns the manifests of every loaded plugin in load order.
func (h *Host) Plugins() []plugin.Manifest {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]plugin.Manifest, 0, len(h.order))
	for _, name := range h.order {
		out = append(out, h.plugins[name].manifest)
	}
	return out
}

// ContributionsOf returns what a loaded plugin registered.
func (h *Host) ContributionsOf(name string) (Contributions, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry, ok := h.plugins[name]
	if !ok {
		return Contributions{}, false
	}
	return entry.contrib, true
}

// recorder is the Registrar a plugin actually registers against: it notes
// what came through and forwards each contribution to the Host, tagged by
// the plugin so unloading withdraws exactly what it brought. Registrar
// methods return nothing, so a contribution the registry refuses lands in
// err and fails the load.
type recorder struct {
	host        *Host
	plugin      string
	contributed bool
	contrib     Contributions
	err         error
}

func (r *recorder) RegisterNode(nt nodeapi.NodeType) {
	if r.contributed {
		nt = normalizeContributedNode(nt)
	}
	r.contrib.Nodes = append(r.contrib.Nodes, nt.Meta().Type)
	r.host.RegisterNode(nt)
}

// normalizedNode preserves execution while replacing contributed metadata with
// its trust-normalized form.
type normalizedNode struct {
	nodeapi.NodeType
	meta nodeapi.NodeMeta
}

// Meta returns metadata whose omitted invocation state is unknown.
func (n normalizedNode) Meta() nodeapi.NodeMeta { return n.meta }

// normalizedTrigger preserves the trigger interface while normalizing metadata.
type normalizedTrigger struct {
	nodeapi.TriggerType
	meta nodeapi.NodeMeta
}

// Meta returns metadata whose omitted invocation state is unknown.
func (n normalizedTrigger) Meta() nodeapi.NodeMeta { return n.meta }

func normalizeContributedNode(nt nodeapi.NodeType) nodeapi.NodeType {
	meta := nt.Meta()
	flow.NormalizeContributedNodeMeta(&meta)
	if trigger, ok := nt.(nodeapi.TriggerType); ok {
		return normalizedTrigger{TriggerType: trigger, meta: meta}
	}
	return normalizedNode{NodeType: nt, meta: meta}
}

func (r *recorder) RegisterConnectionKind(kind integrations.ConnectionKind) {
	r.contrib.Kinds = append(r.contrib.Kinds, kind.Kind)
	r.host.RegisterConnectionKind(kind)
}

func (r *recorder) RegisterPortType(def flow.PortTypeDef) {
	r.contrib.PortTypes = append(r.contrib.PortTypes, def.Name)
	r.host.RegisterPortType(def)
}

func (r *recorder) RegisterFieldControl(def flow.FieldControlDef) {
	r.contrib.Controls = append(r.contrib.Controls, def.Name)
	r.host.RegisterFieldControl(def)
}

func (r *recorder) RegisterAIProvider(def plugin.AIProviderDef) {
	if err := ai.RegisterProvider(aiProviderDef(def), r.plugin); err != nil {
		if r.err == nil {
			r.err = err
		}
		return
	}
	r.contrib.AIProviders = append(r.contrib.AIProviders, def.Name)
}

// RegisterNode implements plugin.Registrar.
func (h *Host) RegisterNode(nt nodeapi.NodeType) { h.nodes.Register(nt) }

// RegisterConnectionKind implements plugin.Registrar.
func (h *Host) RegisterConnectionKind(kind integrations.ConnectionKind) {
	integrations.RegisterKind(kind)
}

// RegisterPortType implements plugin.Registrar.
func (h *Host) RegisterPortType(def flow.PortTypeDef) { flow.RegisterPortType(def) }

// RegisterFieldControl implements plugin.Registrar.
func (h *Host) RegisterFieldControl(def flow.FieldControlDef) { flow.RegisterFieldControl(def) }

// RegisterAIProvider implements plugin.Registrar. Registrations through the
// Host directly carry no owner and are never withdrawn; plugins load through
// the recorder, which tags them.
func (h *Host) RegisterAIProvider(def plugin.AIProviderDef) {
	_ = ai.RegisterProvider(aiProviderDef(def), "")
}

// aiProviderDef maps the plugin surface's data-only definition onto the AI
// registry's.
func aiProviderDef(def plugin.AIProviderDef) ai.ProviderDef {
	return ai.ProviderDef{
		Name: def.Name, Label: def.Label, Wire: def.Wire, BaseURL: def.BaseURL,
		NeedsKey: def.NeedsKey, SupportsTools: def.SupportsTools,
		LegacyMaxTokens: def.LegacyMaxTokens, AppendV1: def.AppendV1,
	}
}
