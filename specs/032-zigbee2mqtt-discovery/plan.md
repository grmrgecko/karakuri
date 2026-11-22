# Design plan: Zigbee2MQTT discovery and device availability

**Feature**: `specs/032-zigbee2mqtt-discovery` | **Date**: 2026-10-31 | **Spec**: [spec.md](spec.md)

## Summary

Availability is one generic mechanism on every MQTT device: an optional
availability topic with online and offline payloads, an optional expected
reporting interval, and the state of the device's connection. The relay keeps
an availability per running device beside its values, persists it with them,
publishes it on the device's broker topic, and refuses commands to offline
devices in `PublishCommand`, which every command path will use. Zigbee2MQTT
needs no special case for availability: adding a device from discovery fills
in its availability topic, and the payload match also accepts Zigbee2MQTT's
JSON form `{"state":"online"}`.

Discovery is a new package, `pkg/zigbee2mqtt`. It reads the retained device
list on `<base>/bridge/devices`, maps each device's exposes to a control kind,
options, a state script, and a payload script, and creates ordinary MQTT
devices that record their IEEE address and base topic. A sync watcher per
connection and base topic in use keeps those devices' state and availability
topics on the current friendly name and marks devices missing when they leave
the list. Payload scripts command `<base>/<ieee>/set`, which Zigbee2MQTT
accepts, so a rename never touches a script.

[research.md](research.md) records the decisions,
[data-model.md](data-model.md) the columns and stored state,
[contracts/interfaces.md](contracts/interfaces.md) the REST, MCP, node, and
broker shapes, and [quickstart.md](quickstart.md) the checks.

## Technical context

- Language and dependencies: Go 1.26.5 and Svelte 5 with TypeScript. No new
  dependencies. The MQTT driver (`pkg/integrations/mqtt`, autopaho) already
  delivers retained messages to new subscriptions.
- Components:
  - `pkg/models/mqtt_device.go`, `mqtt_device_state.go`: availability
    settings, Zigbee2MQTT origin, `missing`, stored availability.
  - `pkg/mqttrelay`: availability state per device, availability topic
    subscription, interval sweep, connection status watch, persistence,
    broker payload, `PublishCommand` refusal; `view.go` definition fields.
  - `pkg/zigbee2mqtt` (new): `bridge.go` (device list types and parsing),
    `exposes.go` (kind, options, field mapping), `scripts.go` (generated state
    and payload scripts), `discover.go` (one-shot read with timeout),
    `sync.go` (rename and missing watcher), `adopt.go` (create and regenerate
    devices).
  - `pkg/api/mqtt_device_handler.go`: discover, add, and regenerate routes;
    command route through `PublishCommand`.
  - `pkg/mcpserver/tools_devices.go`: discovery and add tools; availability
    in device reads and saves.
  - `pkg/nodes/mqttnodes/devices.go`: `mqtt-device-availability` trigger;
    `meta.availability` on `mqtt-device-get` and `mqtt-device-changed`.
  - `pkg/historyfwd/convert.go`: availability as an attribute sample.
  - `cmd/karakuri/serve.go`: connection status into the relay; the sync
    watchers started after devices load.
  - `frontend/src/routes/settings/MQTTDevices.svelte` and a new
    `Zigbee2MQTTDiscovery.svelte`: discovery dialog, availability settings,
    availability and missing badges.
  - `plugins/core/widgets/shared/entitySource.ts`: a device is unavailable
    while offline or unknown.
  - Docs: `docs/mqtt-device-state-script.md` (availability and Zigbee2MQTT
    sections, served to MCP as a guide), `docs/flow-authoring.md` (the trigger), `docs/mcp.md`.
- Storage: new nullable columns on `mqtt_devices` and `mqtt_device_states`,
  added by the existing GORM auto-migration. No new tables.
- Limits: discovery waits at most 5 seconds for the device list; the
  interval sweep runs every 15 seconds; an expected interval below 60 seconds
  is refused.

## Constitution check

- **Failures**: a discovery that hears nothing fails with "Zigbee2MQTT not
  found on <base>/bridge/devices"; adding is per device and reports each
  failure without undoing the others. Unknown availability payloads are
  ignored and shown on the device. A refused command is an error at every
  command path, so a flow's error branch and retry policy apply; retrying an
  offline refusal publishes nothing.
- **Drafts and deployment**: the trigger and its fields come from node
  metadata, so editor, REST, MCP, and validation agree. Adding devices
  changes definitions, not flows.
- **Permissions**: discovery, adding, and regenerating need
  `api:connections:write`, as creating a device does. Dashboard viewers get
  availability on the device topic they may already subscribe to and in the
  reduced device list.
- **Existing state**: columns are added with defaults that keep today's
  behaviour: no availability topic, no interval, commands allowed only while
  not offline, which an existing device never is unless it gets settings.
  Existing devices with no settings are online while their connection is up.
  No key or identity change.
- **Verification**: `make dev`, `make test TARGET=server`,
  `make test TARGET=frontend`, the quickstart on the lab broker (10.0.10.5),
  reading its real Zigbee2MQTT list without commanding a device and driving
  synthetic devices under a `karakuri-test` base topic, a browser check of the
  discovery dialog and device settings, and a restart of the service.

## Design and contracts

### Availability

```text
connection not connected            -> unknown (source "connection")
availability topic set:
  payload == online or {"state": online}   -> online  (source "topic")
  payload == offline or {"state": offline} -> offline (source "topic")
  other payload                            -> unchanged, noted on the device
  connection up, no message yet            -> unknown
expect_every set, no topic:
  last message older than expect_every     -> offline (source "interval")
  any message                              -> online  (source "interval")
neither                                    -> online while connected
```

With both a topic and an interval, the topic decides and the interval only
marks a device offline that the topic still calls online, for firmware whose
last-will never fires. Each change sets `availability_at`, marks state dirty,
publishes on `mqttdevice:<id>` with the values, and calls availability
watchers. Restored availability is stale like values; an offline restore
draws offline, as the spec requires.

The relay watches each connection's status through a small interface that
`serve.go` satisfies with `connmgr.Manager.WatchStatus`, so a broker going down
marks its devices unknown at once.

### Commands

`PublishCommand` refuses `device %q is offline since <time>; allow commands
while offline on the device to send anyway` when the running device is
offline and `offline_commands` is off. The REST command handler moves onto
`PublishCommand`; the node and MCP paths already use it.

### Discovery and adding

```text
discover(connection, base):
  subscribe <base>/bridge/devices; wait <= 5s for the retained list
  for each device not Coordinator and not disabled:
    proposal = map(exposes)
    added = device whose z2m_ieee == ieee and z2m_base == base
add(connection, base, ieees):
  re-read the list; for each ieee: proposal -> Definition with
    topics [<base>/<friendly>], availability_topic <base>/<friendly>/availability,
    z2m_ieee, z2m_base; name = friendly, suffixed if taken
  create through the existing device save path
regenerate(id): rebuild kind, options, scripts from the current list
```

The exposes mapping and generated scripts are in
[research.md](research.md) and [contracts/interfaces.md](contracts/interfaces.md).

### Sync

On start and whenever a device with `z2m_ieee` is saved or deleted, the relay
owner (`serve.go`) asks `zigbee2mqtt.Sync` to hold one subscription to
`<base>/bridge/devices` per connection and base in use. Each list received:

```text
for each added device on that connection and base:
  found by ieee: if friendly changed, rewrite topics and availability topic
                 that still name the old friendly; clear missing
  not found:     set missing (availability stays as reported)
```

Rewrites go through the device save path, so the relay rebinds as it does for
any edit and keeps state, since the kind did not change.

## Verification

| Scenario | Test |
| --- | --- |
| US1 | `zigbee2mqtt` tests on the recorded 2.14.2 list from the lab broker and a synthetic list covering the kinds it lacks: mapping per device type, generated scripts run through `TestStateScript` and the payload script runner against recorded messages, coordinator and disabled devices skipped, already-added detection, name suffixes. Live: quickstart 1 and 2. |
| US2 | `mqttrelay` tests: topic, JSON form, unknown payload, interval sweep with a fake clock, connection status, persistence and stale restore, `PublishCommand` refusal and override. Node test for the availability trigger. `entitySource` test for unavailable. Live: quickstart 3 and 4. |
| US3 | Sync tests: rename rewrites topics only, removal sets missing, return clears it, hand-edited scripts kept. Live: quickstart 5. |
| US4 | Covered by the availability tests with custom payloads. Live: quickstart 3. |

## Risks and open decisions

- Multi-endpoint devices (a double switch with `state_l1` and `state_l2`) map
  their first endpoint to the kind and keep the others as state fields; a
  second endpoint is commanded by adding a second device by hand. Splitting
  endpoints into devices is left for later.
- Generated scripts read Zigbee2MQTT's property names; a converter that
  renames a property after an update shows as a missing value until the
  device is regenerated.
- Zigbee2MQTT's own availability feature must be on for Zigbee devices to
  report offline; the guide says so. Without it, the interval setting still
  works.
