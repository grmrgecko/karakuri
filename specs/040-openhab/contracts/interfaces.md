# Contract: openHAB connection interfaces

## Connection kind `openhab`

Fields `base_url` and `token` (secret). **Test** answers `openHAB 5.2.3,
42 items, authenticated` (or `anonymous`), or the failure: `unauthorized:
check the API token`, `not found: is this an openHAB server?`, or the network
error.

## REST

| Route | Permission | Notes |
| --- | --- | --- |
| `GET /api/connections/{id}/openhab/items` | connection read | Items with label, type, state, location and floor labels, read only, sorted by floor, location, label, with no location last |

Item devices use the existing device routes; **Add from openHAB** uses
`GET /api/mqtt_devices/entities` and `POST /api/mqtt_devices/entities`
through `entitydevices.SourceFor`.

## MCP

`list_openhab_items` (connection read; filters `location` by name or ID,
`type`, `filter` text), `send_openhab_command` (connection invoke; item and
command); `discover_entities` and `add_entities` accept an openHAB connection.

## Nodes (folder openHAB)

- `openhab-item-changed` (trigger): connection, items (picker), groups,
  location, item type, from, to, ignore caught-up changes. Payload
  `{item, type, old, new, label, location, caught_up?}`; topic the item name.
- `openhab-send-command` (action): connection, item (picker; a group is an
  item), command (templated). Passes the input through; a refusal fails the
  node with openHAB's status and reason.
- `openhab-get-item` (pure value): connection, item. Payload `{item, state,
  type, label, location}`.

## Make targets

`make openhab-up` starts `test/openhab/compose.yaml`, waits for the REST API,
creates the test admin and an API token, and writes the token to
`build/openhab-token`; `make openhab-down` removes the container and its
data; `make test TARGET=openhab` runs the `karakuri_openhab` tagged suite in
`test/openhab`.
