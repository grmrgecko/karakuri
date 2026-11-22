# Feature specification: Dashboard layout and publication

**Feature**: `006-dashboard-authoring`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Let authors build themed control panels from registered widget packages, edit
a shared draft, and publish versioned layouts.

## User scenarios and testing

### User story 1: Compose a control panel (Priority: P1)

An author places widgets, binds known devices, and arranges a readable grid.

**Why this priority**: A dashboard is the user-facing way to see and operate the home.

**Independent test**: Create a draft with a clock and bound entity widget and inspect
its geometry.

**Acceptance scenarios**:

1. **Given** a registered widget package, **When** the author places it with grid
   coordinates and configuration, **Then** the draft contains a placement with its own
   identity.
2. **Given** a selected control or HA/MQTT binding, **When** the widget renders it,
   **Then** presentation follows the source kind and current value.

### User story 2: Publish and restore (Priority: P1)

An author edits a draft without changing an existing published panel.

**Why this priority**: Layout changes can also change what viewers are allowed to see.

**Independent test**: Deploy a dashboard, edit its draft, compare both views, and
restore a stored version.

**Acceptance scenarios**:

1. **Given** a dashboard with a deployed version, **When** the author changes draft
   widgets or theme, **Then** ordinary viewers keep the deployed content.
2. **Given** a stored dashboard version, **When** the author restores it, **Then** the
   draft changes without publishing it or replacing live sharing settings.

### User story 3: Organize and inspect layouts (Priority: P2)

An author uses sections and preview diagnostics to check a dashboard at its target
screen size.

**Why this priority**: The same layout must remain usable on the screen it serves.

**Independent test**: Switch layout modes and render a draft at known width and height.

**Acceptance scenarios**:

1. **Given** a scroll dashboard with sections, **When** the author switches it to fit,
   **Then** sections are flattened into the fit layout.
2. **Given** a section containing widgets, **When** the author removes the section,
   **Then** its widgets are removed unless moved out beforehand.

3. **Given** a weather config without a provider, **When** it is rendered or edited,
   **Then** both use Open-Meteo, matching the catalog defaults.
4. **Given** a loaded forecast, **When** provider or location changes and the new
   request fails, **Then** the widget reports that failure instead of displaying the
   old selection's data. Late responses cannot overwrite the new selection.
5. **Given** imperial weather units, **When** a forecast topic update arrives,
   **Then** the widget continues to display imperial values.

### Edge cases

- Widget package names identify types; placement UUIDs identify instances.
- Widget config updates replace the full config; callers must merge prior fields
  themselves.
- Widgets without schemas are unchecked for config validation and produce a warning, not
  invented schema assurance.
- An undeployed dashboard displays its draft through the ordinary viewer path.

## Requirements

### Functional requirements

- **FR-001**: The system MUST create dashboards with stable identity, name, description,
  owner, layout settings, theme choice, and widget placements.
- **FR-002**: The system MUST offer only widget packages registered by currently loaded
  plugins in the placement catalog. Direct placement writes MAY reference an existing
  stored package row while its plugin is unloaded; explicit validation MUST report it
  unavailable, and it MUST confer no dashboard grant.
- **FR-003**: The system MUST express positions and dimensions in grid units and provide
  fit grids and scroll layouts with pixel row heights. Explicit validation MUST report
  nonpositive dimensions and out-of-bounds positions; placement writes MAY save invalid
  geometry for later correction.
- **FR-004**: The system MUST support section ordering, titles, collapse defaults, and
  section-relative widget rows in scroll mode.
- **FR-005**: The system MUST delete contained widgets when a section is deleted and
  flatten sections when switching to fit mode.
- **FR-006**: The system MUST persist edits immediately to the shared draft and notify
  other open editors.
- **FR-007**: The system MUST freeze layout, name, description, and selected theme with
  numbered immutable dashboard deployments.
- **FR-008**: The system MUST keep sharing policy and URL identity live rather than
  freezing them into deployment snapshots.
- **FR-009**: The system MUST serve the deployed version when one exists and the draft
  otherwise; restrict explicit draft inspection to editors.
- **FR-010**: The system MUST restore a stored version into the draft without deploying
  it, including discarding changes against the current deployment.
- **FR-011**: The system MUST keep widget package names distinct from placement IDs, and
  keep browser-injected dashboard IDs out of persisted config.
- **FR-012**: The system MUST replace widget config as an object while preserving
  unknown existing fields in client-authored updates.
- **FR-013**: The system MUST validate geometry, overlaps, config schemas/defaults, and
  known bindings through explicit validation, reporting unavailable schemas or restricted
  references as unchecked and overlaps as warnings. Deployment MUST snapshot the stored
  draft without implicitly running this diagnostic validation; validation is an authoring
  check, not a publication gate.
- **FR-014**: The system MUST use source bindings to render controls and honor
  displayOnly; support control, HA, MQTT device, and named flow-trigger bindings.
- **FR-015**: The system MUST apply dashboard and user theme preferences through the
  registered theme catalog and support dashboard export/import.
- **FR-016**: The system MUST offer draft/deployed diffs, dependency inspection,
  viewport previews, and render diagnostics before publishing.

- **FR-017**: The weather widget MUST default to Open-Meteo in rendering, configuration,
  and catalog metadata. Its catalog MUST describe units, layouts, and built-in backgrounds.
- **FR-018**: Weather data MUST belong to the selected provider, coordinates, units,
  and language. Selection changes MUST clear prior data and ignore superseded responses.
  Same-selection refresh failures MUST be visible even when a prior forecast is retained.
  Forecast subscription coordinates MUST match the server's four-decimal topic format,
  including negative coordinates. Topic updates MUST preserve the selected unit system.

- **FR-019**: Dashboard padding and gap values of zero MUST remain zero in the editor,
  settings, viewer, and image renderer so a fit-grid widget can fill the viewport.
- **FR-020**: Replacing a placement's config or a dashboard's display settings MUST
  raise its revision. A replacement naming the revision it read MUST be refused with a
  conflict when the value was replaced since. The editor MUST name it and, on a
  conflict, reload the other writer's value instead of overwriting it.
- **FR-021**: A dashboard's ID MUST be its canonical address, and every link the app
  builds MUST use it. A slug MUST resolve among the dashboards the caller may view,
  public ones included for anonymous callers. One match opens it, several MUST be
  answered as ambiguous with the matches listed by ID so the viewer can choose, and a
  dashboard the caller cannot see MUST NOT take part. A new slug MUST be refused only
  when a dashboard the caller can see already uses it ([feature 043](../043-flow-sharing/spec.md)).
- **FR-022**: The dashboard list MUST show dashboards and groups added,
  renamed, moved, shared, unshared, or removed in any session, by any path,
  without a page reload, and after the live connection reconnects. What
  appears follows the reader's access as the list API applies it.

### Key entities

- Dashboard and version: mutable draft and immutable published content with live
  sharing.
- Widget placement: package, position, dimensions, section membership, and
  configuration.
- Section and theme: scroll organization and registered visual styles.

## Success criteria

- **SC-001**: A draft edit changes no deployed placement or versioned theme until
  deployment.
- **SC-002**: Restoring a version changes no share rows or stable dashboard URL.
- **SC-003**: Removing a section removes its placements and switching to fit leaves a
  flat layout.
- **SC-004**: Validation reports an overlap or invalid binding and labels missing
  schemas as unchecked.

## Assumptions and scope

Widget configuration comes from its package, defaults, and documented schema. Editing a
binding does not invoke its control. Authoring and publication are separate from viewer
authorization in spec 007.
Config and display replacements are guarded by revisions, as FR-020 states; geometry,
page, region, and named-field patches are not, because they change one field at a time.
Theme selection is versioned by ID, while installed theme CSS and widget bundles remain
live resources.

A deployed version that cannot be found or parsed makes the layout read an error; it
never falls back to the preloaded draft, which would expose unpublished content
contrary to FR-009 and SC-001.

## References

- [Design plan](plan.md)
- [System architecture](../architecture.md)
- [docs/dashboard-authoring.md](../../docs/dashboard-authoring.md)
- [docs/dashboard-security.md](../../docs/dashboard-security.md)
- [pkg/dashops/dashops.go](../../pkg/dashops/dashops.go)
- [pkg/dashops/validate.go](../../pkg/dashops/validate.go)
- [pkg/models/dashboard_version.go](../../pkg/models/dashboard_version.go)
-
  [frontend/src/routes/DashboardEdit.svelte](../../frontend/src/routes/DashboardEdit.svelte)
