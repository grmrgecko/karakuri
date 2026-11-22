# Tasks: Calendar connections and event triggers

**Feature**: `specs/030-calendar-connections`
**Input**: [spec.md](spec.md), [plan.md](plan.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/interfaces.md](contracts/interfaces.md),
[quickstart.md](quickstart.md).

## Phase 1: Shared preparation

- [X] T001 Add a `make proto` target to `Makefile` that regenerates `pkg/plugin/proto/plugin.pb.go` and `plugin_grpc.pb.go` from `plugin.proto` with `protoc`, `protoc-gen-go`, and `protoc-gen-go-grpc`, and run it once to confirm the generated files are unchanged
- [X] T002 [P] Add `github.com/emersion/go-webdav` to `go.mod`

## Phase 2: Foundation, host additions

Every user story phase depends on this phase.

- [X] T003 Add optional `Probe func(ctx, id, cfg) (detail string, err error)` to `integrations.ConnectionKind` in `pkg/integrations/integrations.go`; `connops.Test` in `pkg/connops/connops.go` calls it when set and returns `TestResult.Detail`; show the detail under the test result in the connection settings form (`frontend/src/routes/settings/Connections.svelte` or its form component)
- [X] T004 Add `TriggerRecorder` (`Skipped(msg *Msg, reason string)`) to `pkg/nodeapi`, re-export it from `pkg/engine/nodeapi.go`, and implement it on `triggerCtx` in `pkg/engine/runner.go` by recording a run that settles at once as `canceled` with the reason as its error and the message payload as its trigger payload
- [X] T005 Extend `pkg/plugin/proto/plugin.proto`: `detail` on `ErrorResponse`, host RPCs `ConnectionAccessToken` and `TriggerSkipped` with the messages in contracts/interfaces.md; regenerate with `make proto`
- [X] T006 Implement the host side in `pkg/pluginhost`: `ConnectionAccessToken` resolves the connection, refuses one that is missing, disabled, or whose driver has no `AccessToken(ctx)`, and logs the request at debug; `TriggerSkipped` calls the trigger context's `Skipped`; `TestKindConnection` responses carry `detail` into the proxied kind's `Probe`
- [X] T007 Implement the plugin side in `pkg/plugin/sdk`: a kind's `Probe` answers `TestKindConnection` with its detail; `Host.ConnectionAccessToken(ctx, id)`; the remote trigger context implements `TriggerRecorder` through `TriggerSkipped`
- [X] T008 Tests: connops probe detail; `triggerCtx.Skipped` writes a canceled run; the token callback refuses a non-oauth2 connection and returns a token for a fake oauth2 driver; extend `pkg/pluginhost/external_test.go` and its example plugin to round-trip probe detail and `Skipped`

## Phase 3: User story 1, calendar connections (P1)

**Goal**: ICS, CalDAV, and Google calendars are connections with encrypted, masked credentials and a test that reports what it found.
**Independent check**: quickstart scenario 1.

- [X] T009 [US1] Restructure `plugins/calendar` occurrences in `ics.go` and `models.go`: stable occurrence IDs (`<uid>@<original start>`), RECURRENCE-ID exceptions, cancelled occurrences dropped, connection time zone for floating and all-day times, events without an end, the `window_days` (default 60) and `max_occurrences` (default 5,000) bounds, and the `calendar` name on each occurrence
- [X] T010 [US1] Add `plugins/calendar/kinds.go` and `drivers.go`: kinds `calendar-ics` (`url`, `auth` none/basic/bearer, `username`, `password`, `token`, `timezone`), `calendar-caldav` (`url`, `username`, `password`, `calendars`, `timezone`), `calendar-google` (`calendars`, `auth` api_key/oauth2, `api_key`, `oauth2_connection` as a connection field of kind `oauth2`, `timezone`), with secrets `password`, `token`, `api_key`; `webcal://` read as `https://`; a plugin-side driver map by connection ID; fetch with a short cache and a 24-hour last-good fallback; a probe reporting calendar names and the next seven days' event count
- [X] T011 [P] [US1] CalDAV in `plugins/calendar/caldav.go` with `go-webdav/caldav`: principal and calendar-home discovery, calendar listing, a direct calendar URL when listing is unsupported, and time-range queries parsed through the shared ICS path
- [X] T012 [P] [US1] Google in `plugins/calendar/google.go`: several calendar IDs, API-key requests, and bearer requests with a token from `Host.ConnectionAccessToken`; a signed-out oauth2 connection surfaces as the driver's error
- [X] T013 [US1] Register the kinds in the plugin's `Register` in `plugins/calendar/main.go` and remove the old provider, URL, and API-key fields shared by the nodes
- [X] T014 [US1] Tests in `plugins/calendar/*_test.go`: occurrence IDs and exceptions on a recurring fixture, bounds, time zones, ICS auth modes against `httptest`, a recorded CalDAV exchange, recorded Google responses for both auth modes, probe detail, and stale fallback; plus a REST/MCP masking test for one calendar connection in `pkg/api` or `pkg/mcpserver`

## Phase 4: User story 2, start and end triggers (P1)

**Goal**: the trigger fires at start, end, or both with offsets, once per occurrence, across restarts, with missed moments delivered or dropped.
**Independent check**: quickstart scenarios 2–4.

- [X] T015 [US2] Rewrite `calendar-event` in `plugins/calendar/nodes.go` with the fields in data-model.md (`connection`, `calendars`, `match`, `all_day`, `edges`, `start_offset`, `end_offset`, `interval_s`, `missed_deadline`, `missed_grace`), a one-minute tick, moments per occurrence and edge, the output in contracts/interfaces.md, and `meta.stale_since` when firing from a stale list
- [X] T016 [US2] Durable fired records and catch-up in `plugins/calendar/nodes.go`: `fired:<occurrence>:<edge>` and `seen_until` in the durable store, the record written before emitting, seven-day catch-up from `seen_until` applying `missed_deadline` and `missed_grace` (late emits carry `meta.late_ms`; drops call `Skipped` with `missed <edge> of "<title>" at <moment> by <lateness>; dropped`), seven-day pruning, and test runs that fire a sample event without records
- [X] T017 [US2] Trigger tests with a fake clock in `plugins/calendar/nodes_test.go`: each edge mode with offsets; filters including all-day include/exclude/only; a weekly event with one cancelled occurrence over three weeks (SC-003); a moved and a deleted event; a second trigger instance on the same durable store does not re-fire; catch-up with deliver, drop beyond grace, and within grace; a moment before the first arm never fires
- [X] T018 [US2] Live: quickstart scenarios 2 and 3 against a local ICS feed (SC-001, SC-002)

## Phase 5: User story 3, events now (P1)

**Goal**: a lookup returns events in progress and upcoming events.
**Independent check**: quickstart scenario 5.

- [X] T019 [US3] Rewrite `calendar-events` in `plugins/calendar/nodes.go`: `connection`, the trigger's filters, `window_days`, `limit`; output `{in_progress, upcoming}`; an unreadable calendar fails through `error`, which makes the node retryable
- [X] T020 [US3] Lookup tests for start/end boundaries, empty results, and the error path; live quickstart scenario 5

## Phase 6: User story 4, upgrade and widget (P2)

**Goal**: existing inline settings move into connections, and the widget chooses connections.
**Independent check**: quickstart scenario 6.

- [X] T021 [US4] Startup migration in `pkg/migrate/calendar.go`, called from `AutoMigrate`: scan flow versions (deployed and draft) for `calendar-event` and `calendar-events` nodes with `url`, and `settings` keys `plugins.calendar.*.sources` with sources holding `url`; group by provider, URL, and credential; create encrypted connections named `Calendar: <name or host>` with a numeric suffix when taken; rewrite configs to `connection` (mapping `lead_time_m` to `edges: start` with negative `start_offset`); one transaction per item; log and skip items that fail; skip items already migrated
- [X] T022 [US4] Migration tests in `pkg/migrate/calendar_test.go`: drafts and deployed versions, widget sources keeping name, colour, and tag, two sources sharing one connection, a failing item left untouched, and a second run that changes nothing (SC-004)
- [X] T023 [US4] Plugin HTTP in `plugins/calendar/handler.go`: `GET /connections` (dashboard write; `id`, `name`, `kind` only), sources stored as `{connection_id, name, color, tag, calendars}` with unknown connection IDs refused, and event reads through the driver map
- [X] T024 [US4] Widget panel `plugins/calendar/widgets/calendar/src/CalendarConfig.svelte`: choose a connection per source, keep name, colour, tag, and an optional calendar list; remove URL and API-key fields. `CalendarWidget.svelte` reads events as before
- [X] T025 [US4] Live: quickstart scenario 6 on the development dashboard's calendar widget; browser check of the widget panel and the connection test detail with settled screenshots and a console check

## Phase 7: User story 5, house-mode pattern (P2)

**Goal**: the documented calendar override works as written.
**Independent check**: quickstart scenario 7.

- [X] T026 [US5] Document the calendar trigger, the lookup, and the house-mode override (including overlapping overrides, restoring the mode saved before the first one) in `docs/flow-authoring.md`
- [X] T027 [US5] Build the documented flow live against a test feed with two overlapping events and confirm the mode control changes and returns (quickstart scenario 7)

## Final verification and documentation

- [X] T028 Update `docs/plugins.md` (probe, trigger recorder, token callback), `docs/mcp.md` (test detail), `specs/architecture.md` if state lifetimes change, and set the spec status to Implemented
- [X] T029 Run `make dev`, `make test TARGET=server`, and `make test TARGET=frontend`; remove `030 …` test objects and any test containers; restart `karakuri-dev.service` and confirm it is active and http://localhost:8080 answers

## Dependencies and execution order

- Phase 1 precedes Phase 2; T002 is independent of T001.
- Phase 2 blocks the stories: T005 precedes T006 and T007; T008 follows them.
- US1 (Phase 3) is the MVP and precedes US2, US3, and US4, which all read
  through its drivers. T011 and T012 touch separate files and can proceed in
  parallel after T010.
- US2 and US3 are independent of each other after US1.
- US4 needs US1's kinds; the migration (T021, T022) is independent of the
  widget (T023, T024).
- US5 needs US2.
- Commit each verified phase on its own.
