# Validation: Isolated SQLite queries

Build and test with `make dev TARGET=server`, `make test TARGET=server`, and
`make test TARGET=frontend`. Restart `karakuri-dev.service` and verify it is
active and localhost:8080 answers.

Use MCP to create a disposable flow with Value, SQL query, and Debug nodes.
Execute one statement at a time:

1. `CREATE TABLE readings (id INTEGER PRIMARY KEY, value REAL, label TEXT)`.
2. `INSERT INTO readings(value, label) VALUES (?, ?)` with parameters `[payload.value, payload.label]`.
3. `SELECT id, value, label FROM readings`.
4. Update/delete a reading using bound parameters and verify affected rows.
5. Restart the service and read again. A second flow in private scope must not see the table; global scope must share a separately created table.
6. Attempt ATTACH, VACUUM INTO, PRAGMA, load_extension, file functions, multiple statements and a SELECT over the row cap. Verify errors, no outside files, and unchanged persisted rows.

Inspect SQL query in the browser palette, edit scope, parameters and limits, and
confirm the catalog-driven ports/configuration remain usable. Capture settled
screenshots after interactions and inspect console diagnostics. Report any
verification flows added to development state.

## Recorded checks, 2026-10-06

- `make dev TARGET=server` passed. The restarted user service is active and
  `http://localhost:8080` returns 200.
- `make test TARGET=server` passed, including real SQLite escape, symlink,
  persistence, binding, concurrent update, deadline and capacity fixtures. The
  first full run timed out in the existing MCP discovery connection check;
  the full rerun passed.
- `make test TARGET=frontend` passed with no Svelte diagnostics.
- MCP checked named and positional bindings, CREATE/INSERT/UPDATE/DELETE/SELECT,
  RETURNING, private isolation, global sharing, and reads after service restart.
  ATTACH, VACUUM INTO, file functions and multiple statements failed; prior rows
  survived. An unbounded recursive query stopped at its one-second deadline.
- A draft Value/SQL/Debug flow delivered the sandbox failure through the error
  output. The SQL node ran once. Draft validation refuses SQL retry policies;
  the engine fixture also suppresses retries when one is supplied directly.
- Browser checks covered scope selection and the Parameters input toggle. Its
  typed port appeared and disappeared live. Settled screenshots were captured.
  There were no new console errors; the console retained an earlier unrelated
  404 for another flow's latest run.

Two undeployed verification flows remain: `SQLite verification`
(`a6d851d4-f792-4d79-ab19-e13c0d29b55a`) and `SQLite isolation verification`
(`0f63c81c-6217-45d3-913f-fd8322284bfc`). Their final drafts contain only
`SELECT 1 AS value`. The first flow's database contains an empty `samples`
table. The global verification table was dropped. No outside escape file was
created, and existing application records, secrets and identities needed no
migration.
