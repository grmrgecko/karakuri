# Data model: ESPHome native API

## Connection config (`esphome` kind)

| Field | Type | Secret | Meaning |
| --- | --- | --- | --- |
| `host` | string | no | Address or `name.local`. Required. |
| `port` | integer | no | Default 6053. |
| `encryption_key` | string | yes | Base64 key from the node's `api: encryption: key:`. |
| `password` | string | yes | Legacy API password, for nodes without a key. |

The driver remembers the node name from device info to re-resolve it by mDNS.

## `mqtt_devices` columns added

| Column | Type | Default | Meaning |
| --- | --- | --- | --- |
| `transport` | text | `mqtt` | `mqtt` or `esphome`. |
| `entity_key` | text | empty | ESPHome object ID for `esphome` devices. |
| `entity_type` | text | empty | ESPHome entity type, such as `light` or `sensor`. |

For `esphome` devices, `topics`, `state_script`, `payload_script`, and the
availability topic are empty; `connection_id` is the ESPHome connection;
`missing` is set when the node no longer lists the object ID.
`(connection_id, entity_key)` is unique when `transport` is `esphome`.

## Entity (driver memory)

| Field | Source |
| --- | --- |
| `key` | numeric key, per session |
| `object_id`, `name`, `type` | list entities response |
| `unit`, `device_class`, `accuracy` | sensor and number responses |
| `features` | per type: colour modes, supports position, speed count, options |
| `state` | latest state response, mapped to the kind's values |

## Discovered entity (not stored)

`object_id`, `name`, `type`, `kind`, `commandable`, `can_add`, `reason`,
`device_id`, like Zigbee2MQTT's discovered device.

## Discovered node (not stored)

`name`, `host`, `port`, `mac`, `version`, `board`, from mDNS TXT records.
