package widgets

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// reset clears the package registry between tests, since it is process-wide
// the way the plugin host's registries are.
func reset() {
	mu.Lock()
	packages = make(map[string]pkgEntry)
	mu.Unlock()
}

// TestRegisterMakesPackageServable covers what registering a widget package
// actually buys: the browser fetches /widgets/{package}/index.js, and Available
// is what turns that request into a directory on disk. The path is derived from
// the plugin's own resource directory, which is why a manifest rarely names one.
func TestRegisterMakesPackageServable(t *testing.T) {
	reset()
	t.Cleanup(reset)

	spec := Spec{Name: "Clock", PackageName: "clock", TagName: "clock-widget"}
	require.NoError(t, spec.Validate("clock-plugin", "1.0.0", "Someone", "A clock"),
		"a well-formed widget spec must validate")
	require.NoError(t, Register(zap.NewNop(), "clock-plugin", "/plugins/clock", []Spec{spec}),
		"a validated spec must register")

	dir, ok := Available("clock")
	require.True(t, ok, "a registered package must be servable, or the browser gets a 404 for its script")
	assert.Equal(t, filepath.Join("/plugins/clock", "widgets", "clock"), dir,
		"the directory is derived from the plugin's own resource path, which is why a manifest rarely names one")
}

// TestUnregisterTakesPackageOffTheWire covers unloading. Disabling a plugin is
// how a widget is disabled, so its package has to stop being servable — a
// widget still loadable after its plugin was unloaded would keep running with
// no plugin behind it to answer its calls.
func TestUnregisterTakesPackageOffTheWire(t *testing.T) {
	reset()
	t.Cleanup(reset)

	spec := Spec{Name: "Clock", PackageName: "clock", TagName: "clock-widget"}
	_ = spec.Validate("clock-plugin", "1.0.0", "", "")
	require.NoError(t, Register(zap.NewNop(), "clock-plugin", "/plugins/clock", []Spec{spec}),
		"the package must be registered before unloading means anything")

	Unregister("clock-plugin")

	_, ok := Available("clock")
	assert.False(t, ok,
		"an unloaded plugin's package must stop being servable, or a widget outlives the plugin that answers its calls")
}

// TestRegisterRejectsPackageClaimedByAnotherPlugin covers two plugins claiming
// one package name. The name is the URL widgets are fetched from, so the second
// registration is refused rather than allowed to shadow the first — silently
// replacing it would change what every existing placement loads.
func TestRegisterRejectsPackageClaimedByAnotherPlugin(t *testing.T) {
	reset()
	t.Cleanup(reset)

	first := Spec{Name: "Clock", PackageName: "clock", TagName: "clock-widget"}
	_ = first.Validate("plugin-a", "1.0.0", "", "")
	require.NoError(t, Register(zap.NewNop(), "plugin-a", "/plugins/a", []Spec{first}),
		"the first claim on the name must succeed")

	second := Spec{Name: "Other Clock", PackageName: "clock", TagName: "clock-widget"}
	_ = second.Validate("plugin-b", "1.0.0", "", "")
	err := Register(zap.NewNop(), "plugin-b", "/plugins/b", []Spec{second})
	assert.Error(t, err, "the package name is the URL widgets load from, so a second claim must be refused")

	dir, _ := Available("clock")
	assert.Equal(t, filepath.Join("/plugins/a", "widgets", "clock"), dir,
		"the first plugin keeps the name: shadowing it would change what every existing placement loads")
}

// TestReregisterSamePluginIsIdempotent covers the same plugin registering
// twice, which is what a reload is. It must not collide with itself or leave a
// duplicate entry behind, or reloading a plugin would either fail or double
// every package it ships.
func TestReregisterSamePluginIsIdempotent(t *testing.T) {
	reset()
	t.Cleanup(reset)

	spec := Spec{Name: "Clock", PackageName: "clock", TagName: "clock-widget"}
	_ = spec.Validate("clock-plugin", "1.0.0", "", "")
	for i := 0; i < 2; i++ {
		require.NoErrorf(t, Register(zap.NewNop(), "clock-plugin", "/plugins/clock", []Spec{spec}),
			"pass %d: a plugin re-registering its own package is a reload, not a collision", i)
	}
	assert.Len(t, AvailablePackages(), 1, "a reload must not leave a duplicate entry behind")
}

// TestSpecValidateFillsDefaults covers what a widget inherits from its plugin.
// Version, author, and description come from the manifest so a widget-only
// plugin need not repeat them per widget, and the entry point defaults to the
// file every build produces.
func TestSpecValidateFillsDefaults(t *testing.T) {
	spec := Spec{Name: "Clock", PackageName: "clock", TagName: "clock-widget"}
	require.NoError(t, spec.Validate("clock-plugin", "2.1.0", "Author", "Desc"), "a well-formed spec must validate")

	assert.Equal(t, "index.js", spec.EntryPoint, "the entry point defaults to the file every widget build produces")
	assert.Equal(t, "2.1.0", spec.Version, "a widget inherits its plugin's version, since they ship together")
	assert.Equal(t, "Author", spec.Author, "a widget inherits its plugin's author unless it names its own")
	assert.Equal(t, "Desc", spec.Description, "a widget inherits its plugin's description unless it names its own")

	// The tag is registered as a Custom Element, and the HTML specification
	// requires a hyphen in the name: without one the browser refuses to define
	// it, so the widget would simply never render.
	unhyphenated := Spec{Name: "Clock", PackageName: "clock", TagName: "clock"}
	assert.Error(t, unhyphenated.Validate("clock-plugin", "1.0.0", "", ""),
		"the HTML specification requires a hyphen in a Custom Element name, so a tag without one can never be defined")
}
