# Research: Stateful flow nodes

## Fresh condition values

Decision: Give the engine an explicit fresh-parameter capability with an isolated
pull cache and preserve normal per-run memoization.
Rationale: Re-reading Config cannot refresh wired HA/MQTT/control getters.
Alternative: Hard-code integration readers in Condition wait. Rejected because
existing pure value nodes already define those reads and their parameter chains.

## HA targeting and confirmation

Decision: Extend the existing target map with area_id, device_id and label_id;
poll GetStates for one explicit confirmation entity.
Rationale: The driver already carries arbitrary target maps. Local state-event
registration currently precedes server acknowledgment and cannot guarantee that
an immediate service response is observed. Polling uses the existing reliable API.
Alternative: Infer all area/label members. Rejected because confirmation is ambiguous.
Protocol: https://developers.home-assistant.io/docs/api/websocket/

## HTTP retries

Decision: Retry safe methods and selected transient errors by default. Require
explicit opt-in for mutating methods, classify permanent errors, and apply bounded
Retry-After guidance through the engine retry loop.
Rationale: Repeating a successful side effect after a lost response can duplicate it.
Alternative: Retry inside HTTP Execute. Rejected because that hides attempts from
run history and duplicates runtime policy.
Protocol: https://www.rfc-editor.org/rfc/rfc9110.html#section-9.2.2
and https://www.rfc-editor.org/rfc/rfc9110.html#section-10.2.3

## Existing state

Decision: Preserve unkeyed timer keys and legacy duplicate history values. Add
admission timestamps in optional history records, initializing old entries on first TTL-aware read.
Rationale: Existing node history is irreplaceable development state.
Alternative: Reset all stores. Rejected because it changes active suppression.

## Change-trigger checks

Decision: Add an optional `check` input of kind `signal`, a flow/node-scoped set
of buffered waiter channels, and an optional interruptible parking capability.
Research inspected scheduler dispatch, fresh pulls, standalone tests and editor
port consumers. Optional ports already dispatch independently across runs.

Signal-only executions skip parameter pulls and execute-once claims. Merely
branching inside Condition wait would still read dependencies before Execute.
Canceling a child context to interrupt Park would fail to reacquire the worker.
ParkUntil selects on the signal without canceling the execution context.

Keep polling. Run admission may reject an external change-trigger run while a
wait is active. Signals request observations rather than replaying event values;
coalescing can miss a transient between observations. No restart recovery or
run-admission bypass is added.
