# Design plan: Agent action options and HTTP requests

**Feature**: `020-agent-action-options` | **Date**: 2026-10-06 | **Spec**: [spec.md](spec.md)

## Summary

Extend host actions through optional catalog fields and add an agent-local HTTP node.
Keep process and request mutations non-retryable. Attach capture provenance at the
provider boundary and retain it through desktop helpers.

## Technical context

Go with the existing nodeapi/agentwire catalog, standard-library processes/files/HTTP,
and the separately importable automation module. No new dependencies or persistent
entities. Linux, FreeBSD, Windows and macOS retain their existing provider limits.
Returned process streams remain capped at 32 MiB; HTTP body reads use the agent output
limit. Desktop mapping is optional and provider-authored.

## Constitution check

Pass before and after design: errors/cancellation are explicit; defaults preserve
current behavior; schemas drive shared server/editor validation; host permissions
remain unsandboxed; no database/key/identity migrations; use Makefile builds/tests,
MCP, browser derived-port checks and applicable live desktop checks.

## Project structure

- `agent/exec/`: stream policies, accepted exits and decoded-result destination.
- `agent/agentnode/`: result-path assignment and output type rules.
- `agent/system/`: shared bounded HTTP transport, probe health and service confirmation.
- `agent/storage/`: exclusive creation and guarded staged replacement.
- `automation/autoapi/`, providers and helper transport: optional capture geometry.
- `agent/gui/vision.go`: mapped capture/match output.
- `specs/020-agent-action-options/`: requirements, contracts, tasks and verification.

## Implementation design

Use explicit process policy fields, sharing field metadata and capture behavior between
Command and Script. Extend the internal script run helper to return its actual exit code alongside
the captured streams. Validate policies before filesystem side effects
or launching a child. Result-property writes clone ancestors; non-object payload parents
fail before launch rather than silently discarding the incoming value.

Network Probe and HTTP Request share a small agent-only HTTP helper. Never import server
engine packages. Use direct networking, TLS 1.2+, context deadlines, bounded headers/body,
and strip all configured headers across origins on redirects. Return response headers
but never request headers or credentials in errors. HTTP Request is non-retryable because
agent errors do not preserve ExecutionError classification over the wire. Preserve probe
reachability defaults; health assertions are opt-in. Explicit binary body encoding
decodes base64 string results from binary-output nodes instead of guessing from strings.

Service confirmation validates duration fields before acting, observes explicit final
states, and includes the final state in metadata. Enablement reads systemd UnitFileState,
launchd disabled overrides or SCM startup type. Observe running/stopped independently.
Windows Service Status reads native SCM state rather than parsing localized sc.exe
output, so it no longer returns the raw `details` text.

Create-only writes stage and sync a complete file then link it exclusively to the target.
Replacement checks expected SHA-256 before staging and immediately before installation.
This narrows races but cannot provide compare-and-swap against outside writers. Preserve
permissions by default; reset uses the existing new-file mode.

Capture mapping wraps image.Image with optional provider geometry. PNG transport carries
geometry separately and reconstructs the wrapper. Only providers with known capture
rectangles publish mapping; unknown native-window/portal captures remain unmapped.

## Verification

Meaningful tests cover real process output/exit policies, actual files and exclusive
creation, local HTTP/TLS peers, service-manager executable fixtures and image mapping/
helper serialization. Run agent, server, frontend and automation checks as applicable.
Build via make dev, restart the development service and verify HTTP and active state.
Use MCP to exercise advertised updated nodes. Browser checks change output settings and
verify canvas types live, screenshot each interaction and inspect console errors.

## Risks and limits

No persistent migration. File-only logs and staging still consume host disk. External
writers can race a hash check. Confirmation observes manager state rather than app health.
Capture geometry is omitted where backend coordinate agreement cannot be established.
