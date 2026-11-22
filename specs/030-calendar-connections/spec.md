# Feature specification: Calendar connections and event triggers

**Feature**: `specs/030-calendar-connections`
**Created**: 2026-10-30
**Status**: Implemented
**Input**: Calendar connections and event triggers. Today the calendar plugin
(ICS feeds and Google Calendar by public API key) keeps feed URLs and API keys
in each node and widget config, its calendar-event trigger fires only a lead
time before an event starts, and the set of already-fired events lives in
memory so a restart inside the lead window fires again. Make calendars
connections with encrypted credentials: ICS feed, CalDAV, and Google Calendar
(public by API key, or private through the existing oauth2 connection kind).
Nodes and the calendar widget select a connection; existing inline feed
settings move into connections on upgrade. The trigger fires at event start,
end, or both, each with an offset, filters by title and calendar, and
remembers fired occurrences durably, including ones that came due while the
server was down. A lookup reports events in progress as well as upcoming ones.
Recurring events fire per occurrence. House-mode overrides are built from
existing nodes.

## User scenarios and testing

### User story 1: Connect a calendar once (Priority: P1)

An author adds the family's Nextcloud calendar as a CalDAV connection, picks
the "Family" calendar from the list the server finds, and tests it. Every flow
node and dashboard widget that needs that calendar selects the connection by
name. The password is never shown again and never appears in a flow export.

**Why this priority**: Every other story reads calendars through a connection.
Credentials in node configs travel with every export and every copy of a flow.
**Independent test**: Create one connection of each kind, test each, and list
upcoming events from each through the lookup node.

**Acceptance scenarios**:

1. **Given** an ICS feed URL, optionally with basic auth or a bearer token,
   **When** an author saves and tests the connection, **Then** the test
   reports how many events the feed holds in the next week.
2. **Given** a CalDAV server URL, username, and password, **When** the author
   asks for the account's calendars, **Then** the server lists them and the
   author chooses one or more to read.
3. **Given** a public Google calendar ID and an API key, or a private calendar
   and an existing oauth2 connection signed in to Google, **When** the
   connection is tested, **Then** it reports the calendar's name and upcoming
   event count.
4. **Given** a saved connection, **When** anyone reads it through the editor,
   REST, MCP, or a flow export, **Then** its password, token, and API key are
   masked.
5. **Given** wrong credentials or an unreachable server, **When** the
   connection is tested, **Then** the test fails with the server's reason and
   the connection status shows the error.

### User story 2: Run a flow when an event starts or ends (Priority: P1)

An author wants the house in vacation mode for exactly as long as the
"Vacation" event on the family calendar. A calendar trigger fires at the
event's start and at its end. The start sets the mode control to `away`, and
the end sets it back to `home`. A server restart in the middle of the
vacation changes nothing.

**Why this priority**: Acting on an event's end is what makes calendar-driven
overrides possible, and a trigger that fires twice or not at all across a
restart is worse than none.
**Independent test**: Create an event that starts in two minutes and ends in
four, arm a trigger for both edges, restart the server between them, and
confirm one start run and one end run.

**Acceptance scenarios**:

1. **Given** a trigger set to fire at start, end, or both, with an offset
   before or after each, **When** an event's moment arrives, **Then** one run
   starts carrying the event, which edge fired, and the occurrence's start and
   end.
2. **Given** a recurring event, **When** each occurrence arrives, **Then** the
   trigger fires for that occurrence, and an exception or cancelled
   occurrence in the feed is honoured.
3. **Given** a title filter, a calendar filter, or both, **When** events
   arrive, **Then** only matching events fire; all-day events can be included
   or excluded.
4. **Given** an occurrence that already fired, **When** the server restarts or
   the flow is redeployed, **Then** it does not fire again.
5. **Given** an occurrence whose moment came while the server was down,
   **When** the server starts, **Then** the trigger fires it late or drops it
   according to its missed-deadline setting, and run history shows which.
6. **Given** an event that is moved or deleted in the calendar before its
   moment, **When** the trigger next refreshes, **Then** it fires at the new
   time, or not at all.

### User story 3: Ask what is on the calendar now (Priority: P1)

A morning-briefing flow asks whether a "Vacation" or "Work from home" event is
in progress, and what is on the calendar for the rest of the day.

**Why this priority**: A flow that starts for another reason, such as a
schedule or a sensor, needs the calendar's current state, not only its edges.
**Independent test**: With one event in progress and two later today, the
lookup returns the first as in progress and the others as upcoming.

**Acceptance scenarios**:

1. **Given** a connection, **When** the lookup node runs, **Then** it returns
   events in progress now and upcoming events within a chosen window, each
   marked as which, with the same title and calendar filters as the trigger.
2. **Given** no matching event, **When** the lookup runs, **Then** it returns
   empty lists rather than failing.
3. **Given** an unreachable calendar with no list read in the last day,
   **When** the lookup runs, **Then** it fails with the reason, failing the
   node that pulled it, and a retry policy applies.

### User story 4: Keep existing calendars working after the upgrade (Priority: P2)

An author already has a calendar widget on the kitchen dashboard reading two
ICS feeds with their own names and colours. After the upgrade the widget
still shows both, now through two connections created for it.

**Why this priority**: Moving settings into connections must not break a
dashboard someone relies on.
**Independent test**: Seed a widget and a flow node with inline settings,
start the upgraded server, and confirm both read the same events through new
connections.

**Acceptance scenarios**:

1. **Given** nodes or widgets with inline feed settings, **When** the upgraded
   server starts, **Then** one connection is created for each distinct feed
   and credential, the nodes and widgets point at it, and their inline
   credentials are removed.
2. **Given** a widget showing several sources with names, colours, and tags,
   **When** it is migrated, **Then** each source keeps its name, colour, and
   tag and points at its connection.
3. **Given** the same feed used in several places, **When** migrating,
   **Then** they share one connection.
4. **Given** a migration that cannot create a connection, **When** the server
   starts, **Then** the node or widget keeps its inline settings, still works,
   and the failure is logged with what to fix.

### User story 5: Override a house mode from the calendar (Priority: P2)

The flow guide shows the vacation-mode pattern from story 2 built from the
calendar trigger, a select control, and the existing switch and control
nodes, and the documented flow works as written.

**Why this priority**: Modes and presence are flows the user builds, not
features, so the pattern has to be documented and tested.
**Independent test**: Build the documented flow and run it against a test
event.

**Acceptance scenarios**:

1. **Given** the documented flow, **When** the event starts and ends, **Then**
   the mode control changes to the override and back.
2. **Given** an override already active when another one starts, **When**
   both end, **Then** the documented flow restores the mode that was set
   before the first override.

### Edge cases

- An event with no end is treated as ending at its start, or at the end of
  its day when it is all-day.
- All-day events start and end at local midnight in the connection's time
  zone, or the server's when the calendar gives none.
- A feed that fails to refresh keeps the last good events for up to a day, so
  a short outage does not cancel a planned firing; firing from stale events is
  noted in the run.
- An event that starts and ends inside one refresh interval still fires both
  edges.
- Changing a trigger's edges, offsets, or filters does not re-fire
  occurrences that already fired.
- Catch-up after downtime looks back no further than seven days.
- An oauth2 connection that is signed out or expired makes the calendar
  connection report an error until it is signed in again.
- CalDAV servers that do not support listing calendars accept a calendar URL
  entered directly.
- Large feeds are bounded: a refresh reads at most a configured window ahead
  (default 60 days) and at most 5,000 occurrences.

## Requirements

### Functional requirements

- **FR-001**: The system MUST offer calendar connection kinds for ICS feeds
  (URL, optional basic auth or bearer token), CalDAV (server URL, username,
  password, selected calendars), and Google Calendar (calendar IDs with either
  an API key or a reference to an oauth2 connection).
- **FR-002**: Calendar credentials MUST be stored encrypted and masked
  wherever connections are read, and MUST NOT appear in flow or dashboard
  exports.
- **FR-003**: Testing a calendar connection MUST report the calendar names and
  the number of events in the next seven days, or the reason it failed.
  CalDAV and Google connections MUST be able to list the account's calendars
  for selection.
- **FR-004**: Calendar nodes and the calendar widget MUST select a calendar
  connection instead of holding feed URLs or credentials. A widget source
  MUST keep its own name, colour, and tag.
- **FR-005**: The calendar trigger MUST fire at an event's start, end, or
  both, each with its own offset before or after, once per occurrence of a
  recurring event, honouring exceptions and cancellations.
- **FR-006**: The trigger MUST filter by title text, by calendar, and by
  whether an event is all-day.
- **FR-007**: The trigger MUST record each fired occurrence and edge so that
  neither a restart nor a redeploy fires it again, and MUST forget records
  for occurrences more than seven days past.
- **FR-008**: Occurrences whose moment came while the server was down MUST
  follow the trigger's missed-deadline setting (deliver late by default, or
  drop beyond a grace period), with the outcome in run history, as feature
  029 defines for pending waits.
- **FR-009**: Moved or deleted events MUST fire at their new time or not at
  all, from the next refresh on.
- **FR-010**: The calendar lookup MUST return events in progress and upcoming
  events within a chosen window, using the trigger's filters. It is a value
  source, so when the calendar cannot be read it MUST fail the node that
  pulled it, with the reason, and be retryable.
- **FR-011**: On upgrade, inline feed settings in nodes, deployed versions,
  drafts, and widgets MUST move into connections, one per distinct feed and
  credential, with the inline credentials removed. A source that cannot move
  MUST keep working and be logged.
- **FR-012**: The flow guide MUST document the calendar-driven house-mode
  override, including nested overrides.
- **FR-013**: The editor, REST, and MCP MUST expose the new connection kinds
  and node settings through the existing connection and node catalogs.

### Key entities

- **Calendar connection**: a source of events (ICS, CalDAV, or Google) with
  encrypted credentials and the calendars it reads.
- **Occurrence**: one instance of an event with its own start and end;
  recurring events have many.
- **Fired record**: an occurrence and edge a trigger has fired, kept until
  seven days after the occurrence.
- **Widget source**: a widget's reference to a calendar connection with a
  display name, colour, and tag.

## Success criteria

- **SC-001**: An event that starts in two minutes and ends in four produces
  exactly one start run and one end run, each within one minute of its moment
  plus offset, with a server restart between them.
- **SC-002**: With the server stopped across an event's start, the trigger
  delivers the start late or drops it according to its setting, and never
  both.
- **SC-003**: A weekly recurring event with one cancelled occurrence fires
  for every other occurrence in a three-week test feed.
- **SC-004**: A dashboard widget and a flow node configured with inline
  settings before the upgrade show the same events after it, and no flow
  export or connection read reveals a credential.
- **SC-005**: Each of the three connection kinds passes its connection test
  against a live or recorded server and lists upcoming events through the
  lookup node.
- **SC-006**: Relevant Makefile builds and tests pass, the new settings
  appear in the editor and widget panel without console errors, and the
  development service stays active.

## Assumptions and scope

- The calendar plugin keeps owning calendars; its connection kinds run inside
  the plugin like other plugin connection kinds.
- Reading only. Creating or editing calendar events is out of scope.
- Google private calendars use the existing oauth2 connection kind for sign-in;
  no new sign-in flow is built.
- The existing ICS parse and encode nodes are unchanged.
- The trigger polls; push notifications from calendar servers are out of
  scope.
- The house-mode pattern uses existing nodes. A dedicated mode feature is not
  part of this spec.

## References

- [Plugins](../008-plugin-system/spec.md)
- [Connections](../004-connections-devices/spec.md)
- [Durable waits and missed deadlines](../029-durable-waits/spec.md)
- [Flow guide](../../docs/flow-authoring.md)
- [Plugin guide](../../docs/plugins.md)
