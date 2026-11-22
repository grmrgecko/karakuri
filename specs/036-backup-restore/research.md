# Research: Backup and restore

## What an installation holds

| Part | Where | In the archive |
| --- | --- | --- |
| Main database | `database.path` (SQLite) or `database.dsn` (PostgreSQL, MySQL) | Yes, as portable rows |
| Encryption key, JWT secret | `security.encryption_key`, `security.jwt_secret` in the config file | Yes |
| Agent CA roots | `agent.ca_roots` setting, encrypted with the encryption key | Yes, with the database |
| Agent server identity | `<data>/agents/server.pem`, `server-key.pem` | Yes |
| Widget uploads | `<data>/uploads/widgets/<widget>/<upload>/<file>` | Yes |
| Flow SQLite databases | `<data>/sql/flow-<id>.db`, `global.db` | Yes |
| Flow files | `storage.roots` entries under the data directory, by default `<data>/files` | Yes; roots elsewhere are named only |
| Buffered history | `<data>/history-spool` | Yes |
| External plugins | `plugins.paths` or the resource search beside the binary | Listed by name and version only |
| Renders, assistant traces, wasm cache, logs | `<data>/renders`, traces, `<data>/wasm-cache`, log file | No |

The agent CA's private keys live in a setting encrypted with the
encryption key, so the database and the key must travel together; an archive
that held one without the other could not reconnect a single agent. That is
why both are in one archive rather than the key being left to the operator.

External plugins are platform executables. Copying them would carry a Linux
amd64 binary onto an arm64 restore target, where it cannot run. Their
settings (`plugin_states`) are in the database; the manifest lists name and
version so the restore can name the ones missing on the new machine.

## Database copy: portable rows, not a file copy

- Decision: dump every table AutoMigrate creates as JSON lines keyed by
  database column, read through each model's GORM schema, inside one
  read-only transaction. Restore creates the schema with AutoMigrate on the
  target and inserts the rows through the same schemas.
- Rationale: one format serves all three engines, so SQLite to PostgreSQL
  (FR-006, SC-003) is the same code path as SQLite to SQLite. Decoding each
  value into the field's own Go type (`reflect.New(field.FieldType)` then
  `field.Set`) keeps times, UUIDs, booleans, and byte columns exact, which a
  generic `map[string]any` scan does not across engines. Keying by column
  rather than JSON tag keeps fields hidden from the API (`json:"-"`, such as
  password hashes) in the backup.
- Consistency: a read-only transaction gives one snapshot on every engine.
  The SQLite driver opens a read-only transaction as `BEGIN DEFERRED` with
  `query_only` regardless of `_txlock=immediate`, so under WAL it holds a
  snapshot without blocking writers. PostgreSQL and MySQL use repeatable read,
  read only.
- Alternatives: `VACUUM INTO` or the SQLite backup API give a faster exact
  file for SQLite, but cannot load into PostgreSQL or MySQL, and two formats
  would mean two restore paths to test. `pg_dump`/`mysqldump` need external
  tools and are engine specific.

The model list moves out of `migrate.AutoMigrate` into `models.All()` so the
dump, the restore, and the migration can never disagree about which tables
exist. Soft-deleted rows are dumped (`Unscoped`), since a later purge or
undelete depends on them.

### Load order and keys

GORM creates foreign key constraints for the association fields on PostgreSQL
and MySQL (dashboards to themes, groups, and owners; access rows to users and
groups). The registry order does not satisfy them (`Theme` comes after
`Dashboard`), so the restore sorts tables topologically by their belongs-to
relationships from the parsed schemas. `folders.parent_id` has no constraint.
After loading, PostgreSQL sequences for integer primary keys are moved past
the largest ID (`setval(pg_get_serial_sequence(...), max(id))`); MySQL and
SQLite advance theirs on explicit inserts. Inserts skip hooks
(`Session{SkipHooks: true}`) so `BeforeCreate` does not regenerate IDs or
stamps.

### Older archives

A row from an older version decodes into the current struct: columns the
current model lacks are ignored and new columns take their zero value.
Data migrations that run on start (`freezeAuthoringSettings`, the calendar
migration) then run as on any upgrade. An archive from a newer version is
refused, because a newer column meaning cannot be reconstructed.

## Encryption: age with a passphrase

- Decision: encrypt the archive stream with age (`filippo.io/age` v1.3.2)
  using its scrypt passphrase recipient.
- Rationale: age's payload is a chunked ChaCha20-Poly1305 stream with a final
  chunk flag, so a changed byte, a wrong passphrase, and a truncated file all
  fail authentication (FR-002, SC-005) while the archive still streams in
  64 KiB chunks (FR-003). It is a published format, so an operator whose
  karakuri will not start can still open their archive with the standalone
  `age` tool. Pure Go, no cgo.
- Alternatives: a home-grown scrypt plus AES-GCM stream would avoid a
  dependency but would be a format nobody else can read and that needs its
  own truncation handling. OpenPGP symmetric encryption is heavier and its Go
  library is in maintenance mode.

Inside the encryption the archive is a tar stream compressed with zstd
(`klauspost/compress`, already a dependency), with `manifest.json` as the last
entry so it can carry each part's size and SHA-256 computed while streaming.
Verification therefore reads the whole archive, which it must anyway for age
to authenticate the final chunk.

## Online copies of the other parts

- Flow SQLite databases: `sqlstore` saves each database by writing a
  `.query-*` temporary file and renaming it over the old one. Every file the
  backup opens is therefore a complete database, and an open handle keeps
  reading the version it opened even if a save replaces it.
- History spool: `tswriter` writes each segment as `<name>.seg.tmp` and
  renames it; segments are never appended to. The same holds.
- Uploads and flow files: copied as found.
- Temporary names (`.query-*`, `*.tmp`) are skipped, and a file removed
  between listing and opening is skipped rather than failing the backup.
- No in-process lock is needed, so `backup create` from the command line
  copies the same parts consistently while the server runs. On Windows a
  rename over an open file can fail, so the backup reads each file to the end
  and closes it at once rather than holding it across the archive write, which
  keeps the window for such a collision to one file read.
- Agent identity: two small files, read once.

## Refusing a running server

- Decision: `serve` holds an exclusive lock on `<data>/karakuri.lock`
  (`flock` on Unix, `LockFileEx` on Windows) for its lifetime. `backup
  restore` takes the same lock first and refuses when it cannot.
- Rationale: an operating system lock disappears with the process, so a crash
  never leaves a stale marker the way a PID file does. It also stops two
  `serve` processes sharing one data directory, which today corrupt each
  other's spool.
- Limit: a PostgreSQL or MySQL database used by a server on another machine
  is not detected by a local lock. The populated-database check (FR-005)
  still refuses unless `--replace` is given.

## All or nothing restore

The restore stages everything before touching the target:

1. Decrypt and unpack into `<data>.restore-<stamp>/`; checksums and age
   authentication must pass for every part.
2. With `--replace`, write a backup of the current state beside the archive
   under the same passphrase.
3. Database: SQLite loads into `karakuri.db.restoring` and is renamed over
   the live file (the old one kept as `karakuri.db.pre-restore-<stamp>`);
   PostgreSQL loads in one transaction, since its DDL is transactional; MySQL
   commits DDL implicitly, so a failed load drops the tables it created and,
   under `--replace`, reloads from the backup of step 2.
4. Files: each restored directory is renamed into place, the previous one
   renamed aside; a failure renames them back.
5. Config: secrets are written to a temporary config beside the original and
   renamed last, keeping `config.yaml.pre-restore-<stamp>`. This reuses the
   staged config write of `config rotate-key`.

## Schedule, results, and catch-up

- The schedule lives in the `backup` setting: cron expression parsed by
  `pkg/cronspec`, directory, retention, and the passphrase encrypted with the
  encryption key through `pkg/crypto`, as connection secrets are.
- Each run, manual or scheduled, is a `backup_runs` row; the settings page
  lists archives by scanning the directory and joins the rows by file name,
  so an archive copied in from elsewhere still appears, as unverified.
- On start, when the most recent scheduled time before now is later than the
  last scheduled run, one backup runs after a 2 minute delay, so start-up
  work is not slowed.
- Retention removes the oldest archives matching this installation's file name
  pattern beyond the count, and only after the new archive is complete and
  verified, so a failing backup never costs an old one.

## Archive names

`karakuri-<installation>-<YYYYMMDDTHHMMSSZ>.tar.zst.age`. The installation is
the host name, lowercased with anything outside `[a-z0-9-]` replaced by `-`.
The suffix tells an operator without karakuri which tools open it.
