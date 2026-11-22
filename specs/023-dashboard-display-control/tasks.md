# Tasks: Dashboard display control

**Feature**: `specs/023-dashboard-display-control`
**Input**: [spec.md](spec.md) and [plan.md](plan.md).

## Phase 1: Authored model

- [x] T001 Add `DashboardPage`, `DashboardDisplay`, `WidgetDisplay`, and placement `page_id`, `region`, `display` in `pkg/models/`, register the page table in `pkg/migrate/migrate.go`, and extend the snapshot, restore, and delete paths
- [x] T002 Add page operations and placement page/region/display writes to `pkg/dashops/`, with the `regions` layout mode
- [x] T003 Extend `pkg/dashops/validate.go` for regions, pages, conditions, animations, and display ranges
- [x] T004 Carry pages and display settings through `pkg/dashops/export.go` and the import handler
- [x] T005 Expose pages and the new fields through `pkg/api/dashboard_handler.go` and the MCP dashboard tools
- [x] T006 Test snapshot equality without new settings and the round trip with them; page removal and validation were checked live through MCP

## Phase 2: User story 1 and 2, commands and visibility

- [x] T007 [US1] Implement `pkg/dashlive` runtime state, command validation, locks with start seeding, alerts with expiry, content, and publishing
- [x] T008 [US1] Add `GET /api/dashboards/{id}/live`, `POST /api/dashboards/{id}/commands`, MCP `command_dashboard`, and the live topic gate in `cmd/karakuri/serve.go`
- [x] T009 [US2] Grant condition bindings and the live topic in `pkg/dashscope`
- [x] T010 [US1] Add the `dashboard-command` node and its field controls in `pkg/nodes/core/`
- [x] T011 [US1] Test dashlive and the scope grants

## Phase 3: User story 3, alerts through notifications

- [x] T012 [US3] Add the `dashboard` notification service and its delivery hook in `pkg/integrations/notification/`

## Phase 4: Viewer

- [x] T013 [US2] Add `frontend/src/lib/displayState.ts`, `dashboardLive.ts` (live state and condition sources), `presence.ts`, and `regionLayout.ts`
- [x] T014 [US1] Render visibility, pages, alerts, dimming, idle wake, pixel shift, animations, and the region layout in `frontend/src/routes/DashboardView.svelte`

## Phase 5: Editor

- [x] T015 [US4] Add the page bar to `frontend/src/routes/DashboardEdit.svelte`
- [x] T016 [US2] Add the placement Display section, conditions editor, and animations
- [x] T017 [US6] Add the region layout editing mode and the display settings dialog fields
- [x] T018 [US1] Add the `dashboard-picker` and `dashboard-widgets` controls to `frontend/src/lib/components/editor/ConfigForm.svelte`

## Phase 6: Widgets and theme

- [x] T019 [P] [US7] Build `plugins/core/widgets/rotating-message`
- [x] T020 [P] [US7] Build `plugins/core/widgets/flow-text`
- [x] T021 [P] [US7] Build `plugins/core/widgets/news-feed` and the feed route
- [x] T022 [P] Add `themes/mirror`

## Final verification and documentation

- [x] T023 Run `make dev` and `make test` for server and frontend
- [x] T024 Check the commands through MCP against a live viewer, the notification service, and the flow node
- [x] T025 Check the viewer and editor in the browser, with screenshots and the console
- [x] T026 Update `docs/dashboard-authoring.md`, `docs/dashboard-security.md`, `docs/notifications.md`, `docs/mcp.md`, `specs/spec-kit.md`, `specs/README.md`, and `ideas.md`
- [x] T027 Restart and verify `karakuri-dev.service` and http://localhost:8080

## Dependencies and execution order

Phase 1 precedes everything. Phase 2 needs the model; Phase 3 needs dashlive.
The viewer needs the live endpoints, and the editor needs the model API.
Phase 6 widgets depend only on the live endpoint and the feed route and can be
built beside the editor work.
