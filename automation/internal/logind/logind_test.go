//go:build linux

package logind

import (
	"testing"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/stretchr/testify/assert"
)

// TestRefuseParkedHyprlock pins that a credentialed action never types into a
// parked hyprlock session. The keyboard reaches the foreground desktop, so an
// unlock of user B's parked session while user A is active would type B's
// password and Return into A's focused window.
func TestRefuseParkedHyprlock(t *testing.T) {
	cases := []struct {
		name     string
		action   autoapi.SessionAction
		active   bool
		hyprlock bool
		refused  bool
	}{
		{"unlock of a parked hyprlock session", autoapi.SessionUnlock, false, true, true},
		{"login to a parked hyprlock session", autoapi.SessionLogin, false, true, true},
		{"unlock of a foreground hyprlock session", autoapi.SessionUnlock, true, true, false},
		{"login to a foreground hyprlock session", autoapi.SessionLogin, true, true, false},
		{"unlock of a parked session without hyprlock", autoapi.SessionUnlock, false, false, false},
	}
	for _, tc := range cases {
		req := autoapi.LoginRequest{Action: tc.action, Target: autoapi.SessionInfo{Active: tc.active}}
		err := refuseParkedHyprlock(req, tc.hyprlock)
		if tc.refused {
			assert.ErrorIs(t, err, autoapi.ErrUnsupported, tc.name)
		} else {
			assert.NoError(t, err, tc.name)
		}
	}
}
