# Design plan: Connections, MQTT devices, and controls

**Feature**: `004-connections-devices` | **Date**: 2026-10-04 | **Spec**:
[spec.md](spec.md)
**Status**: Implemented baseline

## Summary

Connect automations to Home Assistant and MQTT, represent devices with typed controls,
and discover real input shapes before issuing actions.

This plan records the design supporting the baseline requirements. It is not
a pending implementation queue. Requirement IDs are local to this feature.

## Technical context and design

`pkg/connmgr` owns drivers while `pkg/connops` validates and persists
connections. Connection kind registration supplies config schemas, secrets,
factory, and test hooks to both backend and UI. MQTT uses autopaho; Home
Assistant uses its WebSocket API. `pkg/controls` defines kinds and the local
store; `pkg/mqttrelay` subscribes devices and runs their state/payload scripts.

The host owns control semantics and payload construction. Widgets submit an
interaction rather than executable protocol code. HA Assist is a flow trigger
with a response input, paired with `homeassistant/custom_components/karakuri_assist`
through HA events and the `karakuri_assist.respond` service.

`pkg/integrations/socket` supplies shared TCP, UDP and WebSocket drivers. Core
registers their settings schemas and seven socket nodes. Driver generations
own listeners, reconnecting clients and bounded subscriptions; TCP requests
use execution-owned sockets. [Feature 019](../019-network-socket-nodes/plan.md)
records framing, session identity, TLS, multicast and lifecycle constraints.

## State and data model

Connection configs persist encrypted; drivers and subscriptions are live
state. Controls persist their values. MQTT device definitions persist. Decoded state is
written to `mqtt_device_states` at most every five seconds and restored on start marked
stale until the device reports again, under feature 029; error information lives in
memory and resets on restart. State messages patch
fields. Commands do not
invent observed state for one-way devices. HA chat logs retain complete AI
response envelopes, including session and provider replay information.

## Contracts and failure handling

Connection reads, writes, and invocation have separate permissions. HA state
reads and device commands also support the dashboard scopes in spec 007. Raw
MQTT publish/subscription remains permission-only. Device test tools return
`action: ignored` for skipped state and `action: none` for commands with no topic.
HA Assist requests carry request/agent IDs, text, conversation metadata, and
transcript; responses correlate by request ID and preserve continuation state.
Masked secret sentinels preserve stored values during config replacement. Undecryptable
configs return an empty config plus an error status, and failed driver startup does not
undo the saved definition. Command coercion applies to devices with declared kinds;
kindless definitions pass commands through, while unknown nonempty kinds fail.

The manager retains lifecycle reports independently of driver instances. Atomic
snapshot/watch queues feed `connection-status` triggers without blocking driver
callbacks on flow execution. Queues hold 64 reports, drop the oldest on overflow
and log the loss. Generation checks exclude retired driver callbacks. Repeated
states keep their entry timestamp; shutdown closes all watchers. See
[server utility nodes](../016-server-utility-nodes/plan.md).

## Constitution check

- Keep the authorization and failure boundaries described above explicit.
- Route authoring through shared operations and preserve draft/deployment separation
  where applicable.
- This baseline adds no schema, wire, identity, or encryption-format migration. Future
  changes must assess the affected persistent state before implementation.
- Use the component checks below and the service handoff checks in `AGENTS.md`.

## Verification

Run `make dev TARGET=server` and `make test TARGET=server`, which also runs the HA
component tests. Driver, control, MQTT relay, script, and Assist fixtures cover
validation and correlation. Prefer MCP discovery and script dry-runs before live
actions. Any real HA service or MQTT command check must use a disposable known target.

These are the acceptance checks for changes to this feature. They do not claim
that every live scenario ran while the baseline documents were written.

## Risks and limits

Device commands perform real external actions. MQTT has no automatic device discovery
here; device definitions are authored explicitly. The Assist component targets the
documented Home Assistant interfaces.

New behavior needs reviewed requirements and an update to this plan.

## References

- [Server and agent audit](../server-agent-audit.md)

- [System architecture](../architecture.md)
- [docs/mqtt-device-state-script.md](../../docs/mqtt-device-state-script.md)
- [docs/mqtt-device-payload-script.md](../../docs/mqtt-device-payload-script.md)
- [homeassistant/README.md](../../homeassistant/README.md)
- [pkg/connops/connops.go](../../pkg/connops/connops.go)
- [pkg/controls/kinds.go](../../pkg/controls/kinds.go)
- [pkg/mqttrelay/device.go](../../pkg/mqttrelay/device.go)

## Reconnect catch-up extension (FR-023 to FR-025)

The Home Assistant driver keeps the last state it knows for every entity: the
`get_states` answer it reads on the first connection, updated by every
`state_changed` event. That first read only seeds the map and reports nothing,
so a server start is not an outage. The map lives outside the driver, keyed by
connection ID and websocket address, so a driver replaced by disabling and
enabling the connection or editing it catches up from what the old one knew;
the live check found this, since disabling a connection replaces its driver
rather than dropping its socket.

On every later session the driver re-subscribes `state_changed` first and then
reads `get_states`, so no change can fall between the read and the live
stream. It then compares each entity with the known map under the driver lock
the event dispatcher also takes:

```text
fresh state != known state (state string or attributes)
  and fresh.last_updated > known.last_updated   -> report (known, fresh), caught up
entity in known but not in fresh                -> report (known, null), caught up
entity in fresh but not in known                -> report (null, fresh), caught up
```

The `last_updated` check drops a fresh read that a live event has already
overtaken. A failed read reports nothing and leaves the catch-up pending for the
next session. Reports go to the same state-change handlers as live events,
through a handler variant that receives a `CaughtUp` flag
(`SubscribeStateChangesDetailed(func(StateChange))`); the existing
`SubscribeStateChanges` wraps it and ignores the flag. Because
`cmd/karakuri/serve.go` publishes every handler call to
`ha:<conn>:state:<entity>`, dashboards, history forwarding, energy recording,
and the device relay receive caught-up changes on the topics they already
read: each widget updates once for its own changed entity, with no frontend
change. The broker message gains `caught_up: true`.

`ha-state` gains **Ignore changes caught up after a reconnect** (default off).
Its message carries `caught_up` in payload and meta. A caught-up change starts a
`held_for` timer like any change, in the new connection generation.

## Control handoff extension (FR-026, FR-027)

`controls.Change` gains a writer: `Source` as today, plus `FlowID`, `RunID`,
`Depth`, and `Chain` (the flow and control names that led here, at most the
loop limit). The store's write methods take a `controls.Writer` instead of a
bare source string; dashboard, API, and MCP writers have no flow. Feature 037
adds `Actor`, the `events.Actor` from the request or node context, to the
writer and the change, so the event log names the user, API key, or flow.

The engine gives nodes the run's identity and its control chain:
`ExecContext.RunID()` and `ExecContext.ControlChain() (depth int, chain
[]string)`, read from the trigger message's `meta.control_chain` and
`meta.control_depth` when the run started. The Set Control node passes them,
so a flow's write carries `Depth` equal to its run's depth and the chain
extended by `<flow name> set <control name>`.

Control Changed replaces `ignore_flow_writes` with `ignore_writes`:
`own_flow` (default), `any_flow`, or `none`. A saved
`ignore_flow_writes: false` reads as `none` and a saved `true` as `own_flow`.
For a flow's write it sets `meta.control_depth` to `Depth + 1` and
`meta.control_chain` to the chain. When `Depth + 1` exceeds the loop limit (8,
a field on the node with that default), the trigger records a skipped start
through `engine.TriggerRecorder` with the reason `control loop: <chain>` and
logs it at warn, instead of starting a run. A run started any other way has
depth 0, so delays, waits, and other triggers end a chain.

## Extension verification

| Scenario | Test |
| --- | --- |
| US4 | Driver test against `hatest`: seed, drop the socket, change, add, and remove entities while down, reconnect, and check one caught-up report per changed entity and none for the rest; a live event arriving during the read is not reported backwards; a failed read retries next session; the first connection reports nothing. `ha-state` test for the caught-up mark and the ignore option. Live: the lab Home Assistant read only, by blocking the connection (disable and enable it) and comparing the reported entities with states read before and after. |
| US5 | Control store test for writers; Control Changed tests for own-flow, any-flow, and none; a two-flow loop through the engine stopping at depth 8 with one skipped start and its reason; a write from a run started by a timer having depth 0. Live: two test flows and a test control through MCP. |

## MQTT delivery extension (FR-010, FR-013, FR-028, FR-029)

### Data

`models.MQTTDevice` gains five columns, all defaulting to today's behavior so
existing rows, backups, and Zigbee2MQTT-generated devices are unchanged:

| Column | Type | Default | Rule |
| --- | --- | --- | --- |
| `state_qos` | int | 0 | 0, 1, or 2; state and availability subscriptions |
| `command_qos` | int | 0 | 0, 1, or 2; command publishes |
| `command_retain` | bool | false | command publishes |
| `state_timeout_ms` | int | 0 | 0 means 250; otherwise 50 to 5000 |
| `payload_timeout_ms` | int | 0 | 0 means 2000; otherwise 100 to 10000 |

`mqttrelay.ValidateDefinition` refuses values outside these ranges with the
range named; it applies to MQTT transport devices only. Entity devices
(ESPHome, Frigate, Home Assistant) ignore them.

### Driver

`pkg/integrations/mqtt` delivers a `Message{Topic, Payload, Retained, QoS}`
built from paho's `PublishReceived` packet (`Retain` and `QoS`).
`SubscribeMessages(filter, qos, func(Message))` registers a handler that
receives it; `Subscribe(filter, qos, func(topic, payload))` stays as a wrapper,
so the raw relay, the MQTT nodes, and capture are unchanged. Karakuri connects
with MQTT 5 and leaves retain-as-published off, so the broker sets `Retain`
only on messages it delivers from its retained store when a subscription is
made, which is the meaning FR-029 needs.

The driver records the QoS the broker granted per filter from the SUBACK
reason codes (`GrantedQoS(filter) (byte, bool)`), separately from the requested
QoS the reconcile loop compares. Two handlers on one filter still share the
higher requested QoS, as today.

### Relay

- `subscribe` and the availability subscription use `state_qos` and
  `SubscribeMessages`; `receive` and `decode` carry `retained`, which the state
  script reads from a preamble `const {binary, state, retained} = meta;`. The
  state script test API and MCP `test_mqtt_device_script` take an optional
  `retained` and the device's limit.
- `decode` runs under the device's state limit; a timeout is a decode failure,
  reported and logged as one.
- `RunPayloadScript` takes the device's payload limit. `ReadPayloadResult`
  reads optional `qos` (0 to 2) and `retain` (boolean) beside `topic` and
  `payload`; an invalid value fails the command with the reason.
  `PublishCommand` publishes with the result's values, falling back to the
  device's `command_qos` and `command_retain`. The Command result and the
  event log entry carry the QoS and retain used.
- `View` gains the five fields and `state_qos_granted` when the broker
  granted less than asked.

### Interfaces

- REST `/api/mqtt_devices` and MCP `save_mqtt_device`, `list_mqtt_devices`:
  `state_qos`, `command_qos`, `command_retain`, `state_timeout_ms`,
  `payload_timeout_ms`.
- Settings, MQTT devices editor: a **Delivery** section with state QoS,
  command QoS, and retain commands, and the two time limits in milliseconds
  with their defaults as placeholders.
- Docs: `docs/mqtt-device-state-script.md` (`retained`, the time limit) and
  `docs/mqtt-device-payload-script.md` (`qos`, `retain`, clearing a retained
  command, the time limit).

### Extension verification (US6)

| Scenario | Test |
| --- | --- |
| Command QoS and retain | Relay test with an embedded broker or the publish fake: device defaults and per-message overrides reach `Publish`; invalid `qos` refused |
| Retained flag | Driver test against an embedded MQTT 5 broker: a retained message on subscribe arrives `Retained` true, a live one false; state script sees `retained` |
| Granted QoS | Driver test recording SUBACK reasons |
| Time limits | Relay test: a busy-loop script succeeds with a raised limit and fails with the default; out-of-range limits refused on save |
| Live | A test device under `karakuri-test` on the lab broker with command QoS 1 and retain; capture the command with its QoS and retain flag; publish a retained state and a live state and read `retained`; clear the retained topics |

## Stateful HA node extension

HA service targets include entity/area/device/label IDs. Optional confirmation
polls fresh `get_states` reads for one entity after one accepted call; timeout
has its own output and read failures are classified as permanent for that call.
HA state triggers track independent held timers. Connection generations change
on connect, disconnect and event queue overflow so elapsed time across a gap
cannot satisfy a continuous observed hold. See `../015-stateful-flow-nodes/`.
