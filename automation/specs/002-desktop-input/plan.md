# Design plan: Desktop input

**Feature**: `automation/specs/002-desktop-input` | **Date**: 2026-10-04
**Spec**: [spec.md](spec.md) | **Status**: Implemented baseline

## Summary

Use the composed keyboard and mouse backends. Keep parsed key descriptions,
per-call clock behavior, portal coordinates, and unsupported operations explicit.

## Technical context and design

`autoapi` defines `Keyboard`, `Mouse`, `KeyStroke`, and `Clock`. Platform builders
choose native providers, X11/Wayland adapters, portals, or host utilities. Local
text typing uses the supplied clock for inter-event delays. The helper protocol
serializes text and delay, not the clock interface; receivers use `RealClock`.
The same rule applies to `NewRemoteSession`.

The RemoteDesktop portal acquires consent lazily and requests monitor sharing
for absolute movement. `streams.go` maps desktop logical coordinates into a
granted monitor with known position and size. Negative origins are supported.
Missing geometry and points outside every grant fail. Relative movement does
not need that absolute desktop mapping. Portal position queries are unsupported.

## State and data model

Key strokes, text, delays, pointer coordinates, button state, and scroll requests
are per-call data. Native/portal connections can live for the session lifetime.
The portal's selected monitor geometry belongs to its current grant; restore
tokens use the caller storage defined in [001](../001-session-composition/plan.md).
Monitor-sharing grants use the token key `remote-desktop-monitor-restore`, separate
from older input-only grants, so the first monitor-sharing session asks for consent.

## Contracts and failure handling

Key parsing errors occur before delivery. Capability errors distinguish provider
limitations, missing utilities, failed consent, and OS grants where supported.
Successful API composition does not establish that an application will accept
input. macOS synthetic input needs Accessibility authorization for the executable.
Windows locked-console input has a separate secure-input path in feature 005.

Helper retry can repeat input after a lost reply. Shared-session clients never
replay a lost input call, but cannot undo input already delivered. Use the
transport contracts rather than assuming exactly-once execution or universal
per-call cancellation.

## Constitution check

- Describe uncertain action replay and coordinate boundaries explicitly.
- This split changes no input wire format, portal token, or privacy grant.
- Build/test through the Makefile and preserve existing desktop authorizations.

## Verification

Run `make dev TARGET=auto` and `make test TARGET=automation` from the repository
root. Existing key, Wayland keymap, portal stream, and Mac keyboard fixtures
cover parsing and backend calculations on their applicable platforms. Real
input needs the documented hosts and disposable windows in `AGENTS.md`.
Check delivery, held-button cleanup, negative-origin mapping, and unsupported
position queries after backend changes. This baseline adds no live-host evidence.

## Risks and limits

Input API success is narrower than application readiness. Compositors can deny
access, portal geometry can be incomplete, and transport retry can repeat an action.

## References

- [Public input contracts](../../autoapi/interfaces.go)
- [Key parser fixtures](../../autoapi/keys_test.go)
- [Wayland keymap fixtures](../../internal/wlinput/keymap_test.go)
- [Portal fixtures](../../internal/portal/streams_test.go)
- [Mac keyboard fixtures](../../internal/macos/keyboard_test.go)
- [Helper receiver](../../helper_serve.go)

## Serialized drag

input.go wraps local Mouse, Keyboard, and Window adapters with a context-aware
gate. Desktop.Drag unwraps those adapters while retaining window metadata wrappers,
then moves, presses, interpolates at bounded intervals, and releases under one
acquisition. Cleanup uses a two-second background context. The real authenticated
transport fixture disconnects a drag caller and observes released button state
before another client can click. CLI/node contracts live in root feature 014.
