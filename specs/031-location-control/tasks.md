# Tasks: Location control kind

**Feature**: `specs/031-location-control`
**Input**: [spec.md](spec.md), [plan.md](plan.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/interfaces.md](contracts/interfaces.md),
[quickstart.md](quickstart.md).

## Phase 1: Shared preparation

- [X] T001 List the deployed and draft `control-changed` nodes in the development database that set `attribute` (MCP `list_flows` and `list_flow_nodes`), and record which would fire differently under the new filter, for the completion report

## Phase 2: Foundation, the kind

Every user story phase depends on this phase.

- [X] T002 Add `KindLocation = "location"` to `pkg/controls/kinds.go`: label "Location", icon `las la-map-marker`, value type object, attributes `state` (string, primary), `latitude`, `longitude`, `accuracy` (numbers), `source` (string), `since` (string, read-only); options `zones` (choices, default `home` → Home and `not_home` → Away), `restrict_zones` (boolean, default false), `manual` (boolean, default false, "Draw the zones as a picker on dashboards"), `show_coordinates` (boolean, default false)
- [X] T003 In `pkg/controls/kinds.go`: `InitialValue` returns `map[string]any{}` for `location`; `NormalizeOptions` refuses `restrict_zones` with an empty `zones` list; `coerceLocationAttribute` enforces `state` "Not empty. One of `zones` when `restrict_zones` is on.", `latitude` "-90 to 90", `longitude` "-180 to 180", `accuracy` "Metres, zero or more", `source` any string, with the error texts in contracts/interfaces.md
- [X] T004 In `Store.apply` in `pkg/controls/store.go`: after merging a `location` write, set `since` to now (RFC 3339 UTC) when `state` differs from the current state, keep the current `since` otherwise, and drop it when there is none, so a `since` passed through `coerceObject` is never stored
- [X] T005 Tests in `pkg/controls/store_test.go` (table-driven, real store): every row of the `since` transitions table in data-model.md, restricted and unrestricted zones, coordinate and accuracy bounds, a bare string set landing on `state`, a single-attribute set, and `restrict_zones` with no zones refused

## Phase 3: User story 2, a person's location in a control (P1)

**Goal**: flows, MCP, and dashboards read and write location controls; Control Changed fires once per state change.
**Independent check**: quickstart scenarios 1 and 2.

- [X] T006 [US2] Change the Control Changed attribute filter in `pkg/nodes/core/controls.go`: on a composite control with `attribute` set, fire when `changedValue(old, attribute)` and `changedValue(new, attribute)` differ (by the store's value comparison), whatever attribute the write named; fill `attribute`, `attribute_value`, and `attribute_old` in the payload from the watched attribute when the write named none
- [X] T007 [US2] Tests in `pkg/nodes/core/controls_test.go`: a whole-value location write that moves `state` fires a `state` watcher once; a coordinate-only write does not; a light whose brightness changes in a whole-value write fires a `brightness` watcher; `From` and `To` still filter
- [X] T008 [P] [US2] Record `location.state` as an enumeration in `pkg/historyfwd/convert.go` with options from `zones`, coordinates and accuracy as numbers, `source` as text, and skip `since`; test in `pkg/historyfwd`
- [X] T009 [P] [US2] Primary-attribute fallback for state conditions: `readValue` in `frontend/src/lib/displayState.ts` reads the kind's primary attribute when the control value is an object and no attribute is named; `frontend/src/routes/DashboardView.svelte` loads the kinds catalog once and passes primary attributes by kind; test in the existing `displayState` test file
- [X] T010 [US2] Location face `plugins/core/widgets/shared/faces/LocationFace.svelte`, drawn from `ControlFace.svelte` for `kind === "location"`: the zone label for `state` (raw state when unlisted), "for <duration>" from `since` refreshed each minute, coordinates and accuracy (`41.8781, -87.6298 ± 25 m`) when shown and present, "Not reported" for an empty value, and a zone picker writing `state` when the source is writable and `manual` is on
- [X] T011 [US2] Add `showCoordinates?: boolean` to `EntitySource` in `plugins/core/widgets/shared/entitySource.ts` and a toggle for it in `plugins/core/widgets/shared/SourceSelect.svelte` shown when the row resolves to a location; coordinates show when the row field or the kind's `show_coordinates` is on
- [X] T012 [US2] Live: quickstart scenarios 1 and 2 over MCP

## Phase 4: User story 1, Home Assistant locations (P1)

**Goal**: `person` and `device_tracker` entities draw as read-only locations.
**Independent check**: quickstart scenario 5, steps 1 and 2.

- [X] T013 [US1] In `plugins/core/widgets/shared/entitySource.ts`: map `person` and `device_tracker` to `location` in `HA_DOMAIN_KINDS` while keeping them read-only (no service in `haService`), give them the default zones in `haOptions`, and build the value in `haValue` from the mapping table in data-model.md, passing the entity's `last_changed` through for `since`
- [X] T014 [US1] Frontend tests for `resolveHA` with a `person` and a `device_tracker` fixture: kind, read-only, value fields, `since`, and `unavailable` for `unavailable` and `unknown`

## Phase 5: User story 3, MQTT trackers (P2)

**Goal**: an MQTT device of kind location keeps `since` and the OwnTracks script works.
**Independent check**: quickstart scenario 3.

- [X] T015 [US3] In `receive` in `pkg/mqttrelay/device.go`: for a device whose control is `location`, discard a `since` the script returned, set `since` to now when the merged `state` differs from the state before the message, and keep the previous `since` otherwise
- [X] T016 [US3] Add the Location row to the keys table and the OwnTracks script (transition enter and leave, location updates, other types ignored) to `docs/mqtt-device-state-script.md`
- [X] T017 [US3] Tests in `pkg/mqttrelay/device_test.go`: `since` set on a state change only and a script-returned `since` discarded; the documented OwnTracks script run through `TestStateScript` on recorded `transition` enter, `location`, and `transition` leave messages (SC-003)
- [X] T018 [US3] Live: quickstart scenario 3

## Phase 6: User story 4, fusion pattern (P2)

**Goal**: the documented fusion flow works across a restart.
**Independent check**: quickstart scenario 4.

- [X] T019 [US4] Document location controls, the Control Changed filter rule, and the fusion pattern (per-source triggers watching `state`, a script deciding whether any source is home, the Trigger node with a state key, Send now Nothing, Then wait 10m, Then send `not_home`, "A message while waiting is ignored", home sent to Reset) in `docs/flow-authoring.md`
- [X] T020 [US4] Live: build the documented flow with a 2m wait and run quickstart scenario 4, including a restart during the wait (SC-004)

## Final verification and documentation

- [X] T021 Document location rows, the coordinates toggle, and the condition fallback in `docs/dashboard-authoring.md`; set the spec status to Implemented and drop "(draft)" from its entry in `specs/spec-kit.md`
- [X] T022 Run `make dev`, `make test TARGET=server`, and `make test TARGET=frontend`; restart `karakuri-dev.service`; run quickstart scenario 5 in the browser with settled screenshots and a console check (SC-005)
- [X] T023 Remove every quickstart test object (controls, devices, flows, dashboard), confirm the service is active and http://localhost:8080 answers, and report the objects created and removed

## Dependencies and execution order

- T001 runs before T006 changes the filter.
- Phase 2 blocks every story. T002 precedes T003 and T004; T005 follows them.
- US2 (Phase 3) is the MVP. T008 and T009 touch separate files and can run in
  parallel with T006 and T007. T010 precedes T011.
- US1 needs the face from T010.
- US3 needs Phase 2 only; T016 precedes T017.
- US4 needs US2's filter change (T006).
- Commit each verified phase on its own.

## Implementation strategy

Phase 2 and US2 give a usable location control on its own. US1 and US3 add
sources, and US4 documents combining them. Each phase leaves the build and
tests passing.
