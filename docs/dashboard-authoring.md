# Authoring dashboards

Read `get_dashboard` with `view: "draft"` before editing. Widget placements and
sections have their own UUIDs; a widget package name identifies its type, not a
placement. Use `list_widget_types` for the installed packages. A node type is a
flow component, so `get_node_type` does not describe dashboard widgets.

## The dashboard list

The dashboard list at `/dashboards` updates without a reload. Dashboards and
folders that are added, renamed, moved, shared, or removed in another tab, by
another user, or through MCP appear as soon as they change.

## Addresses

A dashboard's ID is its address, and the app links by ID. Slugs are readable
aliases and need not be unique. `/dashboard/kitchen` opens the one dashboard
with that slug the viewer can see, counting public ones for anonymous viewers,
and shows a choice when several match. Over the API, a slug that several
visible dashboards share answers `AMBIGUOUS` with the matches by ID, and
`GET /api/dashboards/by_slug/{slug}` lists them. A Dashboard Command node or
notification naming a dashboard by slug resolves it only when exactly one
dashboard uses that slug, so name dashboards by ID there.

## Layout and themes

`update_dashboard` patches the name, description, grid, spacing, or theme.
Choose a numeric `theme_id` from `list_themes`; zero clears it. Themes apply to
the dashboard, not through a widget's config. Leave sharing settings as they
are.

`layout_mode: "fit"` scales `grid_cols` and `grid_rows` to the viewport.
`layout_mode: "scroll"` uses `row_height` in pixels and supports sections.
`layout_mode: "regions"` anchors placements to named screen regions; see
[Region layout](#region-layout).
Use `add_dashboard_section` to append a section, `update_dashboard_section`
to change its title, collapsed default, or `sort_order`, and
`remove_dashboard_section` to delete it and its widgets. Move widgets out first
if they should survive. Switching from scroll to fit flattens the sections.

`add_dashboard_widget` takes a package name, dashboard ID, optional section ID,
`x`, `y`, `width`, `height`, and a `config` object. Positions and sizes are grid
units. Positions start at zero; sizes must be positive. Y is relative to the
section, and an omitted section ID means the sectionless region. Keep widgets
inside the grid and avoid overlaps. On a 12-column grid, a clock at x=0, y=0,
width=4, height=2 is a good first size.

`update_dashboard_widget` patches placement fields. An empty `section_id` moves
the placement to the sectionless region. Its `config` replaces the entire
configuration, so merge changes into the current object before calling it, and
pass the placement's `config_revision` as `expected_config_revision`. If someone
replaced the config since you read it, the write fails with a conflict; read it
again and reapply your change. `update_dashboard` takes
`expected_display_revision` the same way for its `display` object, which holds
the variables and screen settings. `get_dashboard` returns stored configs as
JSON strings; parse them before editing and pass an object back. Do not save
the `dashboardId` or `dashboardWidgetId` fields the browser injects.

## Region layout

The region layout anchors placements to parts of the screen. A placement names
a `region` instead of a grid cell, and each region stacks its placements by
`y`, then `x`. A placement is `height` rows of `row_height` pixels tall and
`width` of `grid_cols` of its region wide. Left regions align narrower
placements to the start, right regions to the end, and the rest center them.

| Region | Where it draws |
| --- | --- |
| `top_bar`, `bottom_bar` | Full width at the top and bottom |
| `top_left`, `top_center`, `top_right` | Three columns under the top bar |
| `upper_third`, `middle_center`, `lower_third` | The space between, split three ways; they align to its top, middle, and bottom |
| `bottom_left`, `bottom_center`, `bottom_right` | Three columns over the bottom bar |
| `fullscreen_below`, `fullscreen_above` | The whole viewport, behind or in front of every other region |

A placement with no region draws in `middle_center`. The region layout ignores
`x` positions and sections but keeps them, so switching back to fit or scroll
restores the grid. In the editor, click a region's outline to choose where new
widgets land, and use the Display panel to move a widget between regions or up
and down in one. For a mirror, the `mirror` theme draws black, borderless tiles.

## Pages

Pages divide a dashboard into views that take turns. `add_dashboard_page`,
`update_dashboard_page`, and `remove_dashboard_page` manage them. A
placement's `page_id` puts it on one page, and an empty `page_id` shows it on
every page, like a clock that stays put. Removing a page moves its placements
to every page rather than deleting them. Pages are part of the draft and
deploy with the layout.

`display.page_interval` rotates pages every 5 to 3600 seconds; 0 leaves the
page until a viewer or a command changes it. Each viewer runs its own timer, so
two panels on one dashboard need not flip together. A page command restarts it.

## Display settings

`update_dashboard` takes a `display` object, which replaces the stored one; an
empty object clears it. It deploys with the layout.

| Field | Meaning |
| --- | --- |
| `page_interval` | Seconds between automatic page changes, 0 or 5 to 3600 |
| `night_dim` | `{from, to, level}`. Dims to `level` percent between two `HH:MM` times in the viewer's clock; a window past midnight is allowed |
| `idle_dim` | `{after, level}`. Dims to `level` percent after `after` seconds without a touch, key, or wheel |
| `pixel_shift` | Moves the whole surface by up to this many pixels, 0 to 16, every two minutes to spread burn-in |
| `variables` | Named values placements are templated on; see [Variables](#variables) |

The darker of the night and idle levels applies. A `dim` command replaces both
until `undim`. A touch, key, or wheel lifts dimming on that viewer for the idle
timeout, or 30 seconds without one, and the first tap on a dimmed screen only
wakes it. Renders ignore dimming, idle, pixel shift, rotation, and reload.

## Placement display

A placement's `display` object holds how the dashboard shows it, separate from
the widget's own `config`. `update_dashboard_widget` replaces it whole; an
empty object clears it.

| Field | Meaning |
| --- | --- |
| `name` | Label shown in pickers, such as the Dashboard Command node's widget list |
| `hidden` | Start hidden under the lock `start`; see [Commanding a live dashboard](#commanding-a-live-dashboard) |
| `conditions`, `match` | Show only while `all` (default) or `any` conditions pass |
| `animate_in`, `animate_out`, `duration` | `none`, `fade`, `slide-up`, `slide-down`, `slide-left`, `slide-right`, or `zoom`, for up to 5000 ms; unset fades for 300 ms |
| `style` | Tile look overriding the theme; see [Tile style](#tile-style) |
| `cues` | Animated borders drawn while their conditions pass; see [Status cues](#status-cues) |

A state condition names a source with the binding fields widgets use and
compares one of its values:

```json
{"kind":"state","source":"ha","connectionId":"<uuid>","entityId":"binary_sensor.garage_door",
 "attribute":"","operator":"equals","value":"on"}
```

`source` is `ha`, `control`, or `mqtt` (with `controlId` or `deviceId`).
`attribute` reads a Home Assistant attribute, a composite control's attribute,
or a device state field. Empty compares the state, the single value, or a
composite control's primary attribute, such as a location's `state` or a
thermostat's `mode`.
`operator` is `equals`, `not_equals`, `contains` (all ignoring case), `above`,
or `below` (numeric). A source that has not answered never matches, so the
placement stays hidden while the page loads. Viewers may read a condition's
source as they may a display-only row.

A time condition is `{"kind":"time","from":"22:00","to":"06:00","days":[5,6]}`
in the viewer's clock. `days` are 0 (Sunday) to 6 and name the day the window
starts; empty means every day. A window whose start equals its end covers the
whole day.

A variable condition is `{"kind":"variable","variable":"person","operator":"equals",
"value":"sam"}` and compares the viewer's current value of that variable with
`equals`, `not_equals`, or `contains`. It reads no source and grants nothing.

Hidden placements keep their place in the grid layouts and give it up in the
region layout. They stay loaded, so a page flip does not reload them.

### Tile style

`style` changes one placement's tile without a theme. The dashboard applies it
to the cell around the widget, so it works for every package:

```json
{"style":{"background":"#1e3a5f","border_color":"#ff8800","border_width":3,
          "radius":0,"text_color":"#ffe08a","accent_color":""}}
```

| Field | Meaning |
| --- | --- |
| `background` | A CSS color, or `transparent` to drop the tile's face, border, and shadow, which suits a mirror |
| `border_color`, `border_width` | A border of 0 to 16 pixels; a color alone draws 1 pixel |
| `radius` | Corner radius of 0 to 64 pixels; unset keeps the theme's, 0 is square |
| `text_color`, `accent_color` | The colors widgets that follow the theme use for text and accents |

Empty fields follow the theme, and an empty style is stored as no style.
Colors are CSS color strings of up to 64 bytes. The server refuses a value
holding `;`, braces, angle brackets, quotes, a backslash, or `url(`.

### Status cues

`cues` draw an animated border around the tile while something needs
attention. Each cue has `conditions` and `match` in the same forms as
visibility conditions, a `color`, and an `animation`, which is `static`
(default), `pulse`, `blink`, `breathe`, `comet`, or `ants`. The first cue whose
conditions pass draws, so order them from most to least urgent. A cue with no
conditions always draws.

```json
{"cues":[
  {"color":"#f38ba8","animation":"blink","conditions":[
     {"kind":"state","source":"ha","connectionId":"<uuid>","entityId":"cover.garage","operator":"equals","value":"open"},
     {"kind":"time","from":"22:00","to":"06:00"}]},
  {"color":"#f9e2af","animation":"breathe","conditions":[
     {"kind":"state","source":"ha","connectionId":"<uuid>","entityId":"cover.garage","operator":"equals","value":"open"}]}]}
```

A placement holds at most 8 cues. The dashboard's viewers may read a cue's
state sources, as they may a visibility condition's, and never write them. A
condition whose source has not answered does not pass, so a border does not
flash while the page loads. Viewers that ask for reduced motion, and renders,
draw the cue's color without motion. The editor canvas draws cues live.

## Variables

Variables are named values placements are templated on. Widgets refer to one in
their configuration, a viewer picks a value, and a flow or a Home Assistant
helper can set it for everyone. A mirror can switch between two people's
calendars, commutes, and sensors this way, with one widget for each instead of
one per person.

```json
{"variables":[
  {"name":"person","label":"Person","type":"query",
   "query":{"source":"ha_entities","connection_id":"<uuid>","pattern":"person.*",
            "regex":"^person\\.(?<value>.+)$"}},
  {"name":"range","label":"History","display":"menu","default":"24",
   "options":[{"value":"6","label":"6 hours"},{"value":"24","label":"24 hours"}]},
  {"name":"ha","type":"constant","value":"<uuid>"}]}
```

`variables` lives in the dashboard's `display` object and deploys with it. A
dashboard has at most 8. A name is a letter followed by up to 31 letters,
digits, or underscores. Every variable takes an optional `label`, a
`description` shown as its picker's tooltip, and a `display`:

| `display` | Picker |
| --- | --- |
| `above` (default) | In a bar above the dashboard, with its label |
| `label_hidden` | In that bar without its label |
| `menu` | Behind a button in the top right corner, which suits a mirror or wall panel |
| `hidden` | None; only links, commands, and followed helpers set it |

### Types

| `type` | Values |
| --- | --- |
| `custom` (default) | `options`, 1 to 50 of `{value, label}`, with unique values of up to 200 bytes. `default` must be one of them; empty means the first |
| `constant` | `value`, never shown and never changed by a command. Use it for something many widgets repeat, such as a connection ID written as `${ha}` |
| `query` | `query`, resolved against Home Assistant or a time-series connection |
| `time_range` | A span `from,to` that charts read. `options` are presets of `{value, label}`, up to 30, and the built-in quick ranges when empty. `default` is a range, `now-6h,now` when empty. Any value that parses is accepted, from a picker, link, or command |
| `interval` | `options` are durations such as `1m`, `5m`, `1h`. With `auto: true` an `auto` option resolves to the nearest of 1s to 30d at or above the `range_variable`'s span divided by `steps` (30), never below `min_interval` |

A query's `source` is `ha_entities` or `ha_select`, and `connection_id` names
the Home Assistant connection.

- `ha_entities` lists the entities whose IDs match `pattern`, where `*` stands
  for any run of characters and other characters are literal. The value is the
  entity ID and the label its friendly name.
- `ha_select` lists the `options` attribute of `entity_id`, an `input_select`
  or `select`. With `follow: true` the dashboard-wide value is that entity's
  state, so a Home Assistant automation that sets the helper switches every
  viewer, and the server refuses `variable` commands. Viewers may read the
  helper's state.

Time-series sources read a Prometheus, InfluxDB, or PostgreSQL connection; see
[timeseries.md](timeseries.md). Saving or previewing a new or changed
time-series query needs `api:connections:read`, as a chart query does.

| `source` | Options |
| --- | --- |
| `prometheus_label_values` | Values of `label`, optionally of the series `match` selects, such as `up{job="node"}` |
| `prometheus_metrics` | Metric names, optionally of `match` |
| `prometheus_query` | An instant query `text`; each result's `label` value, or its series name |
| `influx_query` | The first column of `text` in `language` `influxql` (default), `flux`, or `sql` |
| `sql_query` | `__value` and `__text` columns of `text`, else the first and second |

Their `text` and `match` may refer to variables defined earlier, such as
`label_values(node_filesystem_size_bytes{nodename="${host}"}, mountpoint)` as
a `prometheus_label_values` with `match`. A viewer's pick re-resolves the
variables that refer to it, and a value no longer offered falls back to the
default. Time macros use the `range_variable`'s value, or the last 6 hours.
Viewers may read what a chained query offers for every value of the variables
it refers to.

An optional `regex` extracts the value from each entity ID or option. The value
is its `value` group, else its first group, else the whole match, and a `text`
group becomes the label. The query drops items the regex does not match. `sort`
is empty for Home Assistant's order, or `asc` or `desc` by label. A query keeps
at most 50 options. A `default` the query does not return means the first
option.

Viewers, commands, renders, and validation all see the same options, which
`GET /api/dashboards/{id}/variables` returns. A new Home Assistant entity can
take up to 30 seconds to appear among a query's options. A query that cannot
reach Home Assistant has no options. Viewers refresh options when they load or
reconnect; a followed helper's state is live. `resolve_dashboard_variables`
returns the resolution and any query errors, and the editor's **Run query**
previews an unsaved query.

### References

In any string of a placement's `config`, `${person}` becomes the current value
and `${person:text}` its label. That covers binding fields, as in
`"entityId":"sensor.${person}_steps"`, and text, as in
`"name":"${person:text}'s day"`. The source and comparison fields of state
conditions take references too. Keys do not, and a reference to a variable the
dashboard does not define stays as written. A string that is exactly one
reference whose value is a number or `true`/`false` becomes that type, so
`"hours":"${range}"` reaches a history graph as a number. Widgets receive the
substituted configuration and rebind when a value changes, without reloading.

A time range's ends are `${range:from}` and `${range:to}`. In chart query text,
a plain `${name}` is escaped for the language's string literals, so a value
cannot change the query. PromQL and Flux escape backslashes and double quotes,
InfluxQL escapes backslashes and single quotes, and SQL escapes single quotes.
Write the quotes yourself, as in `{host="${host}"}`. `${name:raw}` inserts the
value unchanged, `${name:regex}` escapes regular-expression metacharacters, and
`${name:sqlstring}` writes a quoted SQL string. Outside query text these
formats insert the plain value.

Time range values grant nothing. They decide when data is read, not which data.
A time range picker offers the presets, a custom range for both ends, a step
back and forward by the current span, and a zoom out. Links carry the range as
`var-range=now-7d,now`, and dragging across a chart sets it as absolute Unix
milliseconds.

Viewers may read what each option's substitution binds, and nothing else. A
placement whose variables combine into more than 256 value combinations grants
none of its bindings. `validate_dashboard` reports that, references to
undefined variables, and queries that fail, return nothing, or are truncated.
It checks each templated config against the widget's schema with every
variable at its default. See [what a dashboard exposes](dashboard-security.md).

### Values

Each viewer holds its own value of each variable. It starts from the address
parameter `var-<name>`, as in `/dashboard/mirror?var-person=sam`, when that
names an option; otherwise from the dashboard-wide value, which a followed
helper or a `variable` command sets; otherwise from the default. After that the
newer of the viewer's own pick and a dashboard-wide change wins. `pin=person`
in the address keeps that viewer's value whatever the dashboard-wide value
does, and `pin=*` pins every variable, so a kitchen panel opened as
`?var-person=sam&pin=person` stays on Sam. A pick changes only that viewer and
records it in the address, so the link opens on it. The server refuses a
command naming a value that is not an option.

### Editing

The Variables tab of the editor's settings dialog lists variables grouped by
where their picker is drawn. Select one to edit it; reorder it within its
group, duplicate, or delete it. Custom options are one per line, written as
`value | label`. A value that holds a bar, such as `web|api`, needs the spaces
around the separator and a label, as in `web|api | Front ends`. Each variable
shows which widgets use it. The toolbar's **Preview** button picks values for
the canvas without changing what viewers see.

Widget settings panels know the dashboard's variables. Connection, control, and
device lists offer each as `${name}` below the real choices and show a stored
reference as itself. An entity field takes a template such as
`person.${person}`, suggests entities from the connection the current values
name, and shows what the reference stands for, as in "Now person.james". The
history graph's hours accept a reference such as `${range}`.

### Widgets that use variables

- **Variable Buttons** (`variable-buttons`) draws a variable's options as
  buttons, for a touch screen where a dropdown is awkward. `variable` names it.
  `scope` is `screen` (default), which switches only that viewer like a pick,
  or `dashboard`, which sets the dashboard-wide value like a Dashboard Command.
  A dashboard-scope press needs the dashboard's control policy, which allows
  people you shared it with at view level or above, and anyone when public
  control is on. `layout` is `row`, `column`, or `grid`; `size` is `small`,
  `medium`, `large`, or `fit`; and `showLabel` draws the variable's label.
- **News Feed** URLs may name variables, as in
  `http://example.com/${topic}.xml`. The server reads the feed URLs itself, so
  the widget sends the viewer's values and the server substitutes only values
  that are options.
- **Calendar** sources take a tag in the widget's panel, such as `james`.
  `showTags` lists the tags to show, comma separated, and may name a variable,
  so `"showTags": "${person}"` shows one person's calendars. Empty shows every
  source.

Karakuri does not substitute other configuration a plugin reads on the server.
Place one widget per value and show each with a variable condition instead.

Renders take variable values as `variables` on `render_dashboard` and
`POST /api/dashboards/{id}/renders`, or as `var-<name>` parameters on the
direct render address. The render refuses a value the rendered layout does not
offer, and any value for a constant. Schedules render with the dashboard-wide
values.

## Commanding a live dashboard

Commands change what open viewers show without touching the draft or a
deployment. They come from the Dashboard Command flow node, MCP
`command_dashboard`, `POST /api/dashboards/{id}/commands`, and Karakuri
dashboard notification endpoints. Read the resulting state with
`get_dashboard_live` or `GET /api/dashboards/{id}/live`, and follow it on the
`dashboard:{id}:live` topic.

| Action | Fields | Effect |
| --- | --- | --- |
| `hide` | `targets`, `lock` | Add the lock (default `command`) to each placement |
| `show` | `targets`, `lock`, `force` | Release the lock; `force` releases every lock, including `start` |
| `page` | `page` | Go to a page by ID, title, or 1-based number; empty returns to the first |
| `next_page`, `previous_page` | | Step every viewer from the page it is on |
| `alert` | `title`, `message`, `level`, `style`, `duration`, `speak` | Show a `toast` or `fullscreen` alert for `duration` seconds (default 10, 0 until cleared); `speak` reads it aloud on viewers open when it arrives |
| `clear_alert` | `alert_id` | Remove one alert, or all of them |
| `dim`, `undim` | `dim_level` | Override the dim level (default 100), or return to the schedule |
| `reload` | | Reload every viewer's page |
| `content` | `targets`, `value`, `format` | Push a value into placements for widgets that show it; no value clears it |
| `variable` | `variable`, `value` | Set a variable's value for every viewer; `value` is an option value or label, and an empty string returns to the default. Constants and followed variables are refused |

A placement is visible only when it holds no locks, its conditions pass, and it
is on the current page. Locks let several automations hide one widget without
undoing each other. The widget reappears only after each has released its own
lock. Targets must be placements of the served layout, which is the deployed
version, or the draft of a dashboard never deployed.

The server holds this state in memory and does not save it. A restart returns
every dashboard to its authored start: no locks beyond `start`, no alerts, no
dim override, no pushed content, every variable at its default, and the first
page. The server keeps at most five alerts and drops the oldest. Alert titles
hold up to 200 bytes, messages up to 4 KiB, and pushed content up to 64 KiB of
JSON.

Content `format` is `text`, `markdown`, or `json`. The server renders Markdown
strings and escapes raw HTML in them.

## Widget configuration

Use a package's `config_schema` and `default_config` when it has them. This
section describes the bundled packages. Unspecified fields take the widget's
defaults. Preserve unknown existing fields, and do not invent configuration
keys for other plugins. Place a widget with `{}` to take every default, and
configure it in its panel afterward.

### Digital clock

Package `digital-clock` draws the time. It takes:

- `timezone`: an IANA timezone, or an empty string for local time.
- `format`: `24h` or `12h`; default `24h`.
- `showSeconds`: boolean, default true.
- `fontStyle`: `monospace`, `sans`, or `segmented`; default `monospace`.
- `color`: CSS color, or an empty string to follow the theme.
- `layout`: `standard-inline`, `stacked-vertical`, `big-date`, `world-list`,
  `retro-lcd`, `time-progress`, or `sidebar-date`.
- `worldTimezones`: array of `{timezone, label}` for the world-list layout.

For example:

```json
{"timezone":"America/Chicago","format":"12h","showSeconds":false,"layout":"big-date"}
```

### Entity control and entities card

Package `entity-control` draws one binding. It takes the binding plus
`showName` and `showState` booleans, both default true. Package `entities-card`
draws a list of bindings. It takes `{"title":"Room","rows":[...]}`, where each
row is a binding with a string `id` unique within that card.

A binding names its source and the identifiers belonging to it:

| source | Required binding fields | Discovery tool |
| --- | --- | --- |
| `control` | `controlId` | `list_controls` |
| `ha` | `connectionId`, `entityId` | `list_connections`, `get_ha_states` |
| `mqtt` | `deviceId` | `list_mqtt_devices` |
| `flow-trigger` | `flowId`, `trigger`, or `triggers` entries | `list_flows`, then inspect the flow's widget triggers |
| `nvr` | `connectionId`, `camera` (`*` for every camera), display only | `list_connections`, `list_nvr_cameras` |

Optional `name` overrides the source label. `icon` is a Line Awesome class such
as `la-lightbulb`. `displayOnly: true` prevents operation; false lets the widget
render the control its source supports. The source decides whether that is a
switch, slider, button, or another control, so do not invent a `control` field
to choose it.

Home Assistant `person` and `device_tracker` entities, location controls, and
MQTT devices of kind location draw as a location. The widget shows the zone's
label (`home` as Home, `not_home` as Away, any other zone as its name), how
long it has been there, and, when shown, coordinates with accuracy. Home
Assistant locations are read-only. A location control with `manual` on draws
its zones as a picker. `showCoordinates: true` on a binding shows the
coordinates whatever the source's own setting, and is the only coordinate
setting a Home Assistant entity has.

openHAB items reach dashboards as devices. Add them under **Settings,
Devices, Add from openHAB** and bind rows with `source: "mqtt"` and the
device's `deviceId`.

The `ha` entity picker groups entities under floor and area headings and
searches area, device, and label names. The entities card config's **Add an
area** appends a row for each of an area's entities, leaving out hidden,
diagnostic, and configuration entities and those already on the card. The
card does not follow the area afterward.

Home Assistant `media_player` entities, media controls, and devices of kind
media draw as a player. A card row shows the title or state, play and pause,
and the volume. An Entity Control shows title, artist, progress, previous,
next, stop, mute, volume, source, and power, each only when the player has the
feature. A Home Assistant player's features come from its `supported_features`,
and progress advances while it plays. Art shows in an Entity Control only from
an absolute URL; the Now Playing widget relays a Home Assistant player's own
art.

A flow-trigger binding may use `triggers`, an array of
`{id, label, flowId, trigger, payload, payloadFormat, confirmFirst}`. Payload is
a string and `payloadFormat` is `json` or `yaml`. These bindings describe
buttons; editing the dashboard does not press them.

### States and value formats

Entity Control, each Entities Card row, and Button take `states`, an ordered
list of up to 16 looks the binding takes on according to its reading:

```json
{"source":"control","controlId":"<uuid>","icon":"la-microphone",
 "states":[{"operator":"equals","value":"on","icon":"la-microphone-slash",
            "background":"#c0392b","color":"#ffffff","iconColor":"#ffffff","label":"Muted"},
           {"label":"Live"}]}
```

A state compares the binding's own reading with `operator` (`equals`,
`not_equals`, `contains`, `above`, `below`) and `value`. `attribute` compares
one part of a composite control, such as a light's `brightness`, and empty
compares its main value. A state with no operator always matches, which makes
it a fallback last entry. Text comparisons ignore case, a switch reads as `on`
or `off` (`true` and `false` work too), and `above` and `below` fail on
anything that is not a number. The first match sets any of `icon`,
`iconColor`, `background`, `color` (text), and `label`; fields it leaves unset
keep the binding's look. A label may hold `{value}`, the formatted reading, and
`{name}`. No state applies while the source is unavailable or has not
answered. States read only the binding, so they grant nothing beyond it. To
style a tile from another source, use a [status cue](#status-cues).

`format` draws a numeric reading as `duration` (seconds as `3:07`, `3:07:42`,
or `1d 1:01:01`), `bytes` (1536 as `1.5 KB`, in 1024 steps), `bytes_per_second`
(`3.5 MB/s`), `percent` (`12.5 %`), or `number`, which keeps the unit.
`precision` sets the decimals of `number` and `percent` and overrides the ones
the source reports. A reading that is not a number draws unchanged.

### Button

Package `button` draws one binding as a square button with a large icon, the
label, and with `showValue` the formatted reading. It takes the binding fields
above, `states`, `format`, `precision`, `showLabel` (default true),
`showValue` (default false), and `confirmFirst`.

Pressing anywhere on it sends the binding's single action. A switch, light, or
fan toggles; a lock locks or unlocks; a scene, script, button entity, or
buttons control with one action runs; a flow trigger fires its first press.
Numbers, lists, text, display-only bindings, and viewers without the
dashboard's control policy send nothing, and the button shows its reading.
With `confirmFirst`, or a trigger press's own `confirmFirst`, the first press
shows "Tap again" for 3 seconds and only a second press sends. A failure
flashes the border and names the reason on hover.

### Image and video

Package `image` draws images or a slideshow. It takes `images`, an array of URL
entries `{type: "url", url, label}`. The default `layout` is `static`, `fit` is
`cover`, `refreshInterval` is 0, `slideInterval` is 5, and `transition` is
`fade`. Preserve existing uploaded-image entries when editing a list.

Package `video` plays a video. It takes a `source` URL, `layout: "player"`,
`sizing: "contain"`, and `autoplay`, `loop`, and `mute` booleans, all default
false. Upload files through the widget's panel.

### Weather

Package `weather` draws a forecast. It takes `latitude`, `longitude`, and
`locationName`. It defaults to `provider: "openmeteo"`, which needs no API key.
`openweathermap` needs a configured plugin key with access to its forecast
API. The panel and renderer use the same provider default when it is omitted.

Use `backgroundStyle: "image"` for a built-in photo chosen from the current
weather, `gradient` for a weather gradient, or `none` (default). Do not stack
an image widget under the weather widget for its background. `iconTheme` is
`static` (default) or `animated`; `units` is `metric` (default) or `imperial`;
and `language` defaults to `en`. `list_widget_types` lists the supported
layouts and the rest of the configuration.

For full-screen weather, use one weather widget covering the fit grid and set
`padding`, `gap`, and `widget_padding` to zero. For example:

```json
{"latitude":34.7304,"longitude":-86.5846,"locationName":"Huntsville, AL",
 "provider":"openmeteo","units":"imperial","layout":"current",
 "backgroundStyle":"image","iconTheme":"static"}
```

For an ESP32 image display, preview at the display's actual pixel dimensions. If
those are unknown, ask before setting up a render schedule. Editing the
dashboard does not deploy it or configure the device.

Changing provider, coordinates, units, or language clears the previous forecast,
so the widget never shows the old selection's data. A failed refresh for the
same selection keeps its last forecast with a visible error and Retry control.

Local station readings reach the widget through a flow's Weather Override node,
not through the widget. A forecast whose coordinates lie within an override's
radius shows the live readings in `current` and lists them under `override`,
with when each was set and when it expires. An override's change or expiry
refetches open widgets nearby.

### Weather air quality

The weather widget's `air-quality` layout shows the air quality index with its
category, the next 24 hours, particulates and gases, and pollen where
Open-Meteo publishes it (Europe). `aqiScale` is `us` (default) or `european`.
The widget reads Open-Meteo's free air-quality API alongside the forecast.
OpenWeatherMap forecasts carry no air quality, and a failed air-quality fetch
leaves the forecast intact.

### Rotating message, flow text, and news feed

Package `rotating-message` shows one line at a time. It takes `lists`, an
object of string arrays. Keys `morning`, `afternoon`, `evening`, and `anytime`
follow `morningStart` (3), `morningEnd` (12), `afternoonStart` (12), and
`afternoonEnd` (17). A `YYYY-MM-DD` key with `.` as a wildcard, such as
`....-12-25`, applies on matching dates, and a five-field cron key applies in
matching minutes. Any other key matches the value of the optional `weather`
binding, such as a Home Assistant `weather.home` entity reporting `rainy`;
write the binding with `displayOnly: true`. `specialDayUnique` shows only the
date and cron lines when one matches. `interval` is seconds between lines and
`fade` the cross-fade in milliseconds. A `content` command with a string or a
list of strings replaces the lines until it is cleared.

Package `flow-text` shows the content a flow pushes, as text, Markdown, or
formatted JSON. It takes an optional `title`, and `placeholder` shows until
content arrives. The widget removes Markdown images from other origins. A list
of strings shows as lines. A list of objects shows as a table whose columns are
the objects' keys. A flow's objects arrive with their keys sorted, so set
`columns` to choose and order them, and `columnLabels` (`{"key":"Header"}`) to
rename them. `maxRows` caps the rows, 0 for all.

Package `news-feed` shows headlines. It takes up to five `feeds`
(`[{"url":"https://example.com/rss","title":"Local"}]`). The server fetches the
URLs saved in the placement and caches each for five minutes; the widget never
sends a URL. `layout` is `ticker` (one headline) or `list` (`listSize`
headlines). `interval`, `reload` (minutes), `maxItems`, `maxAgeHours`,
`showDescription`, `showSource`, and `showTime` tune it. A `content` command
with `next`, `previous`, or `details` steps or expands the headline.

### Camera, media, list, and graph widgets

These packages bind one Home Assistant entity, NVR camera, or media player, or
several entities for the graph, with the binding fields in
[Entity control and entities card](#entity-control-and-entities-card). Viewers
may read only what they bind.

| Package | Binding | Fields |
| --- | --- | --- |
| `camera` | A `camera.` entity, or an `nvr` camera with `source: "nvr"` and `camera`, at the config root, display only | `mode` `live` (relayed MJPEG stream, default) or `snapshot`; `interval` seconds between snapshots (10); `fit` `cover` or `contain`; `showName`; `bbox` draws Frigate's object boxes for an NVR camera |
| `nvr-events` | An `nvr` binding at the root; `camera` `*` lists every camera of the connection | `labels` and `zones` (comma separated), `limit` (1 to 100, default 10), `showThumbnails`. Tapping an event plays its clip |
| `media-player` | One binding at the root: a `media_player.` entity (`source: "ha"`), a media control (`source: "control"`, `controlId`), or a device of kind media (`source: "mqtt"`, `deviceId`) | `layout` `card` or `compact`; `showArt`, `showControls`, `showVolume`. Controls follow the player's features and need a writable binding and the dashboard's control policy |
| `todo-list` | A `todo.` entity at the root | `title` (empty uses the list's name), `showCompleted`, `allowAdd`, `maxItems` (0 for all). Ticking and adding need a writable binding and the control policy |
| `activity` | `rows`, a list of `control`, `mqtt`, or `ha` bindings with `id` and `displayOnly: true` | `categories` and `levels` (comma separated), `limit` (1 to 50, default 10), `showActor`. Lists the event log entries of the bound subjects live; never security entries. See [events.md](events.md) |
| `history-graph` | `series`, a list of rows with `id`, `source`, the source's IDs, `name`, `color`, and `displayOnly: true` | `hours` (1 to 168, default 24), `style` `line`, `area`, or `step`, `min`, `max`, `showLegend`, `showCurrent` |

A live Home Assistant camera keeps one stream open per viewing browser; an NVR
camera shares one stream from Frigate among every viewer at the same height.
Both fall back to snapshots while the stream reconnects. [frigate.md](frigate.md)
has the NVR details. Karakuri relays cover art that Home Assistant hosts; art on
another host loads from that host. `ha` series take their history from Home
Assistant's recorder. Control and MQTT series keep no history and start when
the page loads. Numbers plot directly, and `on`/`off` and `true`/`false` plot
as 1 and 0.

### Charts

Package `chart` draws the results of time-series queries.
[timeseries.md](timeseries.md) covers connections and query macros. For
example:

```json
{"viz":"timeseries","timeRange":"${range}","refresh":"30s",
 "queries":[{"refId":"A","connectionId":"<uuid>","language":"influxql",
   "text":"SELECT mean(\"value\") FROM \"temperature\" WHERE $timeFilter AND \"room\" = '${room}' GROUP BY time($__interval)",
   "legendFormat":"{{room}}"}],
 "fieldConfig":{"defaults":{"unit":"celsius","decimals":1},"overrides":[]},
 "transformations":[],"options":{"legend":{"displayMode":"table","calcs":["mean","max"]}}}
```

- `viz` is `timeseries`, `trend`, `barchart`, `stat`, `gauge`, `bargauge`,
  `table`, `piechart`, `state-timeline`, `status-history`, `heatmap`,
  `histogram`, `candlestick`, or `xychart`.
- `queries` holds 1 to 10 queries. Each has `refId`, `connectionId`,
  `language` (`promql`, `influxql`, `flux`, `sql`, `ha_history`,
  `ha_statistics`, `energy`), `text`, `ha` (`entities`, `attribute`,
  `statistic_ids`, `period`, `types`), or, on connection `energy`, `energy`
  (`targets`, `measure`, `period`; [energy.md](energy.md) lists them),
  `legendFormat`, `maxDataPoints` (default the chart's width in pixels),
  `minInterval`, `instant`, `format` (`time_series` or `table`), and `hide`,
  which keeps a query for transformations without drawing it.
- `timeRange` is `from,to` or a time range variable reference; empty is the
  last 6 hours. `relativeTime` (such as `1h`) replaces it with the last span,
  and `timeShift` (such as `1d`) moves it back. A zoom on the chart replaces
  the relative time until the viewer zooms out. `refresh` is a duration of at
  least `5s`, `off`, or an interval variable reference.
- `fieldConfig` sets units, decimals, min, max, display name, color schemes,
  thresholds, value mappings, no value, and overrides matched by name, regex,
  query, or type.
- `transformations` organize, rename by regex, filter by name or value,
  reduce, calculate, join by field, merge, series to rows, labels to fields,
  group by, sort by, limit, and convert field type.

The settings panel offers each query language's editor with name suggestions,
a preview, and a query inspector. Dragging across a time axis sets the chart's
time range variable, or zooms that chart alone when its range is fixed; a
double click zooms out. Renders wait for every chart's data. The package has no
visualizations for logs, traces, maps, or free canvases.

### Countdown and Wi-Fi QR

Package `countdown` counts down to dates. It takes `events`, a list of
`{label, date: "YYYY-MM-DD", time?, yearly?}`. `layout` is `single` for the
nearest event or `list` for up to `maxItems`. `units` is `days` or `auto`,
which shows hours and minutes inside a day. A yearly event moves to next year
once its day ends, and `showPast` keeps passed events as "N days ago".

Package `wifi-qr` draws a join-network QR code. It takes `ssid`, `password`,
`security` (`WPA`, `WEP`, or `nopass`), and `hidden`, with an optional `label`
and `showPassword`. `colors` is `plain`, black on white, which scans most
reliably, or `theme`. The password is in the widget's config, so every viewer
of the dashboard can read it, as they can from the code.

### Other bundled widgets

These packages work with their defaults. The table lists their common settings.

| Package | Fields |
| --- | --- |
| `analog-clock` | `timezone`, `secondHand` (default `tick`), `faceStyle` (default `dark`), `accentColor`, `layout` (default `classic`), `secondTimezone` |
| `binary-clock` | `timezone`, `format` (`24h` or `12h`), `ledShape` (`circle` or `square`), `activeColor`, `inactiveColor`, `layout` (default `bcd-grid`) |
| `fuzzy-clock` | `timezone`, `fuzziness` (default 3), `language` (default `en`), `typography` (default `serif`), `layout` (default `single-sentence`) |
| `stock` | `symbols` (array of ticker strings), `provider` (default `yahoo`), `layout` (default `single-quote`), `interval` (default `1D`) |
| `crypto` | `pair` (default `BTC/USD`), `provider` (default `coingecko`), `layout` (default `price-card`), `quoteCurrency` (default `USD`) |
| `calendar` | `layout` (default `monthly`), `startOfWeek` (default 0), `showTags`. Sources are set in its panel and stored by the calendar plugin, not in the placement's config. Each source names a calendar connection (ICS, CalDAV, or Google) with its own name, color, tag, and optionally which of the connection's calendars to show; credentials stay in the connection |

Ask for a location or source identifier when it is missing. Do not guess which
Home Assistant entity, MQTT device, or calendar belongs to the user.

## Drafts and deployment

Writes save the draft immediately. An existing deployed dashboard keeps its
published version until you call `deploy_dashboard`, and an undeployed
dashboard shows its draft. The editor chat can deploy when asked. A deployment
freezes the name, description, and theme selection with the layout, while
sharing and URL identity stay live. A theme or widget package updated later
still changes how deployed dashboards look.

Browser and MCP edits share the same draft and appear in other open editors.
`restore_dashboard_version` replaces the whole draft with a stored version;
omitting the version discards all changes since the current deployment. It does
not deploy the restored draft.

Use `render_dashboard` to inspect a screenshot of the draft before deployment.
It uses the dashboard's real renderer at a requested width and height. Check
spacing, clipping, and widget configuration in the image. A screenshot captures
the requested viewport and does not interact with controls.

Before deployment, run `validate_dashboard` for geometry, schema, and binding
checks, then `render_dashboard` with `diagnostics: true` for a screenshot with
widget bounds, overflow, and browser errors. Widgets without config schemas
produce a warning, so read their documentation and preview them.
`diff_dashboard` shows draft changes against the deployment, including title
and theme selection.

Validation runs only when you call it. Overlaps are warnings, and deployment
snapshots the stored draft without running `validate_dashboard`. A config or
display replacement that names its revision cannot overwrite a newer one; on a
conflict the editor says so and reloads the other writer's change. The
placement picker lists loaded packages, but direct writes can name a package
whose plugin is unloaded and can save invalid geometry. `validate_dashboard`
reports both, and a widget whose plugin is unloaded exposes nothing to viewers.
