# Tasks: Event log

**Feature**: `specs/037-event-log`
**Input**: [spec.md](spec.md), [plan.md](plan.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/interfaces.md](contracts/interfaces.md),
[quickstart.md](quickstart.md).

Live checks publish test devices only under the `karakuri-test` base topic on
the lab broker, never under `zigbee2mqtt/`, and name test objects `Event test`.

## Phase 1: Shared preparation

- [X] T001 `pkg/models/event.go`: the `Event` model exactly as [data-model.md](data-model.md) lists it (`category` one of `device`, `connection`, `flow`, `agent`, `security`, `system`, `custom`; `level` one of `info`, `warning`, `error`; `message` at most 500 characters; `details` JSON at most 8 KiB; `count` at least 1; indexes `(category, id)`, `(subject_kind, subject_id, id)`, `(level, id)`, `(actor_kind, actor_id, id)`, `(at)`), registered in `models.All()`
- [X] T002 [P] Permissions `api:events:read`, `api:events:audit`, `api:events:write` in `pkg/permissions`; `pkg/migrate` seeds `api:events:read` on the User role
- [X] T003 [P] `pkg/events/actor.go`: `Actor` (kind `user`, `apikey`, `flow`, `agent`, `system`, `anonymous`; ID; name; `via` of `dashboard`, `api`, `mcp`, `cli`, `flow`; dashboard ID; run ID; address), `WithActor`, `ActorFrom` (system actor when absent), `System()`

## Phase 2: Foundation, recorder and queries

Every user story phase depends on this phase.

- [X] T004 `pkg/events/recorder.go`: `Record(Event)` non-blocking into a 4,096 channel (drop, count, and log at error at most once a minute with the count when full); one writer goroutine batching every 250 ms or 100 entries; folding by category, subject kind and ID, level, and message within 60 s into `count` and `last_at`; truncation of message and details to their limits; after each write publish `event` or `event_update` on `events` or `events:security` and on `events:subject:{kind}:{id}`, and notify watchers; `Start`, `Stop` flushing what is queued; `Watch(fn)`
- [X] T005 `pkg/events/query.go`: filters by categories, levels, subjects, actor, text (`LIKE` on message and subject name, newest 10,000 matches), time range; keyset pagination by `before` ID, limit 1 to 500; visibility excluding `security` without audit in SQL, refusing an explicit `category=security` without it, and a dashboard-scope mode taking an allow function for subjects and never returning security
- [X] T006 `pkg/events/settings.go` and `retention.go`: the `events` setting (retention per category 1 to 3650 days, defaults 30 and 365 for security; `max_rows` 10,000 to 10,000,000, default 1,000,000; `excluded_controls`; `ha_entities` at most 500); sweep at start and every 6 hours deleting by age per category, then the oldest non-security rows in batches of 10,000 until under the limit
- [X] T007 Tests on a real database: folding 100 repeats into one row with count 100 and one publish plus updates; a full channel dropping without blocking; pagination stable while new rows arrive; security hidden without audit and refused when named; retention by age and by row limit keeping security rows; the SC-004 timing on a million synthetic rows for newest 100 by category and by subject

## Phase 3: User story 1, who changed it (P1)

**Goal**: control changes and device commands name their actor.
**Independent check**: quickstart scenario 1.

- [X] T008 [US1] `pkg/middleware`: `UserContext` gains `APIKeyID` and `APIKeyName` from `tryAPIKeyAuth`; an `Actor` middleware after `Auth` and `OptionalAuth` builds the actor from the user or key, `RequestMeta`, and the dashboard context (`via` `dashboard` when a dashboard claim is present, `api` otherwise); the MCP handler sets `via` `mcp`
- [X] T009 [US1] `pkg/engine`: every node's context carries `Actor{kind flow, flow ID and name, run ID, via flow}`
- [X] T010 [US1] `pkg/controls`: `Writer.Actor` copied into `Change.Actor`; the REST handler, MCP tool, and control nodes fill it from the context; update spec 004 (FR and plan) to say control writes record the actor
- [X] T011 [US1] `pkg/events/sources.go`: record control changes from `controls.S.Watch` (skipping `excluded_controls`; message `<name> set to <value>`, or `<name> <attribute> set to <value>`; details old and new), device availability from `mqttrelay.D.WatchAvailability`, and listed Home Assistant entities from their broker topics, re-subscribing when the setting changes; `mqttrelay.Devices.Command` records `<device>: <field> set to <value>` with the context's actor; start and stop in `cmd/karakuri/serve.go`
- [X] T012 [US1] Tests: a control written through REST with a session, with an API key, under a dashboard claim, and from a node records user, key, dashboard, and flow with run; a device command from REST and from a node likewise; an excluded control records nothing; availability changes record warning and info

## Phase 4: User story 2, security (P1)

**Goal**: sign-ins and administrative changes are recorded without secrets.
**Independent check**: quickstart scenario 2.

- [X] T013 [US2] Record in `pkg/api/auth_handler.go` and `pkg/auth`: sign-in (`<user> signed in`), failed sign-in (`Failed sign-in for <username>`, warning, username as given, never the password), sign-out, session revoked, password changed; each with address and client
- [X] T014 [US2] Record user, group, membership, role, role permission, role assignment, and API key create, update, and delete in their REST handlers and MCP tools, naming the actor and the changed field names
- [X] T015 [US2] Record connection create, update (names of changed fields, marking sealed fields as `credentials changed`), and delete in `pkg/connops`; backup download in `backup.Service.LogDownload`; `config rotate-key` writes its entry directly to the database; `backup restore` writes the `backup.restored` setting, and serve records it as the first security entry on start and clears it
- [X] T016 [US2] Tests: each security action records an entry; no recorded entry across the suite contains a test password, token, or key value (a scan of every row); a read-only user sees none through the query layer

## Phase 5: User story 3, failures and system (P2)

**Goal**: failures and lifecycle in one place.
**Independent check**: quickstart scenario 3.

- [X] T017 [US3] Sources: connection status from `connection:*:status` (error on error, info on connected, warning on reconnecting after an error; transitions only); failed runs from `flow:*:runs` with the failing node and error; deploy, enable, and disable in `pkg/flowops`; agent enroll, connect, disconnect, and revoke in `pkg/agenthub`; backup results from `backup.S.Watch`; server start and stop in serve
- [X] T018 [US3] Tests: a connection flapping 100 times in a minute folds to a handful of rows; a failed run records the node and links the run; a deploy and an enable record the actor

## Phase 6: User story 4, page, widget, and flows (P2)

**Goal**: the log where people look and where flows react.
**Independent check**: quickstart scenario 4.

- [X] T019 [US4] `pkg/api/event_handler.go` (`GET /api/events`, `GET|PUT /api/events/settings`) and `pkg/mcpserver/tools_events.go` (`list_events`, `get_event_settings`) per the contract; WebSocket permission callbacks for `events` (read), `events:security` (audit), `events:subject:*` (read, or dashboard scope); `dashscope.AllowsTopic` maps `events:subject:control|device|ha_entity:*` to the control, device, and entity capabilities; the list route under dashboard scope requires subjects the scope allows
- [X] T020 [US4] `pkg/nodes/core/events.go`: `log-event` (message, level, subject, details; actor from the context) and `event-logged` (categories, levels, subjects, match; skips its own flow's entries; refuses to arm on security without the owner's audit permission)
- [X] T021 [US4] `frontend/src/routes/Activity.svelte` with the navigation entry for `api:events:read`: filters, newest-first list with actor and subject links, folded counts, load more by ID, live entries on the newest page, and the settings tab for `api:events:write`
- [X] T022 [US4] `plugins/core/widgets/activity`: rows of control, device, or entity bindings, categories, levels, limit 1 to 50, show actor; live through `events:subject:*`
- [X] T023 [US4] Tests: route and MCP permissions including dashboard scope refusing other subjects and security; node tests for Log Event attribution and Event Logged filtering, own-flow skip, and security refusal

## Final verification and documentation

- [X] T024 Write `docs/events.md` (what is recorded, actors, folding, retention, permissions, the page, the widget, the nodes, REST and MCP), serve it as a guide and MCP resource, and update `docs/flow-authoring.md`, `docs/dashboard-authoring.md`, and `docs/mcp.md`; set the spec status to Implemented
- [X] T025 Run a full `make dev` and `make test`; restart `karakuri-dev.service`; confirm the unit is active and http://localhost:8080 answers; browser check of the Activity page and the widget with settled screenshots and a console check
- [X] T026 Live quickstart; remove the test control, flows, connection, device, user, API key, and dashboard, clear the `karakuri-test` topics, and report what was created and removed

Live checks on 2026-11-05 against the dev server: a control set over MCP was
recorded as the `mcp (admin)` API key through MCP, and the Activity page added
the next change live; a failed sign-in was a warning with the address and
client and no password, the following sign-in an info entry; a Log Event flow
run named the flow and run; an MQTT connection to a closed port was recorded
once with its error, since the driver repeats the same error report rather than
alternating with connecting, so repeats are dropped as unchanged rather than
counted; a sensor under `karakuri-test` on the lab broker took ten readings and
added no entries (SC-005); on start the log recorded the server, every
connection, and every agent; the activity widget showed the control's entries,
updated live, and its config panel listed the bound control. The million-row
timing (SC-004) ran in the unit tests. The test control, dashboard, flow,
connection, and device were removed; nothing was retained on the broker.

## Dependencies and execution order

- T001 precedes everything; T002 and T003 are independent.
- Phase 2 precedes the stories. US1 precedes US4's widget (it needs subject
  entries); US2 and US3 are independent of US1 once the recorder exists.
- Commit each verified phase on its own; run a full `make dev` before
  committing widget code.

## Parallel opportunities

- T002 and T003; T013 and T014 beside T017; T021 beside T022.

## Implementation strategy

The recorder with control and device actors (US1) is the first deliverable,
then security (US2). Failures, the page, the widget, and the nodes build on the
same entries.
