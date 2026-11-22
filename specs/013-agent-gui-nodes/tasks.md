# Tasks: Agent GUI nodes

**Feature**: `specs/013-agent-gui-nodes`

## Preparation and shared operations

- [x] T001 Record requirements, design, contracts, and validation in specs/013-agent-gui-nodes.
- [x] T002 [US1] Move native desktop operations into automation/desktop*.go and wire helper/resident transport.
- [x] T003 [US1] Share snapshots, window selection, state polling, and bounded evidence in automation/ui*.go and cmd/karakuri-auto.

## Agent nodes

- [x] T004 [US1] Add snapshots, control clicks, fresh observations, metadata, and screenshot targeting in agent/gui.
- [x] T005 [US2] Add window/state waits with bounded failure evidence in agent/gui.
- [x] T006 [US3] Add menu, dialog, and display nodes in agent/gui.
- [x] T007 [US4] Add Automation Script through existing command execution in agent/exec.

## Verification and delivery

- [x] T008 Add meaningful serialization/transport regression coverage; run agent/automation tests and platform builds via Makefile.
- [x] T009 Exercise live desktop operations, MCP catalog/execution, and editor config/ports; record evidence in quickstart.md.
- [x] T010 Update docs/agent-nodes.md and relevant root/module specifications and plans.
- [x] T011 Run required build/test targets, restart and verify service/HTTP, review and commit coherent changes.

## Dependencies and execution order

T002 precedes shared consumers. T003 precedes T004-T006. T007 uses independent existing process execution. Verification follows implementation; documentation records final behavior and observed limits. No branch or external publication is required.

## Configuration improvements

- [x] T012 Record requirements and design in specs/013-agent-gui-nodes/spec.md and plan.md.
- [x] T013 [US1] Extend shared selectors and node targeting in automation/ui_target.go and agent/gui/target.go.
- [x] T014 [US2] Add single checks, disabled state, fractional waits, and unmatched results in automation/ui.go and agent/gui/inspection.go.
- [x] T015 [US1] Add Keyboard focus checks, screenshot coordinates, snapshot sources/filters, and control constraints in agent/gui and automation/desktop*.go.
- [x] T016 [US3] Add scoped dialog options to automation/desktop.go, automation/desktop_helper.go, and agent/gui/inspection.go.
- [x] T017 [US4] Add literal inline command files and cleanup in agent/exec/automation.go.
- [x] T018 Verify serialization/helper/process contracts and fractional number inputs in frontend/src/lib/components/editor/ConfigForm.svelte; and run required Makefile builds/tests in automation/desktop_test.go and agent/exec.
- [x] T019 Verify live host behavior and editor fields, recording evidence in specs/013-agent-gui-nodes/quickstart.md.
- [x] T020 Update docs/agent-nodes.md and affected module specifications; restart and verify the development service, then commit.

T013 precedes consumers. T014-T017 follow shared contracts. T018-T020 follow
implementation. Inline command files can be developed independently of GUI work.

- [x] T021 Deploy the committed binaries to all seven active agents and refresh canonical CLI installations; record final checks in specs/013-agent-gui-nodes/quickstart.md.
