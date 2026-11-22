# Research: Durable waits and device state

## Resume the waiting node instead of replaying its output

- **Decision**: a restored wait starts a run at the waiting node and calls a
  new `Resume` method on the node type. `Resume` parks until the stored
  deadline through the same code the node uses today, then emits. The engine
  never emits stored outputs on the node's behalf.
- **Rationale**: the nodes already own their retrigger, reset, extend, quiet
  period, and condition logic. A reset that arrives after a restart must still
  cancel the wait, an extension must still move it, and Condition wait must
  keep observing. Emitting stored outputs from the engine at the deadline
  would need a second copy of every one of those rules.
- **Alternatives**: an engine timer wheel that fires stored outputs (duplicates
  node logic and cannot observe a Condition wait); replaying the original input
  through `Execute` (repeats the Trigger's immediate message and rearms with a
  fresh duration).

## A separate wait store

- **Decision**: add `ExecContext.WaitStore()`, a persisted per-node store with
  its own table, `pending_waits`. It sits next to `NodeStore` (memory only,
  purged on rearm) and `DurableStore` (persisted, purged only on flow delete).
- **Rationale**: the lifecycle the spec requires matches neither existing
  store. Waits must survive restart (unlike `NodeStore`), must be dropped on
  disable and for removed nodes on redeploy (unlike `DurableStore`), and need
  a deadline, a token, and the arming run as columns so the engine can resume,
  list, and claim them without decoding node state.
- **Alternatives**: put waits in `DurableStore` (survives disable, and leaves
  engine-visible fields inside opaque JSON); make `NodeStore` persistent
  (changes the meaning of state that many nodes rely on being reset by a
  redeploy).

## Tokens replace pointer identity and generations

- **Decision**: each wait carries a random token. A waiter holds the token it
  armed with. Reset deletes the row, restart writes a new token, and extend
  keeps the token and moves the deadline. A waiter emits only after a claim
  that deletes the row if its token still matches.
- **Rationale**: Trigger compares integer generations, and Delay and fan-in
  compare `*debounceState`, `*rateEpoch`, and `*partGroup` pointers. Neither
  survives a JSON round trip or a restart. A deleted-and-recreated row would
  also restart an integer generation and let a superseded waiter match. A
  random token keeps "is this still my wait" correct across both.
- **Alternatives**: persist the generation counter separately (needs a row that
  outlives the wait it counts).

## Claims are synchronous; other writes are queued

- **Decision**: arming, extending, and reset writes go through a single-writer
  queue like the durable node store sink. A claim enqueues its delete and waits
  for it to commit before the node emits. Close drains the queue on shutdown.
- **Rationale**: FR-008 forbids a second delivery after a crash. A queued
  delete that had not committed when the process died would restore an
  already-emitted wait. Making only the claim synchronous puts one database
  write on the delivery path and keeps arming off it. Because claims share the
  writer queue, they commit after every earlier write for that wait.
- **Alternatives**: synchronous writes for everything (a database write on
  every motion event that extends a Trigger); a fully queued design (a crash
  can deliver twice).

## Suspension is a cancellation cause

- **Decision**: the engine's base context and `CancelFlowRuns` take a cause.
  Shutdown and redeploy cancel with `engine.ErrSuspended`; disable, operator
  cancel, and run timeout keep their current causes. Nodes check
  `engine.Suspended(ctx)` on a park error and leave their wait stored instead
  of releasing it.
- **Rationale**: today a cancelled waiter releases its arm. That is still right
  for an operator cancel or a disable, and wrong for a shutdown or a redeploy
  that is about to resume it. `context.Cause` carries the distinction without
  a new channel.
- **Alternatives**: a flag on the run (nodes cannot see it without a new
  `ExecContext` method, and it races with cancellation).

## Where restored runs come from

- **Decision**: `Runner.Rearm` resumes waits. After it loads the deployed
  graph and before it arms triggers, it reconciles the flow's stored waits
  against the graph, drops those whose node is missing or changed type, and
  starts one resume run for each remaining wait with a deadline.
  `Runner.Start` therefore restores at startup, and `flowops.Deploy` restores
  after a redeploy, through the same path. `Disarm` from `SetEnabled(false)`
  drops the flow's waits.
- **Rationale**: Rearm already runs at exactly the points where waits must be
  restored, holds `armMu`, and has the deployed graph and version. Restoring
  before triggers start satisfies the spec's "reset right after startup sees
  the restored state".
- **Alternatives**: a separate restore pass in `serve.go` (redeploy would need
  its own copy).

## Missed deadlines are applied by the engine

- **Decision**: `missed_deadline` (`deliver` default, `drop`) and
  `missed_grace` (duration, default `30s`) are config fields that every
  resumable node declares through a shared field helper. The engine reads them
  before calling `Resume`. A drop records a settled run and never calls the
  node. Late delivery calls `Resume`, whose park returns at once, and the engine
  adds `meta.resumed` to every emitted message.
- **Rationale**: one rule for every node. The decision needs only the stored
  deadline and the config, and keeping it in the engine prevents nodes from
  drifting apart. Thirty seconds covers startup and redeploy gaps without
  hiding a real outage.
- **Alternatives**: per-node handling (seven copies); a field on `flow.Node`
  like `retry` (these settings apply to one node family, not every node).

## Run history for restored, dropped, and carried waits

- **Decision**: a resume run is an ordinary run with `TriggerNodeID` set to
  the waiting node, plus a new nullable `resumed_from_run_id` column on `runs`.
  A drop for a missed deadline, a removed node, or a disable is recorded as a
  run that settles immediately as `canceled`. Its error names the reason and
  the original deadline. A suspended run settles as `canceled` with error
  `suspended; pending waits are carried over`.
- **Rationale**: the spec needs these events visible in run history. Existing
  run statuses, list views, and retention apply without a new status or a new
  frontend state.
- **Alternatives**: a `suspended` run status (adds a state to every client);
  writing events into the finished arming run (it may already be pruned by the
  100-run retention).

## Admission control for resumes

- **Decision**: a resume that hits the per-flow run limit is retried with
  backoff from 1 to 30 seconds until admitted or until the flow is disarmed.
  Resumes start in deadline order.
- **Rationale**: the spec forbids silently dropping a restored wait. Each wait
  needed its own run before the restart, so the configured limit already
  allowed them.

## Stored message size

- **Decision**: a wait whose encoded state exceeds 1 MiB stays in memory and is
  not stored. Its run records a `not durable` debug event.
- **Rationale**: webhook bodies are capped at 1 MiB, so ordinary messages fit.
  An image held in a delay should not put megabytes into a SQLite row on every
  extend.

## MQTT device state

- **Decision**: a new table `mqtt_device_states` holds each device's merged
  state and `reported_at`. The `Devices` relay writes it through a coalescing
  writer that saves each changed device at most once every five seconds and
  flushes on stop. `start` seeds a device's state from the row and marks it
  stale. The first merged report clears the flag. The row is deleted when the
  device is deleted or its control kind changes.
- **Rationale**: devices such as power meters report every second, and this
  state is only last-known. Five seconds bounds both write volume and what a
  crash can lose. Restored values do not fan out as `DeviceChange`, so a
  restart does not fire MQTT device triggers on old values.
- **Alternatives**: a column on `mqtt_devices` (the definition row would be
  rewritten every few seconds and would race with editor saves); retained-only
  recovery (many devices do not retain).

## Nodes left out

- **Decision**: fixed and random Delay, debounce, queued rate limit, Trigger,
  Wait until, Condition wait, Join, Batch, and Aggregate resume.
  Home Assistant state holds, HA confirmation, Script, browser prompts, HTTP
  pagination, and ping do not.
- **Rationale**: these match the spec's scope section. The excluded nodes
  depend on live external state or on an exchange that cannot be resumed.
