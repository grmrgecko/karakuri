# Tasks: Isolated SQLite queries

**Feature**: `specs/017-isolated-sqlite`
**Input**: spec.md, plan.md, research.md, data-model.md, contracts/node.md, quickstart.md.

## Phase 1: Required preparation

- [x] T001 Read guides/constitution and resolve SQLite storage and authorization APIs in specs/017-isolated-sqlite/research.md.

## Phase 2: Shared foundation

- [x] T002 Implement bounded memory-only VFS and root-confined snapshots in pkg/sqlstore/vfs.go and pkg/sqlstore/store.go; enforce 16 MiB databases and four active requests.

## Phase 3: User story 3

**Goal**: Contain SQL without host filesystem access.
**Independent check**: Escape/capacity fixtures preserve outside bytes and prior snapshots.

- [x] T003 [US3] Add real SQLite escape, rollback, deadline, and capacity fixtures in pkg/sqlstore/store_test.go.
- [x] T004 [US3] Implement default-deny authorization, one-statement preparation, limits and cancellation in pkg/sqlstore/query.go.

## Phase 4: User story 1

**Goal**: Persistent schema/CRUD within a flow.
**Independent check**: Reopen snapshots, exercise bound values and RETURNING, preserve message envelopes.

- [x] T005 [US1] Add real persistence/CRUD/binding fixtures in pkg/sqlstore/store_test.go and pkg/nodes/core/sqlquery_test.go.
- [x] T006 [US1] Add server flow identity capability in pkg/nodeapi/exec.go and pkg/engine/execctx.go; implement/register pkg/nodes/core/sqlquery.go with scope, expression bindings, timeout 1–60 (default 5), max_rows 1–10000 (default 1000), and nonretryable errors in pkg/nodes/core/core.go.

## Phase 5: User story 2

**Goal**: Deliberate shared storage and serialized updates.
**Independent check**: Different flow UUIDs remain private, global queries share data, concurrent updates lose no writes.

- [x] T007 [US2] Verify scope sharing/isolation and cancellation of queued queries in pkg/sqlstore/store_test.go.

## Final verification and documentation

- [x] T008 Document usage, limits, persistence and trust boundary in docs/flow-authoring.md; index feature 017 in specs/spec-kit.md and specs/README.md.
- [x] T009 Run Makefile server build/tests and frontend checks, exercise MCP and browser configuration, and record evidence in specs/017-isolated-sqlite/quickstart.md.
- [x] T010 Restart/verify karakuri-dev.service and HTTP listener; inspect/stage only feature files and commit the verified change.

## Dependencies and execution order

T001 precedes T002. Security tests and policy precede the node registration. CRUD
and node work depend on the store contract; sharing verification follows it.
Documentation can proceed alongside checks once behavior is fixed. Ship one
verified change because the store, security policy and node form one contract.
No independent implementation phase justifies parallel agents.
