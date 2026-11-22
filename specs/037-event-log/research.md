# Research: Event log

## Where events come from

| Event | Source today | How the log hears of it |
| --- | --- | --- |
| Control change | `controls.Store.Set`/`SetAttribute` with a `controls.Writer` (source, flow, run) | `controls.S.Watch`; the writer gains the actor |
| Device command | `mqttrelay.Devices.Command` from the REST handler, MCP, and the device nodes | Recorded inside `Command` from the actor in its context |
| Device availability | `mqttrelay.D.WatchAvailability` (`AvailabilityChange`) | Watch |
| Connection status | `connmgr` publishes `connection:{id}:status` | Broker subscription `connection:*:status` |
| Flow run failed | Engine publishes `flow:{id}:runs` `run_finished` | Broker subscription, failed runs only |
| Flow deployed, enabled, disabled | `flowops` | Direct calls |
| Agent connect, disconnect, enroll, revoke | `agenthub` | Direct calls |
| Sign-in, failure, sign-out, session revoked | `auth` service and `api/auth_handler.go` | Direct calls |
| Users, groups, roles, permissions, API keys | API handlers and MCP tools | Direct calls |
| Connection created, changed, deleted | `connops` | Direct calls, field names only |
| Encryption key rotated, backup restored | CLI, server stopped | CLI writes the entry to the database itself (rotate) or leaves a marker the next start records (restore) |
| Backup finished, downloaded | `backup.S.Watch`, `LogDownload` | Watch and direct call |
| Server start, stop | `serve` | Direct calls |
| Home Assistant entity | broker `ha:{conn}:state:{entity}` | Subscriptions for listed entities only |

## Who did it: the actor travels in the context

- Decision: a small leaf package `pkg/events` defines `Actor` (kind `user`,
  `apikey`, `flow`, `agent`, `system`, `anonymous`; ID; name; client address;
  `via` of `dashboard`, `api`, `mcp`, `cli`; dashboard ID; run ID) and
  `WithActor(ctx)`/`ActorFrom(ctx)`. The API middleware puts the caller's
  actor in every request context after authentication, the MCP handler does
  the same, and the engine puts the flow's actor in every node's context.
- Rationale: device commands, connection saves, and user changes are reached
  from REST, MCP, and nodes through shared service layers. Carrying the actor
  in the context records the right one at the place the change happens,
  without adding a parameter to every function between. Controls, which take
  an explicit `Writer`, gain an `Actor` field filled from the context by the
  three call sites.
- The auth middleware already resolves API keys; `UserContext` gains the key's
  name so an API key write says which key, not only whose.
- Alternatives: recording at each HTTP handler duplicates every rule in MCP and
  misses flow-originated commands.

## Writing without slowing anything

- Decision: `events.Record(e)` puts the entry on a buffered channel (4,096)
  and returns. One writer goroutine folds and writes batches every 250 ms or
  100 entries. A full channel drops the entry, counts it, and logs at error
  once a minute with the count; the action it describes is never held up
  (FR-013).
- Folding: the writer keeps the last entry ID per key (category, subject kind
  and ID, level, message) for 60 seconds; a repeat inside the window updates
  `count` and `last_at` on that row instead of inserting. A repeat that reaches
  the database after the window starts a new row.
- Live delivery and the Event Logged trigger see the folded result: the first
  entry immediately, later repeats as updates carrying the count, so a
  connection flapping 100 times a minute fires a trigger once.

## Storage and queries

One table, `events`, with an integer ID so newest-first is an index scan on
the primary key. Indexes on `(category, id)`, `(subject_kind, subject_id, id)`,
`(level, id)`, and `(actor_kind, actor_id, id)`. Listing is keyset paginated
(`before` an ID), never `OFFSET`, which keeps SC-004's 100 newest entries with
a filter under 200 ms at a million rows on SQLite. Text search is a `LIKE` on
message and subject name within the other filters and a time range, capped at
the newest 10,000 matching rows; a full-text index is not worth its write cost
for an operator's occasional search.

## Retention

A sweep at start and every 6 hours deletes per category by age, then, if the
table is over the row limit, deletes the oldest non-security rows in batches of
10,000 until under it. Security rows are only removed by their own age, so a
burst of device activity can never push out the audit trail.

## Security entries and permissions

- `api:events:read` lists every category except `security`; `api:events:audit`
  is needed to see `security`. Queries filter in SQL, so a reader never learns
  a hidden entry existed (no counts, no gaps in what they are shown, since
  pagination is by ID among visible rows).
- WebSocket topics: `events` carries non-security entries (read permission),
  `events:security` carries security entries (audit permission), and
  `events:subject:{kind}:{id}` carries a subject's entries for dashboards.
- The User role gains `api:events:read`; administrators hold both through
  `api:*`. Existing installations get the read grant through the seeding that
  already adds missing permissions to default roles.

## Dashboards

- Decision: the activity widget binds its subjects with the existing control,
  device, and Home Assistant bindings, as the entities card does. A dashboard
  scope that allows reading a control allows its log entries; the list route
  under dashboard scope accepts only subjects the scope allows and never
  returns `security`. `dashscope.AllowsTopic` maps
  `events:subject:control:{id}` to the control capability, and likewise for
  devices and entities.
- Rationale: "only what the dashboard shows" is exactly the set of bindings the
  dashboard already grants, so no new grant can drift from it.

## Flows

- Log Event (`log-event`) records a `custom` entry with the flow as actor.
- Event Logged (`event-logged`) is a trigger on `events.Watch`, filtered by
  category, level, and subject. It skips entries whose actor is its own flow,
  of any category: a flow that writes a control and also listens for control
  entries would otherwise trigger itself. Security entries are available to
  the trigger only when the flow's owner holds the audit permission, checked
  when the trigger arms.

## Home Assistant and exclusions

The `events` setting lists Home Assistant entities to log (connection and
entity ID) and controls excluded. The recorder subscribes to exactly the listed
entities' broker topics and re-subscribes when the setting changes.

## Restore and rotate from the command line

The server is stopped for both, so nothing is listening. `config rotate-key`
writes its security entry straight to the database before exiting. `backup
restore` writes a `backup.restored` setting holding the archive name, time,
and operating system user; the next start records the security entry and
clears the setting, which also makes the restore the first entry of a
restored installation.
