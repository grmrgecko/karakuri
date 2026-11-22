# Feature specification: Script node SDK

**Feature**: `specs/026-script-node-sdk`
**Created**: 2026-10-26
**Status**: Implemented
**Input**: Expose executable nodes as typed functions inside the existing script node, with generated editor completion, hover documentation, and script verification. Add visible diagnostics for script and node-call mistakes before execution. Extend the same invocation, discovery, failure, and verification contracts to Python and Bash with language-idiomatic calls and error handling.

## User scenarios and testing

### User story 1: Orchestrate nodes from a script (Priority: P1)

An author uses an ordinary trigger followed by a script node, then calls catalog nodes from code with conditional logic instead of wiring every step. The author supplies node configuration and input messages, reads returned output messages, and handles failures in code.

**Why this priority**: This provides the requested code-first automation model while retaining the flow lifecycle and node catalog.

**Independent test**: Run a script that reads a value through one node function and conditionally invokes an action through another. Inspect the returned values, action count, failures, and run history.

**Acceptance scenarios**:

1. **Given** a deployed trigger and script using two eligible node functions, **When** the trigger starts a run, **Then** the script receives each function's declared outputs and only the branch selected by the code performs its action.
2. **Given** a called node with a retry policy, **When** its initial attempts fail, **Then** the configured attempts are recorded and an exhausted failure is catchable by the script without an unhandled failure ending that run.
3. **Given** an uncaught called-node or script failure, **When** the script node finishes unsuccessfully, **Then** its ordinary failure routing applies and earlier successful calls are not replayed by an outer script retry.
4. **Given** a computed node configuration, **When** the function is invoked, **Then** its type, configuration, inputs, and parameters are validated before the node performs an action.
5. **Given** equivalent JavaScript, Python, and Bash scripts, **When** they invoke the same node and handle a failure, **Then** each receives equivalent outputs and failure evidence through its own data and error conventions, with the same invocation history and execution budgets.

### User story 2: Discover node functions and their contracts (Priority: P1)

An author writing JavaScript, Python, or Bash receives completion for node functions, configuration properties, allowed values, input and output ports, and documented payload properties. Hover and signature help explain the selected function and its options using the same catalog available to the graph editor, including contributed nodes. Each language presents these contracts through its own call and result syntax.

**Why this priority**: Authors should not need to memorize the catalog or maintain a separate hand-written SDK reference.

**Independent test**: Open the flow script editor against a catalog with built-in and contributed nodes, request completion and hover information, and compare the offered functions, fields, and outputs with their catalog definitions.

**Acceptance scenarios**:

1. **Given** the flow script editor, **When** the author requests node completion or types a function argument, **Then** every eligible catalog function and its documented configuration signature are available without hand-written per-node completion entries.
2. **Given** a function or one of its documented options or outputs, **When** the author hovers it, **Then** its description, type, required or default information, and available help are shown.
3. **Given** a node with detailed payload schemas, **When** the author reads a returned output payload, **Then** its documented properties complete with their types; a node without a detailed schema falls back to its declared broad payload type without invented properties.
4. **Given** a device, payload-transform, or remote-agent script editor without node-call capability, **When** completion is requested, **Then** server flow node functions are not offered.
5. **Given** equivalent Python and Bash scripts, **When** the author requests function, option, output, hover, or signature information, **Then** the presented names, stored identifiers, types, and documentation match the same active catalog and the language's actual call and result shapes.

### User story 3: See mistakes before running (Priority: P2)

An author sees localized diagnostics while editing for syntax errors, unknown functions, incorrect argument counts, missing or unknown configuration properties, invalid value types or choices, and invalid input or output usage. Saving an incomplete draft remains possible, while test and deployment checks report statically known executable errors.

**Why this priority**: Immediate feedback prevents avoidable test runs and failed device actions without changing draft lifecycle rules.

**Independent test**: Open a script containing a fixed set of invalid calls, inspect the editor diagnostics without executing the script, then verify the corresponding strict check and runtime rejection of a computed invalid call.

**Acceptance scenarios**:

1. **Given** a script with an unknown function, a missing required option, an unknown option, a wrong option type, and an unknown documented output property, **When** the author edits it, **Then** each statically knowable mistake has a localized diagnostic and no node action occurs.
2. **Given** a script with diagnostics, **When** the author saves the flow draft, **Then** the draft is retained; a test or deployment of the affected executable branch reports the statically known errors instead.
3. **Given** a computed invalid configuration that static analysis cannot inspect, **When** its function is called, **Then** validation rejects it before action and the script receives a failure.
4. **Given** a node with no detailed payload schema, **When** its broad declared type is used, **Then** verification does not report unsupported property errors merely because detailed metadata is absent.
5. **Given** statically invalid Python or Bash node calls, **When** the author edits, tests, or deploys them, **Then** localized diagnostics use the selected runtime's syntax and source locations; saving the draft remains allowed and no call executes during verification.

### Edge cases

- Trigger, annotation, graph-boundary, graph-only routing, nested-script, and fan-in constructs are not eligible as ordinary synchronous functions and are not advertised as such. Nodes requiring opaque capability values that cannot cross the script data boundary are also excluded.
- A pure value evaluation may invoke only pure node functions; an attempted action call fails before performing the action.
- Every call observes cancellation and the active execution budgets. Repeated calls in a loop have distinct invocation records, and stateful or unknown-state calls use an explicit stable invocation identity. Bash calls run as simple foreground commands; background, pipeline, computed-or-escaped-command, wrapper, alias, dynamic-evaluation, and function-redefinition forms that can conceal unsafe execution are rejected before admission.
- The number of calls and the data returned from one script execution are bounded; exceeding the count bound fails the script without starting another call, even if code catches the reported error.
- A called node that emits no messages returns an empty output collection rather than fabricating a result.
- Static diagnostics do not execute nodes or integrations. Dynamic references and computed configuration remain subject to runtime validation.
- Node availability can change with contributed catalogs; an unavailable function fails clearly and never falls back to a different node type.
- Earlier external effects are not rolled back when a later call or the script fails.

## Requirements

### Functional requirements

- **FR-001**: The system MUST expose every eligible ordinary executable catalog node as a synchronous named function or command within the existing server flow JavaScript, Python, and Bash script nodes, including eligible built-in, plugin, and agent nodes, without requiring a separate automation artifact or graph edges between calls.
- **FR-002**: The system MUST expose the exact registered node type as the canonical call target in every supported language, with optional unambiguous lower-camel aliases and a generic dynamic-call form. Every advertised target MUST map back to that exact type and MUST NOT substitute a different implementation when the type is unavailable. Python MUST use an object/function interface and Bash MUST use a shell command interface suited to their existing runtime contracts.
- **FR-003**: Node functions MUST accept node configuration, optional input messages keyed by input port, and invocation options; they MUST return emitted messages keyed by output port while preserving payload, topic, metadata, and grouping information and MUST NOT implicitly execute downstream graph nodes.
- **FR-004**: The system MUST validate the node type, configuration, input ports, value parameters, and invocation options before every call performs an action, including when those values were computed at runtime.
- **FR-005**: Node calls MUST use the active run's connection resolution, cancellation, worker coordination, execution budgets, and per-call retry policy. Once any script-hosted node call has begun execution, an outer script retry MUST NOT replay that call or earlier calls; retries belong to the individual invocation.
- **FR-006**: An exhausted node-call failure MUST be catchable in each supported language with its node identity, attempt count, and available classified failure details: a structured JavaScript or Python exception, or a Bash nonzero status with a structured error document. An uncaught exception or a Bash failure propagated as the script's exit status MUST fail the script node and use its normal failure routing; explicitly handling that status MUST allow a fallback.
- **FR-007**: Run history MUST identify the parent script, stable node type, invocation sequence, attempts, outputs, and failures for each call so repeated invocations remain distinguishable.
- **FR-008**: Pure script evaluation MUST reject calls to impure nodes, including transitively through called execution contexts. Calls to stateful or unknown-state nodes MUST use an explicit stable invocation identity so transient and durable node stores cannot depend on source line numbers or call order. Audited stateless nodes MAY omit it.
- **FR-009**: The system MUST bound node invocations per script execution and invocation result data per call, stop admitting calls after the count bound is reached, fail the script even if count exhaustion is caught in code, and reject oversized results without delivering them to the script.
- **FR-010**: The system MUST derive node-function names, descriptions, configuration signatures, required and default values, choices, help, inputs, outputs, payload types, availability, invocation-state requirements, and retry metadata from the active node catalog rather than maintaining independent hand-written SDK declarations. Contributed nodes with unknown invocation state MUST NOT be treated as audited stateless nodes.
- **FR-011**: The JavaScript, Python, and Bash flow script editors MUST provide function and member completion, signature help, hover documentation, and returned-output member completion from the derived catalog contract, using completion forms valid for the selected language. Config and port identifiers MUST retain their stored names.
- **FR-012**: Each supported flow script editor MUST provide localized diagnostics for syntax errors, unknown functions, wrong argument counts, missing or unknown configuration properties, incorrect value types or choices, and statically invalid input or output usage. Assistance for one language MUST NOT change the syntax, globals, commands, or capabilities advertised for another.
- **FR-013**: Strict script verification MUST use the selected target runtime's syntax rules and apply shared catalog and invocation validation to statically inspectable calls without executing script bodies, node actions, or integrations. Diagnostics MUST identify severity, source location, a stable code, and a useful message. Node-specific checks with no pure validation contract remain runtime failures.
- **FR-014**: Script diagnostics MUST NOT prevent saving a draft. Test and deployment validation MUST reject statically known errors in the executable script, while computed calls MUST remain subject to runtime validation.
- **FR-015**: Verification MUST model each actual script sandbox and its available globals or commands rather than browser, host-runtime, or another language's capabilities. Missing detailed schemas MUST produce the declared broad type, not fabricated properties or unsupported strictness errors.
- **FR-016**: Node-function completion and invocation MUST be available only in supported server flow script contexts with node-call capability; device scripts, payload transforms, remote-agent scripts, and unrelated document editors MUST retain their existing capabilities. Adding Python and Bash support MUST NOT add invocation to their standalone, device, transform, or remote-agent execution paths.
- **FR-017**: Successful script variable writeback MUST apply only values the script itself changed, preserving concurrent writes made by called nodes. Deletions in persistent scopes MUST retain their existing behavior, and interpreter failure MUST continue to discard staged script writes.

### Key entities

- **Node function contract**: Stable function name and node type, configuration and input signature, invocation options, output messages, documentation, availability, purity, retry, and state requirements derived from the catalog.
- **Node invocation**: One synchronous call within a script run, with a stable invocation identity, sequence, configuration, inputs, attempts, outputs, and failure result.
- **Script diagnostic**: One author-facing verification result with severity, source range, stable code, message, and optional related documentation.

## Success criteria

- **SC-001**: A two-function test script reads a value and conditionally performs one action, with both returned outputs and both invocation records available; changing the condition performs no action and starts no action invocation.
- **SC-002**: A retry fixture records the configured attempts for one call, and a caught failure allows a subsequent fallback call; an outer script retry performs no duplicate earlier action.
- **SC-003**: Every eligible type in a mixed built-in and contributed test catalog has a generated function contract, and each contract's config fields and declared outputs match the catalog without per-type editor entries.
- **SC-004**: A fixture containing five specified authoring mistakes (unknown function, missing required option, unknown option, wrong option type, and unknown documented output property) reports all five localized diagnostics before execution and performs no action.
- **SC-005**: A draft containing diagnostics saves successfully, its strict executable check reports the errors, and a computed invalid call is rejected before its test action executes.
- **SC-006**: A called node changes one flow variable while the script changes another; successful completion preserves both, a caught call-limit exhaustion still fails the script, and an interpreter failure commits neither script write.
- **SC-007**: Equivalent Python and Bash fixtures each complete a transform call, catch a failed call and inspect its attempts, preserve the non-replaying and call-budget rules, and produce localized strict diagnostics without executing the invalid call. Their editors expose the same catalog contracts with no node assistance in non-flow contexts.

## Assumptions and scope

- JavaScript, Python, and Bash share invocation, validation, failure, discovery, and verification behavior while retaining their existing message, variable, output, binary, and error conventions. Python calls use dictionaries and exceptions; Bash calls use JSON documents and exit statuses.
- Calls are synchronous and sequential in every language. Parallel invocation, asynchronous script syntax, automatic graph conversion, and triggers registered from script code are outside this feature.
- Ordinary nodes with meaningful single-invocation behavior and serializable data contracts are eligible. Triggers, annotations, subflow boundaries, nested scripts, fan-in constructs, and nodes requiring opaque capability handles or graph routing rather than one execution are excluded.
- Detailed payload-property completion depends on catalog schemas. Broad port types remain available for nodes whose payloads are dynamic or not yet documented in detail.
- Existing script timeout, output, network, and sandbox limits remain in force. Node invocation is a mediated capability and grants no direct filesystem, process, environment, socket, driver, or credential access. The `script.network` policy governs only the script's own HTTP helper; called HTTP, file, plugin, and agent nodes retain the same authority and policy they have on the canvas.
- Existing flow drafts, deployed graphs, node stores, connection configurations, identities, and encrypted settings remain readable. New invocation identities must not change the storage identity of existing graph nodes.
- This feature extends specifications 002, 003, 005, and 008 where their lifecycle, retry, history, sandbox, and contributed-catalog contracts intersect script node calls.

## References

- [Flow graphs and deployment](../002-flow-authoring/spec.md)
- [Execution, failures, and persistent state](../003-flow-execution/spec.md)
- [Sandboxed logic and server file access](../005-script-sandbox/spec.md)
- [Plugins and extensible catalogs](../008-plugin-system/spec.md)
- [Scripting guide](../../docs/scripts.md)
- [Flow authoring guide](../../docs/flow-authoring.md)
