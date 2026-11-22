# Research: Home and overview

## Start page resolved in the browser

- **Decision:** the browser resolves `/` from preferences and the dashboard
  list.
- **Rationale:** the start page only chooses among routes the browser already
  serves, and the dashboard list is already access-filtered by the server.
  No new authorization surface is created.
- **Alternatives:** a server redirect at `/`. Rejected: `/` is the SPA entry,
  and the server does not see the session cookie decision the client makes.

## Attention computed on the server

- **Decision:** a `homeops` service shared by REST and MCP.
- **Rationale:** assistants need the same answer to "is anything broken?".
  Per-source permission gating and access-filtered flow failures belong next
  to the other service-layer rules (principle II).
- **Alternatives:** compose the page from existing endpoints in the browser.
  Rejected: five round trips, and no shared rule for MCP.

## Flapping from the event log

- **Decision:** sum the folded `count` of connection error events in the
  last hour.
- **Rationale:** the connection event source already records every failed
  attempt and folds repeats. The manager keeps only the current state.
- **Alternatives:** a new transition counter in `connmgr`. Rejected as
  duplicate state that would reset on restart.

## Next firing through an optional node interface

- **Decision:** `engine.NextFirer`, implemented by the schedule node.
- **Rationale:** `homeops` must not know the schedule node's config. Plugin
  or calendar triggers can implement the interface later without changing
  `homeops`.
- **Alternatives:** parse schedule configs in `homeops`. Rejected: it
  duplicates cron and sun logic and drifts from the node.

## Device availability left out

- **Decision:** not an attention source.
- **Rationale:** battery Zigbee and ESPHome devices sleep and report offline
  routinely. They would fill the list and hide real failures. The event log
  still records them, so they reach recent problems.
