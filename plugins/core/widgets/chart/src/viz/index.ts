// The ECharts visualizations by ID. Stat, bar gauge, and table are Svelte
// components drawn by ChartView instead.

import type { VizContext, VizResult } from "./common";
import { buildBarChart } from "./barchart";
import { buildCandlestick } from "./candlestick";
import { buildGauge } from "./gauge";
import { buildHeatmap } from "./heatmap";
import { buildHistogram } from "./histogram";
import { buildPieChart } from "./piechart";
import { buildTimeline } from "./timeline";
import { buildTimeSeries } from "./timeseries";
import { buildXYChart } from "./xychart";

const BUILDERS: Record<string, (ctx: VizContext) => VizResult> = {
  timeseries: (ctx) => buildTimeSeries(ctx, false),
  trend: (ctx) => buildTimeSeries(ctx, true),
  barchart: buildBarChart,
  gauge: buildGauge,
  piechart: buildPieChart,
  "state-timeline": (ctx) => buildTimeline(ctx, false),
  "status-history": (ctx) => buildTimeline(ctx, true),
  heatmap: buildHeatmap,
  histogram: buildHistogram,
  candlestick: buildCandlestick,
  xychart: buildXYChart,
};

/** HTML_VIZ are drawn as Svelte components rather than ECharts. */
export const HTML_VIZ = new Set(["stat", "bargauge", "table"]);

/** build draws a visualization's ECharts option, or the message shown instead. */
export function build(viz: string, ctx: VizContext): VizResult {
  const builder = BUILDERS[viz] ?? BUILDERS.timeseries;
  try {
    return builder(ctx);
  } catch (e: any) {
    return { message: `This visualization could not draw the data: ${e?.message ?? e}` };
  }
}
