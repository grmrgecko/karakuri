# Tasks: Faceplates

**Feature**: `specs/042-faceplates`
**Input**: [spec.md](spec.md), [plan.md](plan.md)

## Phase 1: Node, ports, and validation

- [x] T001 Add the faceplate config types, `FaceplatePorts`, and lenient and strict validation in `pkg/flow/faceplate.go`, and wire them into `NodePorts` and `validate.go`.
- [x] T002 Add the Faceplate node in `pkg/nodes/core/faceplate.go`, emitting each control's value or default, and register it.
- [x] T003 Mirror the ports in `frontend/src/lib/utils/nodePorts.ts`.
- [x] T004 Add unit tests for ports, validation, and pulled values.

## Phase 2: Service and REST (US1, US2, US5)

- [x] T005 Add `has_faceplates` to flows, set at deploy, and include faceplate-level flows with faceplates in `flowops.ListFor`.
- [x] T006 Add list, apply, order, and readings in `pkg/flowops/faceplate.go`, with the two Apply paths and the event log entry.
- [x] T007 Add REST routes in `pkg/api/faceplate_handler.go` and API tests for both Apply levels and refusals.

## Phase 3: Authoring and the flow page (US1, US2)

- [x] T008 Add `FaceplateControlsField.svelte` and the `faceplate_controls` field kind in `ConfigForm.svelte`.
- [x] T009 Add the tab bar in `FlowEditor.svelte` and `FaceplatePanel.svelte` with built-in editors, readings, and Apply, plus faceplate-only mode.

## Phase 4: Scripts and templates (US3, US4)

- [x] T010 Add `faceplateSandbox.ts` for template iframes and the change-script runner, with bounded rounds and fallback.
- [x] T011 Ship bundled curve and color templates.

## Phase 5: Subflows and Kuri (US6, US7)

- [x] T012 Apply per-instance `faceplate_values` in `pkg/engine/flatten.go`, and list and apply subflow tabs.
- [x] T013 Add `get_flow_faceplates` and `apply_flow_faceplates` MCP tools.

## Final verification and documentation

- [x] T014 Document faceplates in `docs/flow-authoring.md` and `docs/mcp.md`, and update the spec status.
- [x] T015 Run `make dev` and `make test`, then do browser and MCP checks, and restart and verify the service.
