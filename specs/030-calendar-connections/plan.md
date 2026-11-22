# Design plan: Calendar connections and event triggers

**Feature**: `specs/030-calendar-connections` | **Date**: 2026-10-30 | **Spec**: [spec.md](spec.md)

## Summary

The calendar plugin registers three connection kinds (ICS, CalDAV, Google)
whose drivers fetch, parse, and expand events into occurrences, cache the last
good list, and answer probes. Its nodes and widget select a connection. The
trigger computes start and end moments per occurrence with offsets, records
what it fired in its durable node store, and catches up after downtime from a
stored watermark, applying the missed-deadline setting of feature 029. The
host gains three small additions: an optional connection probe with a detail
string, a trigger recorder for dropped occurrences, and an oauth2 access-token
callback for external plugins. A startup migration moves inline feed settings
into connections.

[research.md](research.md) records the decisions, [data-model.md](data-model.md)
the configs and stored keys, [contracts/interfaces.md](contracts/interfaces.md)
the host, protocol, node, and HTTP shapes, and [quickstart.md](quickstart.md)
the checks.

## Technical context

- Language and dependencies: Go 1.26.5, pure Go. Added:
  `github.com/emersion/go-webdav` for CalDAV. Existing: `emersion/go-ical`,
  `teambition/rrule-go`, gRPC plugin protocol with `protoc-gen-go` and
  `protoc-gen-go-grpc`, invoked through a new `make proto` target.
- Components:
  - `plugins/calendar`: `kinds.go` (three kinds, schemas, secrets, probe),
    `drivers.go` (driver map, fetch, cache, stale window), `caldav.go`,
    `google.go` (API key or oauth2 token), `ics.go` (occurrence keys,
    exceptions, time zones, bounds), `nodes.go` (trigger and lookup),
    `handler.go` (connection list, sources by connection), widget panel and
    widget under `widgets/calendar/src`.
  - `pkg/integrations`: `Probe` on `ConnectionKind`; `pkg/connops`:
    `TestResult.Detail`; frontend connection settings show the detail.
  - `pkg/nodeapi`, `pkg/engine/runner.go`: `TriggerRecorder` on `triggerCtx`,
    writing a canceled run through the history sink.
  - `pkg/plugin/proto`, `pkg/plugin/sdk`, `pkg/pluginhost`: the two host
    callbacks, `detail` on test responses, and kind `Probe` proxied over
    `TestKindConnection`.
  - `pkg/migrate/calendar.go`: the inline-settings migration.
  - Docs: `docs/flow-authoring.md` (calendar trigger, lookup, house-mode
    pattern), `docs/plugins.md` (probe, recorder, token callback),
    `docs/mcp.md` (test detail).
- Storage: no new tables. Connections in `connections`, fired records and
  watermark in `node_store_entries`, widget sources in `settings`.
- Limits: 60-day window and 5,000 occurrences per refresh; seven-day catch-up
  and record retention; 24-hour stale window; the trigger ticks every minute
  or at its refresh interval if shorter.

## Constitution check

- **Failures**: a failing refresh keeps firing from the last good list for a
  day and says so on each run; the lookup fails through its error output with
  retry; connection status shows fetch errors. Late and dropped occurrences
  are recorded. Delivery is at most once per occurrence and edge: the fired
  record is written before emitting.
- **Drafts and deployment**: test runs of the trigger fire a sample event and
  write no fired records. New fields come from node metadata, so the editor,
  REST, MCP, and validation agree. The migration rewrites deployed versions'
  calendar node configs; that alters stored version JSON, which the
  constitution treats as immutable, so the plan calls it out: the rewrite
  only replaces credentials with a connection reference to the same feed and
  changes no behaviour.
- **Permissions**: calendar connections follow connection permissions.
  The widget panel's connection list needs dashboard write and returns names
  only. The token callback is available to plugins, which are trusted code.
  Widget event reads keep the existing dashboard access check.
- **Existing state**: secrets move from plain text in `settings` and node
  configs into encrypted connections under the existing
  `security.encryption_key`. Nothing becomes unreadable; no key change.
- **Verification**: `make proto`, `make dev`, `make test TARGET=server`,
  `make test TARGET=frontend`, the quickstart against a local feed and a
  disposable Radicale container, and a browser check of the widget panel and
  connection test detail.

## Design and contracts

### Drivers and occurrences

Each driver fetches on demand with a short in-memory cache (the refresh
interval of whichever consumer asked), expands recurrences inside the window,
drops cancelled occurrences and applies RECURRENCE-ID exceptions, and returns
occurrences with stable IDs. All-day and floating times resolve in the
connection's `timezone`, else the server's. A failed fetch returns the last
good list and its fetch time while it is under 24 hours old.

### Trigger

```text
Start: load seen_until; catch up moments in (seen_until, now], at most 7 days
  for each moment: fired? skip
                   late > grace and drop → Skipped(reason), record
                   else record, emit with meta.late_ms
tick (1m): refresh when due; for each matching occurrence and edge:
  moment = edge time + offset
  moment in (last tick, now] and not fired → record, emit
  write seen_until = now; prune records older than 7 days past their end
```

A moment before the first arm never fires, because `seen_until` starts at
the arm time. The record is written before `emit`, so a crash between them
loses that firing rather than repeating it, matching feature 029.

### Lookup

Filters as the trigger; `in_progress` holds occurrences with start ≤ now <
end; `upcoming` holds those starting within `window_days`, sorted, capped by
`limit`.

### Migration

On each start, before plugins load: for every flow version and draft holding
a `calendar-event` or `calendar-events` node with `url`, and every
`plugins.calendar.*.sources` setting with sources holding `url`, group by
provider, URL, and credential; create a connection per group with the kind's
config, encrypted; rewrite each config to `connection` and the remaining
fields, mapping `lead_time_m` to `edges: start` and a negative
`start_offset`. Each rewrite commits in one transaction with the connections
it needs. A failure leaves that item untouched and logs which item and why.

## Verification

| Scenario | Test |
| --- | --- |
| US1 | Kind validation and probe tests against an `httptest` ICS server, a recorded CalDAV exchange, and a recorded Google response; connops detail test; REST and MCP masking tests. Live: quickstart 1. |
| US2 | Plugin trigger tests with a fake clock: start, end, both, offsets, filters, recurring with an exception and a cancellation, moved and deleted events, durable records across a second trigger instance, catch-up with deliver, drop, and grace. Live: quickstart 2 and 3. |
| US3 | Lookup tests for in-progress and upcoming boundaries. Live: quickstart 5. |
| US4 | Migration tests on a seeded database: nodes in drafts and deployed versions, widget sources with names, colours, and tags, shared feeds, a failure case, and a second run that changes nothing. Live: quickstart 6. |
| US5 | Guide flow built and run against a test feed. Live: quickstart 7. |
| Host additions | `TriggerRecorder` writes a canceled run; the token callback refuses non-oauth2 connections; plugin host tests over the real protocol. |

## Risks and open decisions

- Deployed versions are rewritten by the migration, as stated above.
- The token callback hands oauth2 access tokens to any external plugin that
  asks. Restricting it to connections a plugin's own configs reference would
  need a reference index the host does not have; deferred.
- A calendar picker control in the connection form would replace pasting
  names from the probe detail; deferred.
- After a restart during a feed outage nothing fires until the feed answers,
  because the stale list is kept in memory only.
- `calendar-events` stays a pure value source, so a read failure fails the
  pulling node rather than taking an error branch; the spec says so.
- Live checks covered the migration, connection probe, lookup, widget panel,
  and one start and end across a restart. Missed-moment catch-up, recurrence,
  overlapping overrides, CalDAV, and Google are covered by tests against a
  fake clock, a real in-process CalDAV server, and recorded Google
  responses, not against external services.
