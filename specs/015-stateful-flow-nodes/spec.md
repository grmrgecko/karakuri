# Feature specification: Stateful flow nodes

**Feature**: `specs/015-stateful-flow-nodes`
**Created**: 2026-10-06
**Status**: Implemented and verified
**Input**: Add Hysteresis and Condition wait; extend keyed timers, dedupe expiry/reset, Home Assistant targeting/confirmation, and HTTP retry controls. Condition wait also accepts value-change checks.

## User scenarios and testing

### User story 1: Use separate sensor thresholds (Priority: P1)

Avoid switching an actuator repeatedly around one threshold.

**Why this priority**: Sensor noise near a switching threshold can repeatedly start and stop an actuator.
**Independent test**: Feed readings across both thresholds and the band, reset state, and reload persisted state.

**Acceptance scenarios**:

1. Given inactive state and a reading at the activation threshold, when the reading arrives, then the active branch emits; readings within the band retain state without emitting.
2. Given active state, when the reading reaches the release threshold, then the inactive branch emits; independent keys never affect each other.

### User story 2: Wait for an observed condition (Priority: P1)

Wait on a live value without acting on a stale run snapshot.

**Why this priority**: A cached value cannot establish that a physical device reached its requested state.
**Independent test**: Wire a changing pure value source, observe stability and timeout, cancel a parked run, and wake concurrent waits from a change trigger.

**Acceptance scenarios**:

1. Given a wired live value, when its condition remains true at every observation for the configured hold duration, then matched emits the original message with the last observation.
2. Given a false condition or a condition that becomes false during its hold, when the deadline arrives, then timeout emits and no downstream matched action runs.
3. Given an active wait and a value-change trigger connected to `check`, when that trigger runs, then the wait obtains a fresh observation before its next poll and retains its original message and deadline.
4. Given several active waits on one node, when `check` arrives, then every wait checks independently; a check on another flow or node has no effect.
5. Given no active wait, when only `check` arrives, then no value source is read, no wait starts and no action emits. Check events do not consume the node's execute-once allowance.

### User story 3: Manage independent timers (Priority: P1)

Reuse one timer node for several devices without cross-device suppression.

**Why this priority**: Flows often share one timer across devices, so one device must not delay another.
**Independent test**: Interleave keyed rate/debounce/trigger messages and reset just one key.

**Acceptance scenarios**:

1. Given messages with different keys, when rate limiting, debouncing, or retriggering occurs, then each key has an independent schedule.
2. Given an armed key, when reset arrives for that key, then only that pending timer is cancelled.

### User story 4: Expire and reset duplicate history (Priority: P1)

Suppress repeats for a chosen interval and clear history deliberately.

**Why this priority**: Permanent history prevents periodic reminders and deliberately replayed events.
**Independent test**: Repeat an event before and at expiry, reload durable state, and reset selected history.

**Acceptance scenarios**:

1. Given a previously admitted value and an expiry duration, when that duration elapses, then the same value is admitted again.
2. Given duplicate history, when a reset-only message arrives, then history clears without emitting; duplicates may route through their own branch.

### User story 5: Target and confirm Home Assistant actions (Priority: P1)

Target groups explicitly and distinguish accepted commands from observed results.

**Why this priority**: Command acceptance alone does not establish that a device changed state.
**Independent test**: Use a protocol peer to inspect targets, change observed state, and force confirmation timeout.

**Acceptance scenarios**:

1. Given entity, area, device, or label targets, when a service runs, then the selected target lists reach Home Assistant.
2. Given an accepted service call, when an explicit confirmation entity reaches the expected state or attribute, then out emits; when it does not, timeout emits without resending the call.
3. Given a state trigger with a hold duration, when the watched value changes away before expiry, then the pending event is cancelled independently per entity.

### User story 6: Retry HTTP requests intentionally (Priority: P1)

Handle temporary failures without repeating unsafe actions or permanent failures.

**Why this priority**: Unconditional retries can repeat commands after a lost response or amplify a permanent error.
**Independent test**: Use a local HTTP server for permanent failures, 429/503, Retry-After, unsafe methods and cancellation.

**Acceptance scenarios**:

1. Given default retry settings, when GET receives a temporary failure, then attempts are bounded and respect the response delay; permanent failures do not retry.
2. Given a mutating request without explicit retry opt-in, when a failure occurs, then one attempt reaches the server.
3. Given an exhausted HTTP failure, when an error branch is wired, then structured response status, bounded body and safe headers accompany the failure.

### Edge cases

- Invalid thresholds, negative new hold/expiry/maximum-wait durations, missing key properties, nonboolean conditions and invalid target/retry settings fail with context before actions.
- Cancellation prevents delayed emissions; waits release workers. Restart and redeploy recovery of pending Trigger, Delay, and Condition wait state follows [feature 029](../029-durable-waits/spec.md).
- Condition stability means stability at observation times; polling and coalesced change signals cannot detect a transient between observations. External change triggers obey normal run admission; polling still works when a change run is dropped.
- Concurrent runs claim state atomically. Reset-only dispatch never emits an action.
- Retry-After parsing accepts seconds and HTTP dates; invalid values use normal backoff, and configured caps bound server-requested delays.
- Confirmation checks one explicit entity even when the command targets a group.
- HTTP pagination retries restart from the first page, with no partial successful output.

## Requirements

### Functional requirements

- **FR-001**: Hysteresis MUST support high-active and low-active thresholds, independent keys, durable remembered state, reset, and active/inactive outputs on state transitions only.
- **FR-002**: Condition wait MUST repeatedly obtain a fresh wired pure value, evaluate a boolean condition, support an observed hold duration and bounded timeout, and emit original-message matched/timeout branches with observation metadata.
- **FR-003**: Explicit fresh reads MUST refresh the whole value dependency chain without changing ordinary per-run value memoization or reusing old message-path outputs.
- **FR-004**: Delay rate/debounce and Trigger MUST support independent state keyed by an optional message path; omitted keys MUST preserve existing single-state behavior.
- **FR-005**: Trigger MUST support reset of a selected key; Delay MUST support keyed reset, debounce maximum wait, and dropped/superseded routing. Restart recovery of pending arms follows feature 029.
- **FR-006**: Dedupe MUST support expiry measured from admission, reset-only dispatch, and duplicate routing. Omitted expiry MUST retain existing history semantics and remembered entries.
- **FR-007**: HA service calls MUST accept interpolated entity, area, device, and label target lists.
- **FR-008**: HA service confirmation MUST use fresh reads of one explicit entity/state or attribute, a bounded timeout, and optional stability duration; confirmation timeout MUST NOT retry the accepted call.
- **FR-009**: HA State MUST support held-for filtering, invalidate pending emissions on contrary value changes, and isolate entities. Disarm MUST cancel pending waits.
- **FR-010**: HTTP requests MUST distinguish retryable transport/response failures from permanent configuration/response failures, default to safe-method retries, and allow explicit opt-in for other methods and selected statuses.
- **FR-011**: HTTP retries MUST honor bounded Retry-After delays, support optional jitter, retain attempt limits and cancellation, and expose bounded safe response details on error branches.
- **FR-012**: Catalog metadata, strict validation, browser fields/ports, REST and MCP MUST agree. Existing secrets, database records, enrolled identities, and durable filter/dedupe/rate state MUST remain readable.
- **FR-013**: Condition wait MUST expose optional `check` input. Check-only messages MUST request fresh observations for all active waits on that flow/node without evaluating value dependencies themselves, starting waits, emitting actions, claiming execute-once, or changing the original message, stability rules or absolute deadline. Polling MUST remain active. Completion, failure and cancellation MUST remove only the corresponding wait.
- **FR-014**: Event-driven waits MUST release workers while parked and reacquire a worker before observing again. Repeated pending signals MAY coalesce; false observations MUST restart stability.

### Key entities

- Hysteresis state: durable active flag owned by one flow/node/key.
- Timer arm: keyed wait and message owned by a live execution, stored as a pending wait under feature 029.
- Duplicate entry: admitted value and optional durable admission timestamp.
- Fresh observation: a newly evaluated pure value chain with an isolated cache.
- Condition waiter: flow/node-scoped wake subscription with an execution-owned message and deadline; its message and deadline are stored under feature 029.
- HTTP failure: retry classification, optional minimum delay, and bounded response details.

## Success criteria

- **SC-001**: Threshold fixtures emit exactly one transition at each crossing and none within the band.
- **SC-002**: A changing value chain matches using new observations, times out when required, and retains ordinary cached values for other consumers.
- **SC-003**: Interleaved keys and selected resets leave other timers unaffected.
- **SC-004**: Expired duplicate keys pass at the boundary and survive a durable JSON round trip.
- **SC-005**: Protocol tests inspect all HA target keys and establish no extra command on confirmation timeout.
- **SC-006**: HTTP peer request counts establish retry defaults, opt-in, delays, caps and structured failures.
- **SC-007**: Relevant Makefile builds/tests pass, new settings update live in the browser without console errors, and the development service stays active.
- **SC-008**: Change-trigger checks wake concurrent waits before their polling interval, preserve message/deadline/stability behavior, and perform no reads or emissions without an active wait.

## Assumptions and scope

- Changes apply to server nodes, not agent node catalogs. No external account setup or live household action is needed for verification.
- Key paths select values on incoming messages; omitted keys use a single bucket. Existing stored graphs remain loadable.
- HTTP default retry behavior intentionally becomes more conservative for methods with side effects. Authors may opt in when endpoint semantics permit repeating the request.
- Queued run policies, watchdogs, and other review proposals remain out of scope. Durable pending waits are specified in feature 029.

## References

- [Design plan](plan.md)
- [Flow guide](../../docs/flow-authoring.md)
- [Execution](../003-flow-execution/spec.md)
- [Connections](../004-connections-devices/spec.md)
- [Durable waits](../029-durable-waits/spec.md)

## Implementation evidence

All six stories are implemented on the server node catalog. See
[quickstart.md](quickstart.md#verification-evidence-2026-10-06) for Makefile,
MCP, HTTP/WebSocket and browser checks. Durable formats remain readable and no
schema, secret or identity migration was needed. In-flight timers remain transient;
condition and confirmation stability describe observed samples.

The US2 extension accepts change-trigger checks while retaining polling. Real
engine runs with SQLite controls verify broadcast, cancellation ownership,
stability resets, deadline preservation and execute-once with one worker. MCP
and browser evidence appears in the event-driven section of quickstart.md.
