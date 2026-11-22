# Design plan: Clipboard and desktop services

**Feature**: `automation/specs/004-desktop-services` | **Date**: 2026-10-04
**Spec**: [spec.md](spec.md) | **Status**: Implemented baseline

## Summary

Use session-owned clipboard and desktop service providers through the composed
capability interfaces. Keep service availability, ownership, and returned errors
visible to library callers.

## Technical context and design

`autoapi` defines clipboard, notification, dialog, speech, media, power, idle,
and opener contracts. Platform composition chooses native providers or utility
wrappers. Linux/FreeBSD providers can use the desktop bus and installed commands;
macOS and Windows compose their platform implementations. Unavailable capabilities
remain populated with failure-reporting backends.

Delegated capability adapters carry these requests to the desktop user's helper.
The helper multiplexes replies, allowing a dialog to wait without blocking an
unrelated notification. Transport errors and cancellation follow feature 005;
shared-session clients follow feature 006.

## State and data model

Most requests and results are transient. Clipboard ownership can depend on a
live process or backend connection, especially on Wayland. Session closers release
held resources. Persisting that ownership through a resident is a consumer choice,
not an installation performed by the module.

Idle calls contain an explicit `now` and return seconds plus a source name.
That timestamp survives transport, unlike the clock interface for keyboard delays.
Opener calls carry a URL or app plus arguments; they do not provide the CLI's
general PID, waited exit-status, or command-file execution contract.

## Contracts and failure handling

Provider support, utility availability, bus ownership, and OS policy determine
which requests succeed. Power operations require appropriate host permissions.
Missing utilities and unsupported capabilities expose the error categories in
feature 001 where applicable. A service call does not establish application
readiness; callers must obtain their own observations before later actions.
Windows notifications use WinRT toasts and fall back to a Shell_NotifyIcon balloon
when the runtime is absent (Windows 7). The balloon path keeps its window alive for
11 seconds or until cancellation so a short-lived command does not exit first.

## Constitution check

- Session ownership and power-operation permissions remain explicit.
- This spec split changes no clipboard persistence, OS service, or request format.
- Preserve held resources and use the Makefile for component checks.

## Verification

Run `make dev TARGET=auto` and `make test TARGET=automation` from the repository
root. Helper fixtures cover clipboard text and service error forwarding; idle
parser and opener fixtures cover their backend data. Use disposable clipboard
text, notifications, dialogs, and applications on changed platforms. Avoid
disruptive power actions during routine verification. Follow `AGENTS.md` for live
host checks and cleanup. No live service matrix is certified by this baseline.

## Risks and limits

Clipboard content can disappear when its owner exits. User dialogs can remain
open after an ordinary helper caller cancels its wait. Media and power support
depends on the selected session and OS authorization.

## References

- [Service contracts](../../autoapi/interfaces.go)
- [Helper fixtures](../../helper_test.go)
- [Idle parser fixtures](../../internal/idleparse/idleparse_test.go)
- [Opener fixtures](../../internal/linuxdesk/xdgopen_test.go)
- [CLI resident lifecycle](../../../specs/010-desktop-automation/plan.md)
