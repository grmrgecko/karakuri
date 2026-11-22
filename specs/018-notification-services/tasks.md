# Tasks: Configured notification services

**Feature**: `specs/018-notification-services`
**Input**: spec.md, plan.md, research.md, data-model.md, contracts and quickstart.

## Phase 1: Preparation

- [x] T001 Read guides/specs and research every upstream service constructor in specs/018-notification-services/research.md; pin notify in go.mod.

## Phase 2: User story 1

**Goal**: Encrypted settings with explicit allowed endpoints.
**Independent check**: REST masked round trips preserve targets by stable ID.

- [x] T002 [US1] Define service catalog, config validation, masks and ID-based secret restoration in pkg/integrations/notification/catalog.go and config.go.
- [x] T003 [US1] Add nested-secret hooks to pkg/integrations/integrations.go and pkg/connops/connops.go; register the notification kind.
- [x] T004 [US1] Add real API encryption, edit and permission fixtures in pkg/api/notification_handler_test.go.
- [x] T005 [US1] Add catalog-driven settings and navigation in frontend/src/routes/settings/Notifications.svelte and Layout.svelte.

## Phase 3: User stories 2 and 3

**Goal**: Deliver once to one approved endpoint across the whole catalog.
**Independent check**: Real HTTP receiver gets the expected payload; invalid IDs cause no requests.

- [x] T006 [US3] Implement all upstream variants and owned wrappers in pkg/integrations/notification/adapters.go and driver.go; mark retired/no-op adapters unavailable.
- [x] T007 [US2] Add transport, cancellation, receiver selection and redaction fixtures in pkg/integrations/notification/driver_test.go.
- [x] T008 [US2] Implement notification resolution in pkg/connops, REST/MCP catalog/discovery/send contracts in pkg/api/notification_handler.go and pkg/mcpserver/tools_notifications.go.
- [x] T009 [US2] Implement/register pkg/nodes/core/notification.go and add real engine message/retry fixtures.
- [x] T010 [US2] Add the endpoint editor control and live service-derived choices in frontend/src/lib/components/editor/ConfigForm.svelte.

## Final verification and documentation

- [x] T011 Update docs/notifications.md, docs/flow-authoring.md, docs/mcp.md and specification indexes to match behavior.
- [x] T012 Run Makefile builds/server/frontend checks and local MCP/browser verification; record actual evidence in specs/018-notification-services/quickstart.md.
- [x] T013 Restart/verify karakuri-dev.service and HTTP, inspect only feature changes, preserve unrelated work and commit.

## Dependencies and execution order

Catalog/config precede persistence and settings. Adapters depend on catalog contracts.
Send operations precede the node and endpoint picker. Settings and transport checks
use the completed shared rules. Documentation follows the final contract. No phase
requires independent implementation agents or a new branch.
