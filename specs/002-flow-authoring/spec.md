# Feature specification: Flow graphs and deployment

**Feature**: `002-flow-authoring`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Let authors compose typed automation graphs, save incomplete work, and deploy
validated immutable versions without losing concurrent edits. Extend executable
validation to statically inspectable script node calls under feature 026.

## User scenarios and testing

### User story 1: Build and publish a flow (Priority: P1)

An author places nodes, wires ports, saves a draft, and deploys a working automation.

**Why this priority**: This is how the home acquires an automation.

**Independent test**: Save a manual/debug draft, validate and test it, then deploy and
enable it.

**Acceptance scenarios**:

1. **Given** a draft missing required configuration, **When** the author saves it,
   **Then** the incomplete draft is retained without publishing it.
2. **Given** a strictly valid draft, **When** the author deploys it, **Then** a numbered
   immutable version is stored and deployed execution uses that version.
3. **Given** a valid trigger branch and disconnected unfinished nodes, **When** the
   author deploys, **Then** the working branch passes executable validation; explicit
   strict validation of the whole document still rejects its unfinished nodes.
4. **Given** a trigger-reachable script with a statically invalid node call, **When** the
   author tests or deploys, **Then** executable validation reports the script diagnostic;
   saving the draft remains allowed and computed calls remain subject to feature 026's
   runtime validation.

### User story 2: Keep edits consistent (Priority: P1)

Browser and programmatic authors edit the same draft without overwriting one another.

**Why this priority**: Silent lost edits can change device actions.

**Independent test**: Read a revision twice, save one change, then attempt a stale save
and invalid patch batch.

**Acceptance scenarios**:

1. **Given** another client has saved a newer draft revision, **When** an author saves
   using the old revision, **Then** the write is refused and the newer graph is
   preserved.
2. **Given** one invalid operation in a focused patch batch, **When** the batch is
   submitted, **Then** none of its operations are saved.
3. **Given** a selected node, **When** a background refresh fetches the same draft
   revision after autosave or a WebSocket reconnect, **Then** the node remains
   selected and its configuration panel stays open.
4. **Given** a selected node in a clean editor, **When** another client saves a
   different graph revision, **Then** the editor loads that graph and clears
   selection so the configuration panel cannot refer to the previous graph.

### User story 3: Reuse and restore graphs (Priority: P2)

An author encapsulates a subgraph, shares it, and restores earlier versions for
correction.

**Why this priority**: Reuse should preserve the graph contract.

**Independent test**: Validate a nested shared subflow and restore a stored version into
a changed draft.

**Acceptance scenarios**:

1. **Given** a deployed shared subflow, **When** a parent references it, **Then** its
   deployed boundary ports determine the parent interface.
2. **Given** an earlier stored version, **When** the author restores it, **Then** graph
   and versioned metadata replace the draft without deploying or changing enabled state.

### Edge cases

- Duplicate node or edge IDs, missing endpoints, unknown config keys, and incompatible
  declared payload types are invalid.
- An ordinary message input accepts one source; multi value parameter ports accept
  several in edge order.
- Recursive subflows and excessive nesting are rejected; tool-call return cycles have
  their own explicit contract.
- Drafts can retain unavailable plugin node types; strict validation rejects them if
  they are in the graph being validated.
- Omitting `expected_revision` on a whole-graph save opts out of stale-client detection.
  Focused patches require it. Revisions hash graph content, not flow metadata.

## Requirements

### Functional requirements

- **FR-001**: The system MUST store graphs with nodes, edges, viewport, and optional
  local subgraphs, with IDs unique within their graph.
- **FR-002**: The system MUST validate drafts leniently, including unavailable node
  types and script diagnostics. Deployment MUST strictly validate the union of
  trigger-reachable branches and their value dependencies, including statically
  inspectable script node calls under feature 026; shared subflows and graphs without
  triggers MUST be validated in full. Test runs MUST strictly validate the selected
  execution graph, while explicit strict graph validation MUST validate the whole
  submitted document. Computed script calls MUST follow feature 026's runtime validation
  contract rather than being treated as statically certified.
- **FR-003**: The system MUST freeze graph, name, description, and error-routing
  metadata in immutable numbered deployments.
- **FR-004**: The system MUST keep enablement separate from editing and restoration; arm
  triggers against the deployed version.
- **FR-005**: The system MUST reject a stale supplied `expected_revision` on whole-graph
  saves, require a graph revision for focused patches, and apply node/edge patch batches
  atomically. Whole-graph saves MAY omit the expected revision.
- **FR-006**: The system MUST remove incident edges when a patch removes their node and
  preserve unspecified fields in object updates.
- **FR-007**: The system MUST reject unknown config fields on known node types except
  supported presentation keys and resolve configuration-driven ports from catalog
  metadata.
- **FR-008**: The system MUST allow message fan-out, enforce one source per ordinary
  input, and reject declared incompatible payload types.
- **FR-009**: The system MUST allow pure outputs to supply exposed config parameters
  through value edges; distinguish permanent port fields from literal config.
- **FR-010**: The system MUST accept multiple value sources only on multi ports and
  preserve their graph edge order.
- **FR-011**: The system MUST support local and deployed shared subflows with typed
  boundaries, cycle checks, and nesting limits.
- **FR-012**: The system MUST expose retry, bypass, and canvas presentation without
  treating node size, color, label, or rotation as execution settings.
- **FR-013**: The system MUST export self-contained flow documents with embedded shared
  subflows, blank connection-typed fields declared by available node metadata, and
  import through the same graph rules. Import MUST leave those fields unbound for
  destination rebinding.
- **FR-014**: The system MUST update live canvas ports when relevant configuration
  changes, center measured bounds at 100% zoom on entry, and restore parent camera
  position on breadcrumb return.
- **FR-015**: The system MUST notify other editors when draft, deployment, or
  restoration changes and offer comparison with the deployed graph and settings.
  Refreshing the same draft revision MUST retain canvas selection and the open
  node configuration. Loading a different graph revision MUST clear selection.

### Key entities

- Flow: stable identity, enabled state, draft revision, draft graph, and current
  deployment.
- Flow version: immutable numbered graph and metadata snapshot.
- Node, edge, and subgraph: catalog type/configuration, port wiring, and reusable typed
  boundaries.

## Success criteria

- **SC-001**: An incomplete trigger branch saves as a draft but fails deployment.
  Disconnected unfinished nodes do not block a valid trigger branch's deployment.
- **SC-002**: A save supplying a stale revision and a failed patch batch leave the
  winning draft unchanged.
- **SC-003**: Restore changes the draft only; a nested graph uses the deployed shared
  subflow interface.
- **SC-004**: Changing switch or script port settings updates canvas handles before
  saving, with no console error.
- **SC-005**: A script with a statically invalid call saves as a draft but fails its
  strict executable check with the feature 026 diagnostic; a computed invalid call is
  rejected before action.

## Assumptions and scope

Flow JSON is shared by browser, REST, and MCP. A format change must keep stored
flows, versions, and exports readable or migrate them.

## References

- [Design plan](plan.md)
- [Script node SDK](../026-script-node-sdk/spec.md)
- [System architecture](../architecture.md)
- [docs/flow-authoring.md](../../docs/flow-authoring.md)
- [pkg/flow/graph.go](../../pkg/flow/graph.go)
- [pkg/flow/validate.go](../../pkg/flow/validate.go)
- [pkg/flowops/patch.go](../../pkg/flowops/patch.go)
- [pkg/flowops/export.go](../../pkg/flowops/export.go)
- [frontend/src/routes/FlowEditor.svelte](../../frontend/src/routes/FlowEditor.svelte)

Numeric configuration inputs accept fractional values without the browser's
implicit integer step constraint. Nodes still validate their own numeric ranges
and integral requirements. Acceptance: a fractional agent wait timeout such as
0.25 remains a valid number input in the editor.

## Browser prompt authoring extension

[Feature 022](../022-agent-browser-prompts/spec.md) adds a Markdown editor with
portable image/file attachments and stable button output IDs. Label/order edits
update the canvas while retaining wiring. Both draft and strict validation check
attachment bytes, MIME and bounds; incomplete drafts may have no buttons or labels.

## Faceplate extension

[Feature 042](../042-faceplates/spec.md) adds Faceplate nodes whose controls
feed value outputs. A flow with faceplates opens on its first faceplate tab,
with this editor as the first tab. Control values are node config, so they
follow this feature's draft, validation, deployment, revision, restoration,
and export rules.

## Flow sharing extension

[Feature 043](../043-flow-sharing/spec.md) makes a flow's owner and shares
decide who may read, edit, deploy, and delete it. The global flow
permissions say whether a user may author flows at all.
