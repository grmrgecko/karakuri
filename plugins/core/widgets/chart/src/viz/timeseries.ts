// Time series and trend. Each number field of a frame with a time field (or,
// for trend, the chosen x field) is one series; its custom field options pick
// how it draws.

import type { Field, Frame } from "../data/types";
import { fieldMinMax, gradientStops, resolveColor, seriesColor, sortedSteps, thresholdValue, withAlpha } from "../data/colors";
import { displayName, findField, timeField } from "../data/frames";
import { formatNumber } from "../data/display";
import { hiddenFromViz } from "../data/fieldConfig";
import { toNumber } from "../data/reducers";
import {
  axisStyle,
  grid,
  legendItem,
  textStyle,
  timeAxis,
  timeText,
  tooltipBase,
  tooltipHeader,
  tooltipMode,
  tooltipRow,
  type LegendItem,
  type VizContext,
  type VizResult,
} from "./common";

interface Drawn {
  name: string;
  field: Field;
  xField: Field;
  color: string;
  axis: number;
}

// AxisKey groups series that share a y axis: same placement and unit.
interface YAxis {
  key: string;
  placement: "left" | "right" | "hidden";
  field: Field;
  min: number;
  max: number;
  /** bounded axes span threshold bands, so the bands fill the plot. */
  bounded?: boolean;
}

function xFieldOf(frame: Frame, ctx: VizContext, trend: boolean): Field | undefined {
  if (!trend) return timeField(frame);
  const name = ctx.options.xField;
  if (name) {
    const hit = findField([frame], name);
    if (hit && hit.field.type === "number") return hit.field;
  }
  return frame.fields.find((f) => f.type === "number");
}

// points reads a field's (x, y) pairs, applying the null handling: a gap
// breaks the line, connected spans it, and a threshold spans only gaps
// shorter than it.
function points(x: Field, y: Field, spanNulls: unknown, negative: boolean): [number, number | null][] {
  const out: [number, number | null][] = [];
  const threshold = typeof spanNulls === "number" && spanNulls > 0 ? spanNulls : null;
  let prev: number | null = null;
  for (let i = 0; i < y.values.length; i++) {
    const t = toNumber(x.values[i]);
    if (t === null) continue;
    let v = toNumber(y.values[i]);
    if (v !== null && negative) v = -v;
    if (threshold !== null) {
      if (v === null) continue;
      if (prev !== null && t - prev > threshold) out.push([prev + (t - prev) / 2, null]);
      prev = t;
    }
    out.push([t, v]);
  }
  return out;
}

// sortedByX orders a trend's points by x, since a trend's x field need not
// arrive in order and a line drawn in arrival order doubles back on itself.
function sortedByX(points: [number, number | null][]): [number, number | null][] {
  return [...points].sort((a, b) => a[0] - b[0]);
}

const STEP: Record<string, string | false> = { stepBefore: "start", stepAfter: "end" };

export function buildTimeSeries(ctx: VizContext, trend = false): VizResult {
  const drawn: Drawn[] = [];
  const series: any[] = [];
  const axes: YAxis[] = [];
  const legend: LegendItem[] = [];
  let anyX = false;
  let anyNumber = false;

  for (const frame of ctx.frames) {
    const x = xFieldOf(frame, ctx, trend);
    if (x) anyX = true;
    for (const field of frame.fields) {
      if (field.type !== "number" || field === x || hiddenFromViz(field)) continue;
      anyNumber = true;
      if (!x) continue;
      const name = displayName(field, frame);
      const color = seriesColor(field, ctx.palette);
      const custom = field.config.custom ?? {};
      if (custom.hideFrom?.legend !== true) legend.push(legendItem(name, color, field, ctx));
      if (ctx.hidden.has(name)) continue;

      const placement = custom.axisPlacement === "right" ? "right" : custom.axisPlacement === "hidden" ? "hidden" : custom.axisPlacement === "left" ? "left" : "auto";
      const key = `${placement}\u0000${field.config.unit ?? ""}`;
      let axis = axes.findIndex((a) => a.key === key);
      if (axis < 0) {
        const resolved = placement === "auto" ? (axes.length === 0 ? "left" : "right") : placement;
        axes.push({ key, placement: resolved, field, min: Infinity, max: -Infinity });
        axis = axes.length - 1;
      }
      const raw = points(x, field, custom.spanNulls, custom.transform === "negative-Y");
      const data = trend ? sortedByX(raw) : raw;
      for (const [, v] of data) {
        if (v === null) continue;
        axes[axis].min = Math.min(axes[axis].min, v);
        axes[axis].max = Math.max(axes[axis].max, v);
      }
      drawn.push({ name, field, xField: x, color, axis });
      series.push(seriesOption(field, name, color, axis, data, ctx, trend));
    }
  }

  if (!anyX || !anyNumber) {
    const missing = [!anyX ? (trend ? "a number x field" : "a time field") : "", !anyNumber ? "a number field" : ""].filter(Boolean);
    return { message: `${trend ? "Trend" : "Time series"} needs ${missing.join(" and ")}`, legend: [] };
  }

  percentStack(series, drawn);
  const visualMap = thresholdMarks(series, drawn, axes, ctx);

  const style = axisStyle(ctx.palette);
  const fallback: YAxis = { key: "", placement: "left", field: undefined as unknown as Field, min: 0, max: 1 };
  const yAxis = (axes.length ? axes : [fallback]).map((a, i) => {
    const custom = a.field?.config.custom ?? {};
    const log = custom.scaleDistribution?.type === "log";
    const percent = custom.stacking?.mode === "percent";
    const softMin = toNumber(custom.axisSoftMin);
    const softMax = toNumber(custom.axisSoftMax);
    const hardMin = a.field?.config.min ?? null;
    const hardMax = a.field?.config.max ?? null;
    return {
      type: log ? "log" : "value",
      logBase: log ? (custom.scaleDistribution?.log ?? 10) : undefined,
      position: a.placement === "right" ? "right" : "left",
      show: a.placement !== "hidden",
      offset: i > 1 ? (i - 1) * 50 : 0,
      name: custom.axisLabel || undefined,
      nameLocation: "middle",
      nameGap: 40,
      min: percent ? 0 : hardMin ?? (a.bounded ? a.min : undefined) ?? (softMin !== null ? (v: { min: number }) => Math.min(v.min, softMin) : undefined),
      max: percent ? 100 : hardMax ?? (a.bounded ? a.max : undefined) ?? (softMax !== null ? (v: { max: number }) => Math.max(v.max, softMax) : undefined),
      scale: hardMin === null && softMin === null && !log,
      ...style,
      splitLine: { ...style.splitLine, show: i === 0 },
      axisLabel: {
        ...style.axisLabel,
        formatter: (v: number) => (percent ? `${Math.round(v)}%` : formatNumber(a.field ? { ...a.field, config: { ...a.field.config, decimals: undefined } } : undefined, v)),
      },
    };
  });

  const right = axes.filter((a) => a.placement === "right").length;
  const xStyle = axisStyle(ctx.palette);
  const xAxis = trend
    ? {
        type: "value",
        scale: true,
        ...xStyle,
        splitLine: { show: false },
        axisLabel: { ...xStyle.axisLabel, hideOverlap: true },
      }
    : timeAxis(ctx);

  return {
    option: {
      animation: ctx.animation,
      textStyle: textStyle(ctx.palette),
      grid: grid({ right: right > 1 ? 16 + (right - 1) * 50 : 16 }),
      xAxis,
      yAxis,
      series,
      visualMap: visualMap.length ? visualMap : undefined,
      tooltip: tooltip(ctx, drawn, trend),
    },
    legend,
    zoomable: !trend,
  };
}

function seriesOption(field: Field, name: string, color: string, axis: number, data: [number, number | null][], ctx: VizContext, trend: boolean): any {
  const c = field.config.custom ?? {};
  const draw = c.drawStyle === "bars" ? "bars" : c.drawStyle === "points" ? "points" : "line";
  const lineWidth = toNumber(c.lineWidth) ?? 1;
  const fillOpacity = (toNumber(c.fillOpacity) ?? 0) / 100;
  const pointSize = toNumber(c.pointSize) ?? 5;
  const stack = c.stacking?.mode && c.stacking.mode !== "none" ? `${c.stacking.group || "A"}:${axis}` : undefined;
  const showPoints = c.showPoints === "always" || draw === "points" || (c.showPoints !== "never" && data.length > 0 && data.length < ctx.width / (pointSize * 4));
  const dash = c.lineStyle?.fill === "dash" ? "dashed" : c.lineStyle?.fill === "dot" ? "dotted" : "solid";

  const base: any = {
    name,
    yAxisIndex: axis,
    data,
    stack,
    color,
    animation: ctx.animation,
    emphasis: { disabled: true },
    // Tooltip text is written by the panel's formatter from field options.
    tooltip: { show: c.hideFrom?.tooltip !== true },
  };

  if (draw === "bars") {
    // Bar alignment: -1 ends each bar at its timestamp, 1 starts
    // it there, 0 centres it. ECharts centres bars on their x value, so the
    // x values move by half the typical step between points.
    const align = toNumber(c.barAlignment) ?? 0;
    if (!trend && align !== 0 && data.length > 1) {
      const steps: number[] = [];
      for (let i = 1; i < data.length; i++) if (data[i][0] > data[i - 1][0]) steps.push(data[i][0] - data[i - 1][0]);
      steps.sort((a, b) => a - b);
      const half = (steps[Math.floor(steps.length / 2)] ?? 0) / 2;
      base.data = data.map(([x, y]) => [x + Math.sign(align) * half, y]);
    }
    return {
      ...base,
      type: "bar",
      barMaxWidth: 40,
      itemStyle: { color: fillOpacity > 0 ? withAlpha(color, Math.max(fillOpacity, 0.2)) : color, borderColor: color, borderWidth: lineWidth > 0 ? 1 : 0 },
    };
  }

  const out: any = {
    ...base,
    type: "line",
    showSymbol: showPoints,
    symbol: "circle",
    symbolSize: pointSize,
    smooth: c.lineInterpolation === "smooth" ? 0.4 : false,
    step: STEP[c.lineInterpolation] ?? false,
    connectNulls: c.spanNulls === true,
    sampling: data.length > ctx.width * 2 ? "lttb" : undefined,
    lineStyle: { width: draw === "points" ? 0 : lineWidth, type: dash, color },
    itemStyle: { color },
  };
  if (draw === "points") out.lineStyle.opacity = 0;
  if (fillOpacity > 0 || stack) {
    const mode = c.gradientMode ?? "none";
    const opacity = fillOpacity > 0 ? fillOpacity : 0;
    out.areaStyle =
      mode === "opacity" || mode === "hue"
        ? {
            color: {
              type: "linear",
              x: 0,
              y: 0,
              x2: 0,
              y2: 1,
              colorStops: [
                { offset: 0, color: withAlpha(color, opacity) },
                { offset: 1, color: withAlpha(color, 0) },
              ],
            },
          }
        : { color: withAlpha(color, opacity), opacity: opacity > 0 ? 1 : 0 };
  }
  if (c.gradientMode === "scheme" && !trend) out.__scheme = true;
  return out;
}

// percentStack rescales stacked series in percent mode to shares of each
// time's stack total.
function percentStack(series: any[], drawn: Drawn[]): void {
  const groups = new Map<string, number[]>();
  drawn.forEach((d, i) => {
    if (d.field.config.custom?.stacking?.mode !== "percent" || !series[i].stack) return;
    if (!groups.has(series[i].stack)) groups.set(series[i].stack, []);
    groups.get(series[i].stack)!.push(i);
  });
  for (const indexes of groups.values()) {
    const totals = new Map<number, number>();
    for (const i of indexes) {
      for (const [t, v] of series[i].data as [number, number | null][]) {
        if (v !== null) totals.set(t, (totals.get(t) ?? 0) + Math.abs(v));
      }
    }
    for (const i of indexes) {
      series[i].data = (series[i].data as [number, number | null][]).map(([t, v]) => {
        const total = totals.get(t) ?? 0;
        return [t, v === null || total === 0 ? null : (v / total) * 100];
      });
    }
  }
}

// thresholdMarks draws each axis's first series' thresholds as lines or
// bands, and colours scheme-gradient series by value.
function thresholdMarks(series: any[], drawn: Drawn[], axes: YAxis[], ctx: VizContext): any[] {
  const visualMaps: any[] = [];
  const marked = new Set<number>();
  drawn.forEach((d, i) => {
    const c = d.field.config.custom ?? {};
    const steps = sortedSteps(d.field.config.thresholds);
    const { min, max } = fieldMinMax(d.field);
    if (series[i].__scheme) {
      delete series[i].__scheme;
      const mode = d.field.config.color?.mode ?? "";
      if (mode === "thresholds") {
        const pieces = steps.map((s, k) => {
          const lo = thresholdValue(s, d.field.config.thresholds, min, max);
          const next = steps[k + 1];
          const hi = next ? thresholdValue(next, d.field.config.thresholds, min, max) : null;
          return { ...(lo !== null ? { gte: lo } : {}), ...(hi !== null ? { lt: hi } : {}), color: resolveColor(s.color, ctx.palette) };
        });
        visualMaps.push({ type: "piecewise", show: false, seriesIndex: i, dimension: 1, pieces });
      } else if (mode.startsWith("continuous-")) {
        visualMaps.push({ type: "continuous", show: false, seriesIndex: i, dimension: 1, min, max, inRange: { color: gradientStops(mode, ctx.palette) } });
      }
    }
    const style = c.thresholdsStyle?.mode ?? "off";
    if (style === "off" || marked.has(d.axis)) return;
    marked.add(d.axis);
    const values = steps.map((s) => thresholdValue(s, d.field.config.thresholds, min, max));
    const colors = steps.map((s) => resolveColor(s.color, ctx.palette));
    if (style.includes("line") || style.includes("dashed")) {
      series[i].markLine = {
        silent: true,
        symbol: "none",
        label: { show: false },
        animation: false,
        data: values
          .map((v, k) => (v === null ? null : { yAxis: v, lineStyle: { color: colors[k], type: style.includes("dashed") ? "dashed" : "solid", width: 1 } }))
          .filter(Boolean),
      };
    }
    if (style.includes("area")) {
      const axis = axes[d.axis];
      const known = values.filter((v): v is number => v !== null);
      const lo = Math.min(axis.min, ...known);
      const hi = Math.max(axis.max, ...known);
      const pad = (hi - lo || 1) * 0.1;
      const bottom = lo - pad;
      const top = hi + pad;
      series[i].markArea = {
        silent: true,
        animation: false,
        data: values.map((v, k) => [
          { yAxis: v ?? bottom, itemStyle: { color: withAlpha(colors[k], 0.15) } },
          { yAxis: values[k + 1] ?? top },
        ]),
      };
      axis.min = bottom;
      axis.max = top;
      axis.bounded = true;
    }
  });
  return visualMaps;
}

function tooltip(ctx: VizContext, drawn: Drawn[], trend: boolean): any {
  const mode = tooltipMode(ctx.options);
  if (mode === "none") return { show: false };
  const sort = ctx.options.tooltip?.sort;
  return {
    ...tooltipBase(ctx.palette),
    trigger: "axis",
    axisPointer: { type: "line", lineStyle: { color: ctx.palette.textMuted } },
    formatter: (raw: any) => {
      let params: any[] = Array.isArray(raw) ? raw : [raw];
      params = params.filter((p) => drawn[p.seriesIndex] && drawn[p.seriesIndex].field.config.custom?.hideFrom?.tooltip !== true);
      if (!params.length) return "";
      if (mode === "single") params = ctx.nearest(params);
      if (sort === "asc" || sort === "desc") {
        params = [...params].sort((a, b) => {
          const x = toNumber(a.value?.[1]) ?? -Infinity;
          const y = toNumber(b.value?.[1]) ?? -Infinity;
          return sort === "asc" ? x - y : y - x;
        });
      }
      const x = params[0].value?.[0];
      const head = trend ? formatNumber(drawn[params[0].seriesIndex].xField, x) : timeText(x);
      const rows = params.map((p) => {
        const d = drawn[p.seriesIndex];
        return tooltipRow(d.color, d.name, formatNumber(d.field, p.value?.[1]));
      });
      return tooltipHeader(head) + rows.join("");
    },
  };
}
