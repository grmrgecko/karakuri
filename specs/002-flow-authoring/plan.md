# Design plan: Flow graphs and deployment

**Feature**: `002-flow-authoring` | **Date**: 2026-10-04 | **Spec**: [spec.md](spec.md)
**Status**: Implemented baseline

## Summary

Let authors compose typed automation graphs, save incomplete work, and deploy validated
immutable versions without losing concurrent edits.

This plan records the design supporting the baseline requirements. It is not
a pending implementation queue. Requirement IDs are local to this feature.

## Technical context and design

`pkg/flow` defines graph documents, port derivation, type propagation,
parameters, subflows, bypass, and validation. `pkg/flowops` owns lifecycle and
revision-checked patches. Its strict path also applies feature 026's static
script verification without executing calls; draft validation remains lenient.
REST and MCP call these operations rather than maintaining their own deploy rules.
`frontend/src/routes/FlowEditor.svelte` uses Svelte Flow and catalog metadata to
draw and edit the same document.

Separate lenient draft validation from strict executable validation. Derive
script, switch, parameter, and subflow handles using the graph configuration.
`ValidateExecutable` strictly validates the union reachable from all triggers,
including pulled value dependencies. Shared subflows and graphs without triggers
use full validation. Test runs validate their selected execution graph; the
explicit strict validation operation checks the full submitted graph. Lenient
draft validation tolerates unavailable node types and skips their port checks.
Message compatibility permits an `any` source; value compatibility requires
its producer's declared type to satisfy the target field.

## State and data model

`models.Flow` holds identity, mutable metadata, enablement, and references to the
draft and deployed `models.FlowVersion` rows. Draft revisions are the SHA-256
of serialized graph content, excluding flow metadata. Deployed version rows
are immutable snapshots. Local subgraphs live in the graph JSON. Shared
subflows refer to separate flow IDs and their deployed versions. Atomic patches
validate the resulting graph once and save one revision. Restore replaces the
versioned draft fields without publishing or changing the enabled flag.

## Contracts and failure handling

Graph JSON uses `source_handle`, `target_handle`, and `kind: "value"` for
parameter edges targeting `param:<field>`. `params` exposes literal fields;
`port` fields are permanent sockets and never enter `params`. Catalog schemas
provide configuration and resolved ports. Optional field `value_schema` and
port `payload_schema` describe nested authoring data without replacing runtime
validation or changing stored graph formats. Whole saves accept optional
`expected_revision`; supplied stale revisions fail. The transaction also checks
that graph content has not changed since its own read. Focused patches require
the revision and use IDs and optional `subgraph_path`. Export embeds shared
subflows and blanks connection-typed fields from available metadata, requiring
destination rebinding. Fields on unknown node types cannot be identified or cleared.
Deployment freezes the whole draft and versioned metadata after validation.

Canvas selection belongs to the editor shell. Accepting refreshed draft metadata
does not clear it. `applyRemoteDraft` clears selection only when the revision
changes and it replaces the canvas graph. This keeps the configuration panel
open when autosave notifications or reconnects fetch the revision already on
the canvas. No stored graph or API format changes.

## Constitution check

- Keep the authorization and failure boundaries described above explicit.
- Route authoring through shared operations and preserve draft/deployment separation
  where applicable.
- This baseline adds no schema, wire, identity, or encryption-format migration. Future
  changes must assess the affected persistent state before implementation.
- Use the component checks below and the service handoff checks in `AGENTS.md`.

## Verification

Run server and frontend Makefile builds and checks for graph/editor changes. Existing
graph validation, port, subflow, patch, and API tests establish server rules. Feature 026
adds strict static script-call diagnostics and confirms that they do not block a draft
save. Exercise live config changes, nested entry, breadcrumb camera restoration, and concurrent refresh
in the browser; take settled screenshots and inspect the console. Keep purely visual
drafts unsaved.

For FR-015, select a node and refresh the unchanged draft through a real server
notification and a WebSocket reconnect. Check that selection and configuration
remain open after the refresh settles. A different revision must still load its
graph and clear selection. Exercise autosave and a live configuration-driven port
change as well.

These are the acceptance checks for changes to this feature. They do not claim
that every live scenario ran while the baseline documents were written.

## Risks and limits

Flow JSON is shared by browser, REST, and MCP. A format change must keep stored
flows, versions, and exports readable or migrate them.

New behavior needs reviewed requirements and an update to this plan.

## References

- [Server and agent audit](../server-agent-audit.md)

- [System architecture](../architecture.md)
- [docs/flow-authoring.md](../../docs/flow-authoring.md)
- [pkg/flow/graph.go](../../pkg/flow/graph.go)
- [pkg/flow/validate.go](../../pkg/flow/validate.go)
- [pkg/flowops/patch.go](../../pkg/flowops/patch.go)
- [pkg/flowops/export.go](../../pkg/flowops/export.go)
- [frontend/src/routes/FlowEditor.svelte](../../frontend/src/routes/FlowEditor.svelte)

ConfigForm renders number fields with step="any" so fractional duration and other
numeric settings remain valid browser inputs. Runtime node validation still
owns integral and range constraints.

## Browser prompt authoring

[Feature 022](../022-agent-browser-prompts/plan.md) adds FieldMarkdown,
FieldAttachments and FieldButtons. ConfigForm pairs Markdown with its declared
attachment field, uses the safe server renderer for Preview, and selects agents
from registered Open URL capabilities. NodePorts and the canvas derive button
outputs by stable ID. New field types preserve existing graphs and image fields.
