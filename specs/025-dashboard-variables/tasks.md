# Tasks: Dashboard variables

**Feature**: `specs/025-dashboard-variables`
**Input**: [spec.md](spec.md) and [plan.md](plan.md).

## Phase 1: Model and validation

- [x] T001 Add `DashboardFilter`, `FilterOption`, `display.filters`, the `filter` condition kind, and `RenderJob.Filters` in `pkg/models/`
- [x] T002 Validate filter definitions and filter conditions in `pkg/dashops/display.go`, and report undefined references and the combination cap in `pkg/dashops/validate.go`

## Phase 2: User story 2, templates and grants

- [x] T003 [US2] Add substitution, reference collection, and placement expansion in `pkg/dashscope/filters.go`, and derive scope from the served filters in `pkg/dashscope/derive.go`
- [x] T004 [US2] Test substitution, expansion grants, and the cap in `pkg/dashscope/filters_test.go`

## Phase 3: User story 1, flows set filters

- [x] T005 [US1] Add the `filter` action and filter values to `pkg/dashlive`, with tests
- [x] T006 [US1] Add "Set filter" to the `dashboard-command` node in `pkg/nodes/core/dashboardcommand.go`

## Phase 4: User stories 2 to 4, viewer and editor

- [x] T007 [US2] Add `frontend/src/lib/filters.ts` and the filter condition in `displayState.ts`; extend the types in `stores/dashboards.ts`
- [x] T008 [US3] Apply values, the picker, and `var-` addresses in `frontend/src/routes/DashboardView.svelte`
- [x] T009 [US4] Add the filter condition kind to `PlacementDisplayPanel.svelte`
- [x] T010 [US2] Add the filter settings and the canvas preview picker to `DashboardEdit.svelte`

## Phase 5: User story 5, renders

- [x] T011 [US5] Carry filter values through `pkg/render`, REST job creation, and MCP `render_dashboard`, resolved by `dashops.ResolveFilterValues`

## Final verification and documentation

- [x] T012 Document filters in `docs/dashboard-authoring.md` and their grant in `docs/dashboard-security.md`; update the feature 023 plan
- [x] T013 Run `make dev`, `make test TARGET=server`, and `make test TARGET=frontend`; restart and verify `karakuri-dev.service`
- [x] T014 Check scope and commands through MCP, and the viewer, picker, links, editor, and conditions in the browser

## Phase 6: Variable model

These tasks follow the first implementation, whose names above are the ones it
used before the rename.

- [x] T015 Rename filters to variables across models, scope, live state, commands, node, renders, MCP, frontend, specs, and guides; move the feature to `specs/025-dashboard-variables`
- [x] T016 [US3] Add `description`, `display` modes, and `pin` addresses; draw bar and menu pickers in `DashboardView.svelte`
- [x] T017 [US7] Add constant variables, refused by commands and renders
- [x] T018 [US2] Substitute whole-string number and boolean references as typed values in Go and TypeScript
- [x] T019 [US6] Add `pkg/dashvars` query resolution with entity patterns, select options, regex, sort, and follow mappings, with tests
- [x] T020 [US6] Resolve variables in scope, commands, renders, validation, and the dependency report; grant followed entities; expire query scopes after 30 seconds
- [x] T021 [US6] Add `GET /dashboards/{id}/variables`, `POST /dashboards/{id}/variables/preview`, and MCP `resolve_dashboard_variables`
- [x] T022 [US6] Follow select entities in the viewer and refresh the resolution on reconnect
- [x] T023 Add `VariablesEditor.svelte` with grouped list, reorder, duplicate, usage, and query preview; move canvas previews into a toolbar popover
- [x] T024 Update the authoring, security, and MCP guides and specs 023 and 007
- [x] T025 Check resolution, commands, scope, renders, the viewer, and the editor against the development server

## Phase 7: Widgets

- [x] T026 [US8] Publish variables through the widget bridge from the viewer and editor; add `shared/variables.ts`, `shared/SourceSelect.svelte`, and one shared `EntityPicker.svelte`
- [x] T027 [US8] Use them in the Entity Control, Entities Card, History Graph, Camera, Media Player, To-do List, and Rotating Message panels; keep hours references in the history graph
- [x] T028 [US9] Add the `variable` binding source, `CapVariable`, `POST /dashboards/{id}/variables/{name}/value`, and the `variable-buttons` widget
- [x] T029 [US10] Substitute validated viewer values into news feed URLs
- [x] T030 [US10] Tag calendar sources and filter by `showTags`; move sources out of placement config and stop sending API keys to the panel
- [x] T031 Fix the editor reusing one settings panel across widgets, the calendar week-start list, the update response's draft flag, the render address, and the draft fallback of unreadable deployments, with a regression test for the last
- [x] T032 Check the panels, buttons, permissions, feed, and calendar against the development server

## Dependencies and execution order

Phase 1 precedes everything. Phases 2 and 3 depend only on it. The viewer needs
the live state from phase 3, and renders need phase 1 and the viewer's address
handling. Documentation follows the behavior it describes.
