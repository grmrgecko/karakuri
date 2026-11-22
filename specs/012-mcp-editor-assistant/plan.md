# Design plan: MCP authoring and editor chat

**Feature**: `012-mcp-editor-assistant` | **Date**: 2026-10-04, revised 2026-11-08 | **Spec**:
[spec.md](spec.md)
**Status**: Implemented baseline

## Summary

Let external clients and signed-in editor assistants inspect and change automations
through the same operations and permissions as the application.

This plan records the design supporting the baseline requirements. It is not
a pending implementation queue. Requirement IDs are local to this feature.

## Technical context and design

`pkg/mcpserver` builds a stateless MCP SDK server and registers catalog,
authoring, run, discovery, inspection, and rendering tools. It authenticates
through the common bearer path and evaluates the endpoint permission before
per-tool guards. Services such as `pkg/flowops`, `pkg/dashops`, and `pkg/connops`
retain the actual business rules. `docs/docs.go` embeds the guides.

`pkg/mcpserver/assistant.go` builds a restricted tool set for the signed-in
editor author and selected document. Server wrappers check the allowlist on every
call and constrain document arguments for mutations, diffs, dependencies, dashboards,
runs, and state. General discovery, `get_flow`, `list_flows`, and reference searches
retain ordinary permissions and can read beyond the selection.
`pkg/mcpserver/assistant.go` streams turn
progress; the editor autosaves/flushed revisions and refreshes from shared state.

## State and data model

MCP requests have no persistent transport session. Standalone node tests own
fresh temporary node/variable stores; placed-node tests return a persisted run
ID without executing neighbors. Drafts, revisions, versions, and run evidence
remain their normal service-owned records. Assistant provider/model preference
persists in the user profile; conversation/replay stays in the editor tab and
is reset after interruption or context changes. The loop allows 50 model rounds
within the existing five-minute deadline. At the round boundary, all tool calls
have results, so it emits `done` with replay messages and a `notice` explaining
how to continue. The editor displays that notice and retains the messages. Stop,
timeout, and transport errors keep the existing replay reset behavior. The system
prompt asks for related node and edge edits in one patch after schema inspection.
This change requires no persistent data or configuration migration.

## Contracts and failure handling

The endpoint is `/mcp` with bearer auth and no `Mcp-Session-Id` requirement;
that header is absent from the CORS allowlist, so browser clients are not a target.
Editor chat reaches the same handlers through an in-process MCP session carrying the
author's identity, so it needs no `api:mcp:use` and works with `mcp.enabled` false.
Successful draft changes publish `editor:flow:<id>` or `editor:dashboard:<id>`
notifications without draft content; subscriptions need flow read or dashboard edit.
`capture_mqtt_messages` registers a temporary QoS 0 consumer on the existing driver
and removes only that consumer; its own subscribe makes the broker replay retained
messages to it alone. Disconnect cancellation needs MCP 2026-07-28 or newer.
Migration froze presentation and error-routing settings of older stored versions
at their upgrade-time values.
Tools describe JSON inputs/outputs through tools/list. Errors name missing
permissions. `test_node` returns outputs/errors directly; `test_flow_node`
accepts resolved input IDs and unsaved config_overrides. `inspect_flow_state`
reads only persistent flow variables and durable node keys. Assistant rendering
uses the spec 007 read-only job credential. Only the latest preview is fed back
on subsequent model turns. Reference searches require paging and expose
incomplete/unchecked cases for dynamic or restricted references.
The allowlist excludes direct device commands but includes real standalone node and
selected-flow tests. It constrains authoring, not all device side effects.

Standalone tests use a separate wall-clock request deadline, including parked time.
The tool defaults to 30 seconds and accepts 1-120; the isolated engine waits up to five
additional seconds for cancellation to settle. It returns `canceling` when the node is
still active, without claiming the underlying call was interrupted. Combined output
and event capture stops at 1000 items or 1 MiB; serialization failures are reported in
a bounded error list. These limits apply to captured values, not the complete encoded
tool response with its metadata.

The editor sends `allow_execution`, default false. Both tool discovery and call
execution exclude deployment, enable/disable, and node/flow test tools while false.
The checkbox grants no subsystem permission; ordinary authorization still applies.
It resets with New chat or model changes. Prompt wording does not grant this option.

## Authoring diagnostics

Preserve public `AppError.Details` in MCP error text, keeping internal causes out.
`validate_flow_graph` accepts a graph or loads `flow_id`'s current draft. Saves and
patches return their normal draft detail plus strict `validation`; this is feedback,
not a new save gate. Document schemas describe nested switch rule values and HA
State payload fields. Patch values describe node and edge properties explicitly.

An optional `mcp.assistant_trace_dir` stores one 0600 NDJSON file per editor turn
in a directory created with mode 0700. Record the normalized initial request,
system prompt and tools once, then complete raw provider responses, decoded calls,
tool results, timing, and termination. Never record HTTP headers. A 32 MiB cap
writes a truncation record and stops further capture without stopping authoring.
I/O failures return an error so missing evidence is visible. Existing images or
provider state may appear in explicitly enabled diagnostic files; operators own
retention. No database, identity, or encryption format changes are needed.

Verify malformed-call feedback and repaired calls through real MCP transport,
catalog schemas against real node execution, and trace files through the assistant
HTTP fixture. Retry the user's unchanged lighting prompt against the configured
local model on the selected draft, without deploying or executing device actions.

## Editor assistant identity

Name the document authoring assistant Kuri in the shared `EditorAssistant.svelte`
launch button, window title, reply labels, and conversation/composer accessibility
labels. Prefix the server-generated system prompt in `runAssistant` with Kuri's name
and its flow/dashboard editor role on every turn. Configured AI node chats keep
their own identities. This change needs no preference, API, or database migration.
Verify both editor launch buttons and window titles in the browser.

## Floating assistant window and retained view

Keep the native nonmodal dialog and transcript mounted while closed, with closed dialogs
hidden explicitly. Save transcript scroll offset before closing and restore it after
opening and focus. Expanded tool details remain in the retained DOM. Store dialog
coordinates in the editor component; pointer capture on the title button supports
mouse and touch dragging, with arrow keys providing keyboard movement. Clamp moved
coordinates on opening, window resize, and dialog size changes. No profile or database
state changes are needed. Verify movement, reopen retention, expanded details,
viewport bounds, and keyboard/close behavior in the browser.

Open with `show()` so the background remains interactive. Give the fixed window a
z-index above the editor and below its blocking dialogs. Center it on first opening.
A bottom-right button uses pointer capture to resize width and height and supports
arrow keys, with Shift increasing the step. Keep dimensions on the mounted element;
CSS bounds and position clamping keep it visible when the viewport changes. Use a
fixed initial height and a flexible, independently scrolling transcript; keep controls
from shrinking and allow the whole window to scroll on very small viewports.
Handle Escape only inside the assistant and suppress canvas shortcuts only for
events originating there. Focus the composer on opening; after a turn, focus it only
if focus is still inside the assistant. Verify background clicks in both editors,
canvas shortcuts, pointer/keyboard resizing, retained size, and viewport constraints.

The composer remains editable during a turn. The existing busy guard rejects
submission and Stop replaces Send until the request and editor refresh settle.
Only starting a new turn clears the submitted text; completion and cancellation
leave any newly typed draft intact. Verify Enter rejection and draft retention with
a delayed browser stream fixture for completion, failure, and Stop.

## Context compaction

`runAssistant` creates one `ai.Compactor` per turn from feature 011's shared
implementation, with the selected driver and model. Kuri has no model handle, so the
window comes from the connection's Context window or the driver's detected and learned
windows, with the default buffer and compaction always on. Each round's `driver.Stream`
call goes through `Compactor.Send`. A compaction emits `context.compacted` with its
reason and sizes before the round's `reply.start`, and the trace records the summary.
`req.Messages` after compaction starts with the checkpoint, so the `done` event's
replay history carries it and the next turn starts compacted. Summary requests are
not model rounds for the 50-round limit; they share the turn's five-minute deadline.
An overflow compaction cannot fix ends the turn with the provider's error, as today.

`EditorAssistant.svelte` and `NodeChat.svelte` render `context.compacted` as a
"Context compacted" divider entry in the transcript. Verify on llama-swap
gemma-4-26B with the 2026-11-08 request, in the browser, with a console check.

## Constitution check

- Keep the authorization and failure boundaries described above explicit.
- Route authoring through shared operations and preserve draft/deployment separation
  where applicable.
- This baseline adds no schema, wire, identity, or encryption-format migration. Future
  changes must assess the affected persistent state before implementation.
- Compaction adds a stream event and changes no stored state; replay history stays a
  browser-tab value.
- Use the component checks below and the service handoff checks in `AGENTS.md`.

## Verification

Run `make dev TARGET=server` and `make test TARGET=server`. MCP permission, draft,
isolated-node, discovery, inspection, and assistant fixtures cover tool contracts. A
compaction fixture (SC-006) drives a turn past a 32,768-token window. Use
the running server's MCP endpoint for functional changes, retrieving the development
token without printing it in logs. Editor chat changes also need frontend builds/checks,
visible streamed writes, Stop handling, screenshots, and console inspection.

These are the acceptance checks for changes to this feature. They do not claim
that every live scenario ran while the baseline documents were written.

## Risks and limits

Testing can cause real device/host actions. Editor chat is an authoring assistant, not a
general server administration agent. Discovery and persisted-state reads can lag
asynchronous execution and report incomplete evidence.

New behavior needs reviewed requirements and an update to this plan.

## References

- [Server and agent audit](../server-agent-audit.md)

- [System architecture](../architecture.md)
- [docs/mcp.md](../../docs/mcp.md)
- [docs/ai.md](../../docs/ai.md)
- [pkg/mcpserver/server.go](../../pkg/mcpserver/server.go)
- [pkg/mcpserver/assistant.go](../../pkg/mcpserver/assistant.go)
- [pkg/mcpserver/tools_nodes.go](../../pkg/mcpserver/tools_nodes.go)
- [pkg/mcpserver/tools_state.go](../../pkg/mcpserver/tools_state.go)
- [pkg/api/ai_handler.go](../../pkg/api/ai_handler.go)

## Isolated value-port configuration

prepareNodeTest clones metadata for its strict configuration check, treating port
fields as literal fields for that check alone. The executable metadata retains
its ports; parameter ports are never accepted as message envelopes. Pure nodes
with no message ports execute as value sources. The real MCP fixture checks a
binary Image value and missing required bytes without creating history rows.
