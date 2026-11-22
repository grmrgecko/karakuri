# Design plan: Energy tracking

**Feature**: `specs/035-energy-tracking` | **Date**: 2026-11-05 | **Spec**: [spec.md](spec.md)

## Summary

A new `pkg/energy` package keeps energy sources, groups, and tariffs in
karakuri's database and records each source's usage per local hour. A
recorder watches the same three change streams the history forwarder uses
(controls, devices, Home Assistant states), turns cumulative readings and
power samples into kWh with the reset, noise, jump, and gap rules, and writes
hourly rows. Prices are stored once per hour for the whole house, separately
from usage, so a tariff change rewrites at most 8,784 rows a year and never
touches usage.

Energy data reaches dashboards through the chart widget of feature 028 rather
than a new widget: the energy store answers time-series queries in a new query
language `energy` on a built-in pseudo-connection named `energy`. Every chart
visualization (stat, bar chart, pie chart, time series) then works on energy
data, dashboard scope needs no new binding because a placed chart already
grants its own queries, and the energy preset is an ordinary dashboard of
chart widgets. This replaces the separate energy binding the spec first named;
FR-017 and FR-021 are revised to say so.

Flows get an Energy Usage node (totals, current power, and comparison with the
usual level), an Energy Price node (price now or at a time, and the cheapest
window), and an Energy Price Period trigger that catches up after restarts the
way the calendar trigger of feature 030 does. Missed hours are filled from Home
Assistant's hourly statistics or by replaying the source's forwarded history.
[research.md](research.md) records the decisions,
[data-model.md](data-model.md) the tables,
[contracts/interfaces.md](contracts/interfaces.md) the routes, query language,
nodes, and preset, and [quickstart.md](quickstart.md) the checks.

## Technical context

- Language and dependencies: Go 1.26.5, Svelte 5. No new dependencies.
- Components:
  - `pkg/energy` (new): `model.go` (sources, groups, tariffs, settings),
    `meter.go` (the accumulation rules, pure and table tested),
    `recorder.go` (watchers, per-source state, hourly flush), `tariff.go`
    (validation, rate at a time, hour pricing, cheapest window), `rates.go`
    (the per-hour price table and its recompute), `rollup.go` (sums by
    target, measure, and period, house and solar derivations), `fill.go`
    (filling missed hours and importing history), `query.go` (the `energy`
    query language as a `tsdb.Querier`), `suggest.go` (energy and power
    candidates among devices, controls, and Home Assistant entities),
    `preset.go` (the preset dashboard).
  - `pkg/energyops` (new): service layer shared by REST, MCP, and the UI:
    validation, permission checks, reload of the recorder, recompute.
  - `pkg/tsdbops`: `Resolve("energy")` returns the energy querier; the
    time-series routes accept `energy` as a connection ID.
  - `pkg/nodes/energynodes` (new): `energy-usage`, `energy-price`,
    `energy-price-period`, in folder Energy.
  - `pkg/api/energy_handler.go`, `pkg/mcpserver/tools_energy.go`.
  - `frontend/src/routes/settings/Energy.svelte` and the chart query editor's
    `energy` language form; `cmd/karakuri/serve.go` starts the recorder.
  - Docs: `docs/energy.md` as a guide; `docs/dashboard-authoring.md`,
    `docs/flow-authoring.md`, `docs/timeseries.md`, `docs/mcp.md`.
- Storage: new tables `energy_sources`, `energy_groups`,
  `energy_group_members`, `energy_tariffs`, `energy_hours`, `energy_rates`;
  settings key `energy` for time zone and week start. Created by
  AutoMigrate; no existing table changes.
- Limits: at most 200 sources and 50 groups; a flush of hourly rows every 30
  seconds; history import up to 400 days in day chunks; a fill reads at most
  10,000 points per source per gap; queries return at most 10,000 points.

## Constitution check

- **Failures**: a reading the rules reject is logged and counted, never
  thrown at a flow. A fill that cannot reach its connection leaves the hours
  marked carried and retries at the next start or on request. Flow nodes fail
  retryably only when the store is unreadable; an unknown source, group, or a
  tariff that cannot answer (no tariff, or a rate control asked about the
  future) fails without retry. The period trigger follows the missed-deadline
  fields of feature 029.
- **Drafts and deployment**: node fields come from metadata; nodes read the
  store at run time, so tariff edits need no redeploy. Sources, groups, and
  tariffs are configuration, not drafts, like connections and controls.
- **Permissions**: reading energy (sources, rollups, queries, prices) needs
  `api:connections:read`; changing sources, groups, tariffs, settings, or
  starting an import needs `api:connections:write`. Dashboard viewers read
  energy only through chart placements, which already authorize their own
  queries. The preset needs dashboard create.
- **Existing state**: new tables only; nothing in existing databases changes;
  no key or identity effect.
- **Verification**: `make dev`, `make test`, table tests of the meter rules and
  tariff maths against hand-worked values, recorder tests on a real database,
  a live check with devices on the lab broker under the `karakuri-test` base
  topic and the lab Home Assistant read only, and a browser check of the
  settings page and the preset.

## Design and contracts

### Recording

```text
change (control | device | HA state) -> source lookup by (kind, id, attribute)
  -> parse number, convert unit to kWh or kW
  -> cumulative: delta = value - last
       delta < 0: noise if |delta| < max(1% of last, 0.01) else reset (delta = value)
       delta > plausible max over elapsed: reject, keep last
       elapsed > gap limit: carried (assign to hour of this reading, flag)
       else split delta over the hours between last and now by time
  -> power: trapezoid between last sample and now when elapsed <= gap limit,
       else gap (add nothing, flag)
  -> add to in-memory current hour per source, mark dirty
every 30 s and on stop: upsert dirty hour rows, persist last readings
```

Readings stamped in the future or over a day old are ignored. `unavailable`,
`unknown`, and non-numbers mark the source unavailable and add nothing.

### Prices

`energy_rates` holds one row per local hour: import rate, export rate, tariff
ID, and currency. The recorder writes the row when an hour opens (and the rate
control's time-weighted mean when it closes). Saving or deleting a tariff
recomputes rows from its effective date to the next tariff's, in one
transaction, and marks rollup caches stale. Fixed daily charges are added at
day level when summing. Cost of a source-hour is `kwh * import rate`; export
credit is the export source's `kwh * export rate`.

### Rollups

`Rollup(target, measures, from, to, period)` sums hours in local time.
Targets: `house`, `untracked`, `solar`, `group:<id>`, `source:<id>`.
Measures: `usage`, `cost`, `import`, `export`, `production`,
`self_consumption`, `solar_share`, `daily_charge`, `total_cost`. House usage
follows FR-006; untracked is clamped at zero per hour.

### Charts

The chart widget's query `{connectionId: "energy", language: "energy",
energy: {targets, measure, period}}` resolves to the energy querier. Periods
`hour`, `day`, `week`, `month`, or `auto` from the chart's interval. A `table`
format answers one row per target for pie and bar charts; the stat panel
reduces as usual.

### Flows

- `energy-usage`: target, span (today, yesterday, this week, this month, last
  N hours, or a range), and compare with usual. Output `{kwh, cost,
  currency, power_w?, usual_kwh?, ratio?, complete}`.
- `energy-price`: mode now, at a time, or cheapest window (length, from, to).
- `energy-price-period` (trigger): fires at each period boundary of the tariff
  in force; durable watermark and fired set as in the calendar trigger;
  missed-deadline fields.

### Preset

`POST /api/energy/preset` creates a dashboard named Energy with stat charts
for today, this week, and this month (usage and cost), a 30-day bar chart, a
pie chart by group with untracked, a table by source, and, when solar sources
exist, a time series of production, import, export, and self-consumption.

## State and data model

See [data-model.md](data-model.md). Hourly rows are keyed by source and UTC
hour start, with the local date stored beside it for day grouping across
daylight saving changes. Deleting a source soft-deletes it; a daily sweep
removes its rows after 30 days.

## Verification

| Scenario | Test |
| --- | --- |
| US1 | Meter rule table tests (reset, noise, jump, gap, split across hours, power integration); recorder test on a real database with a restart mid-hour; house and untracked derivation. Live: an MQTT test device under `karakuri-test` reporting kWh. |
| US2 | Tariff validation (coverage, overlap), hour pricing, daily charge, export credit, effective dates, recompute after an edit; SC-003 timing with a year of 20 sources. |
| US3 | Solar derivations from fixed hourly data. |
| US4 | Node tests for usage, comparison, price now and at, cheapest window; trigger test across a boundary and across a simulated restart. |
| US5 | Preset creation test; browser check of the preset and settings page; viewer of a shared preset reads figures without connection permissions. |

## Risks and open decisions

- A gap in a source whose history can be read back is filled from that
  history once its hours close, instead of being carried, so the outage is
  never counted twice. The part of the outage in the hours on either side,
  which hold live figures, is not filled.

- Forwarded history is replayed point by point; a source forwarded at a long
  minimum interval fills less precisely than live recording. Home Assistant
  statistics give exact hourly change and are preferred when the source is a
  Home Assistant entity.
- A rate control has no future prices, so the cheapest-window node refuses
  with a reason under such a tariff.
- Charts are the only dashboard surface; a dedicated energy widget can follow
  if the chart stat panel proves awkward on small panels.
