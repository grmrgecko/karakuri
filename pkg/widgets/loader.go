// Package widgets registers the dashboard widget packages plugins contribute.
// A widget is one of a plugin's contributions rather than a package discovered
// on its own, so registration is driven by the plugin lifecycle: loading a
// plugin adds its packages, unloading removes them. Enabling and disabling a
// widget is therefore the same act as enabling and disabling its plugin. The
// database row survives an unload, carrying the manifest metadata a dashboard
// placement still refers to, but only a registered package is servable and
// placeable.
package widgets

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
)

// WidgetScope is what placing a widget exposes to the dashboard's viewers.
// A viewer holds no subsystem permission, so this is the whole of what one
// becomes able to read and press by a widget being on the page.
//
// Bindings names where in the widget's config its host bindings live: "." when
// the config is itself one binding, otherwise the field holding them (a card's
// "rows"). The binding shape is the host's, not the widget's, which is what
// lets one parser answer for every widget instead of each manifest restating
// which of its fields mean what.
//
// Topics and API cover a plugin's own namespace, which only the plugin can
// describe: the topic patterns its widgets subscribe to and the routes they
// call on its API, written as "METHOD /path/pattern" where * matches one path
// segment and ** matches the rest. Both are matched rather than derived from a
// widget's config, so they are a blast radius the plugin author chooses. Name
// the routes the widget actually calls: a plugin's other routes are gated on
// permissions a viewer does not hold, and a method-wide grant would open all
// of them.
//
// A topic pattern may carry {dashboard} and {widget}, substituted with the
// placement's dashboard and placement IDs. A plugin whose topic is keyed by
// them ends up with an exact grant per placement rather than a namespace.
type WidgetScope struct {
	Bindings string   `yaml:"bindings"`
	Topics   []string `yaml:"topics"`
	API      []string `yaml:"api"`
}

// Spec is one dashboard widget package as a plugin's manifest declares it.
// Path and EntryPoint default from PackageName, so a plugin whose layout
// follows the convention names only what makes the widget a widget.
type Spec struct {
	ConfigSchema  map[string]any `yaml:"configSchema"`
	DefaultConfig map[string]any `yaml:"defaultConfig"`
	Name          string         `yaml:"name"`
	PackageName   string         `yaml:"packageName"`
	TagName       string         `yaml:"tagName"`
	ConfigTagName string         `yaml:"configTagName"`
	Description   string         `yaml:"description"`
	Tags          []string       `yaml:"tags"`
	// Path locates the built bundle relative to the plugin directory; empty
	// means widgets/<packageName>.
	Path string `yaml:"path"`
	// EntryPoint names the bundle file inside Path; empty means index.js.
	EntryPoint string `yaml:"entryPoint"`
	// Version and Author fall back to the plugin's own when unset.
	Version         string `yaml:"version"`
	Author          string `yaml:"author"`
	DefaultProvider string `yaml:"defaultProvider"`
	// Scope declares what a viewer of a dashboard holding this widget may
	// reach. A widget that declares none reads nothing from the host, which is
	// the right answer for a clock or an image.
	Scope WidgetScope `yaml:"scope"`
}

// Dir returns the widget's directory inside the given plugin directory.
func (s Spec) Dir(pluginDir string) string {
	if s.Path != "" {
		return filepath.Join(pluginDir, s.Path)
	}
	return filepath.Join(pluginDir, "widgets", s.PackageName)
}

// Validate fills a spec's defaults from its plugin and rejects the ones a
// dashboard could not load.
func (s *Spec) Validate(pluginName, pluginVersion, pluginAuthor, pluginDesc string) error {
	if s.PackageName == "" {
		return fmt.Errorf("plugin %s has a widget with no packageName", pluginName)
	}
	if s.Name == "" {
		return fmt.Errorf("plugin %s widget %s has no name", pluginName, s.PackageName)
	}
	// Custom Elements must contain a hyphen per the HTML spec, so a bad tag
	// name fails here rather than as a silent no-op in the browser.
	if !strings.Contains(s.TagName, "-") {
		return fmt.Errorf("plugin %s widget %s: tagName %q must contain a hyphen",
			pluginName, s.PackageName, s.TagName)
	}
	if s.ConfigTagName != "" && !strings.Contains(s.ConfigTagName, "-") {
		return fmt.Errorf("plugin %s widget %s: configTagName %q must contain a hyphen",
			pluginName, s.PackageName, s.ConfigTagName)
	}
	if s.EntryPoint == "" {
		s.EntryPoint = "index.js"
	}
	if s.Version == "" {
		s.Version = pluginVersion
	}
	if s.Author == "" {
		s.Author = pluginAuthor
	}
	if s.Description == "" {
		s.Description = pluginDesc
	}
	return nil
}

// pkgEntry is one live widget package: which plugin brought it, where its
// built bundle lives, and what placing it exposes.
type pkgEntry struct {
	plugin string
	dir    string
	scope  WidgetScope
}

var (
	mu       sync.RWMutex
	packages = make(map[string]pkgEntry)
	// generation counts changes to packages.
	generation uint64
)

// Generation returns a number that changes whenever a package is registered
// or unregistered, so a caller holding something derived from ScopeOf can
// tell when to derive it again.
func Generation() uint64 {
	mu.RLock()
	defer mu.RUnlock()
	return generation
}

// Register adds a plugin's widget packages: it upserts each database row and
// marks the package servable. A package name already claimed by another plugin
// is rejected — the name is both the URL key and the row's unique key, so the
// collision has to surface rather than let one plugin shadow another.
func Register(log *zap.Logger, pluginName, pluginDir string, specs []Spec) error {
	mu.Lock()
	defer mu.Unlock()

	for _, spec := range specs {
		if existing, ok := packages[spec.PackageName]; ok && existing.plugin != pluginName {
			return fmt.Errorf("widget package %q is already provided by plugin %q",
				spec.PackageName, existing.plugin)
		}
	}

	for _, spec := range specs {
		// The row is what a dashboard picks from; the map is what serves the
		// bundle. Tests drive the plugin manager without a database, so the
		// row is skipped there and the package still becomes servable.
		if db.DB != nil {
			if err := upsert(pluginName, spec); err != nil {
				return fmt.Errorf("registering widget %s: %w", spec.PackageName, err)
			}
		}
		packages[spec.PackageName] = pkgEntry{
			plugin: pluginName,
			dir:    spec.Dir(pluginDir),
			scope:  spec.Scope,
		}
		generation++
		log.Info("registered widget",
			zap.String("plugin", pluginName),
			zap.String("package", spec.PackageName),
			zap.String("version", spec.Version))
	}
	return nil
}

// Unregister drops every package a plugin contributed. Rows are left alone, so
// a placement of an unloaded plugin's widget still resolves its metadata and
// comes back intact when the plugin returns.
func Unregister(pluginName string) {
	mu.Lock()
	defer mu.Unlock()
	for name, entry := range packages {
		if entry.plugin == pluginName {
			delete(packages, name)
			generation++
		}
	}
}

// Available resolves a package name to the directory holding its bundle. It
// reports false for a package whose plugin is not currently loaded, which is
// what keeps an unloaded plugin's assets off the wire.
func Available(packageName string) (string, bool) {
	mu.RLock()
	defer mu.RUnlock()
	entry, ok := packages[packageName]
	if !ok {
		return "", false
	}
	return entry.dir, true
}

// ScopeOf returns what a package exposes to a dashboard's viewers, along with
// the plugin that contributed it. A package whose plugin is not loaded reports
// false and therefore grants nothing, which is what keeps a placement of an
// unloaded widget from authorizing anything.
func ScopeOf(packageName string) (WidgetScope, string, bool) {
	mu.RLock()
	defer mu.RUnlock()
	entry, ok := packages[packageName]
	if !ok {
		return WidgetScope{}, "", false
	}
	return entry.scope, entry.plugin, true
}

// AvailablePackages returns the names of every currently registered package,
// sorted. It is what the catalog query filters on, so the widget picker offers
// only what a loaded plugin can actually serve.
func AvailablePackages() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(packages))
	for name := range packages {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// PruneOrphans deletes rows whose plugin no longer exists on disk. It runs
// once after discovery with the full set of known plugin names, so a plugin
// that is merely unloaded keeps its rows.
func PruneOrphans(log *zap.Logger, knownPlugins map[string]bool) {
	if db.DB == nil {
		return
	}
	var rows []models.Widget
	if err := db.DB.Find(&rows).Error; err != nil {
		log.Warn("failed to list widgets for cleanup", zap.Error(err))
		return
	}
	for _, w := range rows {
		if knownPlugins[w.PluginName] {
			continue
		}
		log.Info("removing widget of a plugin no longer on disk",
			zap.String("package", w.PackageName), zap.String("plugin", w.PluginName))
		if err := db.DB.Delete(&w).Error; err != nil {
			log.Warn("failed to delete stale widget",
				zap.String("package", w.PackageName), zap.Error(err))
		}
	}
}

// upsert writes the manifest's view of a widget onto its row.
func upsert(pluginName string, spec Spec) error {
	tagsJSON, _ := json.Marshal(spec.Tags)
	schemaJSON, err := json.Marshal(spec.ConfigSchema)
	if err != nil {
		return fmt.Errorf("encode widget config schema: %w", err)
	}
	defaultsJSON, err := json.Marshal(spec.DefaultConfig)
	if err != nil {
		return fmt.Errorf("encode widget defaults: %w", err)
	}

	var existing models.Widget
	if err := db.DB.Where("package_name = ?", spec.PackageName).First(&existing).Error; err == nil {
		existing.PluginName = pluginName
		existing.Name = spec.Name
		existing.TagName = spec.TagName
		existing.ConfigTagName = spec.ConfigTagName
		existing.Version = spec.Version
		existing.Description = spec.Description
		existing.Author = spec.Author
		existing.Tags = string(tagsJSON)
		existing.EntryPoint = spec.EntryPoint
		existing.DefaultProvider = spec.DefaultProvider
		existing.ConfigSchema = string(schemaJSON)
		existing.DefaultConfig = string(defaultsJSON)
		return db.DB.Save(&existing).Error
	}

	return db.DB.Create(&models.Widget{
		PluginName:      pluginName,
		Name:            spec.Name,
		PackageName:     spec.PackageName,
		TagName:         spec.TagName,
		ConfigTagName:   spec.ConfigTagName,
		Version:         spec.Version,
		Description:     spec.Description,
		Author:          spec.Author,
		Tags:            string(tagsJSON),
		EntryPoint:      spec.EntryPoint,
		DefaultProvider: spec.DefaultProvider,
		ConfigSchema:    string(schemaJSON),
		DefaultConfig:   string(defaultsJSON),
	}).Error
}
