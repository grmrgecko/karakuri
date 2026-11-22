# Feature specification: Energy tracking

**Feature**: `specs/035-energy-tracking`
**Created**: 2026-11-05
**Status**: Implemented
**Input**: Energy tracking on top of the time-series work of features
[027](../027-timeseries-connections/spec.md) and
[028](../028-dashboard-charts/spec.md). Plugs and meters already report power
(W) and energy (kWh) through MQTT devices, Zigbee2MQTT, ESPHome, and Home
Assistant. Define energy sources reading cumulative energy or instantaneous
power, with meter resets and gaps handled, grouped into circuits or rooms, with
a whole-house meter and optional solar production and grid export. Tariffs:
flat rate, time-of-use periods by weekday and time, a fixed daily charge,
currency, effective dates so price changes keep history correct, and export
credit. Rollups: usage and cost per hour, day, week, and month, per source and
group, stored durably and recomputed when a tariff changes, filled from
forwarded history when the server missed readings. Flows read current usage,
period totals, and the current price to automate. An energy dashboard preset
shows today, this week, and this month with cost, a breakdown by source, and
solar against grid. REST and MCP expose sources, tariffs, and rollups.

## User scenarios and testing

### User story 1: Track what the house uses (Priority: P1)

A homeowner has a whole-house meter on a Zigbee clamp (cumulative kWh), six
smart plugs reporting kWh, and an ESPHome sensor on the heat pump reporting
only watts. They add each as an energy source, put the plugs into groups
(Office, Kitchen, Media), and mark the clamp as the whole house. From then on
they can see how many kWh each plug, group, and the house used in any hour,
day, week, or month, and how much of the house is not covered by any plug.

**Why this priority**: Usage per period is what every other part of this
feature builds on. Without trustworthy totals, cost and automation are wrong.
**Independent test**: Feed a source a known sequence of readings, including a
meter reset and a gap, and compare its hourly and daily totals with values
worked out by hand.

**Acceptance scenarios**:

1. **Given** a cumulative energy source reading 100.0 kWh at 10:00 and 101.5
   kWh at 11:00, **When** the 10:00 hour closes, **Then** that hour holds 1.5
   kWh for the source and its groups.
2. **Given** a cumulative source whose meter resets from 250.3 to 0.2 kWh,
   **When** the reading arrives, **Then** the reset adds 0.2 kWh, not minus
   250.1, and the source shows that it reset.
3. **Given** a power-only source reporting 2000 W steadily for 30 minutes,
   **When** the hour closes, **Then** it holds 1.0 kWh.
4. **Given** a power-only source that stops reporting for longer than its gap
   limit, **When** it reports again, **Then** the silent stretch adds nothing
   and is marked as a gap rather than being guessed at.
5. **Given** a whole-house meter reading 3.0 kWh in an hour and plugs summing
   to 1.2 kWh, **When** the breakdown is shown, **Then** it lists 1.8 kWh as
   untracked.

---

### User story 2: Know what it costs (Priority: P1)

The homeowner's electricity has a peak rate on weekdays from 16:00 to 21:00,
an off-peak rate otherwise, a fixed daily charge, and a credit for each kWh
exported from their solar panels. They enter this tariff once with its start
date. Every hour of usage is priced at the rate that applied in that hour, and
each day carries its fixed charge. When the supplier raises prices from the
first of next month they add the new rates with that date; past months keep
their old cost.

**Why this priority**: Cost is what people act on; kWh alone rarely changes
behaviour.
**Independent test**: Define a two-period tariff, feed a day of hourly usage
across both periods, and compare the day's cost with a hand calculation.
Then add a later price change and check that earlier days are unchanged.

**Acceptance scenarios**:

1. **Given** a weekday tariff with peak 16:00 to 21:00 at 0.40 and off-peak at
   0.15 per kWh, **When** 1 kWh is used at 17:00 and 1 kWh at 22:00, **Then**
   the day's energy cost is 0.55 plus the daily charge.
2. **Given** rates that change on 2026-12-01, **When** usage on 2026-11-30 and
   2026-12-01 is costed, **Then** each day uses the rates in force that day.
3. **Given** a tariff edited to correct last month's peak rate, **When** the
   edit is saved, **Then** the affected hours' costs are recomputed and the
   totals for last month change, while usage stays as it was.
4. **Given** 3 kWh exported in an hour with an export credit of 0.08 per kWh,
   **When** the hour is costed, **Then** it shows a credit of 0.24 against that
   hour's import cost.
5. **Given** a tariff whose periods leave part of the week without a rate,
   **When** it is saved, **Then** it is refused with the uncovered times named.

---

### User story 3: Solar and the grid (Priority: P2)

The homeowner has a solar inverter reporting production and a grid meter
reporting import and export separately. They mark these sources accordingly.
For each period they see production, import, export, how much of the solar was
used in the house, and how much of the house's use came from solar.

**Why this priority**: Solar owners are a large share of people who track
energy, but the rest of the feature works without solar.
**Independent test**: Feed production, import, and export for a day and
compare self-consumption and the share of use covered by solar with values
worked out by hand.

**Acceptance scenarios**:

1. **Given** in one hour 4 kWh produced, 1 kWh imported, and 2.5 kWh exported,
   **When** the hour closes, **Then** home use is 2.5 kWh, solar used at home
   is 1.5 kWh, and solar covered 60% of home use.
2. **Given** no whole-house meter but import, export, and production sources,
   **When** the house total is shown, **Then** it is derived as import plus
   production minus export.

---

### User story 4: Automate around price and usage (Priority: P2)

The homeowner wants the dishwasher to start in the cheapest three-hour window
before 07:00, a notification when the house uses more than usual by 20:00, and
the heat pump turned down while the peak rate applies. They build these with
flow nodes that read the current price, find the cheapest window ahead, read a
period's usage against its usual level, and fire when the price period
changes.

**Why this priority**: Acting on price and usage is the reason to have them in
an automation engine rather than a reporting tool.
**Independent test**: With a time-of-use tariff and fixed usage data, run each
node and compare its output with the tariff and data; advance the clock across
a period boundary and check the trigger fires once.

**Acceptance scenarios**:

1. **Given** a time-of-use tariff, **When** a flow asks for the cheapest 3-hour
   window starting before 07:00 tomorrow, **Then** it gets the window's start,
   end, and average price.
2. **Given** a flow trigger on price period changes, **When** the clock passes
   16:00 on a weekday, **Then** the trigger fires once with the new period, its
   price, and when it ends.
3. **Given** a usage read for today so far, **When** the flow runs at 20:00,
   **Then** it gets today's kWh and cost and the average of the same span on
   the previous four same weekdays.
4. **Given** a server restart at 15:59, **When** it comes back at 16:01,
   **Then** the period change trigger fires for 16:00 according to the
   missed-deadline setting of [feature 029](../029-durable-waits/spec.md).

---

### User story 5: An energy dashboard in one step (Priority: P2)

The homeowner creates a dashboard from the energy preset and gets a page
showing today, this week, and this month with usage and cost, a bar chart of
the last 30 days, a breakdown by group and source, and, when solar is set up,
production against import and export. They can then rearrange or edit it like
any dashboard.

**Why this priority**: It makes the feature visible without learning the chart
widget. The data from user stories 1 to 3 is usable without it.
**Independent test**: Create the preset on a server with sources and a tariff,
open it, and compare each figure with the rollups returned by REST.

**Acceptance scenarios**:

1. **Given** sources and a tariff, **When** an author creates a dashboard from
   the energy preset, **Then** it opens with the summary, history, and
   breakdown sections filled in.
2. **Given** no solar sources, **When** the preset is created, **Then** it has
   no solar section.
3. **Given** a viewer of a shared energy dashboard, **When** they open it,
   **Then** they see its figures without any permission on the sources'
   devices or connections, and cannot change sources or tariffs.

### Edge cases

- A cumulative reading that goes down by less than 1% of its value or less than
  0.01 kWh is meter noise, not a reset, and adds nothing.
- A cumulative reading that jumps by more than a source's plausible maximum
  (by default what 25 kW would use over the elapsed time) is rejected as a bad
  reading and logged; the next sane reading continues from the last accepted
  one.
- A source whose device or entity reports `unavailable` or nothing at all
  contributes no usage for that time and the period is marked incomplete for
  that source.
- After a server outage, hours with no readings are filled from the source's
  forwarded history or Home Assistant statistics when available; otherwise a
  cumulative source's usage across the outage is assigned to the hour of the
  first reading after it, and is marked as such.
- A reading timestamped in the future or more than a day in the past is
  ignored, except during a fill from history.
- A source removed from a group keeps its past usage in that group's history;
  only new hours follow the new grouping.
- A deleted source keeps its rollups for 30 days so a mistaken delete can be
  undone, then they are removed.
- Daylight saving: a day with 23 or 25 hours has 23 or 25 hourly rollups, and
  time-of-use periods follow local wall time in the configured time zone.
- Plugs that measure a load also measured by another plug (an extension strip
  behind a plug) are not detected; the author decides what to track.
- A tariff saved with overlapping periods on the same weekday and time is
  refused with the overlap named.
- Recomputing costs after a tariff change covers at most the period it affects
  and finishes within the success criteria below; other requests are served
  meanwhile with the previous figures marked as being recomputed.
- Negative export credits or rates, which some suppliers have, are allowed.

## Requirements

### Functional requirements

- **FR-001**: Authors MUST be able to define an energy source from a control,
  an MQTT, Zigbee2MQTT, or ESPHome device, or a Home Assistant entity,
  choosing the attribute it reads, whether it is cumulative energy or
  instantaneous power, and its unit (Wh, kWh, MWh for energy; W, kW for power).
  A source MUST be suggested from devices and entities whose unit or device
  class says energy or power.
- **FR-002**: Each source MUST have a role: consumer (a circuit, plug, or
  appliance), whole house, grid import, grid export, or solar production. At
  most one source MAY have the whole-house role.
- **FR-003**: Sources MUST be groupable into named groups such as rooms or
  circuits. A source MAY be in several groups.
- **FR-004**: Usage MUST be accumulated per source per hour of local time:
  cumulative sources by the difference between readings, power sources by
  integrating readings over time, each following the reset, noise, implausible
  jump, gap, and unavailable rules in the edge cases.
- **FR-005**: Hourly usage MUST be stored durably and survive restarts and
  upgrades. Day, week, and month totals MUST be derived from hours in local
  time, with the first day of the week configurable.
- **FR-006**: The house total MUST come from the whole-house source when there
  is one, otherwise from import plus production minus export when those exist,
  otherwise from the sum of consumer sources. Untracked usage MUST be the house
  total minus consumer sources, never shown below zero.
- **FR-007**: Self-consumption MUST be production minus export, and the solar
  share of home use MUST be self-consumption divided by the house total, per
  period.
- **FR-008**: When a source's history is forwarded to a time-series connection
  or kept in Home Assistant statistics, the system MUST fill hours the server
  missed from that history, and an author MUST be able to import a source's
  past from it once when adding the source, limited to 400 days.
- **FR-009**: A tariff MUST have a currency, an effective start date, energy
  rates per kWh as a flat rate or as time-of-use periods by weekday and local
  time that together cover the whole week without overlap, an optional fixed
  charge per day, and an optional export credit per kWh, flat or by period.
- **FR-010**: A tariff MUST be able to take its rate from a number control,
  so a flow can feed a dynamic price. The rate in force each hour MUST be
  recorded with that hour's cost so later changes of the control do not change
  past costs.
- **FR-011**: Each hour's cost MUST use the tariff whose effective date covers
  it and the rate in force at that hour. Adding, editing, or deleting a tariff
  MUST recompute the costs of the hours it affects and leave usage unchanged.
- **FR-012**: Costs MUST be reported per source, group, and house for every
  period, with energy cost, export credit, fixed charges, and the total shown
  separately, rounded only for display.
- **FR-013**: A flow node MUST read, for a source, group, or the house, the
  current power where known and the usage and cost of today, yesterday, this
  week, this month, a named past period, or the last N hours.
- **FR-014**: A flow node MUST read the price in force now or at a given time,
  and find the cheapest window of a given length within a given range,
  returning its start, end, and average price.
- **FR-015**: A flow node MUST compare a period's usage so far with the average
  of the same span on the previous four matching weekdays and report both and
  their ratio.
- **FR-016**: A trigger MUST fire when the tariff's price period changes,
  carrying the new period, its price, and when it ends, and MUST follow the
  missed-deadline handling of feature 029 across restarts.
- **FR-017**: Dashboards MUST show energy figures through the chart widget of
  feature 028, which MUST be able to query usage, cost, and solar figures for
  a source, a group, untracked usage, or the house at hour, day, week, or
  month resolution. A placed chart MUST grant its viewers those figures only,
  as charts already grant their own queries.
- **FR-018**: An author MUST be able to create a dashboard from an energy
  preset with a summary of today, this week, and this month, a 30-day history,
  a breakdown by group and source with untracked usage, and a solar section
  when solar sources exist. The result MUST be an ordinary editable dashboard.
- **FR-019**: REST and MCP MUST list, create, edit, and delete sources, groups,
  and tariffs, and read rollups for a source, group, or the house over a range
  at hour, day, week, or month resolution, with the same service-layer rules as
  the UI. Reading needs connection read; changing needs connection management.
- **FR-020**: Settings MUST offer an Energy page to manage sources, groups, and
  tariffs, showing each source's last reading, its state (ok, gap, reset,
  unavailable), and today's usage.
- **FR-021**: The same energy queries MUST be available to the time-series
  query tools of REST and MCP, so energy figures combine with other history.
  Flows read energy through the energy nodes of FR-013 to FR-016.

### Key entities

- **Energy source**: what is read (control, device, or Home Assistant entity
  and attribute), measurement type (cumulative or power), unit, role, groups,
  gap limit, plausible maximum, last accepted reading, and state.
- **Energy group**: a named set of sources, such as a room or circuit.
- **Tariff**: currency, effective start, rate periods, fixed daily charge,
  export credit, and optionally a rate control.
- **Hourly rollup**: one source's usage in one local hour with its
  completeness, how it was obtained (live, filled from history, or carried over
  a gap), and its cost with the rate used.

## Success criteria

- **SC-001**: For a scripted day of readings with one reset, one gap, and one
  implausible jump, every hourly total matches the hand-worked value to within
  0.001 kWh.
- **SC-002**: A day's cost under a two-period tariff with a daily charge and
  export credit matches a hand calculation to the cent.
- **SC-003**: Changing a tariff that affects a year of hourly data for 20
  sources is reflected in every total within 10 seconds.
- **SC-004**: Readings keep accumulating across a server restart: a restart in
  the middle of an hour loses no usage for cumulative sources.
- **SC-005**: An author with sources and a tariff can create the energy preset
  and see today's usage and cost in under a minute.
- **SC-006**: The cheapest-window node returns the correct window for a tariff
  with five periods and a 3-hour window in under a second.

## Assumptions and scope

- Electricity only. Gas and water use the same ideas but have different units
  and tariffs; they are out of scope here.
- Dynamic and spot-price tariffs from suppliers are not fetched by this
  feature; a flow can feed such a price through a rate control (FR-010).
- One tariff applies to the whole house at a time; separate tariffs per circuit
  are out of scope.
- Taxes are part of the rates the author enters; the feature does not compute
  them separately.
- The feature records its own hourly rollups in karakuri's database, so it
  works without a time-series connection; connections and Home Assistant
  statistics are used to fill missed hours and import history.
- Rollups are kept indefinitely; an hour is one small row per source.
- Battery storage is treated as a consumer and a producer when it reports
  charge and discharge as separate sources; battery-specific views are out of
  scope.

## References

- [Feature 027: time-series connections and history forwarding](../027-timeseries-connections/spec.md)
- [Feature 028: dashboard charts](../028-dashboard-charts/spec.md)
- [Feature 029: durable waits](../029-durable-waits/spec.md)
- [Feature 004: connections, MQTT devices, and controls](../004-connections-devices/spec.md)
- [Feature 007: dashboard access](../007-dashboard-access-rendering/spec.md)
- `docs/timeseries.md`, `docs/dashboard-authoring.md`, `docs/flow-authoring.md`
