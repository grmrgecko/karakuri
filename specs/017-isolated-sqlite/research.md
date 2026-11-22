# Research: Isolated SQLite queries

## Storage boundary

Decision: Use a private bounded memory VFS, then persist snapshots from Go under
`os.OpenRoot`. SQLite never opens a host file.

Rationale: ATTACH and VACUUM INTO must fail even if authorization changes later.
Per-query copies also discard partial writes, RETURNING overflow, and interrupts.

Alternatives considered: A disk VFS relies on secure sidecar and symlink handling.
The driver's serdes helpers make full backup copies and use a global handoff
channel. A small private VFS retains direct access to the snapshot and limits
allocation before growth.

## SQLite policy

Decision: Use PREPARE_NO_VTAB, defensive mode, trusted_schema=false, a default-deny
authorizer, reviewed built-ins, input/recursion limits, and context interrupts.

Rationale: SQLite's authorizer operates during prepare and reprepare, so it must
stay active through Step. Ordinary DDL needs internal sqlite_schema operations;
defensive mode blocks direct schema corruption without breaking that DDL.

Sources: [SQLite security](https://sqlite.org/security.html),
[authorizer API](https://sqlite.org/c3ref/set_authorizer.html), and the checked-in
module version's Conn, Stmt, VFS, SliceFile and configuration APIs.

## Capacity and failures

Decision: Cap databases at 16 MiB, wrapper memory at 64 MiB, SQLite heap at 48 MiB,
VFS backing allocation per request, and active queries at four. Keep row and
encoded-result limits separate. Disable retries for every query failure.

Rationale: File-size limits alone do not bound sorter memory or repeated requests.
The ncruces driver deliberately panics on NOMEM, so the request boundary must
convert allocation panics into errors and clean up without persisting.

Alternatives considered: Unbounded caches and connection pools retain resources
between runs. They are unnecessary for bounded household datasets.
