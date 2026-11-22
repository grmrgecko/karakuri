# Design plan: Backup and restore

**Feature**: `specs/036-backup-restore` | **Date**: 2026-11-05 | **Spec**: [spec.md](spec.md)

## Summary

A new `pkg/backup` package writes an installation to one archive: a tar stream
compressed with zstd and encrypted with age under a passphrase. The database
goes in as portable JSON lines per table, read through each model's GORM
schema in one read-only transaction, so the same archive restores into
SQLite, PostgreSQL, or MySQL and the backup never stops the server. The
encryption key and JWT secret, the agent server identity, widget uploads,
flow SQLite databases, flow files under the data directory, and the history
spool go beside it; a manifest written last carries counts and a checksum per
part.

Restore is a command-line action against a stopped installation. It stages and
checks the whole archive first, backs up what it is replacing, and swaps the
database, directories, and config secrets into place so a failure leaves the
installation as it was. A new instance lock held by `serve` is what tells the
restore a server is running.

Scheduled backups, retention, catch-up after downtime, a run history, a
settings page, REST and MCP routes, and two flow nodes (create a backup, react
to a result) sit on the same service. Plugin executables are listed rather
than copied, since they are built for one platform; FR-001 and the edge cases
were revised to say so and to add flow files.
[research.md](research.md) records the decisions,
[data-model.md](data-model.md) the archive layout, table, and settings,
[contracts/interfaces.md](contracts/interfaces.md) the CLI, routes, nodes, and
page, and [quickstart.md](quickstart.md) the checks.

## Technical context

- Language and dependencies: Go 1.26.5, Svelte 5. New dependency
  `filippo.io/age` v1.3.2 (pure Go). zstd from `klauspost/compress`, already
  required.
- Components:
  - `pkg/models`: `All()` returns the model list `AutoMigrate` used, now
    shared; `BackupRun` model.
  - `pkg/backup` (new):
    - `archive.go`: age, zstd, and tar writer and reader with the manifest
      and per-part SHA-256.
    - `dbdump.go`: the read-only snapshot, the column-keyed row encoding, and
      the dump of every table.
    - `dbload.go`: the decode through schemas, the topological load order,
      batched inserts that skip hooks, and the PostgreSQL sequence reset.
    - `files.go`: uploads, flow SQLite databases, flow files, the spool,
      and the agent identity, skipping temporary names.
    - `create.go`: one backup run with a temporary name, rename, and
      verification.
    - `verify.go`: verification.
    - `restore.go`: staging, the safety backup, the swap, and rollback.
    - `schedule.go`: the setting, cron, catch-up, and retention.
    - `service.go`: one at a time, run rows, and watchers for the trigger.
  - `pkg/instlock` (new): `Acquire(dataDir)` with `flock` on Unix and
    `LockFileEx` on Windows; `serve` holds it, restore takes it.
  - `cmd/karakuri/backup.go`: `backup create|verify|list|restore`. The config
    secrets write shares the staged write of `config rotate-key`, moved to
    `internal/config`.
  - `pkg/api/backup_handler.go`, `pkg/mcpserver/tools_backup.go`,
    `pkg/permissions` (`api:backups:read|write|download`).
  - `pkg/nodes/core/backup.go`: `backup-create` and `backup-result`.
  - `frontend/src/routes/settings/Backup.svelte` with its nav entry.
  - `cmd/karakuri/serve.go`: the instance lock, the scheduler, and marking an
    interrupted run as failed.
  - Docs: `docs/backup.md` (new, served as a guide), `docs/flow-authoring.md`,
    `docs/mcp.md`, `docs/agent-nodes.md` (restoring keeps enrolled agents).
- Storage: new table `backup_runs`; settings key `backup`; new file
  `<data>/karakuri.lock`; default archive directory `<data>/backups`. No
  existing table changes.
- Limits: one backup at a time, the next one queued; inserts in batches of 500
  rows; archive name pattern fixed so routes cannot reach other files;
  retention 1 to 365; run rows kept 400 days.

## Constitution check

- **Failures**: a backup failure removes its temporary file, records the
  reason on the run, logs at error, and fires `backup-result`; earlier
  archives stay, and retention runs only after a verified success. The
  `backup-create` node fails retryably on I/O errors and without retry on
  missing configuration. A restore that fails at any step rolls back what it
  had swapped and exits non-zero with the step named.
- **Drafts and deployment**: the schedule is configuration, not a draft.
  Nodes read the stored passphrase at run time.
- **Permissions**: three new permissions held only by admins by default.
  Download is its own permission because a downloaded archive and its
  passphrase hand over every credential and the agent CA. The routes take
  archive names, never paths. Restore has no remote interface at all.
- **Existing state**: the feature exists to protect it. Restore never deletes:
  the replaced database is kept as `karakuri.db.pre-restore-<stamp>` (or as
  the `--replace` safety archive for PostgreSQL and MySQL), replaced
  directories and the config file are renamed aside, and the replaced
  encryption key is in that saved config. The new instance lock is a new file;
  it changes no existing database, identity, or key. The live check in
  scenario 5 copies `build/var` and `build/etc` aside by hand before
  restoring over the dev installation.
- **Verification**: `make dev`, `make test`, table and round-trip tests on real
  SQLite databases, a PostgreSQL restore in a disposable container, the
  quickstart against the dev server, and a browser check of the settings page.

## Design and contracts

### Creating a backup

```text
service.Create(trigger, passphrase, dir)       one at a time; queued after a running one
  run row (running)
  tmp file <dir>/.karakuri-<name>.partial
  age.Encrypt(scrypt recipient) -> zstd -> tar
    db/<table>.jsonl   read-only tx, models.All() order, rows via schema fields, Unscoped
    secrets.json
    agents/*           if present
    uploads/**         walk, skip files removed mid-walk
    sql/*.db           complete files (saved by rename); skip .query-*
    files/<root>/**    roots under the data dir; others to manifest.skipped
    history-spool/**   complete segments (saved by rename); skip *.tmp
    manifest.json      counts, parts {name,size,sha256}, plugins, skipped
  close, fsync, rename to final name
  verify the final file (decrypt, checksums)       run.verified
  retention (schedule only, after success)
  run row (succeeded|failed), notify watchers
```

Rows stream: each table is read through `Rows()` and written as it is
scanned, so memory stays flat for a large `run_events` or `energy_hours`.

### Restoring

```text
instlock.Acquire(data)               refuse if held
open + verify archive -> stage to <data>.restore-<stamp>/
check manifest: format, version, engine of target
target has users?  -> refuse unless --replace
--replace          -> create safety backup beside the archive (same passphrase)
database:
  sqlite   load into karakuri.db.restoring, rename over, keep .pre-restore-<stamp>
  postgres AutoMigrate + load in one transaction, setval sequences
  mysql    AutoMigrate + load; on failure drop created tables (and reload safety backup under --replace)
directories (agents, uploads, sql, files roots, history-spool):
  rename current aside, rename staged in; on failure rename back
config: write secrets to a temp config, rename over, keep .pre-restore-<stamp>
report
```

The target's engine comes from its own config; the archive's engine is only
reported. A `dev` archive or binary skips the version order check with a
warning.

### Schedule

The scheduler computes the next time from `cron` in the server's time zone
with `pkg/cronspec`, sleeps on the engine clock, and runs. On start, if the
latest scheduled time at or before now is after `last_scheduled_at`, it runs
once two minutes after start. Saving the schedule creates the directory when
missing and writes and removes a probe file, refusing with the OS error when
it cannot.

### Nodes

`backup-create` calls `service.Create(trigger flow)` and waits; the run row
records the flow. `backup-result` registers a watcher on the service and emits
on the selected outcomes; it does not replay past runs.

## State and data model

See [data-model.md](data-model.md).

## Verification

| Scenario | Test |
| --- | --- |
| US1 | Round trip on a real SQLite database with every model populated: dump, verify, load into a fresh database, compare rows field by field. A changed byte, a truncated file, and a wrong passphrase all refuse. Concurrent writes during a dump land after the snapshot. Live: quickstart 1. |
| US2 | Restore into an empty data directory, refusal on a populated one, `--replace` with the safety archive, refusal under the instance lock, rollback when a step fails (injected), version refusal. PostgreSQL in a disposable container for SC-003. Live: quickstart 2, 3, 5. |
| US3 | Retention ordering, catch-up decision, schedule validation, interrupted run cleanup. Live: quickstart 4. |
| US4 | Route permission tests including download, name validation against path traversal. Browser: quickstart 6. |

## Risks and open decisions

- A database dumped as rows is slower than a file copy for SQLite; the dev
  database restores in seconds, and an installation with years of run history
  will take minutes. Run history pruning already bounds `runs` and
  `run_events`.
- The main database and the flow SQLite databases are each consistent, but
  not at the same instant. A flow that writes both within one run may restore
  with one write and not the other. The spec's edge cases state it.
- A PostgreSQL or MySQL server shared with another machine's karakuri is not
  caught by the local instance lock; the populated-database refusal is the
  guard.
- MySQL's implicit DDL commits make its rollback a drop and reload rather than
  a transaction abort; tests cover the drop, and the safety archive covers
  the reload.
