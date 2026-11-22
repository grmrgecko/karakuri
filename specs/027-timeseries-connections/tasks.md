# Tasks: Time-series connections and history forwarding

**Feature**: `specs/027-timeseries-connections`
**Input**: [spec.md](spec.md), [plan.md](plan.md), [data-model.md](data-model.md), [contracts/api.md](contracts/api.md), [quickstart.md](quickstart.md).

## Phase 1: Shared preparation

- [x] T001 Add `test/tsdb/compose.yaml`, `test/tsdb/README.md`, and Makefile targets `tsdb-up`, `tsdb-down`, `tsdb-seed`, and `test-tsdb` (`make test TARGET=tsdb`)
- [x] T002 Add `pkg/tsdb`: ranges, intervals, macros, escaping, query and result types, limits, reducers, interfaces, and error classes, with table tests
- [x] T003 Promote `pgx/v5` and `klauspost/compress` in `go.mod`

## Phase 2: User story 1, queries

- [x] T004 [P] [US1] Prometheus kind and driver in `pkg/integrations/prometheus` (query, metadata, remote write sink), with encoder tests
- [x] T005 [P] [US1] InfluxDB kind and driver in `pkg/integrations/influxdb` (v1, v2, v3 queries, Flux CSV, metadata, line protocol sink), with codec tests
- [x] T006 [P] [US1] PostgreSQL kind and driver in `pkg/integrations/postgresql` (read-only queries, metadata, table creation, insert sink)
- T007 Retired: OTLP export was dropped (spec 027 FR-005 retired)
- [x] T008 [US1] Home Assistant statistics, statistic IDs, and attribute history in `pkg/integrations/homeassistant/media.go`, with `hatest` fakes
- [x] T009 [US1] `pkg/tsdbops`: resolve queriers including the Home Assistant adapter; query, metadata, limits, and timeouts
- [x] T010 [US1] Register the kinds in `pkg/nodes/core/plugin.go`; add the `timeseries-query` node in `pkg/nodes/core/timeseries.go`

## Phase 3: User stories 2 and 3, forwarding and buffering

- [x] T011 [US3] `pkg/tswriter`: batching, spool, backoff, cap, rejection, status, metrics, and `Deliver`, with tests
- [x] T012 [US2] `models.HistoryForward` and AutoMigrate; `tsdbops` forward CRUD with validation, deletion with an owning control or device, and writer removal on connection delete
- [x] T013 [US2] `pkg/historyfwd`: indexes, conversion, state-sets, coalescing, heartbeats, and pruning, with tests; wire into `cmd/karakuri/serve.go`

## Phase 4: User stories 4 and 6, write node, REST, and MCP

- [x] T014 [US4] `timeseries-write` node in `pkg/nodes/core/timeseries.go`
- [x] T015 [US6] REST routes in `pkg/api/timeseries_handler.go`, and writer status on connection items
- [x] T016 [US6] MCP tools in `pkg/mcpserver/tools_timeseries.go` and the assistant allowlist

## Phase 5: UI

- [x] T017 [US2] History forwarding settings page, API client and store, and navigation in `frontend/src/routes/settings/`
- [x] T018 [US2] Record history sections in `Controls.svelte` and `MQTTDevices.svelte`

## Final verification and documentation

- [x] T019 Tagged integration suite in `test/tsdb` against every container
- [x] T020 Live checks from [quickstart.md](quickstart.md), including the outage and restart, the heartbeat, and the Home Assistant queries
- [x] T021 Add `docs/timeseries.md`, link it from `docs/`, and update `specs/spec-kit.md` and `specs/architecture.md`
- [x] T022 Run `make dev` and `make test`, restart `karakuri-dev.service`, and verify port 8080

## Dependencies and execution order

T002 precedes every driver. T004 to T007 are independent of each other.
T009 needs T004 to T008. T011 precedes T013 and T014. The UI follows the REST
routes.
