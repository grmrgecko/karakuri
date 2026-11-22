# Feature specification: Isolated SQLite queries

**Feature**: `specs/017-isolated-sqlite`
**Created**: 2026-10-06
**Status**: Implemented
**Input**: SQL query node with persistent SQLite databases per flow or global, supporting reads, writes, and schema changes without escaping SQLite's sandbox.

## User scenarios and testing

### User story 1: Keep structured flow data (Priority: P1)

An author creates tables, records readings, updates or deletes rows, and selects results using multiple SQL query nodes in one flow.

**Why this priority**: Structured local storage is the requested capability.
**Independent test**: Run schema and CRUD statements through real SQLite, reopen storage, and read the written rows from another node in the same flow.

**Acceptance scenarios**:

1. Given flow scope, when two nodes execute in one flow, then they share a database that survives restart and redeployment.
2. Given different flows, when they use flow scope, then they cannot see each other's tables or rows.
3. Given a parameter containing SQL syntax, when it is bound, then it is stored literally rather than executed.
4. Given invalid SQL, a constraint violation, cancellation, or a result limit, when the query fails, then its changes do not persist and normal error routing receives the failure.

### User story 2: Share data deliberately (Priority: P2)

An author selects global scope to share one database between flows.

**Why this priority**: Sharing should be an explicit authoring choice.
**Independent test**: Write in one flow, read in another using global scope, and confirm their private databases remain separate.

**Acceptance scenarios**:

1. Given global scope, when different flows execute queries, then they share one persistent database.
2. Given concurrent updates to one database, when queries complete, then no accepted update is lost.
3. Given a waiting or running query, when its deadline expires, then it releases execution capacity and later queries can complete.

### User story 3: Contain authored SQL (Priority: P1)

An operator permits SQL while preserving host files, application records, secrets, and other flows' private databases.

**Why this priority**: The user's explicit constraint is that SQL cannot escape its database.
**Independent test**: Run escape attempts against real SQLite and verify outside sentinel files and other databases remain unchanged.

**Acceptance scenarios**:

1. Given SQL attempting ATTACH, DETACH, VACUUM INTO, extension loading, file functions, virtual tables, PRAGMA settings, or direct schema writes, when it executes, then it is refused without accessing host files.
2. Given two statements in one query, when execution starts, then neither statement changes persistent data.
3. Given database, result, or memory exhaustion, when execution fails, then the previous snapshot survives and subsequent ordinary queries work.
4. Given a symlink replacing a database snapshot or the SQL storage directory, when it is opened, then the substitution is refused.

### Edge cases

- Empty SQL, unknown scope, missing or extra parameters, duplicate column names, and unsupported parameter types produce useful errors.
- RETURNING rows obey SELECT's result limits; a limit failure does not save the write.
- Transactions cannot span nodes or runs. Temporary objects and virtual tables are unavailable.
- Flow deletion retains its database for operator cleanup; a new flow with the same name has a new database identity.
- Draft flow tests and deployed runs share data. Standalone node tests use their temporary flow identity; global tests still have real side effects.

## Requirements

### Functional requirements

- **FR-001**: Register an impure `sql-query` node with flow scope by default and explicit global scope.
- **FR-002**: Nodes in one flow MUST share persistent data; different flow identities MUST have separate databases; global scope MUST use one separate shared database.
- **FR-003**: Execute one SQLite statement supporting SELECT, INSERT, UPDATE, DELETE, RETURNING, and ordinary table/index/view/trigger schema changes.
- **FR-004**: Accept a parameter expression returning an array or named object, bind scalar/null values separately from SQL, and reject missing or extra bindings. SQL MUST NOT interpolate incoming data.
- **FR-005**: Emit an object containing columns, row objects, affected rows, and last insert ID while preserving the input envelope. Binary results MUST encode as base64; duplicate names MUST require aliases.
- **FR-006**: SQL MUST NOT reach host files, the application database, attached databases, extensions, virtual tables, arbitrary PRAGMA settings, temporary schemas, or user-managed transactions.
- **FR-007**: Confinement MUST use SQLite authorization and storage without host filesystem access from SQLite. SQL text filtering alone is insufficient.
- **FR-008**: Bound each query to 1–60 seconds (default 5), 1–10000 rows (default 1000), 1 MiB of encoded results, 64 KiB of SQL, 16 MiB per database, and bounded memory and concurrent execution.
- **FR-009**: Query failures before snapshot replacement MUST preserve the previous snapshot. Successful writes MUST atomically replace it before emitting success. A directory synchronization failure after replacement can leave an uncertain commit outcome. Queries to one database MUST serialize.
- **FR-010**: Failures MUST use normal error routing without automatic retries, including authored retry overrides, because persistence failures can have uncertain commit outcomes.
- **FR-011**: Catalog, editor, REST, and MCP MUST expose the same node contract without configurable paths or connection strings. Private database identity MUST come from the engine.
- **FR-012**: Existing application databases, encrypted secrets, identities, and node state MUST remain readable without migration or regeneration.

### Key entities

- Scoped database: persistent tables and rows owned by a flow identity or global scope.
- Query request: scope, single SQL statement, bindings, row cap, and deadline.
- Query result: columns, rows, changes, and last insert ID.
- Snapshot: bounded SQLite bytes stored separately from application records.

## Success criteria

- **SC-001**: Real SQLite checks establish CRUD, bound values, reopen persistence, private isolation, global sharing, and concurrent updates without lost writes.
- **SC-002**: Escape fixtures preserve outside sentinel bytes and other databases while ordinary queries remain usable.
- **SC-003**: Cancellation and capacity failures preserve existing rows and permit a later successful query.
- **SC-004**: Relevant Makefile builds/tests, MCP checks, and browser catalog/configuration checks pass; the development service stays active and answers HTTP.

## Assumptions and scope

- Global data is shared between flow authors, like global variables; it is not per-user storage.
- External connections, import/export interfaces, and cross-node transactions are outside this feature.
- Full snapshots suit small local stores and keep SQLite away from host files.
- Code already running as the server's OS account is outside the SQL sandbox boundary.

## References

- [Flow guide](../../docs/flow-authoring.md)
- [Execution](../003-flow-execution/spec.md)
- [Stateful nodes](../015-stateful-flow-nodes/spec.md)
- [Design](plan.md)
