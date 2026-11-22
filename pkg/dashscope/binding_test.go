package dashscope

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBindingCapabilities locks the boundary between what a widget stores and
// what the host authorizes. The config strings here are the real thing: they
// are the shape widgets/shared/entitySource.ts writes and the dashboard rows
// hold, so a change on either side that silently stopped producing
// capabilities would leave a dashboard rendering "Unauthorized" instead of its
// values, or — the direction that matters — grant more than the layout shows.
//
// The write column is the security-relevant half. A row the author marked
// display only shows a reading and nothing more; a trigger has nothing to read,
// so a row bound to one grants a press or nothing at all.
func TestBindingCapabilities(t *testing.T) {
	tests := []struct {
		name   string
		config string
		path   string
		want   []Capability
		why    string
	}{
		{
			name:   "entity-control bound to a light",
			config: `{"source":"ha","control":"auto","showName":true,"connectionId":"161a31b3-b477-4b36-955c-3d36d9a9dc0e","entityId":"light.lamps"}`,
			path:   ".",
			want: []Capability{
				{Kind: CapHA, Owner: "161a31b3-b477-4b36-955c-3d36d9a9dc0e", Ref: "light.lamps"},
				{Kind: CapHA, Owner: "161a31b3-b477-4b36-955c-3d36d9a9dc0e", Ref: "light.lamps", Write: true},
			},
			why: "a tile drawn with the control its domain implies is both read and pressed",
		},
		{
			name:   "a row the author marked display only",
			config: `{"source":"ha","displayOnly":true,"connectionId":"c1","entityId":"sensor.outside"}`,
			path:   ".",
			want:   []Capability{{Kind: CapHA, Owner: "c1", Ref: "sensor.outside"}},
			why:    "display only is the author saying the row shows a reading, so no write follows from it",
		},
		{
			name:   "a row saved before display only had a checkbox",
			config: `{"source":"ha","control":"value","connectionId":"c1","entityId":"sensor.outside"}`,
			path:   ".",
			want:   []Capability{{Kind: CapHA, Owner: "c1", Ref: "sensor.outside"}},
			why: "a deployed version is frozen, so the old spelling has to keep meaning what it " +
				"meant or an upgrade would hand out writes the author never granted",
		},
		{
			name: "a trigger row with several buttons",
			config: `{"source":"flow-trigger","triggers":[` +
				`{"id":"a","label":"On","flowId":"44444444-4444-4444-4444-444444444444","trigger":"Lights on"},` +
				`{"id":"b","label":"Off","flowId":"44444444-4444-4444-4444-444444444444","trigger":"Lights off"},` +
				`{"id":"c","label":"Unset"}]}`,
			path: ".",
			want: []Capability{
				{Kind: CapTrigger, Owner: "44444444-4444-4444-4444-444444444444", Ref: "Lights on", Write: true},
				{Kind: CapTrigger, Owner: "44444444-4444-4444-4444-444444444444", Ref: "Lights off", Write: true},
			},
			why: "each button is granted on its own, so a row cannot reach a trigger it does not draw",
		},
		{
			name: "a card's rows across every source",
			config: `{"title":"Home","rows":[` +
				`{"id":"a","source":"control","controlId":"11111111-1111-1111-1111-111111111111"},` +
				`{"id":"b","source":"mqtt","deviceId":"22222222-2222-2222-2222-222222222222","deviceField":"on"},` +
				`{"id":"c","source":"flow-trigger","flowId":"33333333-3333-3333-3333-333333333333","trigger":"Movie night"}]}`,
			path: "rows",
			want: []Capability{
				{Kind: CapControl, Ref: "11111111-1111-1111-1111-111111111111"},
				{Kind: CapControl, Ref: "11111111-1111-1111-1111-111111111111", Write: true},
				{Kind: CapDevice, Ref: "22222222-2222-2222-2222-222222222222"},
				{Kind: CapDevice, Ref: "22222222-2222-2222-2222-222222222222", Write: true},
				{Kind: CapTrigger, Owner: "33333333-3333-3333-3333-333333333333", Ref: "Movie night", Write: true},
			},
			why: "a trigger has no state to read, so it grants the press alone",
		},
		{
			name:   "a widget that declares no bindings",
			config: `{"format":"24h","showSeconds":true}`,
			path:   "",
			want:   nil,
			why:    "a clock reads nothing from the host and must authorize nothing",
		},
		{
			name:   "an incomplete binding",
			config: `{"source":"ha","connectionId":"c1"}`,
			path:   ".",
			want:   nil,
			why:    "a half-configured row names no entity, so there is nothing to grant",
		},
		{
			name:   "a single-binding tile cannot grant an array of bindings",
			config: `[{"source":"control","controlId":"hidden-control"}]`,
			path:   ".",
			want:   nil,
			why:    "the tile reads one binding object, so an array cannot authorize data the tile does not render",
		},
		{
			name:   "config that does not parse",
			config: `{"source":`,
			path:   ".",
			want:   nil,
			why:    "an unreadable config denies rather than guessing",
		},
	}

	for _, tc := range tests {
		var got []Capability
		for _, b := range bindingsFrom(tc.config, tc.path) {
			got = append(got, b.capabilities()...)
		}
		assert.ElementsMatchf(t, tc.want, got, "%s: %s", tc.name, tc.why)
	}
}

// TestScopeTopicMapping pins the broker topic forms a dashboard's live values
// arrive on against the capabilities its bindings produce. These strings are
// written in three places that cannot see each other — serve.go's HA relay,
// pkg/controls, pkg/mqttrelay, and each widget's own subscribe call — so the
// mapping back is the one place a typo would show as a dashboard that renders
// once and then never updates.
func TestScopeTopicMapping(t *testing.T) {
	scope := newScope()
	scope.add(Capability{Kind: CapHA, Owner: "conn-1", Ref: "light.lamps"})
	scope.add(Capability{Kind: CapControl, Ref: "ctl-1"})
	scope.add(Capability{Kind: CapDevice, Ref: "dev-1"})
	scope.topics = []string{"weather:forecast:*"}

	tests := []struct {
		topic string
		want  bool
		why   string
	}{
		{"ha:conn-1:state:light.lamps", true, "the entity the dashboard binds to"},
		{"ha:conn-1:state:light.bedroom", false, "an entity on the same connection that no widget shows"},
		{"ha:conn-2:state:light.lamps", false, "the same entity ID on a connection the dashboard does not use"},
		{"control:ctl-1", true, "the control a row is bound to"},
		{"control:ctl-2", false, "another control"},
		{"mqttdevice:dev-1", true, "the device a row is bound to"},
		{"weather:forecast:openmeteo:34.7298:-86.5859", true, "inside the namespace the weather plugin declared"},
		{"run:some-run-id", false, "a flow's run stream is nothing a dashboard exposes"},
		{"ha:*", false, "a wildcard subscription is not a binding"},
	}

	for _, tc := range tests {
		assert.Equalf(t, tc.want, scope.AllowsTopic(tc.topic), "%s: %s", tc.topic, tc.why)
	}
}

// TestPluginAPIGrantIsPerRoute locks the plugin API grant to the routes a
// manifest names. A plugin's other routes are gated on permissions a dashboard
// viewer does not hold — weather's geocoding sits beside its forecast, and an
// administrative route could sit beside either — so a grant that admitted a
// whole method would carry a widget's viewers straight into them.
func TestPluginAPIGrantIsPerRoute(t *testing.T) {
	scope := newScope()
	scope.api["weather"] = []apiRoute{{method: "GET", pattern: "/*/forecast/*"}}

	tests := []struct {
		method string
		path   string
		want   bool
		why    string
	}{
		{"GET", "/openmeteo/forecast/34.7298:-86.5859", true, "the route the widget calls"},
		{"get", "/openmeteo/forecast/34.7298:-86.5859", true, "the method compares case-insensitively"},
		{"GET", "/openmeteo/geo", false, "the config panel's geocoding, gated on a permission an author holds"},
		{"GET", "/openmeteo/geo/reverse/1:2", false, "and its reverse lookup"},
		{"POST", "/openmeteo/forecast/1:2", false, "the same path by another method"},
		{"GET", "/openmeteo/forecast/1:2/extra", false, "a longer path than the pattern"},
		{"GET", "/openmeteo/forecast", false, "a shorter one"},
	}

	for _, tc := range tests {
		assert.Equalf(t, tc.want, scope.AllowsPluginAPI("weather", tc.method, tc.path),
			"%s %s: %s", tc.method, tc.path, tc.why)
	}

	assert.False(t, scope.AllowsPluginAPI("calendar", "GET", "/openmeteo/forecast/1:2"),
		"a grant belongs to the plugin whose widget produced it and reaches no other")
}

// TestScopeWithholdsWritesWithoutControl locks the split that keeps publishing
// a dashboard from handing out the switches: the same scope answers reads
// either way, and only a dashboard whose policy permits operating answers a
// write.
func TestScopeWithholdsWritesWithoutControl(t *testing.T) {
	read := Capability{Kind: CapControl, Ref: "ctl-1"}
	write := Capability{Kind: CapControl, Ref: "ctl-1", Write: true}

	scope := newScope()
	scope.add(read)
	scope.add(write)

	scope.Control = false
	assert.True(t, scope.Allows(read), "a read-only dashboard still shows its values")
	assert.False(t, scope.Allows(write), "an anonymous viewer must not operate a dashboard the author did not open for it")

	scope.Control = true
	assert.True(t, scope.Allows(write), "with control permitted the same binding answers the press")
}

// TestNVRBindings covers a camera binding and an all-cameras binding: each
// grants reads of exactly its camera or connection, and the event topics map
// back onto them.
func TestNVRBindings(t *testing.T) {
	scope := newScope()
	for _, b := range []Binding{
		{Source: SourceNVR, ConnectionID: "conn-1", Camera: "doorbell"},
		{Source: SourceNVR, ConnectionID: "conn-2", Camera: AllCameras},
		{Source: SourceNVR, ConnectionID: "conn-3"},
	} {
		for _, c := range b.capabilities() {
			assert.False(t, c.Write, "a camera is only ever read")
			scope.add(c)
		}
	}

	assert.True(t, scope.AllowsCamera("conn-1", "doorbell"))
	assert.False(t, scope.AllowsCamera("conn-1", "garage"), "another camera on the same connection")
	assert.True(t, scope.AllowsCamera("conn-2", "garage"), "every camera of the all-cameras connection")
	assert.False(t, scope.AllowsCamera("conn-3", "doorbell"), "a binding without a camera grants nothing")

	cams, all := scope.Cameras("conn-1")
	assert.Equal(t, map[string]bool{"doorbell": true}, cams)
	assert.False(t, all)
	_, all = scope.Cameras("conn-2")
	assert.True(t, all)

	assert.True(t, scope.AllowsTopic("nvr:conn-1:events:doorbell"))
	assert.False(t, scope.AllowsTopic("nvr:conn-1:events:garage"))
	assert.False(t, scope.AllowsTopic("nvr:conn-1:events:*"), "every camera's events need a grant of every camera")
	assert.True(t, scope.AllowsTopic("nvr:conn-2:events:*"))
}
