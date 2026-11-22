# Design plan: Event log

**Feature**: `specs/037-event-log` | **Date**: 2026-11-05 | **Spec**: [spec.md](spec.md)

## Summary

A new leaf package `pkg/events` holds the log: an `events` table, an
asynchronous recorder that folds repeats and writes in batches, the actor type
that says who did something, and the queries, retention, and live delivery
over the broker. Who did it travels in the request or node context: the API
and MCP middleware put the caller there after authentication and the engine
puts the flow and run there for every node, so a device command or a
connection change made through any path records the right actor without new
parameters. Controls, which already take an explicit writer, gain the actor in
it.

Most sources are already observable: control changes and device availability
through their watchers, connection status and run results through broker
topics, and backups through the backup service. Security events and changes
to users, roles, keys, and connections are recorded where they happen. An
Activity page, a dashboard activity widget built on the existing control,
device, and entity bindings, a Log Event node, an Event Logged trigger, and
REST and MCP reads sit on top. [research.md](research.md) records the
decisions, [data-model.md](data-model.md) the table and settings,
[contracts/interfaces.md](contracts/interfaces.md) the routes, topics, nodes,
page, and widget, and [quickstart.md](quickstart.md) the checks.

## Technical context

- Language and dependencies: Go 1.26.5, Svelte 5. No new dependencies.
- Components:
  - `pkg/events` (new): `actor.go` (Actor, context helpers),
    `model.go` (the `events` table via `pkg/models`), `recorder.go` (channel,
    folding, batch writes, broker publish, watchers), `query.go` (filters,
    keyset pages, visibility), `retention.go` (sweep), `settings.go`,
    `sources.go` (subscriptions to controls, devices, connections, runs,
    backups, and listed Home Assistant entities).
  - `pkg/models/event.go`: the `Event` model in `models.All()`, so backups
    carry the log.
  - `pkg/middleware`: an `Actor` middleware after `Auth` that builds the actor
    from `UserContext` (with the API key name), `RequestMeta`, and the
    dashboard context; `UserContext` gains `APIKeyID` and `APIKeyName`.
  - `pkg/engine`: node contexts carry the flow actor (flow ID and name, run ID).
  - `pkg/controls`: `Writer.Actor`, copied into `Change`; the three write sites
    fill it.
  - `pkg/mqttrelay`: `Devices.Command` records a device entry with the
    context's actor.
  - Direct records in `pkg/auth` and `api/auth_handler.go` (sign-in, failure,
    sign-out, sessions), the user, group, role, permission, and API key
    handlers and MCP tools, `pkg/connops` (create, update with changed field
    names, delete), `pkg/flowops` (deploy, enable, disable), `pkg/agenthub`
    (enroll, connect, disconnect, revoke), `pkg/backup` (download; restore
    marker), `cmd/karakuri` (start, stop, rotate-key).
  - `pkg/api/event_handler.go`, `pkg/mcpserver/tools_events.go`,
    `pkg/permissions` (`api:events:read|audit|write`), `pkg/migrate` (User role
    gains read).
  - `pkg/dashscope`: `AllowsTopic` for `events:subject:*`; the list route's
    dashboard-scope check.
  - `pkg/nodes/core/events.go`: `log-event` and `event-logged`.
  - `frontend/src/routes/Activity.svelte` with its navigation entry, and the
    log settings at `frontend/src/routes/settings/EventLog.svelte` in the
    Settings section; `plugins/core/widgets/activity`.
  - Docs: `docs/events.md` (new guide), `docs/flow-authoring.md`,
    `docs/dashboard-authoring.md`, `docs/mcp.md`.
- Storage: new table `events`; settings `events` and `backup.restored`.
- Limits: channel of 4,096 entries; batches every 250 ms or 100 entries;
  folding window 60 s; message 500 characters; details 8 KiB; list pages of at
  most 500; text search over the newest 10,000 matches; retention sweep every
  6 hours.

## Constitution check

- **Failures**: recording never fails or delays the action it describes; a
  full channel or a write error drops entries, counts them, and logs at error.
  The Event Logged trigger follows trigger rules: a bad filter refuses to arm
  with the reason.
- **Drafts and deployment**: node fields come from metadata; the log is
  configuration and history, not a draft.
- **Permissions**: three new permissions; security entries filtered in SQL
  and by topic; dashboards reach only entries of subjects their bindings
  already grant and never security entries; a trigger on security entries
  needs its owner to hold audit. No secret value is recorded: entries name
  changed fields, never their values, and failed sign-ins record the username
  only.
- **Existing state**: a new table and settings; the User role gains
  `api:events:read` through seeding. No key or identity effect. Backups carry
  the log because the model joins `models.All()`.
- **Verification**: `make dev`, `make test`, recorder tests for folding and
  dropping, query tests for visibility and pagination on a million rows,
  actor tests for each path, live checks with the lab broker under
  `karakuri-test`, and a browser check of the page and widget.

## Design and contracts

### Recording

```text
source -> events.Record(Event{category, level, subject, message, details, actor})
  non-blocking send to channel (drop + count when full)
writer goroutine, every 250 ms or 100 entries:
  for each entry: key = category|subject|level|message
    seen within 60 s -> UPDATE count+1, last_at   -> publish event_update
    else             -> INSERT                    -> publish event
  publish to: events | events:security, and events:subject:{kind}:{id}
  notify watchers (Event Logged triggers)
```

### Actor

```text
HTTP request: Auth -> Actor middleware -> handler
  Actor{kind: user|apikey|anonymous, id, name, via: api|dashboard|mcp, dashboard_id, address}
Engine node invoke: ctx = events.WithActor(ctx, Actor{kind: flow, id, name, run_id, via: flow})
controls: Writer{Source, FlowID, RunID, ..., Actor}  (call sites copy ActorFrom(ctx))
anything else: events.ActorFrom(ctx) or the system actor
```

### What each source records

| Source | Category | Level | Message shape |
| --- | --- | --- | --- |
| Control change | device | info | `Porch light set to on` with old and new |
| Device command | device | info | `Garage door: state set to open` |
| Device availability | device | warning offline, info online | `Garage sensor went offline` |
| Connection status | connection | error on error, info on connected, warning on connecting after error | `Garage MQTT disconnected: <error>` |
| Run failed | flow | error | `Motion lights failed at Send notification: <error>` |
| Deploy, enable, disable | flow | info | `Motion lights deployed (version 4)` |
| Agent | agent | info, warning on unexpected disconnect | `arch connected` |
| Sign-in and the rest of security | security | info, warning on failure | `admin signed in`, `Failed sign-in for admin` |
| Backup | system | info, error on failure | `Backup finished (70 KiB)` |
| Server | system | info | `Server started (version 0.1.0)` |
| Home Assistant entity | device | info | `Front door: on` |
| Log Event | custom | as set | as set |

## State and data model

See [data-model.md](data-model.md).

## Verification

| Scenario | Test |
| --- | --- |
| US1 | Actor tests: a control written through the REST handler with a session, with an API key, from a dashboard scope, and from a node records the right actor; a device command from a node and from REST likewise; availability changes recorded. Live: quickstart 1. |
| US2 | Sign-in, failure, sign-out, key create and delete, role change record security entries with no secret field; visibility tests for read without audit across REST, MCP, topics, and dashboard scope. Live: quickstart 2. |
| US3 | Folding test with 100 repeats; connection status and run failure sources on a fake broker. Live: quickstart 3. |
| US4 | Node tests for Log Event and Event Logged (filters, own-flow skip, security refusal); widget scope tests. Browser: quickstart 4. |
| US5 | Retention sweep by age and by row limit keeping security; SC-004 timing on a million rows. |

## Risks and open decisions

- Control changes are the busiest source. A control written by a script many
  times a second is folded by the 60 second window, and excluded controls skip
  recording entirely; a per-control rate limit can follow if folding is not
  enough.
- Message folding keys on the message text, so a control whose value changes
  every write produces a new message each time and is not folded; these are
  real changes and are kept, and the row limit bounds them.
- Spec 004 states control writes record their source, flow, and run; its plan
  and requirement are updated to add the actor when this feature is
  implemented.
