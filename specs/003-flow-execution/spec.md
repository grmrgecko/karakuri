# Feature specification: Execution, failures, and persistent state

**Feature**: `003-flow-execution`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Execute deployed or explicitly tested graphs with bounded concurrency,
inspectable failures, cancellation, and scoped state. Execute script-hosted node
calls with invocation evidence and non-replaying failure behavior under feature 026.

## User scenarios and testing

### User story 1: Recover from an action failure (Priority: P1)

An author gives a flaky action bounded retries and routes exhausted failures to a
fallback.

**Why this priority**: A device failure must have a predictable effect on the run.

**Independent test**: Use a controlled failing node with retries and both wired and
unwired error outputs.

**Acceptance scenarios**:

1. **Given** a message-path action with resolved parameters and three allowed attempts,
   **When** the first two calls fail and
   the third succeeds, **Then** three attempts are recorded and execution continues.
2. **Given** a failure output is wired and attempts are exhausted, **When** the action
   fails, **Then** the failure message follows that branch and is marked handled.
3. **Given** a script that completed an action call before a later uncaught failure,
   **When** its outer retry policy would run, **Then** the completed call is not replayed;
   individual script-hosted calls instead follow their own policies under feature 026.

### User story 2: Inspect and stop a run (Priority: P1)

An operator sees node results, cancels active work, and tests a draft with a chosen
payload.

**Why this priority**: Operators need to identify and stop unexpected side effects.

**Independent test**: Start a waiting draft, read its run ID and events, then cancel it.

**Acceptance scenarios**:

1. **Given** a running graph parked on a delay, **When** the operator cancels the run,
   **Then** the wait and pending work observe cancellation and the run settles.
2. **Given** several possible trigger starts, **When** a test omits its start selection,
   **Then** the ambiguous test is refused rather than choosing a trigger silently.
3. **Given** a one-minute engine run budget, **When** a delay parks for ten minutes,
   **Then** that parked interval does not exhaust the budget and execution can resume.

### User story 3: Use scoped state (Priority: P2)

An author binds pure values and stores variables for a message, run, flow, or the whole
server.

**Why this priority**: Stateful automation needs clear ownership and lifetime.

**Independent test**: Pull one pure producer through several parameters and restart
after storing a flow variable.

**Acceptance scenarios**:

1. **Given** one successful pure producer feeding several value parameters, **When**
   consumers execute, **Then** the producer is evaluated once for that run.
2. **Given** persisted flow and global variables, **When** the server restarts normally,
   **Then** those values remain, while run and message state do not become persistent.

### Edge cases

- Parameter resolution errors can end execution before an action attempt; retrying the
  action does not re-pull its parameters.
- Unhandled errors inside a tool-call branch become tool observations; unrelated run
  failures still follow normal error routing.
- History and state persistence are asynchronous; events can be dropped when queues fill
  and are not an exactly-once audit log.
- Parked waits release their execution slot and pause the run budget while any node is
  parked, even if other branches are active. Retry backoff holds its slot and does not
  itself pause that budget.
- Cancellation stops scheduling and asks active nodes to stop; settling waits for their
  return. An unhandled action failure stops new scheduling but lets active nodes finish.
- A failed pure producer execution is not memoized and can be evaluated again by another
  consumer; successful output collections are shared for the run.

## Requirements

### Functional requirements

- **FR-001**: The system MUST run enabled trigger events against immutable deployments
  and explicitly selected tests against their strictly validated draft execution graph.
- **FR-002**: The system MUST seed manual and schedule tests from their configuration;
  require test parameters for other triggers unless an explicit payload overrides them.
- **FR-003**: The system MUST enforce configured per-run active node execution and
  per-flow concurrent-run limits; a zero concurrent-run limit MUST mean unlimited runs.
  A positive engine run timeout MUST request cancellation after the configured unparked elapsed
  time; zero MUST disable that timer. Time while any node is parked MUST be excluded,
  so this timeout MUST NOT be described as a wall-clock duration cap. Park MUST release
  its execution slot while waiting; retry backoff MUST retain that slot.
  Interruptible signal waits MUST reacquire that slot before a live execution resumes.
- **FR-004**: The system MUST apply effective retry attempts and delay/backoff/jitter/cap
  settings to message-path action execution, with cancellation during execution and
  retry waits. Parameters MUST resolve before this attempt loop and stay fixed across
  attempts; on-demand pure pulls MUST execute without that retry loop. Script-hosted
  calls MUST use their own invocation policies, and once any call has begun execution an
  outer script retry MUST NOT replay it or earlier calls under feature 026.
  Executions receiving only declared `signal` inputs MUST skip parameter pulls
  and execute-once claims; signal inputs MUST NOT supply primary or bypass payloads.
- **FR-005**: The system MUST route exhausted action-execution failures through their
  wired declared failure output and distinguish handled from unhandled failures.
  Parameter-resolution failures MUST bypass the consumer's failure output and fail the
  run, or become an observation when contained in an active tool branch.
- **FR-006**: The system MUST start the configured error-handler flow for unhandled run
  failures only when the handler is enabled and deployed; error-handler runs MUST NOT
  start another error handler.
- **FR-007**: The system MUST contain tool-branch unhandled failures as observations for
  the waiting model call.
- **FR-008**: The system MUST support message fan-out and grouped downstream execution,
  gated inputs, bypass routing, and execute-once behavior.
- **FR-009**: The system MUST pull pure parameter producers on demand, memoize successful
  output collections per run, and resolve tool argument values per call.
- **FR-010**: The system MUST support msg, run, flow, and global variable scopes;
  persist flow/global values and durable node stores across ordinary restarts.
- **FR-011**: The system MUST expose run identity, status, node states, attempts,
  errors, handled-failure markers, and recorded output events. Script-hosted calls MUST
  additionally expose their parent script and distinct invocation identity and sequence
  under feature 026.
- **FR-012**: The system MUST retain the most recent 100 completed runs per flow plus
  active runs, with access to an exact retained run ID.
- **FR-013**: The system MUST keep event persistence bounded and asynchronous, truncate
  stored payloads above 64 KiB, and report persistence failures. Its 1024-write queue
  MUST drop ordinary writes when full; terminal writes MUST wait at most five seconds
  for room before being dropped with a warning.
- **FR-014**: The system MUST cancel live run contexts and stop pending work during
  shutdown, waiting at most five seconds for run goroutines before logging abandonment
  and proceeding to flush persistence sinks. This wait MUST NOT imply forced interruption
  of a node that ignores its context or a total shutdown bound including sink flushes.
  Do not claim automatic resumption of in-flight graphs after restart; pending
  waits of the nodes named in feature 029 are instead restored as new runs from
  the waiting node under that feature.
- **FR-015**: The system MUST arm webhooks only for enabled deployed triggers, enforce
  configured methods, IP allowlists against the request's effective `RemoteAddr`, and
  optional HMAC over the bytes read, and decode the configured body format.
  Body reads MUST stop at 1 MiB; oversized text or
  binary input is truncated rather than rejected by size alone. Asynchronous requests
  MUST return 202 after emission; synchronous requests MUST wait for a flow response
  under their configured timeout, defaulting to 30 seconds. Client disconnect or response
  timeout MUST release the response slot without canceling the independent flow run.
  The public router MUST expose GET and POST only, even when node configuration names
  other methods; duplicate armed tokens MUST use the last registration and log a warning.

### Key entities

- Run and node instance: one graph execution and its per-message-group node activity.
- Run event: ordered recorded state/output evidence with bounded payload storage.
- Variable and node store: values with explicit message/run or persistent
  flow/global/node lifetime.

## Success criteria

- **SC-001**: The controlled retry fixture reaches the configured attempt count and
  records the expected delay progression.
- **SC-002**: A handled node failure leaves a successful fallback path; an unhandled one
  produces failed run status and the configured error flow.
- **SC-003**: A canceled parked run settles without executing its downstream action.
- **SC-004**: One successful pure producer is pulled once per run and persisted scope
  values survive a normal restart.
- **SC-005**: A script-hosted retry fixture records distinct invocation attempts and a
  caught failure permits a fallback, while an outer script retry performs no duplicate
  earlier action.

## Assumptions and scope

Tests can perform real actions. Run history is bounded diagnostic evidence. The engine
does not guarantee recovery of in-flight work or external exactly-once side effects,
except that pending waits restore under [feature 029](../029-durable-waits/spec.md).
Run cancellation is cooperative. Ordinary runs can remain unsettled if a node ignores
its context. The execution-slot limit bounds active calls, not the number of parked or
slot-waiting goroutines.

## References

- [Design plan](plan.md)
- [Script node SDK](../026-script-node-sdk/spec.md)
- [Durable waits](../029-durable-waits/spec.md)
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

## Classified action failures

Built-in nodes can return explicit permanent/transient failures with a server
minimum delay and bounded JSON details. Plain errors retain the effective retry
policy. Jitter is an optional 0–1 fraction applied before the server minimum;
`max_delay_ms` caps the final wait. Invalid attempt/delay/backoff/jitter settings
are rejected by graph validation and execution. Error messages preserve the
input envelope and add failure metadata.

HTTP defaults retry GET/HEAD/OPTIONS and selected transient statuses only.
Other methods require explicit opt-in. TLS verification, config, decode and
size failures are permanent. Retry-After accepts unsigned seconds and HTTP
dates; response details exclude URL credentials and credential-bearing headers.
See `../015-stateful-flow-nodes/` for the full node contracts.

## Human browser questions

[Feature 022](../022-agent-browser-prompts/spec.md) holds one incoming message
through an interruptible server wait while parallel branches continue. A single
valid answer emits through its selected output with response metadata. Timeout
and launch failures use Error. Cancellation revokes the question's capability.
