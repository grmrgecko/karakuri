# Tasks: Server utility nodes

**Feature**: `specs/016-server-utility-nodes`
**Input**: spec.md, plan.md, research.md, data-model.md and contracts/nodes.md.

## Phase 1: Required preparation

- [x] T001 Read feature guides and baseline contracts; resolve lifecycle and date ownership in specs/016-server-utility-nodes/research.md.

## Phase 2: User story 1

**Goal**: Route messages through local clock and solar windows.
**Independent check**: Overnight weekdays, DST repeated hours and missing sun events.

- [x] T002 [US1] Add date/clock/solar boundary tests in pkg/nodes/core/timewindow_test.go.
- [x] T003 [US1] Implement Time window and register it in pkg/nodes/core/timewindow.go and pkg/nodes/core/core.go.

## Phase 3: User story 2

**Goal**: Observe integration failures, recovery and disabled status.
**Independent check**: Real HA peer disconnect/reconnect and manager replacement.

- [x] T004 [US2] Add real lifecycle/queue contract checks in pkg/connmgr/manager_test.go and pkg/nodes/core/connectionstatus_test.go.
- [x] T005 [US2] Retain statuses, atomically watch bounded queues and reject retired callbacks in pkg/connmgr/manager.go.
- [x] T006 [US2] Implement Connection status trigger in pkg/nodes/core/connectionstatus.go and register in pkg/nodes/core/core.go.

## Phase 4: User story 3

**Goal**: Transform readings with accurate types and preserved messages.
**Independent check**: Reversed mappings, physical units, nested writes and overflow.

- [x] T007 [US3] Add numeric boundary/port checks in pkg/nodes/core/numerictransform_test.go.
- [x] T008 [US3] Implement Numeric transform in pkg/nodes/core/numerictransform.go and register in pkg/nodes/core/core.go.

## Final verification and documentation

- [x] T009 Update docs/flow-authoring.md, specs/spec-kit.md, specs/README.md and relevant baseline connection contracts.
- [x] T010 Run Makefile server build/tests/vet and record results in specs/016-server-utility-nodes/quickstart.md.
- [x] T011 Verify MCP execution and live editor fields/ports with settled screenshots and console checks; record evidence in specs/016-server-utility-nodes/quickstart.md.
- [x] T012 Restart and verify karakuri-dev.service and the HTTP listener, review the diff and commit the verified change.

## Dependencies and execution order

Preparation precedes stories. Stories are independent except shared registration
edits, which remain sequential. Numeric/date test and implementation files can
be developed independently; connection lifecycle tests require manager observation.
Complete every requested story before final verification. No branch or publication
is authorized.
