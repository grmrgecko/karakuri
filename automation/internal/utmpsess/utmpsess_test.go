//go:build linux || freebsd || openbsd || netbsd || dragonfly

package utmpsess

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// TestListWithoutWho locks that a host with no who(1) still reports the
// session the process runs in. The missing program was compared against an
// error type the tool runner never returns, so the listing failed with
// "executable file not found" where it was meant to fall back.
func TestListWithoutWho(t *testing.T) {
	for _, tc := range []struct {
		name string
		who  string
		want []string
	}{
		{name: "who is not installed", want: []string{"self"}},
		{
			name: "who reports a desktop and a terminal",
			who:  "#!/bin/sh\nprintf 'alice :0 2026-01-01 10:00\\nbob pts/1 2026-01-01 10:05\\n'\n",
			want: []string{"alice@:0", "bob@pts/1", "self"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			if tc.who != "" {
				require.NoError(t, os.WriteFile(filepath.Join(bin, "who"), []byte(tc.who), 0o755),
					"The fixture program must be created.")
			}
			t.Setenv("PATH", bin)

			sessions, err := New().List(context.Background())
			require.NoError(t, err, "A missing who is a host without utmp records, not a failed listing.")
			ids := make([]string, 0, len(sessions))
			byID := map[string]autoapi.SessionInfo{}
			for _, s := range sessions {
				ids = append(ids, s.ID)
				byID[s.ID] = s
			}
			assert.Equal(t, tc.want, ids, "The listing holds the utmp rows and then the process's own session.")
			if desktop, ok := byID["alice@:0"]; ok {
				assert.Equal(t, "x11", desktop.Type, "A line naming a display is a graphical session.")
				assert.Equal(t, "tty", byID["bob@pts/1"].Type, "A line naming a terminal is a terminal session.")
			}
		})
	}
}

// TestListMergesOwnDesktopRow locks that the process's own X session is not
// listed twice. A display manager that writes utmp, as SDDM does, records the
// desktop with the display in the line column. The own session was looked up
// under a key no utmp row carries, so it was appended beside its own row and
// one desktop read as two sessions, the real one never marked active.
func TestListMergesOwnDesktopRow(t *testing.T) {
	me, err := user.Current()
	require.NoError(t, err, "The test needs the account it runs as.")

	bin := t.TempDir()
	who := "#!/bin/sh\nprintf '" + me.Username + " :0 2026-01-01 10:00\\n" + me.Username + " pts/3 2026-01-01 10:05\\n'\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "who"), []byte(who), 0o755),
		"The fixture program must be created.")
	t.Setenv("PATH", bin)
	t.Setenv("WAYLAND_DISPLAY", "")
	// A client names the screen too; utmp records the display alone.
	t.Setenv("DISPLAY", ":0.0")

	sessions, err := New().List(context.Background())
	require.NoError(t, err, "The listing must succeed with who present.")
	require.Len(t, sessions, 2, "The utmp rows already cover the desktop this process runs in.")
	assert.Equal(t, me.Username+"@:0", sessions[0].ID, "The desktop keeps its utmp row.")
	assert.True(t, sessions[0].Active, "The row for the desktop this process runs in is the active one.")
	assert.Equal(t, "active", sessions[0].State, "The state follows the active flag.")
	assert.False(t, sessions[1].Active, "A terminal login of the same user is not the desktop.")
}
