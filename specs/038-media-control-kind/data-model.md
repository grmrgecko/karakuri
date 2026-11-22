# Data model: Media control kind

## Kind `media`

| Attribute | Type | Writable | Requires option | Rules |
| --- | --- | --- | --- | --- |
| `state` (primary) | string | yes | | `off`, `on`, `idle`, `playing`, `paused`, `buffering`; writing `idle` needs `stop`, `on` and `off` need `power`; `buffering` is reported only |
| `volume` | number | yes | `volume` | 0 to 100 |
| `muted` | boolean | yes | `mute` | |
| `source` | string | yes | `source` | One of `source_list` when set, and of the reported `sources` when not empty |
| `position` | number | yes | `seek` | Seconds, 0 to `duration` when known |
| `command` | string | write only | `skip` | `next` or `previous`; announced, never stored |
| `title`, `artist`, `album` | string | read only | | |
| `art` | string | read only | | A URL |
| `duration` | number | read only | | Seconds |
| `position_at` | string | read only | | RFC 3339 |
| `sources` | array | read only | `source` | Strings |

Options, all booleans unless noted: `volume` (default on), `mute` (on),
`skip` (on), `seek` (off), `source` (off), `power` (off), `stop` (on),
`source_list` (choices, for a control whose list the author fixes, named
like a light's `effect_list`).

Initial value: `{"state": "idle"}`.

## Catalog change

`controls.Attribute` gains `WriteOnly bool`. The store does not persist a
write-only attribute: it announces a change whose value carries the attribute
for that announcement only. A whole-value write (a report) may carry
`buffering` and skips the option checks on `state`; it may not carry a
write-only attribute.

## Device mappings

- ESPHome media player entity: kind `media`; `skip`, `seek`, and `source` off;
  `volume`, `mute`, `stop`, and `power` from the entity's feature flags (Home
  Assistant's numbering), all on for firmware that reports none.
- MQTT device: `control: media`; the state script returns any of the
  attributes; the payload script receives `field` and `value` as for other
  kinds, including `command`.
