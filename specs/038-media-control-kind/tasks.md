# Tasks: Media control kind

**Feature**: `specs/038-media-control-kind`
**Input**: [spec.md](spec.md), [plan.md](plan.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/interfaces.md](contracts/interfaces.md),
[quickstart.md](quickstart.md).

The lab Home Assistant is read only: no media player is pressed there. Live
checks publish test devices only under the `karakuri-test` base topic on the
lab broker, never under `zigbee2mqtt/`, and name test objects `Media test`.

## Phase 1: Shared preparation

- [X] T001 `pkg/controls/kinds.go`: `Attribute` gains `WriteOnly bool` (JSON `write_only`), included in `GET /api/controls/kinds` and MCP `list_control_kinds`
- [X] T002 `pkg/controls/kinds.go`: `KindMedia` exactly as [data-model.md](data-model.md) lists it (`state` primary, one of `off`, `on`, `idle`, `playing`, `paused`, `buffering`; `volume` 0 to 100 requiring `volume`; `muted` requiring `mute`; `source` requiring `source`; `position` seconds requiring `seek`; `command` write only, `next` or `previous`, requiring `skip`; read-only `title`, `artist`, `album`, `art`, `duration`, `position_at`, `sources`), options `volume` (default on), `mute` (on), `skip` (on), `seek` (off), `source` (off), `power` (off), `stop` (on), and `sources` (choices); initial value `{"state": "idle"}`

## Phase 2: Foundation, coercion and the write-only command

Every user story phase depends on this phase.

- [X] T003 `pkg/controls/kinds.go`: `coerceMediaAttribute` wired into `CoerceAttribute`, refusing with the contract's reasons: `buffering is reported by a player and cannot be set`; `idle` without `stop`, `on` or `off` without `power`, and any attribute whose option is off (`this player has no skip`); `volume 120 is above the maximum of 100` and below 0 likewise; a `source` not in the value's `sources` (or the `sources` option when set) naming the list; `position` below 0 or above a known `duration`; read-only attributes from a write
- [X] T004 `pkg/controls/store.go`: a write to a `WriteOnly` attribute announces a `Change` whose value is the stored value plus that attribute, persists nothing, and leaves the stored value without it, so a later read has no `command`
- [X] T005 `pkg/controls/media_test.go`: a table of accepted and refused writes for every attribute and option combination above, with the refusal text; a store test that `command` `next` reaches a watcher with `attribute: "command"` and is absent from the stored value and from the next read

## Phase 3: User story 1, a player in a card (P1)

**Goal**: Home Assistant `media_player` entities draw as players with the controls they support.
**Independent check**: quickstart scenario 1.

- [X] T006 [US1] `plugins/core/widgets/shared/entitySource.ts`: `HA_DOMAIN_KINDS.media_player = "media"` and out of `HA_READ_ONLY_DOMAINS`; the value from state and attributes per the research table (`standby` reads as `off`, `volume_level` × 100, `is_volume_muted`, `media_title`, `media_artist`, `media_album_name`, `entity_picture`, `media_position`, `media_duration`, `media_position_updated_at`, `source`, `source_list`); options from `supported_features` (pause 1, seek 2, volume set 4, mute 8, previous 16, next 32, on 128, off 256, select source 2048, stop 4096, play 16384); `haService` mapping each write to `media_play`, `media_pause`, `media_stop`, `turn_on`, `turn_off`, `volume_set` (÷ 100), `volume_mute`, `select_source`, `media_seek`, `media_next_track`, `media_previous_track`
- [X] T007 [P] [US1] `plugins/core/widgets/shared/controlKinds.ts`: defaults for the media options and the kind's icon and label, matching the catalog
- [X] T008 [US1] `plugins/core/widgets/shared/faces/MediaFace.svelte` and the `media` branch in `ControlFace.svelte`: full and compact layouts per [contracts/interfaces.md](contracts/interfaces.md); each control only when its option is on; progress computed from `position`, `position_at`, and the clock while `playing`, capped at `duration`; a source dropdown; disabled when unavailable or off except power; no controls for a display-only binding or a read-only dashboard; writes through the binding the row already has
- [X] T009 [US1] `frontend/src/lib/entitySourceMedia.test.ts`: the value mapping (including `standby`, volume scaling, missing attributes), options from several `supported_features` values, and every `haService` write with its service and data

## Phase 4: User story 2, players that are not in Home Assistant (P1)

**Goal**: ESPHome media players and MQTT devices are media devices drawn like a Home Assistant player.
**Independent check**: quickstart scenario 2, steps 1 and 3.

- [X] T010 [US2] `pkg/integrations/esphome/entities.go` and `driver.go`: `ListEntitiesMediaPlayerResponse` becomes an entity of kind `media` with options `volume`, `mute`, `stop`, `power` on and `skip`, `seek`, `source` off; `MediaPlayerStateResponse` maps to `state` (`none` as `idle`, `announcing` as `playing`), `volume` × 100, and `muted`; `Command` maps `state` playing, paused, idle, on, off, `volume` (÷ 100), and `muted` to `MediaPlayerCommandRequest`
- [X] T011 [US2] `pkg/entitydevices/sources.go`: ESPHome media players offered as media devices with those options
- [X] T012 [US2] `pkg/integrations/esphome/esphometest/node.go` serves a media player that answers commands, and `pkg/integrations/esphome/driver_test.go` plus `pkg/entitydevices/esphome_test.go` check the list, state, and every command against it, and that `next` is refused before anything is sent
- [X] T013 [P] [US2] MQTT devices accept `control: media` through the catalog with no extra server code; confirm in `pkg/mqttrelay` tests that a state script's media attributes are stored and a `command` write reaches the payload script with `field` and `value`

## Phase 5: User story 3, flows that drive players (P2)

**Goal**: Control Set, Control Value, Control Changed, and Device Command handle the kind.
**Independent check**: quickstart scenario 2, step 2.

- [X] T014 [US3] `pkg/nodes/core` control and device node tests: Control Set of `state` `paused` and `volume` 30 on a media control, Control Changed firing with `attribute: "command"` for a `next` write, Control Value reading no `command`, and Device Command `next` on an MQTT media device reaching its payload script; attribute pickers in the node editors list `command` as writable and skip read-only attributes

## Phase 6: User story 4, Now Playing for any player (P3)

**Goal**: the Now Playing widget binds to controls and devices.
**Independent check**: quickstart scenario 3.

- [X] T015 [US4] `plugins/core/widgets/media-player/src/config.ts`, `MediaPlayerConfig.svelte`, and `MediaPlayerWidget.svelte`: `source` `ha`, `control`, or `mqtt` with `entityId`, `controlId`, or `deviceId` and display only, resolved through `entitySource.ts` to a media value; the Home Assistant art relay kept for Home Assistant players only; existing widgets with `source: "ha"` unchanged
- [X] T016 [US4] Widget config tests for each source and the existing Home Assistant config loading as before

## Final verification and documentation

- [X] T017 Update `docs/flow-authoring.md` (the kind, `command`, refusals), `docs/dashboard-authoring.md` (the face, Now Playing sources), `docs/esphome.md` (media players and what they lack), `docs/mqtt-device-state-script.md` and `docs/mqtt-device-payload-script.md` (media attributes and `command`); set the spec status to Implemented
- [X] T018 Run a full `make dev` and `make test`; restart `karakuri-dev.service`; confirm the unit is active and http://localhost:8080 answers; browser check of a lab Home Assistant player on a card and Entity Control with nothing pressed, settled screenshots, and a console check
- [X] T019 Live quickstart scenarios 2 and 3 under `karakuri-test/mediatest`; remove the test control, device, flow, and dashboard, clear the `karakuri-test` topics, and report what was created and removed

Live checks on 2026-11-06 against the dev server: the catalog lists `media`;
a media control took state, volume, and source over MCP and refused a volume
of 120, an unlisted source, and `buffering` with the contract's reasons; a
Control Changed flow on `command` ran for a `next` write whose payload carried
`attribute: command`, and the stored value held no `command`. An MQTT media
device under `karakuri-test/mediatest` showed its published title, artist,
progress, volume, and sources; Command Device sent pause, next, and volume
through the payload script (captured on the broker) and refused an unlisted
source and power. In the browser an Entities Card drew two lab Home Assistant
players (idle with volume, off with controls disabled; nothing pressed), the
media control, and the device; pause on the control's row set it and the row
and its Now Playing widget followed live; Next on the device's Entity Control
published `next`. Now Playing drew the control and the device with progress.
Console clean apart from the session refresh. The test control, device, flow,
and dashboard were removed and the `karakuri-test/mediatest` topics cleared.

## Dependencies and execution order

- T001 precedes T002; Phase 2 precedes every story.
- US1's face (T008) is what US2 and US4 draw with, so it precedes their browser checks; US2's server work (T010 to T013) does not need it.
- US3 needs Phase 2 and an MQTT media device from US2.
- Commit each verified phase on its own; run a full `make dev` before
  committing widget code.

## Parallel opportunities

- T007 beside T006; T010 to T012 beside T006 to T009; T013 beside T010.

## Implementation strategy

The kind with its coercion and write-only command is the first deliverable,
since controls and MQTT devices work from it alone. The Home Assistant mapping
and face follow, then ESPHome, the flow checks, and Now Playing.
