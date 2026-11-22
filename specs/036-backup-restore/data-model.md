# Data model: Backup and restore

## Archive layout

The archive is `age(zstd(tar))`. Tar entries, in order:

| Entry | Contents |
| --- | --- |
| `db/<table>.jsonl` | One JSON object per row, keyed by column name, for every table in `models.All()` in registry order |
| `secrets.json` | `{"encryption_key": "...", "jwt_secret": "..."}` |
| `agents/server.pem`, `agents/server-key.pem` | The agent server identity, when present |
| `uploads/...` | `<data>/uploads` as stored |
| `sql/<name>.db` | Each flow and global SQLite database |
| `files/<root>/...` | Each `storage.roots` entry under the data directory, by its path relative to the data directory |
| `history-spool/...` | The spool's segment files |
| `manifest.json` | Written last, see below |

Values in `db/*.jsonl` are the JSON encoding of each field's Go value: times
in RFC 3339 with nanoseconds, byte slices in base64, UUIDs as strings,
serializer fields as their decoded value. `null` is a SQL NULL.

## `manifest.json`

| Field | Type | Notes |
| --- | --- | --- |
| `format` | int | `1`; a reader refuses a larger number |
| `version` | string | karakuri version; `dev` for development builds |
| `created_at` | RFC 3339 | UTC |
| `installation` | string | Host name as used in the file name |
| `database` | string | `sqlite`, `postgres`, or `mysql` |
| `counts` | object | Summary counts `flows`, `dashboards`, `connections`, `devices`, `controls`, `users`, `agents` (live rows, soft-deleted excluded) |
| `tables` | object | Rows per table, soft-deleted rows included |
| `file_roots` | array | Flow files roots the archive holds, by path relative to the data directory, so a restore puts each back where it was |
| `parts` | array | `{name, size, sha256}` per tar entry before the manifest |
| `plugins` | array | `{name, version}` of external plugins loaded at backup time |
| `skipped` | array | Strings naming what was left out: renders, traces, logs, wasm cache, and any `storage.roots` entry outside the data directory |

## `backup_runs`

| Column | Type | Rules |
| --- | --- | --- |
| `id` | uint | Primary key |
| `started_at`, `finished_at` | time | `finished_at` null while running |
| `trigger` | string | `manual`, `schedule`, `flow`, `cli` |
| `user_id` | uint, nullable | Who asked for it; null for the schedule |
| `flow_id` | uuid, nullable | Set for `flow` |
| `directory` | string | Where the archive was written |
| `archive` | string | File name; empty when the run failed before naming it |
| `size` | int64 | Bytes |
| `status` | string | `running`, `succeeded`, `failed` |
| `error` | string | Failure reason, at most 2,000 characters |
| `verified` | string | `pending`, `ok`, `failed`; the post-write verification |
| `counts` | JSON | The manifest's summary counts |

A run left `running` at start (the server stopped mid-backup) is marked
`failed` with the reason `server stopped during the backup`, and its temporary
file is removed. Rows older than 400 days are deleted daily.

## Settings key `backup`

| Field | Type | Rules |
| --- | --- | --- |
| `enabled` | bool | Default false |
| `cron` | string | Valid for `pkg/cronspec`; default `0 3 * * *` |
| `directory` | string | Absolute path; default `<data>/backups`; must exist or be creatable and writable when saved |
| `retain` | int | 1 to 365, default 14 |
| `passphrase` | string | Encrypted with the encryption key; required when `enabled`; at least 12 characters before encryption; never returned by reads, which answer `passphrase_set` |
| `last_scheduled_at` | RFC 3339 | The schedule time the last scheduled run was for; drives catch-up |

## Permissions

| Permission | Allows |
| --- | --- |
| `api:backups:read` | List archives and runs, read the schedule |
| `api:backups:write` | Create, verify, and delete archives; change the schedule |
| `api:backups:download` | Download an archive |

Admins hold all three through `api:*`. The User role gets none. Download is
separate because an archive opened with its passphrase hands over every
credential and the agent CA.
