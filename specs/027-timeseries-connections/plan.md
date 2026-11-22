# Design plan: Time-series connections and history forwarding

**Feature**: `specs/027-timeseries-connections` | **Date**: 2026-10-30 | **Spec**: [spec.md](spec.md)

## Summary

Four new connection kinds speak to the databases. A query service in
`pkg/tsdb` gives every readable source, including Home Assistant, one
request and result shape. Flows, MCP, REST, and the chart widget of feature
028 call that service. Writable kinds expose a sink. One writer per writable
connection batches points, spools them to disk while the destination is
failing, and drains the spool in order. A forwarder turns control, MQTT device,
and Home Assistant changes into points for the forwards that select them. It
also re-sends a series' last value at each heartbeat for destinations that
need one.

[research.md](research.md) records the protocol and library decisions,
[data-model.md](data-model.md) the persistent and wire shapes,
[contracts/](contracts/) the REST, MCP, and node interfaces, and
[quickstart.md](quickstart.md) the checks.

## Technical context

- Language and dependencies: Go 1.26 server, pure Go (`CGO_ENABLED=0`).
  Existing: `pgx/v5` (promoted to a direct dependency), `protobuf`, and
  `prometheus/client_golang`. Added: `klauspost/compress` (already in
  `go.sum`) for snappy.
  InfluxDB and Prometheus are spoken over `net/http` with no client library.
  The remote write message is encoded with `protowire`.
- Components:
  - `pkg/tsdb`: ranges, macros, intervals, escaping, the query and result
    types, reducers, the `Querier`, `Metadata`, and `Sink` interfaces, and
    error classes.
  - `pkg/integrations/prometheus`, `influxdb`, `postgresql`: kinds and
    drivers.
  - `pkg/integrations/homeassistant`: statistics, statistic IDs, and
    attribute history.
  - `pkg/tswriter`: the writer and its disk spool.
  - `pkg/historyfwd`: the forwarder.
  - `pkg/tsdbops`: the service layer shared by REST, MCP, and nodes, including
    the Home Assistant querier.
  - `pkg/nodes/core/timeseries.go`: the query and write nodes.
  - `pkg/api/timeseries_handler.go` and `pkg/mcpserver/tools_timeseries.go`.
  - Frontend: the History forwarding settings page, and the history sections
    of the control and MQTT device editors.
  - `test/tsdb/`: a Docker Compose file and tagged integration tests.
- Storage: table `history_forwards` (AutoMigrate). Spool files under
  `<data>/history-spool/<connection-id>/`. Writer status and heartbeat state are
  in memory.
- Limits, from the spec: 100,000 values per result; at most 11,000 points per
  Prometheus series; 10-year ranges; Home Assistant history of 1 to 50
  entities and at most 31 days; a 30-second default query timeout; a 64 MiB
  default spool; backoff from 1 to 60 seconds; a 60-second default heartbeat.
  The writer flushes every second or at 1,000 points, and its memory queue
  holds 10,000 points.

## Constitution check

- **Failures**:
  - The query node fails retryably for unavailable sources and timeouts, and
    non-retryably for rejected queries, through `nodeapi.ExecutionError`.
  - A write node that queues points can be retried without side effects
    beyond duplicate points, which the destinations collapse. A write node that
    waits for delivery fails retryably and spools nothing, so a retry policy
    does not double-buffer.
  - Writers never block their sources. A full memory queue moves points to the
    spool, and a full spool discards the oldest points and counts them.
  - Cancellation reaches HTTP, gRPC, and pgx calls through the context.
- **Drafts and deployment**: nodes follow the existing lifecycle. Forwards are
  live settings like connections, with no draft. REST, MCP, and the UI call the
  same `tsdbops` functions.
- **Permissions**:
  - Queries and lookups need `api:connections:read`, and forwards need
    `api:connections:write`. No new permission is introduced.
  - PostgreSQL queries run in read-only transactions.
  - Secrets are masked by the existing connection machinery and never placed
    in errors. Drivers strip credentials from URLs in error messages.
  - Dashboard access to queries is feature 028's concern.
- **Existing state**:
  - The new table is created by AutoMigrate. Existing connections, controls,
    and devices are unchanged; the editors read forwards from the new table.
  - No database, identity, or key is invalidated.
  - Spool directories are new.
- **Evidence**:
  - `make dev TARGET=server`, `make test TARGET=server`, and the new
    `make test TARGET=tsdb`, which runs the tagged suite against
    `test/tsdb/compose.yaml`.
  - Live checks through MCP against the containers and the development Home
    Assistant.
  - Browser checks of the forwarding page and the editor sections.

## Design and contracts

### Query service

`tsdb.Query{RefID, Language, Text, HA *HAQuery, Range, MaxDataPoints,
MinInterval, Format, LegendFormat}`.

`tsdbops.Query(ctx, connID, q)` works in these steps:

1. Load the connection and resolve its driver, or the Home Assistant querier
   when the connection's kind is `homeassistant`.
2. Parse the range with `tsdb.ParseRange(from, to, now)`, then compute
   `Interval`.
3. Expand macros for the language (`tsdb.ExpandMacros`).
4. Call `Querier.Query` under the connection's timeout.
5. Apply `tsdb.Limit` to the result.

Results are `tsdb.Result{Series []Series, Truncated, ExecutedQuery,
IntervalMs}`. Each `Series` holds `{Name, Labels, Unit, Fields []Field{Name,
Type, Labels, Values}}`, with time values in Unix milliseconds. Every
`Querier` returns `*tsdb.Error` with a class:

| Class | Raised for | Node | REST |
| --- | --- | --- | --- |
| `unavailable` | Dial errors, 5xx, 429, timeouts | Retryable | 502 |
| `rejected` | Query parse or 4xx | Not retryable | 422 |
| `unsupported` | A language the version lacks, or writing to a connection with writing off | Not retryable | 422 |

Interval derivation uses a rounding table: the raw interval is
`range / maxDataPoints`, raised to `minInterval`. It is rounded to the nearest
of 1ms, 10ms, 20ms, 50ms, 100ms, 200ms, 500ms, 1s, 2s, 5s, 10s, 15s, 20s, 30s,
1m, 2m, 5m, 10m, 15m, 20m, 30m, 1h, 2h, 3h, 6h, 12h, 1d, 7d, and 30d.
`$__rate_interval` is `max(interval + scrape, 4 * scrape)`. `scrape` is the
connection's scrape interval setting, 15 seconds by default.

### Prometheus

| Operation | Endpoint and behavior |
| --- | --- |
| Range query | `GET /api/v1/query_range` with `step` = interval, clamped so range/step ≤ 11,000 |
| Instant query | `/api/v1/query` |
| Metadata | `/api/v1/label/__name__/values`, `/api/v1/labels`, and `/api/v1/label/{name}/values?match[]=` |
| Legend format | `{{label}}` templates |
| Write | Snappy-compressed remote write 1.0 `WriteRequest` to `write_url`, default `<url>/api/v1/write` |
| Test | `GET /api/v1/status/buildinfo`, falling back to `/api/v1/query?query=1` for servers without it; with writing on, an empty remote write |

### InfluxDB

Version-specific endpoints:

| Operation | v1 | v2 | v3 |
| --- | --- | --- | --- |
| InfluxQL query | `/query?db=&epoch=ms` | `/query` (DBRP compatibility) | `/api/v3/query_influxql?format=json` |
| Flux query | — | `/api/v2/query`, annotated CSV parsed | — |
| SQL query | — | — | `/api/v3/query_sql?format=json` |
| Write | `/write` | `/api/v2/write?org&bucket` | `/api/v3/write_lp?db=` |
| Auth | user and password | `Authorization: Token` | `Authorization: Bearer` |

- Writes use millisecond precision.
- Line protocol escapes commas, spaces, and equals signs in tags, quotes in
  string fields, and replaces invalid measurement characters.
- Metadata uses `SHOW MEASUREMENTS`, `SHOW TAG KEYS`, `SHOW TAG VALUES WITH
  KEY`, and `SHOW FIELD KEYS` in InfluxQL; Flux `schema.*` on v2; and
  `information_schema` on v3 SQL.

### PostgreSQL

- `pgxpool` holds at most 4 connections.
- A query runs `BeginTx(ReadOnly)`, then `SET LOCAL statement_timeout`, then
  the query, then rollback.
- A time column (`time` or the first `timestamptz` column) together with
  numeric columns produces time series. A `metric` column splits the series.
  Anything else returns a table.
- Writes use `INSERT ... ON CONFLICT DO NOTHING` into the configured table,
  default `karakuri_history`:

  ```text
  (time timestamptz, metric text, labels jsonb, labels_hash text,
   value double precision, state text, unique(time, metric, labels_hash))
  ```

  - `labels_hash` is the SHA-1 of the sorted labels, so the uniqueness rule
    works where `jsonb` equality indexes would not.
  - The table is created on the first write, with `create_hypertable` when
    `pg_extension` lists `timescaledb`.
- Metadata queries `information_schema`.

### Home Assistant

- History queries `history/history_during_period` without `no_attributes`
  when an attribute is requested; otherwise behavior is unchanged.
- Statistics use `recorder/statistics_during_period` with `types`, `period`,
  and `units`. Statistic IDs come from `recorder/list_statistic_ids`.
- Numeric and boolean-like states (on/off and the like) become numbers.
  Anything else stays a string field, so state timelines can draw it.

### Writer (`pkg/tswriter`)

`tswriter.For(connID)` returns the connection's writer, creating it lazily.
`Enqueue(points)` never blocks: points go to a buffered channel of 10,000, and
overflow is written straight to the spool.

The loop runs in these steps:

1. Batch points every second or at 1,000.
2. If the spool is empty, resolve the sink through the connection manager and
   write the batch.
   - On a retryable failure, append the batch to the spool and enter backoff.
   - On a rejection, count the batch and drop it.
3. If the spool is not empty, append to it, then drain the oldest segment
   first under backoff (1s, doubling to 60s, reset on success).

Spool and lifecycle:

- Segments are files named `<seq>-<count>.seg`, one JSON point per line. The
  spool total is the sum of file sizes. When it exceeds the cap, the oldest
  segments are deleted and their counts added to `discarded`.
- A disabled connection drops points and counts them as skipped. A connection
  that is enabled but not running counts as unavailable.
- `Deliver(ctx, points)` writes directly for wait-for-delivery nodes.
- `Close` spools whatever is pending.
- `Remove(connID)` stops the writer and deletes its directory; `connops.Delete`
  calls it.
- `Status()` reports `{state, last_error, last_success, buffered_points,
  buffered_bytes, written, discarded, rejected, skipped}`.
- Metrics:
  - `karakuri_history_points_total{connection,result}`
  - `karakuri_history_buffered_points{connection}`
  - `karakuri_history_buffered_bytes{connection}`

### Forwarder (`pkg/historyfwd`)

The forwarder subscribes to three sources:

- `controls.S.Watch`
- `mqttrelay.D.Watch`
- `mq.B.Subscribe("ha:*")`, filtered to `ha_state` messages, where a payload
  carries the connection ID taken from the topic.

Handlers only push to a buffered channel of 4,096 events, and one goroutine
does the work. A full channel drops the event and counts it in
`karakuri_messages_dropped_total{kind="history"}`.

Reload compiles forwards into indexes: by control ID, all controls, by device
ID, all devices, and per Home Assistant connection with compiled glob
patterns. `tsdbops` save and delete call `Reload`.

An event becomes points:

- **Controls** use the kind catalog's attributes from `pkg/controls`: a scalar
  kind's primary attribute is `value`, and composite kinds record each
  attribute in the value object.
- **Devices** record each key of `Values`, which is the patch the message
  carried.
- **Home Assistant** records the state as attribute `state`, plus the listed or
  all numeric attributes.
- `unavailable` and `unknown` stop the series.
- Values are converted per destination class (numeric only, or text capable)
  following FR-017. A select becomes a state-set for numeric destinations. A
  Home Assistant entity with an `options` attribute is treated as a select.
- Within one event, points are deduplicated by destination and series key.

A one-second ticker does two things:

- It emits coalesced values for forwards with a minimum interval, keeping the
  latest pending value per series.
- It re-emits the last point of each series whose destination heartbeat is
  due. Heartbeat state is keyed by destination and series. It is cleared when
  a series stops and pruned on reload, or when its control or device no
  longer exists or its entity is absent from `StatesCached`.

Lifecycle: the forwarder starts after `connops.SetManager` and stops before
`connMgr.Stop`, then `tswriter.CloseAll` spools what is pending.

### Nodes

| Node | Inputs | Outputs |
| --- | --- | --- |
| `timeseries-query` | Connection (kinds `prometheus,influxdb,postgres,homeassistant`), language, query text or the Home Assistant fields, from, to, maximum data points, reduction | `out` with `{series, value, truncated, interval_ms}`, plus the error port |
| `timeseries-write` | Connection (writable kinds), metric, labels map, value, timestamp, wait for delivery, or `points` from the payload | Pass-through `out`, plus the error port |

`{{ }}` expressions follow the existing config expression rules. Both nodes
become script functions through feature 026's catalog without extra work.

### REST and MCP

See [contracts/api.md](contracts/api.md). The MCP tools are
`query_timeseries`, `list_timeseries_metadata`, `list_history_forwards`,
`save_history_forward`, `delete_history_forward`, and `get_history_status`.
The read-only ones join the assistant allowlist.

### Frontend

- **History forwarding page** (`/settings/history`): lists forwards with their
  source summary, destinations, and writer status, and has an editor dialog.
  It needs connection write to edit, and connection read to view.
- **Control and MQTT device dialogs**: a "Record history" checkbox with a
  destination multi-select. It loads and saves the source-owned forward
  through `GET /history/forwards?source=control:<id>`, and deletes it when the
  checkbox is cleared.

## State and data model

See [data-model.md](data-model.md).

## Verification

| Spec item | Check |
| --- | --- |
| Ranges, macros, intervals, escaping, reducers, limits | Go table tests in `pkg/tsdb` |
| Line protocol, remote write encoding, Flux CSV decoding | Go tests with byte fixtures |
| Spool order, cap, restart, rejection | `pkg/tswriter` tests with a fake sink and a temporary directory |
| Forward point conversion | `pkg/historyfwd` tests using the controls kind catalog and the `hatest` fake |
| US1, US2, US4, SC-001, SC-002, SC-005 | `test/tsdb` tagged suite against Prometheus, VictoriaMetrics, InfluxDB 1, 2, and 3, and TimescaleDB |
| US3, SC-003 | Live check on the development server: stop the InfluxDB container, change a control 20 times, restart `karakuri-dev.service`, start the container, then query |
| US6, SC-006 | MCP calls against the running server; grep responses for secrets |
| Home Assistant history and statistics | MCP query against the development Home Assistant |
| UI | Browser: forwarding page and the two editor sections, with screenshots and a console check |

## Risks and open decisions

- Prometheus marks a remote-written series stale only through the heartbeat
  stopping. Its five-minute lookback means a series stays visible for up to
  five minutes after it stops.
- InfluxDB 2's InfluxQL compatibility needs a DBRP mapping. Buckets created
  through the UI get a virtual mapping, but buckets created by API in older
  2.x releases may not. The test reports the error when the mapping is
  missing.
- At-least-once delivery can duplicate points after a crash between write and
  spool deletion. The destinations collapse duplicates as the spec states.
