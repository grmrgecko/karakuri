# Contract: Event log interfaces

## REST

| Route | Permission | Notes |
| --- | --- | --- |
| `GET /api/events` | read | Query: `category` (repeatable), `level` (repeatable), `subject_kind` with `subject_id` (repeatable pairs as `subject=kind:id`), `actor` (`kind:id`), `q` (text), `from`, `to` (RFC 3339 or `now-24h`), `before` (an ID, for the next page), `limit` (1 to 500, default 100). Answers `{events: [...], next_before?}`. `security` entries are included only with the audit permission; a request naming `category=security` without it is refused |
| `GET /api/events/settings` | read | Retention, row limit, excluded controls, listed entities, and the table's current size |
| `PUT /api/events/settings` | write | Same fields; refuses retention outside 1 to 3650 days, a row limit outside 10,000 to 10,000,000, or more than 500 entities |

Under a dashboard scope (a viewer of a shared or public dashboard),
`GET /api/events` requires at least one `subject`, every subject must be one
the scope allows reading, and `security` is never returned.

An entry:

```json
{
  "id": 4211, "at": "2026-11-06T08:58:12Z", "last_at": "2026-11-06T08:58:12Z", "count": 1,
  "category": "device", "level": "info",
  "subject": {"kind": "control", "id": "3c0…", "name": "Porch light"},
  "message": "Porch light turned on",
  "details": {"old": false, "new": true},
  "actor": {"kind": "flow", "id": "b7c…", "name": "Motion lights", "via": "flow", "run_id": "4f4…"}
}
```

## WebSocket topics

| Topic | Carries | Requires |
| --- | --- | --- |
| `events` | Every non-security entry and fold update | `api:events:read` |
| `events:security` | Security entries | `api:events:audit` |
| `events:subject:{kind}:{id}` | One subject's entries | `api:events:read`, or a dashboard scope allowing that subject |

Messages have type `event` (new entry) or `event_update` (a fold changed
`count` and `last_at`).

## MCP

`list_events` with the REST filters, and `get_event_settings`, under the same
permissions. No tool writes the log directly; flows do that with Log Event.

## Nodes (folder Utility)

- `log-event` (action): `message` (required, templated from the message),
  `level` (`info` default, `warning`, `error`), optional `control` or `device`
  (pickers) the entry is about, and `include_payload` to store the payload as
  details.
  Output passes the input through; the entry is written in the background,
  so it carries no ID.
- `event-logged` (trigger): `categories` (tokens, empty is all except
  security), `levels` (tokens), `control` or `device` (pickers), `match`
  (text contained in the message). Payload is the entry. Skips entries whose actor is its own
  flow. Security entries need the flow owner to hold `api:events:audit`, or
  the trigger refuses to arm with that reason.

## Activity page

Top-level **Activity** in the navigation for holders of `api:events:read`:
filter bar (time range, categories, levels, subject search, actor, text), a
newest-first list with the actor and subject as links, folded counts as
"×12", load more by ID, and live entries while the newest page is shown. The
settings tab, for `api:events:write`, edits retention, the row limit, excluded
controls, and listed Home Assistant entities.

## Dashboard widget `activity`

Config: rows of control, device, or Home Assistant entity bindings (as the
entities card), `categories`, `levels`, `limit` (1 to 50, default 10), and
`show_actor`. Shows the newest matching entries with relative times and
updates live.
