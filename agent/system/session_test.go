package system

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grmrgecko/karakuri/automation"
)

// TestResolveSessionPrefersTheDesktop pins what logind actually reports for a
// signed-in user, which is two active sessions and not one: the graphical
// session on a seat, and a class=manager session for their systemd user
// instance. Only the first has a screen, and logind refuses the second with
// "Session does not support lock screen" — a message that reads as though the
// action is unsupported rather than as though the wrong session was picked, so
// the ranking is what keeps an unlock from failing for a reason nobody can act
// on. The rows are shaped like `loginctl show-session` on Arch with KDE on
// Wayland, where both sessions belong to uid 1000 and both report Active=yes.
func TestResolveSessionPrefersTheDesktop(t *testing.T) {
	manager := automation.SessionInfo{
		ID: "1", User: "grmrgecko", UID: 1000,
		Type: "unspecified", Class: automation.SessionClassManager,
		State: "active", Active: true,
		Since: time.Date(2026, 8, 26, 21, 17, 42, 0, time.UTC),
	}
	desktop := automation.SessionInfo{
		ID: "2", User: "grmrgecko", UID: 1000, Seat: "seat0", TTY: "tty1",
		Type: "wayland", Class: automation.SessionClassUser,
		State: "active", Active: true,
		Since: time.Date(2026, 8, 26, 21, 18, 2, 0, time.UTC),
	}
	idleDesktop := automation.SessionInfo{
		ID: "5", User: "alice", UID: 1001, Seat: "seat0",
		Type: "x11", Class: automation.SessionClassUser,
		State: "online", Active: false,
		Since: time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC),
	}
	greeter := automation.SessionInfo{
		ID: "c1", User: "sddm", UID: 963, Seat: "seat0",
		Type: "wayland", Class: automation.SessionClassGreeter,
		State: "online", Active: false,
	}
	all := []automation.SessionInfo{manager, desktop, idleDesktop, greeter}

	tests := []struct {
		name string
		cfg  sessionConfig
		want string
		why  string
	}{
		{
			name: "active desktop skips the service manager",
			cfg:  sessionConfig{Target: targetActive},
			want: "2",
			why:  "the manager session is listed first and is equally active, but it has no screen",
		},
		{
			name: "naming a user still means their desktop",
			cfg:  sessionConfig{Target: targetUser, User: "grmrgecko"},
			want: "2",
			why:  "a user matches both of their sessions, and only one of them can be locked",
		},
		{
			name: "a user name is matched case-insensitively",
			cfg:  sessionConfig{Target: targetUser, User: "GRMRGECKO"},
			want: "2",
		},
		{
			name: "a user with only an inactive desktop still resolves",
			cfg:  sessionConfig{Target: targetUser, User: "alice"},
			want: "5",
			why:  "a session switched away from is still the one to unlock",
		},
		{
			name: "an id is taken exactly",
			cfg:  sessionConfig{Target: targetID, SessionID: "1"},
			want: "1",
			why:  "an id names one session and nothing outranks it",
		},
		{
			name: "the greeter is found by class, not by being graphical",
			cfg:  sessionConfig{Target: targetGreeter},
			want: "c1",
		},
	}
	for _, tc := range tests {
		got, err := resolveSession(all, tc.cfg)
		require.NoError(t, err, "resolving %s", tc.name)
		assert.Equal(t, tc.want, got.ID, "%s: %s", tc.name, tc.why)
	}
}

// TestResolveSessionNamesWhatWasThere locks that a target matching nothing
// reports the sessions it could have matched. Unlocking the wrong screen is
// worse than not unlocking one, so the node refuses rather than falling back,
// and the message has to be enough to fix the configuration without shell
// access to the host.
func TestResolveSessionNamesWhatWasThere(t *testing.T) {
	sessions := []automation.SessionInfo{{
		ID: "2", User: "grmrgecko", UID: 1000,
		Type: "wayland", Class: automation.SessionClassUser, Active: true,
	}}

	_, err := resolveSession(sessions, sessionConfig{Target: targetUser, User: "bob"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bob has no session", "the message must name the user that did not match")
	assert.Contains(t, err.Error(), "2 (grmrgecko)", "the message must name the sessions that were there")
}
