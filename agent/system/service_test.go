//go:build linux

package system

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServiceConfirmation uses executable systemctl fixtures to distinguish explicit final states from transitions and failures.
func TestServiceConfirmation(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	calls := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nif [ \"$1\" = --user ]; then shift; fi\nif [ \"$1\" = show ]; then cat '" + state + "'; else printf '%s\\n' \"$1\" >> '" + calls + "'; fi\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, tc := range []struct {
		name, action, active, unitfile string
		matched                        bool
	}{
		{"start", "start", "active", "disabled", true},
		{"stop", "stop", "inactive", "enabled", true},
		{"enable", "enable", "inactive", "enabled", true},
		{"disable", "disable", "active", "disabled", true},
		{"transition is not stopped", "stop", "deactivating", "disabled", false},
		{"failed is not stopped", "stop", "failed", "disabled", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := "LoadState=loaded\nActiveState=" + tc.active + "\nSubState=synthetic\nUnitFileState=" + tc.unitfile + "\n"
			require.NoError(t, os.WriteFile(state, []byte(fixture), 0600))
			cfg := map[string]any{"name": "karakuri-fixture.service", "scope": "user", "action": tc.action, "confirm": true, "confirmation_timeout": "50ms", "confirmation_interval": "5ms"}
			if tc.matched {
				out, err := (serviceControlNode{}).Execute(context.Background(), httpContext(t, cfg), map[string]*nodeapi.Msg{"in": {Payload: "keep"}})
				require.NoError(t, err)
				assert.Equal(t, "keep", out["out"][0].Payload)
				assert.Equal(t, true, out["out"][0].Meta["confirmed"])
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
				defer cancel()
				_, err := awaitService(ctx, nodeapi.RealClock{}, "karakuri-fixture.service", "user", tc.action, 5*time.Millisecond)
				assert.ErrorIs(t, err, context.DeadlineExceeded)
			}
		})
	}
	data, err := os.ReadFile(calls)
	require.NoError(t, err)
	assert.Equal(t, "start\nstop\nenable\ndisable\n", string(data), "confirmation must not repeat a control action")
}
