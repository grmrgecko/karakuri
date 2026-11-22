# Design plan: Durable waits and device state

**Feature**: `specs/029-durable-waits` | **Date**: 2026-10-30 | **Spec**: [spec.md](spec.md)

## Summary

Timer and fan-in nodes keep their pending work in a new persisted wait store
instead of the memory-only node store. The node logic stays where it is; nodes
store their state in a JSON-safe shape, and random tokens replace generation
counters and pointer identity. Shutdown and redeploy cancel runs with a
suspension cause, which leaves stored waits in place. `Runner.Rearm` then
reconciles the flow's waits against the deployed graph and starts one resume
run per wait at the waiting node, before triggers are armed. A resume run calls
the node's new `Resume` method, which parks until the stored deadline and
emits after a synchronous claim. The engine applies the per-node missed-deadline
rule before resuming. The MQTT device relay persists merged state through a
coalescing writer and restores it marked stale.

[research.md](research.md) records the decisions, [data-model.md](data-model.md)
the tables and node states, [contracts/](contracts/) the node API and the REST,
MCP, and broker shapes, and [quickstart.md](quickstart.md) the checks.

## Technical context

- Language and dependencies: Go 1.26.5, pure Go, GORM on SQLite (ncruces) or
  PostgreSQL. No new dependencies.
- Components:
  - `pkg/nodeapi`, `pkg/engine`: `WaitParker`, `WaitStore`, `Wait`, `Resumer`,
    `ErrSuspended`, `Suspended`. A new `pkg/engine/waitstore.go` holds the
    registry, the sink, and the claim. `RunOptions` gains `ResumeNodeID`,
    `ResumeKey`, `ResumeWait`, and `ResumedFromRunID`, and `run.go` calls
    `Resume` instead of `Execute` for that node. `Engine.Stop` and
    `CancelFlowRuns` take a cause. Engine methods `PendingWaits`,
    `CancelWait`, `LoadWaits`, and `DropFlowWaits` are added.
  - `pkg/engine/runner.go`: reconcile and resume in `Rearm`, resume queue with
    admission backoff, and dropping waits on disable.
  - `pkg/nodes/core`: `trigger.go`, `delay.go` (all four modes), `wait.go`,
    `conditionwait.go`, `group.go` and the join, batch, aggregate, and
    sequence-mode limit and sort nodes move their pending state to the wait
    store, add `Resume`, and declare `engine.MissedDeadlineFields()`. Shared
    helpers are in `pkg/nodes/core/waits.go`.
  - `pkg/models`: `PendingWait`, `MQTTDeviceState`, `Run.ResumedFromRunID`;
    the flow delete transaction and the MQTT device delete remove their rows.
    `pkg/migrate` registers the tables.
  - `pkg/flowops`: `Deploy` cancels with `ErrSuspended`; `SetEnabled(false)`
    drops waits; list and cancel operations shared by REST and MCP.
  - `pkg/api/flow_wait_handler.go`, `pkg/mcpserver/tools_state.go`
    (`store: "waits"`, `cancel_flow_wait`).
  - `pkg/mqttrelay/device.go`: stale flag, `reportedAt`, restore on `start`,
    coalescing writer, and row cleanup on delete or kind change.
    `pkg/nodes/mqttnodes/devices.go` adds the read metadata.
  - `cmd/karakuri/serve.go`: load waits before `runner.Start()`, start and
    stop the device state writer.
  - Frontend: a pending waits section in
    `frontend/src/lib/components/editor/RunPanel.svelte` with a cancel action;
    the resumed-from link in run details; a stale mark and report time in
    `frontend/src/routes/settings/MQTTDevices.svelte` and the device widgets
    fed by `dashboardLive.ts`.
  - Docs: `docs/flow-authoring.md` (timer recovery, redeploy, missed-deadline
    fields), `docs/mqtt-device-state-script.md` (stale state), `docs/mcp.md`.
- Storage: tables `pending_waits` and `mqtt_device_states`, plus column
  `runs.resumed_from_run_id`. The resume queue, write queue, and stale flags
  are in memory.
- Limits: 1 MiB encoded wait state; `missed_grace` default 30 seconds;
  admission backoff 1–30 seconds; device state coalesced to one write per
  device per 5 seconds. Existing limits are unchanged: the 64 KiB run event
  payload cap, retention of 100 runs, and the five-second engine stop wait.

## Constitution check

- **Failures**: this feature narrows the "no restart recovery" exclusion for
  the listed nodes only. The spec and docs state at-most-once delivery across
  a crash, loss of one in-flight delivery, and in-memory fallback for waits
  that cannot be stored. In-flight graphs elsewhere still do not resume, and
  feature 003 keeps that statement. A claim error fails the execution through
  normal failure routing without emitting. Resume runs keep cancellation, run
  timeout (excluding parked time), and retry. `Resume` is not retried, because
  the action it follows has already happened.
- **Drafts and deployment**: test runs of drafts never store waits. Only
  enabled deployments resume, and a redeploy resumes into the new deployment.
  Listing and cancelling go through `flowops`, so REST, MCP, and the editor
  share one rule set. New node fields come from the catalog, so editor ports
  and validation agree.
- **Permissions**: listing requires `api:runs:read` and cancelling requires
  `api:runs:cancel`, the same as the existing run endpoints. Device state
  responses keep the dashboard-scoped filtering of `/mqtt_devices`.
- **Existing state**: the new tables and nullable column are additive through
  AutoMigrate. Durable node store rows, variables, control values, encrypted
  secrets, CA material, and enrolled identities are untouched. Rate-limit
  schedules stay in `DurableStore`, so existing `next_at` rows remain in use.
  No break.
- **Verification**: `make dev`, `make test TARGET=server`,
  `make test TARGET=frontend`, the service restart, MCP checks, and the browser
  check in [quickstart.md](quickstart.md).

## Design and contracts

### Storing and claiming

`waitStore.get(flowID, nodeID)` returns a node's store. `ExecContext` from a
deployed run implements `WaitParker`. For test runs, `WaitStore` returns a
memory-only store, so test behavior stays as it is. `Update` serializes
`State` with a 1 MiB cap, writes through the single writer queue, and returns
`durable == false` on an encoding, size, or queue failure. The node then
records one `not durable` debug event. `Claim` takes the store lock, checks the
token, deletes the row in memory, enqueues the database delete, and waits for
it to commit. At-most-once delivery requires `Claim` to also succeed for a
memory-only wait.

### Node changes

The control flow of each node stays the same. Only where the state lives and
how ownership is checked change:

- **Trigger**: `Execute` arms `arm:<key>` with the message, second payload
  source, and deadline. Reset deletes the row. Restart writes a new token.
  Extend moves the deadline. After the first emit, it parks with
  `awaitDeadline`, refreshes the deadline from the row, and claims before it
  returns `expired`. `Resume` starts at the park.
- **Delay fixed/random**: arm `exec:<id>` with the release time. On park
  completion, claim and emit. Reset still does not touch these waits.
- **Delay debounce**: the window row holds the latest message, deadline, and
  maximum. Arrivals replace the message under the lock. The owner claims at
  quiet-period end. `superseded` keeps its current meaning.
- **Delay rate queue**: each queued message is a row `queue:<key>:<seq>` that
  holds the epoch token it was queued under. Reset replaces `epoch:<key>`, and
  a woken message whose epoch does not match emits on `superseded`. On a
  restore, queued messages release at the configured interval from the restart
  in sequence order. With drop configured, rows whose slot passed during the
  outage are dropped.
- **Wait until**: row `exec:<id>` with the target instant.
- **Condition wait**: row `exec:<id>` with the message and absolute timeout.
  `Resume` re-registers the check waiter and polls again. The hold duration
  restarts. A missed timeout with deliver emits `timeout`.
- **Join, Batch, Aggregate**: `collect` keeps parts in `group:<id>`. The row
  has a deadline only when a timeout is set. The timeout owner claims and
  emits the partial group. The completing run claims with the owner's token,
  so exactly one of them emits.

### Suspension, restore, and redeploy

```text
serve start: LoadNodeState → LoadWaits → runner.Start()
  Rearm(flow): load deployed graph
    → reconcile waits: drop rows whose node is missing or retyped (canceled run each)
    → queue resumes for rows with a deadline, in deadline order
    → arm triggers
  resume: missed by > grace and drop → canceled run; else RunFlow(ResumeNodeID…)
Deploy: CancelFlowRuns(ErrSuspended) → Rearm
Disable: Disarm → CancelFlowRuns(canceled) → DropFlowWaits (canceled run each)
Delete: Disarm → delete transaction removes rows → purge memory
Stop: runner.Stop → eng.Stop (base cancel with ErrSuspended) → sinks close
```

A suspended run settles as `canceled` with error
`suspended; pending waits are carried over`. A redeploy waits for the old runs to settle
(up to the five-second stop bound) before reconciling, so an old waiter cannot
claim after its resume starts. A claim race is still harmless, because only
one token holder can claim.

### Missed deadlines and admission

The engine reads `missed_deadline` and `missed_grace` from the node config in
the current deployment. If lateness is greater than the grace and the setting
is `drop`, the wait is deleted and a canceled run is recorded. Otherwise
`RunFlow` starts the resume, and every message the run emits from the waiting
node carries `meta.resumed`. `ErrRunLimit` puts the wait back in the queue
with backoff. Each resume starts only after the previous one has parked
or finished (bounded at two seconds of wall time), so waits that compete for
something, such as a rate limit's slots, are restored in deadline order. The
registry records which run owns each wait's token and forgets it when that run
settles; a rearm while a run is still parked, such as one after a connection
change, resumes only the waits that have no live owner. Disarm discards the queue but leaves the rows, because a disarm
for a redeploy is followed by a rearm.

### Inspection and cancel

`flowops.ListWaits` reads the engine registry, which includes memory-only
waits. `flowops.CancelWait` deletes the row, records the cancellation run, and
leaves the waiter to find its claim refused. This matches how Trigger reset
behaves today. REST and MCP shapes are in [contracts/api.md](contracts/api.md).

### MQTT device state

`start` loads `mqtt_device_states` for the device, seeds `state`, and sets
`stale` and `reportedAt` without fanning out. `receive` clears `stale`, sets
`reportedAt`, and marks the device dirty. A writer goroutine flushes dirty
devices every five seconds and on `Stop`. `Reload` deletes the row when the
control kind changes. Device delete removes it in the same request. A write
failure is logged at error and retried on the next flush.

## State and data model

See [data-model.md](data-model.md). The wait registry is authoritative while
the engine runs, and the table is its durable copy, the same arrangement as
the durable node store. The flow delete transaction owns row removal, and the
engine purges memory afterwards. A wait row belongs to one flow and one node
ID. Its token names the single execution that may emit it.

## Verification

| Scenario | Test |
| --- | --- |
| US1, SC-001 | Engine tests with the fake clock: arm, stop the engine, start a new engine on the same database, and resume at the original deadline. Covers reset and extend after restore, and keyed independence. Live: quickstart 1. |
| US2, SC-002 | Resume with a deadline before the clock, for deliver, drop, and drop within grace. Run rows and `meta.resumed` are checked. Live: quickstart 2. |
| US3, SC-003 | `flowops.Deploy` with a node removed, a node retyped, and a duration changed; disable drops. Live: quickstart 3. |
| FR-008 | Fault-injected sink: a claim whose delete fails does not emit; a crash simulated after the claim restores nothing. |
| FR-009 | A message over 1 MiB stays in memory, is listed with `durable: false`, and is not restored. |
| Per node | Table-driven tests in `pkg/nodes/core` against a real SQLite wait store, for each mode in the node-state table. |
| US4 | Handler and MCP tests for list and cancel permissions and responses. Browser: RunPanel waits section, settled screenshot, console check. |
| US5, SC-005 | `mqttrelay` tests: restore marks stale, no fanout, merge clears, kind change deletes. Live: quickstart 6. |
| SC-006 | Migration test on a database fixture with existing node store and control rows. Live: quickstart 7. |

Run `make dev`, `make test TARGET=server`, `make test TARGET=frontend`, restart
`karakuri-dev.service`, and confirm it is active and answering on
`http://localhost:8080`.

## Risks and open decisions

- A restored wait whose downstream branch depended on run-scoped variables set
  before the wait sees them unset. The spec states this. The flow guide needs
  an example that uses message-scoped data instead.
- A wait cancelled through the API keeps its waiter parked until the deadline,
  which holds one run slot, the same as Trigger reset today. Waking the waiter
  early would need a signal channel per wait. It is deferred unless run-limit
  pressure shows up.
- Extending a Trigger writes one queued row update per message. A motion
  sensor chattering every second is one SQLite write per second per key, which
  the single writer absorbs. Coalescing extends is possible later if it
  measures as a problem.
- External plugin nodes that park keep today's behavior until they implement
  `Resumer`. The plugin docs will say so.
