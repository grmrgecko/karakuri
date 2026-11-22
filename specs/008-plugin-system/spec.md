# Feature specification: Plugins and extensible catalogs

**Feature**: `008-plugin-system`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Add integrations, nodes, widgets, providers, and their lifecycle through a
common registration contract without hard-coding each integration into the host.
Expose eligible contributed node contracts to the script node SDK under feature 026.

## User scenarios and testing

### User story 1: Load a contribution (Priority: P1)

An operator installs a plugin directory and enables its nodes, connection kinds, or
widgets.

**Why this priority**: Extensions must behave like built-in features.

**Independent test**: Load an executable or widget-only plugin and inspect its catalog
entries.

**Acceptance scenarios**:

1. **Given** a valid widget-only manifest, **When** the plugin loads, **Then** its
   widget packages are registered without spawning an executable.
2. **Given** a plugin claims an already registered widget package name, **When** it
   loads, **Then** the conflict is rejected rather than shadowing the existing package.

### User story 2: Disable and recover (Priority: P1)

An operator disables a plugin and later enables it without destroying existing widget
placements.

**Why this priority**: Configuration should survive a deliberate temporary unload.

**Independent test**: Disable and re-enable a plugin referenced by a stored dashboard.

**Acceptance scenarios**:

1. **Given** a loaded external plugin, **When** the operator disables it, **Then** live
   contributions and subscriptions are withdrawn and packages stop serving.
2. **Given** persisted placements of that plugin, **When** the operator enables it
   again, **Then** their metadata and placements resolve again.

### User story 3: Use schemas and scoped APIs (Priority: P2)

A plugin author declares config controls, provider definitions, and the widget's data
access.

**Why this priority**: The host needs one way to validate and authorize extensions.

**Independent test**: Register a field control and a plugin API/topic gate with explicit
scope.

**Acceptance scenarios**:

1. **Given** a custom field control unknown to the frontend, **When** its field is
   edited, **Then** the base datatype editor remains usable.
2. **Given** a plugin publishes an unregistered topic, **When** a viewer subscribes,
   **Then** the subscription is denied.
3. **Given** an eligible plugin node, **When** the active script function catalog is
   derived, **Then** its registered configuration, ports, help, and schemas appear under
   feature 026 without a separate hand-written editor declaration.

### Edge cases

- Compiled-in plugins are listed but cannot be unloaded by an operator.
- Port-type registration order affects color identity; types are not removed and
  reordered on unload. Field control definitions also stay registered.
- A missing plugin directory removes its persisted discovery state/settings/widget rows
  on rescan, while a load failure preserves them.

## Requirements

### Functional requirements

- **FR-001**: The system MUST support compiled-in, external executable, and widget-only
  plugins under the same contribution ownership rules.
- **FR-002**: The system MUST discover manifests from supported resource paths or
  configured paths, with an explicit empty list disabling discovery.
- **FR-003**: The system MUST register nodes, connection kinds, port types, field
  controls, AI providers, settings, widgets, HTTP APIs, permissions, topic gates, and
  provider data as declared. Eligible node metadata MUST also feed the derived script
  function catalog under feature 026 without a second editor-specific registration.
- **FR-004**: The system MUST record contribution ownership for unload and reject widget
  package names or AI provider names already owned by another plugin.
- **FR-005**: The system MUST execute external plugin calls through the plugin transport
  while preserving normal validation, retry, value-pull, trigger, and feature 026
  script-invocation rules.
- **FR-006**: The system MUST keep compiled-in plugins read-only in lifecycle
  administration and attach their resource-directory widgets to the existing built-in
  entry.
- **FR-007**: The system MUST retain enable choices and load errors across restarts,
  using defaultDisabled only when first discovering a plugin.
- **FR-008**: The system MUST withdraw unloadable contributions, subscriptions, and
  executable processes on disable; preserve stored widget rows for re-enable.
- **FR-009**: The system MUST retain port types and field control definitions across
  unload; port-type ordering MUST remain stable so remaining type colors do not shift.
- **FR-010**: The system MUST drop state/settings/widget catalog rows for vanished
  directories at rescan but retain them for present plugins that fail to load.
- **FR-011**: The system MUST serve widget bundles as install resources with
  package-scoped unique names and optional local-only JSON schemas and defaults.
- **FR-012**: The system MUST namespace settings under plugins.<name>.<key> and enforce
  plugin-admin permissions.
- **FR-013**: The system MUST declare plugin widget topic and API grants separately and
  require explicit topic registration for subscriptions. Both kinds of pattern MAY name
  `{dashboard}` and `{widget}`, which the host replaces with the placement's own IDs.
  A bundled plugin whose widget reads its own API or topics MUST declare them, so a
  viewer without the plugin's permission, including a render, can read what the
  widget shows.
- **FR-014**: The system MUST keep the public plugin SDK separate from host database and
  process-management dependencies.

### Key entities

- Plugin manifest/state: identity, contributions, default enablement, saved choice, and
  load result.
- Registration: contribution name/type tagged with its owner.
- Widget package: web components, defaults, config schema, scope, and served resource
  path.

## Success criteria

- **SC-001**: A widget-only plugin loads with no executable process and appears in the
  package catalog.
- **SC-002**: Disabling a plugin removes its unloadable contributions and live grants,
  retains port types and field controls, and re-enabling resolves existing placements.
- **SC-003**: Widget package and cross-owner AI provider name conflicts fail without
  replacing the original catalog entry.
- **SC-004**: An unknown custom field control still renders the base editor and an
  ungated topic remains denied.
- **SC-005**: An eligible contributed node appears in the derived script function
  catalog with its registered config and output contract and executes through the same
  plugin transport when called.

## Assumptions and scope

External plugins are trusted executables, not the user-script sandbox. Port-type
identities are append-only for visual stability. Plugin enablement does not erase
dashboard placements.

Node, connection-kind, port-type, and field-control registries do not enforce
cross-plugin name ownership. Authors must use unique names; these specs do not
promise collision protection for those registrations.

## References

- [Design plan](plan.md)
- [Script node SDK](../026-script-node-sdk/spec.md)
- [System architecture](../architecture.md)
- [docs/plugins.md](../../docs/plugins.md)
- [docs/dashboard-security.md](../../docs/dashboard-security.md)
- [pkg/plugin](../../pkg/plugin)
- [pkg/pluginhost](../../pkg/pluginhost)
- [pkg/widgets/loader.go](../../pkg/widgets/loader.go)
- [pkg/providers](../../pkg/providers)
