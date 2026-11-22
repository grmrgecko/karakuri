# Research: Flow sharing

Each decision below came from reading the current code. Paths are
repository-relative.

## Where the check lives

- **Decision:** `flowops.LoadFor(uid, id, level)` plus `flowops.Level`.
  These are called by REST handlers and MCP tools, mirroring `dashops`.
- **Rationale:** every flow route already resolves through `flowops.Load`
  (`pkg/flowops/flowops.go:125`), so one helper reaches REST and MCP alike.
  Three paths bypass it with direct queries and call `LoadFor` explicitly:
  `inspect_flow_state`, `list_flow_runs`, and the folder move handler.
- **Alternatives:**
  - Middleware keyed on `{id}`: rejected, because run routes carry a run ID
    and MCP has no path parameters.
  - Passing a user into every `flowops` function: rejected as a large
    signature churn for the same check.

## Global permissions keep meaning "may author"

- **Decision:** keep `api:flows:read/write/deploy` as route gates and add
  `api:flows:manage` for blanket access.
- **Rationale:** dashboards already split this way
  (`pkg/dashops/dashops.go:70`). The Admin role's `api:*` covers manage, so
  no seed changes.
- **Alternatives:** dropping global flow permissions for per-flow levels
  only. Rejected: administrators would lose the ability to stop a user
  authoring flows at all.

## Subflow references

- **Decision:** check view only on references a save adds, comparing against
  the current draft. Validation and runtime resolution stay user-free.
- **Rationale:** the resolver (`flowops.dbSubflowResolver`) runs during
  deploy, test, and validation with no user. Checking existing references
  would make a parent unsaveable after its author loses access, which the
  spec forbids.
- **Alternatives:**
  - A user-aware resolver: rejected, since it would break deployed parents
    and the engine's flatten step.

## Live streams

- **Decision:** move `flow:*`, `editor:flow:*`, and `run:*` to
  `RegisterTopicPermissionCallback`.
- **Rationale:** the callback mechanism already gates `editor:dashboard:*`
  and `dashboard:*` (`cmd/karakuri/serve.go:161-170`). `pkg/mq` revalidates
  sockets every 30 seconds and fails closed, so removing a share withdraws
  live debug data without new machinery. A run ID maps to its flow through
  the engine's run snapshot, or the `runs` table once the run is gone.

## Dashboard trigger bindings

- **Decision:** check at dashboard deploy, not at each press.
- **Rationale:** dashboard scope derives grants from the frozen layout
  (`pkg/dashscope/binding.go:193`). Checking at each press would tie a
  dashboard's behavior to the deployer's later shares. Checking at deploy
  closes the hole where a dashboard author binds a flow they were never
  shared and publishes it.
- **Alternatives:**
  - Check on draft save: rejected, because drafts are lenient by design
    (feature 002).

## Event log

- **Decision:** reuse `events.Filter.SubjectAllowed` with a predicate that
  admits non-flow subjects and flow subjects held at view.
- **Rationale:** the filter exists (`pkg/events/query.go`) and dashboards use
  it. Flow events already carry the flow as subject
  (`pkg/eventsources/sources.go`, `flowops.recordFlow`).

## Backup

- **Decision:** register `FlowAccess` in `models.All()`; no backup code
  changes.
- **Rationale:** `pkg/backup/dbdump.go:90` dumps every registered model, and
  restore loads parents first by foreign key.
