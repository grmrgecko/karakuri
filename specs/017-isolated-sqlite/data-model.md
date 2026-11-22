# Data model: Isolated SQLite queries

- Flow database identity: engine flow UUID; no author-supplied ID or filename.
- Global database identity: fixed `global` key, separate from all flow keys.
- Snapshot: `sql/flow-<uuid>.db` or `sql/global.db` under the installation data directory, regular file, maximum 16 MiB, created with mode 0600 in a 0700 directory.
- Query: scope (`flow` by default or `global`), SQL (1–65536 bytes), bindings from an expression (array/object of scalar or null values), timeout (integer 1–60 seconds, default 5), max_rows (integer 1–10000, default 1000).
- Result: `columns` (ordered strings), `rows` (objects with unique column names), `changes` and `last_insert_id` (integers). Binary cells are base64 strings; NULL is null.
- Runtime: cancellable per-database lock, process-wide four-slot semaphore, disposable connection and memory VFS files. Nothing resumes after restart.

A write becomes persistent only after bounded result collection, successful statement completion, connection closure, file synchronization and atomic replacement. Old snapshots remain authoritative on earlier failures. Snapshot files are retained when a flow is deleted.
