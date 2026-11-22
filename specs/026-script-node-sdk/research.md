# Research: Script node SDK

## Execution boundary

**Decision**: Add an optional node-invocation capability to the active execution
context. The server script node passes it to the JavaScript sandbox only when it
exists. Calls execute synthetic node instances in the current run and never
route outputs to graph edges.

**Rationale**: The current run already owns connections, variables, stores,
retry policy, worker slots, cancellation, and history. Reusing those paths keeps
a function call consistent with the same node on the canvas. An optional
capability leaves standalone, device, transform, and remote-agent scripts
unchanged. Node invocation is separate from the `script.network` policy; called
nodes keep their canvas authority rather than inheriting the HTTP helper's policy.

**Alternatives considered**: Calling `NodeType.Execute` directly from the script
node would bypass retries and history. Starting an isolated node test for each
call would lose flow state and durable identity. A separate orchestration node
was rejected because the requested home for the API is the existing script node.

## Retry, purity, and identity

**Decision**: Give each invocation its own retry policy and history identity.
Suppress the outer script retry after any call begins execution. Pure value
execution rejects impure calls, including transitively. Stateful and unknown-state
types require `options.id`, and store identity is separate from per-run history identity.

**Rationale**: Replaying a script after one action succeeded could repeat that
action. Source lines and loop order are not stable durable identities. A pure
pull must not become a hidden action path.

**Alternatives considered**: Memoizing completed actions across an outer retry
would imply recovery and exactly-once behavior the engine does not have.
Deriving identity from a config hash would change state when dynamic config
changes and would still be opaque to the author.

## Callable catalog

**Decision**: Derive function contracts from the active registry. Exclude
triggers, annotations, link and subflow routing constructs, nested scripts, and
fan-in constructs that require graph message scheduling. Keep generic
`nodes.call` for dynamic types and naming collisions.

**Rationale**: One catalog drives canvas forms, runtime functions, strict
verification, and editor declarations. Not every graph construct has meaningful
single-call behavior.

**Alternatives considered**: Advertising every registered type would expose
pseudo-nodes that cannot execute and recursive scripts with independent call
budgets. Hand-written function wrappers would drift from plugin and agent
catalogs.

## Invocation validation

**Decision**: Share node and invocation validation between runtime calls and a
static JavaScript checker. Strict flow operations run the static checker after
ordinary graph validation. Draft saves do not reject script diagnostics.
Computed values are checked again immediately before execution.

**Rationale**: The browser can provide fast feedback, but only the server has the
actual goja grammar, active registry, and node validation rules. The same
operation layer serves deployment, test, REST, and MCP without duplicating
policy.

**Alternatives considered**: TypeScript-only validation would disagree with
goja and cannot apply server node rules. Executing calls to validate them would
risk real actions and is not acceptable for editor feedback.

## Editor analysis

**Decision**: Generate ambient TypeScript declarations from the server function
catalog and query a TypeScript language service in a lazy module worker. Use it
only for the core flow JavaScript script editor. goja remains the runtime.

**Rationale**: TypeScript already provides contextual completion, quick info,
signature help, and semantic diagnostics for JavaScript with `checkJs`. A worker
keeps analysis off the UI thread. Passing the context only through the core
script-node path prevents leakage into MQTT, device, agent, Markdown, and JSON
editors.

**Alternatives considered**: Hand-maintained CodeMirror completion lists cannot
check argument types or returned payloads. A server-hosted language service
would add latency, document synchronization, and another long-lived service
without improving execution authority.

## Declarations and source mapping

**Decision**: Use closed config interfaces, literal unions for select fields,
optional conditional fields, port-keyed message arrays, and JSON Schema-derived
payload types where available. Use `any` for broad or unresolved dynamic types.
Analyze source inside a virtual function wrapper and map every query and result
back to original offsets.

**Rationale**: Closed configs expose unknown and missing options. Conditional
fields cannot be unconditionally required. JSON Schema allows additional
properties by default, so a closed result is justified only when the schema says
so. The wrapper preserves the existing top-level `return` contract without
misplaced diagnostics.

**Alternatives considered**: Generating snake_case-to-camelCase option aliases
would create a second config contract. Treating all schemas as closed would
report valid dynamic payloads as errors. Loading standard declarations from a
CDN would make editing depend on a third party.

## Sandbox library

**Decision**: Bundle the required ES declaration files locally and exclude DOM,
Node, browser, and host type packages. Declare only the existing sandbox globals
and the new `nodes` object.

**Rationale**: Completion must not offer `fetch`, timers, `process`, `Buffer`,
`require`, or other APIs the interpreter cannot provide. The editor still needs
ordinary JavaScript arrays, objects, maps, sets, and JSON to work without missing
standard-library diagnostics.

**Alternatives considered**: A minimal handwritten standard library would be
small but would drift from TypeScript's parser and ordinary JavaScript member
completion. The default TypeScript DOM library would describe the wrong runtime.

## Frontend verification

**Decision**: Add a small Vitest setup for declaration generation and language
service contracts, wire it into `make test TARGET=frontend`, and retain browser
checks for the real worker and CodeMirror interactions.

**Rationale**: Generated declarations and source-offset mapping are external
format boundaries that benefit from direct tests. The existing frontend has no
JavaScript test runner, so `svelte-check` alone cannot exercise them.

**Alternatives considered**: Browser-only checks would make the five diagnostic
fixtures slower and harder to isolate. Adding tests without Makefile wiring
would leave them unused.

## Multi-language invocation

**Decision**: Reuse `ScriptNodeInvoker` unchanged. Add Python `nodes` bindings
and a Bash `node` builtin beside the JavaScript binding, installed only when the
request carries the engine capability. Python uses dictionaries and
`karakuri.NodeCallError`; Bash uses JSON documents and command statuses.

**Rationale**: Invocation identity, retry, purity, history, variable, and budget
rules already belong to the engine and core script tracker. Duplicating them in
new runtimes would let the languages disagree. Object exceptions do not translate
to shell, where `if result=$(node ...); then ... else ... fi` is the ordinary
caught-failure form.

**Alternatives considered**: Emulating JavaScript property calls in shell would
require a second parser and would not be usable Bash. Returning an in-band result
for every failure would let an ignored failure look successful. Giving Bash only
a human-readable error would discard the node type and attempt evidence available
in the other languages.

## Bash message input

**Decision**: Expose the complete first input message as `MSG` JSON beside the
existing payload, topic, metadata, and port-payload variables. Pass it inside a
port-keyed inputs object such as `inputs="{\"in\":$MSG}"`.

**Rationale**: Node calls require complete message envelopes. The existing Bash
variables can express payloads but cannot safely construct an envelope with a
JSON-quoted topic without adding a general JSON encoder. A complete message
variable solves the common case without changing the invocation schema.

**Alternatives considered**: Accepting payload-only input shorthand would make
Bash's call contract differ from the engine and the other languages. Adding a
JSON-encoding utility would be broader than this feature. Reading envelopes from
temporary files would violate the existing no-filesystem boundary.

## Language verification boundary

**Decision**: Dispatch strict verification by configured language through a
small package that can import both the shared catalog and the runtimes. Keep
goja and mvdan AST checks in `pkg/scriptsdk`, and CPython AST checks with the
Python runtime. Share `ValidateCall` and diagnostic codes across all three.

**Rationale**: `pkg/sandbox` already imports `pkg/scriptsdk` for runtime
validation, so making the shared package import the Python and Bash runtimes
would create a cycle. Each target runtime owns its syntax, while the shared
package owns invocation rules. Static analysis parses source and extracts
literals; it never runs the submitted body.

**Alternatives considered**: A third-party Python parser would add a second
grammar that can disagree with CPython. Regular expressions cannot distinguish
calls from strings, comments, nested expressions, or shell expansions. Runtime
registration callbacks would hide checker availability and make strict validation
depend on import order.

## Multi-language editor analysis

**Decision**: Keep the TypeScript worker for JavaScript. Use the same function
catalog for lightweight Python and Bash completion, hover, and signature
adapters, and request debounced diagnostics from `/api/scripts/verify` with
revision and language checks. Do not load TypeScript for the other languages.

**Rationale**: TypeScript cannot parse Python or shell. The server already has
the authoritative parsers and invocation validation, and the editor has flow
write permission. Catalog adapters provide immediate local help while server
responses supply syntax and literal-call diagnostics. Non-flow editors still
receive no context and no capability.

**Alternatives considered**: Running a second language service for each language
would duplicate server validation and add sizeable assets. Client-only regex
diagnostics would misparse valid source. Using the existing JavaScript worker
for all languages would report irrelevant syntax and sandbox capabilities.

## Source positions and runtime availability

**Decision**: Keep server diagnostic offsets in original-source coordinates and
convert them to editor coordinates at the UI boundary. Python verification uses
the installed CPython wasm asset, matching Python execution; a missing asset is
reported explicitly rather than replaced with another parser.

**Rationale**: CPython and mvdan positions do not share JavaScript's string
encoding conventions. One conversion at the editor boundary prevents misplaced
squiggles. Python's runtime is already an explicit installation requirement, and
silently substituting another parser would weaken the verification contract.

**Alternatives considered**: Returning editor-specific offsets from the server
would couple the API to CodeMirror. Skipping Python syntax checks when the asset
is absent would present an unverified script as verified.
