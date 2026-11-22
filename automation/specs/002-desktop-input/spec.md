# Feature specification: Desktop input

**Feature**: `automation/specs/002-desktop-input`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Type text, press key combinations, and operate the pointer using the
selected desktop backend with explicit clock and portal-coordinate behavior.

## User scenarios and testing

### User story 1: Send keyboard input (Priority: P1)

A caller types text or presses a parsed key combination in the intended session.

**Why this priority**: Most desktop workflows need predictable keyboard delivery.
**Independent test**: Type into a disposable text field and exercise parsed key
combinations and delayed text with the applicable clock.

**Acceptance scenarios**:

1. **Given** a supported local keyboard backend and supplied clock, **When** text
   typing uses a delay, **Then** that clock controls the delay between input events.
2. **Given** a delegated or remote keyboard, **When** the caller supplies an injected
   clock, **Then** the receiving process uses its real clock for text delays.
3. **Given** an invalid key description, **When** it is parsed, **Then** parsing returns
   an error before desktop input is attempted.

### User story 2: Operate the pointer within the available geometry (Priority: P1)

A caller moves, clicks, holds, or scrolls the pointer without assuming that a
Wayland backend exposes unrestricted desktop coordinates or pointer position.

**Why this priority**: Invalid geometry can direct input to the wrong target.
**Independent test**: Drive a disposable window and exercise inside/outside portal
coordinates, including a monitor with a negative logical origin.

**Acceptance scenarios**:

1. **Given** granted portal monitor geometry, **When** an absolute coordinate is inside
   that geometry, **Then** the backend maps it into the selected monitor's space.
2. **Given** missing portal position/size or a point outside all grants, **When** an
   absolute move is requested, **Then** the move fails explicitly.
3. **Given** a backend without pointer-position access, **When** position is requested,
   **Then** it reports the unsupported operation rather than inventing a location.

### Edge cases

- Portal geometry uses logical desktop coordinates, including negative monitor origins.
- Portal consent and required monitor sharing happen lazily when input needs them.
- Cancellation and replay depend on the selected local/helper/remote transport.
- Local input support can depend on compositor APIs, host utilities, and OS privacy grants.

## Requirements

### Functional requirements

- **FR-001**: Keyboard input MUST expose text with an inter-event delay and accept a
  caller-supplied clock for local text typing.
- **FR-002**: Keyboard input MUST expose key combinations through `KeyStroke` and
  `ParseKeyStroke`; invalid descriptions MUST return parsing errors.
- **FR-003**: Mouse input MUST expose absolute/relative movement, clicks, held buttons,
  scrolling, and position queries subject to backend support.
- **FR-004**: Delegated and remote text typing MUST use a real clock in the receiving
  process; the caller's clock interface MUST NOT be assumed to cross transport.
- **FR-005**: Portal absolute movement MUST require granted monitor logical position
  and size, support negative origins, and reject coordinates outside all grants.
- **FR-006**: Portal pointer-position queries MUST report lack of support. Missing
  geometry for absolute movement MUST fail rather than select an arbitrary monitor.

### Key entities

- Key stroke: parsed key and modifier combination.
- Clock: local per-call delay source or receiving process's real clock.
- Pointer request: coordinates, relative flag, button state, or scroll parameters.
- Portal grant: selected monitor and its logical desktop geometry.

## Success criteria

- **SC-001**: Key parser fixtures reject invalid descriptions; local delayed typing
  uses the provided clock and transported typing uses the receiver's clock.
- **SC-002**: Portal mapping fixtures accept valid granted points and reject missing
  geometry and points outside the grant.
- **SC-003**: Unsupported pointer-position queries return a failure without a fabricated
  position.

## Assumptions and scope

This feature defines input capabilities. It does not select windows, wait for UI
conditions, or make a sequence of input calls atomic. Helper replay/cancellation
is defined in [005](../005-session-access-and-helpers/spec.md); shared-session input
serialization is defined in [006](../006-shared-session-transport/spec.md).

## References

- [Design plan](plan.md)
- [Module guide](../../README.md)
- [Input interfaces](../../autoapi/interfaces.go)
- [Key parsing](../../autoapi/keys.go)
- [Clock](../../autoapi/clock.go)
- [Portal coordinate mapping](../../internal/portal/streams.go)
- [Helper keyboard](../../helper_client.go)

## Serialized drag

- **FR-010**: Desktop.Drag MUST accept bounded signed points, a left/middle/right button, and zero through thirty seconds of duration; it MUST traverse one path under the composed session's input gate and attempt release with an independent two-second cleanup context after any press attempt.
- **FR-011**: The composed input gate MUST include keyboard, mouse, window actions, control/menu actions, and placement. Independent Sessions do not imply cross-process input exclusion.

Acceptance: one authenticated client's disconnected drag releases its button before
another client's queued click executes. A local drag uses the same composed gate.
Ordinary helper calls do not provide per-call cancellation; bounded drag may finish
after the caller returns. Lost drag replies are never replayed.
