# Design plan: Flow sharing

**Feature**: `specs/043-flow-sharing` | **Date**: 2026-11-21 | **Spec**: [spec.md](spec.md)
**Status**: Implemented

## Summary

Enforce flow ownership and add per-flow shares at faceplate, view, edit, and
admin, following the dashboard model of feature 007. Every flow entry point
asks one question, "what level does this user hold on this flow", and
refuses with not-found below the level it needs. Runs, live streams, the
event log, dashboard trigger bindings, and editor chat follow the same check.
Read [the flow guide](../../docs/flow-authoring.md) and
[dashboard security](../../docs/dashboard-security.md) first.

## Technical context

- **Language and dependencies:** Go with GORM on SQLite (ncruces wasm),
  swaggest REST handlers, the MCP Go SDK, Svelte 5 frontend. No new
  dependencies.
- **Components:**
  - `pkg/models`: `FlowAccess` model, level ranks, the grant query, and
    `ListAccessibleFlows`.
  - `pkg/flowops`: `LoadFor` and the `CanFaceplate`, `CanView`, `CanEdit`,
    and `CanManage` checks, shared by REST and MCP. This mirrors
    `pkg/dashops`.
  - `pkg/api`: flow, subflow, export, run, wait, node chat, folder move, and
    widget trigger handlers; new share handlers; the event handler.
  - `pkg/mcpserver`: flow, diff, node, run, state, reference, dependency, and
    assistant tools.
  - `cmd/karakuri/serve.go`: topic gates for `flow:*`, `editor:flow:*`, and
    `run:*` become callbacks.
  - `pkg/dashops`: the deploy check for flow-trigger bindings.
  - `pkg/permissions`: `api:flows:manage`.
  - `frontend/src`: flow list filtering is server-side; add a flow share
    dialog modeled on `DashboardShare.svelte`, and hide controls the caller's
    level does not allow.
- **Storage:** a new `flow_accesses` table, described in
  [data-model.md](data-model.md). Owners are already stored.
- **Limits:** none new.

## Constitution check

- **Failures:** authorization failures return not-found for flows the caller
  holds nothing on, and forbidden for a held flow at too low a level, as
  dashboards do. Runtime execution is untouched. Triggered runs, error flows,
  and deployed subflows run as the system, so losing a share never breaks a
  deployed automation (FR-008, SC-004).
- **Drafts and deploys:** shares sit outside versions, and restore changes no
  shares. REST, MCP, and editor chat reach the same `flowops` checks
  (principle II).
- **Permissions:** the server checks every flow route. Global permissions keep
  saying whether a user may author at all, and manage admits everything, the
  same split dashboards use (principle III). Dashboard scope keeps its own
  path; only binding a trigger on a flow is newly checked, at deploy.
- **Existing state:** this is an additive table. No secret, identity, or CA
  change. On the development database `admin` owns every flow and keeps
  access through `api:*`. Other accounts lose access to flows they do not own
  until shared, which is the feature (principle IV).
- **Verification:** `make dev`, `make test TARGET=server`, and
  `make test TARGET=frontend`; live checks through MCP and the browser listed
  in [quickstart.md](quickstart.md).

## Design and contracts

### One check

`flowops.LoadFor(uid uuid.UUID, id string, level string) (*models.Flow, error)`
resolves the flow and returns not-found when the caller holds no level on it,
or forbidden when the held level is below `level`. `flowops.Level(uid, f)`
returns the held rank for handlers that branch, such as the flow page
deciding which tabs to show. The rank is the highest of:

- owner;
- `api:flows:manage`;
- direct shares;
- group shares.

It is computed in `models.FlowGrantRank`, which is the same shape as
`dashboardGrantRank`.

The level each route needs is listed in
[contracts/authorization.md](contracts/authorization.md). The rules:

- Reads need view.
- Changes, tests, deploys, enablement, and run or wait cancellation need
  edit.
- Shares and deletion need admin.
- Faceplate routes, when feature 042 adds them, need faceplate.

Global permissions are still checked by route middleware first. The per-flow
level is checked after it.

### Lists

`models.ListAccessibleFlows(uid, groupIDs, page, size, isSubflow)` filters by:

- owner;
- direct share;
- group share.

Holders of manage use the existing unfiltered `ListFlows`. Faceplate-only
grants are excluded from the list until feature 042 can show their
faceplates, so a flow is never listed to someone who cannot open any part of
it. The REST list, MCP `list_flows`, `find_references`, and the widget
trigger list use it.

### Runs, waits, and live streams

- **Run routes:** route on the run's flow. `CancelRun` loads the run, then
  checks edit on its flow. `ActiveRuns` filters snapshots to flows at view or
  above. `list_flow_runs`, `get_flow_run`, and `inspect_flow_state` call
  `LoadFor` before their direct queries.
- **WebSocket gates:** `flow:*`, `editor:flow:*`, and `run:*` move to
  `RegisterTopicPermissionCallback`.
  - The flow callbacks parse the flow ID from the topic.
  - The run callback looks up the run's flow through the engine snapshot,
    falling back to the `runs` table.
  - Each callback keeps the global permission as its first condition.
  - The 30-second socket revalidation in `pkg/mq` withdraws subscriptions
    after a share is removed.
  - The `runs` topic, which carries every flow's run start and finish for the
    flow list and overview, is gated on run read and filtered per message by
    `flowops.CanWatchFlow` on the run's flow. The `flows` list topic carries
    no flow data, so it needs only flow read.

### Cross-flow references

- **Subflow references on save:** `SaveDraftChecked` and `PatchGraph`
  compare the graph's shared subflow IDs with the current draft's. A newly
  added reference needs view on the subflow. References already in the draft
  stay, so a parent never becomes unsaveable because its author lost access
  (FR-008).
- **Validation and runtime:** the validation resolver and runtime resolution
  stay user-free.
- **Error flow:** `error_flow_id` set through `Update` needs view on the
  target.
- **Copying another flow's graph:** `import_subflow` needs view on the
  source. Export needs view on every embedded subflow and names the first one
  it refuses.

### Triggers

- **By permission:** `POST /widget_triggers/{flow_id}/fire` without dashboard
  scope or public marking now needs `api:flows:trigger` plus edit on the
  flow.
- **Dashboard deploy:** `dashops.Deploy` walks the deployed layout's
  flow-trigger bindings and refuses any whose flow the deployer cannot edit.
  Bindings already deployed keep working, since scope derivation reads the
  frozen layout.

### Event log

The event handler and MCP `list_events` pass a subject filter. It drops
`flow` subjects the caller holds no view on, unless they hold manage, which
follows the `SubjectAllowed` pattern dashboards use. Run events already carry
the flow as their subject.

### Editor chat and MCP

The unpinned tools `list_flows`, `get_flow`, `find_references`, and
`get_document_dependencies` call the same checks, so chat about one flow
cannot read another the user was not shared. In `get_document_dependencies`,
the per-reference check goes in `reference()` for kind `flow`.

### Shares API

These are listed in [contracts/rest.md](contracts/rest.md):

- `GET /api/flows/{id}/shares` and `POST` need admin.
- `DELETE /api/flows/{id}/shares/{shareId}` needs admin.
- The flow response gains `access_level` so the editor can hide controls.

Each share change records an event with the actor, grantee, and level
(FR-009).

### Folders

`models.FolderRanks` gives a user's rank on every folder: the folder owner and
shares, carried down to subfolders. `FlowGrantRank` and
`ListAccessibleFlows` include the rank of the flow's folder. Folder routes use
`flowops.AuthorizeFolder`: view to list, edit to rename and add into, admin to
move, delete, and share. `CheckFolderTarget` guards every placement, including
flow create and move, which also needs admin on the flow. `MaskFolder` and
`ListFoldersFor` hide folders the caller cannot see. Shares live in
`folder_accesses` and are served at `/api/folders/{id}/shares`.

### Slugs

IDs are canonical; slugs resolve per caller. `models.LookupDashboardVisible`
matches a slug among dashboards a predicate accepts and reports
`ErrSlugAmbiguous` for several. `dashops.ResolveFor` and the API's
`resolveDashboard`, which also admits the dashboard a render token names, wrap
it. Routes answer an ambiguous slug with `AMBIGUOUS` and the matches by ID.
`GET /api/flows/by_slug/{slug}` and `GET /api/dashboards/by_slug/{slug}` list
the caller's matches. The frontend's `ResolveRef` wraps the flow and dashboard
routes: an ID renders the page, one match replaces the address with its ID,
several show a chooser. Every app link uses IDs. Paths acting for no user,
such as a flow's Dashboard Command node, resolve a slug only when exactly one
dashboard uses it.

### Ownership transfer

`PUT /api/flows/{id}` accepts `owner_id` from holders of manage only. That
covers flows left behind by a deleted user.

## State and data model

See [data-model.md](data-model.md). Deleting a flow, user, or user group
removes its share rows in the same transaction as the deletion. Backup dumps
every registered model, so `FlowAccess` is carried once it is added to
`models.All()`.

## Verification

| Scenario | Evidence |
|---|---|
| US1, SC-001 | An API test: user B's list, get, graph, runs, and delete on user A's flow return not-found; a manage holder sees it. |
| US2, SC-003 | An API test: an edit share saves and deploys and is refused on shares and delete; an admin share passes both; a group share admits a member. |
| US3, SC-002 | Deferred to feature 042's faceplate routes. Here, a test that a faceplate share is refused on every graph route and is not listed. |
| US4 | The same API test adds, lists, and removes shares and refuses an unknown level. |
| US5, SC-004 | A test that a new reference to an unshared subflow is refused, an existing one saves, and a deployed parent runs after access is removed. |
| SC-005 | A test of the topic callbacks for a shared and an unshared user; a test of event log filtering. |
| SC-006 | A test that dashboard deploy with a trigger on an unshared flow fails. |
| UI | A browser check: the share dialog adds and removes a user; a view-share user sees the graph read only with no save or deploy; console clean. |
| SC-007 | `make dev`, `make test`, restart `karakuri-dev.service`, and confirm it answers. |

## Risks and open decisions

- **The User role can no longer act on flows it does not own.** On an
  installation with several users this ends shared editing until flows are
  shared. That is the feature, but the release notes and the flow guide need
  to say so.
- **Folders.** Folder shares inherit down the tree. `models.FolderRanks`
  reads every folder and the user's folder grants in two queries and walks
  each folder's path. That suits a household's folder count, but would need
  caching if folders ran into thousands.
- **Deployed dashboard bindings.** A dashboard deployed before this change
  keeps any existing binding to a flow its author cannot run until it is
  next deployed. Scope derivation reads the frozen layout, and re-checking at
  every press would make a dashboard's behavior depend on its author's later
  shares.
- **Slugs.** Flow and dashboard slugs are no longer unique. The migration
  `relaxSlugIndexes` drops the old unique indexes before AutoMigrate recreates
  them as plain indexes; no rows change. Prometheus labels a flow by slug, so
  two flows with one name share a label series.
