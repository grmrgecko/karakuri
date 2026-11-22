# Design plan: Stateful flow nodes

**Feature**: `specs/015-stateful-flow-nodes` | **Date**: 2026-10-06 | **Spec**: [spec.md](spec.md)

## Summary

Add two server nodes and extend timers, deduplication, HA actions/triggers and
HTTP failure handling. Ship each story as a verified commit on the current branch.

## Technical context

- Go and Svelte use existing engine, expression evaluator, injectable clock,
  durable node store, catalog-driven fields and Home Assistant WebSocket driver.
- Components: `pkg/nodes/core`, `pkg/nodes/hanodes`, `pkg/engine`, `pkg/nodeapi`,
  `pkg/flow`, and frontend catalog/editor metadata.
- Storage: durable hysteresis flags and timestamped duplicate history; rate
  schedules remain durable. Pending timers, debounce windows, queued messages, and
  condition waits are stored as pending waits and resume after a restart or a
  redeploy under feature 029, which adds the `pending_waits` table.
- Limits: existing history count bounds, explicit condition/confirmation deadlines,
  bounded HTTP error bodies and finite retry delay caps.

## Constitution check

- Waiting releases worker slots and observes cancellation. Confirmation timeout
  routes separately from action failure and cannot cause an automatic resend.
- Catalog-based fields and ports retain shared draft/deploy and REST/MCP rules.
- No permission expansion, agent nodes, interpreter changes or dashboard trust changes.
- Existing database, credentials, CA, identities and durable state stay readable.
  HTTP mutating-request retry defaults deliberately become conservative.
- Build/test with Makefile targets; restart and inspect service, use MCP and browser.

## Design and contracts

Hysteresis compares a numeric message property against lower/upper thresholds,
with high-active or low-active direction. Atomic durable state is keyed by an
optional message property. It emits only on a transition, with active/inactive
ports and reset-only input.

Condition wait receives a normal message and an optional `value` parameter.
An explicit engine parameter-refresh capability evaluates its pure dependency
chain with a new isolated cache. Normal memoization remains unchanged. The
condition evaluates against the observed value as payload; without a wire it
can evaluate the incoming payload and live variables. An optional `check`
signal broadcasts through flow/node transient state to all
active waits. Signal-only executions skip parameter pulls and execute-once claims.
Register each buffered wake channel before observing; remove only its own entry
on exit. Never close channels. ParkUntil releases the worker while waiting for a
poll timer, signal or cancellation, then reacquires it before another observation.
Preview and node-test wrappers forward this optional capability. Require
boolean results, restart the stability interval on false, and route deadline
expiry through timeout. Repeated observations do not flood producer run events.

Timer keys select message values. Omitted keys keep existing single-bucket
store entries. Keyed entries use separate prefixed keys. Reset selects one key.
Debounce uses owned generation/state references so cancelled waiters cannot
clear replacement waits. Maximum wait caps the quiet deadline. Dropped and
superseded messages have inspectable branches. Final consumption checks ownership
atomically before emitting.

Dedupe retains legacy durable shapes and attaches admission timestamps to history entries when expiry is enabled. Expiry starts at admission, never duplicate arrival.
Existing entries start their expiry clock on first TTL-aware observation. Reset
clears history and timestamps; duplicate output preserves the envelope.

HA calls combine interpolated comma-separated entity/area/device/label IDs in
one target. Optional confirmation reads one explicit entity through GetStates,
not cached getters, until expected state/attribute is stable or timeout routes.
HA State held-for maintains independent pending work per entity and cancels on
contrary watched-value transitions or disarm. Reconnection gaps invalidate holds.

HTTP errors carry optional retry classification, server minimum delay and safe
bounded details. The engine honors these directives; plain errors keep their
existing policy. Default retries cover safe methods and transient statuses,
with explicit opt-in for other methods. Retry-After supports seconds and HTTP
dates. Optional retry jitter remains bounded by the delay cap. Error outputs
retain their payload and add response details. Redact credentials in URLs and
response headers. Pagination restarts from page one on retry.

## State and data model

See [data-model.md](data-model.md). Graph additions are optional configuration
fields and ports. Hysteresis has new state only. Timer legacy entries and dedupe
history remain readable; no key or identity regeneration is required.

## Verification

Each story gets a small contract/regression suite using real stores, fake clock,
real HTTP listeners and the existing Home Assistant WebSocket peer. Run
`make dev TARGET=server` and `make test TARGET=server` before its commit. Run
frontend builds/checks when editor retry settings change. Use MCP standalone
node checks, and browser unsaved drafts to inspect new nodes, settings and ports.
See [quickstart.md](quickstart.md) for final checks.

## Operational limits

Condition/confirmation stability is sampled and can miss a transient between
observations. Signals coalesce and external triggers obey normal run admission;
polling remains the fallback. Pending waits resume after a restart in a new run
from the waiting node under feature 029; the rest of an in-flight run does not.
Fresh pure chains can invoke
sandbox HTTP again, so authors must use intentional read-only sources. Group HA
targets confirm only the explicitly named entity. Retry opt-in is endpoint-specific.

## Final review

The implemented contracts retain unkeyed store keys and legacy duplicate values.
The HTTP default has 20% jitter; other node policies omit it. Retry validation
bounds attempts to 1–1000, validates finite backoff/jitter and rejects delays
outside the duration range. Classified errors remain an in-process node API;
plugin string errors retain their existing transport contract.

A debounce reset after replacement routes the latest pending message at the old
waiter's wake, rather than routing the already superseded first message again.
The ownership check remains atomic so that waiter cannot clear a replacement.
Verification evidence is recorded in quickstart.md. No remaining design decision
requires user input.

## Event-driven checks

Add the optional message-port kind `signal` to the catalog contract. Signal-only
executions do not pull value parameters or claim execute-once, but retain normal
execution history and cancellation. Signals cannot be primary payload inputs or
bypass payload sources. Mirror bypass eligibility in the editor. Isolated node
tests accept signal-only inputs without requiring unused value overrides.

Verify the existing engine with real SQLite controls and a deterministic clock:
concurrent waits, cross-run broadcast, false observations, unchanged deadlines,
cancellation and isolation. Exercise a deployed change trigger through MCP and
inspect the Check port and live value-source settings in the browser.
