# Tasks: Durable waits and device state

**Feature**: `specs/029-durable-waits`
**Input**: [spec.md](spec.md), [plan.md](plan.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/node-api.md](contracts/node-api.md),
[contracts/api.md](contracts/api.md), [quickstart.md](quickstart.md).

## Phase 1: Shared preparation

- [X] T001 Add `models.PendingWait` in `pkg/models/pending_wait.go` with the columns in data-model.md (unique `flow_id`+`node_id`+`key`; nullable `deadline`, where null means "state only, with no waiter to resume"), plus list, upsert, delete, and per-flow delete helpers. Register it in `pkg/migrate/migrate.go` and delete the flow's rows in the `(*Flow).Delete` transaction in `pkg/models/flow.go`
- [X] T002 [P] Add nullable `ResumedFromRunID *uuid.UUID` (`resumed_from_run_id`) to `models.Run` in `pkg/models/run.go` and carry it through `RunSnapshot` and run history writes in `pkg/engine/history.go`
- [X] T003 [P] Add `models.MQTTDeviceState` (`device_id` primary key, `state` JSON text, `reported_at`, `updated_at`) in `pkg/models/mqtt_device_state.go` with load, upsert, and delete helpers, and register it in `pkg/migrate/migrate.go`
- [X] T004 Add a migration test in `pkg/migrate/` that starts from a database holding node store, variable, and control rows, runs AutoMigrate, and reads them back unchanged (SC-006)

## Phase 2: Foundation, the wait store and resume path

Every user story phase depends on this phase.

- [X] T005 Add `WaitParker`, `Wait`, `WaitStore`, `Resumer`, `ErrSuspended`, and `Suspended` to `pkg/nodeapi/exec.go` as optional interfaces, exactly as in contracts/node-api.md, and re-export them from `pkg/engine/nodeapi.go`
- [X] T006 Implement the wait registry and `DBWaitSink` in `pkg/engine/waitstore.go`: per-flow/node stores, random tokens assigned when `Update` arms with an empty `Token`, a 1 MiB cap on encoded state with in-memory fallback reporting `durable == false`, a single-writer queue, and a `Claim` that checks the token, deletes the row, and blocks until the delete commits. Add `Engine.LoadWaits`, `PendingWaits(flowID)`, `DropFlowWaits(flowID, reason)`, and a `WaitSink` option in `pkg/engine/engine.go`, and close the sink in `Engine.Stop`
- [X] T007 Implement `WaitStore()` and `ExecutionID()` (run ID plus node instance index) on `execCtx` in `pkg/engine/execctx.go`. Return a memory-only store for test, preview, and synthetic runs
- [X] T008 Switch `Engine` to `context.WithCancelCause` in `pkg/engine/engine.go`. `Stop` cancels with `ErrSuspended`, and `CancelFlowRuns` takes a cause. In `pkg/engine/run.go`, a run cancelled with `ErrSuspended` settles as `canceled` with error `suspended; pending waits are carried over`
- [X] T009 Add `ResumeNodeID`, `ResumeKey`, `ResumeWait`, and `ResumedFromRunID` to `RunOptions` in `pkg/engine/run.go`. For that node, call `Resumer.Resume` instead of `Execute`, skip parameter pulls and execute-once, and add `meta.resumed {from_run_id, deadline, late_ms}` to every message it emits
- [X] T010 Add `engine.MissedDeadlineFields()` in `pkg/engine/resume.go` (`internal/nodeutil` imports the engine): `missed_deadline`, a select of `deliver` (default) or `drop`, and `missed_grace`, a duration defaulting to `30s`. The select's options reject other `missed_deadline` values at validation; an unreadable or negative `missed_grace` falls back to the default with a warning
- [X] T011 Add restore to `Runner.Rearm` in `pkg/engine/runner.go`. After loading the deployed graph and before arming triggers, drop each stored wait whose node is missing (`node <id> removed by redeploy; wait dropped`) or whose type differs (`node <id> changed type by redeploy; wait dropped`), recording a canceled run for each. Queue a resume for every row with a deadline, in deadline order. A drop past `missed_grace` records `missed deadline <deadline> by <lateness>; dropped`. Retry `ErrRunLimit` with 1–30 s backoff, and discard the queue but keep the rows on disarm
- [X] T012 Load waits in `cmd/karakuri/serve.go` with `eng.LoadWaits(...)` after `LoadNodeState` and before `runner.Start()`, wiring `DBWaitSink`
- [X] T013 Engine tests in `pkg/engine/waitstore_test.go` and `pkg/engine/resume_test.go`, with a fake clock and a real SQLite database. Cover a claim token mismatch, a claim whose delete fails (no emit), the 1 MiB fallback, a suspension cause leaving rows in place, resume at the original deadline after starting a second engine on the same database, and missed deliver, drop, and within-grace cases

## Phase 3: User story 1, Trigger across a restart (P1)

**Goal**: an armed Trigger fires at its original deadline after a restart and still honours reset and extend.
**Independent check**: quickstart scenario 1.

- [X] T014 [US1] Move Trigger pending state to `WaitStore` in `pkg/nodes/core/trigger.go`. Arm `arm:<key>` with the message and second payload source, replace `gen` with the token, delete on reset, write a new token on restart, move the deadline on extend, and claim before returning `expired`. Keep the row when `engine.Suspended(ctx)`. Add `Resume` starting at the park, and add `MissedDeadlineFields()`
- [X] T015 [US1] Table tests in `pkg/nodes/core/trigger_test.go` against a real wait store: restore and emit at the deadline, reset after restore, extend after restore, restart mode, and three keys with one reset
- [X] T016 [US1] Live: quickstart scenario 1 and SC-001 against `karakuri-dev.service` through MCP

## Phase 4: User story 2, missed deadlines on every timer node (P1)

**Goal**: every resumable node follows the deliver or drop setting.
**Independent check**: quickstart scenarios 2 and 4.

- [X] T017 [P] [US2] Delay in `pkg/nodes/core/delay.go`. Fixed and random modes use `exec:<execution id>` rows (reset still does not cancel them). Debounce uses a `debounce:<key>` row holding the latest message, deadline, and `maximum`, with the owner claiming at the end of the quiet period. The rate queue uses `queue:<key>:<seq>` rows holding an epoch token checked against the state-only `epoch:<key>` row; on restore, queued messages release at the interval from the restart. Leave `next_at` in `DurableStore`. Add `Resume` and `MissedDeadlineFields()`
- [X] T018 [P] [US2] Wait until in `pkg/nodes/core/wait.go`: an `exec:<execution id>` row with the target instant, plus `Resume` and the fields
- [X] T019 [P] [US2] Condition wait in `pkg/nodes/core/conditionwait.go`: an `exec:<execution id>` row with the message and absolute timeout. `Resume` re-registers the check waiter, restarts the hold, and emits `timeout` for a missed timeout under deliver. Add the fields
- [X] T020 [P] [US2] Fan-in in `pkg/nodes/core/group.go` and the join, batch, aggregate, limit (sequence), and sort (sequence) nodes: `group:<group id>` rows holding indexed parts, with a deadline only when a timeout is set. The completing run and the timeout owner both claim with the owner token. Add `Resume` and the fields
- [X] T021 [US2] Table tests for T017–T020 in their `_test.go` files: restore, deliver late, drop, drop within grace, debounce `maximum` preserved, rate-queue order and drop, and count-completed groups after a restore including the earlier parts
- [X] T022 [US2] Live: quickstart scenarios 2 and 4 (SC-002, SC-004)

## Phase 5: User story 3, redeploy and disable (P1)

**Goal**: a redeploy keeps waits whose node keeps its ID and type; disable and delete drop them.
**Independent check**: quickstart scenario 3.

- [X] T023 [US3] In `pkg/flowops/flowops.go`, `Deploy` calls `CancelFlowRuns(f.ID, engine.ErrSuspended)`, waits for those runs to settle (bounded by the engine's five-second stop wait), then rearms. `SetEnabled(false)` drops waits with `flow disabled; wait dropped`. `Delete` purges the in-memory registry after the transaction
- [X] T024 [US3] Tests in `pkg/flowops/` covering a removed node, a retyped node, a changed duration that keeps the original deadline, an unwired emitting output, and drops on disable and delete
- [X] T025 [US3] Live: quickstart scenario 3 (SC-003)

## Phase 6: User story 4, see and cancel pending waits (P2)

**Goal**: operators list waits and cancel one without editing the flow.
**Independent check**: quickstart scenario 5.

- [X] T026 [US4] Add `flowops.ListWaits` and `flowops.CancelWait` in `pkg/flowops/waits.go`. `CancelWait` deletes the row and records a canceled run with `wait cancelled by <user>`
- [X] T027 [P] [US4] REST in `pkg/api/flow_wait_handler.go`: `GET /flows/{id}/waits` (`api:runs:read`) and `DELETE /flows/{id}/waits/{node_id}?key=` (`api:runs:cancel`, 204 or 404). Expose `resumed_from_run_id` on run responses. Register the routes and add handler tests
- [X] T028 [P] [US4] MCP in `pkg/mcpserver/tools_state.go`: `inspect_flow_state` gains `store: "waits"`, reading from the engine registry. Add a `cancel_flow_wait` tool, add it to the assistant allowlist, and add tool tests
- [X] T029 [US4] Frontend: a pending waits section with cancel in `frontend/src/lib/components/editor/RunPanel.svelte`, the resumed-from link in run details, and client methods in `frontend/src/lib/api/client.ts`
- [X] T030 [US4] Browser check of the RunPanel waits section with a settled screenshot after each interaction and a console check; live check of quickstart scenario 5

## Phase 7: User story 5, MQTT device state (P2)

**Goal**: devices show their last reported state after a restart, marked stale until they report again.
**Independent check**: quickstart scenario 6.

- [X] T031 [US5] In `pkg/mqttrelay/device.go`, add `stale` and `reportedAt` to `device`. `start` seeds them from `mqtt_device_states` without fanout. `receive` clears `stale` and marks the device dirty. Add a writer that flushes dirty devices every 5 s and on `Stop`, logging failures at error and retrying them on the next flush. `Reload` deletes the row when `Control` changes, and device delete removes it. Expose `Stale` and `ReportedAt` on `DeviceStatus` and the `mqtt_device_state` broker payload
- [X] T032 [P] [US5] Add `stale` and `reported_at` to `GET /mqtt_devices` in `pkg/api/mqtt_device_handler.go` and to MCP `list_mqtt_devices`. Add `meta.stale` and `meta.reported_at` to `mqtt-device-get` in `pkg/nodes/mqttnodes/devices.go`
- [X] T033 [P] [US5] Frontend: a stale mark with the report time in `frontend/src/routes/settings/MQTTDevices.svelte` and on device widgets fed by `frontend/src/lib/dashboardLive.ts`
- [X] T034 [US5] Tests in `pkg/mqttrelay/device_test.go`: restore marks stale and does not fan out, a merge clears stale, a kind change deletes, coalescing writes at most once per interval, and stop flushes
- [X] T035 [US5] Live: quickstart scenario 6 (SC-005) and a browser check of the stale mark

## Final verification and documentation

- [X] T036 Update `docs/flow-authoring.md`: timer recovery across restart and redeploy, the `missed_deadline` and `missed_grace` fields, at-most-once delivery, the new-run semantics (run variables are not restored), a house-mode example, and removal of "Pending waits do not recover after restart" and "Pending messages remain transient". Update `docs/mqtt-device-state-script.md` for stale state, `docs/mcp.md` for the new store and tool, and `docs/plugins.md` for `Resumer` on external nodes
- [X] T037 Revise `specs/003-flow-execution/plan.md`, `specs/004-connections-devices/plan.md`, and `specs/015-stateful-flow-nodes/plan.md` to match. Update `specs/architecture.md` and set the spec status to Implemented
- [X] T038 Run `make dev`, `make test TARGET=server`, and `make test TARGET=frontend`, then quickstart scenario 7 (SC-006)
- [X] T039 Restart `karakuri-dev.service`, confirm it is active and http://localhost:8080 answers, and report the `029 …` test flows left in the development database

## Dependencies and execution order

- Phase 1 precedes Phase 2. T002 and T003 are independent of T001.
- Phase 2 blocks every story. T005 precedes T006–T011. T013 needs T006–T011.
- US1 (Phase 3) is the MVP and proves the whole resume path on one node.
- US2 (Phase 4) needs Phase 2 and follows the pattern set in T014. T017–T020
  touch separate files and can proceed in parallel.
- US3 (Phase 5) needs Phase 2 and at least one resumable node (T014).
- US4 (Phase 6) needs Phase 2. T027 and T028 can proceed in parallel after T026.
- US5 (Phase 7) needs only T003, so it is independent of Phases 2–6.
- Documentation and final verification follow all stories. Commit each verified
  phase on its own.
