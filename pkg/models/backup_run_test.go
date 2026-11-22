package models

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBackupRunErrorKeepsWholeCharacters locks that a long failure reason is
// cut to BackupErrorMax characters, not bytes. Cutting at byte 2000 split the
// two-byte é at position 2000 (1999 ASCII bytes, then é), and PostgreSQL
// refuses the invalid UTF-8 that leaves, so the run stayed marked running.
func TestBackupRunErrorKeepsWholeCharacters(t *testing.T) {
	testDB(t)
	run := &BackupRun{Trigger: BackupTriggerCLI, Status: BackupFailed, Verified: BackupVerifyFailed,
		Error: strings.Repeat("a", BackupErrorMax-1) + "é" + strings.Repeat("b", 10)}
	require.NoError(t, CreateBackupRun(run))

	runs, err := ListBackupRuns(1)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.True(t, utf8.ValidString(runs[0].Error), "a cut reason must stay valid UTF-8")
	assert.Equal(t, strings.Repeat("a", BackupErrorMax-1)+"é", runs[0].Error, "the reason keeps its first 2000 characters")
}
