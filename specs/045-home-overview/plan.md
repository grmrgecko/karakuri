# Design plan: Home and overview

**Feature**: `specs/045-home-overview` | **Date**: 2026-11-21 | **Spec**: [spec.md](spec.md)

## Summary

`/` becomes a start page that the browser resolves per user. The flow list
moves to `/flows`. Builders get an overview whose server-computed sections
(attention, upcoming, unfinished work) come from a new `pkg/homeops` service.
REST and MCP share that service. Running now, recent problems, and quick
launch reuse existing endpoints. Start page and pins are two keys in the
existing user preferences. Read [the flow guide](../../docs/flow-authoring.md),
[the event log guide](../../docs/events.md), and
[the backup guide](../../docs/backup.md) first.

## Technical context

- **Language and dependencies:** Go with GORM on SQLite, swaggest REST
  handlers, the MCP Go SDK, and a Svelte 5 frontend with svelte-routing. No
  new dependencies.
- **Components:**
  - `pkg/homeops`: new. `Attention`, `Upcoming`, and `Unfinished`, each
    taking the request context and caller.
  - `pkg/engine`: a `NextFirer` optional interface on node types.
  - `pkg/nodes/core`: `ScheduleNode` implements `NextFirer` from the same
    cron and sun code its `Start` uses.
  - `pkg/backup`: exported `Overdue(schedule, lastSuccess, now)`, built on
    `NextTime`.
  - `pkg/agentops`: `Connected(id)` from the hub.
  - `pkg/api`: `home_handler.go` with three GET routes.
  - `pkg/mcpserver`: `tools_home.go` with `get_attention`, `list_upcoming`,
    and `list_unfinished_work`.
  - `frontend/src`:
    - `routes/Home.svelte` resolves the start page and draws the overview.
    - `lib/homeStart.ts` holds the pure resolution rule, with a vitest test.
    - Route and link changes in `App.svelte`, `Nav.svelte`, `Login.svelte`,
      `FlowEditor.svelte`, `FlowShare.svelte`, and `FolderShare.svelte`.
    - A start page field on `settings/Profile.svelte`.
    - A pin toggle on `DashboardList.svelte`.
- **Storage:** two keys in `users.preferences`: `start_page` and
  `pinned_dashboards`. There is no new table, and attention and upcoming
  items are computed per request. See [data-model.md](data-model.md).
- **Limits:**
  - The upcoming window is 12 hours with at most 20 items.
  - Unfinished work lists at most 10 flows and 10 dashboards.
  - Recent problems lists 8 events from 24 hours.
  - Flow failures look back 24 hours. Runs are already capped at 100 per
    flow.

## Constitution check

- **Failures:** each attention source is read independently. A failing
  source is logged at error and reported under `unchecked`, and the others
  still answer, so an empty list never claims all clear for a source that
  could not be read (FR-007). Reads only; nothing retries or has side effects.
  Cancelling a wait or run goes through the existing `CancelWait` and
  `CancelRunFor`.
- **Drafts and deploys:** unfinished work reads the existing draft-change
  comparisons (`flowops.Item`, `dashops.AnnotateDraftState`). Upcoming reads
  only deployed, enabled graphs, so an unsaved or undeployed schedule never
  appears. REST and MCP call the same `homeops` functions (principle II).
- **Permissions:** every source is gated in `homeops`, and the routes need
  only authentication:
  - Connections and history need `api:connections:read`.
  - Agents need `api:agents:read`.
  - Flow failures need `api:runs:read` plus view on the flow.
  - Backups need `api:backups:read`.
  - Upcoming needs `api:flows:read` plus view on the flow; waits also need
    `api:runs:read`.
  - Unfinished work needs edit on each flow or dashboard.

  `auth.Can` is used, so limited API keys apply. The start page and pins are
  validated in the browser against what the dashboard API returns for the
  caller, so a preference can never reveal a dashboard (principle III).
- **Existing state:** this adds preference keys only. No migration, and no
  change to secrets, identities, or CA material (principle IV). The flow list
  URL changes from `/` to `/flows`; no redirect is kept (see spec
  assumptions).
- **Verification:**
  - Builds and tests: `make dev`, `make test TARGET=server`, and
    `make test TARGET=frontend`.
  - Live MCP checks of the attention and upcoming tools, and a browser check
    of the overview, start page, and pins, per [quickstart.md](quickstart.md).

## Design and contracts

### Start page (browser)

`resolveStart(prefs, canReadFlows, dashboards)` in `lib/homeStart.ts`
returns one of `{overview}`, `{dashboard: id}`, or `{list}`:

1. If `start_page` names a dashboard ID that is in the caller's dashboard
   list, that dashboard.
2. Otherwise, if `start_page` is `overview` or the caller holds
   `api:flows:read`, the overview.
3. Otherwise, if the caller can view exactly one dashboard, that one.
4. Otherwise, the dashboard list.

`Home.svelte` loads preferences from the auth store and one page of
dashboards (limit 2, plus a direct get of the preferred ID), then either
navigates with `replace` or draws the overview. An unauthenticated visitor is
sent to `/login`, as `FlowList` does today. Login without a `redirect` query
navigates to `/`, so sign-in lands on the start page.

Profile shows a select with three options: "Default", "Overview", and the
caller's dashboards. A stored ID absent from that list shows as "Unavailable
dashboard" without a name (FR-003).

### Pins (browser)

`pinned_dashboards` is an ordered array of dashboard IDs. The dashboard list
gets a pin toggle per row. Quick launch shows pinned dashboards in order,
resolved against the dashboard list and skipping missing ones, with
up and down reorder buttons and unpin. Writes use the existing
`PUT /api/auth/preferences` merge.

### Attention (`homeops.Attention`)

`Attention(ctx, userID, now) (AttentionResult, error)` gathers the sources
the caller may read. See [contracts/rest.md](contracts/rest.md) for the
shape.

- **Connections:** `models` lists enabled connections, and
  `connops.Manager().Statuses()` gives their state.
  - `error` gives an error item with the error and `since`.
  - `connecting` for over two minutes gives a warning.
  - `connected` with three or more failures in the last hour gives a
    warning. Failures are counted as the sum of `count` over event log rows
    with category `connection`, level `error`, this subject, and
    `last_at >= now-1h`. The event source records every failed attempt and
    folds repeats, so the sum is the drop count.
  - A connection missing from the manager's statuses gives no item.
- **Agents:** enabled, unrevoked agents for which `agentops.Connected(id)`
  is false give an error item with `last_seen_at`. If the hub is not running,
  agents are reported as unchecked.
- **Flows:** runs with `status = failed` and `started_at >= now-24h` for
  flows in `flowops.ReadableFlowIDs`, grouped by flow and error text. Each
  group carries its count, newest time, and newest run ID. The newest
  finished run per flow decides between error and a warning marked
  `recovered`. Handled failures (`had_failures` on a succeeded run) are
  excluded.
- **Backups:** the newest finished run from `models.ListBackupRuns` gives an
  error item if it failed or its verification failed. With the schedule from
  `Service.View()`, `backup.Overdue` is true when the schedule is enabled and
  the next scheduled time after the last successful start (or after
  `LastScheduledAt` when none succeeded) is more than an hour in the past.
  That gives a warning.
- **History:** each history writer status from `tsdbops` in state `failing`
  gives a warning naming the destination connection and its last error.

Items sort with errors first, then by `at` descending. `checked` lists the
sources read, `unchecked` the ones that failed or are not running, and
`summary` holds the counts the all-clear line needs, such as connections
connected and agents online. Each item's `link` is an app path:

| Kind | Link |
| --- | --- |
| Connection | `/settings/connections` |
| History | `/settings/history` |
| Agent | `/settings/agents` |
| Flow | `/flows/{id}`, which opens the editor and its run panel |
| Backup | `/settings/backup` |

### Upcoming (`homeops.Upcoming`)

`engine.NextFirer` is
`NextFire(config json.RawMessage, after time.Time) (time.Time, bool, error)`.
`Upcoming(ctx, userID, now, window)` walks `models.ListDeployedEnabledFlows`
restricted to readable flows. For each, it parses the deployed graph's root
nodes, which are the ones the trigger runner arms, and asks each node type
implementing `NextFirer` for its next time after `now`. It keeps times within
the window. A config that fails, or a sun event with no occurrence, is
skipped and logged at debug. Pending waits with a deadline in the window come
from `models.ListPendingWaits`, filtered to readable flows, when the caller
holds `api:runs:read`. Each wait carries `node_id` and `key` for the existing
cancel route, and `can_cancel` is true when the caller holds
`api:runs:cancel` and edit on the flow. Items sort by time, capped at 20.

### Unfinished (`homeops.Unfinished`)

- **Flows:** flows the caller holds edit on (manage holders see all),
  excluding subflows. `flowops.Item` decides `HasDraftChanges`. Sorted by
  `updated_at` descending, at most 10.
- **Dashboards:** dashboards from `models.ListAccessibleDashboards` where
  `dashops.CanAccess(..., edit)`, annotated with `AnnotateDraftState`.
  Treated the same way.

Each section is returned only when the caller holds `api:flows:write` or
`api:dashboards:write` respectively.

### REST and MCP

The routes are listed in [contracts/rest.md](contracts/rest.md):

- `GET /api/home/attention`
- `GET /api/home/upcoming`
- `GET /api/home/unfinished`

They need authentication only, with per-source gating inside. The MCP tools
`get_attention`, `list_upcoming`, and `list_unfinished_work` return the same
structs and need `api:mcp:use`, which the server already requires.

### Overview (browser)

`Home.svelte` draws these sections in FR-010 order:

1. **Attention** is one card. It shows items with severity icons and links,
   or an all-clear line built from `summary` and `checked`, followed by an
   "Could not check: …" line when `unchecked` is non-empty.
2. **Coming up** is a time-ordered list, with cancel calling
   `DELETE /api/flows/{id}/waits/{node}?key=`.
3. **Running now** uses `/api/runs/active` and appears only when it is
   non-empty. Cancel uses the existing run cancel.
4. **Unfinished work.**
5. **Quick launch.**
6. **Recent problems** uses `/api/events?level=warning&level=error&from=now-24h&limit=8`
   and links to `/activity?level=warning&level=error`. Activity is changed to
   read `level` from the query, as it already reads `subject`.

Each section is shown only when its permission check in the auth store
passes. The overview watches the `runs`, `flows`, and `dashboards` topics and
`events` messages at warning or error level through `watchLists` in
`frontend/src/lib/liveList.ts`. A notice waits 300 ms for quiet, at most two
seconds in a burst, and at least two seconds after the last live refresh. A
reconnect also refreshes. The 30-second interval while
`document.visibilityState` is `visible` and the refresh on `focus` remain for
sources with no topic, such as a backup going overdue.

### Live lists

`models.WatchListChanges` registers GORM create, update, and delete callbacks
on the flow, folder, dashboard, dashboard group, and share tables. Each
affected table marks its topic, `flows` or `dashboards`, and one `changed`
message is published 250 ms after the first write of a burst. That catches
REST, MCP, import, and restore alike and lets the write's transaction commit
before pages read again. Writes through raw SQL are not seen. The notice
carries no data; pages read the list again through the API, which applies the
reader's shares. `flows` is gated on flow read, and `dashboards` on any
signed-in user, since a dashboard can be shared with someone without dashboard
permissions.

The engine publishes every `run_started` and `run_finished` message on the
`runs` topic as well as the flow's own topic. The topic is gated on run read,
and a message filter delivers each message only to sockets whose user may
watch that run's flow, as feature 043 FR-011 requires. The flow list keeps the
runs in progress per flow from these messages and draws its dot from them; a
list notice reads the flows again and reads the last run only of flows new to
the page.

## State and data model

See [data-model.md](data-model.md). The two preference keys are written
through the existing merge endpoint. Attention, upcoming, and unfinished
results are request-scoped structs.

## Verification

| Scenario | Check |
| --- | --- |
| Story 1 (attention) | `homeops` tests with a real test database cover: failed runs grouped by error with the recovered flag; handled failures excluded; flapping from event rows; the readable-flow filter; backup failure and `Overdue`; and an unchecked source. Live: stop a connection through `save_connection` with a bad host and call `get_attention`. |
| Story 2 (start page) | A vitest for `resolveStart` covering the four rules and an unavailable preference. A browser check as admin and as a dashboard-only user. |
| Story 3 (upcoming) | A `ScheduleNode.NextFire` test for cron and sun modes, and a `homeops.Upcoming` test that disabled flows are skipped. Live: deploy a test flow with a schedule an hour out and call `list_upcoming`. |
| Story 4 (unfinished, pins) | A `homeops.Unfinished` test for never-deployed and changed drafts and the edit filter. A browser check of pin, reorder, and unpin. |
| Story 5 | A browser check of running now and recent problems against live data. |

Run `make dev`, then `make test TARGET=server` and `make test TARGET=frontend`.
Restart `karakuri-dev.service` and check that it is active and that
`http://localhost:8080` answers.

## Risks and open decisions

- **Unread status topics:** connection status is read from the manager, not
  the event log, so a connection that failed before the event log was enabled
  still shows. The flapping count depends on the event log's `connection`
  category being recorded. If an administrator excluded it, flapping is not
  detected; the current error state still is.
- **Failure count per refresh:** grouping scans at most 100 runs per readable
  flow from the last 24 hours. This is bounded by existing pruning.
- No open decisions.
