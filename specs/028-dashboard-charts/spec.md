# Feature specification: Dashboard charts

**Feature**: `specs/028-dashboard-charts`
**Created**: 2026-10-30
**Status**: Implemented
**Input**: Graph time-series connections on dashboards:
every visualization that draws query data, several queries per widget
from different connections, field options, overrides, and
transformations, and time ranges and refresh driven by dashboard variables.

## User scenarios and testing

### User story 1: Graph a database on a dashboard (Priority: P1)

An author places a Chart widget, picks the InfluxDB connection, writes a query
for the living room temperature, and sees the line in the editor before saving.
A second query on the same widget draws the outdoor temperature from Home
Assistant statistics.

**Why this priority**: This is what the time-series connections of
[feature 027](../027-timeseries-connections/spec.md) are for on a dashboard.
**Independent test**: Seed a test database, place a time series chart with one
query against it and one against Home Assistant, and compare the drawn values
with the seeded data and Home Assistant's own history view.

**Acceptance scenarios**:

1. **Given** a chart with two queries on different connections, **When** a
   viewer opens the dashboard, **Then** both series are drawn on one time axis
   with their own legend entries.
2. **Given** an author editing a query, **When** they run the preview, **Then**
   the editor draws the result and shows any query error without saving.
3. **Given** one query fails and the other succeeds, **When** the chart loads,
   **Then** the successful series is drawn and the failure is shown on the
   chart with the database's message.
4. **Given** a chart with a 30-second refresh, **When** new points arrive in the
   database, **Then** the viewer draws them within one refresh without
   reloading.

### User story 2: Keep queries away from viewers (Priority: P1)

A public dashboard shows a power graph. An anonymous viewer can see the graph
but cannot make the server run any other query, read any other data, or learn
the connection's credentials.

**Why this priority**: Query text against a database is far more powerful than
the entity bindings dashboards grant today.
**Independent test**: Open a public chart anonymously, then try to run a query
naming another connection, another placement's index, edited query text, and a
variable value that is not an option.

**Acceptance scenarios**:

1. **Given** an anonymous viewer of a public dashboard, **When** the chart
   loads, **Then** the server runs the placement's saved queries and returns
   their series.
2. **Given** the same viewer, **When** it sends query text, a connection, or an
   option that the served layout does not hold, **Then** the request is refused.
3. **Given** a viewer with no access to the dashboard, **When** it asks for a
   placement's data, **Then** it is refused as if the dashboard did not exist.
4. **Given** an editor previewing an unsaved query, **When** the preview runs,
   **Then** it requires edit access to the dashboard and connection read on the
   connection it names.
5. **Given** a dashboard editor without connection read, **When** they save a
   chart with a query, **Then** the save is refused, since the chart would
   otherwise read any connection for them; moving or restyling it succeeds.

### User story 3: Pick a time range for the whole dashboard (Priority: P1)

The author adds a Time range variable displayed above the dashboard. A viewer
picks "Last 7 days", and every chart that names the variable redraws. Dragging
across one chart zooms every chart to that span. A link carries the range.

**Why this priority**: A time picker is how chart dashboards are read, and
without one every chart has a fixed span.
**Independent test**: Bind two charts to `${range}`, pick a preset, zoom by
dragging, step back, and open a `var-range=` link.

**Acceptance scenarios**:

1. **Given** two charts with time range `${range}`, **When** the viewer picks
   "Last 7 days", **Then** both redraw over the last seven days and the address
   records the range.
2. **Given** a chart, **When** the viewer drags across part of its time axis,
   **Then** the variable takes that absolute range and every chart bound to it
   follows.
3. **Given** a chart with its own relative time of 1 hour and a time shift of
   1 day, **When** it loads, **Then** it draws the hour ending one day before
   now whatever the dashboard range is.
4. **Given** an interval variable `${step}` used in a query, **When** the
   viewer picks `5m`, **Then** the query runs with `5m` substituted.

### User story 4: Choose how the data is drawn (Priority: P1)

The author switches the same queries between a time series, a stat with a
sparkline, a gauge, a bar gauge, a table, a pie chart, a state timeline, and
the other visualizations without rewriting the queries.

**Why this priority**: A wall panel needs big numbers and gauges as much as
lines; the choice belongs to the author, not the data source.
**Independent test**: Draw one seeded query in every visualization and check
each against the values the query returned.

**Acceptance scenarios**:

1. **Given** a temperature query, **When** the visualization is Stat with
   calculation `last` and an area graph, **Then** it shows the last value with
   its unit and a sparkline of the range.
2. **Given** thresholds at 0 green, 25 orange, and 30 red, **When** a gauge
   shows 27, **Then** it is drawn orange.
3. **Given** value mappings of 1 to "Open" in red and 0 to "Closed" in green,
   **When** a state timeline draws a door sensor, **Then** each span is
   labelled and coloured by its mapping.
4. **Given** a table of several series, **When** the viewer sorts by a column,
   **Then** the rows reorder in that viewer only.

### User story 5: Shape data before drawing it (Priority: P2)

The author renames series by regex, joins two queries by time, calculates a
difference field, and overrides the colour and unit of one series.

**Why this priority**: Raw query results rarely carry the names and units a
household wants to read.
**Independent test**: Apply a fixed list of transformations and overrides to
seeded data and compare the drawn fields and values with the expected result.

**Acceptance scenarios**:

1. **Given** a join by time and a binary calculation `A - B`, **When** the chart
   draws, **Then** the calculated field holds the difference at each time both
   series have a value.
2. **Given** an override matching fields named `outdoor.*` that sets colour
   blue and unit °F, **When** drawn, **Then** only those series use them.

### User story 6: Use charts in renders and flows' displays (Priority: P3)

A scheduled render of the energy dashboard draws its charts over the range the
render job names.

**Why this priority**: Renders reuse the viewer, so this is small, and reports
are a common use of graphs.
**Independent test**: Render a dashboard with charts with `var-range=now-7d,now`
and check the image shows a week.

**Acceptance scenarios**:

1. **Given** a render job naming a time range value, **When** it renders,
   **Then** every chart bound to the variable has drawn that range before the
   image is taken, with no animation or picker.

### Edge cases

- A chart holds 1 to 10 queries, each labelled `A` to `J` by default.
- A placement's data request is answered from a cache shared by every viewer
  for the same queries, range rounded to the query step, and variable values,
  for at most the chart's refresh interval and never less than 5 seconds.
- The minimum refresh interval is 5 seconds. A refresh of `off` loads once.
- A query returning no data draws the chart's no-value text, "No data" by
  default.
- A visualization that cannot draw the returned shape, such as a candlestick
  without open, high, low, and close fields, shows a message naming the missing
  fields.
- A range variable value that does not parse is ignored and the default used,
  as an invalid option is in feature 025.
- Thresholds, mappings, and overrides that name fields the result lacks are
  ignored.
- Text from query results, series names, and mappings is drawn as text, never
  as markup.
- A chart on a dashboard the viewer cannot read is never loaded.

## Requirements

### Functional requirements

- **FR-001**: The core plugin MUST ship a `chart` widget whose configuration
  holds a visualization type, 1 to 10 queries, a time range setting, a refresh
  interval, field defaults, overrides, transformations, and the visualization's
  own options.
- **FR-002**: Each query MUST name a connection readable by 027's query
  service, its query text or Home Assistant selection, a reference ID, an
  optional legend format, a maximum data points value or automatic sizing from
  the chart's width, an optional minimum interval, and a hidden flag that keeps
  its data available to transformations without drawing it.
- **FR-003**: The server MUST run a placement's queries only by dashboard,
  placement, and reference ID. It MUST read the queries from the layout the
  caller is served, as the news feed route does, substitute viewer variable
  values only when each is an option of a served variable, and refuse anything
  else. Requests MUST carry no query text, connection, or credentials.
- **FR-004**: Viewer data requests MUST require the same access as viewing the
  dashboard, including public, shared, and render access. Editor previews of
  unsaved queries MUST require edit access to the dashboard and connection read.
- **FR-005**: The chart MUST take its time range from a time range variable
  reference, a fixed range, or the default of the last 6 hours. A relative time
  override and a time shift MUST replace or move that range for one chart.
- **FR-006**: The visualizations MUST be time series, trend, bar chart, stat,
  gauge, bar gauge, table, pie chart, state timeline, status history, heatmap,
  histogram, candlestick, and XY chart, each with the options listed in
  the plan.
- **FR-007**: Every visualization MUST support the standard field options:
  unit, decimals, minimum, maximum, display name, colour scheme (single colour,
  palette, thresholds, or a continuous gradient), thresholds in absolute or
  percentage mode, value mappings by value, range, regex, or special value
  (null, NaN, true, false, empty), and no-value text.
- **FR-008**: Overrides MUST match fields by name, name regex, query reference
  ID, or field type, and set any standard field option or visualization field
  option for the matched fields.
- **FR-009**: Transformations MUST run in order on the query results before
  drawing: organize fields, rename by regex, filter fields by name, filter data
  by values, reduce, add field from calculation (binary operation, reduce row,
  or unary operation), join by field, merge, series to rows, labels to fields,
  group by, sort by, limit, and convert field type.
- **FR-010**: Time-based visualizations MUST show a tooltip in single, all, or
  hidden mode, and a legend in list or table mode, at the bottom or right, with
  optional calculated values per series.
- **FR-011**: Dragging across a time-based chart's axis MUST set the bound time
  range variable for that viewer, or zoom only that chart when it has a fixed
  range. A double click MUST zoom out.
- **FR-012**: The chart's settings panel MUST offer a query editor per
  connection kind with metadata completion from 027's lookups, a preview, a
  query inspector showing the substituted query, the response, and its timing,
  and every option above, accepting `${name}` references where 025 allows.
- **FR-013**: Charts MUST draw with the dashboard's theme colours by default and
  follow theme changes without reloading.
- **FR-014**: Renders MUST wait for every chart's data and drawing before the
  capture, and MUST draw with no animation.
- **FR-015**: MCP MUST be able to read a placement's chart data with the same
  access rules, for checks and assistants.
- **FR-016**: Saving a chart placement whose queries change (connection,
  language, text, or Home Assistant selection) through REST, MCP, or import
  MUST require connection read. Moving, resizing, restyling, or placing a chart
  with no queries MUST NOT.

### Key entities

- **Chart configuration**: visualization type, queries, time settings, refresh,
  field defaults, overrides, transformations, options. Stored in the placement's
  config and versioned with the dashboard.
- **Chart data response**: per query, its series or error, the substituted
  range, the interval used, and whether it came from the cache.

## Success criteria

- **SC-001**: Every listed visualization draws the same seeded query correctly,
  checked by screenshot against the seeded values.
- **SC-002**: An anonymous viewer of a public chart receives its series and is
  refused each of the four out-of-bounds requests in user story 2.
- **SC-003**: Picking a range preset and dragging a zoom redraw two charts bound
  to the same variable, checked in a browser with no console errors.
- **SC-004**: Ten viewers of one chart cause one query per refresh interval to
  the database.
- **SC-005**: A render with a range value shows that range in every bound chart.

## Assumptions and scope

- Visualizations that do not draw time-series or table query data are
  out of scope: text, news, dashboard list, alert list, and annotations list
  (Karakuri has its own widgets for these jobs), logs, traces, flame graph, and
  node graph (no connection here returns that data), geomap (needs a map tile
  service), and canvas (a Karakuri dashboard is already a free layout).
- Annotations, alerting rules, library panels, and panel repeating are out of
  scope.
- Charts do not stream points between refreshes.
- The History Graph widget of feature 024 remains for live control and MQTT
  series without a database.
- Time range, interval, and database-backed query variables are specified in
  [feature 025](../025-dashboard-variables/spec.md).

## References

- [Time-series connections](../027-timeseries-connections/spec.md)
- [Dashboard variables](../025-dashboard-variables/spec.md)
- [Dashboard access and rendering](../007-dashboard-access-rendering/spec.md)
- [docs/dashboard-authoring.md](../../docs/dashboard-authoring.md)
- [docs/dashboard-security.md](../../docs/dashboard-security.md)
