# Plugins

A plugin is one directory with a `manifest.yaml` and whichever contributions it
makes. Every contribution is optional. The weather plugin brings nodes, a widget,
settings, an HTTP API, a provider, and permissions; the clocks plugin brings three
widgets and no Go code at all.

| Contribution | Declared by | What it provides |
|---|---|---|
| Node types | `Registrar.RegisterNode` | Palette entries that validate, retry, and test-run like built-in nodes |
| Connection kinds | `Registrar.RegisterConnectionKind` | A connection type with its settings form, driver, validation, and test |
| Port types | `Registrar.RegisterPortType` | Edge compatibility, labels, colors, and value parameters |
| Field controls | `Registrar.RegisterFieldControl` | Config editors, with a fallback editor where the frontend lacks one |
| AI providers | `Registrar.RegisterAIProvider` | An entry in the AI connection's provider picker |
| Dashboard widgets | `widgets:` in `manifest.yaml` | Entries in the widget picker, served at `/widgets/{package}/` |
| What a widget exposes | `scope:` under a widget in `manifest.yaml` | What a dashboard's viewers may read and press through the widget |
| Settings | `Configurable.SettingsSchema` | A settings panel, stored as `plugins.{name}.{key}` |
| An HTTP API | `HTTPPlugin.HandleHTTP` | Routes under `/api/plugins/{name}/*` |
| Providers | `HostAPI.RegisterProvider` | A typed endpoint listed at `/api/providers/{type}` |
| Permissions | `HostAPI.RegisterPermission` | Permissions for roles and API keys |

An AI provider is data only. The definition names one of the host's wire formats
(`chat-completions`, `anthropic`, or `gemini`), a default endpoint, and whether its
models accept tools. No calls cross back into the plugin.

A plugin with more than one widget should give each its own `description`.
Otherwise they all inherit the plugin's and read identically in the picker.

Manage plugins under **Settings, Plugins** or at `GET /api/plugins`, gated on
`api:plugins:read` and `api:plugins:write`. The built-in node sets (core, MQTT,
Home Assistant) appear there too, read-only, because they are compiled into the
server and cannot be unloaded.

## Layout

    plugins/weather/
      manifest.yaml
      main.go nodes.go handler.go openmeteo.go ...     # the executable
      widgets/weather/{package.json,src/,dist/}        # the widget it serves

    plugins/clocks/
      manifest.yaml                                    # widget-only: no Go
      widgets/{analog-clock,binary-clock,fuzzy-clock}/

```yaml
name: "weather"
version: "0.1.0"
description: "Weather forecasts for dashboards and flows."
author: "James Coleman"

# Optional: the executable contributing nodes, an API, and settings.
entryPoint: "weather"

# Optional: ship disabled until an operator enables it, for plugins most
# installs have no use for. Read only the first time the plugin is seen;
# the enable choice stored from the settings panel wins afterwards.
defaultDisabled: true

# Optional: widget packages. path defaults to widgets/<packageName>,
# entryPoint to index.js, and version/author/description to the plugin's own.
widgets:
  - name: "Weather"
    packageName: "weather"
    tagName: "weather-widget"
    configTagName: "weather-config"
    tags: ["weather", "forecast"]
    defaultProvider: "openmeteo"
```

Widget declarations may include `configSchema` (a JSON Schema written as a YAML
object) and `defaultConfig` (a YAML object). `list_widget_types` shows both,
and `validate_dashboard` checks against them. Validation overlays stored config
fields on the defaults without changing either. Local schema references work,
and external schemas are never fetched. `validate_dashboard` reports packages
without schemas as unchecked for config validation.

Widget sources live at `plugins/<name>/widgets/<package>/`, so a manifest rarely
needs a `path`. `packageName` is the widget's URL and its identity in stored
dashboards. A plugin claiming a package name another plugin already registered
is refused.

The server also refuses an AI provider name another plugin holds. Node types,
connection kinds, port types, and field controls are instead replaced by name,
so a second registration silently overwrites the first. Choose names that will
not collide with another plugin's.

A plugin directory named after a built-in (`plugins/core/`,
`plugins/homeassistant/`) holds a manifest with `widgets:` and no `entryPoint`.
Its widgets attach to the built-in's catalog entry and cannot be disabled apart
from it. Name a directory after a built-in only for that purpose. Anything else
there is an error.

The server serves widget bundles from the plugin directory instead of compiling
them into its binary, so a missing plugin directory means missing widgets.

## Declaring a widget's scope

Dashboard viewers often hold no permissions of their own. A widget's `scope`
tells the host what placing the widget on a dashboard lets that dashboard's
viewers reach. A widget with no `scope` reads nothing from the host and grants
nothing, which is right for a clock or an image.

The grant comes from the dashboard's deployed layout. A viewer may read what
the widgets placed on it bind, and nothing else. A placement of a widget whose
plugin is unloaded grants nothing.

### Bindings

`bindings` names the config field that holds host bindings, the
`{source, connectionId, entityId, controlId, deviceId, flowId, trigger,
displayOnly, triggers}` shape that points at a Home Assistant entity, a
control, an MQTT device, or a flow trigger. The field may hold one binding
object or an array of them. `"."` means the whole config is one binding.

```yaml
  - name: "Entities Card"
    packageName: "entities-card"
    scope:
      bindings: "rows"       # the field holding them
  - name: "Entity Control"
    packageName: "entity-control"
    scope:
      bindings: "."          # the config is itself one binding
```

The host reads only that shape from a widget's config. What each source lets a
viewer read and press is described in
[What each binding lets viewers read and operate](dashboard-security.md#what-each-binding-lets-viewers-read-and-operate).

### Topics and API routes

Data that comes from the plugin itself, such as a forecast, is not a host
binding. Declare the topics the widget subscribes to and the plugin routes it
calls:

```yaml
    scope:
      topics:
        - "weather:forecast:*"
      api:
        - "GET /*/forecast/*"
```

A topic pattern uses `:` as the segment separator, and `*` matches one or more
segments. It may contain `{dashboard}` and `{widget}`, which the host replaces
with the placement's dashboard ID and placement ID. A topic keyed by them,
such as `myplugin:{dashboard}:{widget}:data`, grants each placement exactly its
own topic. A topic left as a namespace grants every topic under it, so a viewer
of a dashboard with the weather widget can read forecasts for any location.

An `api` entry is `METHOD /path/pattern`. The path is the part after
`/api/plugins/{name}`. `*` matches one path segment and `**` matches all the
remaining segments. It may contain `{dashboard}` and `{widget}` as well, so a
route such as `GET /{dashboard}/{widget}/data` opens each placement's own data
and nothing else. The host passes the dashboard it authorized the request
through to the plugin, which `apiutil.PluginDashboardGrant` reads. A route keyed
by dashboard should compare it with the dashboard in its path. The host ignores an entry missing the method or the path
instead of widening it. Name only the routes the widget calls. Weather declares its
forecast route and leaves out the geocoding routes beside it, which only its
config panel uses, so a viewer cannot reach them through the dashboard.

Every topic the plugin publishes also needs a WebSocket gate. Register one in
`Init` with `HostAPI.RegisterTopicPermission`. A topic that matches no
registered gate is denied to everyone, so a plugin that publishes on an
unregistered namespace finds its own widget cut off. The `scope` entry then lets
viewers who lack that permission subscribe through the dashboard.

Keep secrets out of a widget's config. The host serves it to every viewer of
the dashboard. Store credentials in the plugin's own settings and return them only through
routes that check edit access with `CheckDashboardAccess`.

## Load and unload

Loading a plugin applies its contributions. A widget-only plugin has no process
to start, and enable and disable work the same way for both kinds.

| Contribution | On load | On unload |
|---|---|---|
| Executable | Start the process, register what it contributes, serve settings, HTTP, and messaging | Stop the process; withdraw its nodes, connection kinds, permissions, providers, and topic gates; drop its subscriptions |
| Widgets | Add them to the picker and serve them | Stop serving them; existing placements keep their metadata |

Port types and field controls stay registered after unload. Removing a port type
would shift the color of every type registered after it.

Disabling a plugin is how a widget is disabled. There is no separate widget
toggle. `GET /widgets` returns the packages a loaded plugin can serve, which is
what the dashboard picker offers, and the list cannot be edited through the API.
A placement of an unloaded widget keeps its config and works again when the
plugin returns.

The enable choice survives restarts, and the panel shows the last load result
for a plugin that is not running. When a plugin directory is deleted, the next
scan removes its state, settings, and widget entries. A plugin that failed
to load keeps all three.

## Writing a plugin

```go
type myPlugin struct{}

func (myPlugin) Manifest() plugin.Manifest {
    return plugin.Manifest{Name: "zigbee", Description: "Zigbee nodes and coordinator connection."}
}

func (myPlugin) Register(r plugin.Registrar) error {
    r.RegisterNode(MyTriggerNode{})
    r.RegisterNode(MyActionNode{})
    r.RegisterConnectionKind(integrations.ConnectionKind{
        Kind: "zigbee", Label: "Zigbee",
        ConfigSpec: []flow.FieldSpec{
            {ID: "serial_port", Label: "Serial port", Type: flow.FieldString, Required: true},
            {ID: "network_key", Label: "Network key", Type: flow.FieldString, Control: "password"},
        },
        Secrets: []string{"network_key"},
        Factory: newDriver,
        Test:    testConnection,
    })
    return nil
}

func main() { sdk.Serve(myPlugin{}) }
```

Nodes implement `engine.NodeType` exactly like the built-ins: `Meta()` returns
the `flow.NodeMeta` (folder, ports, config spec, behavior flags) and `Execute`
runs against `engine.ExecContext`. A trigger also implements
`engine.TriggerType`.

## Connection kinds

`integrations.ConnectionKind` carries the config schema (`[]flow.FieldSpec`),
the fields to mask (`Secrets`), the driver `Factory`, and optional `Validate`,
`Test`, and `Probe` hooks. `Probe` tests like `Test` and also returns a short
description of what it found, such as the calendars an account holds, which the
connection test shows beside its result. When a kind has both, tests call
`Probe`. The server validates required fields from the schema, and the settings
page renders the form from it, so a new kind needs no frontend changes.

## Port types

`flow.PortTypeDef` is data with these fields:

- `Name` and `Label`.
- `WidensTo`, directional assignability. Integer widens to float and number.
- `Value`, usable as a subflow value parameter.
- `Script`, declarable on a script node's ports. The data types allow it, and
  a capability type like the AI plugin's `ai-tool` does not.

Compatibility checks, editor labels, tooltips, script port pickers, and
edge and socket colors all come from the registered types, listed at
`GET /api/nodetypes/porttypes`. Colors follow registration order, so add new
types after existing ones and never reorder them.

## Field controls

A config field's `Type` is its datatype, which validation checks and a value
edge binds to. A field with `Port` set is a **port field**, a permanent value
input port of that port type instead of an editable setting, supplied only by
value edges. `Multi` lets several wires feed it as an array, as with the
AI agent's model and tools inputs.

`Control` names the editor a normal field renders with, referencing a
`flow.FieldControlDef` listed at `GET /api/nodetypes/controls`. Each control
declares a `Base` type. A frontend that does not implement the control renders
the base type's editor instead, so a custom control stays editable. The
frontend implements `password`, `list` (string array), `map` (key/value object),
and `tokens` (a comma-separated string edited as chips; the field's options are
the vocabulary, and a field with no options takes typed entries) beyond the
type-default editors.

## External plugin processes

An external plugin is a separate executable that implements the same
`plugin.Plugin` interface a built-in does. Its `main` calls `sdk.Serve` from
`pkg/plugin/sdk`, which runs nodes, triggers, and drivers inside the plugin
process and talks to the server over gRPC. A plugin node validates, retries,
pulls values, and test-runs exactly like a compiled-in one.

The server looks for plugin directories in `plugins.paths` from the config.
Unset, it searches `plugins/` beside the binary, then `/etc/karakuri` and
`/usr/share/karakuri`. An explicit empty list disables plugins entirely.

Behavior to know when writing an external plugin:

- **Execute.** The node receives its merged config, resolved ports, and inputs,
  and returns outputs. Binary payloads survive the trip. A node error is
  reported as the node's failure, separate from a lost plugin process. One
  message in either direction is limited to 128 MiB.
- **Callbacks.** Run state, variables in every scope, expressions, Debug, Emit,
  and Park all work through `ExecContext` as they do for built-ins. The clock is
  the server's. `NodeStore.Update` may run its function more than once when
  another update lands at the same time, so keep it free of side effects.
- **Missed events.** An armed trigger can record an event it chose not to run,
  such as an occurrence missed while the server was down, through
  `engine.TriggerRecorder`. The run list shows a canceled run with the reason
  and the event's payload.
- **Waits.** A plugin node that parks keeps its wait in memory. A server
  restart or a redeploy ends it, unlike the built-in timer and fan-in nodes,
  whose waits survive restarts.
- **Triggers.** Each emitted message starts a run. The trigger is disarmed when
  its context is canceled.
- **Drivers.** A plugin's connection kind builds its drivers inside the plugin
  process, and the connection shows the status they report. If the plugin
  process exits, its connections report an error. A plugin node's
  `Connection(id)` resolves only connections of kinds its own plugin
  registered; a plugin cannot use another plugin's or the server's drivers.
- **OAuth tokens.** `HostAPI.ConnectionAccessToken` returns a current access
  token from a server oauth2 connection. It refuses connections that are
  stopped or do not issue tokens.

The server logs and skips a plugin directory it cannot use at startup, such as
one whose manifest does not parse or names an entry point that is not on disk.
The plugins beside it load as usual. While any directory is refused, nothing is
removed for plugins missing from the scan, since the refused directory may be
theirs.

`make dev` builds every `plugins/<name>/` into `build/plugins/<name>/`: the Go
executable when there is a `main.go`, each `widgets/<package>/` bundle, and the
manifest. To install a plugin elsewhere, copy that directory into one of the
plugin paths. `plugins/weather/` is a complete example with nodes, a widget,
settings, an API, and scope declarations.

## Settings, HTTP, and messaging

A plugin that does more than contribute nodes implements optional interfaces
alongside `plugin.Plugin`. A missing interface means the plugin does not
contribute that thing.

| Interface | Purpose |
|---|---|
| `Initializer` | `Init(ctx, host)`: read settings and register everything that is not a node type |
| `Shutdowner` | Release what `Init` acquired |
| `Configurable` | A JSON Schema describing the plugin's settings |
| `HTTPPlugin` | Serve the plugin's own API |
| `OpenAPIPlugin` | Publish those routes in the server's API document |
| `MessagePlugin` | Receive broker messages on subscribed topics |

**Settings** are read once, at `Init`, so saving them reloads the plugin. Every
schema property with a `default` is stored on first load, so a fresh install
starts configured. Keys are namespaced as `plugins.{name}.{key}`, so a plugin
asks for `api_key` and cannot read or overwrite another plugin's settings. A
`"format": "password"` property renders masked.

**The HTTP API** is served at `/api/plugins/{name}/*` with the prefix removed,
so a plugin routes within its own namespace. The mount does not require
authentication; each route decides who may reach it (see
[Securing plugin routes](#securing-plugin-routes)). `pkg/plugin/sdk/httpsdk`
carries the boilerplate: `NewService` (an OpenAPI service using the server's
error format), `ServeHTTP` (adapts a proxied request to the service),
`RequirePermission`, `Allowed`, and `OpenAPISpec`. A plugin that only
contributes nodes does not need to import it.

```go
func (p *WeatherPlugin) HandleHTTP(_ context.Context, req *plugin.HTTPRequestParams) (*plugin.HTTPResponseResult, error) {
    return httpsdk.ServeHTTP(p.service, req)
}

func (p *WeatherPlugin) OpenAPISpec() string { return httpsdk.OpenAPISpec(p.service) }
```

**`plugin.HostAPI`** is what `Init` receives:

- `GetConfig`/`SetConfig`: the plugin's own settings namespace.
- `Publish`/`Subscribe`/`Unsubscribe`: the message broker. Subscribing to
  `topic:<pattern>:count` tells a plugin how many dashboards watch a topic; the
  weather, crypto, and stock plugins start polling when it rises above zero and
  stop when the last viewer leaves. Messages arrive one at a time in publish
  order, so the last count handled is the current one.
- `RegisterPermission`/`UnregisterPermission`/`SeedRoleDefaults`/`CheckPermission`:
  define the plugin's permissions and give default roles access.
- `RegisterProvider`/`UnregisterProvider`: advertise a typed endpoint
  (`weather`/`openmeteo`) that a widget's config panel discovers through
  `/api/providers/{type}`.
- `RegisterTopicPermission`/`UnregisterTopicPermission`: gate WebSocket
  subscription to a topic pattern behind a permission.
- `CheckDashboardAccess`: ask whether a user may act on one dashboard at a
  level: `read` to see it, `view` to also operate it, `edit`, or `admin`. Each
  level includes the ones below it. Ask for `read` to serve data a viewer
  watches; `view` refuses a user the dashboard was shared with read-only. A
  plugin storing per-widget configuration needs this rather than a blanket
  dashboard permission, which would let anyone holding it read another
  dashboard's stored credentials.
- `ConnectionAccessToken`: a current token from a server oauth2 connection.

## Securing plugin routes

The server authenticates the caller before forwarding a request, and passes the
result with it. Read it with `apiutil.PluginUserID(r)` and
`apiutil.PluginUsername(r)`; both are empty for an anonymous caller.

`httpsdk.RequirePermission(host, permission)` gates a route on a permission:

```go
s.With(httpsdk.RequirePermission(host, "api:weather:read")).
    Method(http.MethodGet, "/{provider}/forecast/{lat}:{lon}",
        nethttp.NewHandler(p.forecastInteractor()))
```

It answers 401 to an anonymous caller and 403 to a user without the permission.
A request made with an API key limited to a set of permissions must also list
that permission in the key, so a key cannot reach more through a plugin than it
can through the server's own API.

To check a permission inside a handler, call `httpsdk.Allowed(host, r,
permission)`. It applies the same user and API key checks. Do not call
`host.CheckPermission` directly for a request; it checks only the user and
ignores the key's limit.

A request that matches an `api` route in a widget's `scope` arrives with a
dashboard grant, the ID of the dashboard whose viewer made it. Read it with
`apiutil.PluginDashboardGrant(r)`. `RequirePermission` admits a request carrying
a grant without asking for the permission, since a dashboard's viewers usually
hold none. The client cannot forge the grant, because the server passes it
separately from the request headers.

The grant names which dashboard authorized the request. A route keyed by
dashboard, such as `/{dashboardID}/{widgetID}/data`, must compare
`apiutil.PluginDashboardGrant(r)` with its own dashboard path parameter and
refuse a mismatch. Without that check, a viewer of one public dashboard could ask
the route about a private one.

```go
func (p *myPlugin) widgetData(w http.ResponseWriter, r *http.Request) {
    dashboardID := chi.URLParam(r, "dashboardID")
    if grant := apiutil.PluginDashboardGrant(r); grant != "" && grant != dashboardID {
        http.Error(w, "forbidden", http.StatusForbidden)
        return
    }
    // Without a grant, the caller is a signed-in user: check access with
    // host.CheckDashboardAccess(r.Context(), apiutil.PluginUserID(r), dashboardID, "read").
}
```
