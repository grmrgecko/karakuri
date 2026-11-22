//go:build !windows && !darwin

package inspectunix

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// TestMutterSignatures pins the monitor structs to Mutter's DisplayConfig XML.
// Field order and integer width are part of the D-Bus wire contract.
func TestMutterSignatures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		value     any
		signature string
	}{
		{"physical monitors", []mutterMonitor{}, "a((ssss)a(siiddada{sv})a{sv})"},
		{"logical monitors", []logicalMonitor{}, "a(iiduba(ssss)a{sv})"},
		{"monitor configuration", []configuredLogical{}, "a(iiduba(ssa{sv}))"},
	} {
		assert.Equal(t, tc.signature, dbus.SignatureOf(tc.value).String(), "signature mismatch for %s", tc.name)
	}
}

// TestXRandROutputs covers signed desktop positions, disconnected outputs, and
// the current-mode marker in xrandr's text format. The multi-monitor sample is synthetic.
func TestXRandROutputs(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		count      int
	}{
		{"multiple outputs", "Screen 0: minimum 8 x 8, current 3200 x 1080, maximum 32767 x 32767\nDP-1 connected primary 1920x1080+0+0 (normal left inverted right x axis y axis)\n   1920x1080 60.00*+ 59.94\nHDMI-1 connected 1280x720-1280+180 (normal left inverted right x axis y axis)\n   1280x720 59.94*+\nDP-2 disconnected (normal left inverted right x axis y axis)\n", 2},
		{"truncated header", "DP-1 connected primary 1920x", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseXRandR(tc.text, 24)
			require.Len(t, got, tc.count)
			if tc.count == 0 {
				return
			}
			assert.True(t, got[0].Primary)
			assert.Equal(t, 60.0, got[0].Refresh)
			assert.Equal(t, -1280, got[1].X)
			assert.Equal(t, 180, got[1].Y)
			assert.Equal(t, 24, got[1].Depth)
			assert.Equal(t, 59.94, got[1].Refresh)
		})
	}
}

// TestWLRRefresh pins wlr-randr 0.5.0 JSON refresh values to Hz, unlike Sway's
// millihertz. The fixture was captured from Arch's Hyprland desktop.
func TestWLRRefresh(t *testing.T) {
	data, err := os.ReadFile("testdata/wlr-randr.json")
	require.NoError(t, err)
	displays, err := parseWLRDisplays(data)
	require.NoError(t, err)
	require.Len(t, displays, 1)
	assert.InDelta(t, 74.995, displays[0].Refresh, 0.001)
	assert.Equal(t, 1440, displays[0].Width)
	assert.Equal(t, 900, displays[0].Height)
}

// TestSetDisplayReportsCompositorRefusal locks the mode change to its own
// failure: when the compositor's tool refuses the mode, the caller gets what it
// said. It pins the regression where the Hyprland branch dropped that error
// and reported only that the display had not switched.
func TestSetDisplayReportsCompositorRefusal(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = "-j" ]; then
  echo '[{"name":"DP-1","x":0,"y":0,"width":1920,"height":1080,"refreshRate":60,"scale":1,"focused":true,"transform":0}]'
  exit 0
fi
echo "mode rejected by the compositor"
exit 1
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hyprctl"), []byte(script), 0o755))
	t.Setenv("PATH", dir)
	t.Setenv("WAYLAND_DISPLAY", "wayland-test")
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "test")

	err := Backend{}.SetDisplay(context.Background(), autoapi.DisplayMode{Width: 1280, Height: 720, Depth: 32})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mode rejected by the compositor", "the compositor's own refusal must reach the caller")
}
