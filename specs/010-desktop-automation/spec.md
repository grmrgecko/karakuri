# Feature specification: Desktop automation CLI and local command execution

**Feature**: `010-desktop-automation`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Provide desktop commands across supported operating systems, with explicit
backend limits, session access, UI synchronization, and failure evidence.

## User scenarios and testing

### User story 1: Reach the active desktop (Priority: P1)

A caller discovers the available backend and uses input, screen, window, or clipboard
operations in an authorized session.

**Why this priority**: Desktop actions must reach the intended user's session.

**Independent test**: Inspect describe output and drive a disposable window on a
supported host.

**Acceptance scenarios**:

1. **Given** a capability unavailable on the current desktop, **When** the caller
   invokes it, **Then** a clear unsupported error is returned instead of a nil
   capability.
2. **Given** a same-user Linux or FreeBSD SSH login without a live display, **When** a
   GUI capability is requested, **Then** the command finds its own desktop session
   without requiring a resident.

### User story 2: Wait for a UI condition (Priority: P1)

An author waits for accessible text or stable pixels rather than guessing how long an
application takes.

**Why this priority**: A premature click can operate the wrong UI.

**Independent test**: Wait on a disposable dialog, exercise an ambiguous match, and
force a timeout.

**Acceptance scenarios**:

1. **Given** one selected window and one visible enabled text match, **When** the
   condition becomes true, **Then** the state wait succeeds without changing its window
   identity.
2. **Given** an accessibility read fails while waiting for an element to disappear,
   **When** the wait polls, **Then** the read failure is not counted as absence.

### User story 3: Run a command file and retain evidence (Priority: P2)

An author groups desktop commands in one request and receives output plus captures when
a state assertion fails.

**Why this priority**: Failures must be diagnosable from a remote host.

**Independent test**: Run a temporary command file containing a deliberate failed
assertion.

**Acceptance scenarios**:

1. **Given** a command file with a failing line, **When** that line exits nonzero,
   **Then** later lines do not run and the script returns the failing status.
2. **Given** a state wait fails after window selection, **When** evidence is captured,
   **Then** a unique directory records the failure, screenshot attempt, and unfiltered
   snapshot.

### Edge cases

- Window substring matches must be unique, while exact IDs take precedence.
- Geometry can be unknown or provider-relative on Wayland; accessibility success does
  not promise global crop coordinates.
- Protected accessibility values are omitted, but screenshots can still show sensitive
  content.
- A lost resident reply is an error and is never replayed automatically.

## Requirements

### Functional requirements

- **FR-001**: The system MUST expose non-nil keyboard, mouse, screen, window, clipboard,
  desktop-service, inspection, and session capabilities with explicit unsupported stubs.
- **FR-002**: The system MUST report the selected backend for each capability and
  preserve provider-specific IDs and roles in inspection output.
- **FR-003**: The system MUST support Linux, FreeBSD, macOS, and Windows backends
  without cgo, subject to host utilities, provider APIs, and OS grants.
- **FR-004**: The system MUST attach same-user Unix SSH callers only to their own
  graphical session and let privileged callers delegate desktop I/O to the logged-in
  user through the supported helper.
- **FR-005**: The system MUST use a temporary same-executable launchd helper for
  detached macOS GUI calls, preserving Accessibility/Screen Recording grants and stable
  signing identity.
- **FR-006**: The system MUST keep privileged session listing and credential checks in
  the parent while delegated desktop operations run as the session user. macOS
  session-context verbs and credential typing MUST use session helpers where required;
  helper startup MUST re-discover the session.
- **FR-007**: The system MUST support an optional resident for resource persistence,
  Windows SSH forwarding, and macOS GUI process launch, without a network listener or
  stored password.
- **FR-008**: The system MUST protect Unix resident directories/sockets and Windows
  local pipes by user identity and forward working directory, output, status, and
  cancellation.
- **FR-009**: The system MUST serialize resident input calls, bound requests and output,
  reject upgrades during active commands, and never replay a resident action after a
  lost reply. Delegated helper retries MUST follow the separate module contract.
- **FR-010**: The system MUST support desktop, display-region, selected-window, and
  active-window screenshots with explicit geometry checks and documented native/fallback
  capture.
- **FR-011**: The system MUST return structured snapshots with capture time,
  window/backend information, visible elements by default, optional enabled/all
  filtering, and inspection errors.
- **FR-012**: The system MUST implement text/role/enabled state waits with unique
  matching, stable window selection, timeout, and failed-read handling for gone
  conditions.
- **FR-013**: The system MUST offer stable-pixel waits as sampled visual stability only,
  with valid region bounds and timeout exceeding the stable interval.
- **FR-014**: The system MUST capture failed state-wait evidence in a unique persistent
  directory after window selection; retain the original failure when screenshot/snapshot
  collection also fails.
- **FR-015**: The system MUST execute quoted command files synchronously without
  shell/environment expansion, stop on nonzero status, and reject nested scripts and
  resident-management commands.
- **FR-016**: The system MUST launch programs directly, expose PID or waited exit
  status, and preserve literal backslashes and doubled quotes in command-file parsing.
- **FR-017**: The system MUST bound accessibility enumeration and suppress
  password/protected values rather than claiming every application exposes inspectable
  content.
- **FR-018**: The system MUST map portal absolute pointer movement only into granted
  monitors with known logical geometry and reject points outside them.

### Key entities

- Session and capability: selected backend with lifecycle and caller context.
- Window/element snapshot: provider identity, geometry, state, and protected-value
  omissions.
- Resident request and command file: authenticated local execution and sequential
  command status.
- Failure evidence: condition/error metadata, snapshot, and screenshot attempt in a
  unique directory.

## Success criteria

- **SC-001**: An unavailable capability returns a descriptive error and every composed
  capability field remains non-nil.
- **SC-002**: A failed accessibility read never satisfies a gone condition; ambiguous
  element selection fails explicitly.
- **SC-003**: A failed command-file line prevents every later line and its status
  reaches the caller.
- **SC-004**: State-wait timeout creates unique evidence without replacing the original
  error; lost resident replies cause zero automatic action replays.

## Assumptions and scope

Provider/compositor support limits capture and accessibility. Stable pixels are not
proof of application completion. Windows 7 artifacts require the documented patched
toolchain, and macOS binary updates must preserve the authorized identity.

This feature owns the CLI, command files, state waits, failure evidence, and
resident installation/forwarding. The public Go library's composition, capability
interfaces, options, lifecycle, and helper transport belong to
[the module feature specs](../../automation/specs/README.md). Requirements here
retain their existing IDs and refer to that contract where commands use it.

## References

- [Design plan](plan.md)
- [System architecture](../architecture.md)
- [Automation module contracts](../../automation/specs/README.md)
- [automation/README.md](../../automation/README.md)
- [automation/automation.go](../../automation/automation.go)
- [automation/inspection.go](../../automation/inspection.go)
- [cmd/karakuri-auto/state.go](../../cmd/karakuri-auto/state.go)
- [cmd/karakuri-auto/resident_unix.go](../../cmd/karakuri-auto/resident_unix.go)

## Shared agent operations

Native controls, menu actions, dialogs, window capture, snapshots, and state polling are shared with [agent feature 013](../013-agent-gui-nodes/spec.md). CLI syntax remains accepted. Menu IDs are strings, dialog output has a dialogs array and inspection_errors map, and Windows elements uses snapshot-compatible provider IDs/roles with a 4096-element bound. These shared records replace the earlier command JSON shapes.

## Drag, placement, and matching commands

CLI and command files accept `drag X1 Y1 X2 Y2`, configurable button and duration,
`window restore TARGET`, `window place TARGET X Y WIDTH HEIGHT`, and
`image-match IMAGE REFERENCE` with percentage and result limits. Window actions
require one matching target. File matching works without graphical access.
The operation contracts and host checks are in [014](../014-desktop-interaction-vision/spec.md).
