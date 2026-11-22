# Feature specification: Shared session transport

**Feature**: `automation/specs/006-shared-session-transport`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Share an already composed desktop session over caller-authenticated
connections, serialize individual input calls, and avoid replaying uncertain actions.

## User scenarios and testing

### User story 1: Share one session across authenticated clients (Priority: P1)

A host authenticates connections and serves several clients without transferring
ownership of the shared desktop session to any one client.

**Why this priority**: Persistent backend resources must survive individual client exits.
**Independent test**: Use controlled connections to a composed session server and
close a client while keeping another client connected.

**Acceptance scenarios**:

1. **Given** an authenticated connection, **When** `NewRemoteSession` succeeds,
   **Then** it returns capability adapters and the server's composition description.
2. **Given** one client disconnects, **When** its server handler settles, **Then** the
   shared session remains available to its owner and other clients.
3. **Given** two competing keyboard/mouse calls, **When** they reach the same server,
   **Then** individual input calls execute without overlap.

### User story 2: Fail uncertain actions without replay (Priority: P1)

A remote caller receives failure after disconnect or cancellation rather than
automatically repeating an action whose outcome is uncertain.

**Why this priority**: An input event can have executed before its reply was lost.
**Independent test**: Interrupt a controlled connection mid-call and inspect retry,
cancellation, and shared-session lifetime behavior.

**Acceptance scenarios**:

1. **Given** a lost remote reply, **When** the peer disconnects, **Then** the call fails
   and no automatic replay occurs.
2. **Given** a remote call in progress, **When** its context is canceled, **Then** the
   client connection closes and the server cancels that connection's work.
3. **Given** another client remains connected, **When** the canceled client closes,
   **Then** the shared desktop session is not closed by that client.

### Edge cases

- Canceling one remote call closes its client connection and can fail other pending
  calls on that connection.
- Input-call serialization does not make a multi-call sequence atomic.
- Backend cancellation remains cooperative and cannot undo completed actions.
- Authentication and listener permissions belong to the host, outside this transport API.

## Requirements

### Functional requirements

- **FR-001**: `NewRemoteSession` MUST use a supplied authenticated connection, obtain its
  composition description, and close only its client connection when its session closes.
- **FR-002**: `SessionServer` MUST serve caller-supplied authenticated connections and
  cancel disconnected clients' work without closing the shared session. Hosts MUST own
  authentication, listener security, and shared-session cleanup after serving ends.
- **FR-003**: A session server MUST serialize individual keyboard and mouse calls across
  clients. It MUST NOT imply atomicity for a sequence of calls.
- **FR-004**: Remote-session calls MUST never retry a lost call and MUST close their
  connection on caller cancellation. They MUST NOT promise to undo completed actions.
- **FR-005**: Transport MUST preserve supported error categories and diagnostic text,
  return captured image pixels through PNG encoding, and carry explicit request data
  such as idle timestamps. Transported text typing MUST use the receiver's real clock.

### Key entities

- Session server: owner-supplied session and shared input-call gate.
- Remote session: capability adapters over one authenticated client connection.
- Served connection: request/reply stream and its cancellation lifetime.
- Host application: authentication, listener permissions, and shared resource owner.

## Success criteria

- **SC-001**: Connected clients receive capability results while closing one client
  leaves the shared session usable.
- **SC-002**: Controlled lost-reply fixtures cause zero remote-session retries;
  cancellation closes the connection and cancels its served work.
- **SC-003**: Concurrent input fixtures observe no overlapping keyboard/mouse calls;
  supported error categories and image pixels survive transport.

## Assumptions and scope

This feature owns `SessionServer` and `NewRemoteSession`, not socket/pipe creation,
resident installation, command queues, or CLI command/output limits. Those are
consumer behavior in [root feature 010](../../../specs/010-desktop-automation/spec.md).
Ordinary helper calls have the distinct retry/cancellation contract in
[005](../005-session-access-and-helpers/spec.md).

## References

- [Design plan](plan.md)
- [Module guide](../../README.md)
- [Session transport](../../resident.go)
- [Client adapters](../../helper_client.go)
- [Wire error categories](../../helper_wire.go)
- [Framed protocol](../../internal/hrpc/hrpc.go)
- [Transport fixtures](../../resident_test.go)

## Native desktop actions

The shared session exposes desktop controls, menus, dialogs, and window capture. Input serialization includes native control clicks and menu invocation; these calls retain the existing no-replay contract.

## Drag and placement transport

Drag and placement each travel as one desktop mutation and never replay a lost
reply. Shared-session input serialization includes both operations and window
Act. Disconnect cancels the active drag; release uses an independent bounded
context before the gate admits another client's input. Ordinary helper EOF also
cancels active work and waits for its cleanup before closing the composed session.
