# Design plan: Tile appearance

**Feature**: `specs/044-tile-appearance` | **Date**: 2026-11-21 | **Spec**: [spec.md](spec.md)

## Summary

Two layers change. The widget layer (core plugin shared code) gains state
looks and value formats on bindings, evaluated against the binding's own
reading, and a new `button` package built on the same binding code. The host
layer (dashboard viewer and editor) gains a placement `style`, applied as CSS
custom properties on the placement's cell, and `cues`, evaluated with the
visibility condition machinery and drawn as an overlay border. The server
validates and versions style and cues as part of the placement display object
and grants cue state sources the reads visibility conditions get.

## Technical context

- **Language and dependencies:** Go, Svelte 5, the core widget plugin. No new
  dependencies.
- **Server model:** `pkg/models/dashboard_display.go` adds `TileStyle` and
  `StatusCue` to `WidgetDisplay`, and `AllConditions` returning visibility
  conditions then each cue's.
- **Validation:** `pkg/dashops/display.go` validates style ranges, cue count,
  colors' length, animations, and each cue condition with `validateCondition`.
- **Scope:** `pkg/dashscope` reads `AllConditions` for grants and variable
  references; a `SubstituteDisplay` helper substitutes visibility and cue
  conditions together. `tools_dependencies.go` reports cue sources.
- **Frontend host:**
  - `frontend/src/lib/tileStyle.ts` turns a style into cell CSS and picks the
    active cue.
  - `frontend/src/lib/components/TileCue.svelte` draws the animated border.
  - `displayState.ts` factors `conditionListMet` out of `conditionsMet`.
  - `DashboardView.svelte` and `DashboardEdit.svelte` apply both, and watch
    cue sources with `watchConditionSources`.
  - `PlacementDisplayPanel.svelte` edits style and cues.
- **Widgets:** `plugins/core/widgets/shared/appearance.ts` holds state
  matching and value formats. Entity Control and Entities Card apply them; a
  shared `StatesEditor.svelte` edits them; `widgets/button` is the new package.
- **Limits:** 16 states, 8 cues, 16 conditions per cue (the visibility limit),
  border width 0 to 16, radius 0 to 64, color strings up to 64 bytes. A color
  holding `;`, braces, angle brackets, quotes, a backslash, or `url(` is
  refused, because it is written into a style attribute.

## Constitution check

- **Failures:** a state or cue that cannot be evaluated (no reading, wrong
  type) does not match, so the tile falls back to its plain look. A Button
  command failure flashes and reports the error, as Entity Control does.
- **Drafts and deploys:** style and cues live in the placement display object
  and states and formats in widget config, both already drafted, versioned,
  restored, diffed, exported, and imported. Writes stay lenient; explicit
  validation is strict about incomplete cue sources.
- **Permissions:** cue state sources join `ConditionBindings`, so they grant
  read and never write. States read only the binding, which already has its
  grant. The Button declares `scope.bindings: "."` and so grants exactly what
  Entity Control grants.
- **Existing state:** new optional fields only. An unset style and empty cues
  keep `IsZero` true, so existing snapshots compare unchanged. No migration.

## Design and contracts

### Placement display

```json
{"style":{"background":"transparent","border_color":"#ff8800","border_width":3,
          "radius":0,"text_color":"","accent_color":""},
 "cues":[{"match":"all","color":"#f38ba8","animation":"blink",
          "conditions":[{"kind":"state","source":"ha","connectionId":"<uuid>",
                         "entityId":"cover.garage","operator":"equals","value":"open"},
                        {"kind":"time","from":"22:00","to":"06:00"}]}]}
```

The host sets on the cell: `background` (or none with no shadow when
transparent), `border`, `border-radius`, and the custom properties
`--theme-widget-bg`, `--theme-text`, `--theme-accent`, and `--tile-border`.
Custom properties cross the widgets' shadow roots, so packages follow without
changes. Tile-drawing widgets read `--tile-border` before `--theme-border`
for their own frame, so a transparent style clears it.

### Cues

The viewer evaluates each placement's cues in order with
`conditionListMet(conditions, match, …)` against the same records, clock, and
variables as visibility, and draws the first passing cue. The overlay is an
absolutely positioned, pointer-transparent element over the cell with the
cell's radius. Animations are CSS keyframes: pulse (box-shadow glow), blink
(opacity steps), breathe (slow opacity), comet (a rotating conic gradient
masked to the border), ants (moving dashes). `prefers-reduced-motion` and
render mode drop the animation and keep the color.

### States and formats (widget config)

```json
{"source":"control","controlId":"<uuid>","format":"percent","fieldPrecision":1,
 "states":[{"operator":"equals","value":"on","icon":"la-microphone-slash",
            "iconColor":"#fff","background":"#c0392b","label":"Muted"},
           {"icon":"la-microphone","label":"Live {value}"}]}
```

`matchState(states, resolved)` reads the attribute with `attributeValue`, or
the primary attribute of a composite kind, and compares with the same rules as
visibility conditions, except that a switch reads as `on` or `off`.
Unavailable sources match nothing. The resolver folds the binding's `format`
and `precision` into the kind options, and `formatValue` applies the format
(`shared/formats.ts`) before its label and unit handling, so every place a
reading is drawn as text follows it.

### Button package

`plugins/core/widgets/button`: config is an `EntitySource` plus `states`,
`showLabel` (default true), `showValue` (default false), `confirmFirst`.
Reading, following, and commanding a binding moved out of Entity Control into
`shared/bindingSession.ts` (`bindSource`, `sendCommand`, `firePress`), which
both widgets use. Single action: boolean, light, fan toggle the primary
attribute; lock flips; buttons with zero or one choice runs it; flow trigger
fires the first press. Manifest: `scope.bindings: "."` and an open
`configSchema` checking `states`, `format`, and `precision`; Entity Control
and Entities Card rows carry the same open schema.

### Editing

`PlacementDisplayPanel` gains a Tile style section (color fields with a theme
default, numbers for width and radius, a transparent switch) and a Status cues
list. Its condition editor moved to `ConditionListEditor.svelte`, used for
visibility and for each cue. Entity Control's binding settings moved to
`shared/BindingPanel.svelte`, which the Entity Control and Button config
elements wrap with their own switches. It and each Entities Card row include
the shared `AppearanceEditor.svelte` for format, decimals, and states.

### MCP and REST

`WidgetDisplay` is the type both already accept, so the new fields appear in
their schemas through struct descriptions. `docs/dashboard-authoring.md`
documents the fields for editor chat.

## State and data model

No new tables. `dashboard_widgets.display` JSON gains `style` and `cues`;
widget `config` JSON gains `states` and `format`.

## Verification

- `make test TARGET=server`: display validation (ranges, animations, cue
  conditions), `IsZero`, cue grants in dashscope, variable references in cues.
- `make test TARGET=frontend`: `conditionListMet` and cue choice, style CSS.
- Core widget format and state matching run in the frontend group, which
  imports the plugin's shared code.
- Live: MCP places a Button, an Entity Control with states, a transparent
  clock, and a cue on a control; toggle the control with `set_control` and
  check the viewer and editor canvas with screenshots and the console.

## Risks and open decisions

- Widgets that draw their own opaque background ignore `--theme-widget-bg`;
  a transparent style then removes only the host face. Bundled tile widgets
  read the variable.
