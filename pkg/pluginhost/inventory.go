package pluginhost

import (
	"sort"

	"github.com/grmrgecko/karakuri/pkg/models"
)

// Item is one plugin as management clients see it: what it is, whether it is
// running, and what it contributes.
type Item struct {
	Name        string `json:"name" description:"Plugin name from its manifest."`
	Version     string `json:"version,omitempty" description:"Plugin version."`
	Description string `json:"description,omitempty" description:"What the plugin contributes."`
	Author      string `json:"author,omitempty" description:"Plugin author."`
	// Builtin plugins are compiled into the binary and cannot be unloaded.
	Builtin bool   `json:"builtin" description:"Whether the plugin is compiled in."`
	Enabled bool   `json:"enabled" description:"Whether the plugin loads at startup."`
	Status  string `json:"status" description:"Load state: loaded, unloaded, or error."`
	Error   string `json:"error_message,omitempty" description:"Why the last load failed."`
	// HasProcess distinguishes a plugin that runs an executable from one that
	// only ships widget packages.
	HasProcess  bool     `json:"has_process" description:"Whether the plugin runs a subprocess."`
	HasSettings bool     `json:"has_settings" description:"Whether the plugin declares settings."`
	Widgets     []string `json:"widgets,omitempty" description:"Widget packages contributed."`
	Nodes       []string `json:"nodes,omitempty" description:"Node types contributed."`
	Kinds       []string `json:"kinds,omitempty" description:"Connection kinds contributed."`
}

// fillContributions adds what a loaded plugin registered with the host.
func fillContributions(host *Host, name string, item Item) Item {
	if host == nil {
		return item
	}
	if contrib, ok := host.ContributionsOf(name); ok {
		item.Nodes = contrib.Nodes
		item.Kinds = contrib.Kinds
		if len(contrib.Widgets) > 0 {
			item.Widgets = contrib.Widgets
		}
	}
	return item
}

// Inventory lists every plugin: the built-ins compiled into the binary, and
// each one discovered on disk with its stored enable choice and last load
// result. Either argument may be nil, reporting that half as empty.
func Inventory(host *Host, mgr *Manager) []Item {
	items := []Item{}

	// Built-ins first: they are always loaded and never manageable. A
	// built-in with a resource directory (its widgets) is still one plugin,
	// so the directory is folded into this row rather than listed again
	// below.
	builtins := map[string]bool{}
	if host != nil {
		for _, m := range host.Plugins() {
			if !m.Builtin {
				continue
			}
			builtins[m.Name] = true
			items = append(items, fillContributions(host, m.Name, Item{
				Name:        m.Name,
				Version:     m.Version,
				Description: m.Description,
				Author:      m.Author,
				Builtin:     true,
				Enabled:     true,
				Status:      models.PluginStatusLoaded,
			}))
		}
	}

	// Then everything on disk, in name order.
	if mgr != nil {
		discovered := mgr.Discovered()
		names := make([]string, 0, len(discovered))
		for name := range discovered {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			if builtins[name] {
				continue
			}
			mf := discovered[name]
			item := Item{
				Name:        mf.Name,
				Version:     mf.Version,
				Description: mf.Description,
				Author:      mf.Author,
				Enabled:     true,
				Status:      models.PluginStatusUnloaded,
				HasProcess:  mf.HasProcess(),
				HasSettings: mgr.SettingsSchema(name) != "",
			}
			for _, w := range mf.Widgets {
				item.Widgets = append(item.Widgets, w.PackageName)
			}
			// The stored row is the source of truth for enablement and the
			// last load result; live status overrides it when they differ.
			if state, err := models.GetPluginState(name); err == nil {
				item.Enabled = state.Enabled
				item.Status = state.Status
				item.Error = state.ErrorMsg
			}
			if mgr.IsLoaded(name) {
				item.Status = models.PluginStatusLoaded
				item.Error = ""
			}
			items = append(items, fillContributions(host, name, item))
		}
	}

	return items
}
