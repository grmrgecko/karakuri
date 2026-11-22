# Tasks: Dashboard charts

**Feature**: `specs/028-dashboard-charts`
**Input**: [spec.md](spec.md), [plan.md](plan.md), [contracts/api.md](contracts/api.md), [quickstart.md](quickstart.md), and the 025 plan section on time range variables.

## Phase 1: Variables (feature 025 update)

- [x] T001 Model, validation, and `Resolve` for `time_range` and `interval`, and the time-series query sources in `pkg/models/dashboard_display.go`, `pkg/dashops`, and `pkg/dashvars` (chained resolution, union grants)
- [x] T002 Substitution formats and `SubstituteQuery` escaping in `pkg/dashscope/variables.go`, and time range exclusion from grant combinations, with tests
- [x] T003 Frontend: `frontend/src/lib/timeRange.ts`, the formats in `variables.ts`, `TimeRangePicker`, interval and auto resolution in `DashboardView.svelte`, the `VariablesEditor` fields, and the bridge variable types

## Phase 2: User stories 1 and 2, server data

- [x] T004 [US1] `pkg/dashchart`: config parsing, range precedence, query expansion, and the cache, with tests
- [x] T005 [US2] Chart data and preview routes in `pkg/api/dashboard_chart_handler.go`, with router tests for the refusals
- [x] T006 [US1] MCP `get_chart_data`

## Phase 3: User stories 1 and 3 to 5, the widget

- [x] T007 [US4] Data modules (frames, transformations, field config, units, reducers, mappings), with Vitest in `frontend/src/lib/chart/`
- [x] T008 [US4] Visualizations in `plugins/core/widgets/chart/src/viz/`
- [x] T009 [US3] Zoom, refresh, variable watching, readiness, render mode, and theme
- [x] T010 [US1] Settings panel with query editors, preview, inspector, and option groups
- [x] T011 Manifest entry in `plugins/core/manifest.yaml`; bridge `isRenderMode`, and the busy readiness in `DashboardView.svelte`

## Final verification and documentation

- [x] T012 Browser checks from [quickstart.md](quickstart.md) for every visualization, the picker, zoom, links, and the console
- [x] T013 Render check through MCP
- [x] T014 Update `docs/dashboard-authoring.md`, `docs/dashboard-security.md`, and `specs/spec-kit.md`
- [x] T015 Run `make dev` and `make test`, then restart and verify the service

## Dependencies and execution order

T001 and T002 precede T004. T004 precedes T005 and T006. T007 and T008 can
proceed alongside the server work against the contract. T010 needs T005's
preview route.
