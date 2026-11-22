# Quickstart: Time-series connections and history forwarding

## Prerequisites

- Docker with Compose. Start the databases with `make tsdb-up`; stop them
  with `make tsdb-down`.
- The development server built with `make dev` and restarted with
  `systemctl --user restart karakuri-dev.service`.
- An MCP token from `build/`: `./karakuri token list admin --name=mcp --raw`.

## Automated

1. `make test TARGET=server` covers ranges, macros, encoders, the writer spool,
   and forward conversion.
2. `make test TARGET=tsdb` starts nothing itself; with the containers up it
   seeds each database, queries it, writes through every writable kind, and
   reads the points back.

## Live checks through MCP

1. Create one connection of each kind against the containers (addresses in
   `test/tsdb/README.md`), and run `test_connection` on each.
2. `query_timeseries` with `ha_history` for two sensors over 6 hours and with
   `ha_statistics` hourly means over 2 days against the development Home
   Assistant. Expect points matching Home Assistant's history view.
3. Create a number control and `save_history_forward` it to InfluxDB 2 and
   Prometheus. Set the control twice; `query_timeseries` both and expect two
   points (InfluxDB) and the latest value (Prometheus instant query).
4. Outage: `docker compose stop influxdb2`, set the control 20 times, restart
   `karakuri-dev.service`, `get_history_status` shows 20 buffered points,
   `docker compose start influxdb2`, and within a minute the 20 points are
   queryable in order and the buffer is empty.
5. Leave the control unchanged for 6 minutes and run a Prometheus instant
   query; the series is present (SC-005).
6. Grep all responses from steps 1 to 4 for the configured passwords and
   tokens; none appear.

## Browser

Open Settings → History forwarding and the control and MQTT device dialogs;
save a forward from each, check the status column, take screenshots, and
check the console. Delete test controls, forwards, and connections afterward
or report them.
