package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/backup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExternalPluginsListsBesideBrokenManifest pins that a backup still names
// the plugins the server loads when one plugin directory is refused.
func TestExternalPluginsListsBesideBrokenManifest(t *testing.T) {
	root := t.TempDir()
	manifests := map[string]string{
		"broken": "name: \"broken\"\nentryPoint: \"broken\"\n",
		"tides": "name: \"tides\"\nversion: \"1.2.0\"\nwidgets:\n  - name: \"Tides\"\n" +
			"    packageName: \"tides\"\n    tagName: \"tides-widget\"\n",
	}
	for dir, body := range manifests {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, dir, "manifest.yaml"), []byte(body), 0o644))
	}

	saved := config.C
	t.Cleanup(func() { config.C = saved })
	config.C = &config.Config{Plugins: config.PluginsConfig{Paths: []string{root}}}

	assert.Equal(t, []backup.Plugin{{Name: "tides", Version: "1.2.0"}}, externalPlugins(),
		"the plugin beside the refused directory must be named in the archive")
}
