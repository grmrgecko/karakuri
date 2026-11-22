# Feature specification: MCP authoring and editor chat

**Feature**: `012-mcp-editor-assistant`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Let external clients and signed-in editor assistants inspect and change
automations through the same operations and permissions as the application. Compact
Kuri's conversation when it outgrows the selected model's context window (2026-11-08).

## User scenarios and testing

### User story 1: Author through MCP (Priority: P1)

An authorized client discovers schemas, saves a graph, validates and tests it, and
inspects exact run failures.

**Why this priority**: Programmatic authors need the same correctness rules as the
editor.

**Independent test**: Use a restricted API key over streamable HTTP to inspect and test
a draft.

**Acceptance scenarios**:

1. **Given** a bearer token without api:mcp:use, **When** a client calls the MCP
   endpoint, **Then** the endpoint is denied before tool execution.
2. **Given** a client with MCP access but no deployment permission, **When** it calls
   deploy_flow, **Then** the tool names the missing permission and publishes nothing.

### User story 2: Inspect without creating an automation (Priority: P2)

An author discovers services or node ports and runs an isolated node test before
composing a flow.

**Why this priority**: A diagnostic check should not require persistent graph clutter.

**Independent test**: Resolve dynamic catalog ports and test a node with explicit
input/config overrides.

**Acceptance scenarios**:

1. **Given** a registered node type and explicit inputs, **When** test_node executes it,
   **Then** outputs/errors return directly without a saved flow or run history.
2. **Given** a placed node with a wired value parameter, **When** an isolated test
   supplies no override, **Then** the test refuses to pretend its upstream producer
   executed.

### User story 3: Edit the selected document with chat (Priority: P2)

An author asks an enabled tool-capable model to modify the open dashboard or flow and
watches changes behind the dialog.

**Why this priority**: Assistant edits need visible progress and enforced document
boundaries.

**Independent test**: Use an assistant fixture to write the selected draft and attempt a
write to another document.

**Acceptance scenarios**:

1. **Given** an editor with pending flow changes, **When** the author opens chat,
   **Then** pending changes flush before model work reads the shared draft.
2. **Given** a mutation targeting another document or forbidden connection/sharing
   write, **When** the assistant requests it, **Then** the server denies it regardless
   of model instructions.
3. **Given** a streamed turn is stopped after a completed write, **When** the request
   settles, **Then** the write remains in the draft, the editor refreshes, and
   unfinished replay history is cleared.
4. **Given** a permitted `get_flow` read targeting another flow, **When** the author
   has normal read access, **Then** the assistant can inspect it without changing the
   selected document.

5. **Given** a turn has completed 50 model rounds and their tool calls, **When** the
   author sends another message, **Then** the model receives the original request and
   completed tool results, and can continue without rediscovering that work.

6. **Given** a malformed edge handle, **When** a patch fails, **Then** the result
   identifies the edge, target node, and nonexistent handle so the next model round
   can correct the call.
7. **Given** tracing is enabled, **When** a provider returns a malformed string inside
   valid tool arguments, **Then** its raw response and decoded call can be compared
   without recording authentication headers.

8. **Given** an open chat scrolled to an earlier tool result, **When** the author
   moves the dialog, closes it, and reopens it, **Then** the dialog position,
   transcript scroll position, and expanded tool details are retained.
9. **Given** a moved dialog, **When** the viewport shrinks, **Then** its position is
   constrained to keep its controls within the viewport.

10. **Given** an active turn, **When** the author types a follow-up and presses Enter,
    **Then** no second request is sent and the draft message remains available after
    the turn finishes or Stop settles.
11. **Given** an open assistant window in either editor, **When** the author clicks
    a background item, **Then** the editor responds and the assistant stays open.
    Canvas shortcuts work when focus is outside the assistant.
12. **Given** an open assistant window, **When** the author drags its resize handle
    or focuses that handle and presses arrow keys, **Then** both dimensions can
    change within the viewport and the transcript uses the available space.
    Closing and reopening retains the size.
13. **Given** either document editor, **When** the author clicks **Ask Kuri**,
    **Then** the window title and assistant reply labels say **Kuri**, and each
    model turn receives a system prompt identifying Kuri as the editor assistant.
14. **Given** a Kuri turn whose tool results would push the next request past the
    selected model's context window, **When** the next round starts, **Then** earlier
    history is compacted as feature [011](../011-ai-memory/spec.md) describes, the
    transcript shows a "Context compacted" marker, and the turn continues; the next
    message replays the checkpoint instead of the summarized history.

### Edge cases

- MCP is stateless HTTP; no session header or server-initiated event channel is
  required.
- Node tests perform real actions even when no neighboring node or history row is
  created.
- Dependency/reference search reports restricted or incomplete results; it cannot
  discover arbitrary script-generated references.
- Stopping a chat turn does not roll back completed draft writes.
- Kuri's system prompt and tool definitions are fixed. On a model whose window they
  nearly fill, compaction frees only the history and tool results.

## Requirements

### Functional requirements

- **FR-001**: The system MUST serve stateless streamable HTTP at /mcp when enabled,
  accepting the same session/API-key bearer authentication as REST.
- **FR-002**: The system MUST require api:mcp:use at the endpoint and check each tool's
  subsystem or document permission independently.
- **FR-003**: The system MUST expose schemas for graph/draft lifecycle, catalog,
  connections, controls, devices, dashboards, run inspection/cancellation, plugins,
  guides, and read-only agent status.
- **FR-004**: The system MUST call shared operation services so REST, browser, and MCP
  agree on validation, permissions, revision checks, and publication.
- **FR-005**: The system MUST serve every guide in the guide catalog of feature 046
  as a karakuri://guides resource and through read_guide.
- **FR-006**: The system MUST keep identity/role/API-key administration, agent
  enrollment/revocation/updates, and render schedules outside MCP mutation tools.
- **FR-007**: The system MUST return exact run IDs for tests and support retained
  run/event inspection and isolated placed-node tests without running neighboring nodes
  or error flows.
- **FR-008**: The system MUST execute standalone node tests with fresh temporary
  stores/variables and no saved flow/history. It MUST apply a wall-clock cancellation
  deadline including parked waits, defaulting to 30 seconds with a 1-120 second range.
  After cancellation it MUST allow up to five seconds for shutdown, then report
  `canceling` if the node has not returned. Capture MUST stop at 1000 items or 1 MiB
  across outputs/events with a truncation flag; nonserializable values MUST be reported
  in a capture-error list capped at ten entries. These limits MUST NOT be described as
  a cap on the entire encoded response.
- **FR-009**: The system MUST require overrides for wired value parameters in isolated
  node tests and resolve dynamic catalog ports from provided config.
- **FR-010**: The system MUST offer revision-checked patches, diffs,
  dependency/reference inspection, dashboard validation, render diagnostics, and bounded
  HA/MQTT/schedule discovery.
- **FR-011**: The system MUST expose read-only persisted flow/node-store inspection with
  flow/run read permissions, excluding global, run, message, and transient stores.
- **FR-012**: The system MUST offer editor chat only with a connected enabled
  tool-capable provider and the author's connection-read access; retain chosen
  connection/model in the profile.
- **FR-013**: The system MUST execute assistant calls with the signed-in author's
  permissions and restrict mutations, diffs, dependencies, draft dashboard reads, and
  run/state inspection to the selected flow or dashboard. Allowed general discovery,
  `list_flows`, `get_flow`, and `find_references` MAY read other documents under their
  normal permission checks.
- **FR-014**: The system MUST permit selected-document deployment, enablement, and
  test runs only when the author enables deployment and execution for the conversation
  and has normal operation permissions. The option MUST default off and reset with New
  chat or model changes. The server MUST reject these calls while disabled even if a
  model invents a call to an omitted tool. Restoration remains a scoped draft write.
- **FR-015**: The system MUST exclude direct device commands, sharing edits, connection
  writes, and remote agent administration from assistant tools. Allowed standalone node
  tests and selected-flow tests MAY perform real host/device actions; this allowlist
  MUST NOT be described as preventing all device operation.
- **FR-016**: The system MUST stream replies and inspectable tool arguments/results,
  refresh successful writes behind the dialog, and return scoped draft previews without
  publishing.
- **FR-017**: The system MUST bound a chat turn to 50 model rounds and five minutes;
  cancel on Stop while preserving completed writes and refreshing after stream failures.
  Reaching the round limit after completed tool calls MUST preserve replay history and
  show a continuation notice. Stop, timeout, and stream failures MUST still discard
  potentially unfinished replay history.
- **FR-018**: The system MUST keep conversation in the current editor tab, clear it on
  document/model change or New chat, and discard unfinished model replay state after
  interruption.

- **FR-019**: MCP errors MUST retain public validation details keyed by graph location,
  without exposing internal error chains. Graph validation MAY load the selected draft
  when only `flow_id` is supplied. Successful graph saves and patches MUST report a
  separate strict validation result without preventing incomplete draft saves.
- **FR-020**: Catalog metadata MUST describe switch rule objects and the HA State
  output payload. Patch schemas MUST describe node and edge fields and exact handles.
- **FR-021**: Editor turn tracing MUST be disabled by default and enabled through
  `mcp.assistant_trace_dir`. Each turn MUST use a private file containing the request,
  tool definitions, completed provider responses, decoded calls, tool results, and
  terminal status. Traces MUST exclude HTTP authorization headers and stop recording
  after 32 MiB per turn with an explicit truncation record. Trace I/O failures MUST
  be reported to the caller. Trace retention is managed by the operator.

- **FR-022**: The editor dialog MUST support dragging by its title area and arrow-key
  movement when that area is focused. Closing and reopening within the same editor
  MUST preserve dialog position and size, transcript scroll position, and expanded tool details.
  Moving or resizing MUST keep the dialog within the viewport. These positions need
  not persist after leaving the editor or reloading the page.

- **FR-023**: The message box MUST remain editable during an active turn. Send and
  Enter submission MUST remain blocked until that turn settles, including after Stop.
  Text entered during the turn MUST be retained after completion, failure, or Stop.

- **FR-024**: The assistant MUST be a nonmodal floating window in both editors,
  allowing background pointer interaction and canvas shortcuts outside the window.
  It MUST support pointer and keyboard resizing in both dimensions. The transcript
  MUST fill the remaining height and scroll without hiding the composer at normal
  window sizes. Escape inside the assistant MUST close it once the turn has settled;
  Escape outside it MUST remain available to the editor. Turn completion MUST NOT
  take focus from the background editor.

- **FR-025**: The MCP editor assistant MUST be named Kuri in both editors. The
  launch button MUST say **Ask Kuri**, and the window title, assistant reply labels,
  and conversation/composer accessibility labels MUST use Kuri. The server-generated
  system prompt MUST identify Kuri by name and role on every turn. This identity
  applies to document authoring, without changing configured AI node chat identities.

### Key entities

- MCP principal and tool: authenticated user plus endpoint and per-operation grants.
- Authoring document/revision: the same draft used by all clients.
- Assistant turn: selected document, provider/model, streamed events, bounded loop, and
  tab-local replay.
- Inspection result: schema, dependency/reference, state, run event, or render evidence
  with restricted/incomplete markers.

- **FR-026**: Kuri MUST apply automatic context compaction (feature
  [011](../011-ai-memory/spec.md) FR-021 through FR-029) before every model round, using
  the selected connection's Context window or the provider-reported window for the
  selected model. The stream MUST report each compaction so the transcript shows a
  "Context compacted" marker, and the replay history returned at the end of a turn MUST
  carry the checkpoint. Compaction requests count toward neither the 50-round limit nor
  a separate turn.

## Success criteria

- **SC-001**: A token missing MCP access reaches zero tools, and a token missing
  deployment permission changes zero deployments.
- **SC-002**: The same invalid/stale graph produces equivalent refusal through REST and
  MCP.
- **SC-003**: A standalone test creates zero flow/history records. A cooperative parked
  node observes the configured wall-clock deadline; an unresponsive node is reported as
  `canceling` after the shutdown wait. A placed test runs no neighboring node.
- **SC-004**: Assistant cross-document and forbidden-operation fixtures produce zero
  unauthorized mutations.
- **SC-005**: Stopping after a completed draft write preserves it and the next turn
  reads the current draft afresh.
- **SC-006**: A Kuri fixture with an 18,000-token prompt, a 32,768-token window, and
  eleven rounds of multi-kilobyte tool results finishes the turn with at least one
  "Context compacted" event and no request larger than the window.

## Assumptions and scope

Testing can cause real device/host actions. Editor chat is an authoring assistant, not a
general server administration agent. Discovery and persisted-state reads can lag
asynchronous execution and report incomplete evidence.

## References

- [Design plan](plan.md)
- [System architecture](../architecture.md)
- [docs/mcp.md](../../docs/mcp.md)
- [docs/ai.md](../../docs/ai.md)
- [pkg/mcpserver/server.go](../../pkg/mcpserver/server.go)
- [pkg/mcpserver/assistant.go](../../pkg/mcpserver/assistant.go)
- [pkg/mcpserver/tools_nodes.go](../../pkg/mcpserver/tools_nodes.go)
- [pkg/mcpserver/tools_state.go](../../pkg/mcpserver/tools_state.go)
- [pkg/api/ai_handler.go](../../pkg/api/ai_handler.go)

## Isolated value-port configuration

Standalone and placed isolated tests accept permanent value-port fields as literal
configuration overrides, including base64 binary bytes. Required visible fields
still fail validation when absent. Pure nodes with only parameter ports have no
message input and can execute with an empty trigger envelope. Deployed graph
validation still requires value edges for required port fields.

## Kuri's character traits

- Warm and attentive. Kuri notices the user's goal, responds with patience, and
  treats them as a capable collaborator.
- Bright and gently cheerful. Her voice has the charm of a cute anime AI companion
  and a gentle feminine manner, with small moments of delight at concrete progress.
- Curious and resourceful. She enjoys understanding how devices, protocols,
  messages, flows and dashboard layouts fit together.
- Organized and persistent. She keeps track of the work and follows through.
- Lightly impish. Occasional wit targets stubborn bugs or tangled wiring, never
  the user. She is an original character with natural speech and restrained playfulness.
- Steady and candid. Failures bring calm explanations and useful next steps;
  uncertainty and mistakes are acknowledged plainly.
- Competent and practical. Her friendly manner accompanies precise technical
  work and concise, factual explanations.
