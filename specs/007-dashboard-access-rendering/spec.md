# Feature specification: Dashboard sharing and rendering

**Feature**: `007-dashboard-access-rendering`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Share panels without exposing unrelated devices or owner credentials, and
render the same scoped view as images or documents. Let the render page list and
generate API keys that only open one dashboard's render URLs, and put a chosen key
into the copied render, embed, and schedule addresses.

## User scenarios and testing

### User story 1: Share a bounded view (Priority: P1)

An owner shares a panel with a housemate or public viewer without granting
whole-subsystem access.

**Why this priority**: Publishing one tile must not expose every device in the house.

**Independent test**: Read bound and unbound entities using a dashboard-scoped
restricted viewer.

**Acceptance scenarios**:

1. **Given** a public deployed dashboard bound to one HA entity, **When** an anonymous
   viewer requests its entity data, **Then** only that bound entity is returned.
2. **Given** a private dashboard outside a user's shares, **When** the user guesses its
   slug or dashboard header, **Then** the layout remains inaccessible and the header
   grants nothing.

### User story 2: Separate reading from control (Priority: P1)

An owner chooses whether anonymous viewers may press controls and marks readings as
display-only.

**Why this priority**: Public visibility must not implicitly grant device operation.

**Independent test**: Exercise the same binding as anonymous, shared signed-in, and
display-only viewers.

**Acceptance scenarios**:

1. **Given** a public dashboard with public_control off and a bound object without
   independent `allow_public` access, **When** an anonymous viewer
   invokes a bound action, **Then** the action is denied.
2. **Given** a signed-in viewer holding a view share or above on a bound control
   without an independent write grant or `allow_public` access, **When** the viewer
   invokes it, **Then** the permitted bound action runs unless the binding is
   display-only.
3. **Given** a signed-in user with public access and no dashboard share, **When**
   `public_control` is off and the user invokes an action through dashboard scope
   without independent authorization,
   **Then** the action is denied.
4. **Given** a private dashboard shared with a housemate at read level, **When** the
   housemate opens it, **Then** it shows its readings, its widgets draw no controls,
   and a bound action invoked through dashboard scope is denied unless
   `public_control` allows it.
5. **Given** a share request naming an unknown access level, **When** it is
   submitted, **Then** it is refused and no share is stored.

### User story 3: Render a panel without operating it (Priority: P2)

A viewer requests a dashboard image or an operator schedules recurring renders at a
stable address.

**Why this priority**: Rendering must use a scoped read credential without adding
control permissions.

**Independent test**: Render a deployed or editor draft view and probe another dashboard
with the job credential.

**Acceptance scenarios**:

1. **Given** a pending render job for one dashboard, **When** the browser loads it,
   **Then** its read-only credential admits only that view and its scoped data.
2. **Given** a recurring render schedule, **When** a new result finishes, **Then** the
   stable result address serves the latest completed output.
3. **Given** a private dashboard, **When** its owner adds a render key named "Hallway
   ESP32" on the render page and copies the render URL with that key in use, **Then** the URL opens the
   render without signing in, and the same key opens no other dashboard's render and
   no API route.
4. **Given** a render key, **When** its owner loses view access to the dashboard or
   deletes the key, **Then** URLs carrying it stop working.

### Edge cases

- Unknown WebSocket topics fail closed; raw MQTT access is not granted by a device tile.
- A dashboard HA call targeting an area/device/floor/label or omitting the entity target
  is refused.
- Disabled widget plugins grant no capabilities even if stored placements remain.
- An uploaded widget file on a private dashboard is refused to a request without view
  access, and answered as not found.
- Public control and trigger routes remain available independently of display-only
  bindings and render credentials.
- A render key presented to an API route, a viewer page, or another dashboard's render
  URL authenticates nothing.

## Requirements

### Functional requirements

- **FR-001**: The system MUST apply dashboard ownership, user/group shares, public
  visibility, and read/view/edit/admin access on the server. Read shows the dashboard;
  view also operates it. Each level includes the ones below it. Creating a share MUST
  refuse any other level.
- **FR-002**: The system MUST require edit access for layout/deploy operations and admin
  access for sharing/deletion, allowing the explicit management permission.
  A dashboard group's shares and ownership reach the dashboards in it, so moving a
  dashboard into another group MUST require admin access to the dashboard and edit
  access to the target group, and listing a group's shares MUST require edit access
  to the group.
- **FR-003**: The system MUST treat a dashboard header or subscribe-frame dashboard ID
  as a claim, requiring actual view access before deriving grants.
- **FR-004**: The system MUST derive ordinary viewer capabilities from deployed
  registered widget bindings when a deployment exists, otherwise from the undeployed
  draft served to viewers. An editor's draft MUST also grant its authoring-view bindings.
  A binding that references dashboard variables MUST grant what each combination of
  their option values produces, including options a query resolves, as feature 025
  bounds it. A variable that follows an entity MUST grant a read of that entity.
- **FR-005**: The system MUST filter control, HA state, and MQTT device lists to granted
  bindings for callers without subsystem read permissions, also including controls
  marked `allow_public` independently of dashboard bindings.
- **FR-006**: The system MUST permit owners and users with a direct or group dashboard
  share at view level or above to operate writable permitted bindings. Read-share,
  anonymous, and signed-in public-only viewers MUST also require `public_control` for
  operation through dashboard scope. A single-dashboard response MUST report whether
  the caller may operate it, and widgets MUST draw readings instead of controls when
  the caller may not.
- **FR-007**: Dashboard scope MUST grant no writes from display-only bindings and MUST
  restrict flow-trigger actions to the bound trigger name. Independent subsystem grants
  and object-level `allow_public` policy MUST retain their own authorization paths.
- **FR-008**: The system MUST restrict dashboard HA writes to explicit bound entities
  and reject broad targets in both target and service_data. The service MUST be in
  each targeted entity's domain, and a `script` call MUST be `turn_on`, `turn_off`, or
  `toggle`, since every script is also a service of its own.
- **FR-009**: The system MUST keep raw MQTT publish and raw topic subscriptions
  permission-only and deny subscriptions without a matching registered topic gate.
- **FR-010**: The system MUST honor plugin-declared topic/API scope patterns and carry a
  trusted dashboard grant outside client-provided headers.
- **FR-011**: The system MUST keep unknown/unloaded widget grants empty and enforce
  placement/dashboard substitutions in the topic and API patterns of plugin scopes that
  declare them, so a pattern naming `{dashboard}` and `{widget}` grants only that
  placement's own topic or route.
- **FR-012**: The system MUST mint a per-render job credential stored as a digest, valid
  only while pending or processing and scoped to one dashboard with no control
  permission.
- **FR-013**: The system MUST allow deployed renders with view access and explicit draft
  renders with edit access, without allowing ordinary render credentials to request
  drafts.
- **FR-014**: The system MUST bound render workers, queued jobs, execution time, and
  retained files, refusing a full queue.
- **FR-015**: The system MUST render PNG, JPG, PDF, GIF, BMP, or TIFF and support
  cron/timezone schedules serving the latest output at a stable URL. Scheduled outputs
  MUST expire when replaced or after 30 days without replacement.
- **FR-016**: The system MUST settle abandoned jobs and expire their grants; redact
  credentials and URL secrets from diagnostics and omit request headers/bodies.
- **FR-017**: The system MUST serve uploaded widget files only to callers who may view
  the placement's dashboard, resolving the `karakuri_media` cookie or a bearer token,
  and MUST require edit access for a placement the deployed version does not hold. A
  render credential MUST see only its own dashboard's files.
- **FR-018**: Controls marked `allow_public` MUST allow reads/sets, and named widget
  triggers marked `allow_public` MUST allow invocation through their public REST paths
  without a dashboard grant. Dashboard `public_control` and display-only bindings
  MUST NOT revoke this object-level policy.
- **FR-019**: A dashboard render key MUST be an API key naming one dashboard and holding
  no permissions. It MUST authenticate only that dashboard's direct and schedule render
  URLs, and only while its owner may view the dashboard; everywhere else it MUST
  authenticate nothing. Deleting the dashboard MUST delete its render keys.
- **FR-020**: The render page MUST list the caller's render keys for the dashboard in a
  table with each key's name, prefix, creation, and last use; add a key under a name the
  caller gives, such as the device that will carry it, with the API key write
  permission and view access; delete a key; and let the caller use one key, which the
  copied render URL, embed snippet, and schedule URLs carry as `token`. A key's value
  MUST be revealed only to its owner over a signed-in session, and render keys MUST
  also appear in the API key settings list.
- **FR-021**: Deploying a dashboard MUST refuse when its deployer cannot use a
  connection that an entity, device, camera, condition, variable, or chart binding
  reaches, and saving a chart or time-series variable query MUST refuse a connection
  the editor cannot use, as [feature 047](../047-connection-agent-access/spec.md)
  defines. Viewers keep reaching what the deployed layout binds.
- **FR-022**: A refused WebSocket subscription MUST NOT be delivered to that topic's
  widget handlers as a reading. A widget whose live data is refused keeps what it read
  over HTTP, or shows its own error, in the browser and in renders alike.

### Key entities

- Dashboard share and scope: access level and layout-derived read/write bindings.
- Render job: dashboard/view, output parameters, status, token digest, and resulting
  file.
- Render schedule: cron/timezone, enabled state, and stable latest-result identity.
- Render key: an API key bound to one dashboard with no permissions.

## Success criteria

- **SC-001**: All unbound entity/control/device requests in the fixture are denied or
  filtered out for a restricted viewer, except independently public controls.
- **SC-002**: Public binding reads succeed while anonymous and signed-in public-only
  writes fail without `public_control` when the fixture has no independent write grant;
  display-only bindings contribute no write capability.
- **SC-003**: A render credential grants zero control capabilities and cannot read
  another private dashboard, an unauthorized draft, or permission-only APIs. Independently
  public objects remain accessible through their public routes.
- **SC-004**: A full render queue rejects work and a completed/expired job credential no
  longer opens its view.
- **SC-005**: A render key opens its private dashboard's render URL and schedule URL, and
  is refused by another dashboard's render URL and by every `/api` route in the fixture.

## Assumptions and scope

Ordinary subsystem permissions still grant their full subsystem access. Plugin scope
patterns can intentionally grant a namespace, such as forecasts for other locations.
Public render-result URLs must be treated according to their documented access behavior.
Render scope grants no writes, but it does not disable anonymous routes. Public controls
and public widget triggers can still be operated without using the render grant.
An undeployed public dashboard serves its mutable draft, so draft binding edits change
public scope immediately. There is no published version to isolate those edits.

A deployed version that cannot be found or parsed grants nothing and serves no layout; it
never falls back to draft bindings, which would expose unpublished bindings to ordinary
viewers.

Render sockets are revalidated with every other socket, so a settled job's socket is
closed, and unsharing or plugin unload withdraws existing subscriptions, as feature 001
FR-016 states.

## References

- [Design plan](plan.md)
- [System architecture](../architecture.md)
- [docs/dashboard-security.md](../../docs/dashboard-security.md)
- [pkg/dashscope/scope.go](../../pkg/dashscope/scope.go)
- [pkg/dashscope/request.go](../../pkg/dashscope/request.go)
- [pkg/mq/gate_test.go](../../pkg/mq/gate_test.go)
- [pkg/render/render.go](../../pkg/render/render.go)
- [pkg/render/scheduler.go](../../pkg/render/scheduler.go)
- [pkg/api/dashscope_test.go](../../pkg/api/dashscope_test.go)
