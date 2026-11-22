package portal

import (
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMonitorCoordinates pins ScreenCast's a(ua{sv}) and (ii) wire types.
// Synthetic monitors cover a negative origin and logical rather than pixel size.
func TestMonitorCoordinates(t *testing.T) {
	type pair struct{ X, Y int32 }
	wire := []monitorStream{
		{41, map[string]dbus.Variant{"position": dbus.MakeVariant(pair{-1280, 0}), "size": dbus.MakeVariant(pair{1280, 720})}},
		{42, map[string]dbus.Variant{"position": dbus.MakeVariant(pair{0, 0}), "size": dbus.MakeVariant(pair{1920, 1080})}},
	}
	value := dbus.MakeVariant(wire)
	assert.Equal(t, "a(ua{sv})", value.Signature().String())
	// godbus decodes D-Bus structs as slices before Store converts them.
	decoded := [][]any{{wire[0].Node, wire[0].Properties}, {wire[1].Node, wire[1].Properties}}
	value = dbus.MakeVariantWithSignature(decoded, value.Signature())
	streams, err := decodeStreams(value)
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		x, y   int
		node   uint32
		dx, dy float64
	}{
		{"negative monitor", -100, 100, 41, 1180, 100},
		{"shared edge", 0, 100, 42, 0, 100},
		{"outside", 1920, 100, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, x, y, err := streamPoint(streams, tc.x, tc.y)
			if tc.node == 0 {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.node, node)
			assert.Equal(t, tc.dx, x)
			assert.Equal(t, tc.dy, y)
		})
	}
	delete(streams[0].Properties, "position")
	_, _, _, err = streamPoint(streams, -100, 100)
	require.Error(t, err, "optional metadata cannot be replaced with a guessed origin")
	_, err = decodeStreams(dbus.MakeVariant("invalid"))
	require.Error(t, err)
}
