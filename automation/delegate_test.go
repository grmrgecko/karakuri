package automation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpawnTarget pins which session a helper is started in: the one found at
// spawn time when there is one, the startup one when the lookup failed
// outright, and none while nobody is at a desktop.
func TestSpawnTarget(t *testing.T) {
	startup := DelegateTarget{UID: 1000, User: "alice", SessionID: "2"}
	fresh := DelegateTarget{UID: 1000, User: "alice", SessionID: "5"}

	tests := []struct {
		name    string
		fresh   DelegateTarget
		found   bool
		startup DelegateTarget
		want    DelegateTarget
		wantErr error
	}{
		{"a fresh session wins", fresh, true, startup, fresh, nil},
		{"a lookup failure keeps the startup session", DelegateTarget{}, false, startup, startup, nil},
		{"nobody at a desktop now is an error even with a startup session", pendingTarget(), true, startup, DelegateTarget{}, errNoDesktopSession},
		{"a pending startup with no session yet is an error", pendingTarget(), true, pendingTarget(), DelegateTarget{}, errNoDesktopSession},
		{"a pending startup with a failed lookup is an error", DelegateTarget{}, false, pendingTarget(), DelegateTarget{}, errNoDesktopSession},
		{"a pending startup resolves once a session appears", fresh, true, pendingTarget(), fresh, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := spawnTarget(tt.fresh, tt.found, tt.startup)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestDelegateTargetString renders a pending target without a uid or session,
// since it has neither yet.
func TestDelegateTargetString(t *testing.T) {
	assert.Equal(t, "the console session (no user signed in yet)", pendingTarget().String())
	assert.Equal(t, "alice (uid 1000, session 2)", DelegateTarget{UID: 1000, User: "alice", SessionID: "2"}.String())
	assert.Equal(t, "alice (uid 1000)", DelegateTarget{UID: 1000, User: "alice"}.String())
}
