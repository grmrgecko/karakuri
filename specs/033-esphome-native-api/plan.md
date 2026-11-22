# Design plan: ESPHome native API

**Feature**: `specs/033-esphome-native-api` | **Date**: 2026-10-31 | **Spec**: [spec.md](spec.md)

## Summary

An `esphome` connection kind holds one node. Its driver keeps a native API
session open (Noise encryption with the node's key, or the legacy password),
lists the node's entities, subscribes to their states and to the node's
action calls, answers pings, and reconnects with backoff, following the node
to a new address through mDNS. The protocol comes from
`github.com/mycontroller-org/esphome_api`, whose `api` and `connection`
packages are generated from ESPHome's current `api.proto` and depend only on
`flynn/noise` and protobuf.

Entities become devices in the registry MQTT and Zigbee2MQTT devices use.
A device gains a transport: `mqtt`, as today, or `esphome`, naming the
entity's object ID instead of topics and scripts. The device runner takes
state from the ESPHome driver instead of a state script and sends commands
through the driver instead of a payload script, behind one command path all
callers share. Everything that already works for a device then works for an
ESPHome entity: dashboards, conditions, viewer permissions, availability,
stale restore, history, and the device flow nodes. Adding entities is a
discovery dialog like Zigbee2MQTT's, and entities that disappear from the
node mark their devices missing.

The spec was revised during planning from a separate dashboard source beside
Home Assistant entities to devices; [research.md](research.md) says why.
[data-model.md](data-model.md) has the columns and mappings,
[contracts/interfaces.md](contracts/interfaces.md) the kind, REST, MCP, and
node shapes, and [quickstart.md](quickstart.md) the checks.

## Technical context

- Language and dependencies: Go 1.26.5, Svelte 5. Added:
  `github.com/mycontroller-org/esphome_api` v1.4.0 (Apache-2.0; packages
  `pkg/api` and `pkg/connection`, plus `pkg/client` for the read loop) and
  `github.com/flynn/noise`. mDNS uses `golang.org/x/net/dns/dnsmessage` and
  `x/net/ipv4`, already dependencies.
- Components:
  - `pkg/integrations/esphome` (new): `kind.go` (schema, secrets, probe),
    `driver.go` (session, keepalive, reconnect, entity list, state cache,
    watchers), `entities.go` (entity types to kinds, state to values, values
    to command requests), `actions.go` (user-defined actions and node action
    calls), `mdns.go` (browse `_esphomelib._tcp`, resolve a node by name).
  - `pkg/nodes/esphomenodes` (new): plugin registering the kind and the
    `esphome-action` and `esphome-node-action` nodes.
  - `pkg/models/mqtt_device.go`: `Transport`, `EntityKey`, `EntityType`.
  - `pkg/mqttrelay`: transport branch in `start`, `Rebind`, and validation;
    `Command`, the shared command path; `Commandable`.
  - `pkg/esphomedevices` (new): listing a node's entities against devices,
    adding, and marking missing. It imports both `mqttrelay` and the driver;
    `mqttrelay` reaches the driver only through an `EntityTransport`
    interface, so neither imports the other.
  - `pkg/api`, `pkg/mcpserver`: discovery and add routes and tools; node
    browse route for the connection form.
  - `pkg/nodes/mqttnodes`: labels say Device rather than MQTT Device; the
    command node uses the shared command path.
  - Frontend: connection form host picker fed by node browse;
    `ESPHomeDiscovery.svelte`; the settings page becomes Devices with a
    transport column.
  - Docs: `docs/esphome.md` served as an MCP guide,
    `docs/mqtt-device-state-script.md` pointing at it, `docs/mcp.md`,
    `docs/flow-authoring.md`.
- Storage: new columns on `mqtt_devices`; ESPHome device states use
  `mqtt_device_states`. No new tables.
- Limits: keepalive ping every 20 seconds, node considered gone after 60
  seconds without a message; reconnect backoff 1 second doubling to 60; mDNS
  browse 3 seconds; at most 500 entities per node.

## Constitution check

- **Failures**: connection errors show on the connection with the node's
  reason; reading or commanding an entity of a disconnected node fails with
  "ESPHome node <name> is not connected", taking the flow's error branch; a
  command is validated by the control kind before it is sent; nothing is
  optimistic. A malformed message from a node drops the session and
  reconnects.
- **Drafts and deployment**: the new nodes' fields come from metadata;
  existing device node type IDs are unchanged, only labels change, so
  deployed flows are untouched.
- **Permissions**: the connection kind follows connection permissions;
  discovery, adding, and node browse need `api:connections:write`; commands
  follow the existing device command rule (connection invoke or a dashboard
  that grants the device). Node action calls from flows need flow edit like
  any node; the `esphome-action` MCP path needs `api:connections:invoke`.
- **Existing state**: `transport` defaults to `mqtt`, so existing devices are
  unchanged. Encryption keys are stored as connection secrets under the
  existing `security.encryption_key`. No migration of existing rows beyond the
  column default.
- **Verification**: `make dev`, `make test`, a simulated node speaking the
  native API in tests (plaintext and Noise), the quickstart against a real
  node when one is provided, and a browser check of the connection form,
  discovery dialog, and rows.

## Design and contracts

### Session

```text
connect(host:port) -> Noise handshake with key, or plaintext
Hello -> (Connect with password if set) -> DeviceInfo
ListEntities until ListEntitiesDone -> entity table by key and object ID
SubscribeStates, SubscribeHomeassistantServices
loop: state responses -> cache by object ID -> watchers
      node action calls -> action watchers
      every 20s Ping; 60s silence -> drop and reconnect
on error: status error, backoff 1s..60s, re-resolve the node's name by mDNS
          when the configured host does not answer
```

Status follows the existing connection statuses, so feature 032's watcher
already makes every device on a dropped node unknown.

### Devices

`start` for an `esphome` device watches its entity on the driver; each state
update becomes values merged as a decoded message is today, then published,
persisted, and fanned out the same way. `Command(ctx, device, field, value,
on, previous)` resolves the field against the kind, then for `mqtt` runs the
payload script and publishes, for `esphome` builds the entity's command
request. REST, MCP, and the device node call it. Availability needs no new
rule: a disconnected node makes its devices unknown; an entity whose state the
node reports as missing keeps its last values and is not online until it
reports one.

### Adding and following

The discovery dialog lists the node's entities with the proposed kind and
whether each is added, all addable ones ticked. Adding creates devices named
`<node name> <entity name>`. On each entity list after a connect, devices on
that connection whose object ID is gone are marked missing, and ones that
return are cleared.

### Node actions

`esphome-action` calls a user-defined action with arguments checked against
its declared types. `esphome-node-action` is a trigger on the node's
Home Assistant style action calls (`homeassistant.action` in its YAML),
carrying the action name and data.

## Verification

| Scenario | Test |
| --- | --- |
| US1 | Kind validation and probe against a simulated node with and without encryption; wrong key; legacy password; mDNS response parsing from recorded packets. Live: quickstart 1 with a real node. |
| US2 | Entity mapping tests per type; command request building per kind; device start, state merge, and command through the simulated node. Live: quickstart 2. |
| US3 | Node tests for `esphome-action` argument checking and the trigger. Live: quickstart 3. |
| US4 | Driver test: silent node dropped after the timeout, reconnect, re-list; devices unknown then online. Live: quickstart 4. |

## Risks and open decisions

- The device table and broker topics keep their `mqtt_` names; renaming
  them is churn with no behaviour, so only labels change.
- No ESPHome node announces itself on the development subnet and none was
  provided, so interoperability with a real node is untested; the simulated
  node, built to the library's client framing and ESPHome's Noise responder
  handshake, is the evidence.
- The library tracks ESPHome's protocol; a future ESPHome API change needs a
  library update.
- Media players, alarm panels, sirens, valves, updates, and cameras are listed
  but not addable in this feature.
