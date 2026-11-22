# Backup

A backup is one file holding everything an installation needs to come back on
the same machine or a new one. It contains the encryption key, and with the
key every stored credential and the agent CA, so the file is encrypted under a
passphrase. Backups run while the server works. A restore runs from the
command line with the server stopped.

Manage backups under **Settings, Backup**, with the `karakuri backup`
commands, or through the REST routes and MCP tools listed at the end.

## What an archive holds

| Part | Notes |
| --- | --- |
| The database | Every table from one moment, including deleted rows that are kept. |
| Encryption key and JWT secret | `security.encryption_key` and `security.jwt_secret` from the config file. Nothing else from the config is carried. |
| Agent server identity | `agents/server.pem` and `server-key.pem`. The agent CA itself is in the database. |
| Widget uploads | `uploads/`. |
| Flow SQLite databases | `sql/`, each a whole database even when a flow was writing it. |
| Flow files | Each `storage.roots` entry under the data directory, by default `files/`. |
| Undelivered history | `history-spool/`, the points history forwarding has not yet written. |

Every archive's manifest lists what is left out: rendered dashboard images,
assistant traces, logs, the script runtime cache, flow file roots outside the
data directory, and external plugin executables. Plugins are built for one
platform, so the archive lists them by name and version, and a restore names
the ones the new machine lacks. Their settings are in the database.

When a part is a symbolic link to a directory, such as `uploads/` kept on
another disk, the backup follows it and archives its files. Links inside a
part are left out. A restore moves the link aside and puts a directory in its
place.

Each part is copied consistently, but the database and the flow SQLite
databases are not copied at the same instant. A flow that writes both in one
run can restore with one write and not the other.

## The file

Archives are named `karakuri-<host>-<YYYYMMDDTHHMMSSZ>.tar.zst.age`. Each is a
tar stream, compressed with zstd and encrypted with
[age](https://age-encryption.org) under a passphrase. Karakuri refuses an
archive with a changed byte, a cut-off file, or a wrong passphrase. If karakuri
itself will not start, the standard tools open one:

```sh
age -d karakuri-lab-20261106T030000Z.tar.zst.age | zstd -d | tar -x
```

`manifest.json`, the last entry, records the version, the time, the database
engine, row counts, each part's size and SHA-256, the plugins, and what was
left out.

A passphrase has at least 12 characters. **A lost passphrase cannot be
recovered**, and without it the archive is unreadable. Keep a copy somewhere
other than the server.

## Command line

Every command reads the passphrase from `--passphrase-file`, the
`KARAKURI_BACKUP_PASSPHRASE` variable, or a prompt. It never takes the
passphrase as an argument, because other users can read arguments.

| Command | What it does |
| --- | --- |
| `karakuri backup create [--output DIR]` | Writes and verifies an archive, with the server running or not. The directory defaults to the schedule's. |
| `karakuri backup verify ARCHIVE` | Reads the whole archive and prints its manifest, or the part that does not match. |
| `karakuri backup list [--dir DIR]` | Lists archives with size, time, and verification. |
| `karakuri backup restore ARCHIVE [--replace]` | Restores into this installation. |

## Restoring

1. Stop the server. A restore refuses while a server holds
   `<data>/karakuri.lock`.
2. On a new machine, install karakuri and its config first. The restore keeps
   the target's addresses, ports, paths, and database settings.
3. Run `karakuri backup restore ARCHIVE`. The restore refuses an installation
   that already has users unless `--replace` is given. With `--replace`, it
   first backs up the current state beside the archive under the same
   passphrase.
4. Start the server. It upgrades an older archive's database as any upgrade
   does, and enrolled agents reconnect without enrolling again.

The restore unpacks and checks the whole archive before changing anything,
and a failure at any later step puts everything back. Nothing is deleted. The
replaced database, each replaced directory, and the config file stay beside
the originals with a `.pre-restore-<stamp>` suffix. The kept config holds the
replaced encryption key, the only key its data opens with.

Karakuri refuses an archive from a newer release, so upgrade first.
Development builds have no order, so their archives restore with a warning.

### Moving between database engines

The archive stores the database as rows, not as a database file, so an archive
made on SQLite restores into PostgreSQL or MySQL and back. Point the target's
config at the new database and restore. PostgreSQL loads in one transaction.
MySQL commits schema changes as it goes, so on a failed load karakuri drops it
and reloads the safety backup.

The local lock does not cover a PostgreSQL or MySQL server that karakuri uses
on another machine. Stop that server too.

## Before an upgrade

With a passphrase stored for scheduled backups, a new release copies the
installation into the backup directory before it changes the database, as an
ordinary archive dated from the start and stamped with the release it replaces.
It appears in the list beside the scheduled ones and counts toward their
retention. Restore it to undo an upgrade; either release accepts it. See
[Every upgrade, however it arrives](configuration.md#every-upgrade-however-it-arrives).

## Schedule

| Setting | Meaning |
| --- | --- |
| Schedule | Cron in the server's time zone, `0 3 * * *` unless set. |
| Directory | Absolute path, `<data>/backups` unless set. A directory on another disk survives this one failing. |
| Keep | Archives kept, 1 to 365, 14 unless set. |
| Passphrase | Stored sealed with the encryption key, so a scheduled backup needs nobody present. Required to turn the schedule on. |

Saving creates the directory and writes a test file there, so an unwritable
directory is refused at once. After each scheduled backup verifies, karakuri
deletes the oldest of this installation's archives beyond the count. It leaves
other installations' archives and other files in the directory alone, and a
failed backup never removes one.

A server that was stopped at a scheduled time runs one backup two minutes
after it starts. A server stopped during a backup marks that run failed and
removes its partial file when it starts.

Backups run one at a time, and a second waits. Karakuri writes an archive as a
hidden `.partial` file, renames it when complete, then reads it back and
verifies it. An archive that does not verify is removed and the run fails.

## Flows

**Back up** (`backup-create`) writes an archive with the stored passphrase,
in the schedule's directory or its own, waits for it, and outputs `archive`,
`path`, `size`, `duration_ms`, `counts`, and `verified`. Without a stored
passphrase it fails without retrying. A disk error may succeed on retry.
Retention applies only to scheduled backups.

**Backup finished** (`backup-result`) starts a run when a backup from any
trigger fails (the default), succeeds, or either. The payload has `status`,
`trigger`, `archive`, `size`, `error`, `started_at`, and `finished_at`. Pair
it with a notification node to hear about a failed night.

## Permissions, REST, and MCP

| Permission | Allows |
| --- | --- |
| `api:backups:read` | Listing archives and runs, and reading the schedule. |
| `api:backups:write` | Creating, verifying, and deleting archives, and changing the schedule. |
| `api:backups:download` | Downloading an archive. It is separate because an archive and its passphrase hand over every credential. Each download is logged with its user. |

Administrators hold all three.

| Route | Permission |
| --- | --- |
| `GET /api/backups`, `GET /api/backups/runs`, `GET /api/backups/schedule` | read |
| `POST /api/backups`, `POST /api/backups/{name}/verify`, `DELETE /api/backups/{name}`, `PUT /api/backups/schedule` | write |
| `GET /api/backups/{name}/download` | download |

Routes take archive names only, and anything else is not found. MCP has
`list_backups`, `get_backup_schedule`, `create_backup`, `verify_backup`,
`delete_backup`, and `save_backup_schedule`. Restore has no remote interface.
