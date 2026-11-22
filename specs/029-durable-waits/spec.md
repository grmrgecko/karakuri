# Feature specification: Durable waits and device state

**Feature**: `specs/029-durable-waits`
**Created**: 2026-10-30
**Status**: Implemented
**Input**: Pending waits in trigger, delay, wait until, condition wait, and the
fan-in nodes are stored in the database, kept current on reset, extend, and
cancel, and removed when they fire. On startup the engine re-arms waits whose
deadline is still ahead. Waits that came due while the server was down follow a
per-node setting: deliver late (the default) or drop, recorded in run history
either way. A redeploy keeps a wait whose node still exists with the same ID and
drops one whose node was removed. MQTT devices persist their last reported state
and restore it at startup, marked stale until the device reports again. Control
values already persist and need no change.

## User scenarios and testing

### User story 1: Keep a timer across a restart (Priority: P1)

An author builds a flow that sets the house mode control to `guest` and uses a
Trigger node to set it back to `home` four hours later unless a reset arrives.
The server restarts an hour in for an update. Three hours later the mode goes
back to `home`, as it would have without the restart.

**Why this priority**: Long timers are the ones a restart is most likely to
interrupt, and they usually restore a state the household relies on. Losing one
silently leaves the house in the wrong mode with no record of why.
**Independent test**: Arm a Trigger with a short wait, restart the service before
it fires, and confirm the delayed message emits at the original deadline and
the downstream control changes.

**Acceptance scenarios**:

1. **Given** an armed Trigger whose deadline is still ahead, **When** the server
   restarts, **Then** the delayed message emits at the original deadline, not at
   the original wait measured from the restart.
2. **Given** an armed Trigger, **When** a reset for its key arrives after the
   restart and before the deadline, **Then** nothing emits, the same as before
   a restart.
3. **Given** an armed Trigger set to extend on retrigger, **When** a new message
   arrives after the restart, **Then** the restored deadline moves out and one
   delayed message emits.
4. **Given** a keyed Trigger with three armed keys, **When** the server restarts
   and one key is reset, **Then** the other two keys still emit at their own
   deadlines.

### User story 2: Decide what happens to a missed deadline (Priority: P1)

The server was down past a wait's deadline. For "set the mode back to home" the
author wants the message sent as soon as the server is back. For "turn the porch
light off at 23:00" sent at 07:00 the next morning, the author would rather drop
it.

**Why this priority**: No single answer suits every flow. Late delivery restores
state correctly; a late action at the wrong time of day can be worse than none.
**Independent test**: Arm two waits, one configured to deliver late and one to
drop, stop the service past both deadlines, start it, and compare what emitted
and what run history records.

**Acceptance scenarios**:

1. **Given** a wait set to deliver late whose deadline passed while the server
   was down, **When** the server starts, **Then** the message emits once and the
   run records the original deadline and how late it was.
2. **Given** a wait set to drop missed deadlines, **When** the server starts
   after its deadline, **Then** nothing emits and the run history records the
   dropped message with its original deadline.
3. **Given** a node with no explicit setting, **When** its deadline is missed,
   **Then** it delivers late.
4. **Given** a wait set to drop, **When** the server starts slightly after its
   deadline but within the node's grace period, **Then** the message emits as
   on time.

### User story 3: Redeploy without losing pending work (Priority: P1)

An author fixes a typo in a notification node and redeploys the flow while a
four-hour Trigger elsewhere in the same flow is armed. The armed Trigger still
fires on time. A Trigger the author deleted in the same edit does not.

**Why this priority**: Redeploying today clears every pending timer in the flow,
which turns ordinary edits into a reason not to touch a running automation.
**Independent test**: Arm two waits in one flow, delete one node, redeploy, and
confirm only the remaining node emits.

**Acceptance scenarios**:

1. **Given** an armed wait whose node keeps its ID and type in the new
   deployment, **When** the flow is redeployed, **Then** the wait keeps its
   deadline and message and emits through the new deployment's downstream nodes.
2. **Given** an armed wait whose node was removed or changed type, **When** the
   flow is redeployed, **Then** the wait is dropped and run history records why.
3. **Given** an armed wait whose node changed its duration, **When** the flow is
   redeployed, **Then** the existing deadline stands; the new duration applies to
   the next message.
4. **Given** an armed wait whose emitting output no longer exists or is no longer
   wired, **When** the deadline arrives, **Then** the wait ends without emitting,
   the same as any message sent to an unwired output.
5. **Given** armed waits in a flow, **When** the flow is disabled or deleted,
   **Then** its pending waits are dropped and run history records why.

### User story 4: See and cancel what is pending (Priority: P2)

An operator wants to know why the lights will turn off at 02:00 and stop it
without editing the flow.

**Why this priority**: A timer that outlives restarts and redeploys must be
visible, or it becomes behavior nobody can explain.
**Independent test**: Arm waits on several nodes, list them through the editor
and MCP, cancel one, and confirm only the others emit.

**Acceptance scenarios**:

1. **Given** armed waits in a flow, **When** an operator inspects the flow's
   state, **Then** each wait shows its node, key, kind, deadline, missed-deadline
   setting, and the run that armed it.
2. **Given** an armed wait, **When** an operator with permission to cancel runs
   cancels it, **Then** it ends without emitting and run history records who
   cancelled it.

### User story 5: Show the last known device state after a restart (Priority: P2)

After a server restart, the dashboard shows the living room thermostat's last
reported temperature and setpoint instead of blank values, marked as not yet
confirmed. When the thermostat next reports, the mark clears.

**Why this priority**: Many MQTT devices report only on change, so after a
restart a dashboard can sit empty for hours. A stale value with its age is more
useful than nothing, as long as it is labelled.
**Independent test**: Feed a device state message, restart the service, confirm
the dashboard and MCP show the stored values marked stale with their report
time, then publish a new message and confirm the mark clears.

**Acceptance scenarios**:

1. **Given** an MQTT device that reported state before the restart, **When** the
   server starts, **Then** the device shows those values marked stale with the
   time they were last reported.
2. **Given** a stale device, **When** a state message arrives and its script
   merges new fields, **Then** the device is no longer stale and the merged
   state replaces the stored one.
3. **Given** a stale device, **When** a flow or dashboard reads its state,
   **Then** the read includes whether the value is stale and when it was last
   reported.
4. **Given** a device whose definition was deleted, **When** the server starts,
   **Then** no stored state is restored for it.

### Edge cases

- Deadlines are wall-clock times, so a clock change moves when a wait fires. A
  deadline counts as missed only when it came due while the server was down.
- The server restores waits before it arms triggers, so a reset or retrigger
  that arrives right after startup sees the restored state.
- Several waits restored late are delivered in deadline order. Late delivery does
  not bypass the flow's concurrent-run limit; a restored delivery that cannot
  start waits for room and is never silently dropped.
- A wait is claimed before it emits. A crash at that moment can lose the one
  message being delivered but never sends it twice. Normal shutdown loses
  nothing.
- A wait whose message cannot be stored, because it exceeds the stored-message
  size limit or the database write fails, still runs in memory. The run records
  that it is not durable, and the failure is logged.
- Condition wait restores its original message, deadline, and polling. Its hold
  duration restarts from the first observation after restart because stability
  during downtime cannot be observed.
- Fixed and random Delay keep their existing reset behavior: reset does not
  cancel them, restored or not.
- Rate-limit queues restore their queued messages in order and release them at
  the configured rate from the restart. With drop configured, queued messages
  whose turn passed while the server was down are dropped.
- Debounce restores its latest pending message and quiet-period deadline. Its
  maximum-wait window keeps its original start.
- Join, batch, and aggregate keep their collected parts and their timeout. A
  group completed by count after the restart emits every part, including those
  collected before it.
- Restoring a wait whose flow no longer has a deployed version drops it and
  records why.
- A wait held by a node inside a subflow is keyed by the node's
  subflow-qualified ID and restores like any other wait.
- Each wait is checked against the part of the deployment its node's branch
  runs. A canvas fragment no trigger reaches does not block restoring it. A
  wait whose own branch cannot be expanded, such as one placing a shared
  subflow that no longer resolves, stays stored for a deployment that can run
  it.
- A device that reports constantly does not write to the database on every
  message; a crash can lose the most recent few seconds of its state.
- Changing a device's control kind drops its stored state, because the stored
  values may not fit the new kind.

## Requirements

### Functional requirements

- **FR-001**: Trigger, Delay (fixed, random, debounce, and queued rate limit),
  Wait until, Condition wait, Join, Batch, Aggregate, and the sequence modes of
  Limit and Sort MUST store each pending
  wait with its flow, node, key, absolute deadline, held message or collected
  parts, retrigger state, and arming run.
- **FR-002**: Reset, extend, restart, cancel, supersede, and completion MUST
  update or remove the stored wait so that a wait restored after a restart
  behaves the same way an uninterrupted wait would.
- **FR-003**: On startup, before triggers are armed, the server MUST restore
  every stored wait that belongs to an enabled, deployed flow whose deployment
  still contains a node of the same ID and type. A restored wait with a future
  deadline MUST fire at that deadline.
- **FR-004**: A restored wait MUST deliver through the node's output into the
  current deployment as a new run linked to the arming run. Message contents and
  message-scoped variables MUST be restored; run-scoped variables and other
  branches of the original run MUST NOT be.
- **FR-005**: Each node listed in FR-001 MUST offer a missed-deadline setting
  with the values deliver late (default) and drop, and a grace period within
  which a missed deadline counts as on time.
- **FR-006**: Late delivery and dropped deadlines MUST be recorded in run
  history with the original deadline and how late the restore was.
- **FR-007**: A redeploy MUST keep waits whose node keeps its ID and type,
  keeping their original deadlines, and MUST drop and record waits whose node
  was removed or changed type. Disabling or deleting a flow MUST drop its waits
  and record why.
- **FR-008**: A wait MUST be claimed before it emits, so a crash cannot deliver
  it twice. Normal shutdown MUST preserve every pending wait.
- **FR-009**: A wait that cannot be stored MUST continue in memory, and its run
  MUST record that it will not survive a restart. Storage failures MUST be
  logged at error.
- **FR-010**: The editor, REST, and MCP flow state inspection MUST list pending
  waits with node, key, kind, deadline, missed-deadline setting, and arming run.
  Cancelling a pending wait MUST require the same permission as cancelling a run
  and MUST be recorded in run history.
- **FR-011**: The server MUST persist the last merged state of each MQTT device
  with the time it was reported, coalescing frequent reports, and restore it on
  startup marked stale.
- **FR-012**: A device MUST stop being stale when its state script merges a new
  report. Dashboard, flow, REST, and MCP reads of device state MUST include the
  stale flag and the last report time.
- **FR-013**: Deleting an MQTT device or changing its control kind MUST discard
  its stored state.
- **FR-014**: Existing flow, global, and durable node stores, control values,
  secrets, and enrolled identities MUST remain readable. New storage MUST NOT
  require changing or migrating existing records.

### Key entities

- **Pending wait**: one node's held work for one key in one flow: deadline,
  message or collected parts, retrigger state, arming run, and missed-deadline
  setting. Created when a wait arms and removed when it fires, is cancelled, or
  is dropped.
- **Restored delivery**: the run a restored wait starts, linked to the run that
  armed it and recording whether it was late or dropped.
- **Device state record**: an MQTT device's last merged state and the time it
  was reported; stale from restore until the next report.

## Success criteria

- **SC-001**: A Trigger armed for two minutes and interrupted by a restart after
  30 seconds emits within five seconds of its original deadline.
- **SC-002**: With two waits past their deadlines across a restart, the
  deliver-late node emits once and the drop node emits nothing; both appear in
  run history with their original deadlines.
- **SC-003**: Redeploying a flow with two armed waits after deleting one node
  leaves exactly one delivery.
- **SC-004**: One hundred keyed waits across five nodes survive a restart with
  each key emitting once at its own deadline and none emitting twice.
- **SC-005**: A device that reported before a restart shows its values marked
  stale within ten seconds of startup and clears the mark on its next report.
- **SC-006**: A development database from before this feature starts without
  manual steps, and its existing variables, durable node state, and control
  values read back unchanged.
- **SC-007**: Relevant Makefile builds and tests pass, the new node settings
  appear and save in the editor without console errors, and the development
  service stays active.

## Assumptions and scope

- Waits started by flow tests of drafts are not stored; tests run against an
  undeployed graph and have nothing to restore into.
- A restart does not resume the original run. The delayed message starts a new
  run at the wait node, so anything the original run would have done after the
  wait on another branch does not happen.
- Waits stored by a server version without this feature do not exist; the first
  restart after upgrading restores nothing.
- Home Assistant state holds (`held for`) and confirmation waits stay transient.
  They depend on live entity state, which Home Assistant resends on reconnect.
- Control values already persist across restarts under feature 004 and need no
  change. Home Assistant entity state is not stored, because Home Assistant
  resends it on reconnect.
- Waits inside script nodes, browser prompts, HTTP pagination, and ping are not
  covered. Their state belongs to an external exchange that cannot be resumed.
- Device availability (online and offline detection) is out of scope. The stale
  flag is what that later work will build on.

## References

- [Flow guide](../../docs/flow-authoring.md)
- [MQTT device state scripts](../../docs/mqtt-device-state-script.md)
- [Execution and persistent state](../003-flow-execution/spec.md)
- [Connections, MQTT devices, and controls](../004-connections-devices/spec.md)
- [Stateful flow nodes](../015-stateful-flow-nodes/spec.md)
