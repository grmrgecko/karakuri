# Contract: Energy interfaces

## REST

Reading needs `api:connections:read`; changing needs `api:connections:write`.

- `GET /api/energy/sources`, `POST /api/energy/sources`,
  `PUT /api/energy/sources/{id}`, `DELETE /api/energy/sources/{id}`.
  A source view adds `state`, `state_note`, `last_value`, `last_at`, and
  `today_kwh`.
- `GET /api/energy/candidates`: devices, controls, and Home Assistant entities
  whose unit or device class says energy or power, each with the source it
  would make.
- `POST /api/energy/sources/{id}/import {days}`: imports up to 400 days from
  the source's history; answers 202 and reports progress on the source.
- `GET|POST /api/energy/groups`, `PUT|DELETE /api/energy/groups/{id}`, with
  `source_ids`.
- `GET|POST /api/energy/tariffs`, `PUT|DELETE /api/energy/tariffs/{id}`.
  Saving answers with the hours recomputed. Refusals name the uncovered or
  overlapping times, such as `Saturday 00:00 to 07:00 has no rate`.
- `GET|PUT /api/energy/settings`: `timezone`, `week_start`.
- `GET /api/energy/rollups?target=&measures=&from=&to=&period=`: rows of
  `{start, end, values: {measure: number}, complete}`. `from` and `to` take the
  time-series range forms (`now/d`, `now-30d`, RFC 3339).
- `GET /api/energy/price?at=` and
  `GET /api/energy/price/cheapest?hours=&from=&to=`.
- `POST /api/energy/preset`: creates the preset dashboard; needs dashboard
  create. Answers the dashboard.

## MCP

`list_energy_sources`, `save_energy_source`, `delete_energy_source`,
`list_energy_candidates`, `import_energy_history`, `list_energy_groups`,
`save_energy_group`, `delete_energy_group`, `list_energy_tariffs`,
`save_energy_tariff`, `delete_energy_tariff`, `get_energy_rollups`,
`get_energy_price`, `find_cheapest_energy_window`, `create_energy_dashboard`.

## Query language `energy`

On connection ID `energy`, for charts, `POST
/api/connections/energy/timeseries/query`, and `query_timeseries`:

```json
{ "language": "energy",
  "energy": { "targets": ["house", "group:<id>", "source:<id>", "untracked", "solar"],
              "measure": "usage", "period": "day" } }
```

`measure` is one of `usage`, `cost`, `total_cost`, `export_credit`, `import`,
`export`, `production`, `self_consumption`, `solar_share`, `daily_charge`. `period` is
`hour`, `day`, `week`, `month`, or `auto` (from the chart's interval). Time
series format answers one series per target with fields time and value, unit
`kwatth`, the currency code, or `percent`. Table format answers one row per
target with its total over the range. Metadata lookups list targets.

## Nodes (folder Energy)

`energy-usage`: `target`, `span` (`today`, `yesterday`, `this_week`,
`this_month`, `last_hours`, `range`), `hours`, `from`, `to`,
`compare_usual`. Output:

```json
{ "target": "house", "kwh": 14.2, "cost": 3.41, "currency": "USD",
  "power_w": 820, "usual_kwh": 11.0, "ratio": 1.29, "complete": true,
  "from": "...", "to": "..." }
```

`energy-price`: `mode` `now`, `at`, or `cheapest`; `at`; `hours`, `from`,
`to` for cheapest. Output `{rate, currency, period, ends_at}` or `{start, end,
average_rate, currency}`.

`energy-price-period` (trigger): no fields beyond the missed-deadline fields
and `test_params`. Payload `{period, rate, currency, starts_at, ends_at,
previous_period}`.

## Preset dashboard

Name `Energy`; chart widgets on connection `energy`: three stat panels (today,
this week, this month, each usage and total cost), a 30-day bar chart of daily
house usage and cost, a pie chart of groups with untracked over this month, a
table of sources over this month, and, with solar sources, a time series of
production, import, export, and self-consumption by hour for the last 7 days.
