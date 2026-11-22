package homeassistant

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mappingCase is one entry of testdata/mapping.json, which the widgets' vitest
// suite reads too: both copies of the mapping must give these answers.
type mappingCase struct {
	Name     string         `json:"name"`
	Entity   Entity         `json:"entity"`
	Kind     string         `json:"kind"`
	ReadOnly bool           `json:"readOnly"`
	Options  map[string]any `json:"options"`
	Value    any            `json:"value"`
	Writes   []struct {
		Attribute string `json:"attribute"`
		Value     any    `json:"value"`
		Call      *Call  `json:"call"`
	} `json:"writes"`
}

// roundTrip normalizes a value through JSON, so a Go float and a fixture
// number compare equal and absent attributes are absent on both sides.
func roundTrip(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var out any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// TestMappingFixture holds the Go mapping to the shared fixture.
func TestMappingFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/mapping.json")
	require.NoError(t, err)
	var cases []mappingCase
	require.NoError(t, json.Unmarshal(raw, &cases))
	require.NotEmpty(t, cases)

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			m := Propose(tc.Entity)
			assert.Equal(t, tc.Kind, m.Kind)
			assert.Equal(t, tc.ReadOnly, !m.Writable)
			assert.Equal(t, roundTrip(t, tc.Options), roundTrip(t, m.Options))
			assert.Equal(t, roundTrip(t, tc.Value), roundTrip(t, Value(tc.Entity)))
			for _, w := range tc.Writes {
				call, ok := Command(tc.Entity, w.Attribute, w.Value)
				if w.Call == nil {
					assert.False(t, ok, "%s %v takes no command", w.Attribute, w.Value)
					continue
				}
				require.True(t, ok, "%s %v", w.Attribute, w.Value)
				assert.Equal(t, roundTrip(t, w.Call), roundTrip(t, call), "%s %v", w.Attribute, w.Value)
			}
		})
	}
}

// TestValuesUnderPrimaryAttribute covers a scalar kind's device state, which
// carries its value under the kind's primary attribute.
func TestValuesUnderPrimaryAttribute(t *testing.T) {
	assert.Equal(t, map[string]any{"on": true}, Values(Entity{EntityID: "switch.a", State: "on"}))
	assert.Equal(t, map[string]any{"state": "locked"}, Values(Entity{EntityID: "lock.a", State: "locked"}))
	assert.Equal(t, map[string]any{"value": 4.0}, Values(Entity{EntityID: "number.a", State: "4"}))
}

// TestCommandRefusesReportedOnlyStates locks that a lock or cover is only told
// a state that names an action. The lock and cover kinds also accept the
// states a device reports in between, such as locking and jammed, and a flow
// mirroring one lock's state onto another sent them here, where anything but
// locked called lock.unlock and anything but open or closed stopped the cover.
func TestCommandRefusesReportedOnlyStates(t *testing.T) {
	lock := Entity{EntityID: "lock.front", State: "locked"}
	cover := Entity{EntityID: "cover.blind", State: "closed"}
	tests := []struct {
		name    string
		entity  Entity
		value   string
		service string
	}{
		{"lock locked", lock, "locked", "lock"},
		{"lock unlocked", lock, "unlocked", "unlock"},
		{"lock locking", lock, "locking", ""},
		{"lock unlocking", lock, "unlocking", ""},
		{"lock jammed", lock, "jammed", ""},
		{"cover open", cover, "open", "open_cover"},
		{"cover closed", cover, "closed", "close_cover"},
		{"cover stopped", cover, "stopped", "stop_cover"},
		{"cover opening", cover, "opening", ""},
		{"cover closing", cover, "closing", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			call, ok := Command(tc.entity, "state", tc.value)
			if tc.service == "" {
				assert.False(t, ok, "%s is a reading, so it must call no service, but it called %s.%s",
					tc.value, call.Domain, call.Service)
				return
			}
			require.True(t, ok, "%s names an action", tc.value)
			assert.Equal(t, tc.service, call.Service)
		})
	}
}
