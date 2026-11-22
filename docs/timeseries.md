# Time-series connections and history

Karakuri keeps no history of its own. It reads and writes the time-series
databases you already run, and reads Home Assistant's recorder. Dashboard
charts that draw these queries are covered in
[Charts](dashboard-authoring.md#charts).

## Connections

Add them under **Settings, Connections**.

| Kind | Reads | Writes | Notes |
| --- | --- | --- | --- |
| `prometheus` | PromQL | Remote write, when **Write enabled** is on | Also Thanos, Mimir, Cortex, and VictoriaMetrics. Prometheus needs `--web.enable-remote-write-receiver`. Extra headers, such as `X-Scope-OrgID`, apply to every request. |
| `influxdb` | InfluxQL on 1, 2, and 3; Flux on 2; SQL on 3 | Line protocol | InfluxQL on 2 uses the bucket's DBRP mapping. VictoriaMetrics, QuestDB, and Telegraf accept its writes as version 1. |
| `postgres` | SQL, in a read-only transaction | Inserts into one table, `karakuri_history` by default | The first write creates the table, as a TimescaleDB hypertable when the extension is installed. |
| `homeassistant` | Recorder history and long-term statistics | None | The existing Home Assistant connection. History covers up to 31 days and 50 entities per query. |
| `energy` | Energy rollups, language `energy` | None | Built in, with no settings. It reads the hourly usage, cost, and solar figures energy tracking records. The [energy guide](energy.md#charts) lists its targets, measures, and periods. |

Every query takes a range (`now-6h`, `now-1d/d`, RFC 3339, or Unix
milliseconds) and a maximum number of data points, which sets `$__interval`.
Query text may use these macros:

| Macro | Languages | Expands to |
| --- | --- | --- |
| `$__from`, `$__to` | All | Range ends in Unix milliseconds |
| `$__interval`, `$__interval_ms` | All | The step: range / max data points, rounded |
| `$__range`, `$__range_s`, `$__range_ms` | All | The range's length |
| `$__rate_interval` | All | `max(interval + scrape, 4 × scrape)`, where the scrape interval is a Prometheus setting, 15s by default |
| `$__timeFilter(col)`, `$__timeGroup(col, 5m)`, `$__timeGroupAlias`, `$__timeFrom()`, `$__timeTo()`, `$__unixEpochFilter(col)` | SQL | PostgreSQL expressions |
| `$timeFilter` | InfluxQL | `time >= …ms and time <= …ms` |
| `v.timeRangeStart`, `v.timeRangeStop`, `v.windowPeriod` | Flux | RFC 3339 times and a duration |

A result is a list of series with typed fields, with times in Unix
milliseconds. Karakuri cuts a result off at 100,000 values and marks it
truncated. A query times out after the connection's timeout, 30 seconds by
default.

## Flow nodes

**Time series query** runs one query and outputs `{series, value, truncated,
interval_ms}`. With a reduction (last, first, min, max, mean, sum, count, or
delta), `value` is the first series reduced to one number. An unreachable
database fails the node retryably. A refused query does not retry.

**Time series write** writes one point (metric, labels, value, and an optional
timestamp) or a payload's `points` array. By default it succeeds once the
points are queued. With **Wait for delivery** it succeeds only when the
database accepts them, and otherwise fails retryably without buffering.

Scripts get both as functions through the script SDK.

## History forwarding

**Settings, History forwarding** records values without a flow. A forward
picks its sources and one or more writable destinations. Sources are:

- specific controls or all controls;
- specific MQTT devices or all devices;
- the entities of one Home Assistant connection that match the include
  patterns and none of the exclude patterns (`*` matches any run of
  characters), with their state and the listed or all numeric attributes.

The control and MQTT device dialogs have a **Record history** shortcut that
saves a forward for that one source.

Points are named so a dashboard query can find them:

| Source | Metric | Labels |
| --- | --- | --- |
| Control | `karakuri_control` | `id`, `name`, `attribute` |
| MQTT device | `karakuri_mqtt_device` | `id`, `name`, `attribute` |
| Home Assistant | `karakuri_ha_entity` | `entity_id`, `domain`, `friendly_name`, `connection`, `attribute` (`state` for the state), `unit` |

A forward may rename the metric and add its own labels. Each attribute of a
composite control or a device message is its own series. Numbers are stored
as values, booleans as 1 and 0, and datetimes as Unix seconds, or as seconds
since midnight for a time-only control. InfluxDB and PostgreSQL store text and
select values as a text `state`. Prometheus stores numbers only, so a select
becomes one series per option labelled `state`, 1 for the current option and 0
for the others, and plain text is skipped. Home Assistant `unavailable` and
`unknown` states are not recorded.

Prometheus forgets a series that has no sample for five minutes, so karakuri
re-sends each series' last value to Prometheus destinations every 60 seconds,
the connection's **Heartbeat**. InfluxDB and PostgreSQL get writes on change
unless a heartbeat is set. A destination that starts running after the
forwards load, or gets a heartbeat later, receives every selected series'
current value within 30 seconds.

## Buffering

Forwards and write nodes share one queue per writable connection, sent in
batches every second. While the database is unreachable, karakuri keeps the
points on disk under `history-spool/<connection>` in the data directory and
retries with backoff from 1 to 60 seconds. Buffered points survive a restart
and are written oldest first. The buffer holds 64 MiB by default. Past that,
karakuri discards the oldest points and counts them.

Karakuri discards a batch the database refuses as malformed and counts it as
rejected. A refusal caused by the destination's state stays buffered and
retries: an HTTP 401, 403, or 404 on a write, from a revoked token, a deleted
bucket or database, or a disabled remote-write receiver, or a PostgreSQL role
that lost its INSERT grant. A disabled connection, or one with writing off,
receives nothing and buffers nothing. Deleting a connection deletes its
buffer.

Delivery status shows buffered points and bytes, written, discarded, rejected,
and skipped counts, and the last error and success. It appears on the
connection, on the forwarding page, from MCP `get_history_status`, and in the
`karakuri_history_*` server metrics. Delivery is at least once. PostgreSQL
ignores duplicate rows, and InfluxDB and Prometheus overwrite identical
samples.

## REST and MCP

| Purpose | REST | MCP |
| --- | --- | --- |
| Query | `POST /api/connections/{id}/timeseries/query` | `query_timeseries` |
| Metadata | `GET /api/connections/{id}/timeseries/metadata?kind=` | `list_timeseries_metadata` |
| Forwards | `GET/POST /api/history/forwards`, `PUT/DELETE /api/history/forwards/{id}` | `list_history_forwards`, `save_history_forward`, `delete_history_forward` |
| Writer status | `GET /api/history/status` | `get_history_status` |
| Chart data | `POST /api/dashboards/{id}/widgets/{widgetId}/chart` | `get_chart_data` |

Queries and lookups need connection read. Forwards need connection write.
