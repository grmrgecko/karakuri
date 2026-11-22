# Tasks: Graceful process termination

**Feature**: `specs/021-graceful-process-termination`
**Input**: spec.md, plan.md, research.md, data-model.md, contracts/nodes.md, quickstart.md.

## Phase 1: Required preparation

- [x] T001 Read docs/agent-nodes.md and existing specs/009-remote-agents and specs/020-agent-action-options contracts; resolve OS shutdown behavior in research.md.

## Phase 2: User story 1

**Goal**: Opt-in cooperative cleanup with preserved failure outcomes and defaults.
**Independent check**: Real command/script TERM handlers write final markers; normal exits remain immediate; invalid configs cause no file changes.

- [x] T002 [US1] Add real-process cancellation, timeout, normal-exit, and config-boundary fixtures in agent/exec/termination_test.go and agent/exec/termination_windows_test.go.
- [x] T003 [US1] Add termination_grace metadata/validation in agent/exec/policy.go and shared duration parsing in agent/agentnode/termination.go. The duration must be between zero and one minute inclusive; Windows accepts only zero.
- [x] T004 [US1] Connect command/script cancellation to cooperative process-group termination in agent/exec/process_tree_unix.go, agent/exec/process_tree_windows.go, agent/exec/command.go, and agent/exec/script.go.

## Phase 3: User story 2

**Goal**: Escalate remaining group members and retain bounded execution deadlines.
**Independent check**: Ignoring descendants stop even when their leader exits first, with all stream modes; wrapper budgets include grace.

- [x] T005 [US2] Add leader-exit/descendant escalation fixtures in agent/exec/termination_test.go and serialized deadline cases in agent/agentnode/budget_test.go.
- [x] T006 [US2] Add bounded group polling/escalation in agent/exec/process_tree_unix.go and termination allowance in agent/agentnode/budget.go.

## Final verification and documentation

- [x] T007 Run make dev/test for agent/server and supported agent cross-builds; verify live MCP command/script behavior and metadata per quickstart.md, plus settings UI where available (snapshot/visual-check limitation recorded).
- [x] T008 Update docs/agent-nodes.md, specs/spec-kit.md, specs/README.md, and specs/009-remote-agents contracts; record final evidence in quickstart.md.
- [x] T009 Restart and verify karakuri-dev.service and localhost:8080, then commit the coherent verified change preserving unrelated work.

## Dependencies and execution order

T001 precedes US1; US2 depends on shared validation and cancellation from US1. Tests precede their corresponding implementation. Deadline fixtures and OS fixtures may be developed independently after the contract is set. Verify the whole change before committing it.
