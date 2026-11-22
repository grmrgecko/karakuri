# Feature specification: Dashboard variables

**Feature**: `specs/025-dashboard-variables`
**Created**: 2026-10-26
**Status**: Implemented; time range, interval, and time-series query variables drafted 2026-10-30
**Input**: Add filter support to dashboards, flexible enough for a viewer to
pick values or for flows to switch them. For example, a mirror
whose face detection switches it between two people's data. Then enhance
the variable model: display modes, descriptions, constant and query
variables, and the editor's organization. Then add time range and
interval variables, and query variables over the time-series connections of
feature 027, for the charts of feature 028.

## User scenarios and testing

### User story 1: Switch a dashboard between people from a flow (Priority: P1)

A household mirror shows one person's calendar, commute, and steps. A face
detection flow sends a Dashboard Command that sets the `person` variable to
whoever is standing in front of it, and every widget follows.

**Why this priority**: It is the case that asked for the feature. Without it,
an author has to build every widget twice and hide half of them.
**Independent test**: Define a `person` variable with two options, bind a
widget to `sensor.${person}_steps`, open two viewers, send a variable command
from a flow, and see both viewers rebind without a reload.

**Acceptance scenarios**:

1. **Given** a dashboard with a `person` variable of `james` and `wife`,
   **When** a flow sets `person` to `wife`, **Then** every open viewer shows
   the `sensor.wife_steps` reading and a newly opened viewer starts on `wife`.
2. **Given** a variable command naming a value that is not one of the
   variable's options, **When** it arrives, **Then** it is refused and nothing
   changes.
3. **Given** a variable command with an empty value, **When** it arrives,
   **Then** viewers return to the variable's default.
4. **Given** the server restarts, **When** a viewer reconnects, **Then** every
   variable is back at its default.

### User story 2: Template widgets with variable values (Priority: P1)

An author writes `${person}` wherever a widget's configuration names a
person-specific thing: an entity ID, a control, a title. Viewers read only the
sources the variable's options can produce.

**Why this priority**: Templating is what turns one widget into one per option.
Without a bounded grant, a public dashboard could be steered to any entity.
**Independent test**: Make the dashboard public, open it anonymously, and read
the entity for each option; request a third entity on the same connection and
be refused.

**Acceptance scenarios**:

1. **Given** a binding to `sensor.${person}_steps` with options `james` and
   `wife`, **When** an anonymous viewer of the public dashboard reads states,
   **Then** it receives `sensor.james_steps` and `sensor.wife_steps` and no
   other entity on that connection.
2. **Given** a title of `${person:text}'s day`, **When** the variable is `wife`
   with label `Sam`, **Then** the widget shows "Sam's day".
3. **Given** a placement that references a variable the dashboard does not
   define, **When** it renders, **Then** the reference stays as written and
   validation reports it.
4. **Given** a history graph whose `hours` is exactly `${range}` and a `range`
   variable of `6`, `24`, and `168`, **When** the viewer picks `24`, **Then**
   the widget receives the number 24.

### User story 3: Pick a value from a picker (Priority: P2)

A viewer chooses a value from a picker and shares a link that opens on that
value. The author decides where each picker is drawn.

**Why this priority**: A picker suits a wall panel or a
laptop someone stands in front of, and a link is how a second screen is pinned.
**Independent test**: Pick a value in one viewer, confirm a second viewer is
unchanged, then open `?var-person=wife` and see it start on `wife`.

**Acceptance scenarios**:

1. **Given** a viewer, **When** they pick a variable value, **Then** only that
   viewer changes and its address gains `var-person=<value>` without a reload.
2. **Given** an address with `var-person=wife`, **When** it opens, **Then** the
   viewer starts on `wife` whatever the dashboard-wide value is.
3. **Given** an address naming a value that is not an option, **When** it
   opens, **Then** the viewer ignores it.
4. **Given** a viewer that picked `james`, **When** a flow later sets `wife`,
   **Then** the viewer follows the flow, because the latest change wins.
5. **Given** an address with `pin=person`, **When** a flow sets `person`,
   **Then** that viewer keeps its own value.
6. **Given** variables displayed above the dashboard, with the label hidden,
   in the controls menu, and hidden, **When** the dashboard opens, **Then** the
   first two draw in the bar above it, the third behind a menu button, and the
   fourth nowhere. A description shows as the picker's tooltip.

### User story 4: Show placements for one value (Priority: P2)

An author shows a placement only while a variable has a given value, for
widgets whose data the host cannot template, such as a calendar plugin's
sources.

**Why this priority**: It covers widgets whose configuration the server reads
itself, and needs no new grant.
**Independent test**: Add a variable condition of `person equals wife`, switch
the variable, and watch the placement appear and disappear.

**Acceptance scenarios**:

1. **Given** a placement with a variable condition `person equals wife`,
   **When** the variable changes to `wife`, **Then** the placement shows; when
   it changes back it hides.

### User story 5: Render a chosen value (Priority: P3)

A scheduled report renders each person's view by naming variable values on the
render job.

**Why this priority**: Renders reuse the viewer, so this is small.
**Independent test**: Render with `variables: {person: "wife"}` and see that
value in the image; render with an unknown value and be refused.

**Acceptance scenarios**:

1. **Given** a render job naming `person: wife`, **When** it renders, **Then**
   the image shows `wife`'s data and no picker.
2. **Given** a render job naming a value the rendered layout does not offer,
   **When** it is created, **Then** it is refused.

### User story 6: Take options from Home Assistant (Priority: P1)

An author makes `person` a query variable over `person.*` entities, or over an
`input_select.active_person` helper. With the helper, the dashboard follows its
state, so a Home Assistant automation that sets it switches the mirror without a
Karakuri flow.

**Why this priority**: Lists typed by hand drift from the house. Following a
helper connects the dashboard to automations that already exist, such as face
detection in Frigate or Double Take.
**Independent test**: Define an entity query of `person.*` with the regex
`person\.(?<value>.+)`, preview it in the editor, then switch to an
`input_select` source with follow on, change the helper in Home Assistant, and
watch the viewer switch.

**Acceptance scenarios**:

1. **Given** an entity query of `person.*`, **When** the dashboard opens,
   **Then** the picker lists every matching entity by friendly name.
2. **Given** a regex with a `value` group, **When** options resolve, **Then**
   each value is the captured text and entities that do not match are left out.
3. **Given** an `input_select` source with follow on, **When** the helper's
   state changes, **Then** every viewer not pinned switches to it, and a
   variable command naming that variable is refused.
4. **Given** Home Assistant is unreachable, **When** options resolve, **Then**
   the variable has no options, viewers leave its references as written, and
   it grants nothing.

### User story 7: Keep shared values in one place (Priority: P3)

An author writes `${ha}` for a Home Assistant connection ID used across every
widget, so moving the dashboard to another instance changes one value.

**Why this priority**: Saves editing every widget; small.
**Independent test**: Define a constant `ha`, bind widgets with
`connectionId: "${ha}"`, and change the constant.

**Acceptance scenarios**:

1. **Given** a constant variable, **When** the dashboard opens, **Then** no
   picker is drawn, references take its value, and variable commands naming it
   are refused.

### User story 8: Template widgets from their settings panels (Priority: P1)

An author binds an Entity Control to `${ha}` and `person.${person}` from the
widget's own settings panel instead of editing JSON, and reopening the panel
keeps both references.

**Why this priority**: Without it, templated bindings can only be written
through MCP or raw JSON, and opening a panel on one can clear it.
**Independent test**: Choose `${ha}` from the connection list of each bound
widget's panel, type `person.${person}`, close and reopen the panel, and see
both kept.

**Acceptance scenarios**:

1. **Given** a dashboard with variables, **When** a connection, control, or
   device list opens in a widget's settings, **Then** it offers each variable
   as `${name}` beside the real choices.
2. **Given** a stored reference, **When** the panel opens, **Then** the list
   shows that reference rather than an empty choice.
3. **Given** an entity field holding `person.${person}`, **When** it is shown,
   **Then** suggestions come from the connection the current values name and
   the field shows what the reference resolves to.
4. **Given** a history graph, **When** its hours are set to `${range}`, **Then**
   the panel keeps the reference.

### User story 9: Switch with a tap (Priority: P2)

A Variable buttons widget on the mirror draws James and Sam as large buttons.
A tap switches that screen, or every screen when the author chose that and the
viewer may operate the dashboard.

**Why this priority**: A dropdown is awkward on a touch panel, and a tap is the
fallback when face detection picks the wrong person.
**Independent test**: Place the widget for `person`, tap a button on one
screen, and see only it switch; set it to every screen and see both switch;
open it anonymously on a public dashboard without public control and see the
every-screen press refused.

**Acceptance scenarios**:

1. **Given** a Variable buttons widget for `person` set to this screen, **When**
   a button is tapped, **Then** only that viewer changes, as a pick would.
2. **Given** the widget set to every screen, **When** a viewer who may operate
   the dashboard taps a button, **Then** the dashboard-wide value changes.
3. **Given** the same widget on a public dashboard without public control,
   **When** an anonymous viewer taps, **Then** the server refuses the change.

### User story 10: Template server-read settings (Priority: P3)

A news feed's URL names `${topic}`, and a calendar shows only the sources
tagged with `${person}`.

**Why this priority**: These are the settings a viewer's substitution cannot
reach, because the server or the plugin reads them itself.
**Independent test**: Tag two calendar sources, set the widget's tags to
`${person}`, and switch the variable; point a feed at a URL with `${topic}` and
switch it.

**Acceptance scenarios**:

1. **Given** calendar sources tagged `james` and `sam` and a widget showing
   tags `${person}`, **When** `person` is `sam`, **Then** only Sam's events
   show.
2. **Given** a news feed URL containing `${topic}`, **When** the viewer's
   `topic` changes, **Then** the server fetches the URL with that value
   substituted.
3. **Given** a feed request naming a value that is not an option, **When** it
   arrives, **Then** the server refuses it.

### User story 11: Pick a time range (Priority: P1)

An author adds a `range` variable of type time range displayed above the
dashboard. A viewer picks a preset such as "Last 24 hours" or types an absolute
span, steps it back, or zooms out, and every chart naming `${range}` follows.

**Why this priority**: Charts in feature 028 need a shared span, and a
time picker is how such dashboards are read.
**Independent test**: Define the variable with the default presets, pick one,
enter a custom absolute range, step back, open a `var-range=now-7d,now` link,
and send a flow command setting `now-1h,now`.

**Acceptance scenarios**:

1. **Given** a time range variable, **When** a viewer picks "Last 24 hours",
   **Then** its value becomes `now-24h,now` and the address records it.
2. **Given** a viewer typing a custom range, **When** both ends parse, **Then**
   the value is accepted even though no preset names it; when either end does
   not parse, it is refused in the picker.
3. **Given** a range of the last 6 hours, **When** the viewer steps back,
   **Then** the range becomes the 6 hours before it as absolute times; zooming
   out doubles the span around its centre.
4. **Given** a `variable` command naming the time range variable with
   `now-1h,now`, **When** it arrives, **Then** every unpinned viewer takes it.

### User story 12: Choose an interval (Priority: P2)

An author adds a `step` interval variable with `1m`, `5m`, `1h`, and `auto`, and
uses `${step}` in chart queries and as a chart's refresh.

**Why this priority**: Chart dashboards lean on interval variables for
aggregation windows and refresh pickers.
**Independent test**: Pick each option and inspect the substituted query; pick
`auto` with a 7-day range and see a step that yields about the configured
number of buckets.

**Acceptance scenarios**:

1. **Given** an interval variable with `auto` and 30 steps, **When** the bound
   range is 7 days, **Then** `auto` resolves to the nearest whole interval at or
   above 7 days divided by 30.
2. **Given** a value that is not one of its options, **When** a link or command
   names it, **Then** it is ignored or refused like any other option.

### User story 13: Take options from a time-series database (Priority: P2)

An author makes `host` a query variable over Prometheus
`label_values(node_uname_info, nodename)`, and `disk` a second one over
`label_values(node_filesystem_size_bytes{nodename="${host}"}, mountpoint)`
that depends on the first.

**Why this priority**: Database-backed option lists are what make one chart
serve every host, room, or meter a database knows about.
**Independent test**: Define both variables against a test Prometheus, switch
`host`, and see `disk` re-resolve to that host's mount points.

**Acceptance scenarios**:

1. **Given** a Prometheus label values query, **When** options resolve, **Then**
   they are the label's values, sorted and limited as for Home Assistant
   queries.
2. **Given** a variable whose query names `${host}`, **When** the viewer picks
   another host, **Then** its options re-resolve for that host and its value
   falls back to the first option when the old one is gone.
3. **Given** a SQL query returning `__value` and `__text` columns, **When**
   options resolve, **Then** each option takes its value and label from them.
4. **Given** an option value containing a double quote, **When** it is
   substituted into a PromQL string by `${host}`, **Then** it is escaped so the
   query keeps its structure; `${host:raw}` inserts it unchanged.

### Edge cases

- A dashboard holds at most 8 variables. A custom variable has 1 to 50 options;
  a query keeps at most 50 after sorting. Names are a letter followed by up to
  31 letters, digits, or underscores, and are unique in the dashboard. Values
  are 1 to 200 bytes and unique in the variable. Labels are at most 120 bytes
  and descriptions at most 500.
- A custom default that is not an option is refused. An empty default, or a
  query default that the query does not return, means the first option.
- A placement whose referenced variables combine into more than 256 value
  combinations grants nothing from its bindings or conditions, and validation
  reports it.
- A deployment that removes a variable or an option leaves live values that no
  longer match. Viewers ignore them and use the default.
- Substitution touches JSON string values only, never keys, numbers, or
  booleans. `$` without a following `{name}` is left alone, so prices and
  regular expressions are unaffected. A string that is exactly one reference
  whose value is a number or `true`/`false` becomes that number or boolean.
- A variable value reaches widgets as data, not markup.
- A query's results change as the house changes, and with them the grant.
  Resolved options are cached for 30 seconds, so a new entity becomes readable
  within that time.
- A time range value is two ends separated by a comma. Each end is `now`, `now`
  with offsets such as `-6h` and an optional rounding such as `/d`, an RFC 3339
  time, or Unix milliseconds. The start must be before the end and the span at
  most 10 years. A time range variable has up to 30 presets, each a label and a
  value.
- Time range values take no part in grants: they bound when data is read, not
  which data. They are therefore accepted without being presets, and a viewer
  may pass any value that parses.
- Interval values are durations such as `30s`, `5m`, `1h`, `1d`, or `auto`. An
  interval variable has 1 to 50 options.
- A query variable may reference only variables defined before it. A cycle is
  impossible by that rule, and a reference to a later variable is reported by
  validation.

## Requirements

### Functional requirements

- **FR-001**: Dashboard display settings MUST support variables, each with a
  name, label, description, type, display mode, and the type's settings, within
  the limits in the edge cases. Types are custom (listed options and a
  default), constant (one value), query, time range, and interval. Variables
  MUST be part of the
  draft, deployment snapshot, restore, diff, export, and import with the other
  display settings.
- **FR-002**: The viewer MUST replace `${name}` with a variable's current value
  and `${name:text}` with its option label in every string of a placement's
  configuration and in the source fields of its state conditions, before the
  widget receives them. A string that is exactly `${name}` whose value is a
  number or a boolean MUST become that type. A changed value MUST rebind the
  widget without reloading the page. A reference to an undefined variable MUST
  stay as written.
- **FR-003**: Dashboard scope MUST derive a placement's grants from every
  combination of option values of the variables it references, and only those,
  up to 256 combinations. Above that, the placement MUST grant nothing from its
  bindings or conditions. Time range variables MUST take no part in the
  combinations. A query variable that references earlier variables MUST
  contribute the union of its options over every combination of theirs, within
  the same limit.
- **FR-004**: The live display state MUST hold a dashboard-wide value per
  variable. A `variable` command MUST name a custom, query, time range, or
  interval variable of the served layout and a value matching one of its option
  values, or failing that one of its labels ignoring case. A time range
  variable MUST accept any value that parses as a time range. An empty value MUST return the variable to
  its default. Constants, followed variables, and anything else MUST be refused
  without changing state.
- **FR-005**: A viewer MUST start each variable from its `var-<name>` address
  parameter when that names an option, or for a time range when it parses, otherwise from the dashboard-wide value
  when that names an option, otherwise from the default. After that, the latest
  of the viewer's own pick and a dashboard-wide change MUST win, except for a
  variable the address pins with `pin`.
- **FR-006**: The viewer MUST draw a picker above the dashboard for variables
  displayed `above` or `label_hidden`, the latter without its label, and behind
  a menu button for those displayed `menu`. It MUST draw none for `hidden`
  variables and constants. A pick MUST change only that viewer and MUST record
  the value in the address without reloading. Renders MUST draw no picker.
- **FR-007**: Placements MUST support `variable` conditions comparing a
  variable's current value with `equals`, `not_equals`, or `contains`. They
  MUST grant nothing.
- **FR-008**: The `dashboard-command` node, REST, and MCP MUST offer the
  `variable` command. The node MUST accept `{{ }}` expressions in the value.
- **FR-009**: Render jobs created through REST and MCP, and the direct render
  address through `var-<name>` parameters, MUST accept variable values, MUST
  refuse a value the rendered layout does not offer, and MUST render with them.
- **FR-010**: Draft writes MUST refuse malformed variable definitions. Explicit
  validation MUST also report references to undefined variables, placements
  over the combination limit, and queries that return nothing or more than 50.
- **FR-011**: The editor MUST let an author define variables in the dashboard
  settings, grouped by display mode, reorder, duplicate, and delete them, see
  which placements use each, preview a query's options, and preview the canvas
  with any option value without changing the dashboard-wide value.
- **FR-012**: A query variable MUST take its options from a time-series
  connection as FR-021 states, or from a Home Assistant connection: either from
  the entities whose IDs match a `*` pattern (value the entity ID, label its
  friendly name) or from the `options` attribute of an `input_select` or
  `select` entity. An optional regex MUST extract the value,
  and the label when it has a `text` group, from each entity ID or option;
  items it does not match MUST be left out. Options MAY be sorted by label.
- **FR-013**: A query variable over an `input_select` or `select` entity MAY
  follow it. Its dashboard-wide value MUST then be the entity's state, and the
  layout MUST grant viewers a read of that entity.
- **FR-014**: Options MUST resolve on the server for scope, commands, renders,
  and validation, and MUST be served to viewers and editors with the view and
  edit access their layouts need. An unreachable source MUST resolve to no
  options.
- **FR-015**: The host MUST give widgets and their settings panels the
  dashboard's variables and the viewer's current values. Connection, control,
  and device lists in the core widgets' panels MUST offer each variable as
  `${name}` and MUST show a stored reference. Entity fields MUST suggest from
  the connection the current values name and MUST show what a reference
  resolves to. Numeric fields that accept a reference MUST keep it.
- **FR-016**: A `variable-buttons` widget MUST draw a variable's options as
  buttons. Set to this screen, a press MUST change only that viewer. Set to
  every screen, its placement MUST grant setting that variable, and the press
  MUST change the dashboard-wide value only for a caller the dashboard's control
  policy admits.
- **FR-017**: The news feed route MUST accept viewer variable values, MUST
  refuse any that are not options of the served variables, and MUST substitute
  them into the placement's served feed configuration before fetching.
- **FR-018**: Calendar sources MUST accept a tag. A calendar widget with a tag
  list MUST show only events from sources carrying one of those tags; the list
  may reference variables. Sources MUST be stored by the calendar plugin and not
  in the placement's config, and the panel MUST never receive a stored API key.

- **FR-019**: A time range variable MUST hold presets and a default, and the
  viewer MUST draw it as a time picker with its presets, a custom range entry
  for both ends, a step back and forward by the current span, and a zoom out.
  `${name}` MUST substitute the value, and `${name:from}` and `${name:to}` each
  end. Renders MUST accept a value through `var-<name>` and render jobs.
- **FR-020**: An interval variable MUST hold duration options and may hold
  `auto`, which MUST resolve to the nearest of 1s, 5s, 10s, 30s, 1m, 5m, 10m,
  30m, 1h, 3h, 6h, 12h, 1d, 7d, and 30d at or above the span of the time range
  variable it names divided by its step count, 30 by default, and never below
  its minimum interval.
- **FR-021**: A query variable over a time-series connection MUST take its
  options from Prometheus label values, metric names, or a query result's
  labels; from an InfluxDB query's first column; or from a SQL query's
  `__value` and `__text` columns, else its first and second columns. Its query
  MAY reference earlier variables and the time range macros of feature 027,
  evaluated over the default of the time range variable it names or the last
  6 hours. The viewer MUST re-resolve it when a referenced variable changes,
  and the server MUST resolve it with the viewer's values when checking that
  viewer's requests.
  Saving one whose query is new or changed through REST, MCP, or import, and
  previewing one, MUST require connection read, as a chart query does.
- **FR-022**: Substitution into time-series query text MUST escape a value for
  the query language's string literals by default: backslashes and double
  quotes for PromQL and Flux, backslashes and single quotes for InfluxQL, and
  single quotes for SQL. `${name:raw}` MUST insert it unchanged,
  `${name:regex}` MUST escape regular-expression metacharacters, and
  `${name:sqlstring}` MUST quote it as a SQL string.

### Key entities

- **Dashboard variable**: name, label, description, type, display mode; custom
  options and default, a constant's value, a query's source, connection,
  pattern, entity, or query text, regex, sort, and follow flag, a time range's
  presets and default, or an interval's options, step count, minimum, and
  range variable. Part of the dashboard
  display settings and versioned with them.
- **Resolved options**: a query's options at the time they were asked for,
  cached for 30 seconds.
- **Live variable value**: per dashboard and variable, in memory, alongside the
  other live display state.
- **Viewer variable value**: per open viewer, from its address, the
  dashboard-wide value, a followed entity, or its own pick.

## Success criteria

- **SC-001**: An anonymous viewer of a public dashboard with a two-option
  variable reads the entity each option names and is refused every other
  entity on that connection.
- **SC-002**: A variable command changes two open viewers and a newly opened
  one, checked in a browser.
- **SC-003**: A dashboard with no variables reports no draft changes after the
  upgrade.
- **SC-004**: A `var-` link opens on its value, and an invalid one falls back
  to the dashboard-wide value.
- **SC-005**: A followed `input_select` switches an open viewer when its state
  changes in Home Assistant, checked against a real helper.
- **SC-006**: A time range picker, a step back, and a `var-range` link each
  redraw a bound chart over the expected span, checked in a browser.
- **SC-007**: A dependent Prometheus query variable re-resolves when the
  variable it names changes, checked against a test Prometheus.

## Assumptions and scope

- Multi-value, "All", and custom values are out of scope: one field
  cannot hold several values without repeating panels, and a typed value is
  outside the grant.
- Ad hoc filters are out of scope; they rewrite queries with
  viewer-chosen filters, which is outside the option-bounded grant. Time range
  and interval variables are in scope for the charts of feature 028. The
  history graph of feature 024 keeps its own `hours` setting.
- The news feed route substitutes viewer values into feed URLs, and the
  calendar filters sources by tag. Other configuration a plugin reads itself is
  not substituted; use a variable condition to switch between placements for
  it.
- Live variable values belong to the server process like the rest of the live
  display state, and are lost on restart. A followed variable's value is the
  entity's state and survives.
- Render schedules do not carry variable values.
- Query options refresh when a viewer loads or reconnects. A followed entity's
  state is live.

## References

- [Design plan](plan.md)
- [Dashboard display control](../023-dashboard-display-control/spec.md)
- [Time-series connections](../027-timeseries-connections/spec.md)
- [Dashboard charts](../028-dashboard-charts/spec.md)
- [Dashboard access and rendering](../007-dashboard-access-rendering/spec.md)
- [docs/dashboard-authoring.md](../../docs/dashboard-authoring.md)
- [docs/dashboard-security.md](../../docs/dashboard-security.md)
- [docs/plugins.md](../../docs/plugins.md)
