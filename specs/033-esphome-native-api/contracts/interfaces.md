# Contract: ESPHome interfaces

## Connection kind

`esphome`, label "ESPHome", fields as in [data-model.md](../data-model.md).
Test reports, through the probe detail of feature 030:

```text
garage-controller (esp32dev), ESPHome 2026.10.1, MAC 24:6F:28:AA:BB:CC, 14 entities
```

Failures name the cause: `connecting to 10.0.20.14:6053: connection refused`,
`encryption handshake failed: wrong key`, `the node requires an encryption
key`, `invalid password`.

## REST

All need `api:connections:write` unless noted.

- `GET /api/connections/esphome/nodes` browses mDNS for 3 seconds and returns
  `{nodes: [{name, host, port, mac, version, board}]}`.
- `GET /api/mqtt_devices/esphome?connection_id=<uuid>` returns
  `{devices: [DiscoveredEntity]}` for a connected node.
- `POST /api/mqtt_devices/esphome` with `{connection_id, object_ids: [..]}`
  returns `{added: [View], failed: [{object_id, error}]}`.
- `POST /api/connections/{id}/esphome/actions/{name}` with `{args: {..}}`
  calls a user-defined action (`api:connections:invoke`).
- Device views add `transport`, `entity_key`, and `entity_type`.

## MCP

- `discover_esphome_nodes {}`
- `discover_esphome_entities {connection_id}`
- `add_esphome_entities {connection_id, object_ids}`
- `call_esphome_action {connection_id, action, args}`

## Nodes

`esphome-action`: config `connection` (ESPHome), `action` (picker of the
node's actions), `args` (JSON object, `{{ }}` allowed). Arguments are checked
against declared types: bool, int, float, string, and their arrays. Output is
the input message; failures take the error branch.

`esphome-node-action` (trigger): config `connection`, optional `action`
filter. Payload:

```json
{ "action": "notify.mobile_app", "data": { "message": "Garage open" },
  "is_event": false, "connection_id": "…", "node": "garage-controller" }
```

The device nodes keep their type IDs; labels become Device Value, Command
Device, Device Changed, and Device Availability.

## Command path

```go
// Command checks an interaction against the device's kind and sends it over
// the device's transport. It never updates state.
func Command(ctx context.Context, d *models.MQTTDevice, field string, value any, on *bool, previous any) (CommandResult, error)
```

`CommandResult` carries the topic for MQTT (empty when the script chose
nothing) and nothing for ESPHome. Errors: kind refusal (validation),
`OfflineError`, "takes no commands", and transport errors.
