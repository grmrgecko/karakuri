# Tasks: Home Assistant areas and devices

**Feature**: `specs/039-ha-registries`
**Input**: [spec.md](spec.md), [plan.md](plan.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/interfaces.md](contracts/interfaces.md),
[quickstart.md](quickstart.md).

The lab Home Assistant (`Home Assistant (10.0.10.5)`) is read only: registries
are read, nothing is renamed or moved, and no service is called on it. Writes,
registry changes, and device commands are checked against `hatest`. Test
objects are named `HA device test`.

## Phase 1: Shared preparation

- [X] T001 `pkg/integrations/homeassistant/hatest/hatest.go`: answer `config/floor_registry/list`, `config/area_registry/list`, `config/device_registry/list`, `config/entity_registry/list_for_display` (short keys `ei`, `di`, `ai`, `lb`, `en`, `hb`, `ec`), and `config/label_registry/list` from settable fixtures; `SetRegistry`, `UnknownCommand(type)` to mimic an older Home Assistant, and `PushRegistryUpdate(kind, action, ids)` sending the matching `*_registry_updated` event
- [X] T002 [P] `pkg/flow/meta.go` and `validate.go`: field type `ha-target` (`control` one of `area`, `floor`, `device`, `label`; value a comma-separated ID list or a `{{ }}` expression), validated like a string

## Phase 2: Foundation, the registry copy

Every user story phase depends on this phase.

- [X] T003 `pkg/integrations/homeassistant/registry.go`: `Registry` holding floors (`floor_id`, `name`, `level`), areas (`area_id`, `name`, `floor_id`, `aliases`), devices (`id`, `name_by_user` else `name`, `area_id`, `manufacturer`, `model`, `labels`), entity entries (`entity_id`, `device_id`, `area_id`, `name`, `labels`, `hidden`, `category`), labels (`label_id`, `name`, `color`), and per-registry status (`loaded` or the error); `Describe(entityID)` deriving area (own, else device's), floor, labels (own plus device's), and display name (state `friendly_name`, else the ID); `Resolve(Target)` with `area_id`, `floor_id`, `device_id`, `label_id` (any match), `domain`, `device_class`
- [X] T004 `pkg/integrations/homeassistant/driver.go`: load all five registries after authentication and after every reconnect (an `unknown_command` or error leaves that registry empty and recorded in its status, never failing the session); subscribe to the five `*_registry_updated` events and reread the changed registry after a two second debounce; `Registry()` snapshot, `WatchRegistry(fn)`; unavailable registries are logged and reported by `/ha/registry` and `get_ha_registry`, leaving the connection status connected
- [X] T005 `pkg/integrations/homeassistant/registry_test.go`: load against `hatest`; inherited device area; floor ordering by level; label match through the device; an update event changing an entity's area visible within the debounce; reconnect rereads; an older Home Assistant without floors and labels loads the rest and reports the two

## Phase 3: User story 1, find entities by room (P1)

**Goal**: pickers and MCP group and search entities by floor, area, and device.
**Independent check**: quickstart scenario 1.

- [X] T006 [US1] `pkg/api/ha_handler.go`: `HAEntity` gains `area_id`, `area`, `floor_id`, `floor`, `device_id`, `device`, `labels`, `hidden`, `category`; hidden entities omitted unless `?hidden=1`; sorted by floor level, area, device, name with no-area last; `GET /api/connections/{id}/ha/registry` (connection read) returning `{floors, areas, devices, labels, status}`, registered in `connection_handler.go`
- [X] T007 [P] [US1] `pkg/mcpserver/tools_connections.go`: `get_ha_states` entries gain `area`, `device`, `labels` and filters `area`, `floor`, `device`, `label` (ID or name, case-insensitive); new `get_ha_registry` with the same permission as `get_ha_states`
- [X] T008 [US1] `frontend/src/lib/components/editor/ConfigForm.svelte`: entity suggestions grouped under floor and area headings with the device shown, matching search on area, device, and label names; the `ha-target` field as a searchable multi-select of `/ha/registry` that writes comma-separated IDs and accepts typed text
- [X] T009 [US1] `plugins/core/widgets/shared/EntityPicker.svelte`: the same grouping and search, and a "Show hidden" toggle
- [X] T010 [US1] Tests: `/ha/entities` fields, order, and hidden filter on `hatest`; `/ha/registry` permission; MCP filters by area name and floor

## Phase 4: User story 2, target and watch a room (P1)

**Goal**: flows act on and react to areas, floors, devices, and labels.
**Independent check**: quickstart scenario 2.

- [X] T011 [US2] `pkg/nodes/hanodes/ha.go` `ha-call-service`: `area_id`, `device_id`, `label_id` become `ha-target` fields and `floor_id` is added, sent in the target; with the registry loaded, a call whose targets resolve to no entity of the service's domain fails with `no entities in <area>`, `on <floor>`, `on <device>`, or `labelled <label>`
- [X] T012 [US2] `ha-state`: `entity_id` optional when a group target is set (validation requires one or the other); new `area_id`, `floor_id`, `device_id`, `label_id`, `domain`, `device_class`; each `state_changed` matched against the registry at that moment; payload gains `area_id`, `area`, `device_id`; a target matching nothing arms and logs `watching <target>: no matching entities yet`; held-for timers and caught-up handling as today
- [X] T013 [US2] `ha-get-state`: the same group fields; one entity returns that state as today, a group target returns `{states: [...], count}`
- [X] T014 [US2] Tests in `pkg/nodes/hanodes`: a trigger on an area and domain fires for a member and not for others; an entity moved into the area by a registry update fires on its next change without re-arming; floor and label targets; get state group payload; the empty-target refusal; an existing single-entity flow unchanged

## Phase 5: User story 3, Home Assistant entities as devices (P2)

**Goal**: Device Command, Value, and Changed work on Home Assistant entities.
**Independent check**: quickstart scenario 3.

- [X] T015 [US3] `pkg/integrations/homeassistant/mapping.go`: Go port of `HA_DOMAIN_KINDS`, `haOptions`, `haValue`, `haService`, and the read-only domains from `entitySource.ts` (`Propose(state)` kind, options, writable; `Values(state)`; `Command(state, attribute, value)` to domain, service, data)
- [X] T016 [US3] `pkg/integrations/homeassistant/testdata/mapping.json` with a case per domain and per write; `mapping_test.go` and `frontend/src/lib/entitySourceFixture.test.ts` both run every case
- [X] T017 [US3] `pkg/integrations/homeassistant/driver.go`: `WatchEntity` (cached state, then each change mapped through `Values`), `CommandEntity` (mapping `Command`, then `CallService` with the entity as target), `WatchEntityAvailability` (`unavailable` or `unknown`)
- [X] T018 [US3] `pkg/mqttrelay/transport.go`: transport `homeassistant` as an entity transport; writable types checked per transport (Home Assistant domains the mapping commands); `pkg/entitydevices/sources.go`: Home Assistant source listing entities with area and device in the description, `WatchEntityList` from registry and state changes, `SourceFor` accepting the kind; tracker moves `EntityKey` on an entity rename (`action: update` with `old_entity_id`)
- [X] T019 [US3] `frontend/src/routes` devices settings: **Add from Home Assistant** beside ESPHome and Frigate, entities grouped by area
- [X] T020 [US3] Tests on `hatest`: add a light, a media player, and a sensor; Device Command brightness 40 and state paused call the expected services; a write the features refuse sends nothing; the value changes only on the reported state; availability follows `unavailable`; rename keeps the device; removal marks it missing

## Phase 6: User story 4, cards from a room (P3)

**Goal**: fill an entities card from an area.
**Independent check**: quickstart scenario 4.

- [X] T021 [US4] `plugins/core/widgets/entities-card` config: **Add an area** picker appending rows for the area's visible entities without `config` or `diagnostic` category, ordered by device then name, skipping entities already bound

## Final verification and documentation

- [X] T022 Write `docs/homeassistant.md` (registries, status, pickers, targets, devices), serve it as a guide and MCP resource; update `docs/flow-authoring.md`, `docs/dashboard-authoring.md`, and `docs/mcp.md`; set the spec status to Implemented
- [X] T023 Run a full `make dev` and `make test`; restart `karakuri-dev.service`; confirm the unit is active and http://localhost:8080 answers; browser check of grouped pickers, the target picker, and area fill on the lab Home Assistant with screenshots and a console check, leaving drafts unsaved
- [X] T024 Live quickstart read only on the lab Home Assistant; remove `HA device test` and any test objects and report what was created and removed

Live checks on 2026-11-06 against the lab Home Assistant, read only: all five
registries loaded (15 areas, 229 devices, no floors or labels defined);
`get_ha_states` with `area: kitchen` returned the kitchen's 90 entities, most
placed through their device's area; 530 of 1,169 entities sit in an area.
`discover_entities` listed the 1,169 entities by area, and a read-only kitchen
temperature sensor added as a device showed its value and went online, and a
command to it was refused without reaching Home Assistant. In the browser, the
call service node's area picker offered the lab's areas by name; the lab's area
IDs are opaque hashes, so picking Kitchen stored its ID and read it back as
Kitchen. The entities card's **Add an area** listed the 15 areas and filled the
Front Porch's 62 non-diagnostic entities; nothing was pressed. The console's
only entry was a 404 for the active run of the never-deployed test flow, which
the editor asks for on every flow. The test device, flow, and dashboard were
deleted.

## Dependencies and execution order

- T001 and T002 first; Phase 2 precedes every story.
- US1's route (T006) feeds US2's picker field and US4's area fill.
- US3 needs Phase 2 for descriptions and renames but not US1 or US2.
- Commit each verified phase on its own; run a full `make dev` before
  committing widget code.

## Parallel opportunities

- T002 beside T001; T007 beside T006; T015 and T016 beside Phase 4.

## Implementation strategy

The registry copy with grouped pickers (US1) is the first deliverable, then
area targets and triggers (US2). Devices (US3) follow on the shared mapping,
and the area fill (US4) last.
