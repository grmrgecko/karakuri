# Contract: location interfaces

## Control catalog

`GET /api/controls/kinds` and the MCP `list_control_kinds` tool gain:

```json
{
  "name": "location",
  "label": "Location",
  "icon": "las la-map-marker",
  "value_type": "object",
  "attributes": [
    { "id": "state", "label": "State", "type": "string", "primary": true },
    { "id": "latitude", "label": "Latitude", "type": "number" },
    { "id": "longitude", "label": "Longitude", "type": "number" },
    { "id": "accuracy", "label": "Accuracy", "type": "number" },
    { "id": "source", "label": "Source", "type": "string" },
    { "id": "since", "label": "Since", "type": "string", "read_only": true }
  ],
  "options": ["zones", "restrict_zones", "manual", "show_coordinates"]
}
```

The `options` entry above abbreviates the field specs listed in
[data-model.md](../data-model.md).

## Writes

`POST /api/controls/{id}/set`, the MCP `set_control` tool, and the Set Control
node accept a bare string (written to `state`), an object of attributes, or
one named attribute. Errors:

| Write | Error |
| --- | --- |
| state not in zones, restricted | `"gym" is not one of the control's zones: home, not_home` |
| latitude 91 | `value 91 is above the maximum of 90` |
| accuracy -1 | `value -1 is below the minimum of 0` |
| `since` | `Location reports Since but cannot be told it` |
| empty state | `a location state cannot be empty` |

## Control Changed

With `attribute` set on a composite control, the trigger fires when that
attribute's value differs between `old` and `value`, whichever attribute the
write named. The payload then carries `attribute`, `attribute_value`, and
`attribute_old` for the watched attribute. With no attribute set, behaviour is
unchanged.

`also_watch` (field control `control-list`, comma-separated control IDs) adds
controls that start the same trigger. Each message's `control_id`, `name`, and
`kind` name the control that changed. `find_references` reports every listed
control.

## Dashboard conditions

A `control` condition with an empty `attribute` on a control whose value is an
object compares the kind's primary attribute. A condition naming an attribute
is unchanged.

## Widgets

`resolveHA` returns, for `person.*` and `device_tracker.*`:

```ts
{ kind: "location", readOnly: true, options: { zones: DEFAULT_ZONES },
  value: { state, latitude, longitude, accuracy, source, since }, state,
  unavailable }
```

`ControlFace` draws `kind === "location"` through `LocationFace`.
`EntitySource` gains one optional row field, `showCoordinates?: boolean`,
offered by the row editor when the row resolves to a location. Coordinates are
shown when the row field is on or, for a control or MQTT device, when the
kind's `show_coordinates` option is on. A Home Assistant row has no kind
options of its own, so the row field is the only switch there.
