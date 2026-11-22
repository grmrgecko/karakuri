# Data model: Event log

## `events`

| Column | Type | Rules |
| --- | --- | --- |
| `id` | uint | Primary key; newest first is descending ID |
| `at` | time | First occurrence, UTC |
| `last_at` | time | Last occurrence when folded; equals `at` otherwise |
| `count` | int | Occurrences folded into this row, at least 1 |
| `category` | string | `device`, `connection`, `flow`, `agent`, `security`, `system`, `custom` |
| `level` | string | `info`, `warning`, `error` |
| `subject_kind` | string | `control`, `device`, `ha_entity`, `connection`, `flow`, `agent`, `user`, `group`, `role`, `apikey`, `session`, `backup`, `server`, or empty |
| `subject_id` | string | ID of the subject; for `ha_entity`, `{connection}/{entity}` |
| `subject_name` | string | Name at the time, kept after the subject is deleted |
| `message` | string | At most 500 characters |
| `details` | JSON | Structured fields (old and new value, error, attribute, command, field names changed); never secrets; at most 8 KiB |
| `actor_kind` | string | `user`, `apikey`, `flow`, `agent`, `system`, `anonymous` |
| `actor_id` | string | User, key, flow, or agent ID |
| `actor_name` | string | Username, key name with its owner, flow name, or agent name |
| `via` | string | `dashboard`, `api`, `mcp`, `cli`, `flow`, or empty |
| `dashboard_id` | string | For writes from a dashboard |
| `run_id` | string | For flow actors |
| `address` | string | Client address for network actions |

Indexes: `(category, id)`, `(subject_kind, subject_id, id)`, `(level, id)`,
`(actor_kind, actor_id, id)`, `(at)`.

## Settings key `events`

| Field | Type | Rules |
| --- | --- | --- |
| `retention_days` | object | Per category, 1 to 3650; defaults 30, `security` 365 |
| `max_rows` | int | 10,000 to 10,000,000; default 1,000,000 |
| `excluded_controls` | array | Control UUIDs never logged |
| `ha_entities` | array | `{connection_id, entity_id}` logged on state change, at most 500 |

## Setting `backup.restored`

Written by `backup restore`, read and cleared on the next start:
`{archive, version, created_at, restored_at, os_user, replaced}`.

## Permissions

| Permission | Allows | Default holders |
| --- | --- | --- |
| `api:events:read` | All categories except `security`; event settings read | Admin, User |
| `api:events:audit` | `security` entries | Admin |
| `api:events:write` | Changing event settings | Admin |

## Changes to existing types

- `controls.Writer` and `controls.Change` gain `Actor events.Actor`.
- `middleware.UserContext` gains `APIKeyID` and `APIKeyName`, set when the
  request authenticated with an API key.
