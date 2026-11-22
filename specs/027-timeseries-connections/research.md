# Research: Time-series connections and history forwarding

## Client libraries

- **Decision**: speak InfluxDB and Prometheus over `net/http`; use `pgx/v5`
  for PostgreSQL.
- **Rationale**: the official InfluxDB clients differ per major version (v1
  client, v2 client with Flux, v3 client with Arrow Flight and a large
  dependency tree). The HTTP APIs are small and stable, and one driver can
  cover all three versions. `pgx` is pure Go and already in the module graph
  through the GORM PostgreSQL driver.
- **Alternatives**: `influxdb-client-go` (v2 only, deprecated for v3);
  `influxdb3-go` (Arrow Flight, gRPC streaming, heavy).

## Prometheus remote write

- **Decision**: remote write 1.0 (`prometheus.WriteRequest`), protobuf encoded
  by hand with `protowire` and compressed with snappy block format from
  `klauspost/compress/snappy`.
- **Rationale**: 1.0 is accepted by Prometheus (with
  `--web.enable-remote-write-receiver`), VictoriaMetrics, Mimir, Cortex, and
  Thanos Receive. The message has three nested types; importing
  `prometheus/prometheus` for `prompb` would add the whole server module.
- **Alternatives**: remote write 2.0 (not yet universally accepted);
  Pushgateway (wrong semantics for state, and no timestamps).

## Staleness

- **Decision**: heartbeat the last value every 60 seconds for Prometheus
  destinations.
- **Rationale**: Prometheus treats a series without a sample in the last 5
  minutes as absent. A change-only writer would make every quiet sensor
  disappear from instant queries and gauges.

## InfluxQL on InfluxDB 2

- **Decision**: use the `/query` v1 compatibility endpoint with the bucket as
  the database.
- **Rationale**: it lets one InfluxQL path serve all three versions, and
  InfluxDB 2.2+ provides virtual DBRP mappings for every bucket.

## Spool format

- **Decision**: one file per batch, JSON lines, named with a sequence number
  and point count.
- **Rationale**: append and drop-oldest are file creations and deletions,
  which need no index and survive crashes. The point count in the name makes
  discard accounting free. JSON keeps the spool inspectable.
- **Alternatives**: a single append log with offsets (needs compaction); the
  application database (forwarding volume would contend with flow state, and
  PostgreSQL or MySQL deployments make it remote).

## Forwarding sources

- **Decision**: in-process watchers (`controls.S.Watch`, `mqttrelay.D.Watch`)
  and the broker's `ha:*` topics.
- **Rationale**: these exist, run independently of open dashboards, and the HA
  broker publication already re-subscribes on reconnect.

## Docker test environment

- **Decision**: `test/tsdb/compose.yaml` with `prom/prometheus` (remote write
  receiver on), `victoriametrics/victoria-metrics`, `influxdb:1.8`,
  `influxdb:2`, `influxdb:3-core`, and `timescale/timescaledb` (PostgreSQL
  16). Ports in the 19xxx range avoid clashing with local
  services.

## OpenTelemetry export

- **Decision**: dropped on 2026-10-30 at the owner's request, after an
  implementation existed. OTLP carries no queries, so it could only ever be a
  write destination, and the databases above already cover the household
  setups this feature targets.
