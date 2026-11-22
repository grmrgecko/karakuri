# Data model: Calendar connections and event triggers

No new tables. Connections use the existing encrypted `connections` table;
fired records use the existing durable node store; widget sources stay in
plugin settings.

## Connection configs

| Kind | Field | Notes |
| --- | --- | --- |
| `calendar-ics` | `url` | Required. `webcal://` is read as `https://`. |
| | `auth` | `none` (default), `basic`, or `bearer`. |
| | `username`, `password` | Basic auth. `password` is secret. |
| | `token` | Bearer token. Secret. |
| | `timezone` | Optional zone for floating times and all-day events. |
| `calendar-caldav` | `url` | Required. Server or calendar URL. |
| | `username`, `password` | Required. `password` is secret. |
| | `calendars` | Display names or URLs to read; empty reads every calendar. |
| | `timezone` | As above. |
| `calendar-google` | `calendars` | Required. Calendar IDs. |
| | `auth` | `api_key` (default) or `oauth2`. |
| | `api_key` | Secret. Public calendars. |
| | `oauth2_connection` | Connection field, kind `oauth2`. Private calendars. |
| | `timezone` | As above. |

All kinds share `window_days` (default 60) and `max_occurrences` (default
5,000) bounds per refresh.

## Event and occurrence

```json
{
  "id": "<uid>@<original start RFC 3339>",
  "uid": "…",
  "calendar": "Family",
  "title": "Vacation",
  "start": "2026-11-20T00:00:00-06:00",
  "end": "2026-11-27T00:00:00-06:00",
  "all_day": true,
  "location": "",
  "recurring": false
}
```

`id` is the occurrence key: stable when an occurrence is moved, distinct per
occurrence of a recurring event.

## Trigger durable store (per flow and node)

| Key | Value |
| --- | --- |
| `fired:<occurrence id>:start` / `:end` | Fire moment, RFC 3339. Pruned seven days after the occurrence ends. |
| `seen_until` | The last tick, RFC 3339. Bounds catch-up after downtime. |

## Widget sources

Stored under `plugins.calendar.<dashboard>.<widget>.sources`:

```json
[{"connection_id": "…", "name": "James", "color": "#89b4fa", "tag": "james", "calendars": []}]
```

`url`, `provider`, and `api_key` are gone after migration. `calendars`
narrows a connection that reads several calendars.

## Node configs

| Node | Fields |
| --- | --- |
| `calendar-event` | `connection`, `calendars`, `match`, `all_day` (`include`, `exclude`, `only`), `edges` (`start`, `end`, `both`), `start_offset`, `end_offset` (signed durations), `interval_s`, `missed_deadline`, `missed_grace` |
| `calendar-events` | `connection`, `calendars`, `match`, `all_day`, `window_days`, `limit` |

`provider`, `url`, `api_key`, and `lead_time_m` are removed. The migration
turns `lead_time_m: 10` into `edges: start` with `start_offset: -10m`.
