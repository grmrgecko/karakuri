# Feature specification: Server utility nodes

**Feature**: `specs/016-server-utility-nodes`
**Created**: 2026-10-06
**Status**: Implemented and verified
**Input**: Add Time window, Connection status trigger, and Numeric transform to server flows.

## User scenarios and testing

### User story 1: Route events by local time (Priority: P1)

An author routes motion and other events differently during selected hours and weekdays.

**Why this priority**: A schedule starts a run but cannot classify an incoming event.
**Independent test**: Check clock and solar windows, overnight weekday ownership, and daylight saving transitions.

**Acceptance scenarios**:

1. Given a weekday window from 22:00 to 06:00, when a message arrives early the next morning, then it is inside the preceding weekday's window.
2. Given clock or solar endpoints, when a message arrives exactly at the start or end, then start is inside and end is outside.
3. Given a date without the required solar event, when a message arrives, then outside emits with a reason rather than inventing an event time.

### User story 2: React to integration status (Priority: P1)

An author detects connection failure and recovery without polling a device.

**Why this priority**: Silent input failures otherwise leave automation inactive without an actionable event.
**Independent test**: Use a real Home Assistant protocol peer, disconnect and reconnect it, disable the connection, and disarm the trigger.

**Acceptance scenarios**:

1. Given a connected integration and an armed trigger, when connectivity fails and recovers, then its state transitions emit with previous state and timestamps.
2. Given a disabled or failed-start connection, when initial emission is enabled, then its current status emits even though no driver runs.
3. Given a state filter, when a suppressed transition occurs before a selected transition, then the selected event still identifies its actual previous state.
4. Given a disarmed trigger, when the connection changes, then no further runs start.

### User story 3: Transform numeric readings (Priority: P1)

An author converts or calibrates sensor readings without writing a script.

**Why this priority**: Common numeric conversions should have inspectable settings and typed outputs.
**Independent test**: Transform scalar and nested readings, reversed ranges, unit conversions, clamping and rounding through the node execution contract.

**Acceptance scenarios**:

1. Given a reading with topic and metadata, when it is scaled or mapped, then the configured destination receives the result and other properties remain intact.
2. Given compatible units, when conversion runs, then the numeric value changes to the destination unit; incompatible units fail without emitting a result.
3. Given a missing or nonfinite reading, a zero-width mapping range, or numeric overflow, when execution is attempted, then the declared error route receives the failure.

### Edge cases

- Invalid times, unknown weekdays/timezones, missing coordinates, invalid solar offsets, invalid numeric settings, and missing connections fail with context.
- Clock boundaries use local wall time. Both occurrences of a repeated hour are classified alike; nonexistent clock minutes receive no events.
- Weekdays select the nominal start date. Solar offsets may move an endpoint into a neighboring date.
- Connection status queues are bounded. Overflow is reported and retains recent status; this is diagnostic automation, not an exactly-once event log.
- Driver replacement must not allow a retired driver's callbacks to overwrite the new status.
- Cancellation unregisters the status watcher. No state or in-flight execution is restored after restart.

## Requirements

### Functional requirements

- **FR-001**: Time window MUST preserve messages and route them through inside/outside using the current time or an explicit message timestamp, selected weekdays, and an optional IANA timezone.
- **FR-002**: Each window boundary MUST support HH:MM clock time or a named sun event with a bounded offset and explicit or server-default coordinates.
- **FR-003**: Windows MUST include the start and exclude the end, support overnight intervals, and assign weekdays to the nominal start date. Identical configured endpoints MUST be rejected.
- **FR-004**: A missing required solar event MUST route outside with explanatory metadata. Invalid configuration or timestamps MUST use the error route.
- **FR-005**: Connection status trigger MUST watch one connection of any registered kind across reconnects, disable/enable, and driver replacement, with optional initial emission and state filtering.
- **FR-006**: Status events MUST include connection ID, name, kind, state, previous state, error, state timestamp, and whether the event is initial. Repeated reports within one state MUST NOT repeatedly emit.
- **FR-007**: The connection manager MUST retain current statuses for startup failures and disabled connections, provide an atomic snapshot/watch operation, normalize timestamps, and reject retired-driver status callbacks.
- **FR-008**: Status observation MUST NOT block driver lifecycle callbacks on flow execution. Queue overflow MUST be reported; cancellation MUST remove watchers and suppress later emission.
- **FR-009**: Numeric transform MUST read a numeric message property and write a configured destination using scale/offset, range mapping, clamping, rounding, or compatible unit conversion.
- **FR-010**: Mapping MUST allow reversed ranges, reject zero-width inputs, and optionally clamp to the output range. Rounding MUST support decimal precision and nearest/floor/ceil/truncate modes.
- **FR-011**: Numeric transforms MUST reject missing/nonfinite values, incompatible units, invalid precision/settings, and nonfinite results. They MUST preserve unaffected message properties and expose accurate derived port types.
- **FR-012**: Catalog metadata, strict graph validation, REST, MCP, and editor configuration/ports MUST agree. Existing databases, credentials, encrypted configs, and agent identities MUST remain readable.

### Key entities

- Window: weekday selection, boundary definitions, timezone, coordinates and timestamp source.
- Status observation: connection identity, retained status, watcher queue and driver generation; process-local only.
- Numeric operation: source/destination paths, operation settings and compatible units; no stored state.

## Success criteria

- **SC-001**: Clock, solar, midnight and DST fixtures route to the expected single branch and retain the input message.
- **SC-002**: A real integration's failure, recovery, disablement and failed startup produce the expected initial and transition events; disarming produces no later emissions.
- **SC-003**: Numeric fixtures produce expected calibrated, mapped and converted values, and invalid readings produce errors without successful output.
- **SC-004**: All three nodes are discoverable and testable through MCP and editable with correct live port derivation in the browser.

## Assumptions and scope

- Server nodes only. No agent-node, dashboard widget, durable job, new connection kind, or unrelated webhook changes.
- Unit conversion covers temperature, length, speed, pressure, power, energy, and angle. Authors can use scale/offset for other calibrations.
- Solar offsets are limited to plus or minus twelve hours. Named standard events are supported; custom elevation remains a Schedule feature.
- Tests must cover the date/number parsing and algorithmic boundaries and use a real protocol peer for connection lifecycle.

## References

- [Flow authoring](../../docs/flow-authoring.md)
- [Flow execution](../003-flow-execution/spec.md)
- [Connections](../004-connections-devices/spec.md)
- [Design](plan.md)
