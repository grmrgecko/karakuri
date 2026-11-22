# Validation: Stateful flow nodes

1. Run `make dev TARGET=server` and `make test TARGET=server`; when retry editor
   settings change also run `make dev TARGET=frontend` and `make test TARGET=frontend`.
2. Restart `karakuri-dev.service`; verify it is active and localhost:8080 answers.
3. Through MCP inspect `hysteresis` and `condition-wait`; test threshold crossing,
   immediate condition match and a short timeout without saving a flow.
4. Test HTTP requests against a disposable local listener that counts requests and
   returns controlled 400, 429 and 503 responses. Inspect attempts and error details.
5. Use the in-process HA WebSocket peer for targets, confirmation and held state;
   do not switch household devices for these checks.
6. Use a disposable disabled flow for browser checks. Pause its graph writes
   during visual edits because the editor autosaves. Place each new node and change keyed
   timer, dedupe, HA confirmation and HTTP retry settings. Wait for the canvas,
   capture after interactions, inspect derived ports, and check the console.
7. Report any persistent test objects. Leave the development service running.

## Verification evidence (2026-10-06)

- Server Makefile builds and tests passed for each story; frontend build and
  `make test TARGET=frontend` reported zero errors and warnings.
- Fake-clock regressions cover crossings, held/timeout conditions, fresh chained
  pulls versus ordinary snapshots, independent keys, timer ownership, dedupe
  expiry boundaries and persisted legacy history. A reset after debounce replacement
  routes the latest pending message once and preserves the new window. Retry checks cover jitter
  bounds, server minimum/cap, classified errors, envelope preservation and cancellation.
- HTTP peers establish GET retry, permanent 400, default POST suppression,
  explicit POST opt-in, selected statuses and permanent decode failure. Failure
  details bound bodies and omit URL query/userinfo and cookie headers. MCP's local peer
  returned 429 then 200 and recorded exactly two requests.
- The HA WebSocket peer verifies all four target keys, fresh confirmation match
  and timeout with one call, no resend after an observation error, independent
  holds, contrary cancellation and connection-gap invalidation. No household
  devices were operated.
- MCP inspected all eight node schemas, matched/timed out Condition wait,
  crossed Hysteresis, and reset Delay/Dedupe without output. Strict validation
  rejected jitter 1.5 and accepted 0.2.
- Browser screenshots captured nodes/fields, HTTP Object-to-String output typing,
  jitter changed to 35%, HA confirmation fields appearing on enable, and debounce
  maximum wait appearing on mode change. No new runtime console errors appeared
  during these interactions. Existing GET `/api/flows/<id>/run` 404 responses
  were present for flows without history before the controls were changed.
- The temporary disabled flow `Stateful nodes verification 2026-10-06` was
  removed, including its initial autosaved draft. Subsequent visual edits stayed
  local. The local HTTP peer was stopped and the preview returned to the flow list.

## Event-driven condition checks (2026-10-06)

- `make dev`, `make test TARGET=server` and `make test TARGET=frontend` passed.
  The development service was restarted, active, and returned HTTP 200.
  Go vet passed for the changed packages.
- Real engine runs with one worker and migrated SQLite controls verify cross-run
  broadcast to concurrent waits, cancellation without removing peers, flow
  isolation, false observations restarting holds, and unchanged deadlines.
  Same-run branches verify that a signal wakes a parked waiter and that an early
  signal leaves execute-once available. An idle check succeeds with an invalid
  unused control reader, establishing that it performs no value pull.
- A deployed Control Changed trigger woke Condition wait before its one-minute
  poll. The wait matched after 1114 ms including a one-second hold, with its
  original payload/topic and fresh `condition_value`. An isolated MCP check-only
  execution succeeded without value overrides and emitted no outputs.
- Browser checks showed the connected optional Check handle and polling help.
  Changing the reader from Boolean to Number and back updated its canvas type
  live and retained Check and the original message output types. Visual graph
  writes were paused; MCP confirmed the saved reader stayed unchanged. No runtime
  errors occurred during these edits. Screenshots were captured after interactions.
- Temporary flow `a4696e8d-2508-493a-94c5-a11765e29278` and control
  `199c4308-0818-4fc8-9a45-f9a308729f56` were removed. No household device was used.

Connect a change trigger's message output to Condition wait's `check` input and
its live pure reader to `param:value`. Connect the message that should wait to
`in`. Keep a positive poll interval for missed or rejected change-trigger runs;
checks still obey the configured concurrent-run admission limit.
