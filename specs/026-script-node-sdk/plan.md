# Design plan: Script node SDK

**Feature**: `specs/026-script-node-sdk` | **Date**: 2026-10-26 | **Spec**: [spec.md](spec.md)
**Status**: Implemented

## Summary

Expose eligible catalog nodes as synchronous functions or commands in the
existing server flow JavaScript, Python, and Bash script nodes. Language-specific
bindings forward one invocation request to the active engine run. The engine
validates and executes the real node with its own retry, cancellation, stores,
and history. A second catalog projection feeds generated TypeScript declarations
in a lazy JavaScript editor worker and language-appropriate Python/Bash editor
assistance. Runtime-specific static checkers back strict flow validation and the
verification endpoint.

Calls are sequential in every language. Standalone script tests, device scripts,
payload transforms, and remote-agent scripts retain their existing behavior.

## Technical context

- Language and dependencies: Go and the existing goja, CPython WebAssembly, and
  mvdan shell runtimes and parsers; Svelte 5, CodeMirror 6, TypeScript's language
  service for JavaScript, catalog-driven Python/Bash assistance, and Vitest.
- Components: `pkg/nodeapi` for the optional invocation contract,
  `pkg/scriptsdk` for catalog projection and static/runtime validation,
  `pkg/engine` for invocation, `pkg/sandbox` and `pkg/nodes/core` for language
  bindings, `pkg/flowops` for shared strict validation, `pkg/api` for catalog and
  verification endpoints, and `frontend/src/lib/components/editor` plus
  `frontend/src/lib/stores` for analysis.
- Storage: no migration. Invocation evidence uses existing run events and JSON
  payloads. Stateful calls use existing node stores under namespaced invocation
  identities. Graph-node identities and existing rows do not change.
- Limits: 100 admitted node calls per script execution, 1000 messages and 1 MiB
  of JSON result data per call, the existing 256 KiB script-test/verification
  source cap, and existing script output, network, and wall-clock limits. The script wall-clock budget includes time in node calls,
  including waits. Engine parked-time accounting and worker coordination still
  apply within that outer budget.

## Constitution check

- **Failures**: Every call validates before action, has a bounded count and an
  intentional per-call retry policy, observes cancellation, and returns a
  catchable error with attempts and classified details. Any call that begins
  execution suppresses the outer script retry so earlier effects cannot repeat. Earlier effects are
  not rolled back. Caught failures are contained without pretending exactly-once
  delivery or restart recovery.
- **Drafts and deployment**: Draft saves keep their current leniency and retain
  script diagnostics. `pkg/flowops` adds static script verification to every
  strict flow path used by tests and deployment. Computed calls are not treated
  as statically certified and are validated again at invocation.
- **Permissions and trust**: Catalog and verification routes use flow read and
  flow write permissions respectively. Node invocation is a mediated capability
  installed only in the server flow script context. It grants no direct host,
  driver, credential, filesystem, process, environment, or socket access. The
  `script.network` policy governs only the script's own HTTP helper; called nodes
  keep their ordinary canvas authority. Agent nodes keep their enrolled-host
  execution path and OS-account trust boundary.
- **Existing state**: No database, deployment, identity, CA, or encryption
  format changes. New store identities are namespaced beneath the calling script
  node and cannot rewrite the identity of an existing graph node.
- **Evidence**: Use `make dev TARGET=server`, `make dev TARGET=frontend`,
  `make test TARGET=server`, and `make test TARGET=frontend`. Restart and verify
  the development service. Use MCP for safe function and strict-validation
  checks, and the browser for live completion, hover, signature help,
  diagnostics, context exclusion, settled screenshots, and console review.

## Design and contracts

### Function catalog

`pkg/scriptsdk` derives contracts from `NodeMeta`. The exact registry type is
the canonical function property. Unambiguous lower-camel aliases provide the
ergonomic form, and ambiguous aliases are omitted rather than reassigned.
`nodes.call` accepts a dynamic exact type. No fallback type is allowed.

Eligibility excludes trigger and annotation categories, subflow and link
routing, subflow boundaries, the nested script type, fan-in join, batch, and
aggregate constructs, and opaque capability types that cannot cross the script
JSON/base64 boundary. `NodeMeta.InvocationState` distinguishes audited stateless,
stateful, and unknown-state types. Stateful and unknown-state calls require an
explicit invocation ID. Built-ins set the metadata where needed; contributed
nodes with no declaration remain unknown rather than becoming stateless by default.

`GET /api/scripts/node_functions` builds the ordered contract on demand and a
SHA-256 revision. The versioned response includes the full public `NodeMeta`,
canonical name, aliases, stable node type, and identity requirement. It exposes no connection
secrets or live integration configuration.

### Invocation

`pkg/nodeapi` defines `NodeCall`, `NodeCallOptions`, `NodeCallResult`,
`NodeCallError`, and an optional `ScriptNodeInvoker` interface. JavaScript and
Python install `nodes`, and Bash installs `node`, only when
`sandbox.Request.NodeInvoker` is non-nil. Named targets and generic calls decode
into the same request. Python returns dictionaries and raises structured
exceptions; Bash returns JSON documents and uses nonzero statuses with structured
error documents. Binary data uses each runtime's existing base64 representation.

`pkg/nodes/core.ScriptNode` wraps the invoker with a 100-call budget and tracks
whether any call began execution. Its deferred error path marks later script
failures nonretryable after that point. Ordinary scripts without node calls keep
their existing retry behavior. Count exhaustion is recorded as fatal and fails
the script even when code catches the exception or nonzero command status.
Result writes and output processing do not proceed after that failure. Each Bash
execution serializes foreground node handlers and rejects background and pipeline
participation before admission so no call can race the shared worker slot,
outlive the interpreter, or deadlock output. Computed or escaped command names,
command wrappers, aliases, `shopt`, `eval`, traps, and duplicate function declarations
are rejected because the original AST cannot prove which execution form they
will reach. Top-level stdin is never drained.

The engine's invocation path:

1. Resolve the exact type and reject ineligible or unavailable types.
2. Validate config, retry, message inputs, value parameters, stable state ID,
   purity, and payload shapes without executing the node.
3. Build a synthetic `runNode` and `nodeInstance` with direct params and all
   ordinary outputs observed. Use a unique history ID and a separate stable
   store ID for stateful calls.
4. Execute synchronously with the caller's worker slot and the script context.
   Message-path calls use the ordinary retry loop with script-call failure
   containment. Pure pulls use pure evaluation and reject impure types.
5. Normalize every ordinary output port to an array, including empty arrays,
   merge mid-execution emissions, enforce 1000 messages and 1 MiB of JSON result
   data, record invocation evidence, and return outputs without graph delivery.

History uses existing run events. The event payload includes parent script ID,
node type, invocation ID, sequence, attempts, outputs, and errors. Caught failures
set the run's had-failures marker but do not directly stop scheduling. Repeated
calls receive distinct history identities. Existing graph-node state and events
are untouched. Store IDs hash an unambiguous parent/type/invocation tuple in a
reserved namespace rather than exposing delimiter ambiguities.

An explicit value-evaluation mode is inherited by synthetic contexts. It rejects
impure calls, tool calls, and parking during pure execution rather than relying
on input shape. Worker slots are reacquired against the parent run context after
a child-only deadline so script code cannot continue outside the worker bound.

Script variable writeback compares canonical before/after values for run, flow,
and global scopes. It applies only keys the script changed, retains persistent
deletion behavior, and leaves writes made by called nodes intact unless the script
deliberately changed the same key. Called nodes still see the script's initial
snapshot rather than uncommitted script writes.

`Engine.TestNode` clones the full registry before wrapping the tested node, so a
script tested in isolation sees the same callable catalog as a deployed run.
The standalone `/scripts/test` request has no invoker and therefore no `nodes`
global.

### Static and runtime validation

`pkg/scriptsdk` owns shared call validation and the goja AST checker.
`pkg/scriptverify` dispatches by configured language without making that shared
package import the Python runtime. CPython's `ast` parses and extracts literal
Python calls inside the installed wasm runtime; `pkg/scriptsdk` uses mvdan's
parser directly for Bash `node` commands and literal shell-word JSON. All three
checkers recognize
canonical, alias, and generic forms and validate statically inspectable argument
counts, types, config objects, inputs, params, options, and state IDs. Dynamic
expressions and shell expansions are left for runtime checks rather than executed
or guessed. Unknown config keys and opaque capability ports are rejected.

The shared checks cover catalog/schema rules, port shapes, and invocation options.
There is no general pure `ValidateConfig` hook on nodes or external plugins, so
node-specific duration, range, connection, and cross-field checks that exist only
inside `Execute` remain runtime failures. Verification never calls `Execute`, a
connection, or a plugin to improve diagnostics.

The checker returns diagnostics with severity, stable code, source range,
line/column, message, and related node type. `POST /api/scripts/verify` returns
these diagnostics without running nodes or integrations. `pkg/flowops` converts
them to existing validation details on strict paths. Draft saves continue to use
only lenient graph validation.

Runtime invocation uses the same config and invocation checks, plus live type
availability and pure/message-path context. Validation errors have zero attempts
and never execute the target node.

### Editor declarations and analysis

A pure frontend generator turns the function catalog into ambient declarations.
Config interfaces are closed, unconditional required fields are required,
conditional fields remain optional, select values become literal unions, and
stored field and port IDs are preserved with quoted properties where needed.
`value_schema` and `payload_schema` drive structured types. Broad, custom, or
unresolved dynamic payloads become `any`. JSON Schema `additionalProperties` is
honored, so only explicitly closed objects reject unknown members.

Every ordinary output is a required property containing a message array, matching
runtime normalization. Invocation options include per-call retry only for
retryable types and a required `id` for stateful or unknown-state types. JSDoc carries node
labels, descriptions, field help, required/default information, output labels,
and retry/purity notes. Contributed metadata is sanitized before entering
comments.

A lazy TypeScript language-service worker analyzes JavaScript with `checkJs`,
`strict: false`, no emit, no automatic type-package discovery, and no DOM or Node
libraries. Locally bundled ES declarations cover ordinary JavaScript built-ins.
Ambient declarations cover the sandbox globals and `nodes`; browser and host
APIs are absent.

The virtual source is wrapped in a function to preserve top-level `return`.
One offset-mapping module handles every completion, hover, signature, and
diagnostic query and result, including UTF-16 positions and stale revisions.
Worker responses carry document and catalog revisions so an old response cannot
overwrite newer analysis.

CodeMirror uses the worker for JavaScript completion with lazy entry details,
hover quick info, signature tooltips with active arguments, and syntactic plus
semantic lint diagnostics. Python and Bash use catalog-driven local adapters for
function/member completion, hover, and signatures, plus debounced server
verification for syntax and literal-call diagnostics. Conservative direct-call
and direct-assignment analysis adds closed-schema output-member diagnostics;
open or unresolved results produce no guessed findings. Responses carry language,
document, and catalog revisions; offsets convert from server source coordinates
to editor coordinates at this boundary. Documentation renders as text. The worker
or language adapter terminates on editor close or language/context change, with
the existing editor behavior as fallback.

`ConfigPanel` enables analysis only for exact node type `script`. The context
passes through `ConfigForm` and `DocumentField` to `ScriptEditor`; every other
caller defaults to none. A script SDK store refreshes on flow-editor entry and
plugin catalog changes, while runtime availability remains authoritative.

### Metadata

Add `value_schema` and `payload_schema` to the frontend catalog types. Add a
detailed schema to `ha-get-state` so its state payload properties and closed
shape provide a real completion and diagnostic fixture. Other nodes keep their
broad types until their own schemas are filled in; the generator does not invent
certainty.

Detailed interface shapes and examples are in [contracts/script-sdk.md](contracts/script-sdk.md)
and [contracts/python-bash-sdk.md](contracts/python-bash-sdk.md). Identity and
diagnostic fields are in [data-model.md](data-model.md). Research and rejected
alternatives are in [research.md](research.md).

## State and data model

No persistent schema changes are required. Function catalogs and revisions are
derived from the live registry. Invocation evidence is additive to existing run
history. A stateful or unknown-state invocation's durable key consists of flow ID, parent script
node ID, and explicit invocation ID; per-run sequence is never persisted as node
state. Deleting a flow removes that state through existing cleanup.

No draft or deployment format changes are needed. Node-call source is ordinary
script configuration and freezes with the flow version as before.

## Verification

- **Server**: table-driven catalog and static-call contract tests; real goja,
  CPython wasm, and mvdan runtime tests for results, structured failures, and
  capability absence; engine tests for retries, cancellation, purity, budgets,
  history, store identity, variable preservation, and outer-retry suppression.
  API tests exercise all three languages without actions. Strict flow tests prove
  diagnostics do not block draft saves.
- **Frontend**: Vitest covers declaration serialization, hostile metadata,
  broad/closed schemas, diagnostic cases, JavaScript/Python/Bash completion,
  hover, signature help, source mapping, stale responses, and language/context
  switching. `svelte-check` remains in the same Makefile target.
- **Live**: Follow [quickstart.md](quickstart.md) with safe base64 and fail/fallback
  fixtures in every language through MCP. Check exact run IDs and events. Exercise
  production completion, hover, signatures, diagnostics, and language/context
  switching in the browser, with console cleanliness. Keep visual-only drafts
  unsaved and remove disposable functional flows.

## Risks and open decisions

- Static analysis cannot validate arbitrary computed JavaScript, Python, or shell
  expressions. Runtime validation is mandatory and complete static certification
  is not claimed.
- Dynamic ports and broad payload schemas reduce editor precision to `any`.
  Config-aware backend validation still applies where literals allow it.
- The TypeScript worker is a sizeable lazy asset. It is excluded from ordinary
  editor and document paths and requires production-bundle verification.
- Calls share the script wall-clock budget, so a long wait can time out the
  script even though engine parked time is discounted from the separate run
  budget. Changing that relationship is outside this feature.
- Parallel calls, asynchronous script execution, and automatic code/graph
  conversion remain out of scope. Python verification needs the installed wasm
  asset, matching Python execution; Bash and JavaScript remain compiled in.
