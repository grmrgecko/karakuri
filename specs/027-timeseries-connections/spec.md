# Feature specification: Time-series connections and history forwarding

**Feature**: `specs/027-timeseries-connections`
**Created**: 2026-10-30
**Status**: Implemented
**Input**: Use existing time-series databases for history instead of building
one. Add Prometheus-compatible (Prometheus, VictoriaMetrics, Thanos, Mimir),
InfluxDB 1, 2, and 3, and PostgreSQL/TimescaleDB connections, and read Home Assistant's recorder history and statistics. Query
them from flows and MCP, write points from flows, and forward control, MQTT
device, and Home Assistant entity values to the connections an author chooses,
buffering while a destination is down. Dashboards graph the results through
[feature 028](../028-dashboard-charts/spec.md). OpenTelemetry (OTLP) export
was specified and then dropped at the owner's request on 2026-10-30; its user
story 5 and FR-005 are retired and their numbers are not reused.

## User scenarios and testing

### User story 1: Query history from a flow (Priority: P1)

An author adds a Time Series Query node that asks InfluxDB for the average
living room temperature over the last hour and turns the heating on when it is
below 19 °C. The same node asks Prometheus with PromQL, PostgreSQL with SQL, or
Home Assistant for an entity's recorded history or long-term statistics.

**Why this priority**: History-based decisions ("average over 10 minutes",
"rising faster than X") are the reason to have history at all. Flows only see
current values today.
**Independent test**: Seed each test database with a known series, run a
query node against each over a fixed range, and compare the returned points
and the reduced value with the seeded data.

**Acceptance scenarios**:

1. **Given** an InfluxDB connection and an InfluxQL query over the last hour,
   **When** the node runs with reduction `mean`, **Then** it outputs the series
   it read and the mean of its values.
2. **Given** a PromQL range query with a 30-minute range and 100 maximum data
   points, **When** the node runs, **Then** the database is asked for a step
   that yields at most 100 points per series, and the node outputs one series
   per label set.
3. **Given** a Home Assistant connection, a history query for two entities, and
   a statistics query for one statistic with a one-hour period, **When** each
   runs, **Then** the node outputs the recorded states and the hourly
   statistics Home Assistant returns.
4. **Given** an unreachable database, **When** the node runs, **Then** it fails
   retryably and the flow's retry policy and failure routing apply.
5. **Given** a query the database rejects as malformed, **When** the node runs,
   **Then** it fails without retrying and the failure carries the database's
   message.
6. **Given** a PostgreSQL query containing an `INSERT`, **When** the query node
   runs it, **Then** the database refuses it because query runs are read-only.

### User story 2: Record device values without a flow (Priority: P1)

An operator turns on history for the thermostat control and the garage door
MQTT device, and forwards every `sensor.*_temperature` entity from Home
Assistant. Each forward names one or more destinations, such as InfluxDB and
Prometheus together.

**Why this priority**: Most history is "keep what this device reported". A flow
per device would be repetitive and easy to break.
**Independent test**: Forward a number control and a light MQTT device to
InfluxDB and Prometheus, change each value, and read the points back from both
databases with the documented names and labels.

**Acceptance scenarios**:

1. **Given** a forward of a number control to InfluxDB, **When** the control
   changes from 20 to 21, **Then** InfluxDB receives a point of 21 at the time
   of the change, in measurement `karakuri_control` with the control's ID and
   name as tags.
2. **Given** a forwarded light device, **When** its brightness and color change,
   **Then** every attribute of the light is recorded as its own series, labelled
   with the attribute name.
3. **Given** a forward of `sensor.*_temperature` with an exclude of
   `sensor.attic_*`, **When** any matching entity changes, **Then** it is
   recorded unless it matches the exclusion, and a new matching entity is
   recorded without editing the forward.
4. **Given** a forward to Prometheus, **When** a value stays unchanged for ten
   minutes, **Then** it is re-sent at the heartbeat interval so Prometheus does
   not mark the series stale.
5. **Given** a select control forwarded to Prometheus, **When** it changes from
   `heat` to `cool`, **Then** the `state="cool"` series becomes 1 and the
   `state="heat"` series becomes 0. InfluxDB and PostgreSQL record the text
   `cool` instead.
6. **Given** a Home Assistant entity whose state becomes `unavailable`, **When**
   it is forwarded, **Then** no point is written and its heartbeat stops until
   it reports a value again.

### User story 3: Keep history through an outage (Priority: P1)

The InfluxDB server reboots for an update. Readings taken while it is down
appear in it afterwards, in order, including across a Karakuri restart during
the outage.

**Why this priority**: History has gaps exactly when something goes wrong,
which is when it is wanted. This is a reliability engine.
**Independent test**: Stop a test database, change a forwarded control 20
times, restart the Karakuri server, start the database, and confirm all 20
points arrive in order with their original timestamps.

**Acceptance scenarios**:

1. **Given** a destination that refuses connections, **When** forwarded values
   change, **Then** the points are kept on disk and the destination's status
   reports the error, the number of points waiting, and the time of the last
   successful write.
2. **Given** buffered points and a restart, **When** the server starts and the
   destination becomes reachable, **Then** the buffered points are written
   before newer ones, with their original timestamps.
3. **Given** a buffer at its size limit, **When** more points arrive, **Then**
   the oldest points are discarded, the discard is counted and logged, and newer
   points are kept.
4. **Given** a destination that rejects a batch as malformed, **When** the
   rejection arrives, **Then** that batch is discarded and counted as rejected,
   and later batches still flow.
5. **Given** a destination that refuses a write because of its own state (a
   revoked token, a deleted bucket, a disabled receiver, a lost INSERT grant),
   **When** the refusal arrives, **Then** the batch stays buffered and retries.
6. **Given** a disabled destination, **When** forwarded values change, **Then**
   nothing is buffered for it, and enabling it records new changes only.

### User story 4: Write computed values from a flow (Priority: P2)

A flow computes daily energy cost and writes it with a Time Series Write node,
choosing the metric name and labels.

**Why this priority**: Derived values have no device to forward them from.
**Independent test**: Write a point with two labels through each writable kind
and read it back.

**Acceptance scenarios**:

1. **Given** a write node with metric `energy_cost`, label `meter=main`, and
   value 4.2, **When** it runs, **Then** the point is queued for the destination
   and the node succeeds.
2. **Given** the node set to wait for delivery and an unreachable destination,
   **When** it runs, **Then** the node fails retryably and nothing is left in the
   buffer from that attempt.
3. **Given** a message carrying an array of points, **When** the node runs,
   **Then** every point is written in one batch.

### User story 6: Inspect and query from MCP (Priority: P2)

An assistant answers "how warm was the bedroom last night?" through MCP, and
an operator checks whether forwarding is healthy without opening the UI.

**Why this priority**: MCP is the project's functional check surface, and
assistants already use it to inspect the house.
**Independent test**: Run a query through MCP against each readable kind, list
metric and label names, and read the forwarding status during a simulated
outage.

**Acceptance scenarios**:

1. **Given** an MCP caller with connection read, **When** it runs a query,
   **Then** it receives the same series a flow node would.
2. **Given** a Prometheus connection, **When** a caller lists label values for
   `entity_id`, **Then** it receives the values Prometheus reports.
3. **Given** a destination with buffered points, **When** a caller reads
   forwarding status, **Then** it sees the destination's error, buffered point
   count, buffered bytes, written, discarded, and rejected totals.

### Edge cases

- A range whose start is after its end is refused before any query runs. A
  range wider than 10 years is refused.
- A query result holding more than 100,000 values in total is cut off at that
  count and marked truncated, so a mistaken query cannot exhaust the server.
- A Prometheus step never goes below 1 second or above the range, and never
  yields more than 11,000 points per series.
- A Home Assistant history query names 1 to 50 explicit entities and covers at
  most 31 days. Statistics have no range limit because Home Assistant
  aggregates them.
- A forwarded source that is deleted stops being recorded. Its points already
  written stay in the database.
- A forward naming a deleted destination skips that destination and reports it
  in the forward's status.
- Two forwards that select the same source and the same destination write each
  change once.
- Changes faster than a forward's minimum interval are coalesced to the latest
  value per series within that interval.
- A label value or metric name the database cannot store as given is escaped
  or replaced with `_` so the point is never rejected for its name alone.
- Text values are not sent to Prometheus, which stores only numbers.
  Datetime values are sent as Unix seconds. Booleans are 1 and 0.
- Duplicate writes after a crash are possible. PostgreSQL ignores a point
  identical in time, metric, and labels to one already stored; InfluxDB and
  Prometheus overwrite identical samples.
- A query that takes longer than the connection's timeout, 30 seconds by
  default, is cancelled and reported as a timeout.
- Deleting a connection deletes its buffer.

## Requirements

### Functional requirements

- **FR-001**: The system MUST offer connection kinds `prometheus`, `influxdb`,
  and `postgres` through the existing connection lifecycle of
  [feature 004](../004-connections-devices/spec.md), with schema-driven
  settings, encrypted and masked secrets, validation, and a test action that
  checks reachability and credentials.
- **FR-002**: A `prometheus` connection MUST query any server implementing the
  Prometheus HTTP query API with no authentication, basic authentication, or a
  bearer token and extra headers, such as Mimir's tenant header. When writing is
  enabled it MUST send points by Prometheus remote write to a configurable
  address.
- **FR-003**: An `influxdb` connection MUST support InfluxDB 1, 2, and 3 by a
  version setting. It MUST query with InfluxQL on every version, Flux on
  version 2, and SQL on version 3, and MUST write line protocol on every
  version.
- **FR-004**: A `postgres` connection MUST query with SQL inside a read-only
  transaction bounded by the connection's timeout. When writing is enabled it
  MUST create its table if missing, with columns for time, metric, labels,
  numeric value, and text state, a uniqueness rule over time, metric, and
  labels, and a TimescaleDB hypertable when that extension is installed.
- **FR-006**: Home Assistant connections MUST be queryable for entity history,
  optionally of one attribute, and for long-term statistics with a period of
  5 minutes, hour, day, week, or month and the statistic types mean, min, max,
  sum, state, and change. They are read sources only.
- **FR-007**: A query MUST name a connection, its query text or Home Assistant
  selection, a time range, and a maximum number of data points. Ranges MUST
  accept relative expressions such as `now-6h`, `now-1d/d`, and `now`, and
  absolute RFC 3339 times or Unix milliseconds.
- **FR-008**: Query text MUST support the macros `$__from`, `$__to`,
  `$__interval`, `$__interval_ms`, `$__range`, `$__range_s`, and
  `$__rate_interval` on every language, `$__timeFilter(column)` and
  `$__timeGroup(column, interval)` in SQL, `$timeFilter` in InfluxQL, and
  `v.timeRangeStart`, `v.timeRangeStop`, and `v.windowPeriod` in Flux.
  `$__interval` MUST be derived from the range and the maximum data points.
- **FR-009**: Every query MUST return the same result shape: a list of series,
  each with a name, labels, a unit when the source reports one, and fields of
  type time, number, string, or boolean. SQL and InfluxQL results without a
  time column MUST return as a table. Results MUST be bounded as the edge cases
  state.
- **FR-010**: Readable kinds MUST offer metadata lookups for editors and
  variables: metric names, label names, and label values for Prometheus;
  databases or buckets, measurements, tag keys, tag values, and field keys for
  InfluxDB; schemas, tables, and columns for PostgreSQL; and entity and
  statistic IDs for Home Assistant.
- **FR-011**: A Time Series Query node MUST run one query and output its series
  and, when a reduction is chosen, a single value reduced by last, first, min,
  max, mean, sum, count, or delta. Unreachable sources and timeouts MUST fail
  retryably; refused or malformed queries MUST fail without retry.
- **FR-012**: A Time Series Write node MUST write one point or an array of
  points, each with a metric name, labels, a value, and an optional timestamp,
  to a writable connection. By default it MUST succeed once the points are
  queued. With wait for delivery it MUST succeed only after the destination
  accepts them, and MUST fail retryably without leaving them buffered.
- **FR-013**: A history forward MUST select its sources, choose one or more
  writable destinations, and optionally override the metric name, add labels,
  and set a minimum interval. Sources MUST be specific controls or all
  controls, specific MQTT devices or all devices, or the entities of one Home
  Assistant connection matching include patterns and not matching exclude
  patterns, with `*` matching any run of characters.
- **FR-014**: The control and MQTT device editors MUST offer recording history
  for that source to chosen destinations, stored as a forward for that one
  source.
- **FR-015**: A forwarded control or MQTT device MUST record every attribute of
  its kind. A forwarded Home Assistant entity MUST record its state, and the
  attributes the forward lists or all numeric attributes when it says so.
  States `unavailable` and `unknown` MUST NOT be recorded.
- **FR-016**: Forwarded points MUST be named as follows. The metric or
  measurement is `karakuri_control`, `karakuri_mqtt_device`, or
  `karakuri_ha_entity` unless the forward overrides it. Labels are `id` and
  `name` for controls and devices; `entity_id`, `domain`, `friendly_name`, and
  `connection` for Home Assistant; `attribute` for the recorded attribute;
  `unit` when known; and the forward's own labels. Numbers and booleans are
  numeric values; text, select, and datetime values follow the type rules in
  the edge cases and FR-017.
- **FR-017**: For numeric-only destinations a select value MUST be written as
  one series per option, labelled `state`, holding 1 for the current option and
  0 for the others. Text values MUST be skipped. For InfluxDB and PostgreSQL a
  select or text value MUST be written as a text state.
- **FR-018**: Prometheus destinations MUST re-send the last value of
  every forwarded series at a heartbeat interval, 60 seconds by default, while
  its source keeps reporting a recordable value. InfluxDB and PostgreSQL MUST
  write on change, with an optional heartbeat.
- **FR-019**: Each writable connection MUST have one writer shared by forwards
  and write nodes. It MUST batch points, buffer them on disk when a write fails
  for a retryable reason, retry with backoff from 1 second up to 60 seconds,
  write buffered points before newer ones, keep its buffer across restarts, and
  hold at most the configured buffer size, 64 MiB by default, discarding the
  oldest points first.
- **FR-020**: A writer MUST discard and count a batch the destination rejects as
  malformed and continue with later batches. A refusal caused by the
  destination's state (HTTP 401, 403, or 404 on a write, or a lost PostgreSQL
  INSERT grant) is retryable and MUST NOT discard the batch. Pending points MUST
  be moved to the buffer on shutdown.
- **FR-021**: Each writable connection MUST report its writer state with its
  connection status: last error, last successful write, points and bytes
  buffered, and totals of points written, discarded, and rejected. The totals
  MUST also be exported as server metrics.
- **FR-022**: REST and MCP MUST offer queries, metadata lookups, forward
  listing, saving, and deletion, and writer status, sharing the service-layer
  rules of the UI. Queries and lookups MUST require connection read; forwards
  MUST require connection management.
- **FR-023**: The settings UI MUST list forwards with their sources,
  destinations, and status, and edit them.

### Key entities

- **Time-series connection**: a connection of kind `prometheus`, `influxdb`,
  or `postgres`, with endpoint, credentials, query timeout, write
  settings, heartbeat, and buffer size.
- **Query**: connection, language-specific text or Home Assistant selection,
  time range, maximum data points. Not stored by this feature; nodes and
  widgets store their own.
- **Series result**: name, labels, unit, typed fields, truncated flag.
- **History forward**: name, enabled flag, source selection, destinations,
  metric override, extra labels, attribute selection, minimum interval.
  Persistent.
- **Writer buffer**: per writable connection, on disk, ordered batches of
  points awaiting delivery. Deleted with its connection.
- **Writer status**: per writable connection, in memory, reset on restart
  except for the buffered count it reads from disk.

## Success criteria

- **SC-001**: A query node returns the seeded series from each readable kind and
  from Home Assistant, checked against test databases and the development Home
  Assistant.
- **SC-002**: A forwarded number control and light device appear in each
  writable kind under the documented names, labels, and types after one change.
- **SC-003**: 20 changes made while a destination is down, with a server restart
  in between, all arrive in order with their original timestamps once it is
  back.
- **SC-004**: A buffer limited to a small size keeps the newest points and
  reports a nonzero discarded count after an overflow.
- **SC-005**: A Prometheus series forwarded once and left unchanged is still
  returned by an instant query 6 minutes later.
- **SC-006**: No connection secret appears in any query, metadata, status, or
  forward response.

## Assumptions and scope

- Karakuri stores no history of its own; retention and downsampling belong to
  the databases.
- Writes are delivered at least once while the buffer survives. Exactly-once
  delivery is not promised.
- Prometheus receives writes only by remote write, which the operator enables
  on the server (`--web.enable-remote-write-receiver`), or by servers that
  accept it by default, such as VictoriaMetrics and Mimir. A scrape endpoint
  for device values is a separate concern and not part of this feature. Thanos
  Query is read-only.
- VictoriaMetrics, QuestDB, and Telegraf accept InfluxDB line protocol, so an
  `influxdb` connection can write to them; VictoriaMetrics is queried through a
  `prometheus` connection.
- OpenTelemetry (OTLP) export is out of scope.
- Home Assistant history depends on its recorder's retention; long-term
  statistics exist only for entities with a state class.
- The existing History Graph widget of
  [feature 024](../024-home-media-widgets/spec.md) is unchanged.
- Docker containers for each database are the test
  environment; the development Home Assistant is the live Home Assistant check.

## References

- [Dashboard charts](../028-dashboard-charts/spec.md)
- [Connections, MQTT devices, and controls](../004-connections-devices/spec.md)
- [Flow execution](../003-flow-execution/spec.md)
- [MCP authoring](../012-mcp-editor-assistant/spec.md)
- [Script node SDK](../026-script-node-sdk/spec.md)
