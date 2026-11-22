# Design plan: Execution, failures, and persistent state

**Feature**: `003-flow-execution` | **Date**: 2026-10-04 | **Spec**: [spec.md](spec.md)
**Status**: Implemented baseline

## Summary

Execute deployed or explicitly tested graphs with bounded concurrency, inspectable
failures, cancellation, and scoped state.

This plan records the design supporting the baseline requirements. It is not
a pending implementation queue. Requirement IDs are local to this feature.

## Technical context and design

`pkg/engine` owns graph flattening, scheduling, per-run workers, parameter
pulls, tool calls, retry policy, cancellation, trigger subscriptions, and
script-hosted node invocation. Execution uses a context and injectable clock.
Script calls execute synchronously against synthetic node instances in the active
run, with no downstream delivery. Their own retry policy applies; once any call begins
execution it suppresses the outer script retry so earlier effects cannot repeat. Flatten
local/shared subflows before scheduling and retain boundary type validation.
Bypass rewrites routing; fan-out creates message-group instances; execute-once
claims span those instances.

Resolve parameters before the attempt loop. Node failures route through resolved
failure ports after action attempts. Signal-only executions skip parameter pulls
and execute-once claims. Signal inputs never supply primary or bypass payloads.
Interruptible ParkUntil waits for a timer, signal or cancellation while releasing
the worker, then reacquires it before a live execution resumes. Context wrappers
forward this capability. Failed parameter resolution bypasses the attempt
loop and the consumer's error port; pure pulls also have no action retry loop.
Active tool-call failures return to the waiting call. Run
completion invokes only enabled, deployed error-handler flows and marks their
runs so they never start another error handler.

## State and data model

Run and node-instance state are in memory. Pending waits of the timer and
fan-in nodes are the exception: feature 029 stores them in `pending_waits`,
suspends runs with `ErrSuspended` on shutdown and redeploy, and resumes each
wait in a new run linked through `runs.resumed_from_run_id`. Script invocations receive unique
per-run history identities and a separate stable store identity beneath the
calling script node; existing graph-node store IDs do not change. Invocation
metadata rides in existing run-event JSON, so no migration is required.
`pkg/engine/history.go` persists run rows and events through a queue of 1024
writes; payloads above 64 KiB receive truncation markers. Terminal persistence
waits at most five seconds for room. Both ordinary and terminal writes can be
dropped under queue pressure, so the 100-run retention rule describes persisted
history, not guaranteed capture. `models.ReplaceLastRun` prunes completed history
to 100 per flow, retaining active runs. Variables and durable node stores flush
asynchronously. Normal shutdown flushes writers; abrupt termination can lose
pending state.

## Contracts and failure handling

Messages carry payload, topic, and metadata. Port types constrain payloads.
Value edges route no messages; successful pure outputs are memoized per run and tool args
are resolved per call. Node states include pending, running, retrying, succeeded,
failed, and skipped. A handled failure still records a failed node. Cancel is
separate from ordinary failure. APIs can return a run before its history writes
land; inspection may need to wait for persistence.

The execution semaphore bounds active calls, not all run goroutines. `Park` releases
its slot and discounts intervals while any node is parked from the run timeout, even
if another branch is active. Retry waits retain their slot and do not begin a park.
Zero run timeout disables the watchdog; zero concurrent-run limit allows unlimited runs.
Successful pull output collections are cached; failed producer calls are not.

Cancellation halts scheduling and drains active calls cooperatively. Script-hosted
calls observe both the run context and the script timeout, share the caller's worker
slot, and record caught failures without directly halting scheduling. Pure value calls
reject impure types. Stateful and unknown-state calls require an explicit invocation ID. An unhandled
failure halts scheduling without canceling other active calls. A context-ignoring node
can keep an ordinary run unsettled. Engine shutdown waits five seconds for runs, logs
abandonment if necessary, then flushes persistence sinks; the run wait does not bound
the sink flushes.

`pkg/http/webhook.go` dispatches armed tokens, checks effective `RemoteAddr` and optional
HMAC, and reads at most 1 MiB. The cap truncates rather than issuing a size error.
Synchronous calls default to a 30-second response wait and clear the HTTP write
deadline. Losing that caller or its response slot does not cancel the flow run.
The router exposes GET/POST only; the registry accepts broader method declarations.
Enabling proxy trust rewrites `RemoteAddr` before the webhook check, so forwarded
headers can affect its IP allowlist. Duplicate tokens use the last armed registration.

## Constitution check

- Keep the authorization and failure boundaries described above explicit.
- Route authoring through shared operations and preserve draft/deployment separation
  where applicable.
- This baseline adds no schema, wire, identity, or encryption-format migration. Future
  changes must assess the affected persistent state before implementation.
- Use the component checks below and the service handoff checks in `AGENTS.md`.

## Verification

Run `make dev TARGET=server` and `make test TARGET=server`. Engine tests cover retry
delays, fan-out, cancellation, pull caching, tool calls, script invocation, bypass,
and persistent stores.
MCP checks should inspect the exact returned run ID and its events after persistence
settles. Use disposable flows for actions and report any records created.

These are the acceptance checks for changes to this feature. They do not claim
that every live scenario ran while the baseline documents were written.

## Risks and limits

Tests can perform real actions. Run history is bounded diagnostic evidence. The engine
does not guarantee recovery of in-flight work or external exactly-once side effects.

New behavior needs reviewed requirements and an update to this plan.

## References

- [Server and agent audit](../server-agent-audit.md)

- [System architecture](../architecture.md)
- [docs/flow-authoring.md](../../docs/flow-authoring.md)
- [pkg/engine/retry.go](../../pkg/engine/retry.go)
- [pkg/engine/runner.go](../../pkg/engine/runner.go)
- [pkg/engine/history.go](../../pkg/engine/history.go)
- [pkg/engine/pull.go](../../pkg/engine/pull.go)
- [pkg/engine/toolcalls.go](../../pkg/engine/toolcalls.go)
- [pkg/models/run.go](../../pkg/models/run.go)

Explicit condition observations use isolated fresh pure dependency evaluations;
ordinary per-run memoization is retained. Fresh observations skip old message-path
outputs and do not replace the original run snapshot. Condition wait uses
interruptible ParkUntil for polls and optional change-trigger checks, with a
bounded deadline and sampled hold duration, preserving its input message
on matched/timeout outputs. See [feature 015](../015-stateful-flow-nodes/spec.md).

## HTTP retry policy extension

`nodeapi.ExecutionError` carries retry permission, server minimum delay and
JSON failure details for built-in actions. The engine retains plain-error
behavior and cancellation, applies optional policy jitter then server minimum
and cap, and attaches structured details to error-branch metadata. No plugin
wire format changes are required; external plugin string errors retain their
existing retry behavior. HTTP classifies permanent/config/response errors and
applies method/status policy before allowing retries. Error body reads are
bounded to 64 KiB; pagination buffers until all requested pages succeed.

## Browser prompt waits

[Feature 022](../022-agent-browser-prompts/plan.md) registers transient server
questions, delegates only launch to Open URL, and waits using ParkUntil. Request
response/expiry transitions are atomic. Normal run cancellation, disable and
redeploy invalidate pending capabilities. Choice outputs preserve the message;
there is no scheduler re-entry or automatic retry policy.
