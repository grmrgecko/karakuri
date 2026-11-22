# Data model: Energy tracking

## `energy_sources`

| Column | Type | Rule |
| --- | --- | --- |
| `id` | uuid | primary key |
| `name` | text | required, unique among live sources |
| `source_kind` | text | `control`, `device`, or `ha` |
| `source_id` | text | control or device UUID, or Home Assistant connection UUID |
| `entity_id` | text | Home Assistant entity, for `ha` |
| `attribute` | text | attribute to read; empty is the primary value or the HA state |
| `measurement` | text | `energy` (cumulative) or `power` |
| `unit` | text | `Wh`, `kWh`, `MWh` for energy; `W`, `kW` for power |
| `role` | text | `consumer`, `house`, `import`, `export`, `production`; at most one live `house` |
| `gap_limit_s` | int | default 900 for power, 21600 for energy; 60 to 604800 |
| `max_kw` | float | plausible maximum, default 25 |
| `last_value` | float | last accepted reading, nullable |
| `last_at` | time | when it was taken |
| `state` | text | `ok`, `gap`, `reset`, `rejected`, `unavailable`, `waiting` |
| `state_note` | text | last rejected reading or reason |
| `deleted_at` | time | soft delete; rows purged 30 days later |
| `created_at`, `updated_at` | time | |

## `energy_groups`, `energy_group_members`

Groups: `id`, `name` (unique), `sort_order`. Members: `id`, `group_id`,
`source_id`, `joined_at`, and `left_at` (null while a member). Hourly rows
carry no group; a group's hour sums the sources that were members at that
hour, so moving a source changes only hours after the move (spec edge case).

## `energy_tariffs`

| Column | Type | Rule |
| --- | --- | --- |
| `id` | uuid | |
| `name` | text | |
| `currency` | text | ISO 4217 code |
| `effective_from` | date | local date; unique |
| `import` | json | `{"flat": 0.15}` or `{"periods": [{"name","days":[1..7],"start":"16:00","end":"21:00","rate"}]}`, or `{"control_id": "<uuid>"}` |
| `export` | json | same shape, optional |
| `daily_charge` | float | optional |

Periods cover all 168 hours of the week without overlap; `end` after `start`
or `00:00` meaning midnight.

## `energy_hours`

| Column | Type | Rule |
| --- | --- | --- |
| `source_id` | uuid | |
| `hour` | time | UTC start of the hour; primary key with `source_id` |
| `local_date` | date | local calendar date of the hour |
| `kwh` | float | |
| `quality` | text | `live`, `partial`, `filled`, `carried` |

## `energy_rates`

| Column | Type | Rule |
| --- | --- | --- |
| `hour` | time | UTC start; primary key |
| `local_date` | date | |
| `tariff_id` | uuid | nullable when no tariff covers the hour |
| `currency` | text | |
| `import_rate` | float | per kWh |
| `export_rate` | float | per kWh, nullable |
| `period` | text | period name, empty for flat |

## Settings key `energy`

`{"timezone": "America/Chicago", "week_start": 1}`.
