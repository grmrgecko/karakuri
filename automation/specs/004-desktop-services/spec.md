# Feature specification: Clipboard and desktop services

**Feature**: `automation/specs/004-desktop-services`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Expose clipboard, notifications, dialogs, speech, media, power, idle,
and opening operations through the selected desktop session's services.

## User scenarios and testing

### User story 1: Exchange text and interact with the user (Priority: P1)

A caller reads/writes a supported clipboard selection, posts a notification,
or asks a dialog question through the current desktop's service provider.

**Why this priority**: Desktop workflows need session-owned data and user interaction.
**Independent test**: Use a disposable clipboard value, notification, and dialog on
the selected backend and check its supported selection/result behavior.

**Acceptance scenarios**:

1. **Given** a supported clipboard selection, **When** text is written and read while
   its owner remains alive, **Then** the backend exposes that text selection.
2. **Given** a supported dialog, **When** the user responds, **Then** the caller receives
   the provider's dialog result.
3. **Given** an unavailable utility or service, **When** the capability is called,
   **Then** it returns a diagnostic failure.

### User story 2: Operate session services (Priority: P2)

A caller requests speech, media/power actions, or URL/application opening using
the session's provider and permissions.

**Why this priority**: These operations must reach the selected user's desktop services.
**Independent test**: Exercise supported services on an authorized host using a
disposable application and harmless media/speech requests.

**Acceptance scenarios**:

1. **Given** a supported media or speech backend, **When** its request is made,
   **Then** the provider performs it or returns its operational error.
2. **Given** application opening support, **When** an app and arguments are provided,
   **Then** the session's opener handles the request.
3. **Given** an unavailable or denied power action, **When** it is requested,
   **Then** the caller receives an error.

### User story 3: Read idle time (Priority: P2)

A caller obtains elapsed idle time and its source using an explicit current time.

**Why this priority**: Callers need to know where the idle observation came from.
**Independent test**: Exercise idle parser fixtures and a supported host read with
an explicitly supplied time, including helper forwarding.

**Acceptance scenarios**:

1. **Given** a supported idle provider, **When** idle time is read, **Then** elapsed
   seconds and a source name are returned.
2. **Given** a helper/remote idle call, **When** the caller supplies the current time,
   **Then** that explicit time is carried to the receiver.

### Edge cases

- Wayland clipboard ownership can end with the writing process unless a clipboard
  manager or persistent owner holds it.
- Service availability depends on the session bus, installed utilities, and OS policy.
- A dialog can wait for a user while unrelated helper calls remain in flight.
- Successful opening does not prove the target application is ready for input.

## Requirements

### Functional requirements

- **FR-001**: Clipboard capabilities MUST expose text reads/writes for the requested
  selection subject to backend support and ownership lifetime.
- **FR-002**: Notification and dialog capabilities MUST expose their public request
  shapes and return supported results or operational failures.
- **FR-003**: Speech MUST accept the public speech request through the selected provider
  or report missing/unsupported speech support.
- **FR-004**: Media control MUST expose the public media request through the selected
  desktop provider or report a failure.
- **FR-005**: Power control MUST expose supported actions subject to OS permission checks
  and report unsupported or denied actions.
- **FR-006**: Idle reads MUST return elapsed seconds and source information when supported
  and carry the caller's explicit time across helper/remote transport.
- **FR-007**: Opener capabilities MUST expose URL and application opening with supplied
  application arguments through the session's backend or return an error.

### Key entities

- Clipboard selection: session-owned text buffer and ownership lifetime.
- Notification/dialog/speech/media request: provider-specific user interaction parameters.
- Power action: OS-authorized session or machine operation.
- Idle observation: elapsed seconds, source, and supplied current time.
- Open request: URL or application identity and arguments.

## Success criteria

- **SC-001**: Supported clipboard reads return the fixture text while unavailable
  services return diagnostic errors.
- **SC-002**: A waiting helper dialog does not prevent an unrelated notification call
  from receiving its reply.
- **SC-003**: Idle forwarding retains the supplied time and returns its source name;
  app opening errors reach the caller.

## Assumptions and scope

Backend composition and lifetime belong to [001](../001-session-composition/spec.md).
Session switching and credentialed unlock belong to
[005](../005-session-access-and-helpers/spec.md).
CLI `run`, waited exit status, command files, and resident installation belong to
[root feature 010](../../../specs/010-desktop-automation/spec.md).

## References

- [Design plan](plan.md)
- [Module guide](../../README.md)
- [Service interfaces](../../autoapi/interfaces.go)
- [Request data](../../autoapi/types.go)
- [Desktop utility wrappers](../../toolsh/tool.go)
- [Unix desktop services](../../internal/linuxdesk)
- [Helper services](../../helper_client.go)
