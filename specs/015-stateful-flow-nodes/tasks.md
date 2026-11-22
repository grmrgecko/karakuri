# Tasks: Stateful flow nodes

**Feature**: `specs/015-stateful-flow-nodes`
**Input**: spec.md, plan.md, research.md, data-model.md and contracts/nodes.md.

## Phase 1: Preparation

- [x] T001 Read baseline guides/specs, preserve unrelated work, and define scoped contracts in specs/015-stateful-flow-nodes/.

## Phase 2: Hysteresis (US1)

**Goal**: Emit independent threshold transitions with durable remembered state.
**Independent check**: Crossing, band, reset and JSON-normalized store fixtures.

- [x] T002 [US1] Implement and register Hysteresis with regression coverage in pkg/nodes/core/hysteresis.go, hysteresis_test.go and core.go.
- [x] T003 [US1] Document behavior in docs/flow-authoring.md, run server Makefile checks and commit the verified story.

## Phase 3: Condition wait (US2)

**Goal**: Wait on fresh values while preserving normal memoization.
**Independent check**: Changing chained source, hold, timeout and cancellation.

- [x] T004 [US2] Implement isolated refresh capability and contract regression in pkg/nodeapi/exec.go and pkg/engine/{execctx,pull,nodetest,pull_test}.go.
- [x] T005 [US2] Add Condition wait and wait regressions in pkg/nodes/core/conditionwait.go, conditionwait_test.go and core.go.
- [x] T006 [US2] Document fresh waits in docs/flow-authoring.md and specs/003-flow-execution/, run server checks and commit.

## Phase 4: Keyed timers (US3)

**Goal**: Independent keyed waits, reset and bounded debounce.
**Independent check**: Interleaved keys and reset/replacement races.

- [x] T007 [US3] Add keyed state/reset, maximum wait and dropped routing in pkg/nodes/core/{delay,trigger}.go with focused timing regressions.
- [x] T008 [US3] Update docs/flow-authoring.md, run server checks and commit timer changes.

## Phase 5: Dedupe (US4)

**Goal**: Expire admissions and reset history without losing legacy state.
**Independent check**: TTL boundary, restart, reset and duplicate branches.

- [x] T009 [US4] Implement expiry/reset/duplicate with durable regressions in pkg/nodes/core/{dedupe,dedupe_test}.go.
- [x] T010 [US4] Update docs/flow-authoring.md, run server checks and commit dedupe changes.

## Phase 6: Home Assistant (US5)

**Goal**: Broader targets, observed confirmation and held state.
**Independent check**: Real WebSocket peer verifies targets, one action, fresh observations and cancelled holds.

- [x] T011 [US5] Implement targeting, confirmation and held state in pkg/nodes/hanodes/ha.go and connection continuity support in pkg/integrations/homeassistant/.
- [x] T012 [US5] Cover protocol behavior in pkg/nodes/hanodes/ha_test.go using the existing HA peer.
- [x] T013 [US5] Update docs/flow-authoring.md and specs/004-connections-devices/, run server checks and commit.

## Phase 7: HTTP (US6)

**Goal**: Intentional retries and inspectable HTTP failures.
**Independent check**: Local HTTP peer request counts, delay/cap/jitter, error details and cancellation.

- [x] T014 [US6] Add classified failures and jitter through pkg/nodeapi/, pkg/engine/retry.go and pkg/flow/graph.go with retry configuration validation.
- [x] T015 [US6] Add HTTP policy, Retry-After and bounded safe error details in pkg/nodes/core/{httprequest,httppaginate}.go and protocol regressions.
- [x] T016 [US6] Update retry controls in frontend/src/lib/components/editor/ConfigPanel.svelte and frontend catalog types.
- [x] T017 [US6] Update docs/flow-authoring.md and specs/003-flow-execution/, run server/frontend checks and commit.

## Final verification and documentation

- [x] T018 Inspect new catalog and exercise safe standalone nodes through MCP; use unsaved browser drafts for fields/ports and console checks.
- [x] T019 Reconcile specs/015-stateful-flow-nodes/ and baseline specs/indexes with final behavior and verification evidence.
- [x] T020 Verify karakuri-dev.service and HTTP listener, review worktree and report commits and any test objects.

- [x] T021 Final review regression: route the latest pending debounce message after reset and preserve replacement ownership in pkg/nodes/core/delay.go and delay_test.go.

- [x] T022 Final review regression: round HTTP-date Retry-After up to milliseconds so conversion cannot shorten the server minimum in pkg/nodes/core/httpretry.go and httpretry_test.go.

## Dependencies and execution order

T001 precedes all stories. US1, US3, US4 and US5 are independently verifiable;
US2 includes its refresh prerequisite; US6 includes classified-error and editor
prerequisites. Shared registrations/docs are edited sequentially. Research was
parallel under the planning skill; implementation remains on the current branch.

## Event-driven condition checks (US2 extension)

- [x] T023 [US2] Extend requirements, research, plan, data model and contracts for change-trigger checks.
- [x] T024 [US2] Add signal-port execution semantics and interruptible parking in pkg/flow/, pkg/nodeapi/, pkg/engine/, pkg/flowops/node.go and frontend bypass eligibility.
- [x] T025 [US2] Add flow/node-scoped wait subscriptions and fresh signal observations in pkg/nodes/core/conditionwait.go and internal/nodeutil/condition.go; cover scheduling and state behavior with real controls.
- [x] T026 [US2] Update docs/flow-authoring.md and specs/003-flow-execution/, run server/frontend Makefile checks, verify MCP and browser behavior, remove test objects, restart/check the service and commit.

T023 precedes T024 and T025; T026 follows both implementation tasks. Existing
completed stories remain unchanged.
