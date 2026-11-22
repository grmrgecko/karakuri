# Feature specification: Event log

**Feature**: `specs/037-event-log`
**Created**: 2026-11-05
**Status**: Implemented
**Input**: Keep one searchable log of what happened in the house and in
karakuri: who or what changed a control or commanded a device (a person on a
dashboard, an API client, or a flow and its run), devices going offline and
back, connections dropping and recovering, flows deployed, enabled, and
failing, agents connecting and disconnecting, backups, and the security
events an administrator must be able to review (sign-ins, failed sign-ins,
API keys, users, groups, roles, and permission changes, backup downloads and
restores). Show it in a page with filters and live updates, on dashboards,
and to flows and MCP, keep it for a set time, and let flows add their own
entries.

## User scenarios and testing

### User story 1: What happened last night (Priority: P1)

The porch light was on at 3 a.m. The owner opens **Activity**, filters to the
porch light, and sees that the Motion lights flow turned it on at 02:58 after
the porch sensor reported motion, and that nobody switched it off. Another
time the entry says it was Sam, from the hallway dashboard.

**Why this priority**: Without this, the only way to answer "why did that
happen" is to read run history flow by flow, which does not cover dashboard
or API changes at all.
**Independent test**: Change a control from a dashboard, from a flow, and from
the API, and command a device; the log shows each with what made it, and a
filter to that control shows only those.

**Acceptance scenarios**:

1. **Given** a control changed by a flow, **When** the log is opened, **Then**
   the entry shows the control, the old and new value, the flow by name with a
   link to the run, and the time.
2. **Given** a control changed from a dashboard or the API, **When** the log
   is opened, **Then** the entry names the user (or API key) and, for a
   dashboard, the dashboard.
3. **Given** a command sent to a device, **When** the log is opened, **Then**
   the entry shows the device, the command, and what sent it, in the same
   terms as a control change.
4. **Given** a device that stops reporting and later returns, **When** the log
   is opened, **Then** it shows when it went offline and when it came back.
5. **Given** the log page is open, **When** something happens, **Then** the
   entry appears without reloading.

---

### User story 2: Review security events (Priority: P1)

An administrator checks who signed in this week, sees three failed sign-ins
for `admin` from an address they do not know, and sees that an API key was
created yesterday and by whom.

**Why this priority**: Karakuri holds credentials for every connected system;
an administrator needs to know who got in and what they changed.
**Independent test**: Sign in, fail a sign-in, create and delete an API key,
change a user's role, and download a backup; each appears in the security
category with the actor and address, and a user without the audit permission
does not see them.

**Acceptance scenarios**:

1. **Given** a sign-in, successful or failed, **When** the log is filtered to
   security, **Then** the entry shows the username tried, the outcome, the
   address, and the client.
2. **Given** a change to users, groups, roles, permissions, API keys,
   sessions revoked, connections' credentials, or the encryption key,
   **When** the log is filtered to security, **Then** the entry names who made
   it and what changed, without any secret value.
3. **Given** a backup downloaded or restored, **When** the log is filtered to
   security, **Then** the entry names the archive and who did it; a restore
   from the command line is recorded when the restored server starts.
4. **Given** a user without the audit permission, **When** they open the log,
   **Then** security entries are not shown to them, and nothing indicates
   which were hidden.

---

### User story 3: Notice what is failing (Priority: P2)

The owner filters the log to problems and sees that the garage MQTT
connection dropped twelve times today, a flow failed four times with the same
error, and the nightly backup failed.

**Why this priority**: Failures are spread across connection status, run
history, and backup runs; one view makes patterns visible.
**Independent test**: Cause a connection to drop and recover, a flow to fail,
and a backup to fail; each appears with level warning or error, and a filter
by level shows exactly those.

**Acceptance scenarios**:

1. **Given** a connection that drops and reconnects, **When** the log is
   opened, **Then** both transitions appear with the error the connection
   reported.
2. **Given** a flow run that fails, **When** the log is opened, **Then** an
   entry names the flow, the node, and the error, linked to the run.
3. **Given** a flow deployed, enabled, or disabled, an agent connecting or
   disconnecting, a server start or stop, or a backup finishing, **When** the
   log is opened, **Then** each has an entry.
4. **Given** a failure that repeats, such as a connection retrying every few
   seconds, **When** the log is opened, **Then** repeats within a minute are
   folded into one entry with a count rather than flooding the log.

---

### User story 4: Flows and dashboards (Priority: P2)

A flow writes "Holiday mode on" to the log when the family leaves. A tablet in
the kitchen shows the last ten events about the doors. A flow sends a
notification when a failed sign-in is logged.

**Why this priority**: The log is most useful where people already look and
where automations already react.
**Independent test**: Add an entry from a flow, show a filtered list on a
dashboard, and trigger a flow from a logged event.

**Acceptance scenarios**:

1. **Given** a flow with a Log Event node, **When** it runs, **Then** an entry
   appears with the message, level, and subject the node set, attributed to
   the flow and run.
2. **Given** a dashboard with an activity list filtered to some controls and
   devices, **When** those change, **Then** the list updates live, and viewers
   of a shared dashboard see only the entries for things the dashboard shows.
3. **Given** a flow with an Event Logged trigger filtered by category, level,
   or subject, **When** a matching entry is logged, **Then** the flow runs with
   the entry; a flow's own Log Event entries do not trigger that same flow.

---

### User story 5: Keep the log a sensible size (Priority: P3)

The owner keeps 30 days of house activity and a year of security events, and
the log never grows without bound however busy the house is.

**Why this priority**: A sensor house produces many changes a day; an unbounded
log slows the server and fills the disk.
**Independent test**: Set short retention, generate entries, and check old
entries are removed on schedule and the row limit holds.

**Acceptance scenarios**:

1. **Given** retention per category, **When** entries pass their age, **Then**
   they are removed within a day.
2. **Given** a burst beyond the row limit, **When** it is reached, **Then** the
   oldest non-security entries are removed first.

### Edge cases

- Sensor readings are not events: a temperature device reporting every few
  seconds does not fill the log. Device state changes are logged for
  availability and for commands sent; a control is logged on every change, but
  a control can be marked as not logged, and controls that only mirror a
  sensor can be excluded the same way.
- Home Assistant entity changes are logged only for entities the owner lists,
  since an installation may track thousands.
- Secrets never appear: passwords, tokens, keys, and connection credentials are
  not logged, and a failed sign-in records the username tried but never the
  password.
- A change made while the log cannot be written, such as a full disk, still
  takes effect; the log write failure is itself logged to the server log.
- Times are stored in UTC and shown in the viewer's local time.
- An entry about something later deleted keeps its name as it was, with the
  link removed.
- Restoring a backup restores the log with the database, and the restore itself
  is the first entry of the restored installation.
- A flow loop that writes a control many times a second is folded like other
  repeats, so the log does not amplify it.

## Requirements

### Functional requirements

- **FR-001**: The system MUST record entries with time, category (`device`,
  `connection`, `flow`, `agent`, `security`, `system`, `custom`), level
  (`info`, `warning`, `error`), a subject (the control, device, connection,
  flow, agent, user, or archive concerned, by kind, ID, and name), a short
  message, structured details, and an actor (user, API key, flow and run,
  dashboard, agent, or the system), with the client address for actions made
  over the network.
- **FR-002**: Every control change and device command MUST be recorded with
  its actor, including the user and dashboard for dashboard writes and the
  user or API key for API and MCP writes; a control can be excluded from
  logging.
- **FR-003**: Device availability changes, connection status changes, flow
  deploys, enables, disables, and failed runs, agent connects, disconnects,
  enrollments, and revocations, server starts and stops, and backup results
  MUST be recorded.
- **FR-004**: Sign-ins and failed sign-ins, sign-outs and revoked sessions, and
  every change to users, groups, roles, permissions, API keys, connection
  credentials, the encryption key, backup downloads, and restores MUST be
  recorded in the security category, without secret values.
- **FR-005**: Home Assistant entity state changes MUST be recorded only for
  entities listed in the log settings.
- **FR-006**: Identical entries (same category, subject, level, and message)
  within one minute MUST be folded into one entry with a count and the last
  time.
- **FR-007**: The system MUST offer an Activity page listing entries newest
  first with filters by time range, category, level, subject, actor, and text,
  updating live while open.
- **FR-008**: Reading the log MUST require an events read permission; security
  entries MUST additionally require an audit permission, held by
  administrators by default.
- **FR-009**: A dashboard activity widget MUST show entries filtered by subject,
  category, and level, live, and to viewers of a shared dashboard only entries
  whose subjects the dashboard shows; security entries are never shown on a
  dashboard.
- **FR-010**: A Log Event flow node MUST add a `custom` entry with message,
  level, optional subject, and details, attributed to the flow and run; an
  Event Logged trigger MUST start flows on entries matching category, level,
  and subject, and MUST NOT fire a flow on its own Log Event entries.
- **FR-011**: Retention MUST be set per category in days (default 30, and 365
  for security) with an overall row limit (default one million) enforced by
  removing the oldest non-security entries first; removal runs at least daily.
- **FR-012**: REST and MCP MUST offer listing entries with the same filters
  and permissions as the page.
- **FR-013**: Writing an entry MUST NOT block or fail the action it describes.

### Key entities

- **Event**: time, category, level, subject kind, ID, and name, message,
  details, actor kind, ID, and name, client address, repeat count, and last
  time.
- **Log settings**: retention per category, row limit, controls excluded, and
  Home Assistant entities included.

## Success criteria

- **SC-001**: For a control changed by a dashboard user, an API key, and a
  flow, the log names each actor correctly.
- **SC-002**: A failed sign-in appears in the security category within one
  second, and a user without the audit permission sees no security entries
  through the page, the API, MCP, or a dashboard.
- **SC-003**: A connection that fails 100 times in a minute produces at most a
  handful of entries, each with its count.
- **SC-004**: Listing the newest 100 entries with a filter answers in under
  200 ms with a million entries stored.
- **SC-005**: A temperature sensor reporting every second for an hour adds no
  entries.
- **SC-006**: No logged entry contains a password, token, key, or connection
  credential.

## Assumptions and scope

- The log is karakuri's own record in its database; forwarding entries to an
  external log system (syslog, Loki) is out of scope.
- Flow run history stays where it is; the log links to runs rather than
  copying node-level detail.
- The server's own operational log file is unchanged and remains the place for
  debugging detail.
- Device attribute changes beyond availability are not logged by default;
  per-device opt-in can follow if wanted.
- The events read permission is granted to the User role by default, so
  household members see house activity; audit stays with administrators.

## References

- [Feature 001: server security](../001-server-security/spec.md)
- [Feature 004: connections and devices](../004-connections-devices/spec.md)
- [Feature 003: flow execution](../003-flow-execution/spec.md)
- [Feature 009: remote agents](../009-remote-agents/spec.md)
- [Feature 036: backup and restore](../036-backup-restore/spec.md)
