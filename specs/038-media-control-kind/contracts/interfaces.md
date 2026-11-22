# Contract: Media control kind interfaces

## Kind catalog

`GET /api/controls/kinds` and MCP `list_control_kinds` include `media` with
the attributes and options of [data-model.md](../data-model.md); attributes
carry `write_only` where set.

## Writes

The existing routes and nodes write the kind: `POST /api/controls/{id}/set`
with `attribute` and `value`, MCP `set_control`, Control Set, Device Command,
and MCP `command_mqtt_device`. Refusals name the reason:

- `value 120 is above the maximum of 100`
- `this player has no skip` (option off)
- `"Radio 4" is not one of the player's sources: ...` (from `source_list`, or
  for a device or control, from its reported `sources`)
- `buffering is reported by a player and cannot be set`

A `command` write answers with the control unchanged; Control Changed fires
with `attribute: "command"` and the value carrying `command`.

## Faces

`plugins/core/widgets/shared/faces/MediaFace.svelte`, chosen by
`ControlFace.svelte` for kind `media`:

- Full: art (when the row shows it), title and artist, state, a progress bar
  advancing while playing, previous, play or pause, next, stop, a volume
  slider, mute, a source picker, power; each only when its option is on.
- Compact (card rows): title or state, play or pause, and volume.
- Disabled when unavailable or off, except power.

## Home Assistant

`HA_DOMAIN_KINDS.media_player = "media"`, with the options, value, and service
mapping of [research.md](../research.md).

## Now Playing

The `media-player` widget's config accepts `source` `ha`, `control`, or
`mqtt`, with `entityId`, `controlId`, or `deviceId`, display only or not.
