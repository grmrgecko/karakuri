package controls_test

import (
	"testing"

	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMediaWrites holds each media write to the features the player's options
// give it, so a press the player cannot do is refused with the reason before
// anything is sent.
func TestMediaWrites(t *testing.T) {
	all := map[string]any{"volume": true, "mute": true, "skip": true, "stop": true,
		"seek": true, "power": true, "source": true}
	tests := []struct {
		name      string
		opts      map[string]any
		attribute string
		value     any
		want      any
		err       string
	}{
		{name: "play", attribute: "state", value: "playing", want: "playing"},
		{name: "pause with case", attribute: "state", value: " Paused ", want: "paused"},
		{name: "stop by default", attribute: "state", value: "idle", want: "idle"},
		{name: "stop off", opts: map[string]any{"stop": false}, attribute: "state", value: "idle", err: "this player has no stop"},
		{name: "power off by default", attribute: "state", value: "off", err: "this player has no power control"},
		{name: "power on", opts: all, attribute: "state", value: "on", want: "on"},
		{name: "buffering", opts: all, attribute: "state", value: "buffering", err: "buffering is reported by a player and cannot be set"},
		{name: "unknown state", attribute: "state", value: "rewinding", err: `"rewinding" is not one of a media player's states`},
		{name: "volume", attribute: "volume", value: "30", want: float64(30)},
		{name: "volume high", attribute: "volume", value: 120.0, err: "value 120 is above the maximum of 100"},
		{name: "volume low", attribute: "volume", value: -1.0, err: "value -1 is below the minimum of 0"},
		{name: "volume off", opts: map[string]any{"volume": false}, attribute: "volume", value: 30.0, err: "this media player has no volume"},
		{name: "mute", attribute: "muted", value: "on", want: true},
		{name: "mute off", opts: map[string]any{"mute": false}, attribute: "muted", value: true, err: "this media player has no muted"},
		{name: "next", attribute: "command", value: "Next", want: "next"},
		{name: "unknown command", attribute: "command", value: "shuffle", err: `"shuffle" is not one of a media player's commands`},
		{name: "skip off", opts: map[string]any{"skip": false}, attribute: "command", value: "next", err: "this media player has no skip"},
		{name: "seek off by default", attribute: "position", value: 12.0, err: "this media player has no position"},
		{name: "seek", opts: all, attribute: "position", value: 12.0, want: float64(12)},
		{name: "seek before start", opts: all, attribute: "position", value: -3.0, err: "position -3 is below the start"},
		{name: "source off by default", attribute: "source", value: "Radio", err: "this media player has no source"},
		{name: "source any", opts: all, attribute: "source", value: "Radio", want: "Radio"},
		{name: "source listed", opts: map[string]any{"source": true, "source_list": []any{"TV", "Radio"}},
			attribute: "source", value: "Radio", want: "Radio"},
		{name: "source unlisted", opts: map[string]any{"source": true, "source_list": []any{"TV"}},
			attribute: "source", value: "Radio", err: `"Radio" is not one of the player's sources: TV`},
		{name: "title is reported", attribute: "title", value: "Song", err: "Media player reports Title but cannot be told it"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := controls.CoerceAttribute(controls.KindMedia, tc.opts, tc.attribute, tc.value)
			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestMediaReport covers a whole value a player or a mirroring flow states
// about itself: buffering and read-only details are accepted, and an action
// is not part of any value.
func TestMediaReport(t *testing.T) {
	got, err := controls.Coerce(controls.KindMedia, nil, map[string]any{
		"state": "buffering", "title": "Song", "duration": 200.0, "sources": []any{"TV"},
	})
	require.NoError(t, err)
	assert.Equal(t, "buffering", got.(map[string]any)["state"])

	_, err = controls.Coerce(controls.KindMedia, nil, map[string]any{"command": "next"})
	require.Error(t, err, "an action written as part of a value is refused")
}

// TestMediaCheckAgainstValue covers the limits that come from what the player
// reports rather than from its options.
func TestMediaCheckAgainstValue(t *testing.T) {
	current := map[string]any{"sources": []any{"TV", "Radio"}, "duration": 200.0}
	assert.NoError(t, controls.CheckAgainstValue(controls.KindMedia, current, "source", "Radio"))
	err := controls.CheckAgainstValue(controls.KindMedia, current, "source", "Vinyl")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"Vinyl" is not one of the player's sources: TV, Radio`)
	assert.NoError(t, controls.CheckAgainstValue(controls.KindMedia, current, "position", 120.0))
	assert.Error(t, controls.CheckAgainstValue(controls.KindMedia, current, "position", 240.0))
	assert.NoError(t, controls.CheckAgainstValue(controls.KindMedia, map[string]any{}, "source", "Vinyl"),
		"a player that lists no sources takes any")
}

// TestMediaCommandIsAnnouncedNotStored pins the write-only attribute: a skip
// reaches watchers as a change of command and leaves the stored value as it
// was, so no later read finds a player still skipping.
func TestMediaCommandIsAnnouncedNotStored(t *testing.T) {
	s := setupStore(t)
	player, err := s.Create("Media test", controls.KindMedia, "", nil, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"state": "idle"}, player.Value)

	playing, err := s.SetAttribute(player.ID, "state", "playing", controls.By(controls.SourceAPI))
	require.NoError(t, err)

	var changes []controls.Change
	stop := s.Watch(func(c controls.Change) { changes = append(changes, c) })
	defer stop()

	for range 2 {
		got, err := s.SetAttribute(player.ID, "command", "next", controls.By(controls.SourceFlow))
		require.NoError(t, err)
		assert.Equal(t, playing.Value, got.Value, "the write answers with the control unchanged")
	}
	require.Len(t, changes, 2, "every press is announced, even a repeat")
	assert.Equal(t, "command", changes[0].Attribute)
	assert.Equal(t, "next", changes[0].Control.Value.(map[string]any)["command"])
	assert.Equal(t, playing.Value, changes[0].Old)

	read, ok := s.Get(player.ID)
	require.True(t, ok)
	assert.NotContains(t, read.Value.(map[string]any), "command")

	_, err = s.SetAttribute(player.ID, "volume", 120, controls.By(controls.SourceAPI))
	require.Error(t, err)
	assert.Len(t, changes, 2, "a refused write announces nothing")
}
