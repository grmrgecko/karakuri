package permissions

import (
	"sort"
	"strings"
	"sync"
)

// PermissionDef describes a registerable permission and its allowed actions.
type PermissionDef struct {
	Permission  string   `json:"permission"`
	Actions     []string `json:"actions"`
	Description string   `json:"description"`
	// Source names the plugin that registered the permission, empty for the
	// built-in set. Unloading a plugin removes what it registered by source.
	Source string `json:"source,omitempty"`
}

// R is the package-level registry used after RegisterDefaults is called.
var R = &Registry{}

// Registry holds all known permission definitions for validation and autocomplete.
type Registry struct {
	mu   sync.RWMutex
	defs []PermissionDef
}

// Register adds a permission definition to the registry.
func (r *Registry) Register(permission string, actions []string, description string) {
	r.RegisterWithSource(permission, actions, description, "")
}

// RegisterWithSource adds a permission tagged with what registered it, so the
// whole set can be withdrawn again when that source goes away. Re-registering
// the same permission replaces the definition rather than duplicating it,
// which is what makes a plugin reload idempotent.
func (r *Registry) RegisterWithSource(permission string, actions []string, description, source string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	def := PermissionDef{
		Permission:  permission,
		Actions:     actions,
		Description: description,
		Source:      source,
	}
	for i, existing := range r.defs {
		if existing.Permission == permission {
			r.defs[i] = def
			return
		}
	}
	r.defs = append(r.defs, def)
}

// UnregisterFromSource removes one permission, but only when source
// registered it, so a plugin cannot withdraw a built-in permission or
// another plugin's.
func (r *Registry) UnregisterFromSource(permission, source string) {
	if source == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, def := range r.defs {
		if def.Permission == permission && def.Source == source {
			r.defs = append(r.defs[:i], r.defs[i+1:]...)
			return
		}
	}
}

// UnregisterBySource removes every permission a source registered.
func (r *Registry) UnregisterBySource(source string) {
	if source == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.defs[:0]
	for _, def := range r.defs {
		if def.Source != source {
			kept = append(kept, def)
		}
	}
	r.defs = kept
}

// All returns a copy of every registered permission definition.
func (r *Registry) All() []PermissionDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]PermissionDef, len(r.defs))
	copy(out, r.defs)
	return out
}

// Tree builds a nested map suitable for autocomplete in frontends.
// Each segment of the permission string becomes a key, with actions as leaf values.
// When a key is both a leaf and an intermediate node, its actions are stored under "_actions".
func (r *Registry) Tree() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tree := make(map[string]interface{})
	for _, def := range r.defs {
		parts := strings.Split(def.Permission, ":")
		current := tree
		for i, part := range parts {
			if i == len(parts)-1 {
				// Leaf node: store available actions.
				actions := make([]string, len(def.Actions))
				copy(actions, def.Actions)
				sort.Strings(actions)
				if existing, ok := current[part]; ok {
					if m, isMap := existing.(map[string]interface{}); isMap {
						// Already promoted to intermediate node; attach actions inside it.
						m["_actions"] = actions
					} else {
						current[part] = actions
					}
				} else {
					current[part] = actions
				}
			} else {
				child, ok := current[part]
				if !ok {
					child = make(map[string]interface{})
					current[part] = child
				} else if actions, isLeaf := child.([]string); isLeaf {
					// Promote an existing leaf to a map, preserving its actions.
					promoted := map[string]interface{}{"_actions": actions}
					current[part] = promoted
					child = promoted
				}
				current = child.(map[string]interface{})
			}
		}
	}
	return tree
}

// RegisterDefaults populates the global registry with the core API permission set.
func RegisterDefaults() {
	rwActions := []string{"read", "write"}

	R.Register("api:users", rwActions, "User management (beyond self)")
	R.Register("api:groups", rwActions, "Group management")
	R.Register("api:roles", rwActions, "Role management")
	R.Register("api:apikeys", rwActions, "API key management")
	R.Register("api:flows", []string{"read", "write", "deploy", "trigger", "manage"}, "Flow management")
	R.Register("api:runs", []string{"read", "cancel"}, "Run management")
	R.Register("api:connections", []string{"read", "write", "invoke", "manage"}, "Connection management; manage reaches connections the caller was not shared")
	R.Register("api:controls", []string{"read", "write", "set"}, "Control management; set covers operating one from a dashboard")
	R.Register("api:memories", rwActions, "AI memory store management")

	rwdActions := []string{"read", "write", "delete"}
	R.Register("api:dashboards", []string{"read", "write", "deploy", "delete", "manage"}, "Dashboard management; deploy publishes a layout to viewers, manage reaches dashboards the holder neither owns nor is shared")
	R.Register("api:dashboard-groups", []string{"read", "write", "delete", "manage"}, "Dashboard group management; manage reaches groups the holder neither owns nor is shared")
	// Widgets have no permissions of their own: they are contributed by
	// plugins, and managing one means managing its plugin.
	R.Register("api:renders", rwdActions, "Dashboard render jobs and schedules")
	R.Register("api:themes", []string{"write", "delete"}, "Dashboard theme management")
	R.Register("api:plugins", rwActions, "Plugin management")
	R.Register("api:agents", []string{"read", "write", "enroll", "revoke", "manage"}, "Remote agent management; enroll mints join tokens, revoke cuts off a host, manage reaches agents the caller was not shared")
	R.Register("api:backups", []string{"read", "write", "download"}, "Backups; download hands over an archive holding every credential and the agent CA")
	R.Register("api:events", []string{"read", "audit", "write"}, "Event log; audit shows sign-ins and administrative changes")
	R.Register("api:mcp", []string{"use"}, "MCP endpoint access; each tool still checks its own api permission")
}

// MatchPermission checks if a held permission string matches a required permission string.
// Supports wildcard (*) matching at each colon-separated segment.
func MatchPermission(held, required string) bool {
	heldParts := strings.Split(held, ":")
	reqParts := strings.Split(required, ":")

	// Shorter held patterns can match longer required strings when using wildcards.
	// For example, "api:*" matches "api:flows:read" because * matches rest.
	for i := 0; i < len(reqParts); i++ {
		if i >= len(heldParts) {
			return false
		}
		if heldParts[i] == "*" {
			// A wildcard at this position matches. If it's the last segment
			// of the held pattern, it matches all remaining required segments.
			if i == len(heldParts)-1 {
				return true
			}
			continue
		}
		if heldParts[i] != reqParts[i] {
			return false
		}
	}

	return len(heldParts) == len(reqParts)
}
