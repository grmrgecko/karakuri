# Tasks: Flow sharing

**Feature**: `specs/043-flow-sharing`
**Input**: [spec.md](spec.md), [plan.md](plan.md), [data-model.md](data-model.md),
[contracts/authorization.md](contracts/authorization.md),
[contracts/rest.md](contracts/rest.md), [quickstart.md](quickstart.md)

## Phase 1: Required preparation

- [x] T001 Add `api:flows:manage` to the `api:flows` registration and a `PermFlowsManage` constant in `pkg/permissions/registry.go` and `pkg/permissions/constants.go`. Do not add it to the User role in `pkg/migrate/migrate.go`; the Admin role's `api:*` covers it.
- [x] T002 Add the `FlowAccess` model in `pkg/models/flow_access.go` with fields `id`, `flow_id` (uuid, indexed, not null), `user_id` (nullable uuid), `group_id` (nullable uint), `permission` ("`faceplate`, `view`, `edit`, or `admin`. Anything else is refused on create."), and `created_at`. Add level constants, a rank map, `ValidFlowAccess`, `FlowGrantRank(flow, userID, groupIDs)` (the owner ranks admin), and `ListAccessibleFlows(userID, groupIDs, page, pageSize, isSubflow)` filtering by owner, direct share, or group share. Register the model in `pkg/models/registry.go`.
- [x] T003 Delete a flow's share rows in the flow deletion transaction in `pkg/flowops/flowops.go`, a user's direct shares when the user is deleted, and a group's shares when the group is deleted, in the existing user and group delete paths in `pkg/models`.
- [x] T004 Add `flowops.Level(uid, f)`, `flowops.LoadFor(uid, id, level)`, and `CanView/CanEdit/CanManage/CanFaceplate` in `pkg/flowops/access.go`. Manage holders rank admin. Return not-found when the caller holds no level and forbidden when the level is too low.

## Phase 2: User story 1, keep my flows to myself (P1)

**Goal**: a flow is visible and changeable only by its owner, its shares, and manage holders.
**Independent check**: quickstart section 1.

- [x] T005 [US1] Switch every handler in `pkg/api/flow_handler.go` to `flowops.LoadFor` at the level listed in `contracts/authorization.md`, and make the list use `ListAccessibleFlows` unless the caller holds manage. Add `access_level` to flow responses.
- [x] T006 [P] [US1] Apply `LoadFor` in `pkg/api/subflow_handler.go`, `pkg/api/export_handler.go`, `pkg/api/run_handler.go`, `pkg/api/flow_wait_handler.go`, `pkg/api/node_chat_handler.go`, and the flow folder move in `pkg/api/flow_handler.go`. Scope `GET /runs/active` and `POST /runs/{id}/cancel` through the run's flow.
- [x] T007 [P] [US1] Apply the same levels in the MCP tools in `pkg/mcpserver/tools_flows.go`, `tools_diff.go`, `tools_nodes.go`, `tools_runs.go`, and `tools_state.go`, using the caller from `middleware.GetUser`.
- [x] T008 [US1] Add an API test in `pkg/api/flow_sharing_test.go`: user B's list, get, versions, run, and delete on user A's flow return not-found, and a manage holder lists and opens it.

## Phase 3: User story 2, share a flow for editing (P1)

**Goal**: shares at edit and admin work for users and groups.
**Independent check**: quickstart section 2.

- [x] T009 [US2] Add `GET/POST /api/flows/{id}/shares` and `DELETE /api/flows/{id}/shares/{shareId}` in `pkg/api/flow_share_handler.go` per `contracts/rest.md`, requiring admin. Refuse levels outside `faceplate`, `view`, `edit`, and `admin`, and record a `flow` event on add and remove. Register the routes beside the flow routes.
- [x] T010 [US2] Accept `owner_id` on `PUT /api/flows/{id}` from manage holders only, in `pkg/api/flow_handler.go` and `pkg/flowops/flowops.go`.
- [x] T011 [P] [US2] Add a flow share store in `frontend/src/lib/stores/flows.ts` and `frontend/src/routes/FlowShare.svelte`, modeled on `DashboardShare.svelte`, with levels labeled Faceplate only, View, Edit, and Admin. Open it from the flow list and the editor for admins.
- [x] T012 [US2] Extend `pkg/api/flow_sharing_test.go`:
  - an edit share saves and deploys, and is refused on shares and delete;
  - an admin share passes both;
  - a group share admits a member;
  - an unknown level is refused next to a valid one.

## Phase 4: User story 3, view and faceplate levels (P1)

**Goal**: view reads without changing; faceplate is refused every graph route and is not listed until feature 042 adds faceplate routes.
**Independent check**: quickstart section 3.

- [x] T013 [US3] Hide save, deploy, test, run, enable, and share controls in `frontend/src/routes/FlowEditor.svelte` and `FlowList.svelte` when `access_level` is below the level each needs.
- [x] T014 [US3] Extend `pkg/api/flow_sharing_test.go` so a view share reads the graph and is refused on save, deploy, test, and enable, and a faceplate share is refused on every graph route and absent from the list.

## Phase 5: Runs, live streams, events, and triggers (FR-011 to FR-013)

**Goal**: nothing outside the flow routes exposes an unshared flow.
**Independent check**: quickstart sections 3 and 5.

- [x] T015 Move the `flow:*`, `editor:flow:*`, and `run:*` topic gates in `cmd/karakuri/serve.go` to `RegisterTopicPermissionCallback`. Keep the global permission and add view on the flow, resolving a run's flow from the engine snapshot or the `runs` table.
- [x] T016 [P] Filter `flow` subjects in `pkg/api/event_handler.go` and `pkg/mcpserver/tools_events.go` through `events.Filter.SubjectAllowed` for callers without manage.
- [x] T017 [P] Require edit on the flow when `POST /widget_triggers/{flow_id}/fire` is admitted by `api:flows:trigger`, and filter `GET /widget_triggers` by level, in `pkg/api/widgettrigger_handler.go`.
- [x] T018 Refuse dashboard deploys whose flow-trigger bindings name a flow the deployer cannot edit, in `pkg/dashops` deploy, naming the flow in the error.
- [x] T019 Add tests for the topic callbacks, event filtering, and the dashboard deploy refusal, beside the existing tests in `pkg/api/event_handler_test.go` and `pkg/api/widgettrigger_handler_test.go`.

## Phase 6: User story 5, shared subflows and references (P3)

**Goal**: new references need view; existing ones keep saving and running.
**Independent check**: quickstart section 4.

- [x] T020 [US5] In `pkg/flowops/flowops.go` and `pkg/flowops/patch.go`, require view on each shared subflow a save adds compared with the current draft. Require view on `error_flow_id` targets in `Update`, on the source in `import_subflow`, and on every embedded subflow in export (`pkg/flowops/export.go`).
- [x] T021 [US5] Apply per-flow checks in `pkg/mcpserver/tools_references.go` (scan only accessible flows), `pkg/mcpserver/tools_dependencies.go` (`reference()` for kind flow), and the unpinned assistant tools in `pkg/mcpserver/assistant.go`.
- [x] T022 [US5] Add a test in `pkg/api/flow_sharing_test.go`: a new reference to an unshared subflow is refused, an existing one saves, and a deployed parent runs after access is removed.

## Final verification and documentation

- [x] T023 Document ownership, levels, shares, and the change for multi-user installs in `docs/flow-authoring.md` and the MCP tool notes in `docs/mcp.md`. Update `specs/043-flow-sharing/spec.md` status and `plan.md` to match the final behavior.
- [x] T024 Run `make dev`, `make test TARGET=server`, and `make test TARGET=frontend`. Then walk through quickstart sections 1 to 5 against the running service, with browser screenshots and a console check for the share dialog and the view-only editor. Remove the disposable accounts and flows.
- [x] T025 Restart `karakuri-dev.service`, confirm it is active and http://localhost:8080 answers, and commit each coherent step.

## Dependencies and execution order

- Phase 1 blocks everything.
- Phase 2 must precede Phases 3 and 4, which depend on `LoadFor` and the
  list.
- Phase 5 depends only on Phase 1 and can proceed alongside Phases 3 and 4.
- Phase 6 depends on Phase 2.
- Tasks marked [P] touch separate files once their phase's prerequisites are
  done.
- User story 4 (manage shares) is covered by T009, T011, and T012.
- Feature 042 depends on this feature's faceplate level, and adds the
  faceplate routes that list faceplate-only grants.

## Phase 7: Folder sharing and private names (FR-016 to FR-020)

- [x] T026 Add `FolderAccess` and `models.FolderRanks` in `pkg/models/folder_access.go`, and include folder ranks in `FlowGrantRank` and `ListAccessibleFlows` in `pkg/models/flow_access.go`.
- [x] T027 Authorize folder routes, flow create, and flow move in `pkg/api/folder_handler.go` and `pkg/api/flow_handler.go` through `flowops.AuthorizeFolder` and `CheckFolderTarget`; hide unseen folders with `ListFoldersFor` and `MaskFolder`.
- [x] T028 Add `/api/folders/{id}/shares` in `pkg/api/flow_share_handler.go`, filter folder entries in the event log, and add the folder share page and gated folder actions in `frontend/src/routes/FolderShare.svelte` and `FlowList.svelte`.
- [x] T029 Refuse a flow name only when a readable flow uses it (`flowops.NameTaken`), so a hidden flow cannot block a name.
- [x] T030 Add `TestFolderSharing` in `pkg/api/flow_sharing_test.go`.

## Phase 8: Slugs as per-caller addresses (FR-021)

- [x] T031 Drop the unique slug indexes in `pkg/migrate/migrate.go` (`relaxSlugIndexes`), and resolve dashboard slugs per caller with `models.LookupDashboardVisible`, `dashops.ResolveFor`, and `resolveDashboard`, answering `AMBIGUOUS` with the matches.
- [x] T032 Add `GET /api/flows/by_slug/{slug}` and `GET /api/dashboards/by_slug/{slug}` in `pkg/api/slug_handler.go`.
- [x] T033 Wrap flow and dashboard routes in `frontend/src/lib/components/ResolveRef.svelte` and link by ID throughout.
- [x] T034 Add `TestSlugsResolveAmongVisibleItems` in `pkg/api/flow_sharing_test.go`.
