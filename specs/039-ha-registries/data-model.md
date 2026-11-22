# Data model: Home Assistant areas and devices

Nothing new is stored in the database except Home Assistant devices, which are
`mqtt_devices` rows like ESPHome and Frigate devices. The registries are held
in memory per connection and reread on every connect.

## Registry (per connection, in memory)

| Record | Fields | Source |
| --- | --- | --- |
| Floor | `floor_id`, `name`, `level` | `config/floor_registry/list` |
| Area | `area_id`, `name`, `floor_id`, `aliases` | `config/area_registry/list` |
| Device | `id`, `name` (`name_by_user` else `name`), `area_id`, `manufacturer`, `model`, `labels` | `config/device_registry/list` |
| Entity entry | `entity_id`, `device_id`, `area_id`, `name`, `labels`, `hidden`, `category` (`config`, `diagnostic`, or empty) | `config/entity_registry/list_for_display` |
| Label | `label_id`, `name`, `color` | `config/label_registry/list` |

Derived per entity: `area` (own, else device's), `floor` (the area's),
`labels` (own plus device's, for targeting), `display_name` (state's
`friendly_name`, else the entity ID).

Registry status: per registry `loaded`, or the error that left it empty
(`unknown_command` on an older Home Assistant, a refusal, a timeout). Shown in
the connection status detail.

## Home Assistant device

An `mqtt_devices` row with:

- `transport`: `homeassistant`
- `connection_id`: the Home Assistant connection
- `entity_key`: the entity ID; moves on an entity rename
- `entity_type`: the domain
- `control` and options: from the shared mapping at add time; Regenerate
  rereads them
- `missing`: set when the entity leaves the registry and state list

## Area target

Config on `ha-state` and `ha-get-state`, and the targets of `ha-call-service`:

| Field | Type | Rule |
| --- | --- | --- |
| `entity_id` | entities | As today; optional when a group target is set |
| `area_id`, `floor_id`, `device_id`, `label_id` | comma-separated IDs | Any combination; an entity matches if it matches any |
| `domain` | string | Optional narrowing, e.g. `binary_sensor` (triggers and get state) |
| `device_class` | string | Optional narrowing, e.g. `motion` (triggers and get state) |

A trigger or get state needs at least one entity or group target.
