package mcpserver

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAssistantTraceBound keeps diagnostic files parseable when a provider
// response would exceed the per-turn disk limit and reports filesystem failures.
func TestAssistantTraceBound(t *testing.T) {
	trace, err := newAssistantTrace(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, trace.write("turn.start", map[string]any{"prompt": "test"}))
	require.NoError(t, trace.write("model.response", strings.Repeat("x", assistantTraceLimit)))
	require.NoError(t, trace.write("tool.result", "must not be recorded after truncation"))
	require.NoError(t, trace.close(nil))
	raw, err := os.ReadFile(trace.file.Name())
	require.NoError(t, err)
	assert.LessOrEqual(t, len(raw), assistantTraceLimit)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	require.Len(t, lines, 2)
	var marker map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &marker))
	assert.Equal(t, "trace.truncated", marker["type"])

	failed, err := newAssistantTrace(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, failed.file.Close())
	assert.ErrorContains(t, failed.write("turn.start", nil), "writing assistant trace")
	assert.Error(t, failed.close(errors.New("turn failed")))
}
