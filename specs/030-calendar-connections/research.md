# Research: Calendar connections and event triggers

## Connection kinds live in the calendar plugin

- **Decision**: the calendar plugin registers three connection kinds,
  `calendar-ics`, `calendar-caldav`, and `calendar-google`. Their drivers run
  in the plugin process, and the plugin keeps its own map of running drivers
  by connection ID.
- **Rationale**: the plugin already owns calendars: parsing, recurrence
  expansion, the trigger, the lookup, the widget, and its HTTP API. A plugin
  node resolves `Connection(id)` only against drivers in its own process
  (docs/plugins.md), and the plugin's HTTP handlers, which serve the widget,
  need the same drivers. The factory recording each driver in a plugin-side
  map serves both without an SDK change.
- **Alternatives**: core connection kinds in `pkg/integrations` (the plugin's
  nodes could not reach their drivers across the process boundary); one kind
  with a provider select (the three need different credentials and secret
  lists, and a select-driven schema would hide fields the API still accepts).

## CalDAV client

- **Decision**: `github.com/emersion/go-webdav/caldav`.
- **Rationale**: pure Go, from the author of `go-ical`, which the plugin
  already uses for parsing, so events from CalDAV and ICS feeds go through
  the same parser and recurrence expansion. It supports current-user
  principal and calendar-home discovery, calendar listing, and `calendar-query`
  with a time range.
- **Alternatives**: hand-written PROPFIND and REPORT requests (the same work
  done less completely).

## Google private calendars through the oauth2 connection

- **Decision**: add a host callback, `ConnectionAccessToken(conn_id)`, to the
  plugin protocol. The host resolves the connection, requires a driver with
  `AccessToken(ctx)`, and returns a short-lived bearer token. The Google
  driver calls it before each fetch.
- **Rationale**: the oauth2 driver runs in the host. It refreshes and stores
  tokens already, and the HTTP request node reads tokens the same way. The
  plugin process cannot hold the host driver, and a second sign-in flow would
  duplicate the oauth2 kind.
- **Alternatives**: copying refresh tokens into the calendar connection (two
  owners of one token that rotates); a new sign-in flow in the plugin.
- **Trust note**: plugins run as trusted code, and in-process plugins already
  reach every driver; the callback gives an external plugin the same reach
  for oauth2 tokens only. It is logged at debug with the connection ID.

## Showing what a connection test found

- **Decision**: add an optional `Probe func(ctx, id, cfg) (detail string, err
  error)` to `integrations.ConnectionKind`, a `detail` field to the plugin
  protocol's test response, and `detail` to `connops.TestResult`. Calendar
  kinds probe; connection tests in the editor, REST, and MCP show the detail.
- **Rationale**: FR-003 needs the test to name the calendars it found and
  count upcoming events. Changing `Test` for every kind is a wide change for
  three kinds; an optional probe is additive.
- **Calendar selection**: the `calendars` field is a `tokens` control, with
  empty meaning all calendars. The probe's detail lists the names an author
  can paste. A dedicated picker control is left for later.

## Recording a dropped occurrence

- **Decision**: add an optional `TriggerRecorder` interface to trigger
  execution contexts, with `Skipped(msg *Msg, reason string)`, and a matching
  `TriggerSkipped` host callback for external plugins. The host records a run
  that settles at once as `canceled` with the reason and the message payload,
  the same way feature 029 records dropped waits.
- **Rationale**: FR-008 asks for the outcome in run history, and a trigger
  has no run until it emits. Any polling trigger that catches up after
  downtime, schedule triggers included, can use the same call later.
- **Alternatives**: logging only (not in run history); emitting a run with a
  "dropped" flag (downstream nodes would run).

## Fired records and catch-up

- **Decision**: the trigger keeps fired records in its durable node store
  under `fired:<occurrence>:<edge>`, together with a `seen_until` watermark
  written on each tick. On start it looks for moments between the watermark
  and now, at most seven days back, and applies the missed-deadline setting
  to each. Records older than seven days past their occurrence are pruned.
- **Rationale**: the durable node store already crosses the plugin boundary,
  survives restarts and redeploys, and is removed with the flow. These are
  records, not waits; nothing parks, so feature 029's wait store does not
  apply. The watermark separates moments missed while the server was down
  from moments before the trigger was ever armed, which are never fired.
- **Occurrence key**: the event UID plus the occurrence's original start
  (the RECURRENCE-ID for exceptions), so a moved occurrence keeps its key and
  fires once at its new time.

## Stale events during a feed outage

- **Decision**: each driver keeps its last good event list in memory with
  its fetch time and serves it for up to 24 hours when a refresh fails. The
  trigger notes "fired from events fetched at …" on runs it starts from a
  stale list.
- **Rationale**: a calendar server that is down for an hour should not cancel
  the 18:00 arrival of vacation mode. A day bounds how long a deleted event
  can still fire. The list is not stored, so after a restart during an
  outage nothing fires until the feed answers; catch-up then runs from the
  watermark.

## Migrating inline settings

- **Decision**: a startup migration in `pkg/migrate` moves inline feed
  settings into connections. It reads `calendar-event` and `calendar-events`
  nodes in every flow version, drafts included, and widget source lists
  stored under `plugins.calendar.<dashboard>.<widget>.sources`. It creates
  one encrypted connection per distinct provider, URL, and credential, and
  rewrites the configs to reference it.
- **Rationale**: the plugin cannot create connections or edit flow graphs;
  the host owns both tables, and migrations run before plugins load. Deployed
  versions are rewritten too, because leaving an API key in an immutable
  version would defeat FR-002. This is a one-time data upgrade, not an edit
  to deployed behaviour: the rewritten node reads the same feed.
- **Naming**: `Calendar: <source name or host>`, with a numeric suffix when
  the name is taken.
- **Idempotence**: a node or source that already names a connection is
  skipped, so the migration runs on every start at no cost.

## Lookup output

- **Decision**: `calendar-events` returns `{in_progress: [...], upcoming:
  [...]}` instead of a flat list.
- **Rationale**: FR-010 requires both. No flow in the development database
  uses the node, and the project prefers the right shape to a compatibility
  field.
