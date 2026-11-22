# Feature specification: Connection and agent access

**Feature**: `specs/047-connection-agent-access`
**Created**: 2026-11-21
**Status**: Implemented 2026-11-21
**Input**: Limit which connections and agents each user can see and use, and
let a user who adds a connection or enrolls an agent keep it to themselves or
share it. Planning found that any user who can deploy a flow could reach every
connection and run commands on every agent host through flow nodes, node
tests, and script node calls, because nothing checked the connection or agent
a node used.

## User scenarios and testing

### User story 1: Flows reach only what their author may use (Priority: P1)

A household member with the default User role builds a flow. The palette
offers only the agents shared with them, connection pickers list only the
connections shared with them, and a deploy, test, or run that reaches any
other connection or agent fails.

**Why this priority**: Without it, deploying a flow is a shell on every agent
host and full control of every connection, whatever the roles say.
**Independent test**: As a second user holding flow permissions but no
shares, deploy and test a flow that names another user's connection and an
agent's command node, and call that node from a script.

**Acceptance scenarios**:

1. **Given** a user without a share on an agent, **When** they list node
   types, **Then** none of that agent's node types are listed.
2. **Given** a draft referencing a connection the user cannot use, **When**
   they deploy it, **Then** the deploy is refused and names the node.
3. **Given** the same draft, **When** they test the node or run the draft,
   **Then** the node fails with an access error and the connection is not
   contacted.
4. **Given** a script node that calls an agent node type the run's user
   cannot use, **When** the run reaches the call, **Then** the call fails.
5. **Given** a connection ID fed to a node over a value edge, **When** the
   run's user cannot use it, **Then** the node fails.

### User story 2: Keep my connections and agents to myself (Priority: P1)

A user allowed to add connections adds a broker for their workshop, and a
user allowed to enroll agents enrolls their laptop. Each is the owner of what
they added, and other users neither see nor use it.

**Why this priority**: It is what lets an administrator hand out the right to
add connections and agents without handing out everyone else's.
**Independent test**: Add a connection and mint an enrollment token as one
user, and list, read, and invoke them as another.

**Acceptance scenarios**:

1. **Given** user A created a connection, **When** user B lists connections,
   **Then** it is not listed, and reading or invoking it by ID answers not
   found.
2. **Given** an agent enrolled with a token user A minted, **When** user B
   lists agents, **Then** it is not listed.
3. **Given** a user holding the connections or agents manage permission,
   **When** they list, **Then** everything is listed and every route admits
   them.

### User story 3: Share a connection or an agent (Priority: P1)

The administrator shares the household Home Assistant connection with the
family group at use level. Family members can build flows and dashboards
with it and control its entities, but cannot see its credentials, change it,
share it, or delete it.

**Why this priority**: Shared household connections are the common case.
**Independent test**: Share a connection at use, then at edit, with a second
user and exercise invoke, update, share, and delete as that user.

**Acceptance scenarios**:

1. **Given** a use share, **When** the user calls a service or deploys a
   flow on the connection, **Then** both succeed; updating it is refused.
2. **Given** an edit share, **When** the user updates or tests the
   connection, **Then** both succeed; sharing or deleting is refused.
3. **Given** an admin share, **When** the user adds a share or deletes the
   connection, **Then** both succeed.
4. **Given** a share to a group, **When** a member is removed from the group,
   **Then** they lose it on their next request.

### User story 4: Deployed flows act as their deployer (Priority: P2)

A deployed flow runs with the access of the user who deployed it. When that
user loses access to a connection the flow uses, the flow's next use of it
fails instead of continuing on access nobody holds.

**Why this priority**: A check only at deploy would let access outlive its
revocation.
**Independent test**: Deploy a flow as a user with a use share, remove the
share, and fire the flow.

**Acceptance scenarios**:

1. **Given** a flow deployed by user A using A's shared connection, **When**
   the share is removed and the flow fires, **Then** the node fails with an
   access error.
2. **Given** a faceplate-level user applies a faceplate change, **When** the
   flow next runs, **Then** it still acts as the user who last deployed it.

### User story 5: Dashboards expose only what their deployer may use (Priority: P2)

A dashboard binding an entity, a device, a camera, a condition, a variable,
or a chart query on a connection deploys only when its deployer may use that
connection. Viewers keep operating what the deployed dashboard binds.

**Why this priority**: Otherwise any dashboard editor could hand viewers any
connection's entities.
**Independent test**: Deploy a dashboard binding another user's connection
as a user without a share.

**Acceptance scenarios**:

1. **Given** a binding on a connection the deployer cannot use, **When** they
   deploy, **Then** the deploy is refused and names the widget.
2. **Given** a deployed dashboard, **When** a viewer operates a bound entity,
   **Then** it works as before, through the dashboard.

### Edge cases

- A connection or agent with no owner, such as one restored from before
  owners existed, is reachable only by holders of the manage permission until
  one of them shares it.
- A connection that names another connection, such as a calendar connection
  using an OAuth connection, requires use on the named one to save.
- Devices belong to their connection: listing, reading, commanding, and
  defining a device require use on its connection.
- Run, debug, and node test output is the run user's own; a failed access
  check names the connection or agent ID the node referenced, which the
  author already holds.
- A run with no user to act as fails every connection and agent access. Only
  runs of versions deployed before this change and owned by a deleted
  account can lack one; redeploying gives them one.
- Subflows run as the run that calls them.
- Shares sit beside the resource, not in flow or dashboard versions, and
  backups carry them.
- Enrollment tokens are listed to the user who minted them and to holders of
  the agents manage permission. The agent CA and its rotation stay global.
- Installed plugins are trusted code and keep resolving their own
  connections through the host.

## Requirements

### Functional requirements

- **FR-001**: The system MUST record an owner for each connection, set to
  the user who creates it, and for each agent, set to the user who minted the
  token it enrolled with. The owner holds admin.
- **FR-002**: The system MUST store connection shares and agent shares to a
  user or a user group at use, edit, or admin, where each level includes the
  ones below it.
- **FR-003**: Use MUST admit listing the resource, reading its status and
  masked configuration, referencing it from flows, dashboards, devices,
  history forwards, energy sources, and memory stores, invoking it, and
  running an agent's node types.
- **FR-004**: Edit MUST admit changing and testing a connection, and
  renaming, describing, enabling, and disabling an agent.
- **FR-005**: Admin MUST admit sharing and deleting a connection, and
  sharing, deleting, and revoking an agent.
- **FR-006**: Holders of `api:connections:manage` or `api:agents:manage`
  MUST hold admin on every connection or every agent. The role permissions
  that gate each route still apply.
- **FR-007**: A user holding nothing on a connection or agent MUST be
  answered as if it does not exist, on every REST route, MCP tool, and
  WebSocket topic, and list results MUST omit it.
- **FR-008**: The node type list, script node function catalog, and node
  type reads MUST omit an agent's node types for users who cannot use the
  agent.
- **FR-009**: Every run MUST act as a user: the requesting user for node
  tests, draft runs, reruns, and node chat; for runs of a deployed version,
  the user who deployed that version, kept across faceplate applies.
- **FR-010**: A node MUST fail before contacting the connection when its
  resolved configuration, after value-edge parameters, names a connection
  its run's user cannot use, and when it resolves a connection that user
  cannot use. Agent node types and triggers MUST fail to run or arm when the
  run's user cannot use the agent. This applies to script node calls.
- **FR-011**: Deploying a flow MUST refuse when the deployer cannot use a
  connection named in a node's configuration or an agent whose node type the
  graph contains.
- **FR-012**: Deploying a dashboard MUST refuse when the deployer cannot use
  a connection its entity, device, camera, condition, variable, or chart
  bindings name.
- **FR-013**: Event log entries about a connection, an agent, or a device
  MUST be listed only to users who may use the resource.
- **FR-014**: Adding, changing, and removing a share MUST write an event log
  entry, and backup and restore MUST carry shares.
- **FR-015**: The settings pages MUST let an admin-level user share a
  connection or an agent with users and groups, and show each resource's
  owner.

### Key entities

- **Connection owner** and **agent owner**: the user who added it; admin on it.
- **Connection share** and **agent share**: a grant of use, edit, or admin
  on one resource to a user or a user group.
- **Run user**: the user a run acts as, recorded on each deployed version.
- **Manage permissions**: `api:connections:manage` and `api:agents:manage`.

## Success criteria

- **SC-001**: A user with the default User role and no shares cannot list,
  read, invoke, reference, or run another user's connection or agent through
  REST, MCP, a flow, a node test, or a script node call.
- **SC-002**: A use share admits invoking and deploying against the resource,
  and refuses changing it.
- **SC-003**: Removing a share makes the next run of a flow deployed by that
  user fail at the node that uses the resource.
- **SC-004**: A dashboard binding a connection its deployer cannot use fails
  to deploy.
- **SC-005**: `make test` and `make dev` pass and the development service
  stays active.

## Assumptions and scope

- The Admin role holds `api:*` and so keeps every connection and agent.
- The default User role is unchanged. Granting it `api:connections:write` or
  `api:agents:enroll` is what lets ordinary users add their own.
- Controls, memory stores, energy settings, and plugin settings stay gated by
  role permissions alone; this feature covers connections, the devices that
  belong to them, and agents.

## References

- [Server security and permissions](../001-server-security/spec.md)
- [Connections and devices](../004-connections-devices/spec.md)
- [Dashboard access and rendering](../007-dashboard-access-rendering/spec.md)
- [Remote agents](../009-remote-agents/spec.md)
- [MCP and editor assistant](../012-mcp-editor-assistant/spec.md)
- [Event log](../037-event-log/spec.md)
- [Flow sharing](../043-flow-sharing/spec.md)
