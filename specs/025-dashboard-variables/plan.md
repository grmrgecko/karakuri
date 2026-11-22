# Design plan: Dashboard variables

**Feature**: `specs/025-dashboard-variables` | **Date**: 2026-10-26 | **Spec**: [spec.md](spec.md)

## Summary

A variable is a named choice in the dashboard's display settings. Placement
configuration refers to it as `${name}`. The viewer substitutes the current
value before a widget sees its configuration, and dashboard scope substitutes
every option to decide what the layout grants. A custom variable lists its
options, a constant has one, and a query resolves against Home Assistant on the
server. The dashboard-wide value lives in the live display state and changes
through a `variable` command, or is a select entity's state when the variable
follows one. Each viewer keeps its own value, seeded from its address, and
takes the newer of its pick and a dashboard-wide change unless the address pins
it.

The feature started as "filters" and was renamed to "variables" before any
release. Ad hoc filters, a dashboard time range, and interval
variables are not part of it.

## Constitution check

- **Failures**: an unknown variable or value is refused before the state
  changes; a constant or followed variable refuses commands; a query that cannot
  reach Home Assistant resolves to no options and grants nothing. Viewers ignore
  stale values after a redeploy. The command node fails nonretryably.
- **Drafts and deployment**: variables are display settings, so they save to the
  draft and freeze with a deployment through feature 023's paths. Query results
  are not frozen: they are resolved when asked for.
- **Permissions**: the grant stays the deployed layout. A templated binding
  grants the union of its option substitutions. Query options are resolved on
  the server, so the server and viewers agree on them. A followed select grants
  a display-only read of its entity. Previewing an unsaved query takes edit
  access.
- **Existing state**: no dashboard schema change; variables ride in the JSON
  `display` column. Render jobs gain a nullable JSON `variables` column through
  AutoMigrate. The rename means a development dashboard saved with
  `display.filters` loses those definitions; nothing released stored them. No
  database, identity, or secret is invalidated.
- **Evidence**: Go tests cover grant expansion and its cap, the variable command,
  and query resolution against Home Assistant-shaped states. Scope, commands,
  validation, renders, and the viewer and editor were checked against the
  development server and its Home Assistant connection.

## Model

`models.DashboardDisplay.Variables []DashboardVariable`:

| Field | Use |
| --- | --- |
| `name`, `label`, `description` | Reference name, picker label, tooltip |
| `type` | `custom` (empty), `constant`, `query` |
| `display` | `above` (empty), `label_hidden`, `menu`, `hidden` |
| `options`, `default` | A custom variable's list and starting value; set on any type once resolved |
| `value` | A constant's value |
| `query` | `source` (`ha_entities` or `ha_select`), `connection_id`, `pattern` or `entity_id`, `regex`, `sort`, `follow` |

Conditions gain kind `variable` with a `variable` field. `RenderJob` gains
`Variables map[string]string`.

## Resolution

`pkg/dashvars` owns resolution. `Resolve` returns copies with `Options` set for
every type and the followed values; `ResolveFollowing` adds, per followed
variable, a map from each select option as Home Assistant spells it to its
option value. `Query` reads `connops.HADriver(...).StatesCached`, whose
30-second cache bounds load. `FromStates` applies the pattern (anchored, `*` as
any run), the regex (`value` group, first group, or whole match; `text` group
for the label), dedupes, sorts by label, and keeps 50.

Callers resolve where they decide:

- `dashscope.deriveFrom` resolves before expansion and grants followed
  entities. A deployed scope with query variables is cached for 30 seconds
  rather than for the whole version.
- `dashlive.applyVariable` resolves only the commanded variable, so other
  commands never wait on Home Assistant.
- `dashops.ResolveVariableValues` resolves for renders; `ValidateSnapshot`
  resolves for combinations, reports query failures, and checks templated
  configs against widget schemas with defaults substituted.
- The dependency report resolves, reports each query's connection and select
  entity, and dedupes dependencies per placement.

## Substitution

`pkg/dashscope/variables.go` and `frontend/src/lib/variables.ts` rewrite string
values matching `\$\{([A-Za-z][A-Za-z0-9_]{0,31})(:text)?\}`. A string that is
exactly one value reference whose value is a JSON number or `true`/`false`
becomes that type (Go emits `json.Number`). Unknown names stay as written.

## Interfaces

| Surface | Contract |
| --- | --- |
| `display.variables` | Through `update_dashboard`, REST, export, and import |
| `GET /api/dashboards/{id}/variables?view=draft` | Resolved variables, `followed`, `follow_map`; view access, draft for editors and draft renders |
| `POST /api/dashboards/{id}/variables/preview` | Resolve one unsaved variable; edit access |
| MCP `resolve_dashboard_variables` | Resolved variables, followed values, query errors |
| Command `action: "variable"` | `variable`, `value` (a string) |
| Live state | `variables` map; event `variable` |
| Render job | `variables` on REST and MCP; `var-<name>` on the direct address |
| Viewer address | `var-<name>=<value>`, `pin=<name>[,<name>]` or `pin=*` |

## Viewer

`DashboardView.svelte` fetches the resolution before mounting widgets when any
variable is a constant or query, and again after a socket reconnect. It holds
`variableValues`, seeded by `initialValues` (address, then the shared value,
then the default). `sharedValues` is a followed entity's mapped state or the
live value. On a `variable` event or a followed entity's state message it adopts
the variables whose shared value changed, skipping pinned ones. Followed
entities are watched on `ha:{conn}:state:{entity}`, which the layout grants.
Pickers render in the bar for `above` and `label_hidden`, and in a corner
popover for `menu`; none for `hidden`, constants, or renders.

## Editor

`VariablesEditor.svelte` holds drafts with string fields and converts with
`draftOf`/`variableOf`, keeping only the fields a type uses. It lists variables
grouped by display, edits one at a time, reorders within a group, duplicates,
deletes, lists placements by `referencesOf`, and runs the preview endpoint. The
settings dialog is `4xl` wide. The toolbar's Preview popover sets canvas values,
starting from followed values and defaults; the canvas substitutes resolved
variables.

## Widgets

The bridge carries `variables()`, `watchVariables`, and `setVariable` for the
open dashboard, published by the viewer (its values and picks) and the editor
(its preview values). `plugins/core/widgets/shared/variables.ts` reads them and
resolves templates for display. `SourceSelect.svelte` replaces the connection,
control, and device lists in the core panels, and the four copies of
`EntityPicker.svelte` became one shared component. The history graph panel
keeps a whole-string hours reference beside its numeric config.

`variable-buttons` is a `.` binding of source `variable`. With `scope:
dashboard` it grants `CapVariable` for its variable; any other scope grants
nothing. `POST /dashboards/{id}/variables/{name}/value` admits editors, and
otherwise requires that capability with the control policy, then applies the
`variable` command. A screen-scope press calls `setVariable`; a dashboard-scope
press lets the live update switch the pressing screen too, so its address is
not pinned to the value.

The news feed route takes `vars`, a JSON object of the viewer's values; each must
resolve to an option, and the served config is substituted with them and the
defaults before the feed URLs are read. The calendar plugin tags each event with
its source's tag on a copy, because its cache is keyed by feed and shared by
widgets; the widget filters by `showTags`. Sources live only in the plugin's
store: `GET /{dashboard}/{widget}/sources` (edit access) lists them with
`has_api_key` instead of the key, set_source keeps a stored key for a source sent
back without one, and the panel strips sources an older panel left in the config.

## Verification

- `make test TARGET=server`: expansion and its cap, the command, query
  resolution.
- `make test TARGET=frontend`: type checks.
- MCP and REST against the development server: resolution, validation,
  commands for each type, anonymous scope of a public dashboard, renders.
- Browser: display modes, typed substitution, pins, editor tab and preview, with
  screenshots and a console check.

## Time range, interval, and time-series query variables

This section was added 2026-10-30 for features 027 and 028.

### Model additions

| Field | Use |
| --- | --- |
| `type` | Gains `time_range` and `interval` |
| `options`, `default` | Time range presets (label and `from,to` value) and their default; interval durations |
| `auto`, `steps`, `min_interval`, `range_variable` | Interval `auto` sizing |
| `query.source` | Gains `prometheus_label_values`, `prometheus_metrics`, `prometheus_query`, `influx_query`, `sql_query` |
| `query.label`, `query.match`, `query.text`, `query.range_variable` | Prometheus label and selector, query text, and the range used for time macros |

A time range variable with no presets gets the built-in quick ranges:

- the last 5, 15, and 30 minutes
- the last 1, 3, 6, 12, and 24 hours
- the last 2, 7, 30, and 90 days
- today, yesterday, this week, and this month

The default is the last 6 hours.

### Resolution

- `tsdb.ParseRange` (027) validates time range values.
- `DashboardVariable.Resolve(ref)` accepts any parseable value for time range
  variables, so the command, address, render, feed, and chart paths all get
  the rule.
- `dashvars.ResolveFor(vars, values)` resolves in definition order. It hands a
  time-series query the values of the variables before it, substitutes them
  with 025/FR-022 escaping, and runs the query through `tsdbops.VariableOptions`.
- `dashvars.Resolve` (the grant path) runs each chained query once per
  combination of the upstream options and unions the results, capped at 256
  combinations.
- Interval `auto` resolves on the viewer and the server from the named range
  variable's current value.

### Substitution

- Both regexes gain the formats `raw`, `regex`, `sqlstring`, `from`, and
  `to` beside `text`.
- `dashscope.SubstituteConfig` and the viewer apply `from` and `to` for time
  range values and leave the escaping formats as their raw value.
- Language escaping happens only in `dashscope.SubstituteQuery`, which runs on
  the server for time-series query text.
- Grant expansion skips time range variables.

### Viewer and editor

- **Picker:** `DashboardView` draws time range variables with a `TimeRangePicker`
  component in place of the `<select>`. It has a preset list, from and to
  inputs that accept the range grammar, step back, step forward, and zoom out.
  It validates with a TypeScript port of the parser in `frontend/src/lib/timeRange.ts`.
- **Address:** carries `var-range=now-24h,now`.
- **Editor:** `VariablesEditor` gains preset and interval fields, and query
  fields for the time-series sources, with a preview through the existing
  preview route.
