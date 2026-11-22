# Contract: Home Assistant areas and devices interfaces

## REST

| Route | Permission | Notes |
| --- | --- | --- |
| `GET /api/connections/{id}/ha/entities` | connection read | Each entry gains `area_id`, `area`, `floor_id`, `floor`, `device_id`, `device`, `labels`, `hidden`, `category`. `?hidden=1` includes hidden entities. Sorted by floor, area, device, name |
| `GET /api/connections/{id}/ha/registry` | connection read | `{floors, areas, devices, labels, status}`; `status` maps each registry to `loaded` or its error |

Dashboard viewers are unaffected: `/ha/states` keeps its scope rules, and a
dashboard service call still may not target by area, floor, device, or label.

## Nodes

- `ha-call-service`: `area_id`, `device_id`, `label_id`, and a new `floor_id`
  render as `ha-target` pickers. With the registry loaded, a call whose
  targets resolve to no entity of the domain fails: `no entities in Kitchen`
  (or `on Ground floor`, `on Hallway sensor`, `labelled Night`).
- `ha-state`: `entity_id` is optional; new `area_id`, `floor_id`,
  `device_id`, `label_id` (`ha-target`), `domain`, `device_class`. The
  payload gains `area_id`, `area`, and `device_id` of the changed entity. A
  group target that matches nothing arms and logs `watching Kitchen: no
  matching entities yet`.
- `ha-get-state`: the same group fields. With one entity the payload is that
  state as today; with a group target the payload is
  `{states: [...], count}`.

Field type `ha-target`: `control` names the registry; the value is a
comma-separated list of IDs or a `{{ }}` expression; the editor offers a
searchable multi-select and accepts typed text.

## Devices

- **Settings, Devices, Add from Home Assistant**, and MCP `discover_entities`
  and `add_entities` with a Home Assistant connection, list entities grouped
  by area with the kind each becomes; object IDs are entity IDs.
- Device Command on a Home Assistant device maps the write through the shared
  mapping to a service call; a refused write names the reason as for other
  kinds. `/mqtt_devices/{id}/command` and the device nodes are unchanged.

## MCP

- `get_ha_states`: entries gain `area`, `device`, `labels`; new filters
  `area` (ID or name), `floor`, `device`, `label`.
- `get_ha_registry` (new): floors, areas, devices, labels, and status of a
  connection.

## Widgets

- `EntityPicker.svelte` groups suggestions by floor and area, shows the
  device, searches area, device, and label names, and has a "Show hidden"
  toggle.
- Entities card config: **Add an area** appends rows for the area's visible,
  non-diagnostic, non-configuration entities ordered by device, skipping
  entities already bound.
