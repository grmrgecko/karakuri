package mqtt

import (
	"testing"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMatch pins MQTT topic filter matching against the rules the protocol
// specification lays down, not against this implementation's own reading of
// them. Every row is a case the spec settles and that a naive prefix or glob
// match gets wrong: + matches exactly one level and never several, # must be
// the final level, # also matches its own parent, and an empty level is a real
// level rather than nothing.
//
// Getting any of these wrong means a trigger fires on topics it should ignore
// or misses ones it should catch, and neither shows up as an error anywhere.
func TestMatch(t *testing.T) {
	cases := []struct {
		filter string
		topic  string
		want   bool
	}{
		// Exact matches.
		{"a/b/c", "a/b/c", true},
		{"a/b/c", "a/b", false},
		{"a/b", "a/b/c", false},
		{"a", "a", true},
		{"a", "b", false},

		// Single-level wildcard.
		{"a/+/c", "a/b/c", true},
		{"a/+/c", "a/x/c", true},
		{"a/+/c", "a/b/d", false},
		{"a/+/c", "a/b/x/c", false},
		{"+", "a", true},
		{"+", "a/b", false},
		{"+/+", "a/b", true},
		{"zigbee/+/state", "zigbee/kitchen/state", true},
		{"zigbee/+/state", "zigbee/kitchen/light/state", false},
		{"zigbee/+/state", "zigbee/state", false},

		// Multi-level wildcard.
		{"#", "a", true},
		{"#", "a/b/c", true},
		{"a/#", "a/b", true},
		{"a/#", "a/b/c/d", true},
		{"a/#", "b/c", false},
		// '#' matches the parent level itself.
		{"a/#", "a", true},
		{"sport/tennis/#", "sport/tennis", true},
		// '#' must be the final level.
		{"a/#/c", "a/b/c", false},

		// Combined wildcards.
		{"a/+/#", "a/b", true},
		{"a/+/#", "a/b/c/d", true},
		{"a/+/#", "a", false},

		// Empty levels are distinct levels per the spec.
		{"a//c", "a//c", true},
		{"a/+/c", "a//c", true},

		// A leading wildcard never matches a topic beginning with $ (MQTT
		// 3.1.1 section 4.7.2, 5.0 section 4.7.2); only a filter naming the
		// $ level does.
		{"#", "$SYS/broker/uptime", false},
		{"+/broker/uptime", "$SYS/broker/uptime", false},
		{"$SYS/#", "$SYS/broker/uptime", true},
		{"$SYS/+/uptime", "$SYS/broker/uptime", true},
		{"a/#", "a/$b", true},
	}

	for _, c := range cases {
		assert.Equalf(t, c.want, Match(c.filter, c.topic),
			"Match(%q, %q) must follow the MQTT specification", c.filter, c.topic)
	}
}

// TestSubscribeRefusesMalformedFilter locks that a filter the broker would
// reject never joins the driver's registry. A registered filter is sent with
// every reconcile and after every reconnect; a broker that answers a
// malformed one by closing the session closes it again each time, which takes
// every other subscription on the connection down with it, and one that
// refuses only the filter is asked again without end. A shared subscription
// is refused as well: its messages arrive on topics the filter does not
// match, so no handler would ever receive them.
func TestSubscribeRefusesMalformedFilter(t *testing.T) {
	tests := []struct {
		filter string
		valid  bool
	}{
		{filter: "sensors/+/state", valid: true},
		{filter: "sensors/#", valid: true},
		{filter: "#", valid: true},
		{filter: "+", valid: true},
		{filter: "/leading/and/trailing/", valid: true},
		{filter: "$SYS/broker/uptime", valid: true},
		{filter: "sensors/#/state"},
		{filter: "sensors/temp#"},
		{filter: "sensors/te+mp/state"},
		{filter: "sensors/+temp"},
		{filter: "sensors/\x00"},
		{filter: "sensors/\xff"},
		{filter: "$share/group/sensors/#"},
		{filter: ""},
	}
	d, err := New(integrations.ConnModel{ID: uuid.New(), Kind: Kind,
		Config: []byte(`{"broker_url":"mqtt://127.0.0.1:1"}`)}, integrations.Deps{})
	require.NoError(t, err)
	driver := d.(*Driver)
	for _, tt := range tests {
		t.Run(tt.filter, func(t *testing.T) {
			drop, err := driver.Subscribe(tt.filter, 0, func(string, []byte) {})
			if tt.valid {
				require.NoError(t, err, "a well-formed filter must be accepted")
				drop()
				return
			}
			require.Error(t, err, "a filter the broker cannot hold must be refused before it is registered")
			driver.mu.Lock()
			defer driver.mu.Unlock()
			assert.Empty(t, driver.subs, "a refused filter must not be left for the reconcile loop to send")
		})
	}
}
