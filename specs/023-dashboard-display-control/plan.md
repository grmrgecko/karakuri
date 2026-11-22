# Design plan: Dashboard display control

**Feature**: `specs/023-dashboard-display-control` | **Date**: 2026-10-24 | **Spec**: [spec.md](spec.md)

## Summary

A dashboard gains two kinds of new state. Authored state is versioned with the
layout: pages, dashboard display settings, and each placement's page, region,
and display settings. Runtime state is held per dashboard in server memory and
changed by commands: locks, the commanded page, alerts, a dim override, and
pushed content. Viewers read the runtime state over HTTP once and follow a
broker topic after that. Everything a viewer decides from the two (visibility,
the current page, dimming, alerts) is computed in the browser from pure
functions shared by the viewer and the editor.

## Technical context

- Language and dependencies: Go with GORM 1.31 (`serializer:json` for the new
  structured columns), Svelte 5 frontend, Svelte custom-element widgets built by
  the existing widget rule, `gofeed` (already used by the RSS node), and the
  documentation Markdown renderer in `pkg/docs`.
- Components: `pkg/models` (page, display types, snapshot), `pkg/dashops`
  (pages, validation, export), new `pkg/dashlive` (runtime state and commands),
  `pkg/dashscope` (condition grants, live topic), `pkg/api` and `pkg/mcpserver`
  (endpoints and tools), `pkg/nodes/core` (command node), `pkg/integrations/notification`
  (dashboard service), `frontend/src` (viewer, editor, shared layout logic),
  `plugins/core/widgets` (three widgets), `themes/mirror`.
- Storage: new table `dashboard_pages`; new nullable columns `dashboards.display`,
  `dashboard_widgets.page_id`, `dashboard_widgets.region`, and
  `dashboard_widgets.display`. AutoMigrate adds them. Runtime state is memory only.
- Limits: five alerts per dashboard; 16 locks per placement; 64 KiB content per
  placement; 200-byte alert titles and 4 KiB messages; page interval 5 to 3600
  seconds; animation duration up to 5000 ms; pixel shift up to 16 pixels; news
  feeds fetched with a 10-second timeout, a 2 MiB body limit, at most 5 feeds per
  placement, and a 5-minute cache per URL.

## Constitution check

- Failures: an invalid command is refused before any state change. The command
  node and the dashboard notification service mark every failure nonretryable;
  a repeated alert is a visible side effect. News feed fetch failures are
  reported per feed and keep the last good items.
- Draft and deployment: authored settings follow the existing draft rules and
  freeze with deployment. Commands never touch the draft. REST, MCP, editor
  chat, and the browser go through `dashops` for pages and display settings.
- Permissions: the live state is readable by anyone who may view the dashboard,
  the same people who read its layout. Commands through REST and MCP need edit
  access. Flows command any dashboard because flow deployment is already the
  trust boundary for server actions such as setting controls. Condition bindings
  grant display-only reads through dashboard scope. The news route resolves URLs
  from the served placement, so a viewer cannot make the server fetch an
  arbitrary URL.
- Existing state: only additive columns and one new table. Existing snapshots
  parse unchanged, and a dashboard with no new settings snapshots byte-identically
  to its earlier deployments. No keys, identities, or CA material are touched.
- Checks: `make dev`, `make test TARGET=server`, `make test TARGET=frontend`,
  service restart, MCP command checks, and browser checks of the viewer and
  editor with screenshots and the console.

## Design and contracts

### Runtime state (`pkg/dashlive`)

`Store` maps dashboard ID to a `State`:

```go
type State struct {
    Seq     int64                      `json:"seq"`
    Locks   map[string][]string        `json:"locks"`   // placement ID -> lock names
    Page    string                     `json:"page"`    // page ID, empty for none commanded
    Alerts  []Alert                    `json:"alerts"`
    Dim     *int                       `json:"dim"`     // override level 0-100
    Content map[string]Content         `json:"content"` // placement ID -> pushed content
}
```

`Apply(dashboardID, Command)` loads the served layout (deployed snapshot, or
the draft when undeployed), validates the command against it, mutates the
state under the store lock, raises `Seq`, and publishes
`dashboard:{id}:live` with `{state, event}`. `event` names the action for
transient actions (`next_page`, `previous_page`, `reload`) and for `page`, so a
viewer restarts its rotation timer. The first lock change for a placement
seeds `start` when the placement is authored hidden. Alerts carry an ID and an
expiry; one timer per alert removes it and publishes. A deleted dashboard's
state is dropped by `Forget`.

`content` accepts any JSON value up to 64 KiB. With `format: "markdown"` and a
string value, the store also keeps HTML rendered by `docs.Render`, which
escapes raw markup. A `null` value clears the placement's content.

### Commands

`Command` fields: `action`, `targets` (placement IDs), `lock`, `force`,
`page`, `title`, `message`, `level`, `style`, `duration`, `alert_id`,
`dim_level`, `value`, `format`. Entry points:

- `POST /api/dashboards/{id}/commands` and MCP `command_dashboard`, both after
  `dashops.CanEdit`.
- `GET /api/dashboards/{id}/live`, after the same view check the layout read
  uses, including render tokens for that dashboard.
- Flow node `dashboard-command` (folder Dashboards) with field controls
  `dashboard-picker` and `dashboard-widgets`. Text fields interpolate `{{ }}`.
  The `value` field takes the incoming payload when left empty.
- Notification service `dashboard`: endpoint target is a dashboard ID or slug;
  settings `style`, `level`, and `duration`. Its notifier calls a hook that
  `serve.go` binds to `dashlive.Apply`, keeping the notification package free of
  dashboard imports.

### Authored settings

```go
type DashboardDisplay struct {
    PageInterval int        `json:"page_interval,omitempty"`
    NightDim     *DimWindow `json:"night_dim,omitempty"`  // from, to "HH:MM", level
    IdleDim      *IdleDim   `json:"idle_dim,omitempty"`   // after seconds, level
    PixelShift   int        `json:"pixel_shift,omitempty"`
}

type WidgetDisplay struct {
    Name       string      `json:"name,omitempty"`
    Hidden     bool        `json:"hidden,omitempty"`
    Match      string      `json:"match,omitempty"`      // all (default) or any
    Conditions []Condition `json:"conditions,omitempty"`
    AnimateIn  string      `json:"animate_in,omitempty"`
    AnimateOut string      `json:"animate_out,omitempty"`
    Duration   int         `json:"duration,omitempty"`
}
```

A `Condition` is `{kind: "state", source, controlId, connectionId, entityId,
deviceId, attribute, operator, value}` or `{kind: "time", from, to, days}`.
Its binding fields match `dashscope.Binding`, so derivation reuses the binding
parser and marks each one display-only.

Pages are rows of `DashboardPage`. `dashops` adds, updates, reorders, and
removes them; removal clears `page_id` on its placements in the same
transaction. Placement writes accept `page_id`, `region`, and `display`; an
empty `page_id` means every page.

### Viewer and editor

`frontend/src/lib/displayState.ts` holds the pure logic: page order and the
current page, locks for a placement, time windows, condition comparison,
visibility, and the effective dim level. `lib/dashboardLive.ts` loads and
follows the live state and refetches after a socket reconnect.
`watchConditionSources` in the same file loads and follows the sources
conditions name.
`lib/regionLayout.ts` lists regions and groups placements into them.

`DashboardView.svelte` renders grid layouts as before, or `RegionLayout.svelte`
for the region mode, and wraps every placement in a `presence` action that
runs Web Animations on visibility changes and hides through a `data-presence`
attribute (hidden in grids, collapsed in regions) rather than unmounting, so a
widget keeps its state across page flips. The attribute matters: the cell's
style attribute carries its geometry and is rewritten when that changes.
Widgets follow live state through the bridge call `watchDashboardLive`, which
refetches after a socket reconnect.
`DashboardAlerts.svelte` draws toasts and full-screen alerts. A dim overlay
and a pixel-shift transform sit on the viewer surface. Render mode skips dim,
idle, pixel shift, rotation, and `reload`.

`DashboardEdit.svelte` gains a page bar (all pages, each page, add, rename,
reorder, delete) that filters the canvas, a Display section in the widget panel
(name, page, region and order, hidden on start, conditions, animations), the
region layout mode with selectable region cards, and the dashboard display
settings in the settings dialog.

### Widgets and theme

- `rotating-message`: lists keyed `morning`, `afternoon`, `evening`, `anytime`,
  date patterns (`YYYY-MM-DD` with `.` wildcards), five-field cron keys, and
  condition keys matched against an optional `weather` binding, such as a Home
  Assistant `weather.home` entity whose state is `rainy` or `sunny`. The manifest
  declares `scope.bindings: "weather"`, so the binding is granted like any other
  display-only read. `specialDayUnique`, `interval`, `fade`. Pushed string or
  string-list content replaces the lists.
- `flow-text`: shows pushed content as text or server-rendered Markdown, with
  a placeholder when none has arrived, and drops images that are not same-origin.
- `news-feed`: `feeds: [{url, title}]`, `interval`, `maxItems`, `maxAgeHours`,
  `showDescription`; reads `GET /api/dashboards/{id}/widgets/{widget}/feed`.
  `content` commands `next`, `previous`, and `details` steer it.
- `themes/mirror`: black base, transparent widget cells through
  `--theme-widget-bg` (other themes set it to their card color), solid app
  cards so dialogs stay readable, no shadows, light type.

## State and data model

- `DashboardPage{ID uuid, DashboardID, Title, SortOrder, timestamps}`, deleted
  with its dashboard.
- `Dashboard.Display *DashboardDisplay` (JSON column), versioned in
  `DashboardSnapshot.Display`. Feature 025 adds `Variables` to it, and a
  `variables` map of dashboard-wide values and the `variable` action to the live
  state; see [its plan](../025-dashboard-variables/plan.md).
- `DashboardWidget.PageID *uuid`, `Region string`, `Display *WidgetDisplay`
  (JSON column), versioned in `DashboardSnapshotWidget`.
- `DashboardSnapshot.Pages []DashboardSnapshotPage`, sorted by ID.
- New snapshot fields are `omitempty` and zero display settings are stored as
  NULL, so `HasDraftChanges` stays false for dashboards deployed before the
  change.
- `RestoreDashboardSnapshot` writes pages back under their IDs before
  placements, and removes pages added since the version.
- Export carries pages by index, so import recreates them with new IDs and
  remaps placements.

## Verification

- Go tests: `pkg/dashlive` lock semantics with start seeding and forced
  release, refusal of invalid commands (including unknown placements) without
  state change, and the alert cap; snapshot equality for a dashboard without
  new settings and the round trip with them; and, through the real router, a
  condition granting an anonymous viewer one entity read and no write, editor
  only commands, and the public live read. Page removal, validation messages,
  the notification target, and the feed route were checked live through MCP.
- The frontend test group is `svelte-check`; it has no unit-test runner, so
  the pure logic in `displayState.ts` is checked by type-checking and by the
  live browser checks below rather than by unit tests.
- Live checks: MCP `command_dashboard` for every action against two browser
  viewers; flow node run through MCP; notification endpoint send; region
  layout render; editor page bar, display panel, and region editing in the
  browser with screenshots and a console check.

## Risks and open decisions

- Runtime state is lost on restart by design; a flow that hid a widget must
  hide it again after a restart. This is documented, not hidden.
- The live topic follows the socket revalidation of feature 001 FR-016: when
  access changes, an open subscription that no longer passes the view check is
  withdrawn within the revalidation interval.
- Weather-keyed messages need a bound source that reports a condition word.
  Home Assistant weather entities do; the weather plugin's forecasts are not a
  host binding and are not read.
