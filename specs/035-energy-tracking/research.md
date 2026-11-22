# Research: Energy tracking

## Where usage is kept

- **Decision**: karakuri records hourly usage per source in its own database.
- **Rationale**: the spec requires energy to work without a time-series
  connection, and a cost recompute must touch karakuri-held data. An hour per
  source is small: 200 sources for a year is 1.75 million rows of a few
  numbers, which the SQLite and PostgreSQL stores handle with an index on
  (source, hour).
- **Alternatives**: computing on demand from forwarded history in a
  time-series database. Rejected: it fails without a connection, and every
  dashboard load would re-run the reset and gap rules over raw points.

## Prices separate from usage

- **Decision**: one `energy_rates` row per local hour for the house (import and
  export rate, tariff, currency); cost is computed at read time as usage times
  rate.
- **Rationale**: a tariff applies to the whole house (spec assumption), so the
  rate is per hour, not per source. A recompute after a tariff edit rewrites
  at most 8,784 rows a year regardless of source count, which is what makes
  SC-003 (a year of 20 sources in 10 seconds) easy. The rate used stays
  recorded with each hour, which FR-010 needs for rate controls.
- **Alternatives**: storing cost on each source-hour. Rejected: a recompute
  would touch every source's rows.

## Meter rules

- **Decision**: cumulative deltas with noise below max(1%, 0.01 kWh) ignored,
  larger drops treated as resets counting the new value, jumps above the
  plausible maximum (default 25 kW over the elapsed time) rejected, spans over
  the gap limit (default 15 minutes for power, 6 hours for cumulative) carried
  to the reading's hour; power integrated by the trapezoid rule between
  samples within the gap limit.
- **Rationale**: Zigbee plugs (Tasmota, Sonoff, Aqara) reset their counters on
  power loss and occasionally report a slightly lower value after a rounding
  change; Home Assistant's `total_increasing` uses the same reset reading. A
  cumulative meter keeps counting while the server is down, so its usage over
  an outage is real and only its placement in time is uncertain, hence carried
  rather than dropped. A power meter's silent stretch is unknown and is not
  invented.
- **Alternatives**: Home Assistant's 10% reset threshold. Rejected for small
  totals, where 10% of a new plug's counter is less than a real hour's use.

## Hour splitting

- **Decision**: a cumulative delta whose readings straddle hours is split in
  proportion to time in each hour.
- **Rationale**: plugs report every 10 to 60 seconds, so the error is
  negligible, and a meter reporting every 5 minutes still lands its usage in
  the right hours.

## Filling and importing

- **Decision**: Home Assistant entities fill from hourly statistics (`change`
  for `total_increasing` energy sensors, `mean` for power). Controls and
  devices fill by replaying the series their history forward wrote
  (`karakuri_control` or `karakuri_mqtt_device` with `id` and `attribute`
  labels), queried at the destination's native resolution through tsdbops,
  through the same meter rules.
- **Rationale**: these are the two histories karakuri knows how to read and
  name. Replaying through the same rules keeps filled hours consistent with
  live ones.
- **Alternatives**: requiring the author to write a query per source.
  Rejected: the forward's naming is fixed by feature 027 FR-016.

## Dashboards through charts

- **Decision**: a query language `energy` on a built-in pseudo-connection
  `energy`, answered by `pkg/energy`, used by the chart widget.
- **Rationale**: charts already draw stat, bar, pie, table, and time series
  panels and already authorize a placed chart's queries for its viewers
  (`pkg/dashchart`), so no new widget or binding is needed and the preset is
  plain charts. tsdbops's `Resolve` already special-cases Home Assistant by
  kind; a fixed ID is the same kind of special case.
- **Alternatives**: a dedicated energy widget with an `energy` binding source
  and capability. Rejected for now: it duplicates chart rendering and needs a
  new scope kind.

## Price period trigger

- **Decision**: the trigger computes the next boundary from the tariff,
  sleeps on the engine clock, and records fired boundaries in the node's
  durable store with a watermark, catching up to a week back on start with the
  missed-deadline fields of feature 029, as `plugins/calendar/nodes.go` does.
- **Rationale**: same restart contract as the calendar trigger, already tested.

## Time zone and week

- **Decision**: a settings key `energy` holds the IANA time zone (default the
  server's local zone) and the first day of the week (default Monday).
- **Rationale**: tariffs follow local wall time, and the server config has no
  time zone setting of its own.
