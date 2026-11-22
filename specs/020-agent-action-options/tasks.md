# Tasks: Agent action options and HTTP requests

**Feature**: `specs/020-agent-action-options`
**Input**: spec.md, plan.md, research.md, data-model.md and contracts/nodes.md.

## Phase 1: Preparation

- [x] T001 Review behavior and design in docs/agent-nodes.md, specs/009-remote-agents/, specs/014-desktop-interaction-vision/ and automation/specs/.

## Phase 2: User story 1

**Goal**: Expected nonzero exits, file-only/discard streams and preserved input results.
**Independent check**: Real child processes and output files at the 32 MiB boundary.

- [x] T002 [US1] Add shared process policies and result assignment in agent/exec/ and agent/agentnode/, accepting a nonempty nonnegative exit list and return/file/discard modes.
- [x] T003 [US1] Verify real process execution and large file-only streams in agent/exec/policy_test.go and capture_test.go.

## Phase 3: User story 2

**Goal**: Health assertions and execution-triggered HTTP from agent networks.
**Independent check**: Real local HTTP/TLS peers and catalog derived types.

- [x] T004 [US2] Implement bounded HTTP helper, request node and probe health settings in agent/system/http.go, network.go and nodes.go.
- [x] T005 [US2] Verify request/response, redirects, limits and TLS through agent/system/http_test.go.

## Phase 4: User story 3

**Goal**: Confirm explicit service state without replaying control.
**Independent check**: Executable service-manager fixtures and disposable user service.

- [x] T006 [US3] Add confirmation and platform observations in agent/system/service*.go.
- [x] T007 [US3] Verify parsed external service-manager states and confirmation in agent/system/service_test.go.

## Phase 5: User story 4

**Goal**: Exclusive staged creation and guarded replacement.
**Independent check**: Real temporary files, digest checks and concurrent creators.

- [x] T008 [US4] Add create-only, permission setting and SHA-256 guard in agent/storage/atomic.go and readwrite.go.
- [x] T009 [US4] Verify prior-content preservation and exclusive creation in agent/storage/readwrite_test.go.

## Phase 6: User story 5

**Goal**: Reliable optional capture mapping and mapped match bounds.
**Independent check**: Provider geometry, helper PNG round trip and live region capture.

- [x] T010 [US5] Carry provider-authored geometry through automation/autoapi/, capture providers and helper transport; expose it in agent/gui/vision.go.
- [x] T011 [US5] Verify mapping math and serialization in automation/vision_test.go and helper_test.go, plus live desktop checks.

## Final verification and documentation

- [x] T012 Update docs/agent-nodes.md, specs/spec-kit.md, specs/README.md and affected baseline/module specs and plans.
- [x] T013 Build/test applicable targets through Makefile; exercise running nodes through MCP and live derived output types in browser.
- [x] T014 Restart and verify karakuri-dev.service and localhost:8080, record evidence in specs/020-agent-action-options/quickstart.md, and commit verified changes.

## Dependencies and execution order

T001 precedes all implementation. US1-US4 can be designed independently; their shared
catalog and final verification run after implementation. US5 provider metadata precedes
agent outputs and transport tests. Work proceeds sequentially in this checkout. Within
US1 process and file-capture fixtures can run together; US2 local peers are independent;
US3 platform fixtures and US4 separate tempdirs are independent. Initial MVP is US1;
this delivery completes all selected stories.

## Verification limit

T013 verified live browser derivations through DOM interaction and inspection.
Preview snapshot calls failed repeatedly, including after viewport resize/reopen;
settled screenshots could not be captured. See quickstart.md for completed evidence.
