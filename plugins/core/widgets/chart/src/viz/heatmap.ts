// Heatmap: cells of time by bucket, coloured by count or value. It either
// buckets time series values itself or reads buckets from the fields of a
// frame, such as Prometheus histogram series named by their le bound.

import type { Field, Frame } from "../data/types";
import { GRADIENTS, gradientStops, resolveColor, withAlpha } from "../data/colors";
import { displayName, timeField } from "../data/frames";
import { formatNumber } from "../data/display";
import { toNumber } from "../data/reducers";
import { formatValue, formattedText } from "../data/units";
import { parseDuration } from "../data/time";
import { echarts } from "./echarts";
import {
  axisStyle,
  contrastText,
  escapeHtml,
  grid,
  showValues,
  textStyle,
  timeAxis,
  timeText,
  tooltipBase,
  tooltipHeader,
  tooltipMode,
  type VizContext,
  type VizResult,
} from "./common";

interface Cell {
  x0: number;
  x1: number;
  y: number;
  value: number;
}

export function buildHeatmap(ctx: VizContext): VizResult {
  const o = ctx.options;
  const frames = ctx.frames.filter((f) => timeField(f) && f.fields.some((x) => x.type === "number"));
  if (!frames.length) return { message: "Heatmap needs a time field and a number field" };

  const calculate = o.calculate === true || !looksBucketed(frames.flatMap((f) => f.fields.filter((x) => x.type === "number").map((x) => bucketName(x, f))));
  const built = calculate ? calculated(ctx, frames) : bucketed(frames, o.cumulative === true || o.cumulative === "true" ? true : o.cumulative === false || o.cumulative === "false" ? false : undefined);
  if (!built.cells.length) return { message: "Heatmap has no values to draw" };
  const { cells, rows, valueField } = built;

  const color = o.color ?? {};
  const lo = toNumber(color.min) ?? Math.min(...cells.map((c) => c.value));
  let hi = toNumber(color.max) ?? Math.max(...cells.map((c) => c.value));
  if (hi <= lo) hi = lo + 1;
  let stops =
    color.mode === "opacity"
      ? [withAlpha(resolveColor(color.fill ?? "orange", ctx.palette), 0.05), resolveColor(color.fill ?? "orange", ctx.palette)]
      : gradientStops(GRADIENTS[color.scheme] ? color.scheme : "continuous-BlYlRd", ctx.palette);
  if (color.reverse) stops = [...stops].reverse();
  const gap = toNumber(o.cellGap) ?? 1;
  const labels = showValues(o.showValue, cells.length > (ctx.width * ctx.height) / 900);
  const cellUnit = o.cellValues?.unit;
  const cellDecimals = o.cellValues?.decimals;
  const cellText = (v: number) => formattedText(formatValue(v, cellUnit, cellDecimals ?? (calculate ? 0 : undefined)));
  const style = axisStyle(ctx.palette);
  const showLegend = o.legend?.show !== false;
  const mode = tooltipMode(o);

  return {
    option: {
      animation: ctx.animation,
      textStyle: textStyle(ctx.palette),
      grid: grid({ bottom: showLegend ? 40 : 8 }),
      xAxis: timeAxis(ctx),
      yAxis: {
        type: "category",
        data: rows,
        inverse: o.yAxis?.reverse === true,
        position: o.yAxis?.axisPlacement === "right" ? "right" : "left",
        show: o.yAxis?.axisPlacement !== "hidden",
        ...style,
        splitLine: { show: false },
      },
      visualMap: {
        type: "continuous",
        show: showLegend,
        min: lo,
        max: hi,
        dimension: 3,
        orient: "horizontal",
        left: "center",
        bottom: 0,
        itemHeight: Math.min(240, ctx.width * 0.5),
        itemWidth: 10,
        calculable: false,
        inRange: { color: stops },
        textStyle: { color: ctx.palette.textMuted, fontSize: 10 },
        formatter: (v: number) => cellText(v),
      },
      tooltip:
        mode === "none"
          ? { show: false }
          : {
              ...tooltipBase(ctx.palette),
              trigger: "item",
              formatter: (p: any) => {
                const c = cells[p.dataIndex];
                if (!c) return "";
                return (
                  tooltipHeader(timeText(c.x0)) +
                  `<div>${escapeHtml(rows[c.y])}: <b>${escapeHtml(calculate ? cellText(c.value) : formatNumber(valueField, c.value))}</b></div>`
                );
              },
            },
      series: [
        {
          type: "custom",
          animation: false,
          encode: { x: [0, 1], y: 2 },
          data: cells.map((c) => [c.x0, c.x1, c.y, c.value]),
          renderItem: (params: any, api: any) => {
            const c = cells[params.dataIndex];
            if (!c) return null;
            const a = api.coord([api.value(0), api.value(2)]);
            const b = api.coord([api.value(1), api.value(2)]);
            const h = api.size([0, 1])[1];
            const rect = echarts.graphic.clipRectByRect(
              { x: a[0] + gap / 2, y: a[1] - h / 2 + gap / 2, width: Math.max(1, b[0] - a[0] - gap), height: Math.max(1, h - gap) },
              { x: params.coordSys.x, y: params.coordSys.y, width: params.coordSys.width, height: params.coordSys.height },
            );
            if (!rect) return null;
            const fill = api.visual("color");
            const children: any[] = [{ type: "rect", shape: rect, style: { fill } }];
            const text = cellText(c.value);
            if (labels && rect.width > text.length * 6 + 4 && rect.height > 12) {
              children.push({
                type: "text",
                silent: true,
                style: {
                  text,
                  x: rect.x + rect.width / 2,
                  y: rect.y + rect.height / 2,
                  align: "center",
                  verticalAlign: "middle",
                  fontSize: 10,
                  fill: typeof fill === "string" ? contrastText(fill, ctx.palette) : ctx.palette.text,
                },
              });
            }
            return { type: "group", children };
          },
        },
      ],
    },
    zoomable: true,
  };
}

// looksBucketed reports whether field names read as bucket bounds.
function looksBucketed(names: string[]): boolean {
  return names.length > 1 && names.every((n) => /^[+-]?(\d+(\.\d+)?([eE][+-]?\d+)?|Inf|\+Inf)$/.test(n.trim()));
}

function bound(name: string): number {
  const t = name.trim();
  if (t === "+Inf" || t === "Inf") return Infinity;
  return Number(t);
}

// bucketName is a bucket field's upper bound: its le label, as a Prometheus
// histogram series carries it, else its display name.
function bucketName(field: Field, frame: Frame): string {
  return field.labels?.le ?? frame.labels?.le ?? displayName(field, frame);
}

// bucketed draws one row per bucket field. Prometheus histogram buckets are
// cumulative (each le counts everything at or below it), so when the fields
// carry le labels each bucket's count at a time is its value minus the next
// lower bucket's. cumulative
// false keeps the values as they are; true forces the subtraction.
function bucketed(frames: Frame[], cumulative?: boolean): { cells: Cell[]; rows: string[]; valueField?: Field } {
  const columns: { name: string; time: Field; field: Field; le: boolean }[] = [];
  for (const frame of frames) {
    const time = timeField(frame)!;
    for (const field of frame.fields) {
      if (field.type !== "number") continue;
      const le = field.labels?.le ?? frame.labels?.le;
      columns.push({ name: bucketName(field, frame), time, field, le: le !== undefined });
    }
  }
  columns.sort((a, b) => bound(a.name) - bound(b.name));
  const subtract = cumulative ?? (columns.length > 1 && columns.every((c) => c.le));
  let below = new Map<number, number>();
  const cells: Cell[] = [];
  columns.forEach((col, y) => {
    const times = col.time.values.map((t) => toNumber(t));
    const here = new Map<number, number>();
    for (let i = 0; i < col.field.values.length; i++) {
      const t = times[i];
      let v = toNumber(col.field.values[i]);
      if (t === null || v === null) continue;
      here.set(t, v);
      if (subtract) v = Math.max(0, v - (below.get(t) ?? 0));
      const next = times[i + 1] ?? (i > 0 && times[i - 1] !== null ? t + (t - (times[i - 1] as number)) : t + 60000);
      cells.push({ x0: t, x1: next, y, value: v });
    }
    below = here;
  });
  return { cells, rows: columns.map((c) => c.name), valueField: columns[0]?.field };
}

function calculated(ctx: VizContext, frames: Frame[]): { cells: Cell[]; rows: string[]; valueField?: Field } {
  const o = ctx.options.calculation ?? {};
  const points: { t: number; v: number }[] = [];
  let unitField: Field | undefined;
  for (const frame of frames) {
    const time = timeField(frame)!;
    for (const field of frame.fields) {
      if (field.type !== "number") continue;
      unitField ??= field;
      for (let i = 0; i < field.values.length; i++) {
        const t = toNumber(time.values[i]);
        const v = toNumber(field.values[i]);
        if (t !== null && v !== null) points.push({ t, v });
      }
    }
  }
  if (!points.length) return { cells: [], rows: [] };

  // x buckets: a size such as 5m, a count across the range, or one per
  // pixel-ish column by default.
  const span = ctx.range.to - ctx.range.from;
  let xSize: number;
  if (o.xBuckets?.mode === "size" && parseDuration(String(o.xBuckets.value ?? ""))) xSize = parseDuration(String(o.xBuckets.value))!;
  else if (o.xBuckets?.mode === "count" && Number(o.xBuckets.value) > 0) xSize = span / Number(o.xBuckets.value);
  else xSize = Math.max(span / Math.max(10, Math.floor(ctx.width / 12)), 1000);

  const log = o.yBuckets?.scale?.type === "log";
  const base = Number(o.yBuckets?.scale?.log) || 2;
  const tr = (v: number) => (log ? Math.log(Math.max(v, 1e-9)) / Math.log(base) : v);
  const inv = (v: number) => (log ? Math.pow(base, v) : v);
  const ys = points.map((p) => tr(p.v));
  const yMin = Math.min(...ys);
  const yMax = Math.max(...ys);
  let ySize: number;
  if (o.yBuckets?.mode === "size" && Number(o.yBuckets.value) > 0) ySize = log ? 1 : Number(o.yBuckets.value);
  else {
    const count = o.yBuckets?.mode === "count" && Number(o.yBuckets.value) > 0 ? Number(o.yBuckets.value) : 10;
    ySize = (yMax - yMin || 1) / count;
  }
  const yStart = Math.floor(yMin / ySize) * ySize;
  const yCount = Math.max(1, Math.ceil((yMax - yStart) / ySize + 1e-9));

  const counts = new Map<string, number>();
  for (const p of points) {
    const x = Math.floor((p.t - ctx.range.from) / xSize);
    const y = Math.min(yCount - 1, Math.floor((tr(p.v) - yStart) / ySize));
    const key = `${x}:${y}`;
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  const cells: Cell[] = [];
  for (const [key, n] of counts) {
    const [x, y] = key.split(":").map(Number);
    const x0 = ctx.range.from + x * xSize;
    cells.push({ x0, x1: x0 + xSize, y, value: n });
  }
  const rows: string[] = [];
  for (let y = 0; y < yCount; y++) rows.push(formatNumber(unitField ? { ...unitField, config: { ...unitField.config, decimals: undefined } } : undefined, inv(yStart + y * ySize)));
  return { cells, rows, valueField: unitField };
}
