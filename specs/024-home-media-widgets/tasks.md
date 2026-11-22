# Tasks: Home media and list widgets

**Feature**: `specs/024-home-media-widgets`
**Input**: [spec.md](spec.md) and [plan.md](plan.md).

## Phase 1: Server and host

- [x] T001 Add `History`, `TodoItems`, and `Fetch` to `pkg/integrations/homeassistant/media.go`
- [x] T002 Add the history, to-do, camera snapshot and stream, and media art routes in `pkg/api/ha_media_handler.go`, registered in `pkg/api/connection_handler.go` and `pkg/api/router.go`
- [x] T003 Test the routes' dashboard scope through the real router with the fake Home Assistant in `pkg/api/ha_media_test.go`
- [x] T004 Add `raw` to the API client and `fetch` to the widget bridge
- [x] T005 [US5] Accept `speak` on alerts in `pkg/dashlive`, MCP, the Dashboard Command node, and the dashboard notification service; speak new alerts in `frontend/src/routes/DashboardView.svelte`

## Phase 2: Widgets

- [x] T006 [P] [US1] `plugins/core/widgets/camera`
- [x] T007 [P] [US2] `plugins/core/widgets/media-player`
- [x] T008 [P] [US3] `plugins/core/widgets/todo-list`
- [x] T009 [P] [US4] `plugins/core/widgets/history-graph`
- [x] T010 [P] [US6] `plugins/core/widgets/countdown` and `plugins/core/widgets/wifi-qr`
- [x] T011 [P] [US6] Table rendering in `plugins/core/widgets/flow-text`
- [x] T012 [P] [US6] Air quality in `plugins/weather` and its widget

## Final verification and documentation

- [x] T013 Run `make dev` and `make test`
- [x] T014 Check each widget live against the development Home Assistant in the browser, with screenshots and the console
- [x] T015 Update `docs/dashboard-authoring.md`, `docs/dashboard-security.md`, `docs/notifications.md`, `specs/spec-kit.md`, and `specs/README.md`
- [x] T016 Restart and verify `karakuri-dev.service` and http://localhost:8080

## Dependencies and execution order

Phase 1 fixes the contracts the widgets consume; the widget tasks are independent
of each other and were built beside the server work.
