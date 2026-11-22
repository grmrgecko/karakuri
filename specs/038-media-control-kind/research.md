# Research: Media control kind

## Where kinds live

- `pkg/controls/kinds.go` is the catalog every source declares itself in:
  attributes, options as `flow.FieldSpec`, coercion per attribute. The settings
  panel and the MQTT device panel render a kind's options from it with no
  frontend change.
- Home Assistant entities are mapped to kinds in the widgets' shared
  `entitySource.ts`: `HA_DOMAIN_KINDS`, an options derivation from attributes,
  a value derivation from state and attributes, and `haService`, which turns a
  kind attribute write into a service call. The server holds no such mapping.
- Faces are in `plugins/core/widgets/shared/faces`; `ControlFace.svelte`
  dispatches by kind, with `LocationFace.svelte` as the precedent for a kind
  with its own face.
- ESPHome entities become devices through `pkg/entitydevices` and
  `pkg/integrations/esphome/entities.go`, which maps each entity type to a kind
  and each kind attribute write to a command message.

## The kind

- Decision: kind `media`, composite. `state` is primary and writable;
  `volume`, `muted`, `source`, and `position` are writable when their option is
  on; `title`, `artist`, `album`, `art`, `duration`, `position_at`, and
  `sources` are read-only reports; `command` is write-only.
- Options are booleans for the features a player has: `volume`, `mute`,
  `skip` (next and previous), `seek`, `source`, `power`, `stop`, plus
  `sources` (choices) for a control whose author fixes the list.
- `state` values: `off`, `on`, `idle`, `playing`, `paused`, `buffering`.
  Writing `idle` is stop, so it needs `stop`; `on` and `off` need `power`.
  `buffering` is reported, never written.

### Skip as a command

A skip is an action, not a state: storing `next` would leave a player
"nexting". The button kind already treats a write as a press that always
announces. Decision: `command` is a write-only attribute (`next`,
`previous`), and the store announces a change carrying `command` in the
announced value without persisting it, so Control Changed and a payload
script see it and a later read does not. The attribute gains a `WriteOnly`
flag in the catalog so the editor and the face know not to show it as a value.

## Home Assistant mapping

From the `media_player` state and attributes:

| Kind | Home Assistant |
| --- | --- |
| `state` | state (`off`, `on`, `idle`, `playing`, `paused`, `buffering`; `standby` reads as `off`) |
| `volume` | `volume_level` × 100 |
| `muted` | `is_volume_muted` |
| `title`, `artist`, `album` | `media_title`, `media_artist`, `media_album_name` |
| `art` | `entity_picture`, relayed through the existing art route when it is Home Assistant's own |
| `position`, `duration`, `position_at` | `media_position`, `media_duration`, `media_position_updated_at` |
| `source`, `sources` | `source`, `source_list` |

Options come from `supported_features`: pause 1, seek 2, volume set 4, mute 8,
previous 16, next 32, turn on 128, turn off 256, select source 2048, stop
4096, play 16384.

Writes map to `media_player` services: `state` playing `media_play`, paused
`media_pause`, idle `media_stop`, on `turn_on`, off `turn_off`; `volume`
`volume_set` with `volume_level` ÷ 100; `muted` `volume_mute`; `source`
`select_source`; `position` `media_seek`; `command` next `media_next_track`,
previous `media_previous_track`.

## ESPHome mapping

The API carries a media player entity (`supports_pause`, `feature_flags`), its
state (`state` none, idle, playing, paused, announcing, off, on; `volume` 0 to
1; `muted`), and a command request (play, pause, stop, mute, unmute, turn on,
turn off, and a volume). It has no next, previous, title, or source. Decision:
an ESPHome media player becomes a media device with options `volume`, `mute`,
`stop`, and `power` on, `skip`, `seek`, and `source` off; `announcing` reads as
`playing`.

## Flows and Home Assistant players

Control Set and Device Command reach controls and devices. A Home Assistant
entity is neither, and the only flow path to it is the service call node.
Making Home Assistant entities devices belongs with the registry work, so the
spec narrows user story 3 and SC-003 to controls and devices.

## Now Playing

The widget's binding is fixed to `source: "ha"`. Decision: it takes any
binding the entities card takes, resolving it to a media value through
`entitySource.ts`, and keeps its Home Assistant art relay for Home Assistant
players; other players' art loads from its URL.

## Progress

`position` with `position_at` lets a face compute the current position while
playing (`position + now − position_at`, capped at `duration`). Home Assistant
reports it this way; MQTT scripts may return `position_at`, and a missing one
is taken as the time the value arrived.
