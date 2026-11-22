// Package plugin defines how node sets are contributed to karakuri. A plugin
// is a unit of registration: it names itself with a manifest and registers
// its contributions, meaning node types, connection kinds, port types, and
// config field controls, against the Registrar the host hands it.
//
// The built-in node sets (core, mqtt, homeassistant) are plugins compiled
// into the binary and loaded at startup, so the registration surface is
// exercised by everything the app ships with. External plugins and remote
// agents are the same interface behind a process boundary.
//
// This package is only that surface. Loading, the subprocess manager, and the
// host-side callbacks live in pkg/pluginhost, which keeps this side free of
// the data model: a plugin main or an agent links pkg/plugin and pkg/nodeapi
// and no database driver.
package plugin

import (
	"context"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// Manifest identifies a plugin in the catalog.
type Manifest struct {
	// Name is the unique plugin identifier, e.g. "core".
	Name string `json:"name" description:"Unique plugin identifier."`
	// Version is a semver string, informational.
	Version string `json:"version,omitempty" description:"Plugin version."`
	// Description says what the plugin contributes.
	Description string `json:"description,omitempty" description:"What the plugin contributes."`
	// Author names who maintains the plugin.
	Author string `json:"author,omitempty" description:"Plugin author."`
	// Builtin marks a plugin compiled into the binary rather than loaded from
	// outside.
	Builtin bool `json:"builtin,omitempty" description:"Whether the plugin is compiled in."`
}

// Registrar is the surface a plugin registers its contributions against.
// Everything a node set needs is here; a plugin that only brings nodes calls
// one method and ignores the rest.
type Registrar interface {
	// RegisterNode adds a node type to the engine registry.
	RegisterNode(nt nodeapi.NodeType)
	// RegisterConnectionKind adds a connection driver kind: factory, config
	// schema, secrets, and validation/test hooks.
	RegisterConnectionKind(kind integrations.ConnectionKind)
	// RegisterPortType adds a payload type ports can declare. Compatibility,
	// labels, and colors all derive from the definition.
	RegisterPortType(def flow.PortTypeDef)
	// RegisterFieldControl adds a named config-field editor with a fallback
	// base type for frontends that do not implement it.
	RegisterFieldControl(def flow.FieldControlDef)
	// RegisterAIProvider adds a provider to the shared AI connection kind's
	// picker. The definition is data — which of the host's wire formats it
	// speaks and where — so no calls cross back into the plugin.
	RegisterAIProvider(def AIProviderDef)
}

// AIProviderDef describes one AI provider as data. Most providers are the
// chat-completions shape at a different address, which is why a plugin can
// contribute one without bringing code: the host's wires do the talking.
type AIProviderDef struct {
	// Name is the identifier a connection's provider field stores.
	Name string `json:"name"`
	// Label is the display name shown in the provider picker.
	Label string `json:"label"`
	// Wire names the host wire format: chat-completions, anthropic, or gemini.
	Wire string `json:"wire"`
	// BaseURL is the default endpoint; empty means the operator supplies one.
	BaseURL string `json:"base_url,omitempty"`
	// NeedsKey requires an API key in the config.
	NeedsKey bool `json:"needs_key,omitempty"`
	// SupportsTools allows models from this provider to receive tool
	// definitions and return tool calls.
	SupportsTools bool `json:"supports_tools,omitempty"`
	// LegacyMaxTokens sends max_tokens instead of max_completion_tokens on
	// the chat-completions wire.
	LegacyMaxTokens bool `json:"legacy_max_tokens,omitempty"`
	// AppendV1 appends /v1 to the base URL.
	AppendV1 bool `json:"append_v1,omitempty"`
}

// Plugin is one unit of registration.
type Plugin interface {
	// Manifest identifies the plugin.
	Manifest() Manifest
	// Register adds the plugin's contributions to the host. Called once at
	// load; an error rejects the plugin without unloading what it already
	// registered, so register fallible things first.
	Register(r Registrar) error
}

// The interfaces below are optional: a plugin implements the ones matching
// what it contributes, and the built-in node sets implement none. The host
// asks once per plugin and treats a missing implementation as "does not
// contribute this" rather than an error.

// Initializer runs once after registration with the host callback channel in
// hand. It is where a plugin reads its settings, registers permissions and
// providers, and subscribes to topics.
type Initializer interface {
	Init(ctx context.Context, host HostAPI) error
}

// Shutdowner releases whatever Init acquired.
type Shutdowner interface {
	Shutdown(ctx context.Context) error
}

// Configurable declares the plugin's settings as a JSON Schema. Every
// property carrying a default is seeded into the settings store the first
// time the plugin loads, so a fresh install starts with sane values.
type Configurable interface {
	SettingsSchema() string
}

// HTTPPlugin serves the plugin's own API. The host proxies every request
// under /api/plugins/{name}/ to it, with the prefix already stripped.
type HTTPPlugin interface {
	HandleHTTP(ctx context.Context, req *HTTPRequestParams) (*HTTPResponseResult, error)
}

// OpenAPIPlugin reports the plugin's API as an OpenAPI document so its routes
// appear in the host's published spec.
type OpenAPIPlugin interface {
	OpenAPISpec() string
}

// MessagePlugin receives broker messages on the topics the plugin subscribed
// to. Subscription-count messages are how a plugin learns something is
// watching a topic and starts producing for it.
type MessagePlugin interface {
	HandleMessage(ctx context.Context, topic, msgType string, payload []byte) error
}

// HTTPRequestParams is one proxied HTTP request. Path is the suffix after
// /api/plugins/{name}, so a plugin routes within its own namespace. UserID and
// Username are empty for an anonymous request; the host resolved them already.
type HTTPRequestParams struct {
	Method      string
	Path        string
	Headers     map[string]string
	Body        []byte
	QueryString string
	RemoteAddr  string
	UserID      string
	Username    string
	// DashboardGrant is the ID of the dashboard the host authorized this
	// request against, empty when it authorized none. That dashboard's deployed
	// layout holds one of this plugin's widgets and the manifest names this
	// route, which is what lets a plugin route answer a viewer holding no
	// plugin permission. It is not a header: the host forwards a client's
	// headers verbatim, and a claim the client could write would not be a
	// grant.
	//
	// A plugin route keyed by dashboard must check this against its own path
	// parameter. The grant names the dashboard that authorized the request; it
	// does not say that dashboard is the one the request asks about.
	DashboardGrant string
	// KeyLimited says the caller authenticated with an API key limited to
	// KeyPermissions. The host's permission check answers for the user, so a
	// plugin must also find the permission among these.
	KeyLimited     bool
	KeyPermissions []string
}

// HTTPResponseResult is a plugin's answer to a proxied request.
type HTTPResponseResult struct {
	StatusCode int
	Headers    map[string]string
	Body       []byte
}

// RoleDefault grants a permission to named roles, or to every role, the first
// time a plugin loads. Existing grants are left alone.
type RoleDefault struct {
	Permission string
	Roles      []string
	AllRoles   bool
}

// HostAPI is what a plugin calls back on the host. Unlike the ExecContext a
// node runs against, these are not scoped to a run: the host knows which
// plugin is calling and tags every registration with its name, so unloading
// reverses exactly what that plugin added.
type HostAPI interface {
	// GetConfig reads a setting; found is false when the key is unset.
	GetConfig(ctx context.Context, key string) (value string, found bool, err error)
	SetConfig(ctx context.Context, key, value string) error

	// Publish sends a message onto the broker; Subscribe asks the host to
	// deliver a topic's messages back through HandleMessage.
	Publish(ctx context.Context, topic, msgType string, payload []byte) error
	Subscribe(ctx context.Context, topic string) error
	Unsubscribe(ctx context.Context, topic string) error

	RegisterPermission(ctx context.Context, permission string, actions []string, description string) error
	UnregisterPermission(ctx context.Context, permission string) error
	SeedRoleDefaults(ctx context.Context, defaults []RoleDefault) error
	CheckPermission(ctx context.Context, userID, permission string) (bool, error)
	// CheckDashboardAccess answers whether a user may act on one dashboard at
	// the given level: read to see it, view to also operate it, edit, or
	// admin. Each level includes the ones below it. A plugin storing per-widget
	// configuration needs this: a dashboard permission is not enough, since it
	// would let anyone with it read another dashboard's stored credentials.
	CheckDashboardAccess(ctx context.Context, userID, dashboardID, level string) (bool, error)
	// CheckConnectionAccess answers whether a user may use one connection: a
	// plugin that lists host connections or stores a reference to one offers
	// only the connections the user was shared.
	CheckConnectionAccess(ctx context.Context, userID, connectionID string) (bool, error)

	// RegisterProvider advertises a typed service endpoint a widget can point
	// at, e.g. type "weather", name "openmeteo".
	RegisterProvider(ctx context.Context, providerType, name, endpoint, displayName string) error
	UnregisterProvider(ctx context.Context, providerType, name string) error

	// RegisterTopicPermission gates WebSocket subscription to topics matching
	// a pattern behind a permission.
	RegisterTopicPermission(ctx context.Context, pattern, permission string) error
	UnregisterTopicPermission(ctx context.Context, pattern string) error

	// ConnectionAccessToken returns a current access token from a host
	// connection that issues them, such as an oauth2 connection. A plugin
	// process cannot hold a host driver, so it signs requests this way.
	ConnectionAccessToken(ctx context.Context, connectionID string) (string, error)
}

// NodeRegistry adapts a Registrar to the nodeapi.NodeRegistrar interface, so
// a plugin can hand it to a package's RegisterAll helper.
type NodeRegistry struct{ R Registrar }

// Register adds a node type through the wrapped Registrar.
func (n NodeRegistry) Register(nt nodeapi.NodeType) { n.R.RegisterNode(nt) }

// MaxMessageBytes bounds one message on the host-plugin and host-agent wires
// in both directions. gRPC's default of 4 MiB refused a node input or an
// execution result past that, which a file read or a command's output reaches,
// with nothing in the docs saying so.
const MaxMessageBytes = 128 << 20
