# Tasks: Script node SDK

**Feature**: `specs/026-script-node-sdk`
**Input**: spec.md and plan.md in that directory, plus research.md, data-model.md,
contracts/script-sdk.md, and quickstart.md.

## Task format

Every task uses a checkbox and sequential `T###` ID. `[P]` marks work that can
proceed in parallel after its dependencies. Story labels map to spec.md. Tests
cover serialization boundaries and real engine/editor behavior, not restatements
of helpers.

## Phase 1: Required preparation

- [x] T001 Add audited `InvocationState` metadata with stateless, stateful, and unknown semantics in `pkg/flow/meta.go`; annotate stateful built-ins and preserve unknown state for contributed metadata in `pkg/pluginhost` and `pkg/agenthub`.
- [x] T002 Implement the derived function catalog in `pkg/scriptsdk/catalog.go` with exact-type canonical names, unambiguous lower-camel aliases, trigger/routing/nested-script/fan-in/opaque-capability exclusions, ordered contracts, and revision digests in `pkg/scriptsdk/catalog_test.go`.
- [x] T003 Implement shared invocation validation in `pkg/scriptsdk/validate.go` and narrowly exported config helpers in `pkg/flow`, covering unknown/missing config, retry, message inputs, params, binary payloads, state identity, and eligible dynamic ports; add valid-plus-invalid contract cases in `pkg/scriptsdk/validate_test.go`.

## Phase 2: User story 1

**Goal**: Orchestrate eligible nodes synchronously from the existing script node.
**Independent check**: A script reads through one function, conditionally invokes another, catches failures, and shows distinct invocation evidence without replaying earlier calls.

- [x] T004 [US1] Add optional node-call types and capability contracts in `pkg/nodeapi`, store IDs and inherited value-evaluation context in `pkg/engine/execctx.go` and `pkg/engine/run.go`, and refactor `pkg/engine/retry.go` so invocation can use the script context without changing graph retry behavior.
- [x] T005 [US1] Implement synchronous invocation in `pkg/engine/invoke.go` with direct params, all observed outputs, no graph delivery, pure restrictions, per-call retries, catchable classified errors, bounded results of 1000 messages and 1 MiB, unique history, stable hashed store IDs, and real-engine coverage in `pkg/engine/invoke_test.go`.
- [x] T006 [US1] Install exact-type and alias JavaScript functions plus `nodes.call` only for capable requests in `pkg/sandbox/js.go`; add the 100-call fatal budget and outer-retry suppression in `pkg/nodes/core/script.go`, with structured error and capability tests in `pkg/sandbox/sandbox_test.go` and `pkg/nodes/core/script_test.go`.
- [x] T007 [US1] Change script run/flow/global writeback to canonical before/after diffs in `pkg/nodes/core/script.go`, preserving called-node writes, persistent deletions, and interpreter-failure rollback; add a reentrant write regression in `pkg/nodes/core/script_test.go`.
- [x] T008 [US1] Clone the complete registry and forward invocation capability for isolated script tests in `pkg/engine/nodetest.go` and `pkg/engine/nodetest_test.go`, while leaving standalone `/scripts/test` without `nodes` in `pkg/api/script_test_handler.go`.

## Phase 3: User story 2

**Goal**: Generate JavaScript function, option, output, hover, and signature information from the active catalog.
**Independent check**: Built-in and contributed metadata produce matching completion and documentation in the core flow script editor and nowhere else.

- [x] T009 [P] [US2] Serve the versioned function catalog from `pkg/api/nodetype_handler.go` or a focused script SDK handler, then add its typed client and revision-aware store in `frontend/src/lib/api/client.ts` and `frontend/src/lib/stores`; add an API contract test in `pkg/api`.
- [x] T010 [P] [US2] Add direct TypeScript, CodeMirror lint, and Vitest dependencies in `frontend/package.json` and `frontend/package-lock.json`, and wire the focused frontend tests into `Makefile` without removing `svelte-check`.
- [x] T011 [P] [US2] Implement sanitized declaration generation in `frontend/src/lib/components/editor/scriptSdk.ts`, covering exact names and aliases, closed configs, conditional fields, select unions, invocation identity and retry options, quoted IDs, broad fallbacks, JSON Schema additional-properties behavior, and hostile metadata in `scriptSdk.test.ts`.
- [x] T012 [US2] Implement the lazy TypeScript language-service worker and locally bundled ES declarations in `frontend/src/lib/components/editor/scriptAnalysisService.ts`, `scriptAnalysis.worker.ts`, `scriptAnalysisClient.ts`, and `scriptAnalysisLibs.ts`, with no DOM or Node globals; test the five diagnostic fixtures, completion, hover, signature, top-level return, stale revisions, and UTF-16 offsets in `scriptAnalysisService.test.ts` and `scriptAnalysisClient.test.ts`.
- [x] T013 [US2] Implement CodeMirror completion, hover, signature, and diagnostic adapters in `frontend/src/lib/components/editor/scriptAnalysisCodeMirror.ts` and the corresponding styles in `frontend/src/lib/utils/scriptLang.ts`, using text-only documentation and revision-aware responses.
- [x] T014 [US2] Pass an exact-core-script-only analysis context through `ConfigPanel.svelte`, `ConfigForm.svelte`, `DocumentField.svelte`, and `ScriptEditor.svelte`; manage worker lifecycle and language switching without changing MQTT, agent, Markdown, JSON, Python, or Bash editors.
- [x] T015 [P] [US2] Add `value_schema` and `payload_schema` to frontend catalog types in `frontend/src/lib/stores/nodeCatalog.ts` and publish a detailed closed `ha-get-state` output schema in `pkg/nodes/hanodes/ha.go` for real returned-member completion and diagnostics.

## Phase 4: User story 3

**Goal**: Show statically knowable script mistakes before execution while retaining lenient drafts and runtime checks for computed calls.
**Independent check**: Five invalid-call fixtures produce localized diagnostics without actions; drafts save, strict checks fail, and computed invalid input stops before execution.

- [x] T016 [P] [US3] Implement goja-backed parsing and literal-call analysis in `pkg/scriptsdk/check.go` and `pkg/scriptsdk/ast.go`, returning ordered diagnostics with severity, stable codes, source ranges, line/column, and related type for canonical, alias, and generic calls in `pkg/scriptsdk/check_test.go`; computed expressions remain unchecked rather than certified.
- [x] T017 [US3] Apply static diagnostics on every strict path in `pkg/flowops` without rejecting draft saves, and expose bounded `POST /api/scripts/verify` diagnostics through the same checker in `pkg/api/script_test_handler.go`; add strict-versus-draft and no-action API tests in `pkg/flowops` and `pkg/api`.

## Final verification and documentation

- [x] T018 Update `docs/scripts.md`, `docs/flow-authoring.md`, `specs/spec-kit.md`, `specs/README.md`, and feature 002/003/005/008 plans to match final function, failure, state, testing, network-policy, and editor behavior.
- [x] T019 Run `make dev TARGET=server`, `make dev TARGET=frontend`, `make test TARGET=server`, and `make test TARGET=frontend`; perform the safe MCP function, retry, variable, catalog, and strict-validation checks in `specs/026-script-node-sdk/quickstart.md`.
- [x] T020 Exercise production JavaScript completion, hover, signature help, diagnostics, language switching, and context exclusions in the browser; capture settled screenshots, inspect the console, and leave visual-only drafts unsaved.
- [x] T021 Run `systemctl --user restart karakuri-dev.service`, verify the unit is active and `http://localhost:8080` answers, review `git status` and the final diff for unrelated state or generated artifacts, and commit the coherent verified changes.

## Phase 5: Python and Bash language parity

**Goal**: Offer the same orchestration, discovery, failure, and verification contracts through idiomatic Python and Bash bindings without widening their sandboxes.
**Independent check**: Each language completes a transform, catches a retry failure, preserves invocation budgets and state, shows catalog-derived editor help, and rejects literal mistakes during strict verification without executing them.

- [x] T022 [US1] Implement Python `nodes` exact-type, alias, and generic bindings with dictionary results and `karakuri.NodeCallError` in `pkg/sandbox`, passing `sandbox.Request.NodeInvoker` only from the existing capable script path; add real-wasm success, failure, and capability-absence coverage in `pkg/sandbox` tests.
- [x] T023 [US1] Implement the Bash `node` builtin with JSON config/input/options arguments, exact-type/alias/generic targets, JSON output and error documents, nonzero failures, serialized foreground calls, rejected background calls, and the complete-message `MSG` variable in `pkg/sandbox`; add shell success, caught-failure, and capability-absence coverage in `pkg/sandbox` tests.
- [x] T024 [US1] Exercise per-call retries, outer-retry suppression, pure restrictions, fatal call budgets, invocation history, and variable preservation through both new language bindings in `pkg/nodes/core` and real-engine tests without changing `pkg/engine/invoke.go` semantics.
- [x] T025 [P] [US3] Add language-dispatched verification and CPython AST literal-call analysis with original-source diagnostics in `pkg/scriptverify`, `pkg/scriptsdk`, and `pkg/sandbox`, keeping user source and node bodies unexecuted and computed expressions runtime-checked.
- [x] T026 [US3] Add mvdan parser-based Bash verification for literal `node` calls, shell-word JSON extraction, dynamic fallbacks, and original-source diagnostics in `pkg/sandbox` and `pkg/scriptverify` tests.
- [x] T027 [US3] Extend `POST /api/scripts/verify` and every strict flow validation path to Python and Bash in `pkg/api/script_verify_handler.go` and `pkg/flowops`, adding draft-versus-strict and no-action API tests for both languages.
- [x] T028 [US2] Add catalog-driven Python and Bash function/member completion, hover, signature help, and revision-aware server diagnostics in `frontend/src/lib/components/editor` and `frontend/src/lib/api/client.ts`, scoped to the exact core flow script context without loading the TypeScript worker.
- [x] T029 [US2] Add focused Vitest coverage for Python and Bash completion contexts, stored config/port IDs, documentation, diagnostics, stale responses, and language/context switching in `frontend/src/lib/components/editor` tests.
- [x] T030 Update `docs/scripts.md`, `docs/flow-authoring.md`, `specs/spec-kit.md`, `specs/architecture.md`, and feature 005/026 requirements, plans, contracts, data model, research, and quickstart for the final multi-language behavior.
- [x] T031 Run `make dev` and `make test`, then perform safe MCP transform, caught-retry, outer-retry, variable, call-budget, pure-call, and strict-versus-draft checks for Python and Bash; remove all disposable flows and record evidence in `specs/026-script-node-sdk/quickstart.md`.
- [x] T032 Exercise production Python and Bash completion, hover, signature help, diagnostics, language switching, and non-flow context exclusion in the browser, capture settled screenshots and console results, then verify `karakuri-dev.service`, review the worktree, and commit coherent changes.

## Dependencies and execution order

T001 through T003 establish the shared catalog and validation contract. After
that foundation, Phase 2 runtime work, Phase 3 editor work, and Phase 4 static
verification can proceed independently. T009 and the frontend dependency or
declaration tasks marked `[P]` can overlap. T016 can overlap runtime and editor
work but must precede T017. Final documentation and live checks depend on all
implementation phases.

The runtime MVP is T001 through T008: a real JavaScript script can call nodes
safely before rich editor support arrives. The complete user-facing feature
requires all three story phases. For the language extension, T022 and T023 can
proceed independently and must precede T024. T025 and T026 can overlap after
their dispatch boundary is agreed, then feed T027; they share the dispatch package
and are not parallel tasks. T028 and T029 depend on the
verification endpoint contract but not on runtime completion. T030 through T032
follow the implementation. No task authorizes a branch, push, external
publishing, or destructive test action.
