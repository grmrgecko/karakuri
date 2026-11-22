# Validation: Configured notification services

Run `make dev`, `make test TARGET=server`, and `make test TARGET=frontend`.
Restart `karakuri-dev.service`, verify active state and localhost:8080.

1. Start a disposable local HTTP receiver and configure its URL as one named
   endpoint of an HTTP notification service under Settings → Notifications.
2. Read/update the service without retyping targets; verify the receiver still
   gets the exact subject/body while reads and history contain no target secret.
3. Create an undeployed Value/Notification/Debug flow; send through MCP and the
   node, then try an unknown ID and a URL in place of an ID. Count receiver calls.
4. Reorder/remove endpoint rows and verify ID-based preservation and revocation.
5. Inspect all service choices, credential fields, target help and unavailable
   reasons. Switch selected services in a node and inspect endpoint options live.
6. Exercise notification parameter ports, capture settled screenshots and inspect
   browser console. Remove disposable settings/flows when functional checks end,
   or report any remaining development records.

## Evidence recorded on 2026-10-06

- `make dev` built server, frontend, agent, automation and plugins. Server and
  frontend `make test` targets passed. Go tests include the real REST permission
  chain, encrypted nested-secret edits, endpoint reordering/removal, real engine
  interpolation/envelope/retry behavior, HTTP cancellation and SMTP wire/MIME
  delivery. The pinned inventory fixture covers all 36 variants.
- MCP discovery returned 36 variants, including the two unavailable variants.
  Six requests reached a disposable loopback receiver across direct sends,
  single-node/full-flow runs, wired subject/body inputs and a masked settings edit.
  Raw URL/unknown ID attempts and the routed error run added no requests.
  Duplicate-ID saves were rejected. The error Debug node executed normally.
- Browser checks covered the complete settings dropdown, FCM credential help,
  WhatsApp's disabled save, the notification canvas, subject/message parameter
  handles and endpoint choices. Switching A to B changed the picker to B's
  endpoint while preserving A's ID as unavailable. Screenshots were captured
  after interactions. Console inspection found only an earlier 404 for a
  different flow's run endpoint, with no notification-related console error.
  The shared preview intermittently failed automation calls; the successful
  snapshots and DOM checks above were repeated after reconnecting.
- The notification verification flow and both temporary connections were deleted.
  The loopback receiver stopped. No notification test records remain. Existing
  development flows and connections were preserved.
- External-provider credentials were not available. Compilation and adapter
  inspection cover the full catalog; local checks do not claim live delivery
  to every cloud or paid provider.
