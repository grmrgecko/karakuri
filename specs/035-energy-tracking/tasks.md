# Tasks: Energy tracking

**Feature**: `specs/035-energy-tracking`
**Input**: [spec.md](spec.md), [plan.md](plan.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/interfaces.md](contracts/interfaces.md),
[quickstart.md](quickstart.md).

Live checks publish test devices only under the `karakuri-test` base topic on
the lab broker, never under `zigbee2mqtt/`, and read the lab Home Assistant
without changing it.

## Phase 1: Shared preparation

- [X] T001 Models in `pkg/models/energy.go` for `energy_sources`, `energy_groups`, `energy_group_members`, `energy_tariffs`, `energy_hours`, and `energy_rates` exactly as [data-model.md](data-model.md) lists them (source `role` one of `consumer`, `house`, `import`, `export`, `production`, at most one live `house`; `gap_limit_s` 60 to 604800, default 900 for power and 21600 for energy; `max_kw` default 25; `energy_hours` keyed by `source_id` and UTC `hour` with `local_date` and `quality` one of `live`, `partial`, `filled`, `carried`; `energy_rates` keyed by UTC `hour`), with list, find, upsert, and range queries; register them in `pkg/migrate/migrate.go`
- [X] T002 Settings key `energy` (`timezone` default the server's local zone, `week_start` 1 to 7 default 1) with load and save in `pkg/energy/settings.go`, and local hour and day helpers that handle 23- and 25-hour days

## Phase 2: Foundation, meter rules and tariff maths

Every user story phase depends on this phase.

- [X] T003 `pkg/energy/meter.go`: the pure accumulation rules from the plan's recording diagram: unit conversion (Wh, kWh, MWh; W, kW), cumulative delta with noise below max(1% of the last value, 0.01 kWh) ignored, larger drops as resets counting the new value, jumps above `max_kw` over the elapsed time rejected keeping the last value, spans over the gap limit carried to the reading's hour, deltas split across hours by time, power by trapezoid between samples within the gap limit, readings in the future or over a day old ignored; returns per-hour kWh with quality and the new state
- [X] T004 [P] `pkg/energy/tariff.go`: tariff validation (ISO 4217 currency, periods covering all 168 week hours without overlap with refusals naming the uncovered or overlapping times, unique `effective_from`, negative rates allowed), the tariff in force on a date, the rate and period at a local time, the next period boundary, and the cheapest window of N hours within a range (refused for a rate control)
- [X] T005 Tests: meter rule table covering every case in the spec's edge cases with hand-worked values to 0.001 kWh (SC-001), and tariff tests for validation, a two-period day with daily charge and export credit to the cent (SC-002), effective dates, daylight saving days, and the cheapest window with five periods (SC-006)

## Phase 3: User story 1, track usage (P1)

**Goal**: sources record hourly usage that survives restarts.
**Independent check**: quickstart scenario 1.

- [X] T006 [US1] `pkg/energy/recorder.go`: load live sources, watch `controls.S.Watch`, `mqttrelay.D.Watch`, and the `ha:*:state:*` broker topic as `pkg/historyfwd/forwarder.go` does, route each change to its sources by kind, ID, entity, and attribute, apply `meter.go`, hold the current hours in memory, and every 30 seconds and on stop upsert dirty `energy_hours` rows and persist `last_value`, `last_at`, `state`, and `state_note`; `Reload` on configuration changes; start and stop in `cmd/karakuri/serve.go`
- [X] T007 [US1] `pkg/energy/rollup.go`: sums by target (`house`, `untracked`, `solar`, `group:<id>`, `source:<id>`) and period (`hour`, `day`, `week`, `month`) in local time, with group membership by `joined_at` and `left_at`, house per FR-006, untracked clamped at zero per hour, and `complete` false when any contributing hour is partial, carried, or missing
- [X] T008 [US1] `pkg/energy/suggest.go`: candidates among MQTT devices (attribute units), number controls (unit option), and Home Assistant entities (`device_class` energy or power, `unit_of_measurement`), each with the source it would make
- [X] T009 [US1] `pkg/energyops`: service layer for sources and groups (validation, at most 200 sources and 50 groups, one house source, soft delete with a daily purge after 30 days, membership history on group edits), settings, and rollup reads, with permission checks (`api:connections:read` to read, `api:connections:write` to change) and recorder reload
- [X] T010 [US1] REST in `pkg/api/energy_handler.go` and MCP in `pkg/mcpserver/tools_energy.go` for sources, candidates, groups, settings, and rollups per the contract
- [X] T011 [US1] Tests on a real database: recorder with a control, a device, and a Home Assistant state; a restart mid-hour losing nothing (SC-004); group history after a membership change; house and untracked derivation; soft delete and purge

## Phase 4: User story 2, cost (P1)

**Goal**: every hour is priced and tariff edits recompute.
**Independent check**: quickstart scenario 2.

- [X] T012 [US2] `pkg/energy/rates.go`: write the `energy_rates` row when an hour opens and the rate control's time-weighted mean when it closes; recompute rows from a tariff's effective date to the next tariff's in one transaction on save and delete
- [X] T013 [US2] Cost measures in `rollup.go`: `cost` (usage times import rate), export credit, `daily_charge` per local day, and `total_cost`, unrounded
- [X] T014 [US2] Tariff CRUD in `pkg/energyops`, REST, and MCP, answering with the hours recomputed
- [X] T015 [US2] Tests: pricing across a period change and an effective date, an edit recomputing cost and not usage, a rate control's recorded mean, and the SC-003 timing with a year of 20 sources

## Phase 5: User story 3, solar and grid (P2)

**Goal**: production, import, export, self-consumption, and solar share.
**Independent check**: user story 3's scenarios on fixed data.

- [X] T016 [US3] `import`, `export`, `production`, `self_consumption`, and `solar_share` measures and the `solar` target in `rollup.go`, with house usage derived from import plus production minus export when there is no house source
- [X] T017 [US3] Tests with the hour of user story 3 (4 kWh produced, 1 imported, 2.5 exported: home 2.5, self-consumption 1.5, share 60%)

## Phase 6: User story 4, flows (P2)

**Goal**: flows read usage and price and fire on period changes.
**Independent check**: quickstart scenario 3.

- [X] T018 [US4] `pkg/nodes/energynodes`: plugin and folder Energy; `energy-usage` (target, span, hours, range, compare with the previous four matching weekdays, output per contract), `energy-price` (now, at, cheapest), failing without retry for unknown targets, no tariff, or a rate control asked about the future
- [X] T019 [US4] `energy-price-period` trigger: next boundary from the tariff on the engine clock, fired boundaries and a watermark in the durable store with a week of catch-up and `engine.MissedDeadlineFields()`, as `plugins/calendar/nodes.go` does; payload per contract
- [X] T020 [US4] Node tests: usage and comparison on fixed data, price now and at, cheapest window, the trigger firing once across a boundary and catching up after a simulated restart

## Phase 7: User story 5, dashboards (P2)

**Goal**: energy in charts and a preset dashboard.
**Independent check**: quickstart scenario 4.

- [X] T021 [US5] `pkg/energy/query.go`: the `energy` language as a `tsdb.Querier` (targets, measure, period including `auto` from the interval, time series and table formats, units `kwatth`, the currency code, or `percent`, at most 10,000 points, metadata listing targets); `tsdb.Query` gains `Energy *EnergyQuery`; `tsdbops.Resolve("energy")` returns it; `query_timeseries`, the time-series query route, and charts accept it
- [X] T022 [US5] Chart query editor in `plugins/core/widgets/chart/src/editor/QueryEditor.svelte` and `config.ts`: connection Energy with a form for targets, measure, and period
- [X] T023 [US5] `pkg/energy/preset.go` and `POST /api/energy/preset` and MCP `create_energy_dashboard` (needs dashboard create): the charts the contract lists, the solar section only with solar sources
- [X] T024 [US5] Tests: the querier's series and table answers against fixed rollups; preset contents with and without solar; a viewer of a shared preset reading its charts without connection permissions

## Phase 8: Settings, filling, and import

- [X] T025 `pkg/energy/fill.go`: on start and on request, fill hours the server missed from Home Assistant hourly statistics (`change` for energy, `mean` for power) or by replaying the source's forwarded `karakuri_control` or `karakuri_mqtt_device` series through `meter.go`, at most 10,000 points per gap, marking hours `filled`; the import of up to 400 days in day chunks with progress on the source; REST and MCP `import`
- [X] T026 `frontend/src/routes/settings/Energy.svelte` with the settings nav entry: sources with last reading, state, and today's usage, add from candidates, groups, tariffs with a week grid of periods, settings, import, and create the preset
- [X] T027 Tests for filling from a seeded history and from Home Assistant statistics through the hatest fake

## Final verification and documentation

- [X] T028 Write `docs/energy.md` (sources and roles, meter rules, groups, tariffs, costs, solar, charts and the `energy` language, flows, the preset, filling and import, limits), serve it as a guide and MCP resource, and update `docs/dashboard-authoring.md`, `docs/flow-authoring.md`, `docs/timeseries.md`, and `docs/mcp.md`; set the spec status to Implemented
- [X] T029 Run a full `make dev`, `make test TARGET=server`, and `make test TARGET=frontend`; restart `karakuri-dev.service`; browser check of the Energy settings page and the preset with settled screenshots and a console check
- [X] T030 Live quickstart with test devices under `karakuri-test` and a read of a lab Home Assistant energy entity; remove the test devices, sources, tariff, and dashboard, clear the test topics, confirm the service is active and http://localhost:8080 answers, and report what was created and removed

Live checks on 2026-11-05 used a test device under `karakuri-test` on the lab
broker reporting kWh and watts: suggestions found both readings; implausible
jumps were rejected and three in a row started the meter again; a reset added
its new value; a restart mid-hour lost nothing (0.019 kWh plus 0.005 after the
restart); a tariff missing Saturday morning was refused with that time named;
cost followed the peak rate and an edit to it repriced the hour without
changing usage; Energy Price found the off-peak window and Energy Usage the
day's figures; the settings page and the preset dashboard were checked in the
browser. T021's change to the Time Series Query node was dropped: flows read energy
through the energy nodes, and FR-021 says so.

## Dependencies and execution order

- T001 and T002 precede everything. Phase 2 precedes the stories; T003 and
  T004 are independent of each other.
- US1 precedes US2 (prices need hours), US3 (measures extend rollups), and US4
  and US5 (both read rollups). US3, US4, and US5 can proceed in parallel after
  US2.
- Phase 8 needs US1; its settings page needs US2's tariffs.
- Commit each verified phase on its own; run a full `make dev` before any
  commit touching chart widget code.

## Parallel opportunities

- T003 and T004; T008 beside T007; T016 beside T018 and T021; T022 beside
  T023.

## Implementation strategy

Usage per source and the house (US1) is the first deliverable, then costs
(US2). Solar, flows, and charts follow on the same rollups, and filling and
the settings page finish the feature.
