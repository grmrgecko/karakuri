# Feature specification: Flow sharing

**Feature**: `specs/043-flow-sharing`
**Created**: 2026-11-21
**Status**: Implemented 2026-11-21
**Input**: Share a flow with a person or a group, as dashboards are shared,
choosing full edit or faceplate access only. Planning found runs, live
streams, the event log, dashboard trigger bindings, and editor chat that
also expose flows and need the same checks. Today a flow records an owner
but nothing checks it: the default User role holds flow read, write, and
deploy, so every user can change and deploy every flow. Then share folders as
dashboard groups are shared, and stop flow names confirming private flows.

## User scenarios and testing

### User story 1: Keep my flows to myself (Priority: P1)

A household member creates a flow for their own lamp. Another member signed
in on the same server does not see it in their flow list and cannot open,
change, deploy, run, or delete it.

**Why this priority**: Every other story depends on flows having an owner
that the server enforces.
**Independent test**: Create a flow as one user and request it, its runs,
and its graph routes as a second user with ordinary flow permissions.

**Acceptance scenarios**:

1. **Given** a flow owned by user A, **When** user B with the default User
   role lists flows, **Then** the flow is not listed.
2. **Given** that flow's ID, **When** user B requests its graph, runs,
   versions, or any write route, **Then** the server answers not found.
3. **Given** a user holding the flow manage permission, **When** they list
   flows, **Then** every flow is listed and every route admits them, as the
   dashboard manage permission does for dashboards.
4. **Given** user B creates a flow, **When** it is saved, **Then** user B owns
   it and holds admin on it.

### User story 2: Share a flow for editing (Priority: P1)

The owner of the porch light flow shares it with their partner at edit
level. The partner can open the graph, change it, test it, deploy it, and
enable or disable it, but cannot share it further or delete it.

**Why this priority**: Shared editing is the common household case.
**Independent test**: Share a flow at edit with a second user and exercise
edit, deploy, share, and delete routes as that user.

**Acceptance scenarios**:

1. **Given** an edit share, **When** the user saves and deploys the flow,
   **Then** both succeed and the version records that user.
2. **Given** an edit share, **When** the user adds a share or deletes the
   flow, **Then** the server refuses.
3. **Given** an admin share, **When** the user adds a share or deletes the
   flow, **Then** both succeed.
4. **Given** a share to a user group, **When** a member of that group opens
   the flow, **Then** they hold the shared level; a user removed from the
   group loses it on their next request.

### User story 3: Share a flow's faceplates only (Priority: P1)

The owner shares the TV art mode flow with their kids at faceplate level.
The kids see the flow in their list, open it to its faceplate tabs, pick a
gallery, and Apply. They never see the graph.

**Why this priority**: It is how an administrator exposes a few settings to
people who should not edit automations, which is the reason faceplates
need sharing.
**Independent test**: Share a flow with faceplates at faceplate level and
exercise the faceplate and graph routes as that user.

**Acceptance scenarios**:

1. **Given** a faceplate share, **When** the user opens the flow, **Then**
   only its faceplate tabs are shown and every graph, version, run detail,
   and export route refuses them.
2. **Given** a faceplate share, **When** the user applies a value change,
   **Then** it deploys as feature 042 describes for faceplate-only users.
3. **Given** a faceplate share on a flow with no Faceplate node, **When** the
   user lists flows, **Then** the flow is not listed.
4. **Given** a view share, **When** the user opens the flow, **Then** they see
   the graph and faceplate tabs read only and cannot save, deploy, Apply,
   run, or enable it.

### User story 4: Manage who a flow is shared with (Priority: P2)

The owner opens the flow's share dialog, sees each person and group it is
shared with and at which level, changes one, and removes another.

**Why this priority**: Shares that cannot be reviewed are not deliberate.
**Independent test**: Add, list, and remove user and group shares through
the API and the dialog.

**Acceptance scenarios**:

1. **Given** an admin on a flow, **When** they add a share naming a user or
   group and a level, **Then** it is stored and listed.
2. **Given** a share request with a level outside faceplate, view, edit, and
   admin, **When** it is submitted, **Then** it is refused.
3. **Given** a removed share, **When** that user next requests the flow,
   **Then** they no longer hold the level.

### User story 5: Reuse a shared subflow I cannot edit (Priority: P3)

A user builds a flow that calls a shared subflow someone else owns and
shared with them at view level. They can reference it, see its interface,
and run it as part of their flow, but cannot change it.

**Why this priority**: Shared subflows are a reuse path, and sharing must not
silently break flows that already call one.
**Independent test**: Reference a subflow shared at view from a flow the user
owns, deploy, and run it.

**Acceptance scenarios**:

1. **Given** a subflow shared at view, **When** the user references it from
   their own flow, **Then** validation and deployment succeed.
2. **Given** a subflow the user holds no share on, **When** they try to
   reference it, **Then** the server refuses the reference.
3. **Given** a deployed parent that already calls a subflow its author can no
   longer see, **When** the parent runs, **Then** it runs as deployed; the
   loss of access affects only new edits.

### User story 6: Share a folder of flows (Priority: P2)

The owner keeps the garage flows in a "Garage" folder and shares the folder
with their partner at edit. The partner sees the folder, works on every flow
in it and in its subfolders, and adds new flows there, without a share per
flow. Someone without a share on the folder does not see its name.

**Why this priority**: Sharing flow by flow does not scale past a few flows,
and folder names were visible to every flow reader.
**Independent test**: Share a folder at view and then edit, and exercise its
flows, the folder list, rename, move, and delete as the grantee.

**Acceptance scenarios**:

1. **Given** a folder shared at view, **When** the grantee lists folders and
   flows, **Then** the folder and every flow inside appear at view.
2. **Given** a folder shared at edit, **When** the grantee creates a flow in
   it or renames it, **Then** both succeed; moving or deleting it is refused.
3. **Given** a flow shared directly out of a folder the grantee cannot see,
   **When** they open it, **Then** it shows at their root without naming the
   folder.
4. **Given** a user without edit on a folder, **When** they create or move a
   flow or folder into it, **Then** the server refuses.

### Edge cases

- Shares, like dashboard shares, sit outside version snapshots; restoring a
  version changes no shares.
- A flow whose owner account is deleted keeps its shares; users with the
  manage permission can reassign ownership.
- Runs started by triggers are unaffected by who the flow is shared with. They
  act as the user who deployed the version, whose connection and agent access
  their nodes use, as [feature 047](../047-connection-agent-access/spec.md)
  defines.
- Dashboard flow-trigger bindings keep their own authorization path through
  the dashboard, as feature 007 defines, but a dashboard can only bind a
  trigger on a flow its deployer may run. Otherwise anyone who can open a
  public dashboard could fire a flow its author was never shared.
- Live run and debug streams, active-run lists, run cancellation, and pending
  waits follow the flow they belong to, so a share decides who watches a
  flow run as well as who edits it.
- Flow entries in the event log are shown to users who hold view or above on
  the flow, or the manage permission.
- An error flow named by `error_flow_id` must be visible to whoever sets it,
  at view level or above.
- Export by a faceplate-level user is refused; export by a view-level user
  is allowed, since it reveals no more than the graph they can read.
- A flow's level is the highest of its owner, its own shares, and the owner
  and shares of the folder holding it and every folder above.
- Putting a flow or folder into a folder is a sharing change, so it needs
  admin on what moves and edit on the destination.
- Flow names need not be unique across owners. Creating a flow whose name a
  flow the creator can see already uses is refused; a name used only by flows
  they cannot see is accepted, so a refusal confirms nothing private.
- Slugs are readable addresses, not identities. `/flows/porch-light` or
  `/dashboard/kitchen` opens the one match the viewer can see, offers a choice
  when several are visible, and the page then lives at the item's ID.

## Requirements

### Functional requirements

- **FR-001**: The system MUST record a flow's owner at creation and treat the
  owner as admin on it.
- **FR-002**: The system MUST store flow shares to a user or a user group at
  one of faceplate, view, edit, or admin, each including the levels below
  it, and MUST refuse any other level.
- **FR-003**: Every flow route, including list, graph, versions, runs,
  tests, deploy, enablement, export, deletion, faceplates, and MCP tools,
  MUST authorize against the caller's level on that flow. A flow the caller
  holds no level on MUST answer as not found.
- **FR-004**: Faceplate level MUST admit listing the flow when it has a
  Faceplate node, reading its faceplates, setting their values, and Apply as
  feature 042 defines, and nothing else.
- **FR-005**: View level MUST admit reading the graph, versions, run history,
  faceplates, and export, and MUST refuse every change and every run.
- **FR-006**: Edit level MUST admit saving, testing, deploying, Apply with
  draft edits, enabling, disabling, and running. Admin MUST also admit
  sharing and deletion.
- **FR-007**: The global flow permissions MUST say whether a user may author
  flows at all, not whether they may act on someone else's. A new flow
  manage permission MUST admit every flow at admin level.
- **FR-008**: Referencing a shared subflow MUST require view level or above
  on it. Running a deployed parent MUST NOT depend on the author's current
  access to its subflows.
- **FR-009**: Adding, changing, and removing a share MUST write an event log
  entry naming who changed it, the flow, the grantee, and the level.
- **FR-010**: Backup and restore MUST carry flow shares.
- **FR-011**: Run history, live run and debug streams, active-run lists, and
  pending waits MUST be shown only to users at view level or above on the
  run's flow. Cancelling a run or a wait MUST require edit level.
- **FR-012**: Firing a flow's widget trigger by permission MUST require edit
  level on the flow. Deploying a dashboard MUST refuse a flow-trigger
  binding to a flow the deployer does not hold edit on; triggers marked
  public keep their own path.
- **FR-013**: Flow entries in the event log MUST be listed only to users at
  view level or above on that flow or holding the manage permission.
- **FR-014**: Copying another flow's graph, by exporting embedded subflows or
  importing a subflow into a flow, MUST require view level on the source.
- **FR-015**: Editor chat and MCP tools that read beyond the selected flow,
  such as listing flows, references, and dependencies, MUST apply the same
  per-flow checks.
- **FR-016**: The system MUST record a folder's owner and store folder shares
  at the same four levels. A folder's owner and shares MUST reach its
  subfolders and every flow inside them.
- **FR-017**: Listing folders MUST return only folders the caller holds view
  or above on, and MUST NOT name a parent the caller cannot see. A flow in a
  folder the caller cannot see MUST be reported without its folder.
- **FR-018**: Renaming a folder MUST require edit, moving or deleting it and
  managing its shares MUST require admin, and placing a flow or folder in a
  folder MUST require edit on the destination. Moving a flow MUST require
  admin on the flow.
- **FR-019**: Creating, importing, or promoting a flow MUST refuse a name only
  when a flow the caller can read already uses it. Flow and dashboard slugs
  MUST NOT be required to be unique.
- **FR-021**: A flow's or dashboard's ID MUST be its canonical address. A slug
  MUST resolve among the items the caller may read; several matches MUST offer
  a choice listing each by ID, and an item the caller cannot see MUST NOT take
  part.
- **FR-020**: Folder entries in the event log MUST follow FR-013's rule for
  the folder.

### Key entities

- **Flow owner**: the user who created the flow; admin on it.
- **Flow share**: a grant of one level on one flow to a user or a user group.
- **Folder share**: a grant of one level on a folder, reaching its subfolders
  and the flows inside them.
- **Flow manage permission**: a global permission admitting every flow.

## Success criteria

- **SC-001**: A second user with the default User role cannot list, open, or
  change a flow they were not shared.
- **SC-002**: A faceplate-level user changes and applies a faceplate value,
  and every graph route refuses them.
- **SC-003**: An edit-level user deploys a change; the same user's share
  request is refused.
- **SC-004**: A deployed flow calling a subflow its author lost access to
  still runs.
- **SC-005**: A user without a share on a flow receives none of its live run
  messages and cannot see its event log entries.
- **SC-006**: A dashboard binding a trigger on a flow its deployer cannot
  run fails to deploy.
- **SC-007**: `make test` and `make dev` pass and the development service
  stays active.

## Assumptions and scope

- Existing flows keep their recorded owners. On the development database
  every flow belongs to `admin`, so other accounts lose access to them until
  they are shared. That is the intended change and needs no migration.
- The `admin` account keeps every flow through the new manage permission,
  which the Admin role is seeded with.
- Dashboard share levels are unchanged here; feature 007 owns them.

## References

- [Faceplates](../042-faceplates/spec.md)
- [Flow graphs and deployment](../002-flow-authoring/spec.md)
- [Flow execution](../003-flow-execution/spec.md)
- [Server security and permissions](../001-server-security/spec.md)
- [Dashboard access and rendering](../007-dashboard-access-rendering/spec.md)
- [MCP and editor assistant](../012-mcp-editor-assistant/spec.md)
- [Event log](../037-event-log/spec.md)
- [Backup and restore](../036-backup-restore/spec.md)
