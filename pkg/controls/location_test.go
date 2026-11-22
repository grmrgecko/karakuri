package controls_test

import (
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLocationSinceMovesOnStateOnly covers the one attribute no caller writes.
// since is how long someone has been where they are, so a phone sending a
// fresh fix every minute must not reset it, and a state change must.
func TestLocationSinceMovesOnStateOnly(t *testing.T) {
	s := setupStore(t)

	person, err := s.Create("James", controls.KindLocation, "", nil, false)
	require.NoError(t, err, "a location needs no settings")
	assert.Equal(t, map[string]any{}, person.Value, "a new location claims neither home nor away")

	home, err := s.Set(person.ID, "home", controls.By(controls.SourceFlow))
	require.NoError(t, err, "a bare set lands on state")
	value := home.Value.(map[string]any)
	assert.Equal(t, "home", value["state"])
	since, ok := value["since"].(string)
	require.True(t, ok, "a state change stamps since")
	_, err = time.Parse(time.RFC3339, since)
	require.NoError(t, err, "since is RFC 3339")

	tests := []struct {
		name      string
		write     any
		attribute string
		keepSince bool
	}{
		{name: "coordinates only", write: map[string]any{"latitude": 41.88, "longitude": -87.63}, keepSince: true},
		{name: "same state with accuracy", write: map[string]any{"state": "home", "accuracy": 10}, keepSince: true},
		{name: "one attribute", attribute: "source", write: "gps", keepSince: true},
		{name: "new state", write: map[string]any{"state": "work"}, keepSince: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := s.Get(person.ID)
			prev := before.Value.(map[string]any)["since"]
			// A stamp is to the second, so a state change inside the same
			// second as the last one would look unchanged.
			if !tc.keepSince {
				time.Sleep(1100 * time.Millisecond)
			}
			var next controls.Snapshot
			if tc.attribute != "" {
				next, err = s.SetAttribute(person.ID, tc.attribute, tc.write, controls.By(controls.SourceAPI))
			} else {
				next, err = s.Set(person.ID, tc.write, controls.By(controls.SourceAPI))
			}
			require.NoError(t, err)
			got := next.Value.(map[string]any)["since"]
			if tc.keepSince {
				assert.Equal(t, prev, got, "since stays when state does")
			} else {
				assert.NotEqual(t, prev, got, "since moves with state")
			}
		})
	}

	final, _ := s.Get(person.ID)
	value = final.Value.(map[string]any)
	assert.Equal(t, 41.88, value["latitude"], "a partial write merges into what is there")
	assert.Equal(t, "gps", value["source"])

	_, err = s.SetAttribute(person.ID, "since", "2020-01-01T00:00:00Z", controls.By(controls.SourceAPI))
	assert.Error(t, err, "since is read-only when named")
	next, err := s.Set(person.ID, map[string]any{"since": "2020-01-01T00:00:00Z", "accuracy": 5}, controls.By(controls.SourceAPI))
	require.NoError(t, err, "a whole-value write carrying since still lands its other attributes")
	assert.Equal(t, value["since"], next.Value.(map[string]any)["since"], "a since inside a whole value is ignored")
}

// TestLocationValidation covers what a location refuses: impossible
// coordinates, a negative accuracy, an empty state, and, when the author has
// restricted it, a zone the control does not list.
func TestLocationValidation(t *testing.T) {
	s := setupStore(t)

	open, err := s.Create("Open", controls.KindLocation, "", nil, false)
	require.NoError(t, err)
	_, err = s.Set(open.ID, "Grandma's", controls.By(controls.SourceAPI))
	assert.NoError(t, err, "an unrestricted location keeps whatever zone a source names")

	restricted, err := s.Create("Restricted", controls.KindLocation, "", map[string]any{
		"restrict_zones": true,
		"zones":          []any{"home", "not_home", "work"},
	}, false)
	require.NoError(t, err)

	tests := []struct {
		name    string
		write   any
		wantErr string
	}{
		{name: "listed zone", write: "work"},
		{name: "unlisted zone", write: "gym", wantErr: `"gym" is not one of the control's zones: home, not_home, work`},
		{name: "empty state", write: "  ", wantErr: "a location state cannot be empty"},
		{name: "latitude above", write: map[string]any{"latitude": 91}, wantErr: "value 91 is above the maximum of 90"},
		{name: "longitude below", write: map[string]any{"longitude": -181}, wantErr: "value -181 is below the minimum of -180"},
		{name: "negative accuracy", write: map[string]any{"accuracy": -1}, wantErr: "value -1 is below the minimum of 0"},
		{name: "unknown attribute", write: map[string]any{"altitude": 3}, wantErr: `Location has no "altitude" attribute`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Set(restricted.ID, tc.write, controls.By(controls.SourceAPI))
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tc.wantErr)
		})
	}

	_, err = s.Create("No zones", controls.KindLocation, "", map[string]any{
		"restrict_zones": true,
		"zones":          []any{},
	}, false)
	assert.Error(t, err, "a location restricted to no zones could never be set")
}

// TestStampSinceCompositeState pins the MQTT relay path, where a device's
// state script returns state unchecked. Comparing two []any states with == is
// a runtime panic, raised while the relay holds the device lock, so the
// second report of a list-valued state wedged every subscription on that
// broker.
func TestStampSinceCompositeState(t *testing.T) {
	first := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	before := controls.StampSince(nil, map[string]any{"state": []any{"home", "garage"}}, first)

	later := first.Add(time.Hour)
	var same, moved any
	require.NotPanics(t, func() {
		same = controls.StampSince(before, map[string]any{"state": []any{"home", "garage"}}, later)
		moved = controls.StampSince(before, map[string]any{"state": []any{"work"}}, later)
	}, "a list-valued state must compare without panicking")
	assert.Equal(t, "2026-11-01T08:00:00Z", same.(map[string]any)["since"], "an unchanged list keeps since")
	assert.Equal(t, "2026-11-01T09:00:00Z", moved.(map[string]any)["since"], "a changed list restamps since")
}
