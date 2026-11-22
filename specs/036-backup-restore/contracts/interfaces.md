# Contract: Backup interfaces

## Command line

The commands read the same config file as `serve`. Passphrases come from
`--passphrase-file`, the `KARAKURI_BACKUP_PASSPHRASE` environment variable,
or an interactive prompt asked twice, in that order; never from an argument,
which other users can read in the process list.

- `karakuri backup create [--output DIR] [--passphrase-file F]`: writes an
  archive into `DIR` (default: the schedule's directory). Works with the
  server running or stopped. Prints the path, size, and summary counts.
- `karakuri backup verify ARCHIVE [--passphrase-file F]`: prints the manifest
  (version, created, installation, database, counts, plugins, skipped) and
  `ok`, or the first part that fails and exits 1.
- `karakuri backup list [--dir DIR]`: archives in the directory with size,
  time, and the recorded verification result.
- `karakuri backup restore ARCHIVE [--replace] [--passphrase-file F]`:
  - Refuses while a server holds `<data>/karakuri.lock`:
    `the server is running; stop it with systemctl stop karakuri first`.
  - Refuses a database holding users unless `--replace`:
    `the installation already has 3 users and 12 flows; pass --replace to back it up and replace it`.
  - Refuses `format` above 1 and a release version newer than this binary:
    `archive is from karakuri 1.4.0; this is 1.3.2; upgrade before restoring`.
  - Prints what it set: database engine and counts, each restored directory,
    the config keys written and the saved copy's path, the safety backup's
    path under `--replace`, and the external plugins the archive lists that
    this machine does not have.

## REST

Under `/api/backups`. Each route needs the permission named.

| Route | Permission | Notes |
| --- | --- | --- |
| `GET /api/backups` | read | `{archives: [{name, size, created_at, verified, run_id?, counts?}], running: run?}` |
| `GET /api/backups/runs?limit=` | read | Recent runs, newest first, default 50 |
| `POST /api/backups {passphrase?}` | write | Starts a backup; without a passphrase, uses the stored one. Answers 202 with the run. A second request while one runs is queued and answers its own run |
| `POST /api/backups/{name}/verify {passphrase?}` | write | Answers the manifest and result |
| `GET /api/backups/{name}/download` | download | Streams the file, `Content-Disposition: attachment`. Logged at info with user and file |
| `DELETE /api/backups/{name}` | write | Removes the file; the run row stays |
| `GET /api/backups/schedule` | read | `{enabled, cron, description, directory, retain, passphrase_set, next_at, last}` |
| `PUT /api/backups/schedule` | write | Same fields with `passphrase` write-only. Refuses an unwritable directory, a bad cron, a short passphrase, or enabling without one |

`{name}` must match the archive name pattern and name a file in the
configured directory; anything else is 404, so the routes never reach another
path.

## MCP

`list_backups`, `create_backup`, `verify_backup`, `delete_backup`,
`get_backup_schedule`, `save_backup_schedule`, under the same permissions.
There is no download or restore tool.

## Nodes (folder System)

- `backup-create` (action): optional `directory` override. Uses the stored
  passphrase; fails without retry when none is set. Waits for the backup and
  outputs `{archive, path, size, duration_ms, counts, verified}`. A failure
  is retryable when it was an I/O error (disk full, directory gone) and not
  when the configuration is missing.
- `backup-result` (trigger): fires when a backup finishes. `on`:
  `failed` (default), `succeeded`, or `any`. Payload `{status, trigger,
  archive?, size?, error?, started_at, finished_at}`.

## Settings page

`Settings, Backup`, shown to holders of `api:backups:read`:

- Schedule: enabled, schedule (cron with its description and next time),
  directory, retain, passphrase (set or replace, with "A lost passphrase
  cannot be recovered. Keep a copy somewhere other than this server.").
- Archives: name, time, size, verified, with Verify, Download, and Delete;
  the running backup with elapsed time; Back up now.
- Last runs: status, trigger, duration, error.
