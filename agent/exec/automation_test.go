package exec

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAutomationCommandFile preserves literal templates and CRLF in a closed private file.
func TestAutomationCommandFile(t *testing.T) {
	source := "type \"{{ payload.command }}\"\r\nwait state --text 'Ready'\r\n"
	path, err := writeAutomationCommands(source)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(path) })
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, source, string(contents))
	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
	_, err = writeAutomationCommands(" \r\n")
	assert.ErrorContains(t, err, "commands are required")
}
