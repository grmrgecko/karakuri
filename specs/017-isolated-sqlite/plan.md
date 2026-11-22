# Design plan: Isolated SQLite queries

**Feature**: `specs/017-isolated-sqlite` | **Date**: 2026-10-06 | **Spec**: [spec.md](spec.md)

## Summary

Add a server SQL query node backed by private SQLite snapshots. SQLite executes
against a bounded memory-only VFS. Go reads and atomically replaces files under
an `os.Root`; SQLite receives no disk filesystem capability.

## Technical context

- Go 1.26.5, existing `github.com/ncruces/go-sqlite3` 0.35.3, no new dependencies.
- `pkg/sqlstore` owns scope selection, persistence, concurrency, VFS, and authorization.
- `pkg/nodes/core/sqlquery.go` supplies catalog metadata and message bindings.
- An optional server execution-context flow identity capability selects private storage without changing plugin transports.
- Snapshots live in the existing data directory's `sql` child directory. No application schema migration.
- Each database is capped at 16 MiB. Four queries may execute simultaneously. Each query has 64 MiB of SQLite wrapper memory, a 48 MiB SQLite heap limit, and bounded Go-backed VFS allocation.

## Constitution check

- Failure routing and cancellation remain normal engine behavior. Classified errors prohibit retries even with an override.
- Drafts and deployment remain separate; tests may mutate the same database as deployed runs, like other storage actions.
- Flow authors deliberately choosing global scope share data. No new path, connection, or public endpoint is introduced.
- Existing databases, encrypted credentials, CA material, and identities stay unchanged.
- Server Makefile build/tests, frontend checks, MCP functional execution and browser catalog inspection provide evidence.

## Design and contracts

Decode and validate scope, query, parameter expression, deadline, and row limit.
Evaluate parameters against the incoming message without interpolating SQL. Obtain
flow identity from the server context, acquire its cancellable scope lock, then a
global execution slot. Read a regular snapshot under `os.OpenRoot` with a size cap.

Register a private VFS with a random name for each request. That VFS accepts only its main
file and anonymous memory journals, never delegates to the OS, rejects negative
or oversized allocations, and returns OPEN_MEMORY. One connection exclusively
owns each registered file. Initialize memory journal/temp policy, defensive mode,
untrusted schema, SQLite input/recursion/binding limits, and interrupt context.

Prepare one statement with PREPARE_NO_VTAB under a default-deny authorizer. Permit
ordinary main-schema CRUD/DDL and a reviewed set of built-in scalar, aggregate,
window, JSON, and date functions. ALTER may inspect and update the empty temporary
schema catalog internally; authored temporary objects remain unavailable.
Deny attachment, PRAGMA, temporary schema,
virtual tables, explicit transactions, and unknown actions/functions. Keep the
authorizer installed through execution and reprepare. Prepare the trailing text
without stepping it; any second statement rejects the whole query.

Collect rows within count/JSON byte limits. Do not persist until the statement
fully completes and the connection closes. A failed or interrupted statement
only changes the disposable memory snapshot. Non-read-only success writes a
private temporary file, syncs it, then renames it over the scope file. A failure
after rename can have an uncertain persistence outcome and is never retried.

The driver panics on SQLite out-of-memory errors. Recover its known allocation
panic within the disposable request, release all resources, and report a normal
capacity failure. Unexpected driver panics also fail the request without saving.

## State and data model

See [data-model.md](data-model.md) and [node contract](contracts/node.md).
Flow deletion retains snapshots for operator cleanup. No background database
connections or asynchronous persistence exist. Scope-lock entries disappear when
no request uses them.

## Verification

Use real SQLite and temporary root directories for persistence, CRUD, scope,
concurrency, escape, cancellation, and capacity contracts. Exercise node bindings
through the engine/MCP, including error routing and authored retries. Run
`make dev TARGET=server`, `make test TARGET=server`, and frontend checks. Restart
and verify the service. Inspect the new node/configuration in the browser, take
settled screenshots, and check console diagnostics.

## Risks and open decisions

Full snapshots make this a small local store, not a high-throughput database.
Global scope shares all tables between flow authors. Database files are separate
backup material and survive flow deletion. Disk synchronization failures after
rename may report failure despite accepted data. No unresolved decisions remain.
