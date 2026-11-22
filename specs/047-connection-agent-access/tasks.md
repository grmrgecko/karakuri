# Tasks: Connection and agent access

**Feature**: `specs/047-connection-agent-access`
**Input**: [spec.md](spec.md), [plan.md](plan.md)

- [x] T001 Add connection and agent owners, share tables, manage permissions, and `FlowVersion.RunAsID`; register the tables for migration and backup and clean them up on user, group, and resource deletion.
- [x] T002 Add `pkg/access` with ranks, authorization, ID sets, node type filtering, event limits, and run-user checks.
- [x] T003 Give every run a run user, record the deployer on deploy and keep it on faceplate apply, and check connection fields, resolved connections, and agent node types and triggers at run time.
- [x] T004 Refuse deploys naming unusable connections or agents, and faceplate values naming unusable connections.
- [x] T005 Check REST connection, device, discovery, time-series, notification, history, energy, memory, chart, and agent routes, and filter their lists.
- [x] T006 Check MCP tools through the connection argument middleware and per-tool checks, and filter MCP lists, references, and dependencies.
- [x] T007 Refuse dashboard deploys and chart or variable query saves naming unusable connections.
- [x] T008 Limit event log queries, live events, and WebSocket topics to usable resources, and filter the home attention list and editor chat models.
- [x] T009 Add the plugin protocol's `CheckConnectionAccess` and use it in the calendar plugin.
- [x] T010 Add share routes, the share page, and access-aware connection and agent settings.
- [x] T011 Add tests, update specs 004, 007, 009, 043, and the guides, build, test, and check through MCP and the browser.
