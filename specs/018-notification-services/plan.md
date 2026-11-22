# Design plan: Configured notification services

**Feature**: `specs/018-notification-services` | **Date**: 2026-10-06 | **Spec**: [spec.md](spec.md)

## Summary

Add notification settings and a server action node using nikoksr/notify v1.6.0.
The service owns credentials and named endpoint targets. Flow config contains
only connection and endpoint IDs plus notification templates.

## Technical context

- Go 1.26.5 and Svelte 5; pin the latest tagged notify release, v1.6.0.
- `pkg/integrations/notification` owns the 36-variant catalog, validation,
  nested secret handling, per-send notifier construction and delivery.
- Existing encrypted Connection rows own settings; no tables, keys or migrations.
- `pkg/connops` resolves running notification drivers; REST and MCP use it.
- `frontend/src/routes/settings/Notifications.svelte` renders catalog-driven
  credentials and explicit endpoint rows. ConfigForm adds an endpoint picker.
- At most four sends remain active, including timed-out SDK operations. Endpoint
  lists hold 1–100 entries, targets up to 32 KiB, subject/body up to 4/64 KiB,
  and node timeouts accept 1–120 seconds (default 30).

## Constitution check

- The node is impure and nonretryable. Delivery may be uncertain after errors.
- Draft/node tests have real effects; settings changes affect future sends.
- Connections read/write/invoke permissions govern discovery/configuration/direct
  sends. Flow actions retain existing execution authorization.
- Encryption and persistent formats remain readable. Stored notification config
  is a new connection kind, not an application DB migration.
- Verify through real local receivers, Makefile builds/tests, MCP and browser.

## Design and contracts

Catalog metadata supplies service-specific string credential fields and endpoint
formats. Parse structured credentials/endpoints or JSON strings for the generic
connection form. Validate before persistence. Extend ConnectionKind with mask and
restore hooks for structured secrets; notification hooks redact credential fields
and all endpoint targets. Sentinels restore by stable endpoint ID, never position.
Service type is fixed after creation. Removed endpoint targets are discarded.

Drivers start without contacting providers. Construct a fresh notifier only after
checking enabled state and exact endpoint membership. Register only the selected
receiver and never use notify's process-global singleton. Use upstream adapters
where correct, and owned wrappers for allocation/lifecycle/delivery defects.
FCM must use explicit inline credentials and inspect rejected-token responses;
Lark must not create a hidden heartbeat; SMTP uses a cancellable connection.
LINE Notify and the WhatsApp stub appear unavailable with reasons.

A send acquires a cancellable four-slot lease. Its worker retains that lease until
the underlying operation ends, even if the caller's deadline has already expired.
Context-aware adapters stop network work; several legacy SDKs only check context
before I/O and may finish later. Do not claim that a timeout proves non-delivery.
Bound clients where injection is available; adapter-owned HTTP clients do not
follow redirects. Keep upstream errors private because
they can contain credentials, receiver addresses and response bodies; public
errors state the failure category without raw upstream text.

Settings expose the catalog and use existing connection CRUD. Discovery returns
only endpoint IDs and labels. Direct REST/MCP sends share connection operations.
The notification node interpolates subject/body, uses the selected ID literally,
and preserves the message after successful acceptance. The endpoint picker derives
its options from the selected masked connection; stale selections remain visible.

## State and data model

See [data-model.md](data-model.md) and [contracts/notifications.md](contracts/notifications.md).
Existing dependency tracking identifies the node's connection field, so deletion
is blocked for referenced services and exports clear connection IDs as usual.

## Verification

Real HTTP fixtures verify selected-recipient delivery, malformed/rejected requests,
redaction, cancellation and no network activity for unknown endpoints. API fixtures
verify encrypted nested-secret round trips, ID-based restoration and permissions.
Catalog coverage compares all 36 variants with the pinned upstream inventory.
Run server/frontend builds and tests through Makefile, restart and verify the
service. Use a local receiver for MCP sending. Inspect settings and live endpoint
selection/parameter ports in the browser, capture screenshots and check console.

## Risks and open decisions

Importing all providers adds their SDK dependency trees. Some upstream SDKs have
limited cancellation and may occupy execution slots after callers time out.
No live credentials are available for paid/cloud providers, so local checks do not
claim delivery to every external service. No unresolved product decisions remain.
