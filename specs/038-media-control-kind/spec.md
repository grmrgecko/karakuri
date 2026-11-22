# Feature specification: Media control kind

**Feature**: `specs/038-media-control-kind`
**Created**: 2026-11-05
**Status**: Implemented
**Input**: A `media` control kind: a player's state (playing, paused, idle,
off), what is playing (title, artist, album, art), progress, volume and mute,
and its source, with play, pause, stop, next, previous, volume, mute, and
source commands. Home Assistant `media_player` entities are drawn as it in the
Entities Card and Entity Control instead of as read-only text, ESPHome media
players and MQTT devices (such as a Snapcast or Squeezebox bridge) can be media
devices, karakuri controls can be media players a flow drives, and flows read
and command any of them through the existing control and device nodes. The
Now Playing widget of feature 024 reads the same model, so it works for every
media source rather than Home Assistant alone.

## User scenarios and testing

### User story 1: A player in a card (Priority: P1)

The living room card shows the speaker with what is playing, a play and pause
button, skip, and a volume slider, beside the lights. Today a Home Assistant
media player on a card is a line of text saying "playing".

**Why this priority**: Media players are the most common Home Assistant
domain the card draws as plain text.
**Independent test**: Place an Entities Card row and an Entity Control bound to
a Home Assistant `media_player`; start playback in Home Assistant and watch the
row follow; press pause and change the volume from the dashboard.

**Acceptance scenarios**:

1. **Given** a `media_player` entity, **When** it is placed in a card row,
   **Then** it shows its state, title and artist when known, and the controls
   the player supports.
2. **Given** a player that reports which features it supports, **When** it is
   drawn, **Then** only those controls appear: a player without volume control
   has no slider, one without next track has no skip.
3. **Given** a press of pause, next, or a volume change, **When** the dashboard
   permits control, **Then** the matching Home Assistant service is called, and
   a display-only binding or read-only dashboard draws no controls.
4. **Given** the player turns off or becomes unavailable, **When** it is drawn,
   **Then** the row shows that and its controls are disabled.

---

### User story 2: Players that are not in Home Assistant (Priority: P1)

The owner runs an ESPHome speaker and a Squeezebox bridge that speaks MQTT.
Both show as players on the same card, with the same controls, and flows can
pause them.

**Why this priority**: Devices are how karakuri reaches hardware without Home
Assistant; a media device kind closes the gap the ESPHome and MQTT integrations
leave for players.
**Independent test**: Add an ESPHome media player entity and an MQTT device of
kind media; drive each from the card and from a flow.

**Acceptance scenarios**:

1. **Given** an ESPHome node with a media player entity, **When** its entities
   are added as devices, **Then** the player is a media device whose state,
   volume, and mute follow the node, and play, pause, stop, volume, and mute
   commands reach it.
2. **Given** an MQTT device of kind media, **When** its state script returns
   media attributes, **Then** the device shows them, and a command reaches its
   payload script with the attribute and value, as for other kinds.
3. **Given** a media device, **When** it is placed in a card or an Entity
   Control, **Then** it is drawn exactly as a Home Assistant player is.

---

### User story 3: Flows that drive players (Priority: P2)

A flow pauses the ESPHome speaker and the Squeezebox bridge when the doorbell
rings and lowers their volume after 22:00. Another flow keeps a karakuri media
control in step with a player it drives over HTTP, so the dashboard shows that
player too.

**Why this priority**: Automations around media are common, and a device's
payload script should not need a flow to know its wire format.
**Independent test**: Pause a media device and a media control from flows with
Device Command and Control Set, and read their state with the device state and
Control Value nodes.

**Acceptance scenarios**:

1. **Given** a media control, **When** a flow sets `state` to `paused`, or
   `volume` to 30, **Then** the control changes and Control Changed fires with
   the attribute, so a flow that drives the real player can follow.
2. **Given** a media device, **When** a Device Command sets `state` to
   `paused` or sends `next`, **Then** the device is commanded.
3. **Given** a media control written with `next` or `previous`, **When** the
   write lands, **Then** the command is announced to watchers as a change of
   the `command` attribute without changing the stored state, since a skip is
   an action rather than a state.
4. **Given** an out-of-range volume or a source the player does not list,
   **When** it is written, **Then** the write is refused with the range or the
   sources named.

---

### User story 4: Now Playing for any player (Priority: P3)

The Now Playing widget, today Home Assistant only, shows an ESPHome speaker or a
media control the same way.

**Why this priority**: One model should mean one widget.
**Independent test**: Bind Now Playing to a media device and a media control.

**Acceptance scenarios**:

1. **Given** Now Playing bound to a media device or control, **When** it plays,
   **Then** title, artist, art, progress, and controls show as for a Home
   Assistant player.
2. **Given** art given as a URL on another host, **When** shown, **Then** the
   browser loads it directly; only Home Assistant's own art is relayed, as
   today.

### Edge cases

- Progress is reported as a position at a time; the face advances it locally
  while playing and resets on each report, so a player that reports position
  rarely still shows a moving bar.
- A player that reports volume as a fraction (0 to 1), as Home Assistant does,
  is shown and set as 0 to 100.
- Turning a player off and on is offered only when the player supports it.
- A source list longer than the face can show is a dropdown.
- A media control with no title shows its state alone.
- Art from an MQTT or ESPHome player is whatever URL the device reports; a
  device that reports none shows no art.
- Grouped speakers, playlists, browsing, and shuffle and repeat modes are not
  part of the kind; flows reach them through Home Assistant service calls.

## Requirements

### Functional requirements

- **FR-001**: The system MUST offer a `media` control kind with attributes
  `state` (one of `off`, `idle`, `playing`, `paused`, `buffering`; primary),
  `volume` (0 to 100), `muted`, `title`, `artist`, `album`, `art` (a URL),
  `position` and `duration` (seconds), `position_at` (when the position was
  reported), `source`, and `sources`, and options naming which features the
  player has (volume, mute, next and previous, seek, source, power).
- **FR-002**: Writing `state` MUST accept `playing`, `paused`, `idle` (stop),
  `off`, and `on`, and writing `command` MUST accept `next` and `previous`,
  announced as a change without being stored. Writes MUST be refused for
  features the options leave off, volumes outside 0 to 100, and sources not in
  `sources`.
- **FR-003**: Home Assistant `media_player` entities MUST be drawn and
  commanded as the media kind, mapping state, attributes, and
  `supported_features` to the kind and each write to the matching
  `media_player` service.
- **FR-004**: ESPHome media player entities MUST become media devices whose
  state, volume, and mute follow the node and whose commands reach it.
- **FR-005**: MQTT devices MUST accept kind `media`, with state scripts returning
  media attributes and payload scripts receiving media commands as for other
  kinds.
- **FR-006**: The Entities Card and Entity Control MUST draw a media face:
  state, title and artist, art when the row shows it, transport buttons and a
  volume slider for the supported features, mute, and a source picker, with
  progress advancing locally while playing.
- **FR-007**: The Now Playing widget MUST accept a media device or control as
  its binding, in addition to a Home Assistant `media_player`.
- **FR-008**: Control Set, Control Value, Control Changed, Device Command, and
  the device state nodes MUST handle the media kind's attributes without new
  node types. Home Assistant players are commanded from flows with the existing
  service call node, as today.

### Key entities

- **Media kind**: the attributes and options in FR-001.
- **Media command**: a write of `state`, `volume`, `muted`, `source`, or
  `command` (`next`, `previous`).

## Success criteria

- **SC-001**: A Home Assistant player on a card shows play, pause, skip, and
  volume when it supports them, and a press reaches Home Assistant within one
  second.
- **SC-002**: The same card face draws a Home Assistant player, an ESPHome
  player, an MQTT media device, and a media control.
- **SC-003**: A flow pauses an ESPHome player, an MQTT media device, and a
  media control with the existing nodes and no wire formats.
- **SC-004**: A write the player cannot do (volume on a player without it, an
  unknown source) is refused with a reason before anything is sent.

## Assumptions and scope

- The kind models one player's transport, volume, and now-playing details;
  library browsing, queues, grouping, and shuffle or repeat are out of scope.
- Commanding Home Assistant entities from flows by kind attribute, rather than
  by service, needs Home Assistant entities as devices, which belongs with the
  Home Assistant registry work; until then flows use the service call node.
- Testing against real players uses the lab Home Assistant read only, so
  Home Assistant commands are checked against the test fake and a disposable
  player; MQTT and ESPHome media devices are checked with test devices.
- The Now Playing widget keeps its current look; it gains sources, not a
  redesign.

## References

- [Feature 004: connections and devices](../004-connections-devices/spec.md)
- [Feature 024: home media widgets](../024-home-media-widgets/spec.md)
- [Feature 031: location control kind](../031-location-control/spec.md)
- [Feature 033: ESPHome native API](../033-esphome-native-api/spec.md)
