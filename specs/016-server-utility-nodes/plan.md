# Design plan: Server utility nodes

**Feature**: `specs/016-server-utility-nodes` | **Date**: 2026-10-06 | **Spec**: [spec.md](spec.md)

## Summary

Add three core server nodes using registered metadata and the existing graph,
execution, connection and editor contracts.

## Technical context

- Go, existing engine/flow APIs, standard time/math packages and `pkg/sunevent`.
- Core registration in `pkg/nodes/core/core.go`; connection lifecycle in `pkg/connmgr`.
- No dependencies, migrations or persistent state additions.
- Time windows inspect four neighboring start dates; solar offsets are bounded to twelve hours.
- Connection watchers use queues of 64 reports. Overflow drops the oldest report and logs the loss.

## Constitution check

- Deterministic numeric/time errors have error outputs and no retry side effects. Status watches stop on cancellation.
- Metadata supplies config schemas and derived ports to validation, REST, MCP and the generic editor.
- Connection selection uses the existing connection field and dependency tracking. No new permission path is introduced.
- Databases, encrypted configs, keys and agent identities remain unchanged.
- Build/test through Makefile, restart the user service, check MCP and exercise live editor derivations.

## Design and contracts

Time window preserves the input and routes once. Evaluate `Clock().Now()` or
an explicit RFC3339/Unix timestamp path. Clock endpoints compare civil local
times rather than elapsed UTC durations, including through repeated DST hours.
Resolve solar events on their nominal local date by checking adjacent UTC dates
with `sunevent.On`. Apply offsets to actual instants, then compare local civil
timestamps. Choose the first end boundary after the start. Weekdays select the
nominal start date. Missing solar events mean outside with a reason; invalid
settings mean error.

Numeric transform uses source/target message paths and finite numbers. Scale
uses value*factor+offset. Mapping permits reversed endpoints and optional output
clamping. Clamp and round are separate operations. Unit conversion uses one base
unit per dimension with affine temperature offsets. Clone before writing.
Derive scalar number or nested object ports from paths; metadata writes retain
the incoming payload type.

Connection status reads `connops.Manager()` and a stored connection identity.
WatchStatus atomically captures the retained snapshot and registers a bounded
queue. Driver callbacks enqueue without waiting for flow execution. Track all
states even when filtered; emit only state changes. Initial emission defaults
off. Standalone tests read a snapshot without subscribing. The manager records
connecting, failed-start and disabled states and guards callbacks by driver
generation. Repeated states retain their entry timestamp. Shutdown closes watchers.

## State and data model

Time and numeric nodes are stateless. Statuses, generations and watchers are
process-local and contain no configs. Existing IDs remain stable across driver
replacement. See [data model](data-model.md) and [contracts](contracts/nodes.md).

## Verification

Table-driven tests cover local timestamps, overnight weekdays, DST, absent sun
events, reversed ranges and physical unit constants. A real HA WebSocket peer
verifies status lifecycle with the real manager and encrypted connection model.
Run `make dev TARGET=server`, `make test TARGET=server` and vet through Makefile.
Use MCP standalone tests and browser screenshots after config changes; check
console diagnostics. No live device commands are needed.

## Risks and open decisions

Queues and run admission can lose transitions; this is not an audit log.
Clock windows follow wall time; solar offsets are elapsed durations. Missing
sun events do not imply a full-day window. No open decision requires user input.
