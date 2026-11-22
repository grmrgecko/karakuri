# Authoring flow graphs

A flow is a JSON graph document of nodes wired by edges. This page is the
reference for writing one programmatically, over the MCP tools or the REST
API. The editor produces the same document; nothing here is MCP-specific.

## Lifecycle

A flow always has a **draft**, and the draft is what you edit. Saving the
draft validates leniently. Required config fields may be missing or empty, so
you can store half-finished work. A draft can also keep types from unavailable
plugins. **Deploy** strictly validates the branches reachable from every
trigger, including their value dependencies, then freezes the whole draft as an
immutable numbered version. Disconnected unfinished fragments do not block a
working flow. Deploy validates shared subflows and graphs without triggers in
full. **Enable** arms the deployed version's triggers. A test run strictly
validates the branch reachable from its selected start and executes it from the
draft, so you can try a graph without deploying it. `validate_flow_graph` with
`strict` validates the whole submitted document.

Browser and MCP edits share the same stored draft, and open editors refresh
when another client changes it. A refresh with the same draft revision keeps
the selected node and its configuration panel open, including after autosave
or a reconnect. Loading a different graph revision clears selection.
`get_flow` returns `draft_revision`. Pass it as `expected_revision` when saving
a whole graph, so your save cannot overwrite a concurrent edit. For focused
edits, `patch_flow_graph` applies an atomic, revision-checked batch of node and
edge changes. Before deploying, read `diff_flow` to compare the draft's graph
and settings with the current deployment. Restore replaces the whole draft,
including its name, description, and error routing, with a stored version. It
does not deploy or change enable state.

Work in this loop. Save the graph, run `validate_flow_graph` with `strict` set
to check the whole document, run `run_flow_test`, then read per-node results
with `get_flow_run`. Deploy and enable once the run behaves.

## Ownership and sharing

Whoever creates a flow owns it. The owner shares it with people or user
groups at one of four levels, each including the ones below it:

| Level | Allows |
|---|---|
| `faceplate` | The flow's faceplate tabs only. No graph. |
| `view` | The graph, versions, run history, live runs, pending waits, export, and the flow's event log entries. Nothing that changes or runs it. |
| `edit` | Saving, testing, deploying, enabling, running, and cancelling runs and waits. |
| `admin` | Sharing and deleting. The owner holds admin. |

Holders of `api:flows:manage` hold admin on every flow; the Admin role has it
through `api:*`. `api:flows:read`, `write`, and `deploy` say whether a user may
read, author, and deploy flows at all. The share level says which flows they
may act on. A flow the user holds no level on answers not found.

Share from the share button on the flow list or the editor toolbar, or with
`POST /api/flows/{id}/shares` and `{"username": "...", "permission": "edit"}`
or `{"group_name": "...", ...}`. The event log records each change. Shares are
not versioned, so restoring a version leaves them alone. Only a manage holder
may move a flow to another owner, with `owner_id` on `PUT /api/flows/{id}`.

**Folders** share the same way. A folder's owner and shares reach its
subfolders and every flow inside them, and a flow's level is the highest of
its own shares and those of every folder above it. Share a folder from its
share button in the flow list, or with `POST /api/folders/{id}/shares`.
Renaming a folder needs edit; moving, deleting, and sharing it need admin.
Putting a flow or folder into a folder needs edit on the destination. Moving a
flow also needs admin on the flow, since the destination's shares will reach
it. The flow list omits folders a user cannot see and shows a flow shared out
of one at the user's root.

The flow list updates without a reload. Flows and folders that are added,
renamed, moved, enabled, shared, or removed in another tab, by another user,
or through MCP appear as soon as they change. Each flow's dot shows its runs
as they happen: orange while it runs, green when it succeeds, yellow when it
succeeds after handled failures, red when it fails, and grey when it is
cancelled. A hollow dot means the flow has not run yet.

Flow names need not be unique across owners. Karakuri refuses a new name only
when a flow you can already see uses it. A flow's ID is its address. A slug
such as `/flows/porch-light` opens the one flow you can read with it, and
offers a choice when you can read several. `GET /api/flows/by_slug/{slug}`
lists them.

A few rules reach across flows:

- **Subflows.** A save may add a reference to a shared subflow only when the
  user may view it. References the draft already holds keep saving. An
  imported flow has no draft, so the import checks every reference in it. A
  deployed flow keeps running after its author loses access to a subflow,
  because runs resolve subflows as the system.
- **Copying another flow's graph.** Export and `import_subflow` need view on
  every subflow they copy. A copy made by `import_subflow` saves like any
  other draft, so the shared subflows it calls need view as well.
- **Error flow.** A new error flow must be one the user may view.
- **Widget triggers.** Pressing one through `api:flows:trigger` needs edit on
  the flow. A dashboard binding a trigger deploys only for someone with edit
  on that flow. Its viewers then press through the dashboard, as
  [dashboard security](dashboard-security.md) describes.
- **Connections and agents.** A flow reaches only the connections and agents
  its user may use. Tests and draft runs act as the user who starts them. A
  deployed flow acts as whoever deployed it, and keeps doing so after a
  faceplate change. A deploy is refused when it names a connection or agent
  node the deployer cannot use. At run time a node fails, before it contacts
  anything, when the connection it names, including one wired in over a value
  edge or picked by a script, or the agent it runs on is not shared with the
  run's user. Remove someone's share and the flows they deployed stop reaching
  that connection on their next run.

The default User role can create and deploy its own flows but cannot open or
change anyone else's until the owner shares them. On a server with several
accounts, share the flows other people work on.

## The graph document

```json
{
  "nodes": [
    {"id": "n1", "type": "manual", "position": {"x": 0, "y": 0},
     "config": {"payload": {"value": 42}}},
    {"id": "n2", "type": "switch", "position": {"x": 240, "y": 0},
     "config": {"property": "payload.value",
                "rules": [{"id": "high", "operator": "gt", "value": "10"}]}},
    {"id": "n3", "type": "debug", "position": {"x": 480, "y": 0}}
  ],
  "edges": [
    {"id": "e1", "source": "n1", "source_handle": "out",
     "target": "n2", "target_handle": "in"},
    {"id": "e2", "source": "n2", "source_handle": "high",
     "target": "n3", "target_handle": "in"}
  ],
  "viewport": {"x": 0, "y": 0, "zoom": 1}
}
```

Node IDs and edge IDs are free-form strings that must be unique within the
document. `viewport` is where the editor's camera sits, and
`{"x": 0, "y": 0, "zoom": 1}` is fine for a generated graph. On entry, the
editor centers the measured node bounds in the canvas at 100% zoom, including
when entering a subflow. Large graphs stay at 100%; use the fit-to-view control
to show the whole graph. Returning through a subflow breadcrumb restores the
parent camera position.

## Focused draft patches

`patch_flow_graph` takes the flow `id`, its current `draft_revision` as
`expected_revision`, and `operations`. For example:

```json
{"op":"update_node","id":"n2","value":{"config":{"_label":"Kitchen"}}}
```

Operations use node or edge IDs, not JSON Pointer paths or array indices. The
operations are `add_node`, `update_node`, `remove_node`, `add_edge`,
`update_edge`, and `remove_edge`. Adds take the new item's fields. Updates
merge objects, replace arrays, and remove fields set to null. Removing a node
removes its incident edges. The optional `subgraph_path` array names local
subgraph keys from the root to the graph being edited. The batch validates and
saves once, and an invalid operation or stale revision saves nothing.
Validation errors include the node or edge ID and the failed constraint.
Successful patches and whole-graph saves return `validation` with strict
diagnostics. `validation.valid: false` means the draft saved but would not
pass strict validation yet. To check the stored draft without resending it,
call `validate_flow_graph` with `flow_id` and `strict: true`.

Switch `rules` is an array of `{id, operator, value, label}` objects. The rule's
`id` is its output port ID. `operator` names the comparison, such as `eq`,
`gt`, or `expression`. `value` is a string, including for numeric comparisons,
and only `exists` and `otherwise` omit it. `property` selects the tested
message path. With HA State, `payload.new` is the new state or selected
attribute value, and `payload.new_state` is the full state object. Rules that
name each state explicitly keep `unknown` and `unavailable` from counting as
`off`. The trigger's `to` filter can also select one state directly without a
switch.

## Nodes

A node is `{id, type, position, config}`. The catalog lists what exists.
`list_node_types` returns the summaries, and `get_node_type` returns one type's
config field schema (`config_spec`), its ports, and its port type rules.
Numeric editor inputs accept fractional values; each node enforces its own
range and integer requirements. Structured fields may include a `value_schema`
JSON Schema. Output ports may include a `payload_schema` describing the payload
inside the message envelope. Config is an object whose keys are the field IDs
from `config_spec`, and validation rejects a field the spec does not declare.

Optional node fields:

- `retry` overrides the type's retry policy:
  `{"max_attempts": 3, "delay_ms": 1000, "backoff": 2, "max_delay_ms": 30000, "jitter": 0.2}`.
  Only retryable types accept one.
- `bypass` disables the node without unwiring it. The node does not run, and
  each output receives the message from the input whose payload type matches
  it.
- `params` lists config field IDs exposed as value input ports (see
  [Value edges](#value-edges)).
- `width`, `height`, `collapsed` are canvas presentation and do not affect
  runs.

Three config keys are presentation state that runs and validation ignore:
`_label` (a display name overriding the type's), `_color` (a card tint), and
`_rotation` (degrees). Leave them out of generated graphs unless asked.

## Edges and ports

An edge names its endpoints by node ID and **port ID**. `source_handle` is an
output port of the source node, and `target_handle` an input port of the
target. Port IDs come from the type's metadata (`inputs` and `outputs` in
`get_node_type`). Most nodes read on `in` and emit on `out`, and a switch emits
on its rule ports. A node with a failure branch has an `error` output that
receives the message when the node fails and its retries are exhausted.

An input port accepts **one** incoming edge, and wiring two sources into one
input is a validation error. Fan out from one output is fine. The one
exception is a **multi** parameter port (`multi` on the port in
`get_node_type`, such as the AI agent's `tools` input). It takes any number of
value edges, and the pulled values reach the config field as an array in edge
order. A source wired through a reroute, a set/get pair, or a subflow boundary
keeps the position of the edge that enters the port.

A port's type describes its **payload only**; every port carries a full
message (payload, topic, metadata). `any` is the wildcard and `binary` is real
bytes. On the message path an `any` output may feed a typed input, because a
webhook body is not knowable until the run. Validation rejects a *declared*
mismatch, such as `string` into `number`. Karakuri never sniffs content. A
source names its format (`webhook.body_format`, `mqtt-in.format`,
`http-request.response_format`), and bytes that do not match it fail rather
than arriving as something else. Nodes that only route or delay propagate
their upstream type, so a delay behind a JSON parser reports `object`.

## Value edges

A value edge (`"kind": "value"`) binds a config field to another node's
output. No message travels along it during a run. The consumer pulls the value
when it executes, once per run. The source must be a **pure** node, marked
`pure` in the catalog, such as `manual`, `file-read`, `ha-get-state`, and
`control-get`. To supply a config field, list its ID in the node's `params`
array and use `param:<field>` as the edge's `target_handle`. The field's
literal in `config` stays as the fallback.

A trigger with a wired field reads the value once, when the flow is armed, and
arms with it. A deploy rearms the flow and reads it again, which is how a new
faceplate value reaches a schedule or a watched topic.

Some fields are **port fields** (`port` on the field spec). They are permanent
value input ports rather than settings. Nobody types into them, they never
appear in `params`, and a value edge to `param:<field>` is the only way to
supply them. The AI agent's `model` and `tools` inputs are port fields,
because they hold another node's output (a model handle, a tool set) that
nobody could write by hand. A required port field must be wired to deploy. A
multi port field takes several edges, as described in
[Edges and ports](#edges-and-ports).

The AI **Tool** node adds one more wiring shape, and the `ai` guide covers it
in full. Its `out` wires to an agent's `param:tools` as a value edge. Its
`call` output starts the tool's branch with an ordinary message edge, and the
branch's last node wires **back into** the tool's `return` input. That cycle is
legal. Each argument declared in its `tool_args` config is an output port named
`arg:<id>`. It wires to a branch node's `param:<field>` input as a value edge,
read again for each call. Argument ports carry values only, never messages.
Deploy requires `call` wired, `return` wired, and the tool name unique in the
graph.

## Script node calls

A JavaScript, Python, or Bash script node can invoke eligible catalog nodes
through its language's node binding instead of wiring every step. Calls use the
real node configuration and input message shapes, run synchronously inside the
active run, and return output messages without delivering them to graph edges.
See [Scripting](scripts.md) for function names, options, results, eligibility,
state identity, and errors.

Strict validation, used by test runs and deploy, checks calls whose node type
and configuration are written literally in the script, using the same rules
that apply when the script runs. You can still save a script with diagnostics
as a draft. Configuration the script computes cannot be checked ahead of time,
so Karakuri validates it when the call is made, before the node acts.
Validation never runs a node or touches an integration.

Run history gives each call a distinct invocation record with its parent script,
node type, sequence, attempts, outputs, and failure. Caught failures can continue
to a fallback. Once any call begins execution, Karakuri stops retrying the
outer script, so a later failure cannot replay earlier calls. Pure value pulls
can call only pure nodes.

Node calls are unavailable in the standalone script test. A flow-node test
(`test_flow_node`) can call every eligible node and performs real actions, like
other node tests.

## Triggers and test runs

A run starts at a trigger (`trigger` in the catalog summary). A graph with no
trigger can still be test-run from a pure source node, which is how you
exercise a hand-driven flow. When a graph has several possible starts,
`run_flow_test` needs `trigger_node_id`.

A test run seeds its message from the trigger. The manual trigger uses its
configured payload, and a schedule synthesizes its tick. Every other trigger
reads a `test_params` value from its config (for a webhook, the sample body),
and `run_flow_test` refuses it without one. The `payload` argument of
`run_flow_test` overrides the seed for one run without editing the node.

`get_flow_run` returns the last persisted run with `node_states`, which gives
each node's latest state (`pending`, `running`, `retrying`, `succeeded`,
`failed`, `skipped`), its attempt count, its error message, and whether an
error branch handled its failure. Karakuri keeps the latest 100 completed runs
per flow and every active run. Use `list_flow_runs` for history and pass
`run_id` to `get_flow_run` to inspect one exact execution.

Before creating a flow, inspect a type with `get_node_type` (see
[Nodes](#nodes)). Passing `config` to `get_node_type` resolves its dynamic
ports. `test_node` accepts a type, config, and input messages, and returns
outputs and errors directly without creating a flow or saving history. It uses
real connections with fresh, temporary node stores and variables. Its
wall-clock timeout defaults to 30 seconds, including parked waits.

`list_flow_nodes` lists placed node IDs, types, labels, availability, and
bypass state. Use `get_flow_node` for configuration and resolved ports, and
`list_flow_edges` for wiring in a draft or deployment. `test_flow_node`
executes only the selected draft node with explicit `inputs` keyed by its
resolved message port IDs. Temporary `config_overrides` replace fields for that
test without saving. Wired value parameters require overrides, because their
producers do not execute. Triggers accept `inputs.__trigger__` or their
configured test payload. Neighboring nodes and the flow's error handler do not
run, but the selected node still performs real actions and uses its retry
policy. Use `run_flow_test` when routing or a subflow branch needs to execute.
Inspect the returned run ID with `get_flow_run` and `include_events: true` for
outputs.

## Faceplates

A **Faceplate** node is one tab of settings on the flow's page. Its config
holds a tab `name`, an ordered list of `controls`, the applied `values`, and an
optional change `script`. Each control is a value output named by its ID; wire
it into the parameter it drives. A pulled faceplate emits each control's
applied value, or its default when none was applied. A faceplate has no
message ports and never runs on its own.

| Control type | Value | Output port |
|---|---|---|
| `number` | a number, kept within `min` and `max` | `number` |
| `boolean` | `true` or `false` | `bool` |
| `string` | text, or one of `options` | `string` |
| `entity` | `{"connection_id": "...", "entity_id": "sensor.lux"}`; its live state shows beside the field | `object` |
| `array`, `object` | JSON, usually edited by a template | `object` |

A flow with faceplates opens on its first faceplate tab. The flow editor is
the first tab, and editors reorder faceplate tabs with the arrows beside the
selected tab. **Apply** checks every value against its control, saves, and
deploys:

- **Edit access** saves the draft with the values, draft edits included, and
  deploys it.
- **A faceplate-level share** (see [Ownership and sharing](#ownership-and-sharing))
  sees only the faceplate tabs. Its Apply deploys the current version with
  only the new values, so an editor's unfinished draft stays undeployed, then
  writes the values into the draft.
- **A view share** reads the tabs but cannot apply.

The REST routes are under `/api/flows/{id}/faceplates`: `GET` for the tabs,
`POST .../apply` with `{"values": {"<key>": {"<control>": value}}}`,
`POST .../order` with `{"keys": [...]}`, and `GET .../readings` for entity
states. A key is the faceplate's node ID, `subgraph/node` inside a local
subflow, or `subflowNode>innerKey` inside a shared subflow.

**Templates.** A control's `template` is HTML and JavaScript that draws a
custom editor. It runs in a sandboxed frame with no same-origin access and a
content policy that blocks network requests, and talks to the page through
`window.karakuri`:

- `value`, `control`, `values` (every faceplate value by key), `reading` (this
  control's entity state), `readings` (all of them), and `theme`;
- `onUpdate(fn)` to redraw when any of them changes;
- `setValue(v)` to set this control's value.

A template that fails or does not start falls back to the plain editor. The
curve editor and color picker ship as templates written the same way.

**Change scripts.** A faceplate's `script` runs in the same kind of sandbox
whenever any faceplate value in the flow changes:

```js
faceplate.onChange((change) => {
  // change: { tab, control, value, old }; tab is the faceplate key.
  if (change.control === 'max') {
    faceplate.set('Advanced', 'curve', [{ x: 0, y: 10 }, { x: 100, y: change.value }]);
  }
});
```

`faceplate.get(tab, control)` reads a value and `faceplate.set(tab, control,
value)` changes one, where `tab` is a tab name or key. Sets can trigger further
changes. After 10 rounds Karakuri stops the scripts and the tab shows why.

**Shared subflows.** A Faceplate node inside a shared subflow shows as a tab of
each flow that calls it, labelled with the calling node. Karakuri stores values
applied there on that subflow node, so two callers keep their own settings. An
export embeds the subflow as a local subgraph and writes each calling node's
values into its own copy.

## Subflows

A subflow node runs a nested graph. It references either a **local subgraph**,
with `"subgraph_id"` naming a key in the document's `subgraphs` map, or a
**shared subflow**, with `"subflow_id"` naming another flow created with
`is_subflow` and resolved against its deployed version. Inside a subflow
document, boundary port nodes declare the interface, and each declares its
`port_type`. Validation checks the inside against that contract and checks
nesting for cycles and depth.

## Variables

Scripts and the variable nodes read and write four scopes. `msg` rides one
message, `run` spans one run, `flow` persists per flow, and `global` persists
server-wide. `flow` and `global` survive restarts. The default scope is `msg`
and the default path is `payload`.

## Failure handling

Only retryable node types accept a retry policy. When a node's retries are
exhausted, a wired error output receives the message instead of the run failing.
Value parameters are read before the node's first attempt, so a failed pull is
not retried and does not reach the consumer's error output. Failures inside
active tool-call branches return to the waiting AI agent as observations. A
flow can also name an `error_flow_id`, a flow that starts when a run fails
unhandled. Wire the failure path for any node whose failure matters instead of
leaving the run to fail.

The run timeout counts elapsed time, excluding intervals while any node waits
in a delay or a tool wait, even if another branch is active. Retry backoff does
not pause it, so the run timeout is not a wall-clock cap. Zero disables it. A
zero per-flow concurrent-run limit allows unlimited runs.

Cancellation starts no further nodes and asks active nodes to stop. The run
finishes after they return, and a node that ignores cancellation can keep it
running. An unhandled action failure starts no further nodes but lets active
ones finish. On shutdown the server waits up to five seconds for active runs,
then abandons them and logs that it did. A pure source that succeeds is read
once per run. If it fails, the next node that pulls it tries it again.

The webhook endpoint accepts GET and POST. Armed triggers further filter their
configured methods and IP allowlists, and can verify HMAC over the bytes read.
With `server.trust_proxy` enabled, forwarding headers replace the address
checked by that allowlist only on requests from a peer in
`server.trusted_proxies` (loopback by default). The server reads
`X-Forwarded-For` from the right, so a client cannot forge its address by
prepending one. List your reverse proxy's address there. When two armed
webhooks use the same token, the one armed last receives requests and the
server logs a warning. A webhook reads at most 1 MiB and can accept a truncated
text or binary body. A synchronous response wait defaults to 30 seconds. A
disconnected caller or response timeout does not cancel the independent flow
run.

## Discovering inputs and inspecting state

Before configuring an HA action, use `list_ha_services` with its connection UUID
and optional domain/service filters to read the available fields and targets.
For MQTT triggers, `capture_mqtt_messages` collects payload samples from a topic
filter for up to 30 seconds without publishing. `preview_schedule` validates a
cron expression and lists upcoming times in the selected timezone without
arming the trigger.

Use `find_references` before changing a shared flow, connection, or control.
It finds the flow graphs and editable dashboard bindings that reference it,
including nested deployed subflows. Read every source-document page and check
`incomplete`. The search cannot find every dynamic reference or value a script
constructs.

`inspect_flow_state` reads a flow's persisted variables or durable node store
keys. It is read-only and can trail running nodes by a moment. Removed nodes
may still have stored state. It excludes global, run, message, and transient
node state. Use `get_flow_run` for execution events. With `store: "waits"` it
lists the flow's [pending waits](#pending-waits) instead.

## Time windows

Time window (`time-window`, under Time) classifies incoming messages on `inside`
or `outside`. It preserves the payload, topic, metadata, and group. Empty
weekdays select every day. Set `location` to an IANA timezone, such as
`America/Chicago`, or leave it empty for server-local time. Empty `time` uses
the current time. Otherwise it names a message property containing a
timestamp, such as `payload.at`. It accepts RFC3339 strings, Unix seconds, and
Unix milliseconds, as Wait Until does.

Each endpoint independently selects `clock` or `sun`. Clock times use `HH:MM`,
with defaults 08:00 and 18:00. Sun endpoints select a standard named event and
an optional elapsed offset from `-12h` to `12h`. Coordinates can be explicit or
use the server's configured location. Custom solar elevation belongs to Schedule.

Start is inclusive and end exclusive. An end before the start continues into the
next day. Weekdays belong to the nominal start date, so a Friday window from
22:00 to 06:00 includes early Saturday. Solar offsets can move an endpoint into
an adjacent date without changing its weekday ownership. Identical endpoints
fail. Clock comparisons follow local wall time. Both occurrences of a repeated
DST hour match, and a skipped minute has no arriving event. If a required sun
event does not occur, `outside` includes `meta.time_window_reason: "no_sun_event"`.
Invalid times, settings, or timestamps use `error`.

## Connection status triggers

Connection status trigger (`connection-status`, under Network) observes one saved
integration connection of any kind. Select any combination of `connecting`,
`connected`, `error`, and `disabled`; empty selects every state change. A
disconnect uses the integration's native `error` or `connecting` report,
followed by `connected` on recovery. Saving an enabled connection restarts it
through `connecting` without reporting `disabled`. Repeated reports of the same
state do not start more runs.

`emit_initial` defaults to false. Enable it to emit the current selected status
once on arming, including disabled connections and connections that failed to
start. Standalone node tests read one status without subscribing. An optional
`test_params` sample supplies a synthetic payload instead.

The output payload contains `connection_id`, `connection_name`, `connection_kind`,
`state`, `previous_state`, `error`, `since`, and `initial`. Topic is the
connection UUID. `since` is an RFC3339 timestamp for entry into the state, and
repeated error details do not reset it. Initial events have empty
`previous_state`. Filtered changes still update the baseline, so recovery
identifies the actual preceding state. Payloads contain no connection
configuration or credentials.

The trigger keeps watching when the connection is saved or restarted, and
stops when the flow is disarmed. Karakuri does not store statuses, so after a
server restart the trigger starts from the current state. A trigger that falls
more than 64 reports behind drops the oldest and logs the loss, and the flow's
concurrent-run limit can also drop runs. Use this trigger for alerts and
recovery actions, not an audit log.

Device Availability (`mqtt-device-availability`) starts a run when a device
goes online, offline, or unknown. It watches one device, or every device when
`device` is empty, optionally only on changes `to` one value. The message
carries `device_id`, `name`, `availability`, `previous`, `source`
(`connection`, `topic`, `interval`, `default`, or `entity`), and `at`. Device
Value and Device Changed add `meta.availability`, and Command Device fails on
an offline device unless the device sends commands while offline. The MQTT
device state script guide explains how availability is decided.

ESPHome Action (`esphome-action`) calls one of a node's own actions with
arguments checked against their declared types, and ESPHome Node Action
(`esphome-node-action`) starts a run when a node calls `homeassistant.action`
or fires `homeassistant.event`. The ESPHome guide has the details.

NVR Event (`nvr-event`) starts a run when Frigate tracks an object, filtered by
camera, label, sub label, zone entry, score, and stage. NVR Review
(`nvr-review`) starts one on an alert or detection. NVR Image (`nvr-image`)
reads a camera's latest frame or an event's snapshot as bytes and as an image.
A Frigate switch whose camera receives no frames is offline with source
`entity`. The Frigate guide has the details.

Energy Usage (`energy-usage`) reads the usage and cost of the house, a group,
or a source over a span, optionally against the average of the same span on
the previous four matching weekdays. Energy Price (`energy-price`) reads the
price now or at a time, or finds the cheapest window. Energy Price Period
(`energy-price-period`) starts a run at each price boundary, with the
missed-deadline fields of the other resumable nodes. The energy guide has the
details.

Log Event (`log-event`) adds an entry to the event log with an interpolated
message, a level, and optionally the control or device it is about, naming the
flow and run. Event Logged (`event-logged`) starts a run on new matching
entries, filtered by category, level, subject, and message text. It never
starts on entries its own flow caused or on folded repeats, and security
entries need the owner's audit permission. The events guide has the details.

Back up (`backup-create`) writes an encrypted backup of the installation with
the passphrase stored in **Settings, Backup** and waits for it to verify. It
fails without retrying when no passphrase is stored. Backup finished
(`backup-result`) starts a run when any backup fails, succeeds, or either, such
as to send a notification after a failed night. The backup guide has the
details.

## Numeric transforms

Numeric transform (`numeric-transform`, under Logic) reads `source` and writes
`target`, both defaulting to `payload`. Sources can be numeric payload or
metadata properties. Destinations can replace payload or write
`payload.reading` or `meta.reading`. Other message fields and group
information stay intact.

| Operation | Settings and behavior |
| --- | --- |
| `scale` | `value * factor + offset`; defaults factor 1 and offset 0. |
| `map` | Map input endpoints to output endpoints. Reversed ranges are allowed; `clamp` limits the result to the output endpoints. Equal input endpoints fail. |
| `clamp` | Bound the value between `min` and `max`, default 0 and 100. Minimum must not exceed maximum. |
| `round` | Integer `decimals` from -12 to 12. `rounding` selects nearest with ties away from zero, floor, ceiling or truncation toward zero. Negative decimals round tens or larger places. |
| `convert` | Convert compatible `from_unit` and `to_unit` values. Defaults Celsius to Fahrenheit. |

Unit choices cover temperature, length, speed, pressure, power, energy, and
angle. Use scale and offset for a calibration outside these dimensions.
Missing or nonfinite readings, incompatible units, invalid settings, and
numeric overflow use `error`. A scalar payload has Number ports, and a nested
payload path derives Object. Metadata writes preserve the incoming payload
type. The editor updates these types live, and graph validation uses the same
rules.

## Stateful node behavior

Hysteresis watches a numeric message property using separate lower and upper
thresholds. High-active mode activates at or above upper and releases at or
below lower; low-active mode reverses those conditions. Inside the band the
node keeps its remembered state. Only transitions emit on `active` or
`inactive`, and each key starts inactive. State is durable across redeploys
and restarts. An optional key property, such as `topic`, isolates devices. A
reset-only input clears that key without emitting. Lower must be below upper,
and thresholds and readings must be finite numbers.

Condition wait polls an optional `Observed value` wire from any pure source,
including HA Get State, Device Value, or Control Value. Each observation reads
the source and its own value inputs again; other nodes in the run keep the
values they already read. The boolean condition sees the observed value as
`payload` and reads current flow and global variables. Without a value wire, it
sees the original incoming payload and live variables. `Held for` requires
matching observations for that duration, and a false observation restarts the
interval. Connect a value-change trigger, such as Control Changed or HA State,
to `Check` to request an immediate fresh observation. Check messages wake every
active wait on this flow and node, including waits started by another run. The
wait ignores the check payload. A check starts no wait, emits nothing, and
leaves each wait's original message and deadline intact. Polling continues as
a fallback. Change triggers still obey the per-flow concurrent-run limit, so a
saturated limit can drop their runs. Repeated pending checks can coalesce, and
the wait can miss changes between observations. Configure a positive timeout
and poll interval, with held duration no greater than timeout. `matched` and
`timeout` preserve the original message and include `meta.condition_value` and
`meta.condition_wait_ms`. Cancellation emits nothing, and read or expression
failures use `error`. A pending wait survives restarts and redeploys; see
[Pending waits](#pending-waits).

Delay and Trigger accept `key`, a message property selecting independent state
such as `topic` or `payload.device_id`. Empty keeps one shared state for every
message. A message missing the configured key property fails. Trigger reset
cancels only the selected arm. Delay reset clears the selected rate schedule
and invalidates queued and debounced messages; fixed and random delays are
unchanged. A queued message invalidated by reset emits on `superseded` when its
wait wakes. Delay sends rate-limited messages to `dropped`, and a message
replaced in a debounce leaves `superseded` once. Debounce `max_wait` bounds a
window from its first arrival even when new messages keep extending its quiet
period. A reset cancels the pending window, and its latest message later leaves
`superseded`. A window started after the reset is unaffected.

Dedupe `expiry` forgets a value or higher/later watermark after the configured
duration from its last admission. Duplicates never extend that deadline. Empty
expiry keeps values until the history count or the watermark moves past them.
When you add expiry to a dedupe that already has history, each remembered
value starts its expiry clock the next time it is seen. Expiry deadlines
survive restarts. `duplicate` forwards suppressed messages unchanged. A
reset-only input clears every logic's history without emitting.

## Pending waits

Trigger, Delay (every mode), Wait until, Condition wait, Join, Batch, Aggregate,
and the sequence modes of Limit and Sort hold work between messages: a timer, a
held message, or a partial group. In deployed runs Karakuri stores that work as
a pending wait, so it survives a restart and a redeploy. Test runs of the draft
keep their waits in memory.

After a restart, each wait resumes in a new run that starts at the waiting node.
It fires at its original deadline, not the original duration measured from the
restart, and resets, extensions, and keys behave as they would have without the
restart. The new run carries the message the wait held, including binary
payloads. Run variables and other branches of the original run do not come
back. Messages it emits carry `meta.resumed` with `from_run_id`, the original
`deadline`, and `late_ms`, and the run links back through `resumed_from_run_id`.
Queued rate-limited messages whose slots passed while the server was down leave
one interval apart from the restart, in their original order. A queued message
whose slot has not passed moves back when it would otherwise leave less than an
interval after them. A Condition wait resumes observing and restarts its hold,
because nothing could observe stability while the server was down. When the
hold is longer than the time left, it cannot finish, so the wait leaves on
`timeout` at its original deadline.

Each of these nodes has two settings for a deadline that came due while the
server was down. `missed_deadline` is `deliver` (the default), which sends the
message as soon as the server is back, or `drop`, which discards it. With
`drop`, `missed_grace` (default `30s`) still sends a wait that is late by less
than that, so the seconds a restart takes do not drop anything. Run history
records both late and dropped waits. A dropped wait appears as a canceled run
giving its original deadline and how late it was.

A crash at the moment a wait emits can lose the one message being delivered,
but it never sends it twice. A normal shutdown loses nothing. A wait whose
state cannot be stored, such as one holding more than 1 MiB, still runs but
will not survive a restart, and its run says so.

A redeploy keeps the waits of nodes that keep their ID and type in the new
version. They keep their deadlines, and a changed duration applies to the next
message. The redeploy drops and records the waits of nodes that were removed
or changed type. Disabling a flow drops all of its waits, and deleting it
removes them.

The run panel in the editor lists pending waits with their countdown and a
button to cancel one. Over REST, `GET /api/flows/{id}/waits` lists them and
`DELETE /api/flows/{id}/waits/{node_id}?key=…` cancels one. Over MCP, use
`inspect_flow_state` with `store: "waits"` and `cancel_flow_wait`. Listing needs
`api:runs:read` and cancelling needs `api:runs:cancel`. Run history records a
cancelled wait as a canceled run naming who cancelled it.

A house mode is a select control the flows set. To put guest mode back to home
after four hours, feed the message that sets `guest` into a Trigger with a `4h`
duration whose Expired output sets `home`. A calendar event ending can send
Reset to cancel it early. Because the Trigger's wait is stored, a restart or a
redeploy in those four hours does not leave the house in guest mode. Keep
anything the later branch needs in the message rather than in run variables.

## Locations and presence

A location control (kind `location`) holds where a person or a tracked thing
is. Its primary attribute is `state` (`home`, `not_home`, or a zone name), and
it also holds `latitude`, `longitude`, `accuracy` in metres, and `source`.
`since` is read-only. Karakuri sets it when `state` changes and leaves it
alone when only the coordinates move. A Set Control with a bare value writes
`state`. Writing an object merges, so a phone fix can update the coordinates
without touching the zone. With `restrict_zones` on, the control refuses a
state outside `zones`. A new location starts empty rather than claiming home
or away.

Control Changed's `ignore_writes` decides which flow writes it skips.
`own_flow` (the default) skips the writes of its own flow's Set Control nodes,
so one flow can hand a control to another. `any_flow` skips every flow's
writes, and `none` skips nothing. Dashboard, API, and MCP writes always fire. A
flow's write puts `flow_id` and `run_id` in the payload. When flows start each
other through controls, each handoff adds one to a chain. Past `loop_limit`
(default 8) the trigger records a skipped start, `control loop: Flow C set Y →
Flow D set X → ...`, instead of running. A run started any other way, by a
timer, a delay, or another trigger, starts a new chain.

Control Changed with `attribute: state` fires once per change of zone, not
once per fix. With an attribute set, it fires when that attribute's value
differs from before, whichever part the write named, and the payload carries
`attribute_value` and `attribute_old`. Without an attribute it fires on every
change, coordinates included.

Karakuri has no built-in presence from several sources; you build it as a
flow. This one marks a person home as soon as any source says home, and away
only after every source has said away for ten minutes:

1. One Control Changed watches every source, the first in `control` and the
   rest in `also_watch`. An input takes one wire, so one trigger is how several
   sources reach one decision. Each change carries its own control's `name`
   and `value`. Turn a Home Assistant or MQTT source into a control first, with
   a small flow per source that sets a toggle or a location control from its
   own trigger.
2. A Script keeps each source's latest reading in a flow variable and routes
   on the result:

   ```javascript
   const sources = get("presence_sources", "flow") || {};
   const v = payload.value;
   sources[payload.name] = v === true || v?.state === "home";
   set("presence_sources", sources, "flow");
   return Object.values(sources).some(Boolean) ? { home: "home" } : { away: "not_home" };
   ```

   Declare outputs `home` and `away`. A toggle counts as home when on, a
   location when its state is `home`. A source that has not reported yet
   counts as away. Flow variables are stored, so the readings survive a
   restart.
3. `home` feeds a Set Control writing `home` to the person, and the Reset input
   of a Trigger.
4. `away` feeds that Trigger's input. Set Send now to Nothing, Then wait to
   `10m`, and A message while waiting to Is ignored, so later away readings
   keep the first deadline instead of extending it. Its Expired output feeds a
   Set Control writing `not_home`.

The Trigger's wait is stored, so a restart inside the ten minutes still sets
`not_home` on time, and a home reading from any source in that window cancels
it.

## Home Assistant rooms

HA State, HA Get State, and HA Call Service take areas, floors, devices, and
labels (`area_id`, `floor_id`, `device_id`, `label_id`, comma-separated
registry IDs from `get_ha_registry`) as well as entities. HA State and HA Get
State narrow them with `domain` and `device_class`. The nodes read membership
from Home Assistant's registries at each change, so an entity added to a
watched room counts with no redeploy. The trigger's payload carries the
entity's `area_id`, `area`, and `device_id`. HA Get State with a group returns
`{states, count}`. HA Call Service with only group targets fails when they
reach no entity of the service's domain. You can also add Home Assistant
entities as devices and drive them with the device nodes by kind attribute;
the homeassistant guide has the details.

## openHAB items

**openHAB Item Changed** watches items, group members at any depth, or every
item in a location, narrowed by type and by from and to states. Its payload is
`{item, type, label, old, new, location}`, with `caught_up` after a reconnect.
**openHAB Send Command** sends a templated command to an item or group, and
**openHAB Get Item** reads one. openHAB items added as devices are driven with
the device nodes by kind attribute instead, held to the item's limits. The
openhab guide has the item-type mapping.

## Weather station overrides

**Weather Override** (`weather-override`, from the weather plugin) replaces a
location's current conditions with readings from a local station. Forecasts
served within `radius_km` of the override (default 10) show the readings in
place of the provider's. That covers the dashboard weather widget, Weather
Forecast, and Weather Changed, for either provider. Each reading expires on its
own `max_age` after it was last set (default `15m`, at most `24h`), so a sensor
that stops reporting returns to the provider's value while the others stay.
Where radii overlap, the nearest override wins. `operation: clear` drops every
reading for the location at once.

The location is a `postal_code` (with an ISO `country`, since codes repeat
between countries) or `latitude` and `longitude`. A postal code resolves
through Open-Meteo's geocoding to the centre of the place that lists the code,
not the code's own centroid. US 35757 resolves to Madison, Alabama at
34.6993, -86.7483, so a widget at a house elsewhere in that code needs the
radius to reach it.

The metric fields are `temp`, `feels_like`, `dew_point`, `humidity`,
`pressure` (sea level), `wind_speed`, `wind_deg`, and `uvi`. Each takes a
number or `{{ }}` text, read in `units`, which is `metric` (°C, m/s, hPa) or
`imperial` (°F, mph, inHg). A blank field, or one rendering as empty, `null`,
`unknown`, `unavailable`, or `none`, keeps the provider's value. Anything else
that is not a number, or is outside the metric's physical range, fails the
node. The output payload has the resolved `latitude`, `longitude`, the `set`
readings, and every live reading with `updated_at` and `expires_at`, all in
metric units.

A station publishing Weather Underground-style JSON, such as
`cotech-weather-station/state`, needs MQTT In (`format: json`) into one
override with `units: imperial`, `temp: "{{ payload.tempf }}"`,
`feels_like: "{{ payload.windchillf }}"`, `dew_point: "{{ payload.dewptf }}"`,
`humidity: "{{ payload.humidity }}"`, `pressure: "{{ payload.baromin }}"`,
`wind_speed: "{{ payload.windspeedmph }}"`, and
`wind_deg: "{{ payload.winddir }}"`. A station reporting each sensor on its
own topic can use one override per topic for the same location, and the
readings merge. Overrides are not stored, so after a restart the provider's
values show until the station's next report.

## Media players

A media control (kind `media`) is a player. `state` is primary and one of
`off`, `on`, `idle`, `playing`, `paused`, or `buffering`. `volume` is 0 to 100.
`muted`, `source`, and `position` (seconds) are writable. `title`, `artist`,
`album`, `art`, `duration`, `position_at`, and `sources` are reports. Options
declare the player's features. `volume`, `mute`, `skip`, and `stop` are on by
default, and `seek`, `power`, and `source` are off. The control refuses these
writes before sending anything:

- a write for a feature the player lacks;
- `buffering`;
- a volume outside 0 to 100;
- a `source` the player does not list in `sources` or `source_list`;
- a `position` past `duration`.

Writing `idle` stops; `on` and `off` need `power`.

Next and previous are writes of `command` (`next` or `previous`). A command is
an action rather than a state. The write announces a change of `command`, so
Control Changed with `attribute: command` fires on every press. Nothing is
stored, so Control Value never returns a `command`. A whole-value write may
report `buffering` and the read-only details, which is how a flow mirroring a
player it drives over HTTP keeps the control in step. It may not include
`command`.

ESPHome speakers and MQTT devices of kind media take the same writes through
Command Device. An MQTT device's payload script receives `field` and `value`,
`command` included. A Home Assistant player added as a device takes them too.
Command one that is not added as a device with Call Service
(`media_player.media_pause`, `volume_set`, and so on).

## Calendars

Calendars are connections from the calendar plugin:

- **Calendar (ICS feed)** for any ICS or webcal address, with optional basic
  or bearer authentication.
- **Calendar (CalDAV)** for Nextcloud, iCloud, Fastmail, Radicale, and other
  CalDAV servers.
- **Calendar (Google)** for Google calendars, public ones by API key and
  private ones through an oauth2 connection signed in to Google with the
  `calendar.readonly` scope.

Karakuri encrypts and masks their credentials like any other connection's. A
connection's Test names the calendars it found and counts the coming week's
events. CalDAV and Google connections read the calendars listed in
`calendars`, or all of them when it is empty. Karakuri expands recurring
events in their own time zone and honours excluded, moved, and cancelled
occurrences. A failing refresh keeps serving the last good events for up to a
day and shows the error on the connection.

Calendar Event (`calendar-event`) starts a run at events' starts, ends, or
both. `start_offset` and `end_offset` move each moment earlier (negative) or
later. It filters by `match` (title text), `calendars`, and `all_day`
(`include`, `exclude`, or `only`), and fires once per occurrence of a
recurring event. The message carries `edge` (`start` or `end`), `event`,
`moment`, and `fired_at`. Each start or end fires once, and a restart or a
redeploy never fires it again. The trigger catches up moments that came due
while the server was down, up to a week back, under `missed_deadline` and
`missed_grace` as described in [Pending waits](#pending-waits). Late runs
carry `meta.late_ms`, and dropped ones appear as canceled runs naming the
event. An event that already started when the trigger was first armed does
not fire. A moved or deleted event fires at its new time, or not at all, from
the next refresh (`interval_s`).

Calendar Events (`calendar-events`) is a value source returning
`in_progress` (events under way now) and `upcoming` (events starting within
`window_days`, at most `limit`), with the same filters. Wire it into a
Condition or Switch to ask whether a vacation is on right now.

To drive a house mode from the calendar, feed a Calendar Event with
`edges: both` and `match: Vacation` into a Set Control whose value is
`{{ payload.edge == "start" ? "away" : "home" }}`. For overlapping overrides,
keep the mode to return to in a flow variable. On `start`, save the current
mode only when no override is active and count active overrides up. On `end`,
count them down and restore the saved mode when the count reaches zero.

## Home Assistant targets and observed confirmation

`ha-call-service` accepts comma-separated `entity_id`, `area_id`, `device_id`,
and `label_id` targets. These fields support `{{ }}` expressions and may be
combined. Area, device, and label fields take Home Assistant registry IDs.
Dashboard invocation still enforces its allowed entity list, so broader
registry targets require ordinary flow execution.

Set `confirm: true` to wait after an accepted service call. Choose exactly one
`confirmation_entity_id`, an optional `confirmation_attribute`, and a text
`confirmation_value` (expressions supported). The node reads fresh state every
`confirmation_interval` (default `1s`) until `confirmation_timeout` (default
`30s`). `confirmation_stable_for` requires matching samples for that duration.
The node does not see changes between polls. The original message leaves `out`
on a match, or `timeout` without a match. Metadata includes `ha_result`,
`confirmed`, `confirmation_entity`, and `confirmation_value`. A timeout or
failed observation does not resend an accepted command, and an observation
error uses the error route.

`ha-state.held_for` delays a matching change until the selected state or attribute
has held continuously in the observed event stream. Timers are independent per
entity. Any contrary change cancels its timer before from/to filtering;
unrelated attribute changes leave a state timer alone. Disarm, disconnect,
reconnect, or a lost event invalidates the hold. Empty `held_for` keeps
immediate triggering. Arming does not synthesize a change from the current
state.

After Home Assistant reconnects, Karakuri reads every entity and reports each
one that changed, appeared, or disappeared while the connection was down, once,
with its latest state. It does not replay transitions that came and went
during the outage. `ha-state` fires for these like any change, with
`caught_up: true` in its payload and meta. `ignore_caught_up` fires only for
changes seen as they happened. The first connection after the server starts
reports nothing.

## HTTP retry controls

`http-request.retry_mode` defaults to `safe`, which lets GET, HEAD, and OPTIONS
retry. Choose `all` only for an endpoint that handles repeated commands safely,
and `none` to disable retries. A node-level retry override changes timing and
attempt limits but still respects this method setting and permanent failure
decisions. `retry_statuses` selects comma-separated HTTP error codes from 400
to 599, defaulting to `408,429,500,502,503,504`. Transport failures may retry.
TLS certificate verification, invalid config, malformed response data, and
response size failures do not. With `fail_on_error_status: false`, error
statuses become ordinary output.

The HTTP default is three attempts with 500 ms initial delay, backoff 2, a 5 s
maximum, and 20% jitter. Retry policy `jitter` is a fraction from 0 to 1, and
the editor shows it as a percent. Omitted jitter keeps other nodes' existing
timing. `Retry-After` accepts unsigned seconds or an HTTP date. The wait is the
larger of the jittered backoff and the server's minimum, capped by
`max_delay_ms`; a zero maximum means no configured cap. Invalid or past server
values use backoff. Attempts must be from 1 to 1000, delays nonnegative and
representable, and backoff zero (constant) or at least one. All settings must
be finite.

Error routes retain the incoming payload, topic, metadata, and group. They add
`error`, `attempts`, `node_id`, and, for classified failures, `retryable`,
optional `retry_after_ms`, and `error_details`. HTTP details include the
method, the URL without query or userinfo, the status, selected diagnostic
headers (at most 1024 bytes each), and up to 64 KiB of response body with
`body_truncated`. Run history records returned error bodies. The details
exclude cookie and authorization headers. Pagination emits no partial output,
and retries restart at page one.

## SQL query

The **SQL query** node under Storage reads and writes an isolated SQLite
database. **Database scope** defaults to **This flow**, where every SQL node in
that flow shares its database, including nodes in called subflows. **Global**
selects another database shared by all flows and authors.

Use one statement per execution. The node supports SELECT, INSERT, UPDATE,
DELETE, RETURNING, and ordinary table, index, view, and trigger changes. Create
a table with one node before querying it from another. Each statement commits
on success, and transactions cannot span nodes or runs.

**SQL** is literal text. **Parameters** is an expression evaluated against the
incoming message and bound separately, so message values never become SQL:

```sql
INSERT INTO readings (room, value) VALUES (?, ?)
```

Set Parameters to `[payload.room, payload.value]`. Named parameters also work,
so `WHERE room = :room` takes `{":room": payload.room}`. Include the
placeholder's `:`, `@`, or `$` prefix in object keys. Every placeholder needs a
value, and extra values are errors. Bind strings, finite numbers, booleans,
null, or binary bytes. Encode structured data as JSON text upstream.

The output payload contains `columns`, `rows`, `changes`, and `last_insert_id`.
Rows are objects keyed by column name. Use aliases when selected columns would
have duplicate names. NULL stays null, and binary results are base64 strings.
Topic, metadata, and message grouping survive.

Timeout defaults to 5 seconds and accepts whole seconds from 1 to 60, including
time waiting for the database. Maximum rows defaults to 1000 and accepts 1 to
10000. Results are capped at 1 MiB, SQL at 64 KiB, and each database at 16 MiB.
Four queries can execute concurrently, and queries to one database serialize.
SQL execution failures, interrupted queries, and oversized results preserve
the previous database and route to the error output. SQL failures never retry
automatically, even with a retry override, because a disk error can leave a
commit outcome uncertain.

Each database runs in memory, isolated from the host. It cannot open host files
or Karakuri's application database. The node refuses ATTACH, DETACH, VACUUM,
PRAGMA, extension loading, file functions, virtual tables, temporary objects,
and authored transaction control. Ordinary built-in aggregate, date, window,
and scalar JSON functions are available. JSON table functions use virtual
tables and are unavailable.

Snapshots live under the installation data directory's `sql/` directory as
`flow-<flow UUID>.db` or `global.db`. Include that directory in backups. A
successful write atomically replaces the snapshot before the node emits
success. Databases survive restart and redeployment. Deleting a flow retains
its file for operator cleanup, and a new flow with the same name does not
reuse the deleted identity. Draft tests use the same data as deployed runs.
Standalone node tests have a temporary private identity, while global tests
change the shared database.

## Sending notifications

The Network `notification` node selects a notification connection and one
allowed endpoint ID configured in **Settings, Notifications**. Subject and
message support incoming-message expressions and parameter ports. A successful
send preserves the input envelope on `out`. Provider failures use `error` and
never retry automatically. Timeout defaults to 30 seconds and may leave
delivery uncertain. See [notification services](notifications.md) for
configuration, secret handling, and the complete provider catalog.

## Network sockets

The Network palette includes TCP In, TCP Out, TCP Request, UDP In, UDP Out,
WebSocket In, and WebSocket Out. Select a TCP, UDP, or WebSocket connection
from **Settings, Connections**. Input nodes decode text, binary, base64, or
JSON and start runs. Outputs send once, preserve the input envelope, and route
failures without automatic retry. TCP Request uses a private socket and
replaces the payload with one bounded response.

Replies use the connection and session metadata from the input node, and
replies to retired sessions fail. Listener broadcasts require explicit
selection. The connection's settings hold TCP framing, UDP bind and multicast
settings, and the WebSocket path, origin, and authentication. See
[network socket nodes](network-sockets.md) for configuration and limits.

## Browser questions on agents

`browser-prompt` holds its incoming message while the selected agent opens a
Markdown question. Parallel branches continue. Configure `agent`, `markdown`,
optional `browser_base_url`, `buttons: [{id,label}]`, `timeout` (default `15m`,
`1s` through `24h`), and optional `feedback`/`require_feedback`. A button ID is
its output handle, so labels and order do not change wiring. The selected
output carries the original message with `meta.browser_prompt` holding `id`,
`label`, `text`, `request_id`, and `answered_at`. Unwired choices end the
branch. Timeouts and browser-launch failures route to `error`.

A button only selects its output, and any policy behind it is yours to wire.
Route Retry to a new downstream action or an intentional new run; an
already-dispatched node cannot execute again through a back-edge. Use Stop's
branch to set a flow or global suppression variable if later requests should
be gated. The Instructions editor attaches portable images and files in
`attachments` and references them as `attachment:key`. See
[Browser prompts](browser-prompts.md) for storage limits, access, and
lifecycle.
