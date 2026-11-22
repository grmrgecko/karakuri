# Design plan: Desktop automation CLI and local command execution

**Feature**: `010-desktop-automation` | **Date**: 2026-10-04 | **Spec**:
[spec.md](spec.md)
**Status**: Implemented baseline

## Summary

Provide desktop commands across supported operating systems, with
explicit backend limits, session access, UI synchronization, and failure evidence.

This plan records the design supporting the baseline requirements. It is not
a pending implementation queue. Requirement IDs are local to this feature.

## Technical context and design

The standalone `automation/` Go module probes platform backends and composes
an `automation.Session`. `cmd/karakuri-auto` parses commands, screenshot targets,
state waits, command files, and resident lifecycle. Agent nodes call the same
module. The library API and lifecycle are specified separately in
[the module feature plans](../../automation/specs/README.md).

Same-user Linux/FreeBSD CLI calls adopt discovered session environment in their
own process. Privileged or macOS detached callers use same-executable helpers.
Helpers start lazily, rediscover the current session, and close with their owning
session. The optional resident keeps clipboard/portal resources alive and
forwards through a private local socket or named pipe. It is a mode of the CLI,
not a second Mac binary to sign or authorize.

## State and data model

Backend sessions and resident capability connections are transient. Restore
tokens and capture directories persist under the desktop user's configuration.
Resident requests retain separate cwd/stdout/stderr/status. Resident limits are
16 requests, 30 minutes per command, and 4 MiB per output stream. Evidence calls
have separate ten-second deadlines; on Unix directories/files use 0700/0600.
Signing material, portal grants, and live resident installs are host state.

## Contracts and failure handling

`describe` identifies backends. Snapshot JSON records geometry-known state,
nullable unknown window enabled state, and inspection errors. Role/element IDs
retain their provider spelling; no OCR is implied. `wait state` and `assert
state` use the same predicate. Stable waits use a 100 ms polling ticker;
backend latency determines the actual sampling cadence. CLI `run` has no shell
expansion. Command files ignore
blank/comment lines, group quoted arguments, and stop synchronously on failure.
Portal sessions preserve selected-monitor geometry rather than assuming one
unbounded desktop.
The Unix resident serves command subprocesses through `backend.sock` beside
`control.sock` in the private per-user directory. The Windows resident pipe
listener uses explicit Win32 connection handling because on Windows 7 the library
listener can connect a client to an unused reservation handle, and replies are
acknowledged before the server closes the pipe so buffered output is not lost.

## Constitution check

- Keep the authorization and failure boundaries described above explicit.
- Route authoring through shared operations and preserve draft/deployment separation
  where applicable.
- This baseline adds no schema, wire, identity, or encryption-format migration. Future
  changes must assess the affected persistent state before implementation.
- Use the component checks below and the service handoff checks in `AGENTS.md`.

## Verification

Run `make dev TARGET=auto` and `make test TARGET=automation`; agent desktop changes also
need the agent targets. Use the platform hosts in AGENTS.md and canonical
~/karakuri-auto paths. Verify real inspection, input, captures, state waits, and cleanup
with disposable windows. Sign Mac uploads with the persistent identity and use the
patched Makefile toolchain for Windows 7. No live host behavior is certified by this
documentation addition.

These are the acceptance checks for changes to this feature. They do not claim
that every live scenario ran while the baseline documents were written.

## Risks and limits

Provider/compositor support limits capture and accessibility. Stable pixels are not
proof of application completion. Windows 7 artifacts require the documented patched
toolchain, and macOS binary updates must preserve the authorized identity.

New behavior needs reviewed requirements and an update to this plan.

## References

- [System architecture](../architecture.md)
- [Automation module designs](../../automation/specs/README.md)
- [automation/README.md](../../automation/README.md)
- [automation/automation.go](../../automation/automation.go)
- [automation/inspection.go](../../automation/inspection.go)
- [cmd/karakuri-auto/state.go](../../cmd/karakuri-auto/state.go)
- [cmd/karakuri-auto/resident_unix.go](../../cmd/karakuri-auto/resident_unix.go)

## Shared agent operations

Native desktop operations now live in automation/desktop*.go and cross the desktop helper protocol. CLI adapters retain argument parsing and output rendering. automation/ui*.go owns snapshots, selectors, polling, and bounded evidence utilities. CLI evidence retains separate sessions and ten-second per-capture budgets; agent evidence uses its own eight-second total budget.

## Drag, placement, and matching commands

interaction.go routes drag and placement through Session.Desktop. Placement
prints requested and observed geometry. vision.go reads host-local PNG/JPEG files
and calls MatchImage before session composition; Windows preflight also bypasses
resident forwarding for this headless command. Command files reuse these handlers.
Root feature 014 owns the new agent schemas and verification record.
