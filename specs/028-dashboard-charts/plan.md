# Design plan: Dashboard charts

**Feature**: `specs/028-dashboard-charts` | **Date**: 2026-10-30 | **Spec**: [spec.md](spec.md)

## Summary

A core `chart` widget draws data visualizations with Apache ECharts
and Svelte. Its configuration holds up to ten queries. The browser never sends
query text: it asks the server for a placement's data by dashboard and
placement ID. The server reads the queries from the layout it serves that
caller, as the news feed route does, substitutes viewer variable values that
are options (with the language escaping of 025/FR-022), and runs them through
feature 027's `tsdbops.Query` behind a shared cache. The widget then applies
transformations, field options, and overrides in the browser and draws the
result.

## Technical context

- Language and dependencies: Svelte 5 custom elements built by Vite like the
  other core widgets. Adds `echarts` 5, imported per chart type and component
  and drawn with the SVG renderer. SVG keeps text crisp in renders and lets the
  theme's CSS colours apply.
- Components:
  - `plugins/core/widgets/chart/`, with `src/data/` for frames,
    transformations, field config, units, and reducers, `src/viz/` for one
    module per visualization, and `src/editor/` for the panel.
  - `plugins/core/manifest.yaml` (the entry and its schema).
  - `pkg/api/dashboard_chart_handler.go` (data and preview routes).
  - `pkg/dashchart` (config parsing, range resolution, the cache).
  - `pkg/mcpserver` (the `get_chart_data` tool).
  - `frontend/src/lib/widgetBridge.ts` (`isRenderMode`, a busy flag).
  - `DashboardView.svelte` (readiness waits on busy widgets).
- Storage: none persistent. The query result cache is in memory, keyed and
  bounded as below.
- Limits from the spec:
  - 1 to 10 queries per chart.
  - Refresh of at least 5 seconds.
  - Cache entries live for at least 5 seconds and at most the chart's refresh.
  - The request width is clamped to 50 to 4,000 pixels and sets the default
    maximum data points.
  - The cache holds 512 entries, evicted least recently used.

## Constitution check

- **Failures**: each query fails alone. Its error is returned with its
  reference ID and drawn on the chart while the other series draw. A
  connection that is down returns `unavailable`, and the widget keeps its last
  data and retries at the next refresh.
- **Drafts and deployment**: chart configuration is placement config, so it
  saves to the draft and freezes with deployment. Viewers read queries from the
  served layout; editors and draft renders read from the draft.
- **Permissions**: data requests check `canReadDashboard` and read only the
  served placement. Variable values must be options, except time range values,
  which only need to parse (025/FR-003). Previews need edit access and
  `api:connections:read`. Credentials never leave the server. Query text in the
  placement config is visible to viewers, as all placement config is; the
  dashboard-authoring guide states this.
- **Existing state**: no schema change. Existing dashboards are unaffected.
- **Evidence**:
  - `make test TARGET=server` covers the route, scope, and cache.
  - `make test TARGET=frontend` covers type checks and Vitest tests of the
    widget's data modules, which `frontend` imports from the plugin path.
  - Browser checks of every visualization against seeded containers, with
    screenshots and the console. Renders through MCP.

## Design and contracts

### Data route

`POST /api/dashboards/{id}/widgets/{widgetId}/chart`, with optional auth:

```json
{"vars": {"range": "now-24h,now", "host": "a"}, "from": "", "to": "", "width": 800, "refs": ["A"]}
```

The server handles it in these steps:

1. **Find the placement.** Resolve the dashboard and check `canReadDashboard`.
   Pick the draft or the deployed layout as the feed route does. Find the
   placement and require `PackageName == "chart"`.
2. **Resolve variables.** Call `dashvars.ResolveFor(served, values)`, which
   resolves in order, giving chained query variables the viewer's values of
   earlier ones. Then validate each requested value with `Resolve`; time range
   values only need to parse. Missing variables take their defaults.
3. **Pick the range.** The order of precedence is: the request's `from`/`to`
   (a zoom of a fixed-range chart, parsed and capped like any range); the
   `timeRange` config after substitution; the default `now-6h,now`. Then apply
   `relativeTime` and `timeShift`.
4. **Expand each query.** Substitute variables into the text with
   `dashscope.SubstituteQuery(text, language, values, vars)`. Set max data
   points to the query's own value, or else the width. Leave hidden queries in,
   because transformations may use them.
5. **Run.** Execute the queries concurrently through the cache.
6. **Respond.** Return `{results: [{ref_id, result | error, cached}], range:
   {from, to}, interval_ms}`.

`POST /api/dashboards/{id}/chart/preview` takes `{query, vars, from, to,
width}` and runs one unsaved query. It needs edit access and connection read.

MCP `get_chart_data` takes `dashboard`, `widget_id`, `vars`, `from`, and
`to`, and applies the data route's rules for the MCP principal.

### Cache

The cache key is `connection ID + expanded text + language + HA fields + from
and to rounded down to the interval + max data points`. Relative ranges
rounded this way give every viewer the same key inside an interval.
`singleflight` collapses concurrent misses. The TTL is
`clamp(min(refresh, interval), 5s, 5m)`. Errors are cached for 5 seconds so a
dead database is not hammered.

### Widget

- **Config:** `{viz, queries[], timeRange, relativeTime, timeShift, refresh,
  maxDataPoints, fieldConfig: {defaults, overrides[]}, transformations[],
  options}`. The manifest schema validates the top level and query shapes;
  visualization options stay open objects.
- **Data pipeline (`src/data`):**
  - Response series become frames.
  - Legend format and display names are applied.
  - Transformations run in order.
  - Field config is resolved per field: defaults, then the first matching
    overrides in order.
  - The visualization receives frames with resolved field config.
  - Pure functions, tested by Vitest.
- **Units (`src/data/units.ts`):** Common unit IDs:
  - none, short, percent, percentunit
  - humidity, celsius, fahrenheit, kelvin
  - watt, kwatt, watth, kwatth, volt, amp, hertz, lux, pressurehpa, ppm,
    conmgm3
  - bytes, decbytes, bits, bps, Bps
  - ms, s, m, h, d, dtdurationms, dtdurations, dateTimeAsIso, dateTimeFromNow
  - currencyUSD, currencyEUR, currencyGBP
  - lengthm, lengthkm, velocityms, velocitykmh
  - litre, m3, flowlpm
  - `suffix:<text>` and `prefix:<text>` customs
  - Scaling follows SI or IEC.
- **Visualizations (`src/viz`):**

  | Visualization | Options |
  | --- | --- |
  | Time series | draw style (line, bars, points), interpolation (linear, smooth, stepBefore, stepAfter), line width, line style, fill opacity, gradient mode, show points, point size, stacking (none, normal, percent), null handling (gaps, connected, threshold), bar alignment, axis placement, label, soft min and max, log scale, thresholds style (off, line, area, dashed) |
  | Trend | Time series options with an x field |
  | Bar chart | x field, orientation, stacking, group width, bar width, value labels, x label rotation |
  | Stat | calculation, fields, color mode (none, value, background gradient, background solid), graph mode (none, area), text mode, justify, orientation, percent change |
  | Gauge | calculation, show threshold markers and labels, min, max, orientation |
  | Bar gauge | calculation, display mode (basic, gradient, lcd), value display, show unfilled, orientation, minimum size |
  | Table | cell type per field (auto, color text, color background, gauge, sparkline, JSON), show header, sortable, column filter, pagination, footer calculations, column width, alignment |
  | Pie chart | pie or donut, calculation, labels (name, value, percent), legend values, sort |
  | State timeline | merge equal values, show values, row height, line width, fill opacity, align values |
  | Status history | show values, row height, column width |
  | Heatmap | calculate buckets from series or use bucket fields, y buckets, color scheme and steps, cell gap, cell values, y axis |
  | Histogram | bucket size, bucket count, offset, combine series, stacking, fill opacity |
  | Candlestick | mode (candles, OHLC, volume), open, high, low, close, and volume field mapping, up and down colors |
  | XY chart | x field, y fields, series mapping, point size, show lines and points, color and size by field |

  Time-based types share legend, tooltip, zoom, and axis code.
- **Theme:** `getComputedStyle(host)` reads `--theme-*` values, and the
  palette is blue, green, peach, mauve, red, yellow, teal, sky, lavender, and
  pink with fallbacks. A `MutationObserver` on `<html>` style and `<head>`
  children re-reads them and redraws.
- **Zoom:** ECharts `brush`/`dataZoom` on the x axis. When `timeRange` is a
  whole `${name}` reference to a time range variable, a zoom calls
  `setVariable(name, from + "," + to)`. Otherwise it sets a local range sent as
  `from`/`to`. A double click resets the local range, or doubles the bound
  variable's span.
- **Refresh:** a timer at the resolved refresh (`5s` minimum; `off` loads
  once). `watchVariables` refetches when a referenced variable changes, and the
  live socket's reconnect refetches.
- **Readiness:**
  - The widget sets `data-widget-busy` on its host from mount until its first
    data has drawn or failed.
  - `markReady` in `DashboardView.svelte` waits for no
    `[data-widget-state="loading"]` and no `[data-widget-busy]`, within the
    existing 15-second deadline.
  - `window.__karakuri.isRenderMode()` turns off ECharts animation.
- **Panel (`src/editor`):** A panel editor fitted to the
  settings drawer:
  - a visualization picker
  - query rows, each with connection (`SourceSelect` limited to readable
    kinds), language, a CodeMirror-free textarea with metadata suggestions from
    027's lookup route, legend, and hidden
  - preview with a query inspector (expanded query, interval, timing, raw
    JSON)
  - time settings
  - option groups for panel, standard, thresholds, value mappings,
    overrides, and transformations

  Fields that accept `${name}` keep references.

## State and data model

No persisted entities. The placement config is versioned with the dashboard.

## Verification

| Spec item | Check |
| --- | --- |
| FR-003, FR-004, US2, SC-002 | Go tests through the router: anonymous public viewer gets data; refused for no access, unknown placement, non-chart placement, non-option variable, and query text in the body (ignored, never run) |
| Cache, SC-004 | Go test counting querier calls for 10 concurrent requests |
| Range precedence, relative time, shift | Go table tests in `pkg/dashchart` |
| Transformations, field config, units, reducers, mappings | Vitest in `frontend/src/lib/chart/*.test.ts` |
| US1, US3, US4, US5, SC-001, SC-003 | Browser: one dashboard with every visualization over seeded containers and Home Assistant, a time range variable, zoom, step back, screenshots, console check |
| US6, SC-005 | MCP `render_dashboard` with `var-range` and visual check of the image |

## Risks and open decisions

- ECharts adds roughly 600 KB minified to the chart package. It loads only on
  dashboards that place a chart.
- The possible option set is large. This plan lists the options implemented;
  anything not listed is out of scope until requested.
