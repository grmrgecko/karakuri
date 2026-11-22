//go:build unix && !darwin

package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCompositorFromEnvironment(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "Plasma", env: map[string]string{"XDG_CURRENT_DESKTOP": "KDE"}, want: CompositorKWin},
		{name: "GNOME", env: map[string]string{"XDG_CURRENT_DESKTOP": "GNOME"}, want: CompositorGNOME},
		{name: "COSMIC", env: map[string]string{"XDG_CURRENT_DESKTOP": "COSMIC"}, want: CompositorCOSMIC},
		{name: "Sway", env: map[string]string{"XDG_SESSION_DESKTOP": "sway"}, want: CompositorWlroots},
		{name: "Hyprland socket", env: map[string]string{"HYPRLAND_INSTANCE_SIGNATURE": "instance"}, want: CompositorWlroots},
		{name: "unknown", env: map[string]string{"XDG_CURRENT_DESKTOP": "example"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, CompositorFromEnvironment(test.env))
		})
	}
}
