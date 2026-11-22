# Design plan: Dashboard layout and publication

**Feature**: `006-dashboard-authoring` | **Date**: 2026-10-04 | **Spec**:
[spec.md](spec.md)
**Status**: Implemented baseline

## Summary

Let authors build themed control panels from registered widget packages, edit a shared
draft, and publish versioned layouts.

This plan records the design supporting the baseline requirements. It is not
a pending implementation queue. Requirement IDs are local to this feature.

## Technical context and design

`pkg/dashops` owns dashboard mutations, export, validation, and widget
operations. `pkg/models` holds draft layout and versions. The Svelte dashboard
editor renders web-component widgets loaded from plugin resources and sends
placement/config updates through common service operations. Sections belong to
scroll layouts; fit mode scales an explicit row/column grid to the viewport.

`pkg/widgets` and `pkg/themes` supply catalogs. Validation overlays stored
config on package defaults without mutating either and uses local JSON Schema
references only. Configuration without a schema remains explicitly unchecked.
Overlaps are warnings. These diagnostics run through explicit validation; deployment
snapshots the draft without calling them.
Placement catalogs list loaded packages, but writes resolve stored package rows and
can retain unloaded packages or invalid geometry. Validation reports those problems;
an unloaded package grants no scope and serves no bundle.

The dashboard list follows the `dashboards` topic for FR-022. The notice comes
from the database callbacks feature 045's plan describes, carries no data, and
makes the list read dashboards and groups again through the API.

## State and data model

Dashboard, section, and placement records describe the mutable draft.
`DashboardVersion` snapshots content for publication. Name, description, theme,
layout, sections, and placement configs are versioned; owner, shares, public
policy, and URL identity remain live. Config stores JSON but authoring operations
accept objects. Browser-provided context IDs are injected at render time.
Theme IDs are frozen, while their installed CSS and widget bundles remain live.
A placement's `config_revision` and a dashboard's `display_revision` rise in the same
transaction as each replacement of those values. A write naming `expected_config_revision`
or `expected_display_revision` is applied only while the revision still matches and is
otherwise refused with 409; `TestRevisionsRefuseStaleReplacements` pins both. Moves and
field patches do not raise them, so the editor's own drags never conflict with its config
saves. `ApplyDeployedLayout` returns an error when the deployed version cannot be found
or parsed rather than leaving the draft in place.

## Contracts and failure handling

`get_dashboard` selects draft or deployed view; a stored widget config is a
JSON string which programmatic callers parse before editing. Placement updates
replace config rather than deep-merging it. An empty section ID moves a widget
to the sectionless region. Theme ID zero clears selection. Binding IDs must come
from actual controls, connections/entities, devices, or flow trigger definitions.
Diff, dependency, and validation reads require document edit access as specified
in the MCP and dashboard security guides.

## Weather widget recovery

Align the renderer's omitted-provider default with Open-Meteo in the panel and
manifest. Advertise the weather configuration schema and built-in photo background
so programmatic authors can use one widget instead of overlapping placements.
Scope HTTP results, errors, timers, and topic callbacks to the selected forecast
request. Clear data when that selection changes and reject superseded responses.
Use topic updates to refetch the cached HTTP response in the selected units, since
broadcasts contain metric data. Match server/browser coordinate rounding and
four-decimal topic formatting for west/south coordinates and trailing zeroes.
Use nullish spacing defaults in the dashboard editor and viewer so saved zero padding
and gaps are honored in both interactive layouts and image renders.
No stored dashboard configuration, deployment, identity, or key migration is needed.

Verify provider omission, failure after success, response races, presentation-only
edits, and imperial topic refreshes with the real browser widget and controlled API
responses. Check the live forecast and photo background without publishing test edits.

## Constitution check

- Keep the authorization and failure boundaries described above explicit.
- Route authoring through shared operations and preserve draft/deployment separation
  where applicable.
- This baseline adds no schema, wire, identity, or encryption-format migration. Future
  changes must assess the affected persistent state before implementation.
- Use the component checks below and the service handoff checks in `AGENTS.md`.

## Verification

Run server and frontend builds/tests through Makefile targets. Dashboard operation,
validation, and API tests cover versioning and access. In the browser check fit/scroll
geometry, section movement, widget controls, and draft refresh after another client
edits. Use a read-only draft render to inspect clipping and errors; visual-only drafts
should remain unsaved.

These are the acceptance checks for changes to this feature. They do not claim
that every live scenario ran while the baseline documents were written.

## Risks and limits

Widget configuration comes from its package, defaults, and documented schema. Editing a
binding does not invoke its control. Authoring and publication are separate from viewer
authorization in spec 007.

New behavior needs reviewed requirements and an update to this plan.

## References

- [Server and agent audit](../server-agent-audit.md)

- [System architecture](../architecture.md)
- [docs/dashboard-authoring.md](../../docs/dashboard-authoring.md)
- [docs/dashboard-security.md](../../docs/dashboard-security.md)
- [pkg/dashops/dashops.go](../../pkg/dashops/dashops.go)
- [pkg/dashops/validate.go](../../pkg/dashops/validate.go)
- [pkg/models/dashboard_version.go](../../pkg/models/dashboard_version.go)
-
  [frontend/src/routes/DashboardEdit.svelte](../../frontend/src/routes/DashboardEdit.svelte)

## Addresses (FR-021)

The dashboard ID is canonical and app links use it. Slugs keep a plain index rather
than a unique one (`relaxSlugIndexes` in `pkg/migrate` drops the old unique index).
`dashops.ResolveFor` matches a slug among the dashboards the caller may view, and the
API's `resolveDashboard` also admits the dashboard a render token names. Several
matches answer `AMBIGUOUS` with the matches by ID; `GET /api/dashboards/by_slug/{slug}`
feeds the frontend chooser in `ResolveRef.svelte`. Feature 043's plan records the
shared design.
