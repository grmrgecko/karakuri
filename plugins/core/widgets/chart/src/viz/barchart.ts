// Bar chart: one category per row of a frame's x field (a string field by
// default, else time), one bar per number field.

import type { Field, Frame } from "../data/types";
import { seriesColor, valueColor } from "../data/colors";
import { displayName, findField } from "../data/frames";
import { formatNumber } from "../data/display";
import { hiddenFromViz } from "../data/fieldConfig";
import { toNumber } from "../data/reducers";
import { isoDateTime } from "../data/units";
import {
  axisStyle,
  grid,
  legendItem,
  showValues,
  textStyle,
  tooltipBase,
  tooltipHeader,
  tooltipMode,
  tooltipRow,
  type LegendItem,
  type VizContext,
  type VizResult,
} from "./common";

function pickFrame(ctx: VizContext): { frame: Frame; x: Field } | null {
  const name = ctx.options.xField;
  if (name) {
    const hit = findField(ctx.frames, name);
    if (hit) return { frame: hit.frame, x: hit.field };
  }
  for (const frame of ctx.frames) {
    const x = frame.fields.find((f) => f.type === "string");
    if (x) return { frame, x };
  }
  for (const frame of ctx.frames) {
    const x = frame.fields.find((f) => f.type === "time");
    if (x) return { frame, x };
  }
  return null;
}

export function buildBarChart(ctx: VizContext): VizResult {
  const picked = pickFrame(ctx);
  if (!picked) return { message: "Bar chart needs a string or time field for the x axis" };
  const { frame, x } = picked;
  const values = frame.fields.filter((f) => f.type === "number" && f !== x && !hiddenFromViz(f));
  if (!values.length) return { message: "Bar chart needs a number field" };

  const o = ctx.options;
  const categories = x.values.map((v) => (x.type === "time" ? isoDateTime(Number(v)) : String(v ?? "")));
  const horizontal = o.orientation === "horizontal" || (o.orientation === "auto" && ctx.width < ctx.height * 0.8);
  const stacking = o.stacking === "normal" || o.stacking === "percent" ? o.stacking : "none";
  const groupWidth = clamp(toNumber(o.groupWidth) ?? 0.7, 0, 1);
  const barWidth = clamp(toNumber(o.barWidth) ?? 0.97, 0.05, 1);
  const crowded = categories.length * values.length > Math.max(ctx.width, ctx.height) / 24;
  const labels = showValues(o.showValue, crowded);

  const totals = categories.map((_, r) => values.reduce((sum, f) => sum + Math.abs(toNumber(f.values[r]) ?? 0), 0));
  const legend: LegendItem[] = [];
  const drawn: { name: string; field: Field; color: string }[] = [];
  const series: any[] = [];
  for (const field of values) {
    const name = displayName(field, frame);
    const color = seriesColor(field, ctx.palette);
    legend.push(legendItem(name, color, field, ctx));
    if (ctx.hidden.has(name)) continue;
    const byValue = field.config.color?.mode === "thresholds" || field.config.color?.mode?.startsWith("continuous-");
    drawn.push({ name, field, color });
    series.push({
      type: "bar",
      name,
      animation: ctx.animation,
      stack: stacking === "none" ? undefined : "total",
      barCategoryGap: `${Math.round((1 - groupWidth) * 100)}%`,
      barGap: `${Math.round(((1 - barWidth) / barWidth) * 100)}%`,
      itemStyle: { color, borderRadius: stacking === "none" ? 2 : 0 },
      emphasis: { disabled: true },
      label: {
        show: labels,
        position: stacking === "none" ? (horizontal ? "right" : "top") : "inside",
        color: ctx.palette.text,
        fontSize: 11,
        formatter: (p: any) => formatNumber(field, field.values[p.dataIndex]),
      },
      data: field.values.map((v, r) => {
        let n = toNumber(v);
        if (n !== null && stacking === "percent") n = totals[r] ? (n / totals[r]) * 100 : 0;
        return byValue ? { value: n, itemStyle: { color: valueColor(field, toNumber(v), ctx.palette) } } : n;
      }),
    });
  }

  const style = axisStyle(ctx.palette);
  const category = {
    type: "category",
    data: categories,
    ...style,
    splitLine: { show: false },
    axisLabel: { ...style.axisLabel, rotate: toNumber(o.xTickLabelRotation) ?? 0, hideOverlap: true },
  };
  const value = {
    type: "value",
    ...style,
    max: stacking === "percent" ? 100 : (values[0].config.max ?? undefined),
    min: values[0].config.min ?? undefined,
    axisLabel: {
      ...style.axisLabel,
      formatter: (v: number) => (stacking === "percent" ? `${Math.round(v)}%` : formatNumber({ ...values[0], config: { ...values[0].config, decimals: undefined } }, v)),
    },
  };

  const mode = tooltipMode(o);
  return {
    option: {
      animation: ctx.animation,
      textStyle: textStyle(ctx.palette),
      grid: grid(),
      xAxis: horizontal ? value : category,
      yAxis: horizontal ? { ...category, inverse: true } : value,
      series,
      tooltip:
        mode === "none"
          ? { show: false }
          : {
              ...tooltipBase(ctx.palette),
              trigger: mode === "multi" ? "axis" : "item",
              axisPointer: { type: "shadow" },
              formatter: (raw: any) => {
                const params: any[] = Array.isArray(raw) ? raw : [raw];
                if (!params.length) return "";
                const row = params[0].dataIndex;
                return (
                  tooltipHeader(categories[row] ?? "") +
                  params.map((p) => tooltipRow(drawn[p.seriesIndex].color, drawn[p.seriesIndex].name, formatNumber(drawn[p.seriesIndex].field, drawn[p.seriesIndex].field.values[p.dataIndex]))).join("")
                );
              },
            },
    },
    legend,
  };
}

function clamp(n: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, n));
}
