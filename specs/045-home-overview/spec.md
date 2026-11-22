# Feature specification: Home and overview

**Feature**: `specs/045-home-overview`
**Created**: 2026-11-21
**Status**: Implemented 2026-11-21
**Input**: Home screen and attention overview. Move the flow list from `/` to
`/flows`. Make `/` a per-user home: a start page preference (the overview or a
specific dashboard); users who cannot read flows default to their dashboards,
or to their only dashboard. The overview shows, each part gated by permission:
needs attention (connections down or flapping, agents offline, flows failing
grouped by repeated error and linked to the run, a backup failed or overdue,
history forwards failing), collapsing to one all-clear line; coming up
(schedule triggers in the next hours and pending durable waits, with cancel);
running now (active runs); unfinished work (flows and dashboards with
undeployed draft changes); quick launch (pinned dashboards, a per-user pin
list); and recent problems (the latest warning and error events, linked to
Activity). The attention list is computed on the server and offered through
the API and MCP so the browser and assistants share it. No object counts,
run-volume charts, or user-arranged home layouts.

This feature reads state that other features own: connection status
([004](../004-connections-devices/spec.md)), run history and active runs
([003](../003-flow-execution/spec.md)), agents
([009](../009-remote-agents/spec.md)), history forwards
([027](../027-timeseries-connections/spec.md)), durable waits
([029](../029-durable-waits/spec.md)), backups
([036](../036-backup-restore/spec.md)), the event log
([037](../037-event-log/spec.md)), and flow access
([043](../043-flow-sharing/spec.md)). It changes none of their rules.

## User scenarios and testing

### User story 1: See what is broken (Priority: P1)

The owner opens karakuri in the morning. The page at `/` says the garage MQTT
connection has been in error for 40 minutes, the desk agent is offline since
02:10, and "Porch lights" failed 4 times since yesterday with the same Home
Assistant timeout. Each line links to where it is fixed: the connection's
settings, the agent's page, the flow's latest failed run. On a day when
nothing is wrong the section is one line saying so.

**Why this priority**: Failures today are spread across connection settings,
agent settings, run history, backups, and the event log. A reliability-focused
engine needs one place that says whether everything is working.
**Independent test**: Stop a connection's broker, disable a flow's target so
its run fails twice with one error, and open `/`; both appear with working
links. Restore them and the section reads as all clear.

**Acceptance scenarios**:

1. **Given** a connection in the error state, **When** the overview loads,
   **Then** an error item names the connection, its error, and how long it
   has been down, and links to its settings.
2. **Given** a connection that is connected now but went down three times in
   the last hour, **When** the overview loads, **Then** a warning item says it
   is unstable and how many times it dropped.
3. **Given** an enabled agent that is not connected, **When** the overview
   loads, **Then** an error item names it with when it was last seen.
4. **Given** a flow whose runs failed 4 times in 24 hours with one error and
   once with another, **When** the overview loads, **Then** two items appear
   for that flow, each with its count and a link to its newest failed run.
5. **Given** that flow's newest finished run succeeded, **When** the overview
   loads, **Then** its failure items show as warnings marked as recovered
   rather than errors.
6. **Given** the newest finished backup failed, or its verification failed,
   **When** the overview loads, **Then** an error item says so and links to
   backup settings.
7. **Given** an enabled backup schedule whose last scheduled time passed more
   than an hour ago with no successful backup since, **When** the overview
   loads, **Then** a warning says the backup is overdue.
8. **Given** an enabled history forward whose newest delivery failed,
   **When** the overview loads, **Then** a warning names it with the error.
9. **Given** none of the above, **When** the overview loads, **Then** one
   all-clear line states what was checked, such as "9 connections and 2
   agents online, no flow failures in 24 hours".

---

### User story 2: Land where I work (Priority: P1)

A household member who can only view dashboards signs in and lands on the
kitchen dashboard, not on a flow list they cannot use. The owner lands on the
overview. A family member who prefers the living room dashboard picks it as
their start page in their profile.

**Why this priority**: Today everyone lands on the flow list, which is wrong
for every user who does not build flows.
**Independent test**: Sign in as a user with dashboard read only and one
shared dashboard; `/` opens that dashboard. Set the admin's start page to a
dashboard; `/` opens it. Clear it; `/` shows the overview.

**Acceptance scenarios**:

1. **Given** a user who can read flows and has no start page set, **When**
   they open `/`, **Then** the overview draws.
2. **Given** a user who cannot read flows and can view exactly one dashboard,
   **When** they open `/` or sign in, **Then** that dashboard opens.
3. **Given** a user who cannot read flows and can view several dashboards,
   **When** they open `/`, **Then** the dashboard list opens.
4. **Given** a user whose start page is a dashboard they can view, **When**
   they open `/` or sign in, **Then** that dashboard opens.
5. **Given** a start page dashboard that was deleted or is no longer shared
   with the user, **When** they open `/`, **Then** the default for their
   permissions applies and the profile shows the start page as unavailable.
6. **Given** any signed-in user, **When** they open `/flows`, **Then** the
   flow list draws; the nav's Flows link and every "back to flows" link go
   there, and the Karakuri name goes to `/`.

---

### User story 3: Know what happens next (Priority: P2)

Before bed the owner checks that the heater flow fires at 05:30 and that the
porch light's sunset schedule is armed. A wait node holding "turn the fan off
in 2 hours" shows its deadline, and the owner cancels it from the overview.

**Why this priority**: Home automation runs on time; confirming what will run
tonight is a routine question the product cannot answer today without opening
each flow.
**Independent test**: Deploy and enable a flow with a schedule node firing in
an hour and a flow holding a durable wait; both appear in time order, and
cancelling the wait removes it.

**Acceptance scenarios**:

1. **Given** enabled, deployed flows with cron and sun schedule nodes, **When**
   the overview loads, **Then** each next firing within the next 12 hours
   appears with its time, the flow, and the node's label, soonest first.
2. **Given** a disabled flow with a schedule node, **When** the overview
   loads, **Then** it does not appear.
3. **Given** a pending durable wait with a deadline in the window, **When**
   the overview loads, **Then** it appears with its deadline, and a user who
   may cancel runs on that flow can cancel it there.
4. **Given** nothing scheduled in the window, **When** the overview loads,
   **Then** the section says nothing is scheduled in the next 12 hours.

---

### User story 4: Pick up where I left off (Priority: P2)

The owner edited two flows yesterday and saved drafts without deploying. The
overview lists them under unfinished work, with the dashboard they were
rearranging. Their three most-used dashboards sit as large tiles under quick
launch, pinned from the dashboard list.

**Why this priority**: Saved but undeployed drafts are easy to forget, and
the dashboards a person opens daily should be one click from home.
**Independent test**: Save a flow draft without deploying and pin a
dashboard; both appear on the overview, and deploying the flow or unpinning
the dashboard removes them.

**Acceptance scenarios**:

1. **Given** flows the user may edit whose draft differs from the deployed
   version or that were never deployed, **When** the overview loads,
   **Then** they are listed newest change first, each linking to the editor.
2. **Given** dashboards the user may edit with undeployed draft changes,
   **When** the overview loads, **Then** they are listed the same way,
   linking to the dashboard editor.
3. **Given** a user who pins dashboards from the dashboard list, **When** the
   overview loads, **Then** quick launch shows them in pin order; unpinning
   removes one.
4. **Given** a pinned dashboard the user can no longer view, **When** the
   overview loads, **Then** it is left out.
5. **Given** no pinned dashboards, **When** the overview loads, **Then** quick
   launch says how to pin one.

---

### User story 5: Watch what is running and what went wrong recently (Priority: P3)

While a long backup flow runs the overview shows it with its start time and a
cancel button. Below, the latest warnings and errors from the event log give
context: a device that went unavailable, a login that failed.

**Why this priority**: Running work and recent problems are visible on other
pages; the overview only needs a short view with links.
**Independent test**: Start a flow with a delay node; it appears under
running now and disappears when it ends. Cause a warning event; it appears
under recent problems.

**Acceptance scenarios**:

1. **Given** active runs of flows the user can view, **When** the overview
   loads, **Then** they appear with the flow, trigger, and start time, with
   cancel for users who may cancel runs.
2. **Given** no active runs, **When** the overview loads, **Then** the
   section is not shown.
3. **Given** warning and error events in the last 24 hours, **When** the
   overview loads, **Then** the newest eight the user may read appear, and a
   link opens Activity filtered to warnings and errors.

### Edge cases

- A user with none of the permissions behind a section never sees that
  section, including its empty or all-clear text.
- A user who can read flows but not connections sees flow failures only, and
  the all-clear line names only what they may see.
- Flow failures, waits, schedules, and runs of flows the user cannot view
  under flow sharing are left out, as they are elsewhere.
- A connection the user disabled is not an attention item; disabled is a
  choice, not a failure. A disabled agent is not offline.
- A connection still connecting two minutes after it last changed state is a
  warning; a shorter connect is normal startup.
- A run that failed but whose failure an error branch handled is not a
  failure item.
- A sun schedule with no event in the window, such as polar night, is
  omitted rather than shown as an error.
- A schedule node whose configuration fails to parse is omitted from coming
  up; deployment validation already refuses such graphs.
- When the server cannot read one source, such as an unreachable agent hub,
  the overview shows the other sections and names the source it could not
  check instead of claiming all clear.
- A start page preference naming a dashboard the user cannot see never
  reveals that dashboard's name.

## Requirements

### Functional requirements

- **FR-001**: The flow list MUST be served at `/flows`. The nav's Flows link,
  links back from the flow editor and share pages, and links that open the
  flow list MUST go there. The Karakuri name in the nav MUST go to `/`.
- **FR-002**: Opening `/`, and finishing sign-in without a requested
  destination, MUST resolve the user's start page: their chosen dashboard
  when they can view it; otherwise the overview when they can read flows;
  otherwise their only viewable dashboard; otherwise the dashboard list.
- **FR-003**: Each user MUST be able to choose their start page in their
  profile: the overview, a dashboard they can view, or the default. A chosen
  dashboard they can no longer view MUST be reported there as unavailable
  without its name.
- **FR-004**: Each user MUST be able to pin and unpin dashboards they can
  view, from the dashboard list and from quick launch, and reorder pins. Pins
  are private to the user.
- **FR-005**: The server MUST compute a needs-attention list for the caller
  containing, only for sources the caller may read:
  connections in the error state (error), connections connecting for more
  than two minutes (warning), connections that went down three or more times
  in the last hour (warning); enabled agents not connected (error); flows
  with failed runs in the last 24 hours, one item per flow and distinct
  error, with count, newest time, and newest run (error when the flow's
  newest finished run failed, otherwise warning marked recovered); the newest
  finished backup failed or failed verification (error); an enabled backup
  schedule more than an hour past a scheduled time with no successful backup
  since (warning); enabled history forwards whose newest delivery failed
  (warning); a newer release than the running one that a check found and this
  server has not installed, for users with `api:plugins:write` (warning), as
  [feature 048](../048-automatic-updates/spec.md) FR-014 defines.
- **FR-006**: Each attention item MUST carry its severity, a subject name, a
  message, when the condition began or last occurred, and a link target in
  the app where it is fixed or inspected. Items MUST order errors before
  warnings, then newest first.
- **FR-007**: The attention result MUST state which sources were checked and
  which could not be checked, so an empty list reads as all clear only for
  the sources checked.
- **FR-008**: The server MUST compute the caller's upcoming items for a
  window of 12 hours: the next firings of cron and sun schedule nodes in
  enabled, deployed flows, and pending durable waits whose deadline falls in
  the window, ordered by time, with flow, node label, and time.
- **FR-009**: The attention list and upcoming items MUST be available through
  the REST API and as MCP tools under the same permission rules.
- **FR-010**: The overview MUST show, in this order and each only when the
  user holds its permissions: needs attention; coming up; running now (only
  while runs are active); unfinished work; quick launch; recent problems.
- **FR-011**: Needs attention MUST collapse to one all-clear line when the
  list is empty, naming the checked sources, and MUST name any source that
  could not be checked.
- **FR-012**: Coming up MUST offer cancel on a wait when the user may cancel
  runs on its flow, using the existing wait cancellation.
- **FR-013**: Running now MUST list the user's viewable active runs with
  cancel for users who may cancel runs.
- **FR-014**: Unfinished work MUST list up to ten flows and ten dashboards
  the user may edit whose draft differs from the deployed version or that
  were never deployed, newest change first.
- **FR-015**: Recent problems MUST list the newest eight warning and error
  events of the last 24 hours the user may read and link to Activity with
  those levels selected.
- **FR-016**: The overview MUST refresh its sections without a page reload
  when a run the user may read starts or finishes, when a flow or dashboard
  list changes, and when a warning or error is logged that the user may read.
  It MUST also refresh while visible at least every 30 seconds, when the
  window regains focus, and after the live connection reconnects. Refreshes
  caused by live changes MUST be at least two seconds apart.
- **FR-017**: The overview MUST NOT show object counts other than those in
  attention text and the all-clear line, run-volume charts, or a layout the
  user arranges.
- **FR-018**: The flow list's status dot MUST follow each flow's runs as they
  happen, without a page reload: orange while any run of the flow is in
  progress, then green for success, yellow for success with handled
  failures, red for failure, and grey for cancellation, from the run that
  finished last.
- **FR-019**: The flow list MUST show flows and folders added, renamed,
  moved, enabled, shared, unshared, or removed in any session, by any path
  including MCP and import, without a page reload, and after the live
  connection reconnects. A user MUST hear only that a list changed; what
  appears follows their shares as the list API applies them.

### Key entities

- **Start page preference**: per-user; the overview, a dashboard ID, or
  unset.
- **Pinned dashboards**: per-user ordered list of dashboard IDs.
- **Attention item**: computed, not stored; severity, kind (connection,
  agent, flow, backup, history forward), subject ID and name, message,
  count, time, link target.
- **Upcoming item**: computed, not stored; kind (schedule or wait), flow,
  node, time, and for waits the wait ID.

## Success criteria

- **SC-001**: With one connection in error and one flow failing twice with
  one error, the overview shows exactly two attention items, each link
  opening the matching page, verified in the browser and through MCP.
- **SC-002**: With every connection connected, every enabled agent online,
  and no failures in 24 hours, the attention section is one line.
- **SC-003**: A user with only dashboard read and one viewable dashboard
  reaches that dashboard from sign-in without any extra click.
- **SC-004**: A schedule node set to fire in an hour appears under coming up
  at its time, and disabling the flow removes it on the next refresh.
- **SC-005**: Saving a flow draft without deploying adds it to unfinished
  work, and deploying removes it on the next refresh.
- **SC-006**: A user who cannot view a flow sees none of its failures,
  schedules, waits, or runs on the overview or through the MCP tools.

## Assumptions and scope

- Device availability (an MQTT or Zigbee device offline) is not an attention
  item in this feature: battery devices report offline routinely and would
  fill the list. The event log already records it, so recent problems shows
  it.
- Calendar event triggers are not in coming up; only schedule nodes and
  durable waits are.
- Having no backup schedule is not an attention item.
- The Settings overview of object counts is unchanged.
- The dashboard view still hides the app nav, as feature 006 states; a
  household user who lands on a dashboard reaches the app through that
  view's existing controls.
- Old bookmarks of `/` that expected the flow list now reach the start page,
  which links to the flow list; `/` cannot redirect because it is the start page.

## References

- [docs/flow-authoring.md](../../docs/flow-authoring.md)
- [docs/dashboard-authoring.md](../../docs/dashboard-authoring.md)
- [docs/events.md](../../docs/events.md)
- [docs/backup.md](../../docs/backup.md)
- [docs/mcp.md](../../docs/mcp.md)
- Features 003, 004, 009, 027, 029, 036, 037, and 043.
