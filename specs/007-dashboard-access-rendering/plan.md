# Design plan: Dashboard sharing and rendering

**Feature**: `007-dashboard-access-rendering` | **Date**: 2026-10-04 | **Spec**:
[spec.md](spec.md)
**Status**: Implemented baseline

## Summary

Share panels without exposing unrelated devices or owner credentials, and render the
same scoped view as images or documents.

This plan records the design supporting the baseline requirements. It is not
a pending implementation queue. Requirement IDs are local to this feature.

## Technical context and design

`pkg/dashscope` parses registered host binding shapes and derives grants
from the applicable layout. REST handlers first evaluate their normal permission
and consult dashboard scope where supported. `pkg/mq` combines registered topic
gates with scope for WebSocket subscriptions. `X-Karakuri-Dashboard` identifies
the requested document; it never authenticates the caller. Plugin topic and route
patterns have `{dashboard}` and `{widget}` replaced per placement before they enter
the scope. A refused subscription is answered with an `error` frame naming the topic;
the page's socket store logs it and never dispatches it to topic handlers, since its
payload is the refusal and not a reading.

`pkg/render` queues bounded jobs and uses go-rod with Chromium to load the
actual dashboard page. It supplies a job credential rather than the owner's
session. Render scope shares the binding parser but is always read-only. A draft
job carries an explicit draft flag through HTTP and WebSocket scope checks.

Render-token validity is checked for HTTP requests, when resolving a new socket's
identity, and on every revalidation of an open socket, which closes a socket whose job
has settled. Removed shares and unloaded topic gates withdraw existing subscriptions on
the next revalidation, which those changes trigger. Uploaded files are checked by
`uploads.ServeFile` against the placement's dashboard; `TestWidgetUploadsFollowDashboardAccess`
pins anonymous refusal, cookie access, public access, and unpublished placements.

## State and data model

Share rows and live public policy sit outside version snapshots. A derived
scope is computed from registered widgets and the authorized layout. Render
and ordinary scopes use draft bindings when nothing has been deployed. Public draft
edits therefore change public grants immediately. On a cache miss, `servedPlacements`
returns an error for a deployed version it cannot find or parse, so the scope is empty;
`TestUnreadableDeploymentServesNoDraft` pins that against the earlier draft fallback.
Render scope never permits control. Ordinary scope permits control for owners and
direct/group shares at view level or above, or when `public_control` is enabled.
A read share, or being signed in to see a public dashboard, does not grant control.
`dashscope.CanOperate` is that decision; the single-dashboard response reports it as
`can_operate`, and the widget bridge's `canOperate()` lets core widgets draw readings
instead of controls. Share creation refuses levels outside read, view, edit, and admin,
and `TestDashboardShareLevels` pins both the refusal and `can_operate` per level.
Plugins asking `CheckDashboardAccess` to serve watched data ask for `read`. Render
records store token digests and lifecycle state; validity ends at completion,
failure, or sweeper settlement. Files are retained for the configured ad-hoc
period; scheduled results remain until replacement or their 30-day expiry. Uploaded widget files
remain separately addressed by placement/upload identity.

## Contracts and failure handling

HA writes need explicit allowed entity IDs and reject area/device/floor/label
selection in both request containers. MQTT device views omit scripts and broker
configuration. Plugin grants use METHOD/path patterns and registered topics;
plugins keyed by dashboard must compare the trusted `DashboardGrant` to path
identity. Renderer output supports the six documented formats. Diagnostics
remove URL credentials, query strings, fragments, and render tokens.
Controls and named widget triggers with `allow_public` keep their independent public
REST paths. A dashboard's display-only setting or render scope adds no write grant,
but also does not revoke those public routes or ordinary subsystem permissions.

HA writes from a dashboard must also use a service of the bound entity's domain; the
`script` domain admits only `turn_on`, `turn_off`, and `toggle`, because every script
is its own service and `shell_command`, `rest_command`, and `recorder.purge_entities`
take `entity_id` as data. List endpoints (HA states, controls, MQTT devices) filter to
the scope; single-entity routes (history, to-do, camera, art) refuse an unbound
entity, and art is relayed only from HA's `/api/media_player_proxy/` path. A row
stored as `control: "value"` reads as display-only, since frozen versions cannot be
rewritten. Deploying a `flow-trigger` binding needs edit access to the flow, and a
flow the deployer holds nothing on is reported as missing.

The `pkg/mq` gate denies a topic no registered gate matches, so a plugin topic without
`RegisterTopicPermission` is cut off. The hub revalidates every socket every 30
seconds, after each successful API or MCP write, on plugin gate unload, and when a
render job settles. A session that stops validating leaves the socket anonymous with
`auth_expired`, and the browser re-presents a token in an `auth` frame without
reconnecting; a subscription that no longer passes is withdrawn with a `revoked`
frame. Uploaded files authenticate through the `karakuri_media` cookie, scoped to
`/widget/`, mirroring the page's access or render token because `<img>` and `<video>`
carry no Authorization header; refusals answer not found.

Dashboard writes follow dashboard sharing (`edit` to edit and deploy, `admin` to
share and delete), with `api:dashboards:manage` and `api:dashboard-groups:manage` as
the overrides. Moving a dashboard between groups needs `admin` on it and `edit` on the
target group. The embedded owner is redacted to ID and username for callers who are
neither the owner nor holders of `api:dashboards:read`.

Render keys (FR-019, FR-020) are API keys with `dashboard_id` set and no
permissions. The ordinary auth middleware treats one as no credential: `Auth`
answers 401 and `OptionalAuth` leaves the request anonymous. Only the render URL
group uses `RenderURLAuth`, which admits it, and `renderViewable` requires
`RenderKeyAllows` (the key names this dashboard) and `CanView` under the key's empty
limit, so the manage override never opens a dashboard for it. Creation refuses a
dashboard the creator sees only through manage, since such a key could open
nothing. `Dashboard.Delete` removes the dashboard's keys in its transaction.
`GET /api/api_keys/{id}/value` decrypts a render key for its owner over a session
only. The render page lists keys by `dashboard_id` in a table, adds one under a name
the author gives, deletes one, remembers the key in use per dashboard in local
storage, and appends `token` to the copied render, embed, and schedule addresses. `TestRenderKeyOpensOnlyItsDashboardRender` pins the
contract.

## Constitution check

- Keep the authorization and failure boundaries described above explicit.
- Route authoring through shared operations and preserve draft/deployment separation
  where applicable.
- This baseline adds no schema, wire, identity, or encryption-format migration. Future
  changes must assess the affected persistent state before implementation.
- Use the component checks below and the service handoff checks in `AGENTS.md`.

## Verification

Run `make dev TARGET=server` and `make test TARGET=server`. Dashboard scope, topic gate,
render job, and API tests check the authorization matrix, forbidden HA targets, and
token lifecycle. Rendering changes require real Chromium previews at the requested
viewport. UI sharing changes also need frontend checks and browser verification.

These are the acceptance checks for changes to this feature. They do not claim
that every live scenario ran while the baseline documents were written.

## Risks and limits

Ordinary subsystem permissions still grant their full subsystem access. Plugin scope
patterns can intentionally grant a namespace, such as forecasts for other locations.
Upload URLs and public render-result URLs must be treated according to their documented
access behavior.

New behavior needs reviewed requirements and an update to this plan.

## References

- [Server and agent audit](../server-agent-audit.md)

- [System architecture](../architecture.md)
- [docs/dashboard-security.md](../../docs/dashboard-security.md)
- [pkg/dashscope/scope.go](../../pkg/dashscope/scope.go)
- [pkg/dashscope/request.go](../../pkg/dashscope/request.go)
- [pkg/mq/gate_test.go](../../pkg/mq/gate_test.go)
- [pkg/render/render.go](../../pkg/render/render.go)
- [pkg/render/scheduler.go](../../pkg/render/scheduler.go)
- [pkg/api/dashscope_test.go](../../pkg/api/dashscope_test.go)
