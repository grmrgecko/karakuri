// Package dashscope answers one question: what may a viewer of this dashboard
// read and press?
//
// A dashboard's deployed layout is the grant. Placing a widget that binds to a
// light is what authorizes a viewer to read that light and, if the dashboard
// permits control, to switch it — nothing else on the connection, and nothing
// at all on a connection no widget mentions. Viewers hold no subsystem
// permission, so without this a shared or public dashboard shows errors where
// its values should be; with it, it shows exactly what its author put on it.
//
// The grant comes from the deployed version rather than the draft, for the same
// reason viewers are served the deployed version: editing a dashboard must not
// silently widen what the world can see. An author with edit access is the one
// exception — the editor renders live widgets against the draft they are
// building, so their own draft grants them too.
package dashscope

import (
	"strings"

	"github.com/grmrgecko/karakuri/pkg/mq"
)

// CapKind names the sort of thing a capability points at.
type CapKind string

const (
	// CapHA is one Home Assistant entity on one connection.
	CapHA CapKind = "ha"
	// CapControl is one karakuri control.
	CapControl CapKind = "control"
	// CapDevice is one MQTT device.
	CapDevice CapKind = "device"
	// CapTrigger is one named widget trigger of one flow.
	CapTrigger CapKind = "trigger"
	// CapVariable is setting one dashboard variable's dashboard-wide value.
	CapVariable CapKind = "variable"
	// CapNVR is one camera of an NVR connection, or every camera when Ref
	// is AllCameras.
	CapNVR CapKind = "nvr"
)

// AllCameras is the camera reference that grants every camera of an NVR
// connection, for an event list over all of them.
const AllCameras = "*"

// Capability is one thing a dashboard exposes. Owner holds the connection or
// flow the reference belongs to and is empty for globally identified things;
// Write separates reading a value from acting on it, because a dashboard that
// displays the house should not by itself be able to drive it.
type Capability struct {
	Kind  CapKind
	Owner string
	Ref   string
	Write bool
}

// key is a capability without its direction, so one map holds both and the
// stored value says whether writing was granted too.
type key struct {
	kind  CapKind
	owner string
	ref   string
}

func (c Capability) key() key { return key{c.Kind, c.Owner, c.Ref} }

// Scope is everything one dashboard exposes, plus whether this viewer may act
// on it. A scope with Control false still answers reads: the dashboard shows
// its values, the buttons simply do not press.
type Scope struct {
	// DashboardID is the dashboard this scope came from. A plugin route keyed
	// by dashboard is told which one authorized the request, so it can check
	// that against what the request asks about.
	DashboardID string

	// Control is the dashboard's policy resolved for this viewer: an
	// identified viewer the owner shared with, or an anonymous one on a
	// dashboard whose author opted into public control.
	Control bool

	caps map[key]bool
	// topics are the topic patterns plugins declare for their own widgets,
	// already substituted for the placement they came from.
	topics []string
	// api maps a plugin name to the routes its widgets' viewers may reach on
	// its own API.
	api map[string][]apiRoute
}

// LiveTopic is the broker topic carrying a dashboard's runtime display state.
func LiveTopic(dashboardID string) string {
	return "dashboard:" + dashboardID + ":live"
}

// apiRoute is one route a plugin declares its widgets need: a method and a
// path pattern where * matches one segment and ** matches the rest.
type apiRoute struct {
	method  string
	pattern string
}

func newScope() *Scope {
	return &Scope{caps: make(map[key]bool), api: make(map[string][]apiRoute)}
}

// add records a capability, keeping a write grant once anything granted it.
func (s *Scope) add(c Capability) {
	k := c.key()
	s.caps[k] = s.caps[k] || c.Write
}

// Allows reports whether the scope covers a capability. A write additionally
// needs the dashboard's control policy, which is what keeps a public wall
// panel from being a public remote control.
func (s *Scope) Allows(c Capability) bool {
	if s == nil {
		return false
	}
	write, ok := s.caps[c.key()]
	if !ok {
		return false
	}
	if c.Write {
		return write && s.Control
	}
	return true
}

// AllowsCamera reports whether the scope shows one camera of an NVR
// connection, by name or through a grant of every camera.
func (s *Scope) AllowsCamera(connectionID, camera string) bool {
	return s.Allows(Capability{Kind: CapNVR, Owner: connectionID, Ref: camera}) ||
		s.Allows(Capability{Kind: CapNVR, Owner: connectionID, Ref: AllCameras})
}

// Cameras returns the cameras the scope shows on one NVR connection, with
// all true when it grants every camera.
func (s *Scope) Cameras(connectionID string) (cameras map[string]bool, all bool) {
	cameras = map[string]bool{}
	if s == nil {
		return cameras, false
	}
	for k := range s.caps {
		if k.kind != CapNVR || k.owner != connectionID {
			continue
		}
		if k.ref == AllCameras {
			all = true
			continue
		}
		cameras[k.ref] = true
	}
	return cameras, all
}

// Refs returns every reference of one kind the scope covers, which is what a
// list endpoint filters against so a dashboard grant never hands back the
// inventory of everything the server can see.
func (s *Scope) Refs(kind CapKind) map[string]bool {
	out := make(map[string]bool)
	if s == nil {
		return out
	}
	for k := range s.caps {
		if k.kind == kind {
			out[k.ref] = true
		}
	}
	return out
}

// AllowsTopic maps a broker topic back onto the capability it carries. The
// topics a dashboard needs are the live half of the same values its widgets
// already read over HTTP, so they are granted by the same bindings rather than
// by a second list that could drift from it.
func (s *Scope) AllowsTopic(topic string) bool {
	if s == nil {
		return false
	}

	switch {
	case strings.HasPrefix(topic, "control:"):
		return s.Allows(Capability{Kind: CapControl, Ref: strings.TrimPrefix(topic, "control:")})
	case strings.HasPrefix(topic, "mqttdevice:"):
		return s.Allows(Capability{Kind: CapDevice, Ref: strings.TrimPrefix(topic, "mqttdevice:")})
	case strings.HasPrefix(topic, "ha:"):
		// ha:{connection}:state:{entity}; an entity ID carries no colon.
		parts := strings.SplitN(strings.TrimPrefix(topic, "ha:"), ":", 3)
		if len(parts) != 3 || parts[1] != "state" {
			return false
		}
		return s.Allows(Capability{Kind: CapHA, Owner: parts[0], Ref: parts[2]})
	case strings.HasPrefix(topic, "events:subject:"):
		// events:subject:{kind}:{id}: a subject's event log entries, visible
		// where the subject itself is.
		kind, id, ok := strings.Cut(strings.TrimPrefix(topic, "events:subject:"), ":")
		if !ok {
			return false
		}
		switch kind {
		case "control":
			return s.Allows(Capability{Kind: CapControl, Ref: id})
		case "device":
			return s.Allows(Capability{Kind: CapDevice, Ref: id})
		case "ha_entity":
			conn, entity, ok := strings.Cut(id, "/")
			return ok && s.Allows(Capability{Kind: CapHA, Owner: conn, Ref: entity})
		}
		return false
	case strings.HasPrefix(topic, "nvr:"):
		// nvr:{connection}:events:{camera}, where camera * carries every
		// camera's events and needs a grant of every camera.
		parts := strings.SplitN(strings.TrimPrefix(topic, "nvr:"), ":", 3)
		if len(parts) != 3 || parts[1] != "events" {
			return false
		}
		return s.Allows(Capability{Kind: CapNVR, Owner: parts[0], Ref: parts[2]})
	}

	// The dashboard's own runtime display state is part of viewing it.
	if s.DashboardID != "" && topic == LiveTopic(s.DashboardID) {
		return true
	}

	// A plugin's own namespace, declared by the widget's manifest.
	for _, pattern := range s.topics {
		if mq.Match(pattern, topic) {
			return true
		}
	}
	return false
}

// AllowsPluginAPI reports whether a viewer may reach one route of a plugin's
// own API. The plugin declares the routes its widgets call, so the host never
// has to know what a plugin's routes mean — and a route it did not declare
// keeps whatever permission it was gated on, which is what stops a widget's
// grant from reaching an administrative endpoint next door.
//
// path is the suffix after /api/plugins/{name}, the same string the plugin
// routes against.
func (s *Scope) AllowsPluginAPI(plugin, method, path string) bool {
	if s == nil {
		return false
	}
	method = strings.ToUpper(method)
	for _, route := range s.api[plugin] {
		if route.method == method && matchPath(route.pattern, path) {
			return true
		}
	}
	return false
}

// matchPath compares a request path against a declared pattern segment by
// segment: * stands for one segment, ** for every remaining one.
func matchPath(pattern, path string) bool {
	pat := strings.Split(strings.Trim(pattern, "/"), "/")
	seg := strings.Split(strings.Trim(path, "/"), "/")

	for i, p := range pat {
		if p == "**" {
			return true
		}
		if i >= len(seg) {
			return false
		}
		if p != "*" && p != seg[i] {
			return false
		}
	}
	return len(pat) == len(seg)
}

// empty reports a scope that grants nothing, so a caller can skip work.
func (s *Scope) empty() bool {
	return s == nil || (len(s.caps) == 0 && len(s.topics) == 0 && len(s.api) == 0)
}
