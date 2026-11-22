# Design plan: Location control kind

**Feature**: `specs/031-location-control` | **Date**: 2026-10-31 | **Spec**: [spec.md](spec.md)

## Summary

The control catalog gains a composite `location` kind whose primary attribute
is `state`, with coordinates, accuracy, and source beside it and a read-only
`since` that the writer sets when `state` changes. The control store and the
MQTT device relay are the two writers, so each sets `since` itself. Dashboards
draw Home Assistant `person` and `device_tracker` entities through the same
kind by mapping them client-side, with no write path. Three gaps in
existing code stood between the spec and its success criteria, and each became
a change to existing behaviour rather than a new node:

- The Control Changed trigger's attribute filter compares the write's named
  attribute, so a whole-value write that moves `state` never fires a trigger
  watching `state`. It will compare the watched attribute's old and new
  values instead.
- A dashboard state condition on a composite control with no attribute named
  compares the JSON of the whole object. It will compare the kind's primary
  attribute.

- A flow cannot feed several sources into one decision, because an input
  takes one edge and a Script cannot pull Control Value nodes. Control
  Changed gains `also_watch`, a list of further controls that start the same
  trigger. Planning missed this one; implementation found it.

The Trigger node of feature 029, with its reset port and "is ignored"
retrigger, then gives "away after ten minutes unless a source says home
first", durably.

[research.md](research.md) records the decisions,
[data-model.md](data-model.md) the value and options,
[contracts/interfaces.md](contracts/interfaces.md) the catalog, write, trigger,
condition, and HA mapping contracts, and [quickstart.md](quickstart.md) the
checks.

## Technical context

- Language and dependencies: Go 1.26.5 and Svelte 5 with TypeScript. No new
  dependencies.
- Components:
  - `pkg/controls/kinds.go`: the `location` kind, its options, initial value,
    and `coerceLocationAttribute`.
  - `pkg/controls/store.go`: `since` handling in `apply`.
  - `pkg/nodes/core/controls.go`: the Control Changed attribute filter and
    `also_watch`; `pkg/nodes/core/plugin.go` registers the `control-list`
    field control, `pkg/mcpserver/tools_dependencies.go` reports each listed
    control as a reference, and `ConfigForm.svelte` edits it as a checkbox
    list.
  - `pkg/mqttrelay/device.go`: `since` for location devices on receive.
  - `pkg/historyfwd/convert.go`: `location.state` as an enumeration with the
    zones as its options; `since` not recorded.
  - `plugins/core/widgets/shared/entitySource.ts`: `person` and
    `device_tracker` mapped to `location`, read-only, with `since` from
    `last_changed`.
  - `plugins/core/widgets/shared/faces/ControlFace.svelte` and a new
    `LocationFace.svelte`: label, relative time, coordinates, zone picker.
  - `plugins/core/widgets/shared/SourceSelect.svelte`: the row's
    `showCoordinates` toggle.
  - `frontend/src/lib/displayState.ts` and its caller in
    `frontend/src/routes/DashboardView.svelte`: primary-attribute fallback.
  - Docs: `docs/mqtt-device-state-script.md` (keys table and OwnTracks
    script), `docs/flow-authoring.md` (location controls, the Control Changed
    filter change, the fusion pattern), `docs/dashboard-authoring.md`
    (location rows and the condition fallback).
- Storage: no new tables or columns. A location value is JSON in
  `controls.state` and in `mqtt_device_states`, as other composite kinds are.
- Limits: none new. Coordinates are held to their ranges and accuracy to zero
  or more.

## Constitution check

- **Failures**: invalid writes fail with the reason and the allowed zones, as
  other kinds' writes do; a flow's Set Control node fails through its error
  output. Nothing new runs in the background.
- **Drafts and deployment**: the kind appears in the catalog that the editor,
  REST, MCP, the control-attribute picker, and the port typing rule all read,
  so they agree with no per-surface code. The trigger filter change alters
  when deployed Control Changed nodes with an attribute fire: they now also
  fire on whole-value writes that move that attribute. That was the intent of
  the filter, and the guide states the new rule.
- **Permissions**: control writes keep the existing permission checks. Home
  Assistant location rows send no service calls.
- **Existing state**: no migration. Existing controls, devices, databases,
  identities, and encrypted secrets are untouched.
- **Verification**: `make dev`, `make test TARGET=server`,
  `make test TARGET=frontend`, the quickstart over MCP and the browser, and a
  restart of `karakuri-dev.service`.

## Design and contracts

### Writes

`CoerceAttribute` for `location`:

```text
state      trimmed string, not empty; when restrict_zones, one of zones
latitude   number in [-90, 90]
longitude  number in [-180, 180]
accuracy   number >= 0
source     string
since      refused (read-only)
```

`coerceObject` passes read-only attributes through unchanged, which is how a
thermostat reports `current`. For `location` that would let a caller write
`since`, so `apply` overwrites it after the merge:

```text
merged = current merged with the coerced write
if kind is location:
  if merged.state != current.state: merged.since = now (RFC 3339, UTC)
  else if current has since:         merged.since = current.since
  else:                              delete merged.since
```

Then `sameValue` decides whether anything changed, as now. A write that only
moves coordinates changes the value and announces with `since` unchanged.

### Control Changed

The attribute filter becomes:

```text
attribute set and composite:
  fire only if changedValue(new, attribute) != changedValue(old, attribute)
```

`From` and `To` compare the watched attribute as today. The payload's
`attribute`, `attribute_value`, and `attribute_old` are filled from the
watched attribute when the write named none. A trigger with no attribute still
fires on every change, including coordinate updates; the guide says to watch
`state` for presence.

`also_watch` holds further control IDs, comma-separated. The trigger fires for
a change of any watched control, applying the attribute, From, and To filters
to each, and the message names the control that moved. The attribute filter is
dropped per control for scalar kinds, as it is for the main control.

### MQTT devices

In `receive`, after merging the decoded values into the device state and
before publishing: for a device whose control is `location`, set `since` to
now when `state` differs from the state before the message, keep the previous
`since` otherwise, and never take a `since` the script returned. Restored
state keeps its stored `since`.

### Dashboards

`resolveHA` maps `person` and `device_tracker` to `location`, read-only, with
value `{state, latitude, longitude, accuracy: gps_accuracy, source, since:
last_changed}` where `source` is the person's `source` attribute or the
tracker's `source_type`. The entity's options carry the default zone labels.
Rows gain an optional `showCoordinates` field, offered by the row editor
(`SourceSelect.svelte`) for a location row, because a Home Assistant row has
no kind options to carry `show_coordinates`.

The location face draws the state's zone label (falling back to the raw state)
and "for 2 h" from `since`, refreshed each minute. With `show_coordinates` on
and coordinates present, a second line shows `41.8781, -87.6298 ± 25 m`. A
writable control with `manual` on draws the zones as a picker that writes
`state`; otherwise the row reads only. An empty value reads "Not reported".

### Conditions

`readValue` for a control with no attribute named and an object value reads
the kind's primary attribute. `DashboardView` already loads controls; it also
loads the kind catalog once and passes primary attributes by kind into the
evaluator.

## Verification

| Scenario | Test |
| --- | --- |
| US1 | `entitySource` tests for `person` and `device_tracker`: kind, read-only, value, `since`, unavailable. Live: browser check of an Entities Card and an Entity Control bound to a `person` entity from the lab Home Assistant. |
| US2 | `controls` tests: validation, restricted zones, `since` only on state change, `since` not writable, partial writes. Control Changed tests: whole-value write fires a `state` watcher once and a coordinate write does not. `displayState` test for the primary fallback. `historyfwd` test for the state enumeration. Live: quickstart 1 and 2. |
| US3 | `mqttrelay` test: a location device sets `since` on state change only; the documented OwnTracks script run through `TestStateScript` on recorded messages. Live: quickstart 3. |
| US4 | Live: quickstart 4, including a restart during the wait. |

## Risks and open decisions

- Control Changed nodes with an attribute filter change behaviour as above.
  Before changing it, the implementation lists the deployed `control-changed`
  nodes in the development database that set `attribute` and reports any whose
  firing changes.
- `since` for Home Assistant rows is `last_changed`, which Home Assistant also
  moves when an entity comes back from `unavailable`. That is its definition,
  and the row shows it as reported.
- Zones are display labels, not geofences; a map and distances stay out of
  scope.
