# Interfaces: Device auto import

## REST

| Method and path | Access | Body or query | Response |
| --- | --- | --- | --- |
| `GET /api/connections/{id}/auto_import` | Use on the connection | none | Import status |
| `GET /api/mqtt_devices/mqtt_discovery` | `api:connections:write` and Use | `connection_id` | `{devices: [Discovered]}` |
| `POST /api/mqtt_devices/mqtt_discovery` | `api:connections:write` and Use | `{connection_id, object_ids}` | `{added: [View], failed: [{object_id, error}]}` |

Import status:

```json
{
  "enabled": true,
  "remove_after": "7d",
  "imported": 12,
  "skipped": [{"key": "sensor/abc", "name": "Plug power", "reason": "template uses {% %} blocks"}],
  "last_import_at": "2026-11-22T10:00:00Z",
  "last_removal_at": null
}
```

`Discovered` is the same shape the entity pickers use (`object_id`, `name`,
`type`, `kind`, `commandable`, `can_add`, `reason`, `device_id`, `group`);
`object_id` is the discovery key and `group` the discovery device name.

Device views add `auto_imported` (bool) and `last_reported_at` (time or
null).

## MCP

- `get_auto_import_status {connection_id}`: import status.
- `discover_mqtt_discovery_devices {connection_id}`: discovered list.
- `add_mqtt_discovery_devices {connection_id, object_ids}`: add result.

## Discovery topics

`<prefix>/<component>/[<node_id>/]<object_id>/config` and device-based
`<prefix>/device/<object_id>/config` with `cmps`, each component naming its
platform in `p`. Abbreviated keys are expanded, `~` in a topic value is
replaced with the base topic, and `device`, `availability`, and `origin`
blocks are expanded the same way.

## Component mapping

| Component | Kind | Read | Command |
| --- | --- | --- | --- |
| switch | boolean | `on` from `state_topic` vs `state_on`/`payload_on` | `command_topic` with `payload_on`/`payload_off` |
| binary_sensor | boolean, read only | `on` vs `payload_on` | none |
| sensor | number with unit when it has a unit or state class, else text | `value` | none |
| light (default) | light | `on`, `brightness` (scaled from `brightness_scale`), `color_temp` (mireds or kelvin), `color` (rgb or hs), `effect` | matching command topics |
| light (json) | light | the same keys from the JSON state | JSON on `command_topic` |
| cover | cover | `state` from state payloads or position, `position`, `tilt` | `command_topic`, `set_position_topic`, `tilt_command_topic` |
| lock | lock | `state` from `state_locked`, `state_unlocked`, `state_jammed`, `state_locking`, `state_unlocking` | `payload_lock`, `payload_unlock` |
| fan | fan | `on`, `percentage` (speed range scaled), `preset`, `oscillating`, `direction` | matching command topics |
| climate | climate | `mode`, `target`, `target_low`, `target_high`, `current`, `action`, `fan_mode` | matching command topics |
| number | number | `value` | `command_topic` |
| select | select | `value` | `command_topic` |
| button | buttons, one button | none | `payload_press` on `command_topic` |
| text | text | `value` | `command_topic` |

Every mapped device reads `json_attributes_topic` objects into extra state
fields and takes availability from the first availability topic, with
`payload_available` and `payload_not_available` (defaults `online` and
`offline`).
