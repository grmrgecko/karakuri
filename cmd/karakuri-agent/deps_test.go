package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAgentLinksNoDatabase pins the agent's dependency shape. The agent keeps
// its whole state in PEM files under --dir, so reaching pkg/models or a GORM
// driver means an import edge grew back somewhere in pkg/plugin, pkg/nodeapi,
// or pkg/integrations. Nothing fails at build time when it does: the SQLite
// driver is pure Go, so the agent would keep compiling and silently carry a
// megabytes-large embedded database it never opens. This is the only place
// that notices.
//
// internal/config is the edge most likely to grow one: both binaries link the
// whole package, so a server section that reaches for pkg/models arrives in the
// agent with it.
func TestAgentLinksNoDatabase(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	require.NoError(t, err, "go list must run for the dependency set to be checkable")

	deps := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		deps[strings.TrimSpace(line)] = true
	}

	banned := []struct {
		pkg string
		why string
	}{
		{"github.com/ncruces/go-sqlite3", "the embedded SQLite wasm is megabytes the agent has no database to spend them on"},
		{"gorm.io/gorm", "the agent has nothing to persist"},
		{"github.com/grmrgecko/karakuri/pkg/models", "the data model belongs to the server"},
		{"github.com/grmrgecko/karakuri/pkg/database", "the agent opens no connection"},
		{"github.com/grmrgecko/karakuri/pkg/engine", "the agent implements nodes against pkg/nodeapi, not the run loop"},
		{"github.com/grmrgecko/karakuri/pkg/pluginhost", "the agent is a plugin, not a plugin host"},
	}
	for _, b := range banned {
		assert.Falsef(t, deps[b.pkg], "the agent must not link %s: %s", b.pkg, b.why)
	}

	// The contract the agent does build against, so a split that accidentally
	// empties it fails here rather than at the next enrollment.
	assert.True(t, deps["github.com/grmrgecko/karakuri/pkg/nodeapi"],
		"the agent contributes node types, so it must link pkg/nodeapi")
}
