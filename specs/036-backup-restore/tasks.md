# Tasks: Backup and restore

**Feature**: `specs/036-backup-restore`
**Input**: [spec.md](spec.md), [plan.md](plan.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/interfaces.md](contracts/interfaces.md),
[quickstart.md](quickstart.md).

The dev database, agent identities, and encryption key are irreplaceable.
Tests use temporary data directories. The one live restore over the dev
installation (quickstart 5) runs only after `build/var` and `build/etc` are
copied aside by hand with the server stopped.

## Phase 1: Shared preparation

- [X] T001 Add `filippo.io/age` v1.3.2 to `go.mod`; move the model list out of `migrate.AutoMigrate` into `models.All()` in `pkg/models/registry.go` and have `AutoMigrate` use it; add `models.BackupRun` in `pkg/models/backup_run.go` exactly as [data-model.md](data-model.md) lists it (`trigger` one of `manual`, `schedule`, `flow`, `cli`; `status` one of `running`, `succeeded`, `failed`; `verified` one of `pending`, `ok`, `failed`; `error` at most 2,000 characters; `counts` JSON), with create, update, list newest first, and delete-older-than queries, registered in `models.All()`
- [X] T002 [P] `pkg/instlock`: `Acquire(dataDir) (release func(), err error)` on `<data>/karakuri.lock` with `syscall.Flock(LOCK_EX|LOCK_NB)` on Unix and `windows.LockFileEx` with fail-immediately on Windows; a held lock answers an error saying the server is running; `serve` acquires it before opening the database and holds it until exit
- [X] T003 [P] Permissions `api:backups:read`, `api:backups:write`, `api:backups:download` in `pkg/permissions/constants.go` and the registry, not granted to the User role

## Phase 2: Foundation, archive and database copy

Every user story phase depends on this phase.

- [X] T004 `pkg/backup/archive.go`: a writer over `age.Encrypt` with an scrypt recipient (passphrase at least 12 characters), zstd, and tar, that hashes each entry with SHA-256 as it streams and appends `manifest.json` last; a reader that decrypts, decompresses, iterates entries, recomputes hashes, and checks them and the entry list against the manifest at the end; manifest fields per the data model, `format` 1, refusal of a larger format
- [X] T005 `pkg/backup/dbdump.go`: open a read-only transaction (`sql.TxOptions{ReadOnly: true}`, repeatable read on PostgreSQL and MySQL), and for each model in `models.All()` parse its GORM schema, select `Unscoped` rows through `Rows()`, scan into the struct, and write one JSON object per row keyed by each field's `DBName` (skipping association fields), counting rows; summary counts of live flows, dashboards, connections, MQTT devices, controls, users, and agents
- [X] T006 `pkg/backup/dbload.go`: decode each column with `reflect.New(field.FieldType)` and `json.Unmarshal` then `field.Set`, ignoring unknown columns; sort tables topologically by belongs-to relationships from the parsed schemas; insert in batches of 500 with `Session{SkipHooks: true}`; on PostgreSQL move each integer primary key's sequence past the largest ID with `setval(pg_get_serial_sequence(...))`
- [X] T007 Tests in `pkg/backup/db_test.go` on real SQLite databases: populate every model (including a soft-deleted row, a byte column, a time with nanoseconds, a nullable foreign key, a user's password hash), dump, load into a fresh database, and compare field by field; a write committed during the dump does not appear in it; an unknown column from an older archive is ignored and a missing one takes its zero value

## Phase 3: User story 1, back up while running (P1)

**Goal**: one verified archive of a running installation.
**Independent check**: quickstart scenario 1.

- [X] T008 [US1] Confirm in tests that `sqlstore` saves and `tswriter` segment writes only ever rename complete files into place, so the backup can copy them without their locks; a test saves a flow database repeatedly while a copy runs and every copied file opens as a valid SQLite database
- [X] T009 [US1] `pkg/backup/files.go`: walk `<data>/uploads`, `<data>/sql`, `<data>/history-spool`, each `storage.roots` entry under the data directory (`files/<path relative to data>`), and `<data>/agents/server*.pem`, skipping temporary names (`.query-*`, `*.tmp`), files removed during the walk, and anything that is not a regular file or directory, reading each file to the end and closing it before the next; list roots outside the data directory, renders, traces, logs, and the wasm cache in `skipped`; list loaded external plugins by name and version from `pluginhost`
- [X] T010 [US1] `pkg/backup/create.go` and `service.go`: `Service.Create(ctx, Request{Trigger, UserID, FlowID, Passphrase, Directory})` running one backup at a time with later requests queued; write `<dir>/.karakuri-<installation>-<stamp>.partial`, fsync, rename to `karakuri-<installation>-<YYYYMMDDTHHMMSSZ>.tar.zst.age` (installation is the host name lowercased with anything outside `[a-z0-9-]` replaced by `-`), then verify the final file and record `verified`; on any failure remove the partial file and record the reason; record the run row throughout; notify watchers with the finished run; log lifecycle at info and failures at error
- [X] T011 [US1] `pkg/backup/verify.go`: `Verify(path, passphrase) (Manifest, error)` naming the first part that fails or `wrong passphrase or damaged archive` when age refuses
- [X] T012 [US1] `cmd/karakuri/backup.go`: `backup create [--output DIR] [--passphrase-file F]`, `backup verify ARCHIVE`, and `backup list [--dir DIR]` per the contract, with the passphrase from `--passphrase-file`, `KARAKURI_BACKUP_PASSPHRASE`, or a prompt asked twice, never an argument; `create` works with or without a running server, since the database copy is a read transaction and every other part is saved by rename
- [X] T013 [US1] Tests in `pkg/backup/archive_test.go` and `create_test.go`: a full create and verify of a populated temporary installation; one changed byte, a truncated file, and a wrong passphrase refuse (SC-005); a failure mid-write leaves no partial file and keeps earlier archives; a second request queues behind the first; the manifest's counts, parts, plugins, and skipped entries

## Phase 4: User story 2, restore (P1)

**Goal**: an archive restores onto an empty or replaced installation, or changes nothing.
**Independent check**: quickstart scenarios 2, 3, and 5.

- [X] T014 [US2] Generalize `stageConfigKey` in `cmd/karakuri/config_cmd.go` into `stageConfigValues`, a staged write of named settings that resolves the dev config's symlink and keeps comments; `config rotate-key` keeps its behavior, and restore's commit keeps the replaced file as `<config>.pre-restore-<stamp>`. It stays in `cmd/karakuri` beside the owner-preserving helpers it needs; `pkg/backup` takes it as a callback
- [X] T015 [US2] `pkg/backup/restore.go`: acquire the instance lock or refuse; verify and unpack into `<data>.restore-<stamp>/`; refuse `format` above 1 and a release version newer than this binary naming both (a `dev` archive or binary warns instead); refuse a target whose database has users unless `Replace`, naming the user and flow counts; under `Replace`, write a safety backup beside the archive with the same passphrase
- [X] T016 [US2] Restore the database: SQLite into `<path>.restoring` with AutoMigrate and the load, renamed over the live file with the old one kept as `<path>.pre-restore-<stamp>` (with its `-wal` and `-shm` checkpointed or moved with it); PostgreSQL with AutoMigrate and the load in one transaction; MySQL with AutoMigrate and the load, dropping the tables it created on failure and, under `Replace`, reloading the safety backup
- [X] T017 [US2] Swap the directories (`agents`, `uploads`, `sql`, each restored files root, `history-spool`): rename each current one to `<name>.pre-restore-<stamp>` and the staged one in, renaming back on any failure; then commit the staged config secrets; remove the staging directory; return a report of the engine and counts, directories, config keys written and the saved config path, the safety archive path, and the listed plugins absent from this machine's plugin paths
- [X] T018 [US2] `backup restore ARCHIVE [--replace] [--passphrase-file F]` in `cmd/karakuri/backup.go`, printing the report and the refusals worded as in the contract
- [X] T019 [US2] Tests in `pkg/backup/restore_test.go` with temporary data directories and configs: restore into an empty installation and compare rows and files; refusal on a populated one; `--replace` writes the safety archive and keeps `.pre-restore` copies; refusal while the instance lock is held; an injected failure in the directory swap leaves database, directories, and config as they were; a newer-version archive refuses; restored secrets open an encrypted connection field and the agent CA roots setting

## Phase 5: User story 3, schedule (P2)

**Goal**: backups on a schedule with retention, catch-up, and flow nodes.
**Independent check**: quickstart scenario 4.

- [X] T020 [US3] `pkg/backup/schedule.go`: the `backup` setting (`enabled` default false, `cron` default `0 3 * * *` validated with `pkg/cronspec`, `directory` absolute with default `<data>/backups`, `retain` 1 to 365 default 14, `passphrase` encrypted with the encryption key through `pkg/crypto`, at least 12 characters, required when enabled, never returned, `last_scheduled_at`); saving creates the directory when missing and writes and removes a probe file, refusing with the OS error
- [X] T021 [US3] The scheduler in `schedule.go` on the engine clock: run at each cron time and record `last_scheduled_at`; on start run once two minutes in when the latest scheduled time at or before now is after `last_scheduled_at`; after a verified success remove the oldest archives matching this installation's name pattern beyond `retain`; on start mark runs left `running` as failed with `server stopped during the backup` and remove their partial files; delete run rows older than 400 days daily; start and stop in `cmd/karakuri/serve.go`
- [X] T022 [US3] `pkg/nodes/core/backup.go`: `backup-create` (optional `directory`; waits; output `{archive, path, size, duration_ms, counts, verified}`; retryable on I/O errors, not retryable without a stored passphrase) and `backup-result` trigger (`on` one of `failed` default, `succeeded`, `any`; payload `{status, trigger, archive, size, error, started_at, finished_at}`; no replay of past runs), folder System
- [X] T023 [US3] Tests: retention keeps 3 of 5 (SC-006) and never removes another installation's archives or after a failure; the catch-up decision for missed, not missed, and never-run schedules; schedule validation refusals; interrupted-run cleanup; the nodes on a fake service

## Phase 6: User story 4, settings, REST, and MCP (P2)

**Goal**: administrators manage backups without a shell.
**Independent check**: quickstart scenario 6.

- [X] T024 [US4] `pkg/api/backup_handler.go`: the routes in the contract with their permissions; `{name}` must match the archive name pattern and exist in the configured directory or the route answers 404; download streams with `Content-Disposition: attachment` and logs user and file at info; create answers 202 with the run
- [X] T025 [US4] `pkg/mcpserver/tools_backup.go`: `list_backups`, `create_backup`, `verify_backup`, `delete_backup`, `get_backup_schedule`, `save_backup_schedule`
- [X] T026 [US4] `frontend/src/routes/settings/Backup.svelte` and its nav entry for holders of `api:backups:read`: schedule form with the cron description and next time and the lost-passphrase warning, archive list with verify, download, and delete, back up now, the running backup, and recent runs
- [X] T027 [US4] Tests: each route refuses without its permission (download separately from read); names with `/`, `..`, or another pattern answer 404; MCP tools on a temporary directory

## Final verification and documentation

- [X] T028 Write `docs/backup.md` (what is and is not in an archive, the format and opening it with `age`, passphrases, CLI, schedule and retention, restore steps and refusals, moving between database engines, plugins to reinstall), serve it as a guide and MCP resource; update `docs/flow-authoring.md` (the two nodes), `docs/mcp.md`, and `docs/agent-nodes.md` (a restored server keeps enrolled agents); set the spec status to Implemented
- [X] T029 Run a full `make dev`, `make test`; restart `karakuri-dev.service`; confirm the unit is active, http://localhost:8080 answers, and the instance lock is held; browser check of the Backup page with settled screenshots and a console check
- [X] T030 Live quickstart scenarios 1 to 4 and 6 with a throwaway passphrase in the scratchpad, a disposable PostgreSQL container for scenario 3, and a second installation under the scratchpad for scenario 2
- [X] T031 Quickstart scenario 5 after copying `build/var` and `build/etc` aside with the server stopped: restore over the dev installation with `--replace`, start the unit, confirm counts, a connection test, and an enrolled agent reconnecting (SC-002); then remove the test schedule, archives, second installation, container, flows, and passphrase file, keep the hand copy until the user says otherwise, and report what was created and removed

Live checks on 2026-11-05: the CLI backed up the running dev server in 1.5
seconds (16 flows, 4 dashboards, 8 connections, 7 agents) and verified it; a
second installation restored it and `config rotate-key --dry-run` opened every
stored secret, the agent CA key, and the agent credential keys with the restored
key; a populated target was refused, `--replace` wrote a safety archive and kept
the replaced files aside, and a restore under the running dev server was refused
by the lock. The same SQLite archive restored into PostgreSQL 18 with every
table's count matching, after two fixes the check found: the memories
embedding column was declared `blob`, which PostgreSQL rejects, and an empty
string in a json column needed to load as NULL. A one-minute schedule ran five
verified backups and kept three; a read-only directory was refused on save; a
backup into a directory made read-only failed, kept the earlier archives, and
fired a Backup finished flow with `status: failed`; a server stopped across a
scheduled minute caught up one backup two minutes after it started. The
Backup settings page set the schedule, backed up, verified, downloaded (logged
with the user), and deleted. Restoring a fresh archive over the dev
installation with `--replace` kept the config symlink, and on start the arch,
freebsd, localhost, and localhost-root agents reconnected without enrolling and
the lab MQTT connection test passed. The hand copy of `build/var` and
`build/etc` is at `build/var-copy-20261106-pre036` and
`build/etc-copy-20261106-pre036`.

## Dependencies and execution order

- T001 precedes everything; T002 and T003 are independent of it and of each
  other.
- Phase 2 precedes the stories. T004 and T005 are independent; T006 needs
  T005's encoding; T007 needs both.
- US1 precedes US2 (restore reads archives), US3 (the schedule calls
  create), and US4 (routes call the service). US3 and US4 can proceed in
  parallel after US1; US2 can proceed beside them.
- T014 precedes T017.
- Commit each verified phase on its own.

## Parallel opportunities

- T002 and T003 beside T001; T004 beside T005; T008 beside T004 to T007; T022
  beside T024 and T025; T026 beside T027.

## Implementation strategy

A verified archive from the command line (US1) is the first deliverable: it
already protects the installation, since an operator can open it with `age`.
Restore (US2) makes it usable without hand work, then the schedule (US3) and
the settings page (US4) make it routine.
