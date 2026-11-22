# Feature specification: Tile appearance

**Feature**: `specs/044-tile-appearance`
**Created**: 2026-11-21
**Status**: Implemented 2026-11-21
**Input**: Tile appearance for dashboards: (1) per-binding state looks:
entity-control tiles, entities-card rows, and a new button widget take an
ordered list of states, each matching the binding's own value (or an
attribute) with equals/not_equals/contains/above/below, and overriding icon,
icon color, background, text color, and label (label may include the formatted
value). (2) A deck-style `button` widget: one binding drawn as a square tile
with a large icon, a short label, optional value line; the whole tile presses
the binding's single action (toggle, lock/unlock, scene/run, flow trigger),
with optional confirm-first; kinds with no single action display only. (3)
Per-placement appearance overrides in the placement display object, applied
by the host to any widget: background (including transparent, which drops the
tile face and border), border color and width, corner radius, text color,
accent color. (4) Animated border status cues on placements: an ordered list
of cues, each with visibility-style conditions (state/time/variable, all/any)
and a color plus an animation from static, pulse, blink, breathe, comet,
marching ants; the first passing cue draws. (5) Unit-aware value formatting on
bindings: a `format` of duration (seconds to h:mm:ss), bytes, bytes per
second, percent, or number with precision, used wherever the binding's value
is drawn.

This feature extends the placement display settings of
[feature 023](../023-dashboard-display-control/spec.md) and the bindings of
[feature 006](../006-dashboard-authoring/spec.md). Conditions on cues use the
condition forms and grants 023 defines; variable conditions are those of
[feature 025](../025-dashboard-variables/spec.md).

## User scenarios and testing

### User story 1: A tile that looks like its state (Priority: P1)

An author binds a microphone mute switch to an Entity Control tile. They add
two states: when the value is `on`, the icon is a slashed microphone on a red
background with the label "Muted"; otherwise the plain microphone on the
theme's background with the label "Live". A garage door row in an entities
card turns its icon orange and its label "Open {value}" while the door is
open. The viewer sees the look change as soon as the source reports.

**Why this priority**: State is what a wall panel exists to show, and color
and icon read from across a room where text does not.
**Independent test**: Bind a boolean control to an Entity Control tile with
two states, toggle the control through MCP, and see the tile's icon,
background, and label change on an open viewer.

**Acceptance scenarios**:

1. **Given** a tile with states `equals on` then a state with no condition,
   **When** the value is `on`, **Then** the first state's icon, colors, and
   label draw; **When** it is `off`, **Then** the second state's do.
2. **Given** a state with `above 30` on a temperature sensor, **When** the
   reading is 31.2, **Then** the state applies; at 30 it does not.
3. **Given** a state that sets only the background, **When** it applies,
   **Then** the icon, label, and text color stay as the binding sets them.
4. **Given** a state label `CPU {value}` on a binding with format `percent`,
   **When** the value is 42.3, **Then** the label reads `CPU 42.3 %`.
5. **Given** no state matches, **When** the tile draws, **Then** it draws as
   it did before this feature.
6. **Given** a source that is unavailable, **When** the tile draws, **Then**
   no state applies and the tile shows the unavailable reading.

### User story 2: A deck of buttons (Priority: P1)

An author builds a phone remote of square buttons: a scene, a light toggle, a
front door lock, a flow trigger "Movie night" that asks for a second tap, and
a display-only button showing the hall temperature. Each is a large icon with
a short label. Pressing anywhere on a button runs its action.

**Why this priority**: A grid of big single-purpose buttons is the most common
touch panel layout and karakuri has no widget built for it.
**Independent test**: Place a Button bound to a boolean control, press it in
the viewer, and see the control flip and the button's look follow.

**Acceptance scenarios**:

1. **Given** a Button bound to a boolean control, **When** a viewer presses
   it, **Then** the control toggles and the button reports the new state.
2. **Given** a Button bound to a lock, **When** pressed, **Then** a locked
   lock is unlocked and an unlocked one is locked.
3. **Given** a Button bound to a buttons control with one action or a scene,
   **When** pressed, **Then** that action runs.
4. **Given** a Button bound to a flow trigger with `confirmFirst`, **When**
   pressed once, **Then** it shows that a second tap is needed and fires only
   on a second press within 3 seconds.
5. **Given** a Button with `confirmFirst` bound to any pressable source,
   **When** pressed once, **Then** nothing is sent until the second press.
6. **Given** a Button bound to a number, select, or text source, or marked
   display only, **When** pressed, **Then** nothing is sent and it shows its
   value line.
7. **Given** a viewer without control permission, **When** they press,
   **Then** nothing is sent and the button draws as display only.
8. **Given** a command that fails, **When** pressed, **Then** the button
   flashes an error and says why on hover.

### User story 3: Style any tile (Priority: P2)

An author wants the clock on a mirror dashboard to have no tile face, a
camera tile with a thick accent border and square corners, and a weather tile
with a dark blue background. They set these in the placement's Display panel,
and they apply to any widget package without the package knowing.

**Why this priority**: Themes set one look for every tile; a dashboard needs a
few tiles to stand apart, and a mirror needs tiles that vanish.
**Independent test**: Set a transparent background on a digital clock's
placement and see the tile's face, border, and shadow disappear in the viewer
and the editor canvas.

**Acceptance scenarios**:

1. **Given** a placement with background `transparent`, **When** it draws,
   **Then** the tile has no background, border, or shadow, and the widget's
   contents still draw.
2. **Given** a border color `#ff8800` and width 3, **When** it draws,
   **Then** the tile's border is 3 pixels of that color.
3. **Given** a corner radius 0, **When** it draws, **Then** the tile is
   square.
4. **Given** a text color and an accent color, **When** a widget draws with
   the theme's text and accent colors, **Then** it uses the placement's.
5. **Given** an empty style, **When** it draws, **Then** the theme applies
   unchanged and the stored placement is identical to one without a style.

### User story 4: A border that warns (Priority: P2)

An author adds cues to a garage door tile: a red blinking border while the
door is open after 22:00, and an amber breathing border while it is open at
any other time. The tile draws a plain border otherwise.

**Why this priority**: Motion draws the eye to the one tile that needs
attention without a toast or alert interrupting everything else.
**Independent test**: Add a cue with a state condition on a boolean control,
flip the control, and see the border animate and stop.

**Acceptance scenarios**:

1. **Given** two cues whose conditions both pass, **When** the tile draws,
   **Then** only the first cue's border draws.
2. **Given** a cue with `match: any` and two conditions, one passing,
   **When** the tile draws, **Then** the cue draws.
3. **Given** a cue whose condition source has not answered, **When** the page
   loads, **Then** the cue does not draw.
4. **Given** each animation `static`, `pulse`, `blink`, `breathe`, `comet`,
   and `ants`, **When** its cue passes, **Then** the border draws in the cue's
   color with that motion.
5. **Given** a viewer with reduced motion requested, **When** an animated cue
   passes, **Then** the border draws in the cue's color without motion.
6. **Given** a public dashboard, **When** an anonymous viewer opens it,
   **Then** the cue's condition source is readable to it as a visibility
   condition's is, and nothing about the source is writable.
7. **Given** a render of the dashboard, **When** a cue passes, **Then** the
   border draws in its color without motion.

### User story 5: Readable numbers (Priority: P3)

An author shows an uptime sensor in seconds, a network rate in bytes per
second, a disk used in bytes, and a battery as a percent. They pick a format on
each binding, and the tile and row show `3:07:42`, `3.5 MB/s`, `1.5 KB`, and
`87 %`.

**Why this priority**: Raw numbers are correct and unreadable, but this is a
refinement of what already displays.
**Independent test**: Bind a sensor reading 187 with format `duration` and see
`3:07`.

**Acceptance scenarios**:

1. **Given** format `duration` and 187, **Then** it reads `3:07`; 11262 reads
   `3:07:42`; 90061 reads `1d 1:01:01`.
2. **Given** format `bytes` and 1536, **Then** it reads `1.5 KB`; 0 reads
   `0 B`; 5 GiB reads `5 GB`.
3. **Given** format `bytes_per_second` and 3670016, **Then** it reads
   `3.5 MB/s`.
4. **Given** format `percent` and 12.5, **Then** it reads `12.5 %`.
5. **Given** format `number` with precision 1 and 21.4083, **Then** it reads
   `21.4`, followed by the binding's unit when one is set.
6. **Given** a format on a value that is not a number, **Then** the value is
   drawn as it was without a format.

### Edge cases

- A state names an attribute the source does not have: it never matches.
- A state's `above` or `below` compares a value that is not a number: no match.
- A state icon that is not in the icon pack is drawn as its text, as a binding
  icon is.
- A color value that is not a CSS color is drawn by the browser as unset, so
  the theme applies; explicit validation warns about it.
- A state list longer than 16, or a cue list longer than 8: explicit
  validation reports it.
- A placement style or cue on a widget in the region layout applies the same.
- A transparent background with a cue: the cue's border still draws.
- Variables in a state value or label are substituted as in any config string.
- A Button with no binding draws a hint to pick a source.

## Requirements

### Functional requirements

- **FR-001**: Entity Control, each Entities Card row, and the Button widget
  MUST accept an ordered list `states` of at most 16 entries. Each entry MAY
  name an `attribute`, an `operator` (`equals`, `not_equals`, `contains`,
  `above`, `below`) and a `value`; an entry with no operator always matches.
  Text comparisons MUST ignore case; `above` and `below` MUST compare numbers
  and fail on anything else.
- **FR-002**: The first matching state MUST apply. A state MAY set `icon`,
  `iconColor`, `background`, `color` (text), and `label`; each unset field
  MUST leave the binding's own look. No state MUST apply while the source is
  unavailable or has not answered.
- **FR-003**: A state label MUST replace `{value}` with the binding's
  formatted value and `{name}` with the binding's name.
- **FR-004**: States MUST read only the binding's own value and MUST grant
  nothing beyond what the binding grants.
- **FR-005**: The core plugin MUST ship a `button` widget whose config is one
  binding of source `control`, `ha`, `mqtt`, or `flow-trigger` plus `states`,
  `showLabel`, `showValue`, and `confirmFirst`. It MUST draw a large icon, the
  label, and an optional value line, filling its cell.
- **FR-006**: Pressing anywhere on a Button MUST send the binding's single
  action: toggle the primary attribute of a boolean, light, or fan; lock or
  unlock a lock; run the only action of a buttons control or a scene or
  script; fire the first press of a flow trigger. Other kinds, display-only
  bindings, and viewers without control permission MUST send nothing.
- **FR-007**: With `confirmFirst`, the first press MUST arm the Button for 3
  seconds and only a second press in that time MUST send. A trigger press's own
  `confirmFirst` MUST behave the same.
- **FR-008**: The Button MUST show the busy, succeeded, and failed results the
  Entity Control tile shows, and the failure reason on hover.
- **FR-009**: A placement's display object MUST accept a `style` with
  `background` (a CSS color or `transparent`), `border_color`, `border_width`
  (0 to 16 pixels), `radius` (0 to 64 pixels), `text_color`, and
  `accent_color`. The host MUST apply it to every widget package by setting the
  theme values the tile reads, without the package's involvement.
- **FR-010**: A transparent background MUST remove the tile's background,
  border, and shadow.
- **FR-011**: A placement's display object MUST accept `cues`, at most 8, each
  with `conditions`, `match` (`all` or `any`), `color`, and `animation`
  (`static`, `pulse`, `blink`, `breathe`, `comet`, `ants`). The first cue
  whose conditions pass MUST draw a border in its color with its animation,
  over the style's border. A cue with no conditions MUST always pass.
- **FR-012**: Cue conditions MUST take the forms and semantics of visibility
  conditions (023-FR-007, 025), and their state sources MUST grant read access
  the way visibility condition sources do (023-FR-008).
- **FR-013**: Animations MUST stop under a reduced-motion preference and in
  renders, keeping the cue's color.
- **FR-014**: A binding MUST accept a `format` of `duration`, `bytes`,
  `bytes_per_second`, `percent`, or `number`, applied wherever the binding's
  value is drawn as text in Entity Control, Entities Card, and Button. `number`
  and `percent` MUST honor the binding's precision; `bytes` and
  `bytes_per_second` MUST use 1024 steps and at most one decimal place.
  `duration` MUST read seconds as `m:ss`, `h:mm:ss`, or `Nd h:mm:ss`.
- **FR-015**: Style, cues, states, and formats MUST be part of the draft,
  deployment snapshot, restore, diff, export, and import as the configuration
  and display they live in already are. A placement without them MUST store
  identically to one saved before this feature.
- **FR-016**: Explicit validation MUST report malformed cues, unknown
  animations, out-of-range border width or radius, more than 8 cues, more than
  16 states, unknown state operators, and unknown formats.
- **FR-017**: The editor MUST edit states in the Entity Control, Entities Card
  row, and Button panels; style and cues in the Display panel; and format in
  each binding's panel. The canvas MUST show style, cues, and states live.
- **FR-018**: REST, MCP, the browser editor, and editor chat MUST share the
  service-layer rules for style and cues.

### Key entities

- **State look**: attribute, operator, value, icon, icon color, background,
  text color, label. Ordered per binding, in the widget's config.
- **Placement style**: background, border color and width, radius, text
  color, accent color. In the placement display settings.
- **Status cue**: conditions, match, color, animation. Ordered per placement,
  in the placement display settings.
- **Value format**: one of five names on a binding, with its precision and
  unit.

## Success criteria

### Measurable outcomes

- **SC-001**: A boolean control toggled through MCP changes a bound tile's
  state look on an open viewer within the time the tile's value updates today.
- **SC-002**: A Button bound to each of boolean, lock, scene, and flow trigger
  sends exactly one command per press, and two presses with `confirmFirst`.
- **SC-003**: A transparent style on any of the bundled widget packages
  removes its tile face, checked on at least a clock, a weather, and an
  Entity Control placement.
- **SC-004**: An anonymous viewer of a public dashboard sees a cue driven by a
  Home Assistant entity, and can write nothing through it.
- **SC-005**: A dashboard deployed before this feature reports no draft
  changes after the upgrade.
- **SC-006**: Each format example in user story 5 renders exactly as written.

## Assumptions

- States match against the binding's own reading only. Styling a tile from
  another source's state is done with a cue, whose conditions can name any
  source.
- Tap is the only gesture. Hold and double-tap actions, and buttons that change
  page or dashboard, are out of scope.
- Colors are CSS color strings; the editor offers a color picker and a
  "theme" choice that clears the field.
- Formats apply to text readings. Sliders and switches keep their own drawing.
