# Design plan: Connection and agent access

**Feature**: `specs/047-connection-agent-access` | **Date**: 2026-11-21 | **Spec**: [spec.md](spec.md)

## Summary

Connections and agents gain an owner and shares, modelled on flow sharing. One
package, `pkg/access`, answers every question about them, and each way in
calls it: REST handlers and middleware, an MCP request middleware, the flow
engine, the plugin host, dashboard deploy, the event log, and WebSocket topic
gates. Runs act as a user, so the engine checks the connections and agents a
node actually reaches at run time, not only what a deploy can see.

## Technical context

- **State.** `Connection.OwnerID` and `Agent.OwnerID` (nullable), tables
  `connection_accesses` and `agent_accesses` (`models.ConnectionAccess`,
  `models.AgentAccess`: resource ID, user or group, level), and
  `FlowVersion.RunAsID`. All are added by AutoMigrate and registered in
  `models.All`, so backups carry them. User and group deletion removes their
  shares. Deleting a connection or agent removes its shares.
- **Levels.** use (1), edit (2), admin (3), in `models/resource_access.go`.
  The owner holds admin. `api:connections:manage` and `api:agents:manage` hold
  admin on every one; the Admin role's `api:*` covers both.
- **pkg/access.** `ConnectionRank`, `AgentRank`, `Connection`/`Agent` (load and
  authorize, answering not found at rank 0 and forbidden below the level),
  `ConnectionIDs`/`AgentIDs` (a set, nil for managers), `VisibleNodeTypes`,
  `LimitEvents`, `EventVisible`, `UseConnections`, run-user context helpers, and
  `CheckRunConnection`/`CheckRunAgentType`. It imports only models, auth,
  errors, events, flow, and permissions, so the engine and plugin host can use
  it without import cycles.

## Runs and the engine

`engine.RunOptions.RunAs` names the user a run acts as. Deployed runs take
`engine.RunAsFor(flow, version)`: the version's `RunAsID`, then its
`CreatedByID`, then the flow's owner. `flowops.Deploy` records the deployer;
a faceplate-level apply copies the previous version's run user. Trigger arm,
wired-config evaluation, trigger emits, error-handler runs, resumed waits,
upcoming firings, and dashboard widget triggers all use `RunAsFor`. Node
tests, draft runs, reruns, standalone node tests, and node chat set the
requesting user.

Checks, each fail-closed for a zero run user:

- `execCtx.Config` checks the connection-typed fields of the merged config,
  after value-edge parameters, so every node, plugin nodes and script node
  calls included, fails before reading a connection its run user cannot use.
  `execCtx.Connection` checks the ID it resolves. Answers are cached per run.
- `triggerCtx.Config` and `triggerCtx.Connection` do the same for armed
  triggers.
- `withFlowActor` puts the run user in the node's context, and the trigger arm
  context carries it. The plugin host's `remoteNode.Execute` and
  `remoteTrigger.Start`, which every agent node passes through, refuse an agent
  the context's run user cannot use.

`flowops.CheckResourceAccess` runs at deploy over the flattened graph and
names the node whose literal connection or agent type the deployer cannot use.
Faceplate applies require use on any `connection_id` a value names.

## Surfaces

- **REST.** Connection CRUD and test use `access.Connection` at use, edit, or
  admin. Authenticated `/connections/{id}/...` routes take `requireConnection`
  middleware. The dashboard-facing optional-auth routes use the per-connection
  `canReadConnection`/`canInvokeConnection`, so dashboard scope still admits
  viewers. Device routes check the device's connection; discovery routes check
  their `connection_id`. Agent list, update (edit), delete and revoke (admin),
  and token list and delete (minter or manager) check per agent. Shares live at
  `/connections/{id}/shares` and `/agents/{id}/shares`.
- **MCP.** `connectionArgumentAccess`, a receiving middleware, refuses any
  `tools/call` whose `connection_id` argument is a UUID the caller cannot use,
  which covers the editor chat too. Tools that name a connection otherwise
  (`save_connection`, `delete_connection`, `test_connection`,
  `list_notification_endpoints`, device tools by device ID) and agent tools
  check explicitly. `find_references` and `get_document_dependencies` refuse or
  hide references to resources the caller cannot use.
- **Lists.** Connections, agents, devices, node types, the script function
  catalog, history forwards and writer status, energy candidates, the editor
  chat model list, and the home attention list are filtered to usable
  resources.
- **Dashboards.** `dashops.Deploy` calls `checkDraftConnections`, which takes
  the connections and devices `dashscope.DraftConnectionRefs` derives from the
  draft, plus chart and variable query connections. Chart query and time-series
  variable saves check each connection they add.
- **Events and WebSocket.** `events.Filter` gains connection and agent limits;
  entity subjects match on their connection prefix. Connection, entity, MQTT,
  and device topics use `connectionTopicAllowed`/`deviceTopicAllowed`. The live
  event filter applies `EventVisible` to every entry.
- **Plugins.** The plugin protocol gains `CheckConnectionAccess`, additive, and
  the calendar plugin lists and references only connections the author may use.
- **Frontend.** Connection and agent settings show the caller's level and offer
  edit, share, and delete only at the matching level.
  `settings/ResourceShare.svelte` serves `/settings/connections/:id/share` and
  `/settings/agents/:id/share`.

## Constitution check

- **Permissions.** Every route keeps its role permission and adds the resource
  check; manage permissions mirror `flows:manage`.
- **Existing state.** Owners are nullable. Connections and agents created before
  this change have none and reach only managers until shared. Versions frozen
  before `run_as_id` existed act as their creator or the flow's owner. No data is
  rewritten, and the plugin protocol change is additive.
- **Failures.** A node refused for access fails with an error naming the
  connection or agent, and its failure routing and retries apply as for any
  node error.

## Risks and limits

- Connection and agent names stay unique across owners, so creating one with a
  name another user's connection holds is refused with a conflict.
- Installed plugins are trusted code; `ConnectionAccessToken` still answers any
  oauth2 connection a plugin names.
- Dashboard viewers keep what a deployed dashboard binds after its deployer
  loses access, until the next deploy.

## Verification

- `make test TARGET=server`: access ranks and filters, deploy and run refusals,
  REST and MCP refusals for a second user, and share routes.
- MCP, as a second user with the User role: list, invoke, test node, and script
  node call against an unshared connection and agent, then after a use share.
- Browser: connection and agent settings at each level, the share page, and the
  console.
