# Energy

Karakuri turns energy and power readings from plugs, meters, and Home
Assistant sensors into usage per hour, prices each hour under the house's
tariff, and shows the totals on charts and in flows. It keeps its own hourly
figures, so it needs no time-series database. When a source's history is
forwarded to one, or the source is a Home Assistant entity, karakuri fills the
hours the server missed from that history.

Manage energy under **Settings, Energy**, or through the REST routes and MCP
tools listed at the end.

## Sources

A source reads one value:

| Field | Meaning |
| --- | --- |
| Reads | A device (an attribute of its state, `value` by default), a control, or a Home Assistant entity (its state, or an attribute). |
| Measures | **Energy** is a meter that counts up, in Wh, kWh, or MWh. **Power** is W, kW, or MW. Units match without regard to case, except that milliwatt units (`mW`, `mWh`) are refused instead of read as megawatts. |
| Role | **Consumer** (a plug, circuit, or appliance), **Whole house**, **Grid import**, **Grid export**, or **Solar production**. Only one source can be the whole house. |
| Gap limit | The longest silence bridged, 60 seconds to 7 days. 15 minutes for power and 6 hours for energy unless set. |
| Plausible maximum | The most the source could draw, 25 kW unless set. |

**Suggest sources** lists devices, number controls, and Home Assistant sensors
whose unit is an energy or power unit, and Zigbee2MQTT-style devices with
`energy` or `power` in their state.

### How readings become usage

A meter's usage is the difference between readings, spread by time over the
hours between them:

- A drop of less than 1% of the reading, or less than 0.01 kWh, is noise and
  adds nothing.
- A larger drop is a reset, such as a plug losing power. Karakuri counts the
  new reading from zero, and the source's state shows `reset`.
- A rise faster than the plausible maximum is rejected as a glitch, and the
  source shows `rejected`. After three rejected readings in a row, the meter
  starts again from the new value, for a meter that was replaced.
- A silence longer than the gap limit is filled from the source's history when
  that history can be read back. Karakuri fills the silent hours once they
  close, including the hours the silence starts and ends in. Otherwise the
  usage across the silence lands in the hour of the next reading, marked
  carried, since the meter kept counting.

A power sensor's usage is the average of each pair of readings times the time
between them. A silence longer than the gap limit adds nothing. A reading of
`unavailable`, or anything that is not a number, adds nothing and shows
`unavailable`.

Karakuri writes hours and each meter's last reading every 30 seconds, so a
restart in the middle of an hour loses no usage from a meter. It ignores
readings stamped in the future or more than a day old.

### The house

The house total is the whole-house source when there is one. Otherwise it is
grid import plus production minus export, when those exist, and otherwise the
sum of consumers. **Untracked** is the house total minus consumers, never below
zero. **Solar used at home** is production minus export, and the solar share
is that divided by the house total.

## Groups

A group, such as a room or circuit, sums its sources. A change of members
applies from that moment, and past hours keep the grouping they had.

## Tariffs

A tariff has a currency, a start date, and a rate per kWh. The rate is one flat
rate, time-of-use periods, or a number control a flow keeps current. Periods
name ISO weekdays (Monday is 1) and local start and end times, and together
they must cover every hour of the week once. An end of 00:00 means midnight,
and a period crossing midnight is split in two. A tariff can add a fixed charge
per day and a credit per exported kWh. Negative rates are allowed.

Karakuri prices each recorded hour at the rate in force then and stores the
rate with the hour. Usage a meter counted while the server was down is spread
over those hours, and each is priced the same way, except under a rate
control, whose average was not recorded then. Saving, changing, or deleting a
tariff reprices the recorded hours it covers and leaves usage alone.

Under a rate control, each hour is priced at the control's average over the
hour. Future prices are unknown, so the cheapest window cannot be found.

A source's or group's cost is its usage times the rate. The house's cost is
grid import times the rate when there is an import source, and otherwise house
usage times the rate. The house's total cost subtracts the export credit and
adds the daily charge.

## Time

Hours, days, and tariff periods follow the energy time zone, which is the
server's unless set. Weeks start on the configured day. A day with a daylight
saving change has 23 or 25 hours.

## Charts

The chart widget reads energy from the built-in **Energy** connection with the
`energy` language:

```json
{"refId":"A","connectionId":"energy","language":"energy",
 "energy":{"targets":["house","group:<id>","source:<id>","untracked","solar"],
           "measure":"usage","period":"day"},"format":"time_series"}
```

`measure` is `usage`, `cost`, `total_cost`, `export_credit`, `daily_charge`,
`import`, `export`, `production`, `self_consumption`, or `solar_share`.
`period` is `hour`, `day`, `week`, `month`, or `auto`, which follows the
chart's range.

A range rounded to a calendar unit, such as `now/d`, is rounded in the energy
time zone, and `now/w` starts on the configured first day of the week. A chart
that reads only energy draws its axis over that range. Each target is one
series. The `table` format gives one row per target with its total, for pie and
bar charts. Units are `kwatth`, the currency, or `percent`. A placed chart
grants its viewers its own queries, so a shared energy dashboard needs no
permission on the sources.

**Create energy dashboard** makes an ordinary dashboard of charts:

- today, this week, and this month, with usage and total cost;
- 30 days of daily use;
- a breakdown by group, or by source when there are no groups, with untracked
  use;
- a table of sources;
- production, import, export, and self-consumption by hour, when there is
  solar.

## Flows

Nodes are in the **Energy** folder.

**Energy Usage** reads a target's kWh and cost over today, yesterday, this
week, this month, the last hours, or a range. For the house, the cost includes
charges and credit. A power source's current draw is `power_w`. With **Compare
with usual**, the node adds `usual_kwh`, the average of the same span on the
previous four matching weekdays, and `ratio`, the number an unusual-use alert
compares.

**Energy Price** reads the price and period now or at a given time, with
`ends_at` when the price next changes. It can instead find the cheapest window
of a given length within a range, returning `{start, end, average_rate,
currency}`. For a load that must finish by 07:00, ask for a window between now
and 07:00.

**Energy Price Period** starts a run at each price boundary with `{period,
rate, currency, starts_at, ends_at, previous_period}`. Boundaries passed while
the server was down are caught up to a week back, following the node's
missed-deadline setting, and none fire twice.

## Filling and importing history

Home Assistant entities fill from Home Assistant's hourly statistics, using
the change of an energy meter or the mean of a power sensor. Home Assistant
keeps no statistics for attributes, so a source that reads an attribute has no
history and carries its usage across a silence. Home Assistant's statistics
hours start on the UTC hour, so in a time zone offset by 30 or 45 minutes,
each is shared between the two local hours it overlaps.

Controls and devices fill by replaying, through the same meter rules, the
series their history forward writes to a Prometheus or InfluxDB connection.
Filling never overwrites hours recorded live, except the two hours a filled
silence starts and ends in, which were heard for only part of the hour.

**Import history** on a source reads up to 400 days the same way, a day at a
time in the background, and shows its progress on the source.

## REST and MCP

Reading needs `api:connections:read`, changing needs `api:connections:write`,
and the dashboard preset needs `api:dashboards:write`.

| REST | MCP |
| --- | --- |
| `GET, POST /api/energy/sources`, `PUT, DELETE /api/energy/sources/{id}` | `list_energy_sources`, `save_energy_source`, `delete_energy_source` |
| `GET /api/energy/candidates` | `list_energy_candidates` |
| `POST /api/energy/sources/{id}/import` | `import_energy_history` |
| `GET, POST /api/energy/groups`, `PUT, DELETE /api/energy/groups/{id}` | `list_energy_groups`, `save_energy_group`, `delete_energy_group` |
| `GET, POST /api/energy/tariffs`, `PUT, DELETE /api/energy/tariffs/{id}` | `list_energy_tariffs`, `save_energy_tariff`, `delete_energy_tariff` |
| `GET, PUT /api/energy/settings` | |
| `GET /api/energy/rollups?target=&measures=&from=&to=&period=` | `get_energy_rollups` |
| `GET /api/energy/price?at=`, `GET /api/energy/price/cheapest?hours=&from=&to=` | `get_energy_price`, `find_cheapest_energy_window` |
| `POST /api/energy/preset` | `create_energy_dashboard` |
| `POST /api/connections/energy/timeseries/query` | `query_timeseries` with `connection_id` `energy` |

A deleted source keeps its hours for 30 days. Karakuri then removes them.

## Limits

| Limit | Value |
| --- | --- |
| Sources | 200 |
| Groups | 50 |
| Writes | Every 30 seconds |
| History import | 400 days |
| Cheapest window | 15 minutes to 48 hours, within 7 days |
| Rollup | 10,000 periods |

Energy tracking covers electricity only, not gas or water. Karakuri does not
fetch supplier price feeds. A flow can feed a dynamic price through a rate
control.
