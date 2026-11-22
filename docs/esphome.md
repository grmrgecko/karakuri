# ESPHome

An ESPHome connection talks to one ESPHome node over its native API, as Home
Assistant does, so the node needs no change and Home Assistant can stay
connected to it at the same time. The node's entities can be added as devices
beside MQTT and Zigbee2MQTT devices, and flows can call the node's actions.

## Connecting

Add a connection of kind **ESPHome** under **Connections**:

| Field | Meaning |
| --- | --- |
| Host | The node's address or name, such as `garage-controller.local`. **Find nodes** lists the nodes announcing themselves on the server's network and fills host and port. Nodes on another subnet do not answer the search, so type their address. |
| Port | 6053 unless the node's `api:` block sets another. |
| Encryption key | The base64 key from the node's `api: encryption: key:`. Stored encrypted and never shown again. |
| Password | The legacy API password, for nodes from before encryption keys. Leave it empty when the node uses a key. |

**Test** connects once and reports the node's name, board, ESPHome version,
MAC address, and entity count, or why it failed: `connection refused` when
nothing listens, `wrong key` when the encryption handshake fails, `the node
requires an encryption key` when the key is missing, or `invalid password`.

The connection stays open. It pings the node every 20 seconds and counts the
node gone when a ping goes unanswered for 10. It then reconnects on its own,
waiting 1 second and doubling up to a minute, and looks the node up by name on
the network in case its address changed.

## Entities as devices

**Settings, Devices, Add from ESPHome** (or `discover_esphome_entities` and
`add_esphome_entities` with the ESPHome connection) lists a connected node's
entities with the device kind each becomes. Every entity that can be added
starts ticked. Devices are named after the node and the entity, such as
`garage Porch`, and can be renamed. With auto import on the connection, every
entity is added when the node connects, without opening the list; the Device
auto import guide under **Help** covers it.

| ESPHome entity | Device kind | Takes commands |
| --- | --- | --- |
| switch | Toggle | yes |
| binary_sensor | Toggle, labelled by device class (door: Open and Closed, motion: Detected and Clear) | no |
| light | Light, with brightness, colour, colour temperature, and effects as the node supports | yes |
| cover | Cover, with position and tilt when supported | yes |
| fan | Fan, with speed as a percentage of the node's speed steps, oscillation, direction, and presets | yes |
| lock | Lock | yes, unless it needs a code |
| climate | Thermostat, with modes, target or range, current temperature, action, and fan modes | yes |
| number | Number, with the node's bounds, step, and unit | yes |
| select | Dropdown | yes |
| text | Text | yes |
| button | Buttons, one button | yes |
| sensor | Number reading with the node's unit and precision | no |
| text_sensor | Text reading | no |
| event | Text reading of the last event type | no |
| media_player | Media player, with volume, mute, stop, and power as the node reports them | yes |

Alarm panels, sirens, valves, update entities, date and time entities, and
cameras are listed but cannot be added yet. ESPHome's API carries a media
player's state, volume, and mute but no track details, next or previous, seek,
or source. Those controls are off, karakuri refuses a skip before sending it,
and an announcement plays as `playing`.

An ESPHome device works like any device: dashboard rows, conditions, history
forwarding, and the Device Value, Command Device, Device Changed, and Device
Availability nodes. Karakuri checks a command against the kind, then sends it
to the node. The device's value changes when the node reports the new state,
never before.

A device follows its entity by object ID, which survives reflashing. When a
node no longer lists an entity, the entity's device is marked missing and drawn
as unavailable until the entity returns.

A device is online while its node is connected and unknown while it is not.
The connection status trigger and Device Availability both see the change.
After a restart, devices show their earlier values marked stale until the node
reports.

## Nodes

Nodes are in the **ESPHome** folder.

**ESPHome Action** calls one of the node's own actions, declared under
`api: actions:` in its YAML. `call_esphome_action` does the same over MCP. Give
arguments by name. Karakuri checks them against the declared types (bool, int,
float, string, and lists of them). A string that is one whole `{{ }}` keeps
the expression's type, so `"times": "{{ payload.count }}"` sends a number.

```json
{ "connection": "<esphome connection>", "action": "beep", "args": { "times": "{{ payload.count }}" } }
```

**ESPHome Node Action** starts a run when a node calls `homeassistant.action`
or fires `homeassistant.event`, which a node uses to ask Home Assistant to act.
The message carries `action`, `data`, `is_event`, `connection_id`, and `node`.
Set `action` to take only that name.

## Limits

| Limit | Value |
| --- | --- |
| Entities per node | 500 |

The integration has no Bluetooth proxy, voice assistant, camera streams, or
firmware updates, and does not include the ESPHome dashboard or compiler.
