# Tasks: Home and overview

**Feature**: `specs/045-home-overview` | **Plan**: [plan.md](plan.md)

## Server

- [x] T001 Add `NextFirer` to `pkg/nodeapi` and alias it in `pkg/engine`.
- [x] T002 Add `Engine.NextFirings` and `Engine.NodeLabel` in `pkg/engine/upcoming.go`, reading triggers the way the runner arms them.
- [x] T003 Implement `ScheduleNode.NextFire` for cron and sun modes in `pkg/nodes/core/schedule.go`.
- [x] T004 Add `Service.Overdue` in `pkg/backup/ops.go` and `models.LastSucceededBackupRun`.
- [x] T005 Add `agentops.Running` and `agentops.Connected`.
- [x] T006 Add `pkg/homeops`: `Attention`, `Upcoming`, `Unfinished`.
- [x] T007 Add `GET /api/home/{attention,upcoming,unfinished}` in `pkg/api/home_handler.go`.
- [x] T008 Add MCP tools `get_attention`, `list_upcoming`, `list_unfinished_work`.
- [x] T009 Report `flow_name` on active run snapshots for running now.
- [x] T010 Test flow failure grouping and recovery in `pkg/homeops/homeops_test.go`.
- [x] T011 Test the backup overdue grace in `pkg/backup/schedule_test.go`.

## Frontend

- [x] T012 Serve the flow list at `/flows` and point the nav, editor, and share back links there.
- [x] T013 Add `lib/homeStart.ts` with `resolveStart` and its vitest table.
- [x] T014 Add `routes/Home.svelte`: start page resolution and the overview sections.
- [x] T015 Add the start page select to `settings/Profile.svelte`.
- [x] T016 Add the pin toggle to `DashboardList.svelte`.
- [x] T017 Read `level` from the Activity query string.

## Documentation and verification

- [x] T018 Write `docs/home.md`; list the tools in `docs/mcp.md`; index the feature.
- [x] T019 `make dev`, `make test TARGET=server`, `make test TARGET=frontend`.
- [x] T020 Live checks per [quickstart.md](quickstart.md) through REST, MCP, and the browser.
