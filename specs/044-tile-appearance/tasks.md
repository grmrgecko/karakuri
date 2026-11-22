# Tasks: Tile appearance

**Feature**: `specs/044-tile-appearance`
**Input**: [spec.md](spec.md), [plan.md](plan.md)

## Phase 1: Server model, validation, and grants (US3, US4)

- [x] T001 Add `TileStyle`, `StatusCue`, `AllConditions`, and the `IsZero` change in `pkg/models/dashboard_display.go`.
- [x] T002 Validate style and cues in `pkg/dashops/display.go`.
- [x] T003 Read cue conditions for grants, references, substitution, and dependency reports in `pkg/dashscope` and `pkg/mcpserver/tools_dependencies.go`.
- [x] T004 Add server tests for validation, zero display, cue grants, and cue variable references.

## Phase 2: Host style and cues (US3, US4)

- [x] T005 Factor `conditionListMet` in `displayState.ts`; add `tileStyle.ts` and `TileCue.svelte` with frontend tests.
- [x] T006 Apply style and cues in `DashboardView.svelte`, `RegionLayout` cells, and the editor canvas, watching cue sources.
- [x] T007 Edit style and cues in `PlacementDisplayPanel.svelte`.

## Phase 3: States and formats (US1, US5)

- [x] T008 Add `shared/appearance.ts` with `matchState` and `stateLabel`, and `shared/formats.ts` applied inside `formatValue`, with frontend tests.
- [x] T009 Apply states and formats in Entity Control and Entities Card; add `AppearanceEditor.svelte` to their panels.

## Phase 4: Button widget (US2)

- [x] T010 Add the `button` package, manifest entry with scope and schema, and its config panel.

## Final verification and documentation

- [x] T011 Update `docs/dashboard-authoring.md`, `specs/README.md`, and `specs/spec-kit.md`.
- [x] T012 `make dev`, `make test`, restart the service, and verify live through MCP and the browser.
