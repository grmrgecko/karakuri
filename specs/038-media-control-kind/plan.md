# Design plan: Media control kind

**Feature**: `specs/038-media-control-kind` | **Date**: 2026-11-05 | **Spec**: [spec.md](spec.md)

## Summary

The `media` kind joins the control catalog in `pkg/controls/kinds.go` with the
attributes, options, and coercion of [data-model.md](data-model.md). Skips are
a write-only `command` attribute the store announces without storing, so a
flow watching the control sees the press and no player is ever left
"skipping". Because the catalog is what every source declares itself in, karakuri
controls and MQTT devices of kind media need no further server work, and their
option forms render from the catalog.

Home Assistant players are mapped in the widgets' shared `entitySource.ts`,
where every other domain is: state and attributes to the kind's value,
`supported_features` to its options, and each write to a `media_player`
service. ESPHome media player entities become media devices in
`pkg/integrations/esphome`, whose API has state, volume, mute, and transport
commands but no skip or now-playing details. A new `MediaFace.svelte` draws the
kind for cards and Entity Controls, and the Now Playing widget accepts any
binding the card takes.

Flows command controls and devices with the existing nodes; Home Assistant
players stay on the service call node, since making Home Assistant entities
devices belongs with the registry work. The spec's user story 3, FR-008, and
SC-003 were narrowed to say so. [research.md](research.md) records the mappings,
[contracts/interfaces.md](contracts/interfaces.md) the writes, faces, and
widget config, and [quickstart.md](quickstart.md) the checks.

## Technical context

- Language and dependencies: Go 1.26.5, Svelte 5. No new dependencies; the
  ESPHome API library already carries the media player messages.
- Components:
  - `pkg/controls/kinds.go`: `KindMedia`, `Attribute.WriteOnly`, options,
    initial value, `coerceMediaAttribute` (state values against the options,
    volume 0 to 100, source in `sources`, position within duration, command
    `next` or `previous` with `skip`, refusing `buffering` and read-only
    attributes from a write).
  - `pkg/controls/store.go`: a write-only attribute is announced in the
    change's value and not persisted.
  - `pkg/integrations/esphome`: list, state, and command handling for media
    player entities; `entities.go` maps them to kind media.
  - `pkg/entitydevices`: ESPHome media players offered as media devices.
  - `plugins/core/widgets/shared/entitySource.ts`: `media_player` domain,
    options, value, and service mapping.
  - `plugins/core/widgets/shared/faces/MediaFace.svelte` and the dispatch in
    `ControlFace.svelte`; `controlKinds.ts` defaults for the new options.
  - `plugins/core/widgets/media-player`: binding to any source.
  - Docs: `docs/flow-authoring.md` (the kind and `command`),
    `docs/dashboard-authoring.md` (the face and Now Playing sources),
    `docs/esphome.md`, `docs/mqtt-device-state-script.md`,
    `docs/mqtt-device-payload-script.md`.
- Storage: none new; controls and devices store the kind's value as any
  composite kind.

## Constitution check

- **Failures**: writes a player cannot do are refused before anything is sent,
  with the reason; a device command failure is a node failure as for every
  kind.
- **Drafts and deployment**: no change; nodes resolve kinds at run time.
- **Permissions**: unchanged. Faces send writes through the existing control,
  device, and service routes and their dashboard control policy; display-only
  bindings draw no controls.
- **Existing state**: a new kind; no stored value or option changes for
  existing kinds.
- **Verification**: `make dev`, `make test`, kind coercion table tests, store
  test for the write-only command, ESPHome mapping tests against the simulated
  node, widget tests for the Home Assistant mapping, and a live check with an
  MQTT media device under `karakuri-test` and the lab Home Assistant read
  only.

## Design and contracts

### Writes

```text
set(control, attribute, value)
  coerceMediaAttribute: option checks, ranges, choices
  attribute.WriteOnly -> announce Change{attribute, value + {command}} ; nothing stored
  otherwise           -> merge, persist, announce
```

### Face

The face reads the kind's options to decide which controls exist, computes the
live position from `position`, `position_at`, and the clock while `state` is
`playing`, and writes through the binding the row already has: a control set,
a device command, or a Home Assistant service through `haService`.

## Verification

| Scenario | Test |
| --- | --- |
| US1 | Widget tests of the Home Assistant mapping (value, options from `supported_features`, every service); browser check with a lab player, nothing pressed. |
| US2 | ESPHome list, state, and command mapping against the simulated node; MQTT media device live under `karakuri-test`, presses captured. |
| US3 | Kind coercion table; store test that `command` announces and is not stored; Control Changed carries it; Device Command reaches a payload script. |
| US4 | Now Playing bound to a control and a device in the browser. |

## Risks and open decisions

- Progress depends on players reporting `position_at`; an MQTT device that
  does not shows progress from when its value arrived, which drifts if the
  device reports rarely.
- Source lists can change at run time on Home Assistant; the face reads them
  from the value each time rather than from options.
