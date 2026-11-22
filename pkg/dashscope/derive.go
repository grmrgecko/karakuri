package dashscope

import (
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/dashvars"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/widgets"
)

// cacheEntry holds one derived scope against the layout it came from. A deploy
// raises the version, so the key changes and the old entry simply stops being
// asked for; nothing has to invalidate it.
type cacheEntry struct {
	version int
	// widgets is the widget registry's generation the scope was derived
	// under. A placement grants only while its plugin is loaded, so loading
	// or unloading one makes the entry stale without a deploy.
	widgets uint64
	scope   *Scope
	// expires is set when the layout has query variables, whose options and
	// so whose grants change with the house rather than with a deploy.
	expires time.Time
}

// queryScopeTTL is how long a scope derived from query variables is reused.
const queryScopeTTL = 30 * time.Second

var (
	cacheMu sync.RWMutex
	cache   = make(map[uuid.UUID]cacheEntry)
)

// Invalidate drops a dashboard's cached scope. Deploying changes the version
// and needs no help, but restoring or editing the draft an editor is served
// does, and a deleted dashboard should not sit in the map.
func Invalidate(dashboardID uuid.UUID) {
	cacheMu.Lock()
	delete(cache, dashboardID)
	cacheMu.Unlock()
}

// deployedScope returns the scope of a dashboard's deployed layout, deriving it
// once per version. A dashboard that has never been deployed falls back to its
// draft, matching what a viewer is actually served.
func deployedScope(d *models.Dashboard) (*Scope, error) {
	// The generation is read before deriving, so a plugin loaded meanwhile
	// leaves the entry stale rather than current.
	generation := widgets.Generation()
	// A dashboard that has never been deployed is served its draft, and a
	// draft moves with every gesture, so there is no version to cache against
	// and it is derived fresh.
	if d.DeployedVersion > 0 {
		cacheMu.RLock()
		entry, ok := cache[d.ID]
		cacheMu.RUnlock()
		if ok && entry.version == d.DeployedVersion && entry.widgets == generation &&
			(entry.expires.IsZero() || time.Now().Before(entry.expires)) {
			return entry.scope, nil
		}
	}

	placements, variables, err := servedPlacements(d)
	if err != nil {
		return nil, err
	}
	scope := deriveFrom(placements, variables)

	if d.DeployedVersion > 0 {
		cacheMu.Lock()
		entry := cacheEntry{version: d.DeployedVersion, widgets: generation, scope: scope}
		if hasQuery(variables) {
			entry.expires = time.Now().Add(queryScopeTTL)
		}
		cache[d.ID] = entry
		cacheMu.Unlock()
	}
	return scope, nil
}

// servedPlacements returns the placements a viewer of this dashboard is served
// and the variables they are templated on: the deployed version's, or the draft's
// when it has never been deployed.
//
// A deployed version that cannot be read or parsed is an error, so the scope
// is empty and requests are denied. Deriving from the draft instead would
// grant bindings the author has not published.
func servedPlacements(d *models.Dashboard) ([]models.DashboardWidget, []models.DashboardVariable, error) {
	if d.DeployedVersion > 0 {
		version, err := models.FindDashboardVersion(d.ID, d.DeployedVersion)
		if err != nil {
			return nil, nil, err
		}
		snap, err := models.ParseDashboardSnapshot(version.Snapshot)
		if err != nil {
			return nil, nil, err
		}
		rows, err := snap.WidgetRows(d.ID)
		return rows, displayVariables(snap.Display), err
	}
	rows, err := models.ListDashboardWidgets(d.ID)
	return rows, displayVariables(d.Display), err
}

// displayVariables returns a display's variables, none for no display.
func displayVariables(d *models.DashboardDisplay) []models.DashboardVariable {
	if d == nil {
		return nil
	}
	return d.Variables
}

// deriveFrom walks a layout's placements and collects what each one exposes.
// A placement whose widget package is not registered contributes nothing: an
// unloaded plugin cannot serve the widget, so it should not authorize its data
// either. A placement templated on variables grants what each combination of
// their options produces, and nothing a value outside them could name.
func deriveFrom(placements []models.DashboardWidget, variables []models.DashboardVariable) *Scope {
	scope := newScope()
	variables, _ = dashvars.Resolve(variables)
	// A variable that follows a select entity reads that entity's state for
	// every viewer, so the layout grants it as a display-only read.
	for _, v := range variables {
		if v.Follows() {
			scope.add(Capability{Kind: CapHA, Owner: v.Query.ConnectionID, Ref: v.Query.EntityID})
		}
	}
	for _, p := range placements {
		pkg := p.Widget.PackageName
		if pkg == "" {
			continue
		}
		spec, plugin, ok := widgets.ScopeOf(pkg)
		if !ok {
			continue
		}

		for _, b := range placementBindings(p, spec.Bindings, variables) {
			for _, c := range b.capabilities() {
				scope.add(c)
			}
		}

		// A plugin's topic or route pattern may name the placement it belongs
		// to, which turns a namespace into an exact grant.
		for _, pattern := range spec.Topics {
			scope.topics = append(scope.topics, substitutePlacement(pattern, p))
		}
		for _, decl := range spec.API {
			if route, ok := parseRoute(substitutePlacement(decl, p)); ok {
				scope.api[plugin] = append(scope.api[plugin], route)
			}
		}
	}
	return scope
}

// substitutePlacement fills a topic or route pattern's placement placeholders.
func substitutePlacement(pattern string, p models.DashboardWidget) string {
	pattern = strings.ReplaceAll(pattern, "{dashboard}", p.DashboardID.String())
	return strings.ReplaceAll(pattern, "{widget}", p.ID.String())
}

// parseRoute reads a "METHOD /path/pattern" declaration. A declaration missing
// either half is dropped rather than widened into a method-wide grant.
func parseRoute(decl string) (apiRoute, bool) {
	method, pattern, ok := strings.Cut(strings.TrimSpace(decl), " ")
	if !ok {
		return apiRoute{}, false
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	pattern = strings.TrimSpace(pattern)
	if method == "" || pattern == "" {
		return apiRoute{}, false
	}
	return apiRoute{method: method, pattern: pattern}, true
}

// draftScope derives straight from the draft rows, for an author whose editor
// is rendering the layout they are still changing. It is never cached: the
// draft moves with every gesture, and only someone who may edit sees it.
func draftScope(d *models.Dashboard) (*Scope, error) {
	placements, err := models.ListDashboardWidgets(d.ID)
	if err != nil {
		return nil, err
	}
	return deriveFrom(placements, displayVariables(d.Display)), nil
}

// merge folds one scope into another, used to give an editor both what the
// dashboard serves and what their draft is about to.
func (s *Scope) merge(other *Scope) {
	if other == nil {
		return
	}
	for k, write := range other.caps {
		s.caps[k] = s.caps[k] || write
	}
	s.topics = append(s.topics, other.topics...)
	for plugin, routes := range other.api {
		s.api[plugin] = append(s.api[plugin], routes...)
	}
}

// hasQuery reports whether any variable is a query.
func hasQuery(variables []models.DashboardVariable) bool {
	for _, v := range variables {
		if v.Kind() == models.VariableTypeQuery {
			return true
		}
	}
	return false
}

// DraftTriggerFlows returns the IDs of the flows a dashboard's draft binds
// widget triggers on, so a deploy can check its author may run each one.
func DraftTriggerFlows(d *models.Dashboard) ([]string, error) {
	scope, err := draftScope(d)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for k := range scope.caps {
		if k.kind == CapTrigger && !seen[k.owner] {
			seen[k.owner] = true
			out = append(out, k.owner)
		}
	}
	return out, nil
}

// DraftConnectionRefs returns what a dashboard's draft reads through
// connections: the connections its entity, camera, condition, and variable
// bindings name, and the devices it binds, which each speak on a connection.
// A deploy checks its author may use every one.
func DraftConnectionRefs(d *models.Dashboard) (connections, devices []string, err error) {
	scope, err := draftScope(d)
	if err != nil {
		return nil, nil, err
	}
	seenConn := map[string]bool{}
	seenDev := map[string]bool{}
	for k := range scope.caps {
		switch k.kind {
		case CapHA, CapNVR:
			if k.owner != "" && !seenConn[k.owner] {
				seenConn[k.owner] = true
				connections = append(connections, k.owner)
			}
		case CapDevice:
			if k.ref != "" && !seenDev[k.ref] {
				seenDev[k.ref] = true
				devices = append(devices, k.ref)
			}
		}
	}
	return connections, devices, nil
}
