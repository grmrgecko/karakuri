// Option descriptors the panel's forms are generated from. A spec's path is
// relative to the object its form edits: the panel options, the default
// field's custom options, or one transformation's options.

import { REDUCERS } from "../data/reducers";
import { GRADIENTS } from "../data/colors";

export type SpecKind = "select" | "number" | "boolean" | "text" | "multi" | "csv" | "color" | "field" | "spanNulls" | "json" | "unit";

export interface Spec {
  path: string;
  label: string;
  kind: SpecKind;
  choices?: { value: string; label: string }[];
  default?: unknown;
  hint?: string;
  min?: number;
  max?: number;
  step?: number;
  /** list stores a select's choice as a one-item list. */
  list?: boolean;
  /** numeric stores a select's choice as a number. */
  numeric?: boolean;
  /** when shows the field only if another path holds one of these values. */
  when?: { path: string; values: unknown[] };
}

const c = (...pairs: [string, string][]) => pairs.map(([value, label]) => ({ value, label }));
const REDUCER_CHOICES = REDUCERS.map((r) => ({ value: r.id, label: r.name }));

const LEGEND: Spec[] = [
  { path: "legend.showLegend", label: "Show legend", kind: "boolean", default: true },
  { path: "legend.displayMode", label: "Legend mode", kind: "select", choices: c(["list", "List"], ["table", "Table"]), default: "list" },
  { path: "legend.placement", label: "Legend placement", kind: "select", choices: c(["bottom", "Bottom"], ["right", "Right"]), default: "bottom" },
  { path: "legend.calcs", label: "Legend values", kind: "multi", choices: REDUCER_CHOICES },
];

const TOOLTIP: Spec[] = [
  { path: "tooltip.mode", label: "Tooltip", kind: "select", choices: c(["single", "Single"], ["multi", "All"], ["none", "Hidden"]), default: "single" },
  { path: "tooltip.sort", label: "Tooltip sort", kind: "select", choices: c(["none", "None"], ["asc", "Ascending"], ["desc", "Descending"]), default: "none", when: { path: "tooltip.mode", values: ["multi"] } },
];

const REDUCE: Spec[] = [
  { path: "reduceOptions.values", label: "Show every value", kind: "boolean", default: false },
  { path: "reduceOptions.calcs", label: "Calculation", kind: "select", choices: REDUCER_CHOICES, default: "lastNotNull", list: true },
  { path: "reduceOptions.limit", label: "Limit", kind: "number", min: 1, when: { path: "reduceOptions.values", values: [true] } },
  { path: "reduceOptions.fields", label: "Fields", kind: "field", hint: "Empty for every number field, a name, or /regex/." },
];

const ORIENTATION: Spec = { path: "orientation", label: "Orientation", kind: "select", choices: c(["auto", "Auto"], ["horizontal", "Horizontal"], ["vertical", "Vertical"]), default: "auto" };
const SHOW_VALUE: Spec = { path: "showValue", label: "Show values", kind: "select", choices: c(["auto", "Auto"], ["always", "Always"], ["never", "Never"]), default: "auto" };

/** VIZ_OPTIONS are each visualization's panel options. */
export const VIZ_OPTIONS: Record<string, Spec[]> = {
  timeseries: [...LEGEND, ...TOOLTIP],
  trend: [{ path: "xField", label: "X field", kind: "field", hint: "A number field; the first one by default." }, ...LEGEND, ...TOOLTIP],
  barchart: [
    { path: "xField", label: "X field", kind: "field", hint: "A string or time field; the first string field by default." },
    ORIENTATION,
    { path: "stacking", label: "Stacking", kind: "select", choices: c(["none", "Off"], ["normal", "Normal"], ["percent", "100%"]), default: "none" },
    { path: "groupWidth", label: "Group width", kind: "number", min: 0, max: 1, step: 0.01, default: 0.7 },
    { path: "barWidth", label: "Bar width", kind: "number", min: 0.05, max: 1, step: 0.01, default: 0.97 },
    SHOW_VALUE,
    { path: "xTickLabelRotation", label: "X label rotation", kind: "number", min: -90, max: 90, step: 15, default: 0 },
    ...LEGEND,
    ...TOOLTIP,
  ],
  stat: [
    ...REDUCE,
    { path: "colorMode", label: "Color mode", kind: "select", choices: c(["none", "None"], ["value", "Value"], ["background", "Background gradient"], ["background_solid", "Background solid"]), default: "value" },
    { path: "graphMode", label: "Graph mode", kind: "select", choices: c(["none", "None"], ["area", "Area"]), default: "area" },
    { path: "textMode", label: "Text mode", kind: "select", choices: c(["auto", "Auto"], ["value", "Value"], ["value_and_name", "Value and name"], ["name", "Name"], ["none", "None"]), default: "auto" },
    { path: "justifyMode", label: "Text alignment", kind: "select", choices: c(["auto", "Auto"], ["center", "Center"]), default: "auto" },
    ORIENTATION,
    { path: "showPercentChange", label: "Show percent change", kind: "boolean", default: false },
  ],
  gauge: [
    ...REDUCE,
    { path: "showThresholdMarkers", label: "Show threshold markers", kind: "boolean", default: true },
    { path: "showThresholdLabels", label: "Show threshold labels", kind: "boolean", default: false },
    ORIENTATION,
  ],
  bargauge: [
    ...REDUCE,
    { path: "displayMode", label: "Display mode", kind: "select", choices: c(["basic", "Basic"], ["gradient", "Gradient"], ["lcd", "Retro LCD"]), default: "gradient" },
    { path: "valueMode", label: "Value display", kind: "select", choices: c(["color", "Value color"], ["text", "Text color"], ["hidden", "Hidden"]), default: "color" },
    { path: "namePlacement", label: "Name placement", kind: "select", choices: c(["auto", "Auto"], ["top", "Top"], ["left", "Left"], ["hidden", "Hidden"]), default: "auto" },
    { path: "showUnfilled", label: "Show unfilled area", kind: "boolean", default: true },
    ORIENTATION,
    { path: "minVizHeight", label: "Minimum bar size", kind: "number", min: 8, default: 16 },
    { path: "maxVizHeight", label: "Maximum bar size", kind: "number", min: 8, default: 300 },
  ],
  table: [
    { path: "showHeader", label: "Show header", kind: "boolean", default: true },
    { path: "cellHeight", label: "Cell height", kind: "select", choices: c(["sm", "Small"], ["md", "Medium"], ["lg", "Large"]), default: "sm" },
    { path: "sortable", label: "Sortable columns", kind: "boolean", default: true },
    { path: "footer.show", label: "Show footer", kind: "boolean", default: false },
    { path: "footer.reducer", label: "Footer calculation", kind: "multi", choices: REDUCER_CHOICES, when: { path: "footer.show", values: [true] } },
    { path: "footer.countRows", label: "Count rows", kind: "boolean", when: { path: "footer.show", values: [true] } },
    { path: "footer.enablePagination", label: "Pagination", kind: "boolean", default: false },
  ],
  piechart: [
    { path: "pieType", label: "Pie type", kind: "select", choices: c(["pie", "Pie"], ["donut", "Donut"]), default: "pie" },
    ...REDUCE,
    { path: "displayLabels", label: "Labels", kind: "multi", choices: c(["name", "Name"], ["value", "Value"], ["percent", "Percent"]) },
    { path: "sort", label: "Sort", kind: "select", choices: c(["desc", "Descending"], ["asc", "Ascending"], ["none", "None"]), default: "desc" },
    ...LEGEND.filter((s) => s.path !== "legend.calcs"),
    { path: "legend.values", label: "Legend values", kind: "multi", choices: c(["value", "Value"], ["percent", "Percent"]) },
    ...TOOLTIP.slice(0, 1),
  ],
  "state-timeline": [
    { path: "mergeValues", label: "Merge equal values", kind: "boolean", default: true },
    SHOW_VALUE,
    { path: "alignValue", label: "Align values", kind: "select", choices: c(["left", "Left"], ["center", "Center"], ["right", "Right"]), default: "left" },
    { path: "rowHeight", label: "Row height", kind: "number", min: 0.1, max: 1, step: 0.05, default: 0.9 },
    { path: "lineWidth", label: "Line width", kind: "number", min: 0, max: 10, default: 0 },
    { path: "fillOpacity", label: "Fill opacity", kind: "number", min: 0, max: 100, default: 70 },
    ...LEGEND.filter((s) => s.path !== "legend.calcs"),
    ...TOOLTIP.slice(0, 1),
  ],
  "status-history": [
    SHOW_VALUE,
    { path: "rowHeight", label: "Row height", kind: "number", min: 0.1, max: 1, step: 0.05, default: 0.9 },
    { path: "colWidth", label: "Column width", kind: "number", min: 0.1, max: 1, step: 0.05, default: 0.9 },
    { path: "lineWidth", label: "Line width", kind: "number", min: 0, max: 10, default: 0 },
    { path: "fillOpacity", label: "Fill opacity", kind: "number", min: 0, max: 100, default: 70 },
    ...LEGEND.filter((s) => s.path !== "legend.calcs"),
    ...TOOLTIP.slice(0, 1),
  ],
  heatmap: [
    { path: "calculate", label: "Calculate from data", kind: "boolean", default: false, hint: "Off reads buckets from fields named by their bound, such as le." },
    { path: "cumulative", label: "Bucket counts", kind: "select", choices: c(["", "Auto: cumulative when fields carry le"], ["true", "Cumulative, as Prometheus histograms"], ["false", "Per bucket"]), when: { path: "calculate", values: [false, undefined] } },
    { path: "calculation.xBuckets.mode", label: "X buckets", kind: "select", choices: c(["size", "Size"], ["count", "Count"]), when: { path: "calculate", values: [true] } },
    { path: "calculation.xBuckets.value", label: "X bucket value", kind: "text", hint: "A duration such as 5m for size, a number for count.", when: { path: "calculate", values: [true] } },
    { path: "calculation.yBuckets.mode", label: "Y buckets", kind: "select", choices: c(["count", "Count"], ["size", "Size"]), when: { path: "calculate", values: [true] } },
    { path: "calculation.yBuckets.value", label: "Y bucket value", kind: "text", when: { path: "calculate", values: [true] } },
    { path: "calculation.yBuckets.scale.type", label: "Y scale", kind: "select", choices: c(["linear", "Linear"], ["log", "Logarithmic"]), when: { path: "calculate", values: [true] } },
    { path: "calculation.yBuckets.scale.log", label: "Log base", kind: "select", choices: c(["2", "2"], ["10", "10"]), numeric: true, when: { path: "calculation.yBuckets.scale.type", values: ["log"] } },
    { path: "color.mode", label: "Color mode", kind: "select", choices: c(["scheme", "Scheme"], ["opacity", "Opacity"]), default: "scheme" },
    { path: "color.scheme", label: "Color scheme", kind: "select", choices: Object.entries(GRADIENTS).map(([value, g]) => ({ value, label: g.name })), default: "continuous-BlYlRd", when: { path: "color.mode", values: [undefined, "scheme"] } },
    { path: "color.fill", label: "Color", kind: "color", default: "orange", when: { path: "color.mode", values: ["opacity"] } },
    { path: "color.reverse", label: "Reverse colors", kind: "boolean" },
    { path: "color.min", label: "Color min", kind: "number" },
    { path: "color.max", label: "Color max", kind: "number" },
    { path: "cellGap", label: "Cell gap", kind: "number", min: 0, max: 10, default: 1 },
    SHOW_VALUE,
    { path: "cellValues.unit", label: "Cell value unit", kind: "unit" },
    { path: "cellValues.decimals", label: "Cell value decimals", kind: "number", min: 0, max: 10 },
    { path: "yAxis.axisPlacement", label: "Y axis placement", kind: "select", choices: c(["left", "Left"], ["right", "Right"], ["hidden", "Hidden"]), default: "left" },
    { path: "yAxis.reverse", label: "Reverse Y buckets", kind: "boolean" },
    { path: "legend.show", label: "Show color legend", kind: "boolean", default: true },
    ...TOOLTIP.slice(0, 1),
  ],
  histogram: [
    { path: "bucketCount", label: "Bucket count", kind: "number", min: 1, default: 30 },
    { path: "bucketSize", label: "Bucket size", kind: "number", min: 0 },
    { path: "bucketOffset", label: "Bucket offset", kind: "number", default: 0 },
    { path: "combine", label: "Combine series", kind: "boolean", default: false },
    { path: "stacking.mode", label: "Stacking", kind: "select", choices: c(["none", "Off"], ["normal", "Normal"]), default: "none" },
    { path: "fillOpacity", label: "Fill opacity", kind: "number", min: 0, max: 100, default: 80 },
    ...LEGEND,
    ...TOOLTIP.slice(0, 1),
  ],
  candlestick: [
    { path: "mode", label: "Mode", kind: "select", choices: c(["candles+volume", "Candles and volume"], ["candles", "Candles"], ["volume", "Volume"]), default: "candles+volume" },
    { path: "candleStyle", label: "Candle style", kind: "select", choices: c(["candles", "Candles"], ["ohlcbars", "OHLC bars"]), default: "candles" },
    { path: "colorStrategy", label: "Color strategy", kind: "select", choices: c(["open-close", "Since open"], ["close-close", "Since prior close"]), default: "open-close" },
    { path: "colors.up", label: "Up color", kind: "color", default: "green" },
    { path: "colors.down", label: "Down color", kind: "color", default: "red" },
    { path: "fields.open", label: "Open field", kind: "field" },
    { path: "fields.high", label: "High field", kind: "field" },
    { path: "fields.low", label: "Low field", kind: "field" },
    { path: "fields.close", label: "Close field", kind: "field" },
    { path: "fields.volume", label: "Volume field", kind: "field" },
    { path: "includeAllFields", label: "Draw other fields", kind: "boolean", default: false },
    ...LEGEND,
    ...TOOLTIP.slice(0, 1),
  ],
  xychart: [
    { path: "seriesMapping", label: "Series mapping", kind: "select", choices: c(["auto", "Auto"], ["manual", "Manual"]), default: "auto" },
    { path: "xField", label: "X field", kind: "field", when: { path: "seriesMapping", values: [undefined, "auto"] } },
    { path: "sizeField", label: "Size field", kind: "field", when: { path: "seriesMapping", values: [undefined, "auto"] } },
    { path: "colorField", label: "Color field", kind: "field", when: { path: "seriesMapping", values: [undefined, "auto"] } },
    { path: "series", label: "Series", kind: "json", hint: "A list of {name, x, y, size, color} field names.", when: { path: "seriesMapping", values: ["manual"] } },
    { path: "show", label: "Show", kind: "select", choices: c(["points", "Points"], ["lines", "Lines"], ["points+lines", "Points and lines"]), default: "points" },
    { path: "pointSize.fixed", label: "Point size", kind: "number", min: 1, default: 5 },
    { path: "pointSize.min", label: "Smallest point", kind: "number", min: 1, default: 3 },
    { path: "pointSize.max", label: "Largest point", kind: "number", min: 1, default: 20 },
    { path: "lineWidth", label: "Line width", kind: "number", min: 0, default: 1 },
    ...LEGEND,
    ...TOOLTIP.slice(0, 1),
  ],
};

const SERIES_CUSTOM: Spec[] = [
  { path: "drawStyle", label: "Style", kind: "select", choices: c(["line", "Lines"], ["bars", "Bars"], ["points", "Points"]), default: "line" },
  { path: "barAlignment", label: "Bar alignment", kind: "select", numeric: true, choices: c(["-1", "Before"], ["0", "Centre"], ["1", "After"]), default: "0", when: { path: "drawStyle", values: ["bars"] }, hint: "Where a bar sits relative to its timestamp: ending at it, centred on it, or starting at it." },
  { path: "lineInterpolation", label: "Line interpolation", kind: "select", choices: c(["linear", "Linear"], ["smooth", "Smooth"], ["stepBefore", "Step before"], ["stepAfter", "Step after"]), default: "linear" },
  { path: "lineWidth", label: "Line width", kind: "number", min: 0, max: 10, default: 1 },
  { path: "lineStyle.fill", label: "Line style", kind: "select", choices: c(["solid", "Solid"], ["dash", "Dash"], ["dot", "Dots"]), default: "solid" },
  { path: "fillOpacity", label: "Fill opacity", kind: "number", min: 0, max: 100, default: 0 },
  { path: "gradientMode", label: "Gradient mode", kind: "select", choices: c(["none", "None"], ["opacity", "Opacity"], ["hue", "Hue"], ["scheme", "Scheme"]), default: "none" },
  { path: "showPoints", label: "Show points", kind: "select", choices: c(["auto", "Auto"], ["always", "Always"], ["never", "Never"]), default: "auto" },
  { path: "pointSize", label: "Point size", kind: "number", min: 1, max: 40, default: 5 },
  { path: "stacking.mode", label: "Stack series", kind: "select", choices: c(["none", "Off"], ["normal", "Normal"], ["percent", "100%"]), default: "none" },
  { path: "stacking.group", label: "Stack group", kind: "text", when: { path: "stacking.mode", values: ["normal", "percent"] } },
  { path: "spanNulls", label: "Connect null values", kind: "spanNulls", default: false },
  { path: "transform", label: "Transform", kind: "select", choices: c(["constant", "Constant"], ["negative-Y", "Negative Y"]), default: "constant" },
  { path: "axisPlacement", label: "Axis placement", kind: "select", choices: c(["auto", "Auto"], ["left", "Left"], ["right", "Right"], ["hidden", "Hidden"]), default: "auto" },
  { path: "axisLabel", label: "Axis label", kind: "text" },
  { path: "axisSoftMin", label: "Soft min", kind: "number" },
  { path: "axisSoftMax", label: "Soft max", kind: "number" },
  { path: "scaleDistribution.type", label: "Scale", kind: "select", choices: c(["linear", "Linear"], ["log", "Logarithmic"]), default: "linear" },
  { path: "scaleDistribution.log", label: "Log base", kind: "select", choices: c(["2", "2"], ["10", "10"]), default: 10, numeric: true, when: { path: "scaleDistribution.type", values: ["log"] } },
  { path: "thresholdsStyle.mode", label: "Show thresholds", kind: "select", choices: c(["off", "Off"], ["line", "As lines"], ["dashed", "As dashed lines"], ["area", "As filled regions"], ["line+area", "As lines and filled regions"], ["dashed+area", "As dashed lines and filled regions"]), default: "off" },
];

const TABLE_CUSTOM: Spec[] = [
  { path: "cellOptions.type", label: "Cell type", kind: "select", choices: c(["auto", "Auto"], ["color-text", "Colored text"], ["color-background", "Colored background"], ["gauge", "Gauge"], ["sparkline", "Sparkline"], ["json-view", "JSON view"]), default: "auto" },
  { path: "cellOptions.mode", label: "Background mode", kind: "select", choices: c(["gradient", "Gradient"], ["basic", "Basic"]), default: "gradient", when: { path: "cellOptions.type", values: ["color-background"] } },
  { path: "align", label: "Column alignment", kind: "select", choices: c(["auto", "Auto"], ["left", "Left"], ["center", "Center"], ["right", "Right"]), default: "auto" },
  { path: "width", label: "Column width", kind: "number", min: 20 },
  { path: "minWidth", label: "Minimum column width", kind: "number", min: 20 },
  { path: "filterable", label: "Column filter", kind: "boolean", default: false },
];

const HIDE_FROM: Spec[] = [
  { path: "hideFrom.legend", label: "Hide in legend", kind: "boolean" },
  { path: "hideFrom.tooltip", label: "Hide in tooltip", kind: "boolean" },
  { path: "hideFrom.viz", label: "Hide in graph", kind: "boolean" },
];

/** VIZ_CUSTOM are each visualization's field options, stored under custom. */
export const VIZ_CUSTOM: Record<string, Spec[]> = {
  timeseries: [...SERIES_CUSTOM, ...HIDE_FROM],
  trend: [...SERIES_CUSTOM, ...HIDE_FROM],
  table: TABLE_CUSTOM,
  barchart: HIDE_FROM,
  histogram: HIDE_FROM,
  xychart: HIDE_FROM,
};

/** TRANSFORM_OPTIONS are the form fields of each transformation; the rest are edited as JSON. */
export const TRANSFORM_OPTIONS: Record<string, Spec[]> = {
  organize: [],
  renameByRegex: [
    { path: "regex", label: "Match", kind: "text", hint: "A regex matched against each series name." },
    { path: "renamePattern", label: "Replace", kind: "text", hint: "$1 inserts the first group." },
  ],
  filterFieldsByName: [
    { path: "include.pattern", label: "Keep fields matching", kind: "text", hint: "A regex; empty keeps every field." },
    { path: "include.names", label: "Keep fields named", kind: "csv" },
    { path: "exclude.pattern", label: "Drop fields matching", kind: "text" },
    { path: "exclude.names", label: "Drop fields named", kind: "csv" },
  ],
  filterByValue: [],
  reduce: [
    { path: "mode", label: "Mode", kind: "select", choices: c(["seriesToRows", "Series to rows"], ["reduceFields", "Reduce fields"]), default: "seriesToRows" },
    { path: "reducers", label: "Calculations", kind: "multi", choices: REDUCER_CHOICES },
    { path: "includeTimeField", label: "Include time field", kind: "boolean", when: { path: "mode", values: ["reduceFields"] } },
    { path: "labelsToFields", label: "Labels to fields", kind: "boolean", when: { path: "mode", values: [undefined, "seriesToRows"] } },
  ],
  calculateField: [
    { path: "mode", label: "Mode", kind: "select", choices: c(["reduceRow", "Reduce row"], ["binary", "Binary operation"], ["unary", "Unary operation"]), default: "reduceRow" },
    { path: "reduce.reducer", label: "Calculation", kind: "select", choices: REDUCER_CHOICES, default: "sum", when: { path: "mode", values: [undefined, "reduceRow"] } },
    { path: "reduce.include", label: "Fields", kind: "csv", hint: "Empty uses every number field.", when: { path: "mode", values: [undefined, "reduceRow"] } },
    { path: "binary.left", label: "Left", kind: "field", hint: "A field or a number.", when: { path: "mode", values: ["binary"] } },
    { path: "binary.operator", label: "Operator", kind: "select", choices: c(["+", "+"], ["-", "-"], ["*", "*"], ["/", "/"], ["%", "%"], ["^", "^"]), default: "+", when: { path: "mode", values: ["binary"] } },
    { path: "binary.right", label: "Right", kind: "field", when: { path: "mode", values: ["binary"] } },
    { path: "unary.operator", label: "Function", kind: "select", choices: c(["abs", "abs"], ["exp", "exp"], ["ln", "ln"], ["floor", "floor"], ["ceil", "ceil"]), default: "abs", when: { path: "mode", values: ["unary"] } },
    { path: "unary.fieldName", label: "Field", kind: "field", when: { path: "mode", values: ["unary"] } },
    { path: "alias", label: "Alias", kind: "text" },
    { path: "replaceFields", label: "Replace all fields", kind: "boolean" },
  ],
  joinByField: [
    { path: "byField", label: "Field", kind: "field", hint: "Empty joins on the time field." },
    { path: "mode", label: "Mode", kind: "select", choices: c(["outer", "Outer"], ["inner", "Inner"]), default: "outer" },
  ],
  merge: [],
  seriesToRows: [],
  labelsToFields: [
    { path: "mode", label: "Mode", kind: "select", choices: c(["columns", "Columns"], ["rows", "Rows"]), default: "columns" },
    { path: "valueLabel", label: "Value field name", kind: "text", hint: "A label whose value names the value field." },
    { path: "keepLabels", label: "Labels", kind: "csv", hint: "Empty keeps every label." },
  ],
  groupBy: [],
  sortBy: [
    { path: "sort.0.field", label: "Field", kind: "field" },
    { path: "sort.0.desc", label: "Descending", kind: "boolean" },
  ],
  limit: [{ path: "limitField", label: "Rows", kind: "number", min: 0, default: 10 }],
  convertFieldType: [
    { path: "conversions.0.targetField", label: "Field", kind: "field" },
    { path: "conversions.0.destinationType", label: "As", kind: "select", choices: c(["number", "Number"], ["string", "String"], ["time", "Time"], ["boolean", "Boolean"]) },
    { path: "conversions.0.dateFormat", label: "Date format", kind: "text", hint: "For text read as time, such as DD/MM/YYYY HH:mm; X is Unix seconds. Empty reads ISO dates and Unix milliseconds.", when: { path: "conversions.0.destinationType", values: ["time"] } },
  ],
};
