# Design plan: Plugins and extensible catalogs

**Feature**: `008-plugin-system` | **Date**: 2026-10-04 | **Spec**: [spec.md](spec.md)
**Status**: Implemented baseline

## Summary

Add integrations, nodes, widgets, providers, and their lifecycle through a common
registration contract without hard-coding each integration into the host.

This plan records the design supporting the baseline requirements. It is not
a pending implementation queue. Requirement IDs are local to this feature.

## Technical context and design

`pkg/plugin` exposes the authoring contract over `pkg/nodeapi`, `pkg/flow`,
and `pkg/integrations`. `pkg/pluginhost` owns executable discovery, subprocesses,
callbacks, and database-backed settings. External binaries use hashicorp
go-plugin with gRPC and the SDK callback channel. Built-ins load through the
same registrar into engine and integration catalogs. Keeping `pkg/plugin` free of
`pkg/pluginhost` keeps plugin binaries, the remote agent included, free of GORM and
the embedded SQLite engine. Widget bundles are served from plugin directories, not
embedded, matching the SPA and avoiding a compile-time dependency on the npm build.

Manifest widget entries feed `pkg/widgets`; API/topic scope feeds
`pkg/dashscope`, which substitutes placement placeholders in both. Weather, crypto,
and stock declare namespaces for public data. Calendar declares its event route and
update topic per placement, and its data handler accepts the host's dashboard grant
when it names the dashboard in the path. Widget-only directories need no executable. Built-in resource
manifests attach to their compiled contribution rather than loading a duplicate.
Providers and field controls are catalog data, including fallback editor types.
Eligible node metadata also feeds feature 026's generated script function
catalog. Script calls use the same external execution transport and receive no
new host privileges.

## State and data model

`PluginState` stores operator enablement and last load results. Settings are
namespaced model records. Widget rows persist across unload to preserve metadata
for stored placements, but are removed when their source directory disappears.
Contribution ownership permits withdrawal of unloadable registrations. Port types
and field controls remain registered; removing port types would change
order-indexed colors. Plugin executables own no host database.

## Contracts and failure handling

The manifest names an optional `entryPoint` and widget packages with tag names,
resource paths, defaults, schema, and scope. Widget package and cross-owner AI
provider registrations reject collisions. Other type registries replace entries
by name, so plugin authors must avoid those conflicts; ownership tracking alone
does not prevent them.
`HostAPI.RegisterTopicPermission` makes a plugin topic subscribable; declaring a
widget scope alone does not register its gate. Plugin HTTP proxies carry a
trusted dashboard grant separately from forwarded client headers. Connection
factories and node proxies retain their shared metadata and execution APIs.
The grant carries the authorizing dashboard ID rather than a flag, so dashboard-keyed
plugin routes can compare it with their path. `httpsdk.Allowed` applies an API key's
permission limit on top of the host's user check.
A widget's scope `api` entries are `METHOD /path` suffixes after `/api/plugins/{name}`,
with `*` matching one segment and `**` the rest; a declaration missing its method or
path is dropped rather than widened. Topic patterns may carry `{dashboard}` and
`{widget}`, substituted per placement for an exact grant. A widget with no scope, or
whose plugin is unloaded, grants nothing.
Across the process boundary, `NodeStore.Update` is compare-and-swap: the closure runs
plugin-side and commits under the host store lock, retrying on conflict. Plugin drivers
live in the plugin process behind a host proxy fed by the `StartDriver` stream, and a
plugin node resolves only its own process's drivers, since a Go interface cannot cross
processes. While any plugin directory is refused at scan time, state for plugins
missing from the scan is kept, since the refused directory may be theirs.

## Constitution check

- Keep the authorization and failure boundaries described above explicit.
- Route authoring through shared operations and preserve draft/deployment separation
  where applicable.
- This baseline adds no schema, wire, identity, or encryption-format migration. Future
  changes must assess the affected persistent state before implementation.
- Use the component checks below and the service handoff checks in `AGENTS.md`.

## Verification

Run `make dev TARGET=server` and `make test TARGET=server` for registration/host
changes; use a full development build when widget bundles or plugin executables change.
Plugin host, widget loader, registration, and topic-gate tests exercise lifecycle and
collisions. A contributed-node fixture checks generated script contracts and execution
through the plugin transport. Widget UI changes require browser checks and frontend validation.

These are the acceptance checks for changes to this feature. They do not claim
that every live scenario ran while the baseline documents were written.

## Risks and limits

External plugins are trusted executables, not the user-script sandbox. Port-type
identities are append-only for visual stability. Plugin enablement does not erase
dashboard placements. The durable wait store (`nodeapi.WaitParker`, `Resumer`) does not
cross the plugin boundary, so a parked plugin node's wait ends on restart or redeploy.

New behavior needs reviewed requirements and an update to this plan.

## References

- [Server and agent audit](../server-agent-audit.md)

- [System architecture](../architecture.md)
- [docs/plugins.md](../../docs/plugins.md)
- [docs/dashboard-security.md](../../docs/dashboard-security.md)
- [pkg/plugin](../../pkg/plugin)
- [pkg/pluginhost](../../pkg/pluginhost)
- [pkg/widgets/loader.go](../../pkg/widgets/loader.go)
- [pkg/providers](../../pkg/providers)
