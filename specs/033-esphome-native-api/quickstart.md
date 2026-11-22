# Quickstart: ESPHome native API

Prerequisites: `make dev`, `systemctl --user restart karakuri-dev.service`, the
MCP token, and, for live checks, a reachable ESPHome node with its API
encryption key, chosen so that toggling its entities is harmless. Without a
node, scenarios 1 to 4 are covered by the simulated node in tests and the
report says so.

## 1. Connect

1. `discover_esphome_nodes` lists nodes announced on the subnet.
2. Save an `esphome` connection with the node's host and key; `test_connection`
   reports name, board, version, MAC, and entity count.
3. Save with a wrong key and expect the handshake error; restore the key.

## 2. Entities as devices

1. `discover_esphome_entities` lists the node's entities with kinds.
2. `add_esphome_entities` with all addable object IDs; expect devices named
   `<node> <entity>` in `list_mqtt_devices` with `transport: esphome`.
3. Bind a dashboard row to a switch or light device, operate it, and expect
   the node's state to come back on the row.

## 3. Flows

1. A flow with Device Changed on a sensor device and a debug node; change the
   reading on the node; expect a run.
2. `call_esphome_action` on a user-defined action, or the `esphome-action`
   node in a test flow; expect the node to run it.

## 4. Offline

1. Unplug or isolate the node; within 60 seconds expect the connection in
   error and its devices unknown and drawn unavailable.
2. Bring it back; expect reconnection, fresh states, and devices online.

## 5. Browser

1. The connection form's host picker lists found nodes.
2. The Add from ESPHome dialog lists entities with kinds and added marks.
3. Settled screenshots and a clean console.

## Cleanup

Delete test devices, flows, dashboards, and the test connection; report what
was created.
