// Histogram: counts of values in equal buckets, per series or combined.

import type { Field } from "../data/types";
import { seriesColor, withAlpha } from "../data/colors";
import { displayName } from "../data/frames";
import { formatNumber } from "../data/display";
import { hiddenFromViz } from "../data/fieldConfig";
import { toNumber } from "../data/reducers";
import { echarts } from "./echarts";
import {
  axisStyle,
  grid,
  legendItem,
  textStyle,
  tooltipBase,
  tooltipHeader,
  tooltipMode,
  tooltipRow,
  type LegendItem,
  type VizContext,
  type VizResult,
} from "./common";

/** niceStep rounds a bucket size to 1, 2, or 5 times a power of ten. */
export function niceStep(raw: number): number {
  if (!(raw > 0) || !Number.isFinite(raw)) return 1;
  const pow = Math.pow(10, Math.floor(Math.log10(raw)));
  const n = raw / pow;
  return (n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10) * pow;
}

export function buildHistogram(ctx: VizContext): VizResult {
  const o = ctx.options;
  let series: { name: string; field: Field; values: number[]; color: string }[] = [];
  const legend: LegendItem[] = [];
  for (const frame of ctx.frames) {
    for (const field of frame.fields) {
      if (field.type !== "number" || hiddenFromViz(field)) continue;
      const name = displayName(field, frame);
      const color = seriesColor(field, ctx.palette);
      const values = field.values.map(toNumber).filter((v): v is number => v !== null);
      series.push({ name, field, values, color });
    }
  }
  if (!series.length) return { message: "Histogram needs a number field" };
  if (o.combine === true) {
    series = [{ name: "Count", field: series[0].field, values: series.flatMap((s) => s.values), color: series[0].color }];
  }
  for (const s of series) legend.push(legendItem(s.name, s.color, o.combine ? undefined : s.field, ctx));
  series = series.filter((s) => !ctx.hidden.has(s.name));
  const all = series.flatMap((s) => s.values);
  if (!all.length) return { message: "Histogram has no values to draw", legend };

  const min = Math.min(...all);
  const max = Math.max(...all);
  const count = Math.max(1, Math.floor(toNumber(o.bucketCount) ?? 30));
  const size = toNumber(o.bucketSize) && toNumber(o.bucketSize)! > 0 ? toNumber(o.bucketSize)! : niceStep((max - min || 1) / count);
  const offset = toNumber(o.bucketOffset) ?? 0;
  const start = Math.floor((min - offset) / size) * size + offset;
  const buckets = Math.min(5000, Math.max(1, Math.floor((max - start) / size) + 1));

  const counts = series.map((s) => {
    const c = new Array(buckets).fill(0);
    for (const v of s.values) c[Math.min(buckets - 1, Math.floor((v - start) / size))]++;
    return c;
  });
  const stacked = o.stacking?.mode === "normal" || o.stacking === "normal";
  const fill = (toNumber(o.fillOpacity) ?? 80) / 100;

  const data: [number, number, number, number, number][] = [];
  const base = new Array(buckets).fill(0);
  counts.forEach((c, si) => {
    for (let b = 0; b < buckets; b++) {
      if (!c[b]) continue;
      const lo = stacked ? base[b] : 0;
      data.push([start + b * size, start + (b + 1) * size, lo, lo + c[b], si]);
      if (stacked) base[b] += c[b];
    }
  });

  const style = axisStyle(ctx.palette);
  const unitField = series[0].field;
  const mode = tooltipMode(o);
  return {
    option: {
      animation: ctx.animation,
      textStyle: textStyle(ctx.palette),
      grid: grid(),
      xAxis: {
        type: "value",
        min: start,
        max: start + buckets * size,
        ...style,
        splitLine: { show: false },
        axisLabel: { ...style.axisLabel, hideOverlap: true, formatter: (v: number) => formatNumber({ ...unitField, config: { ...unitField.config, decimals: undefined } }, v) },
      },
      yAxis: { type: "value", ...style, minInterval: 1 },
      tooltip:
        mode === "none"
          ? { show: false }
          : {
              ...tooltipBase(ctx.palette),
              trigger: "item",
              formatter: (p: any) => {
                const d = data[p.dataIndex];
                if (!d) return "";
                const s = series[d[4]];
                return tooltipHeader(`${formatNumber(unitField, d[0])} to ${formatNumber(unitField, d[1])}`) + tooltipRow(s.color, s.name, String(d[3] - d[2]));
              },
            },
      series: [
        {
          type: "custom",
          animation: false,
          encode: { x: [0, 1], y: [2, 3] },
          data,
          renderItem: (params: any, api: any) => {
            const d = data[params.dataIndex];
            const a = api.coord([d[0], d[3]]);
            const b = api.coord([d[1], d[2]]);
            const rect = echarts.graphic.clipRectByRect(
              { x: a[0] + 0.5, y: a[1], width: Math.max(1, b[0] - a[0] - 1), height: Math.max(0, b[1] - a[1]) },
              { x: params.coordSys.x, y: params.coordSys.y, width: params.coordSys.width, height: params.coordSys.height },
            );
            if (!rect) return null;
            const color = series[d[4]].color;
            return { type: "rect", shape: rect, style: { fill: withAlpha(color, fill), stroke: color, lineWidth: 1 } };
          },
        },
      ],
    },
    legend,
  };
}
