// Shared shapes of the chart's data pipeline. The JSON names of the server
// types follow pkg/tsdb.

export type FieldType = "time" | "number" | "string" | "boolean" | "other";

/** ServerField is one column of a pkg/tsdb Series. */
export interface ServerField {
  name: string;
  type: string;
  labels?: Record<string, string>;
  unit?: string;
  values: unknown[];
}

/** ServerSeries is a pkg/tsdb Series. */
export interface ServerSeries {
  name: string;
  ref_id?: string;
  labels?: Record<string, string>;
  unit?: string;
  fields: ServerField[];
}

/** ServerResult is a pkg/tsdb Result. */
export interface ServerResult {
  series: ServerSeries[] | null;
  truncated?: boolean;
  executed_query?: string;
  interval_ms?: number;
}

/** QueryError is a failed query's class and the database's message. */
export interface QueryError {
  class?: string;
  message: string;
}

/** QueryResponse is one query's answer in the data route. */
export interface QueryResponse {
  ref_id: string;
  result?: ServerResult | null;
  error?: QueryError | null;
  cached?: boolean;
}

/** ChartResponse is the data route's answer. */
export interface ChartResponse {
  results: QueryResponse[];
  range?: { from: number | string; to: number | string };
  interval_ms?: number;
}

export interface ThresholdStep {
  /** value is null for the base step, which covers everything below the next. */
  value: number | null;
  color: string;
}

export interface Thresholds {
  mode: "absolute" | "percentage";
  steps: ThresholdStep[];
}

export interface MappingResult {
  text?: string;
  color?: string;
  index?: number;
}

export type ValueMapping =
  | { type: "value"; options: Record<string, MappingResult> }
  | { type: "range"; options: { from: number | null; to: number | null; result: MappingResult } }
  | { type: "regex"; options: { pattern: string; result: MappingResult } }
  | { type: "special"; options: { match: SpecialMatch; result: MappingResult } };

export type SpecialMatch = "null" | "nan" | "null+nan" | "true" | "false" | "empty";

export interface FieldColor {
  /** mode is fixed, palette-classic, thresholds, or a continuous-* gradient. */
  mode: string;
  fixedColor?: string;
  /** seriesBy picks the value a gradient colours a whole series by. */
  seriesBy?: "last" | "min" | "max";
}

/** FieldConfig is the standard field options plus visualization custom options. */
export interface FieldConfig {
  displayName?: string;
  unit?: string;
  decimals?: number | null;
  min?: number | null;
  max?: number | null;
  noValue?: string;
  color?: FieldColor;
  thresholds?: Thresholds;
  mappings?: ValueMapping[];
  custom?: Record<string, any>;
}

export interface OverrideMatcher {
  /** id is byName, byNames, byRegexp, byFrameRefID, or byType. */
  id: string;
  options?: any;
}

export interface OverrideProperty {
  /** id is a standard option name or custom.<option>. */
  id: string;
  value?: any;
}

export interface Override {
  matcher: OverrideMatcher;
  properties: OverrideProperty[];
}

export interface FieldConfigSource {
  defaults: FieldConfig;
  overrides: Override[];
}

/** FieldState is what the pipeline derives about a field. */
export interface FieldState {
  /** displayName is the name the series had before any rename. */
  displayName?: string;
  seriesName?: string;
  /** refId is the query the field came from, if any. */
  refId?: string;
  /** seriesIndex orders series for palette colours. */
  seriesIndex?: number;
  /** range is the min and max the field's thresholds and gradients use. */
  range?: { min: number; max: number };
}

export interface Field {
  name: string;
  type: FieldType;
  values: unknown[];
  labels?: Record<string, string>;
  config: FieldConfig;
  state: FieldState;
}

export interface Frame {
  name?: string;
  refId?: string;
  fields: Field[];
}

/** Transformation is one step of the transformation list. */
export interface Transformation {
  id: string;
  options?: any;
  disabled?: boolean;
}

/** TimeRange is a resolved range in Unix milliseconds. */
export interface TimeRange {
  from: number;
  to: number;
}

/** Palette is the colours a chart draws with, read from the theme. */
export interface Palette {
  /** series is the palette-classic order. */
  series: string[];
  /** named maps colour names to theme colours. */
  named: Record<string, string>;
  text: string;
  textMuted: string;
  border: string;
  background: string;
  surface: string;
  font: string;
}
