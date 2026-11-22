package pluginhost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/engine"
)

// writePlugin lays out one plugin directory under root and returns root.
func writePlugin(t *testing.T, root, name, manifest string, exeName string) {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755), "the fixture needs a plugin directory")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(manifest), 0o644),
		"the fixture needs a manifest, which is what makes a directory a plugin")
	if exeName != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, exeName), []byte("#!/bin/sh\n"), 0o755),
			"a manifest naming an entryPoint needs the file to exist on disk")
	}
}

// TestDiscoverWidgetOnlyPlugin covers a plugin that ships widgets and no
// executable, which is a whole shape of plugin rather than a degenerate one:
// loading it starts no subprocess, and enable and disable behave the same as
// for a plugin with a binary. The widget inherits the plugin's version and
// author so a manifest need not repeat them per widget, and its directory
// follows the widgets/<package> convention so a manifest rarely names a path.
func TestDiscoverWidgetOnlyPlugin(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "digital-clock", `
name: "digital-clock"
version: "0.1.0"
description: "A clock"
author: "Someone"
widgets:
  - name: "Digital Clock"
    packageName: "digital-clock"
    tagName: "digital-clock-widget"
    configTagName: "digital-clock-config"
`, "")

	got, err := DiscoverExternal([]string{root})
	require.NoError(t, err, "a widget-only manifest must discover")
	require.Len(t, got, 1, "the directory holds one plugin")

	m := got[0]
	assert.False(t, m.HasProcess(),
		"no entryPoint means no subprocess: a widget-only plugin loads by applying its contributions")
	require.Len(t, m.Widgets, 1, "the manifest declares one widget")

	w := m.Widgets[0]
	assert.Equal(t, "index.js", w.EntryPoint, "the entry point defaults to the file every widget build produces")
	assert.Equal(t, "0.1.0", w.Version, "a widget inherits the plugin's version, since they ship and version together")
	assert.Equal(t, "Someone", w.Author, "a widget inherits the plugin's author unless it names its own")
	assert.Equal(t, filepath.Join(root, "digital-clock", "widgets", "digital-clock"), w.Dir(m.Dir),
		"the directory follows the widgets/<package> convention, which is why a manifest rarely names a path")
}

// TestDiscoverExecutableAndWidget covers the other shape: one plugin bringing
// both a Go binary and a widget, so they version, load, and disable together —
// the arrangement the weather plugin uses, where the widget is useless without
// the process feeding it. The explicit path is here because a manifest may
// override the convention when a build lands elsewhere.
func TestDiscoverExecutableAndWidget(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "weather", `
name: "weather"
version: "0.2.0"
entryPoint: "weather"
widgets:
  - name: "Weather"
    packageName: "weather"
    tagName: "weather-widget"
    path: "bundle"
`, "weather")

	got, err := DiscoverExternal([]string{root})
	require.NoError(t, err, "a manifest declaring both an executable and a widget must discover")
	require.Len(t, got, 1, "both contributions belong to one plugin, not two")
	assert.True(t, got[0].HasProcess(), "a declared entryPoint means the host starts a subprocess for it")

	assert.Equal(t, filepath.Join(root, "weather", "bundle"), got[0].Widgets[0].Dir(got[0].Dir),
		"an explicit path must win over the widgets/<package> convention")
}

// TestDiscoverRejectsBrokenManifests covers the manifests refused at discovery,
// against the two working shapes above. Every contribution key is optional, but
// a manifest naming none contributes nothing and would appear in the plugin
// panel as an entry that does nothing when enabled; the other two describe
// something that cannot work, and catching them at discovery is what turns them
// into a startup log line rather than a widget that never renders.
func TestDiscoverRejectsBrokenManifests(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		exe      string
		why      string
	}{
		{
			name:     "contributes nothing",
			manifest: "name: \"nothing\"\nversion: \"1\"\n",
			why:      "a plugin with no contributions would sit in the panel doing nothing when enabled",
		},
		{
			name:     "entryPoint not on disk",
			manifest: "name: \"broken\"\nentryPoint: \"broken\"\n",
			why:      "the host would try to start a process that is not there",
		},
		{
			name: "Custom Element tag without a hyphen",
			manifest: "name: \"bad\"\nwidgets:\n  - name: \"Bad\"\n" +
				"    packageName: \"bad\"\n    tagName: \"bad\"\n",
			why: "the HTML specification requires a hyphen, so the browser would refuse to define the element",
		},
	}
	for _, c := range cases {
		root := t.TempDir()
		writePlugin(t, root, "p", c.manifest, c.exe)

		_, err := DiscoverExternal([]string{root})
		assert.Errorf(t, err, "%s must be refused at discovery: %s", c.name, c.why)
	}
}

// TestDiscoverSkipsDirectoriesWithoutManifests covers what makes a directory a
// plugin: the manifest, and nothing else. The plugins path is a shared
// directory that may hold build output, caches, or anything else an operator
// put there, so a directory with no manifest is skipped silently rather than
// failing discovery for every other plugin.
func TestDiscoverSkipsDirectoriesWithoutManifests(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "notaplugin"), 0o755),
		"the fixture needs a directory with no manifest in it")

	got, err := DiscoverExternal([]string{root})
	require.NoError(t, err, "a stray directory must not fail discovery for every other plugin")
	assert.Empty(t, got, "only a directory with a manifest is a plugin: %+v", got)
}

// TestDiscoverEarlierPathWins covers the search-path precedence: plugins beside
// the binary shadow the ones installed system-wide, which is what lets a
// developer run a local build of a plugin without uninstalling the packaged
// one. The shadowed copy is dropped entirely rather than loaded twice, since
// both claim the same name and the same widget package.
func TestDiscoverEarlierPathWins(t *testing.T) {
	local, system := t.TempDir(), t.TempDir()
	writePlugin(t, local, "clock", "name: \"clock\"\nversion: \"local\"\nwidgets:\n  - name: C\n    packageName: clock\n    tagName: c-widget\n", "")
	writePlugin(t, system, "clock", "name: \"clock\"\nversion: \"system\"\nwidgets:\n  - name: C\n    packageName: clock\n    tagName: c-widget\n", "")

	got, err := DiscoverExternal([]string{local, system})
	require.NoError(t, err, "two copies of one plugin must not fail discovery")
	require.Len(t, got, 1, "the shadowed copy is dropped, not loaded alongside: both claim the same name")
	assert.Equal(t, "local", got[0].Version,
		"the earlier path wins, so a local build shadows the packaged one rather than the reverse")
}

// TestBrokenManifestDoesNotHideOtherPlugins pins that one plugin directory the
// host cannot use costs only that plugin. Discovery stopped at the first bad
// manifest and LoadAll returned its error, which the server treats as fatal:
// a manifest naming an entry point that was never built kept every other
// plugin from loading and the server from starting. The error is still
// reported, so the bad directory becomes a startup log line.
func TestBrokenManifestDoesNotHideOtherPlugins(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "broken", "name: \"broken\"\nentryPoint: \"broken\"\n", "")
	writePlugin(t, root, "tides", "name: \"tides\"\nwidgets:\n  - name: \"Tides\"\n"+
		"    packageName: \"tides\"\n    tagName: \"tides-widget\"\n", "")

	got, err := DiscoverExternal([]string{root})
	assert.ErrorContains(t, err, "broken", "the refused manifest must still be reported")
	require.Len(t, got, 1, "the plugin beside the broken one must still be discovered")
	assert.Equal(t, "tides", got[0].Name, "the usable plugin is the one returned")

	mgr := NewManager(NewHost(engine.NewRegistry()), zap.NewNop())
	t.Cleanup(mgr.Stop)
	require.NoError(t, mgr.LoadAll([]string{root}), "a broken plugin directory must not fail the scan")
	assert.True(t, mgr.IsLoaded("tides"), "the usable plugin must load")
}
