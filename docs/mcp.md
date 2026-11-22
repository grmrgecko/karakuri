# MCP

Karakuri serves the Model Context Protocol at `/mcp`, so an AI agent (Claude
Code, or any other MCP client) can operate the instance: author and deploy
flows, run tests and read per-node failures, manage controls, connections, MQTT
devices, and dashboards, and read the built-in guides. MCP tools follow the same
rules and checks as the web interface and REST API, so both give the same
answers.

The endpoint uses stateless streamable HTTP. Every request is an independent
POST, there is no session header, and the server never sends messages on its
own. Set `mcp.enabled: false` in the configuration to remove the endpoint.

## Authentication and permissions

`/mcp` accepts the same bearer tokens as `/api`: a session token or an API key.
Three checks decide what a caller may do.

1. **Using MCP at all** requires the `api:mcp:use` permission. Only
   administrators hold it by default, through their `api:*` grant. Anyone else
   needs a role carrying `api:mcp:use` assigned to them or their group, so that
   role decides which groups may drive the server over MCP.
2. **Each tool** requires the same `api:*` permission as the matching REST
   endpoint: `list_flows` needs `api:flows:read`, `deploy_flow` needs
   `api:flows:deploy`, `set_control` needs `api:controls:set`, and so on. A
   refusal names the missing permission.
3. **Each flow** also checks the caller's access level on that flow, under the
   [ownership and sharing](flow-authoring.md#ownership-and-sharing) rules. Reads
   need view, changes and runs need edit, and deletion needs admin.
   `list_flows`, `list_active_runs`, `find_references`, and `list_events` cover
   only flows the caller may read. `get_document_dependencies` reports a
   referenced flow the caller may not read as unchecked, without its name.
4. **Each connection and agent** checks that it is shared with the caller, as
   [connections and agents](#connections-and-agents) describes. A tool naming
   one the caller cannot use answers that it was not found, and the lists,
   node types, references, and dependencies leave it out.

### Connections and agents

Whoever adds a connection, or mints the token an agent enrolls with, owns it.
The owner shares it with people or groups from its share button in
**Settings > Connections** or **Settings > Agents**, or with
`POST /api/connections/{id}/shares` or `POST /api/agents/{id}/shares` and
`{"username": "...", "permission": "use"}` or `{"group_name": "...", ...}`:

| Level | Allows |
|---|---|
| `use` | Seeing it, using it in flows, dashboards, devices, history, energy, and memory stores, invoking it, and running an agent's nodes. |
| `edit` | Also changing and testing a connection, or renaming, enabling, and disabling an agent. |
| `admin` | Also sharing and deleting it, and revoking an agent. |

Holders of `api:connections:manage` or `api:agents:manage` hold admin on every
connection or agent; the Admin role has both through `api:*`. The role
permissions still decide what a user may do with connections and agents at all,
and the share decides which ones. To let ordinary users add their own, grant
their role `api:connections:write` or `api:agents:enroll`.

For an agent, set up a dedicated user in its own group, a role granting
`api:mcp:use` plus exactly the `api:*` permissions the agent should have, and an
API key created for that user. The key is the one secret the agent's MCP
configuration needs. For local development, create or retrieve an admin token
from `build/`:

```sh
./karakuri token list admin --name=mcp --raw
./karakuri token create admin mcp   # when the list is empty
```

The create command prints the bearer token. The list command can show keys
created by recent versions. Replace a key it cannot show with a new one.

### Limiting a key

An API key can be limited to a set of permissions, written the way roles write
them, wildcards included. Choose **Settings, API keys, Create key, Only the
permissions I choose**, where paths complete as they do in a role, or send
`permissions` to `POST /api/api_keys`. A limited key passes a permission check
only when its user's roles grant the permission and one of the key's entries
matches it, so it never holds more than its user and loses whatever a role
loses. The limit applies everywhere: REST routes, MCP tools, plugin routes, and
the manage permissions that open every flow, dashboard, and event. An
administrator's key limited to `api:mcp:use` and `api:flows:read` therefore
reads only the flows the administrator owns or was shared, not every flow
through `api:flows:manage`. Ownership and shares need no permission and still
apply.

You cannot give a key a permission you do not hold yourself, and a request made
with a limited key can only create keys limited to what that key holds. A key
without a limit holds all of its user's permissions.
`GET /api/api_keys/permissions` lists the permissions you may give a key.

## Connecting

```sh
claude mcp add --transport http karakuri http://localhost:8080/mcp \
  --header "Authorization: Bearer kk_..."
```

Any client that speaks streamable HTTP works the same way: point it at `/mcp`
and have it send the bearer header. Clients running in a web browser are not
supported.

## Tools

Each tool's JSON schema, listed through `tools/list`, describes its inputs and
outputs. The groups, and the permission each checks:

| Group | Tools | Permission |
|---|---|---|
| Flows | `list_flows`, `get_flow`, `create_flow`, `update_flow`, `delete_flow`, `save_flow_graph`, `patch_flow_graph`, `diff_flow`, `validate_flow_graph`, `list_flow_versions`, `restore_flow_version`, `export_flow`, `import_flow` | `api:flows:read` / `write` |
| | `deploy_flow`, `set_flow_enabled` | `api:flows:deploy` |
| Nodes | `list_flow_nodes`, `get_flow_node`, `list_flow_edges` | `api:flows:read` |
| Runs | `run_flow_test`, `test_flow_node`, `test_node` | `api:flows:write` |
| | `get_flow_run`, `list_flow_runs`, `list_active_runs` | `api:runs:read` |
| | `cancel_run` | `api:runs:cancel` |
| Catalog | `list_node_types`, `get_node_type`, `list_port_types`, `preview_schedule` | `api:flows:read` |
| Controls | `list_controls`, `save_control`, `delete_control`, `set_control` | `api:controls:read` / `write` / `set` |
| | `list_control_kinds` | signed in |
| Connections | `list_connections`, `list_connection_kinds`, `get_ha_states`, `get_ha_registry`, `list_ha_services`, `list_openhab_items`, `capture_mqtt_messages` | `api:connections:read` |
| | `save_connection`, `delete_connection`, `test_connection` | `api:connections:write` |
| | `call_ha_service`, `publish_mqtt` | `api:connections:invoke` |
| Time series | `query_timeseries`, `list_timeseries_metadata`, `list_history_forwards`, `get_history_status` | `api:connections:read` |
| | `save_history_forward`, `delete_history_forward` | `api:connections:write` |
| Charts | `get_chart_data` | dashboard view access; editors read the draft |
| MQTT devices | `list_mqtt_devices` | `api:connections:read` |
| | `save_mqtt_device`, `delete_mqtt_device`, `test_mqtt_device_script`, `discover_zigbee2mqtt_devices`, `add_zigbee2mqtt_devices`, `regenerate_mqtt_device` | `api:connections:write` |
| | `command_mqtt_device`, `call_esphome_action`, `send_openhab_command` | `api:connections:invoke` |
| | `discover_esphome_nodes`, `discover_esphome_entities`, `add_esphome_entities`, `discover_entities`, `add_entities`, `discover_mqtt_discovery_devices`, `add_mqtt_discovery_devices` | `api:connections:write` |
| | `get_auto_import_status` | `api:connections:read` |
| NVR | `list_nvr_cameras`, `list_nvr_events`, `get_nvr_snapshot` | `api:connections:read` |
| Energy | `list_energy_sources`, `list_energy_candidates`, `list_energy_groups`, `list_energy_tariffs`, `get_energy_rollups`, `get_energy_price`, `find_cheapest_energy_window` | `api:connections:read` |
| | `save_energy_source`, `delete_energy_source`, `import_energy_history`, `save_energy_group`, `delete_energy_group`, `save_energy_tariff`, `delete_energy_tariff` | `api:connections:write` |
| | `create_energy_dashboard` | `api:dashboards:write` |
| Notifications | `list_notification_services`, `list_notification_endpoints` | `api:connections:read` |
| | `send_notification` | `api:connections:invoke` |
| Events | `list_events`, `get_event_settings`; security entries need `api:events:audit` | `api:events:read` |
| Backups | `list_backups`, `get_backup_schedule` | `api:backups:read` |
| | `create_backup`, `verify_backup`, `delete_backup`, `save_backup_schedule` | `api:backups:write` |
| Dashboards | `create_dashboard`, `import_dashboard` | `api:dashboards:write` |
| | `update_dashboard`, widget placement, `add_dashboard_section`, `update_dashboard_section`, `remove_dashboard_section`, `add_dashboard_page`, `update_dashboard_page`, `remove_dashboard_page`, `export_dashboard`, `deploy_dashboard`, `list_dashboard_versions`, `restore_dashboard_version` | owner, an edit share, or `api:dashboards:manage` |
| Live dashboards | `get_dashboard_live`, `command_dashboard`, `resolve_dashboard_variables` | owner, an edit share, or `api:dashboards:manage` |
| | `delete_dashboard` | owner, an admin share, or `api:dashboards:manage` |
| | `list_dashboards`, `get_dashboard`, `list_widget_types`, `list_themes` | signed in; the accessible set |
| Dashboard inspection | `diff_dashboard`, `validate_dashboard` | edit access |
| Faceplates | `get_flow_faceplates`, `apply_flow_faceplates` | faceplate access to the flow or higher; see [faceplates](flow-authoring.md#faceplates) |
| Dependencies | `get_document_dependencies` | flow read or dashboard edit; referenced subsystems checked separately |
| Reference search | `find_references` | referenced subsystem read; flow read / dashboard edit |
| Persisted state | `inspect_flow_state` | `api:flows:read` and `api:runs:read` |
| Pending waits | `cancel_flow_wait` | `api:runs:cancel` |
| Overview | `get_attention`, `list_upcoming`, `list_unfinished_work`; see [home.md](home.md) | signed in; each source checks its own permission |
| Agents | `list_agents`, `get_agent_status` | `api:agents:read` |
| Dashboard previews | `render_dashboard` | edit access for draft; view access for deployed |
| Plugins | `list_plugins`, `get_plugin_settings` | `api:plugins:read` |
| | `set_plugin_enabled`, `update_plugin_settings` | `api:plugins:write` |
| Guides | `read_guide` | signed in |
| Scripts | `test_script` | `api:flows:write` |

[timeseries.md](timeseries.md) describes time-series queries, history forwards,
and chart data. `query_timeseries` returns typed series with
times in Unix milliseconds.

Graph tools send and return graphs in the flow graph JSON format. The
`karakuri://guides/flow-authoring` resource documents it, and the server tells
agents to read it before writing a graph. The usual authoring loop is
`save_flow_graph`, `validate_flow_graph` with `strict`, `run_flow_test`, then
`get_flow_run` for the result of each node.

`get_flow_faceplates` lists a flow's faceplate tabs, controls, and values, and
`apply_flow_faceplates` applies values and deploys, under the same rules as the
faceplate tabs in [the flow guide](flow-authoring.md#faceplates). To build a
faceplate, add a `faceplate` node with the graph tools. Editor chat on a flow
may use both.

## Resources

Every guide in the app's **Help** pages is also a markdown resource at
`karakuri://guides/<name>`, where the name is the guide's address under `/help`,
such as `karakuri://guides/flow-authoring`. `read_guide` returns the same markdown
by name.

## What is deliberately absent

User, group, role, and API key administration work only through REST and the
web interface, so an agent cannot change its own permissions. There is
no rerun tool; use `run_flow_test` with a `payload` override to replay a run.
Agent enrollment, revocation, and updates, and render schedules, are also not
available over MCP. Agent status and capabilities can be read.

## Editor chat

Kuri, the editor's AI assistant, uses these same tools with the signed-in
author's identity and permissions. It can inspect, edit, deploy, restore, and
test-run the selected document. It does not need
`api:mcp:use`, because it is part of the signed-in editor, and it keeps working
when `mcp.enabled` is false; that setting only turns off the external `/mcp`
endpoint. See [Editing dashboards and flows with
AI](ai.md#editing-dashboards-and-flows-with-ai) for using it.

Widget and section changes must name a placement on the specified dashboard.
`update_dashboard` accepts `theme_id` from `list_themes`; zero clears the theme.
Editor chat can read a dashboard's live state with `get_dashboard_live`. It can
call `command_dashboard` only with **Allow deployment and execution** on,
because a command reaches every open viewer.

## Shared drafts and deployment

REST, MCP, and editor chat all edit the same stored draft. After each change,
editors that have the document open reload it, as they do after reconnecting.
Only users who may read the flow, or edit the dashboard, are notified.

Tools that return a flow report the caller's `access_level` and leave out the
folder when the caller cannot see it, as `list_flows` does.

`get_flow` returns `draft_revision`. Pass it as `expected_revision` to
`save_flow_graph` to refuse the save if someone else changed the graph since you
read it. The browser always does this. If another author changes the graph while
you have unsaved edits in the browser, the editor keeps your edits and offers
**Reload shared draft** instead of writing over the newer draft. MCP clients
should read the flow again and reapply their edits after a conflict.

Saving a draft does not deploy it. `deploy_flow` fixes the graph, name,
description, and error routing for the deployed flow; `deploy_dashboard` fixes
layout, widgets, sections, name, description, and theme. Enabling and disabling
apply to the deployed flow. Dashboard sharing and URL settings take effect
immediately and are not part of a deployment.

`restore_flow_version` and `restore_dashboard_version` replace the whole draft
with a stored version. Leaving out `version` restores the current deployment,
discarding changes made since. Restoring never deploys or changes whether the
flow is enabled. All open editors receive the restored draft.

Versions saved by older releases did not record presentation or error-routing
settings. Restoring one of them keeps those settings as they were when you
upgraded.

## Testing and failure inspection

`run_flow_test` runs the draft, including real device actions, and returns a
`run_id`. Pass that ID with the flow `id` to `get_flow_run` to inspect exactly
that run, even if a trigger starts another one meanwhile. Without `run_id`,
`get_flow_run` returns the latest run. `list_flow_runs` pages through stored
runs and filters by status or `had_failures`. Each flow keeps its latest 100
completed runs and all active runs, and Karakuri removes older completed runs
with their events.

`get_flow_run` always returns the state of each node. Set `include_events` for
the raw events, optionally filtered by `node_id`, and page with
`after_event_id` and `limit` (default 100, maximum 500). `next_event_id` and
`has_more_events` describe the next page.

`test_connection` returns `ok`, an `error` when the test failed, and, for kinds
that probe, a `detail` describing what it found, such as a calendar connection's
calendar names and upcoming event count.

## Visual dashboard inspection

`render_dashboard` returns a JPEG screenshot of a dashboard. It accepts `view`
(`draft` by default, or `deployed`), `width`, and `height` (default 1280 by 800;
each must be 100 to 2048 pixels). Draft previews require edit access. Karakuri
takes the screenshot with temporary read-only access to that dashboard and none
of your other permissions. Scheduled renders and render jobs still use the
deployed layout.

Editor chat shows the image in the expandable tool entry and passes it to the
model, which must accept images to see it. Only the latest screenshot is sent to
the model on later turns; earlier ones stay visible in the chat. A render
captures one screen, so request the size the dashboard is meant to fill.

## Focused edits and comparison

`patch_flow_graph` applies 1 to 200 operations to the shared draft, all or
nothing. Pass `expected_revision` from `get_flow`. Each operation has `op`,
`id`, and, for adds and updates, `value`. Operations are `add_node`,
`update_node`, `remove_node`, `add_edge`, `update_edge`, and `remove_edge`.
Updates merge objects recursively; null removes a field and arrays are replaced.
IDs cannot change. Removing a node also removes its edges. `subgraph_path` is an
optional array of local subgraph keys locating the graph to edit. Edit a shared
subflow through its own flow ID. Karakuri checks the whole batch before saving
it, and a stale revision or an invalid operation leaves the draft unchanged.

`diff_flow` and `diff_dashboard` compare what a deployment would contain. `from`
defaults to `deployed` and `to` to `draft`; either also accepts a version number
as a string. `deployed` is empty when nothing has been deployed. Results contain
`changed` and a `changes` array with `op`, `path`, `before`, and `after`. Paths
use JSON Pointer escaping, with nodes, edges, widgets, and sections keyed by ID.
The diff reports changes to flow edge order separately, because value inputs can
depend on edge order. Widget config is compared as parsed JSON. Sharing, access
settings, and whether a flow is enabled are not compared. Dashboard diffs
require edit access even when comparing deployed versions.

## Dependencies and validation

`get_document_dependencies` takes `kind` (`flow` or `dashboard`), `id`, and
`view` (`draft` by default, or `deployed`). It lists what the document refers
to, including local subgraphs, deployed shared subflows, error flows, and widget
bindings. Each dependency gives its location in the document and a status:
`available`, `missing`, `unavailable`, `invalid`, `dynamic`, or `unchecked`.
Dynamic values cannot be resolved without running a flow. References to
subsystems you cannot read are `unchecked` and reveal nothing about the
referenced object. Connection credentials and control values are never
returned. The listing stops after 32 nested graphs or 2,000 entries and reports
`truncated`.

`validate_dashboard` checks the layout's geometry, section references, widget
packages, widget configs, and bindings. Overlaps, disconnected services, and
widgets without a config schema are warnings; malformed configs, placements
outside the grid, and missing bindings are errors. `valid` means no errors were
found; check warnings and `truncated` too. Configs are checked with the widget's
defaults filled in. The check never changes the draft, deploys, or operates a
device.

`list_agents` reports each enrolled host, whether it is connected, and how many
capabilities it has registered. `get_agent_status` adds the node type IDs that
host provides; use `get_node_type` for their schemas. Capabilities list what the
host offers, not whether desktop permissions are granted or a node will succeed.

## Render diagnostics

`render_dashboard` with `diagnostics: true` returns a screenshot plus a report
from the same capture. Diagnostics are off by default, and access checks are the
same either way. The report covers readiness, viewport and page size, widget IDs
and positions, load state, widgets placed off screen, and content overflowing
its container. Overflow can be intentional scrolling, so it does not by itself
mean something looks wrong. Collapsed sections are not expanded, and some
built-in widget internals cannot be inspected.

The report also lists uncaught errors, console errors, failed requests, and
responses with status 400 or above. It strips credentials, query strings, and
fragments from URLs, and it does not collect request bodies or headers. Reports
list at most 100 browser issues, 500 placements, and 10,000 inspected elements,
and say when they were cut short. Diagnostic renders are kept as long as other
renders.

## Nodes without a flow

`list_node_types` lists the available node types, including plugin and
connected agent node types. `get_node_type` returns a type's configuration
fields and ports. Supply an optional `config` object to get `resolved_inputs`
and `resolved_outputs` for ports that depend on configuration. Neither tool
needs a flow ID.

`test_node` runs a node type directly:

```json
{
  "type": "script",
  "config": {"language": "javascript", "script": "return payload * 3;"},
  "inputs": {"in": {"payload": 11}},
  "timeout_seconds": 30
}
```

The response includes `status`, `outputs` (message arrays keyed by port),
`events` (node states and debug messages), `duration_ms`, and any `error`.
Results come back in the tool response, and no run is saved. A node failure is
a result with `status: "failed"`. Invalid configuration or inputs are tool
errors. Outputs from successful attempts, and messages emitted before an attempt
finishes, are included. Output stops at 1000 items or 1 MiB across outputs and
events, with `truncated: true`. Values that cannot be converted to JSON are
reported in `capture_errors`.

Tests use real node code and your configured connections, including remote
agents, and can perform real actions. No flow, version, or run history is
created. Node stores and variables start empty and are discarded after each
test. The timeout includes time the node spends waiting, defaults to 30
seconds, and accepts 1 to 120 seconds. When it expires, Karakuri asks the node
to stop. `status: "canceling"` means it had not stopped five seconds later. The
response can therefore arrive up to five seconds after the timeout, and a
system or plugin call that does not respond may keep running.

Triggers run once with the supplied sample and do not start listening. Inputs
follow the same rules as `test_flow_node`. Message inputs use the resolved port
IDs, binary payloads use base64, and triggers or nodes with no message inputs
accept `inputs.__trigger__`. For an isolated test, supply permanent value-port
fields in `config`, with base64 strings for binary data. Required fields are
still required, and parameter ports are not message inputs. Subflow calls, graph
tools, and routing nodes need a flow; use `run_flow_test` for them. The flow
editor's AI chat can use `test_node` to try a configuration before adding it to
the draft.

## Placed nodes and isolated tests

`list_flow_nodes` returns short summaries with `id`, `type`, `label`,
`available`, and `bypass` when set. Labels use the saved `_label`, or else the
type's label or identifier. Use `get_flow_node` for the saved configuration,
resolved ports, connected edges, and the full config schema. `list_flow_edges`
returns the wiring in order, optionally filtered by `node_id`. All three accept
`view` (`draft` or `deployed`) and `subgraph_id` (a key in the root's
`subgraphs` map). Draft responses include the revision for a following patch.
Nodes whose type is not installed are listed with `available: false`; ports
that cannot be resolved have `ports_known: false` in `get_flow_node`.

`test_flow_node` runs one draft node with the inputs you supply:

```json
{
  "id": "FLOW_UUID",
  "node_id": "transform",
  "inputs": {"in": {"payload": 42, "topic": "test"}},
  "config_overrides": {"script": "return payload * 2;"}
}
```

Read the node's resolved input IDs first. Every required message input needs a
message, unless you are testing only an optional input such as reset. Binary
inputs take base64 text. A trigger or a node with no message inputs accepts
`inputs.__trigger__`, or uses its saved test payload when that is left out.
`config_overrides` replace config fields for this run only. Each wired value
input needs an override, because the nodes feeding it do not run. The draft and
deployment are not changed.

The node performs real actions, with its normal retry policy. Nodes before and
after it, and error-handler flows, do not run. Routing nodes, subflow calls,
tools, annotations, and bypassed nodes cannot be tested alone. Use
`run_flow_test` to test the branch instead. To test a regular node inside a
local subgraph, supply `subgraph_id`. Its history and stored state use the
returned `node_id`, prefixed with `@subgraph/<key>/`, and are kept apart from
where the subgraph is used. Testing a top-level node shares its stored state
with normal runs. Values that other nodes would normally supply, such as run
variables and tool-call state, are absent.

Use the returned `run_id` with `get_flow_run` and `include_events: true` to see
outputs and failures. History keeps the first message on each output port, with
a count when there were more. Editor chat can use these tools too, limited to
the selected flow.

## Discovery and troubleshooting

`list_ha_services` reads the connected Home Assistant's service catalog. Pass
`connection_id` and optional exact `domain` and `service` filters. Each entry
includes its domain, service name, and the full service description under
`schema`, including the fields, selectors, and targets that integration
provides. Results are sorted and paged with `offset` and `limit` (default 50,
maximum 100). Read `has_more` and `next_offset` for the next page. This uses
Home Assistant's [`get_services` WebSocket
command](https://developers.home-assistant.io/docs/api/websocket/#fetching-services)
and never calls a service.

`capture_mqtt_messages` records messages from an existing broker connection for
a short time. It takes `connection_id`, `topic_filter`, optional
`duration_seconds` (default 5, range 1 to 30), and `max_messages` (default 20,
range 1 to 100). Topic filters accept `+` and `#`. The tool refuses shared
subscriptions. The duration includes setting up the subscription. Flows and
devices using the same connection are not affected, and cancelling the request
stops the capture. Clients older than MCP protocol version 2026-07-28 cannot
cancel by disconnecting and wait for the full duration.

The result's `messages` give topic, time received, payload, and `encoding`
(`utf8` or `base64`). Retained messages are included when the broker holds any,
even if a flow or device already subscribes to the same filter. QoS and retained
flags are not reported. `stop_reason` is `timeout`, `message_limit`, or
`byte_limit`. A single payload over 128 KiB, or more than 1 MiB of output, stops
the capture with `truncated: true`. `subscription_active` reports whether the
subscription was still active at the end, and `connection_state` tells an empty
sample apart from a disconnected broker. Capture never publishes and may miss
messages. Both `list_ha_services` and `capture_mqtt_messages` require
`api:connections:read`.

`preview_schedule` takes `expr`, an optional IANA `location`, an optional
RFC3339 `after`, and `count` (default 5, range 1 to 20). It reads expressions
the same way schedule nodes do: five fields, optional seconds, and descriptors.
It returns `valid`, a description, field labels, and upcoming RFC3339 times with
offsets. Invalid expressions and zones return `valid: false` with an error. It
requires `api:flows:read` and never starts a trigger.

`find_references` finds what refers to something. Supply `kind` (`connection`,
`control`, `mqtt_device`, `flow`, `ha_entity`, or `agent`) and `ref`. HA
entities also need their connection UUID as `owner`. Controls match by UUID or
name. `view` selects the draft (default) or deployed version of the documents
searched; nested shared flows always use their deployment. The search requires
read permission for the referenced subsystem. It searches flows only with
`api:flows:read`, and dashboards only where you have edit access. It never reads
live HA entity states.

Each `page` (starting at 1) searches up to 50 flows and 50 accessible
dashboards. Keep paging while `has_more` is true, even if a page has no matches.
Each result identifies its document and where the reference is. A page returns
at most 2,000 matches. `incomplete` reports hitting a limit, dynamic references,
unknown node or widget types, or documents that failed to load. `issues` names
documents that could not be searched. The search covers declared references, not
text inside scripts. Edits made during the search can shift the pages.

`inspect_flow_state` reads stored state for flow `id`. Choose `store` as
`variables` (default), `nodes`, or `waits`; node state accepts an exact
`node_id` filter. `waits` lists the flow's pending timers, held messages, and
partial groups, with each wait's node, `key`, `deadline`, `missed_deadline`
setting, the `run_id` that started it, and whether it is `durable`. Pass a
wait's `node_id` and `key` to `cancel_flow_wait` to end it without delivering
it. Run history records the cancellation. Both accept an exact `key`, `limit`
(default 50, maximum 100), and `after_id` for paging with the returned
`next_id`. Entries contain JSON values and update times. Values over 64 KiB, or
beyond the page's 1 MiB total, are left out with `value_omitted: true`; their
keys are still listed.

State inspection requires both `api:flows:read` and `api:runs:read`. It shows
only that flow's stored variables and durable node state, including state left
by removed nodes. It does not show global variables, temporary node state, or
run and message variables. Results have `source: persisted`, so very recent
changes may not appear yet, and pages of a running flow can change between
requests. It never changes state. Whatever data a flow stores is returned as it
is, so these permissions give access to that data.

Editor chat offers connection discovery, MQTT capture, and reference search in
both editors. Flow chat also offers schedule preview and state inspection, which
is limited to the selected flow.

### Graph authoring diagnostics

Graph errors name the node or edge they concern. Successful `save_flow_graph`
and `patch_flow_graph` calls include a separate `validation` result from a
strict check. You can save an incomplete draft while that result is invalid. Fix
the reported problems before treating the graph as finished.
`validate_flow_graph` accepts `flow_id` without `graph` to check the stored
draft. Use `strict: true` to check that the configuration can run.

`get_node_type` includes an optional `value_schema` on structured config fields
and `payload_schema` on output ports. These JSON Schemas describe nested data.
They do not change what the node accepts or which ports can connect.

### Notification services

`list_notification_services` returns every notification provider with its
credential fields, target examples, and the reason a provider is unavailable.
`list_notification_endpoints` takes a connection `id` and returns only that
connection's allowed endpoint IDs and labels. Both require
`api:connections:read`.

`send_notification` takes `connection_id`, `endpoint`, an optional `subject`, a
required `message`, and an optional integer `timeout` (1 to 120 seconds, default
30). It requires `api:connections:invoke` and sends only to an endpoint ID
listed in that connection's settings. A successful result contains `accepted:
true`. A failure or timeout may leave delivery uncertain and is never retried
automatically. Use the connection tools to configure services. Reads mask
secrets and targets, and saving with an unchanged mask keeps the stored target.
The [notification guide](notifications.md) describes the configuration format.
