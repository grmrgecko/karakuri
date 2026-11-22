//go:build windows

package exec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWindowsTerminationGrace rejects unsupported cooperative shutdown before
// launch while preserving ordinary Windows command and script execution.
func TestWindowsTerminationGrace(t *testing.T) {
	for _, kind := range []string{"command", "script"} {
		for _, grace := range []string{"0s", "500ms"} {
			t.Run(kind+"/"+grace, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "out")
				require.NoError(t, os.WriteFile(path, []byte("original"), 0600))
				cfg := map[string]any{"command": "cmd", "args": []string{"/c", "echo completed"}, "script": "#!cmd\n@echo off\necho completed", "termination_grace": grace, "stdout_path": path}
				raw, err := json.Marshal(cfg)
				require.NoError(t, err)
				var n nodeapi.NodeType = &commandNode{}
				if kind == "script" {
					n = &scriptNode{}
				}
				out, err := n.Execute(context.Background(), processTestContext{config: string(raw)}, nil)
				if grace == "0s" {
					require.NoError(t, err)
					assert.Equal(t, "completed", out["out"][0].Payload)
				} else {
					assert.ErrorContains(t, err, "unsupported on Windows")
					data, err := os.ReadFile(path)
					require.NoError(t, err)
					assert.Equal(t, "original", string(data), "unsupported grace must not touch output files")
				}
			})
		}
	}
}
