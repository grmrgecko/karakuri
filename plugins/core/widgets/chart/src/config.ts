import { parseAttr } from "../../shared/dashboardLive";
import type { FieldConfigSource, Transformation } from "./data/types";

/** MAX_QUERIES matches the server, which runs no more than this many per chart. */
export const MAX_QUERIES = 10;

/** REF_IDS are the default reference IDs, one per query slot. */
export const REF_IDS = ["A", "B", "C", "D", "E", "F", "G", "H", "I", "J"];

/** MIN_REFRESH_MS is the shortest refresh the server's cache honours. */
export const MIN_REFRESH_MS = 5000;

/** VIZ lists the visualizations in picker order. */
export const VIZ = [
  { id: "timeseries", name: "Time series" },
  { id: "trend", name: "Trend" },
  { id: "barchart", name: "Bar chart" },
  { id: "stat", name: "Stat" },
  { id: "gauge", name: "Gauge" },
  { id: "bargauge", name: "Bar gauge" },
  { id: "table", name: "Table" },
  { id: "piechart", name: "Pie chart" },
  { id: "state-timeline", name: "State timeline" },
  { id: "status-history", name: "Status history" },
  { id: "heatmap", name: "Heatmap" },
  { id: "histogram", name: "Histogram" },
  { id: "candlestick", name: "Candlestick" },
  { id: "xychart", name: "XY chart" },
] as const;

export type VizId = (typeof VIZ)[number]["id"];

/** HAQuery is pkg/tsdb's HAQuery, stored with its JSON names. */
export interface HAQuery {
  entities?: string[];
  attribute?: string;
  statistic_ids?: string[];
  period?: string;
  types?: string[];
}

/** EnergyQuery is pkg/tsdb's EnergyQuery: targets, a measure, and a period. */
export interface EnergyQuery {
  targets?: string[];
  measure?: string;
  period?: string;
}

/** ENERGY_CONNECTION is the built-in connection energy queries name. */
export const ENERGY_CONNECTION = "energy";

/** ChartQuery is one query of a placement. The server reads it from the saved layout. */
export interface ChartQuery {
  refId: string;
  connectionId: string;
  language: string;
  text: string;
  ha: HAQuery | null;
  energy?: EnergyQuery | null;
  legendFormat: string;
  maxDataPoints: number;
  minInterval: string;
  instant: boolean;
  format: "time_series" | "table";
  hide: boolean;
}

// ChartConfig is a placement's chart. Unknown keys are kept, so a field a
// later version adds survives an edit in this one.
export interface ChartConfig {
  viz: VizId;
  queries: ChartQuery[];
  timeRange: string;
  relativeTime: string;
  timeShift: string;
  refresh: string;
  maxDataPoints: number;
  fieldConfig: FieldConfigSource;
  transformations: Transformation[];
  options: Record<string, any>;
  [key: string]: unknown;
}

/** newQuery returns an empty query with the first free reference ID. */
export function newQuery(existing: ChartQuery[]): ChartQuery {
  const used = new Set(existing.map((q) => q.refId));
  const refId = REF_IDS.find((r) => !used.has(r)) ?? `Q${existing.length + 1}`;
  return {
    refId,
    connectionId: "",
    language: "",
    text: "",
    ha: null,
    legendFormat: "",
    maxDataPoints: 0,
    minInterval: "",
    instant: false,
    format: "time_series",
    hide: false,
  };
}

function str(v: unknown, fallback = ""): string {
  return typeof v === "string" ? v : typeof v === "number" ? String(v) : fallback;
}

function num(v: unknown, fallback = 0): number {
  const n = Number(v);
  return Number.isFinite(n) && n >= 0 ? n : fallback;
}

function obj(v: unknown): Record<string, any> {
  return v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, any>) : {};
}

function strings(v: unknown): string[] | undefined {
  return Array.isArray(v) ? v.filter((x) => typeof x === "string") : undefined;
}

function parseQuery(v: unknown, index: number): ChartQuery {
  const q = obj(v);
  const ha = q.ha && typeof q.ha === "object" ? obj(q.ha) : null;
  return {
    ...q,
    refId: str(q.refId) || REF_IDS[index] || `Q${index + 1}`,
    connectionId: str(q.connectionId),
    language: str(q.language),
    text: str(q.text),
    ha: ha
      ? {
          entities: strings(ha.entities),
          attribute: str(ha.attribute) || undefined,
          statistic_ids: strings(ha.statistic_ids),
          period: str(ha.period) || undefined,
          types: strings(ha.types),
        }
      : null,
    legendFormat: str(q.legendFormat),
    maxDataPoints: Math.floor(num(q.maxDataPoints)),
    minInterval: str(q.minInterval),
    instant: q.instant === true,
    format: q.format === "table" ? "table" : "time_series",
    hide: q.hide === true,
  };
}

/** parseConfig reads the host's config attribute, falling back field by field. */
export function parseConfig(attr: string | Record<string, unknown>): ChartConfig {
  const c = parseAttr(attr);
  const viz = VIZ.some((v) => v.id === c.viz) ? (c.viz as VizId) : "timeseries";
  const queries = Array.isArray(c.queries) ? c.queries.slice(0, MAX_QUERIES).map(parseQuery) : [];
  const fc = obj(c.fieldConfig);
  return {
    ...c,
    viz,
    queries: queries.length ? queries : [newQuery([])],
    timeRange: str(c.timeRange, "now-6h,now"),
    relativeTime: str(c.relativeTime),
    timeShift: str(c.timeShift),
    refresh: str(c.refresh, "30s"),
    maxDataPoints: Math.floor(num(c.maxDataPoints)),
    fieldConfig: {
      defaults: obj(fc.defaults),
      overrides: Array.isArray(fc.overrides) ? fc.overrides.filter((o: unknown) => o && typeof o === "object") : [],
    },
    transformations: Array.isArray(c.transformations)
      ? c.transformations.filter((t: unknown) => t && typeof t === "object" && typeof (t as any).id === "string")
      : [],
    options: obj(c.options),
  };
}

/** LANGUAGES lists the query languages each connection kind accepts. */
export const LANGUAGES: Record<string, { id: string; name: string }[]> = {
  prometheus: [{ id: "promql", name: "PromQL" }],
  influxdb: [
    { id: "influxql", name: "InfluxQL" },
    { id: "flux", name: "Flux" },
    { id: "sql", name: "SQL" },
  ],
  postgres: [{ id: "sql", name: "SQL" }],
  homeassistant: [
    { id: "ha_history", name: "History" },
    { id: "ha_statistics", name: "Statistics" },
  ],
  energy: [{ id: "energy", name: "Energy" }],
};

/** CHART_KINDS are the connection kinds a chart can query. */
export const CHART_KINDS = Object.keys(LANGUAGES);
