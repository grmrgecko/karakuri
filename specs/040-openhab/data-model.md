# Data model: openHAB connection

## Connection config (`openhab`, stored encrypted)

| Field | Rule |
| --- | --- |
| `base_url` | Required; `http` or `https`; the server, without `/rest` |
| `token` | Optional API token, a declared secret, masked on read |

## Item (in memory, per connection)

| Field | Source |
| --- | --- |
| `name` | Item name; the device's entity key |
| `label` | Item label, else the name |
| `type` | Item type; `Number:Temperature` keeps its dimension |
| `group_type` | A group's base type, empty for other items |
| `state` | Last known state string; `NULL` and `UNDEF` are no value |
| `state_description` | `minimum`, `maximum`, `step`, `options`, `read_only`, `pattern` |
| `tags`, `group_names` | As openHAB lists them |
| `location`, `location_label`, `floor`, `floor_label` | Derived from semantics (research.md) |

## Item device

An `mqtt_devices` row with `transport` `openhab`, `connection_id`, `entity_key`
= item name, `entity_type` = item type, and the kind and options of the
mapping at add time. `missing` while the item is absent.

## Trigger target

`openhab-item-changed` config: `items` (names), `groups` (names; members at
any depth), `location` (a location group), `item_type`, `from`, `to`,
`ignore_caught_up`. At least one of items, groups, or location.
