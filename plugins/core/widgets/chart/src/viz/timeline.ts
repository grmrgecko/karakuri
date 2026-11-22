// State timeline and status history: one row per field, coloured spans or
// cells labelled with each value's display text (its mapping, else the value
// in its unit).

import type { Field } from "../data/types";
import { withAlpha } from "../data/colors";
import { display, displayText, type DisplayValue } from "../data/display";
import { displayName, timeField } from "../data/frames";
import { hiddenFromViz } from "../data/fieldConfig";
import { toNumber } from "../data/reducers";
import { duration } from "../data/units";
import { echarts } from "./echarts";
import {
  axisStyle,
  contrastText,
  grid,
  showValues,
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

interface Span {
  start: number;
  end: number;
  row: number;
  value: DisplayValue;
  field: Field;
}

export function buildTimeline(ctx: VizContext, status: boolean): VizResult {
  const o = ctx.options;
  const rows: string[] = [];
  const spans: Span[] = [];
  let anyTime = false;

  for (const frame of ctx.frames) {
    const time = timeField(frame);
    if (!time) continue;
    anyTime = true;
    const times = time.values.map((t) => toNumber(t));
    for (const field of frame.fields) {
      if (field === time || field.type === "time" || hiddenFromViz(field)) continue;
      const row = rows.length;
      rows.push(displayName(field, frame));
      const cells: Span[] = [];
      for (let i = 0; i < field.values.length; i++) {
        const t = times[i];
        // A missing value is a gap, not a state of its own.
        if (t === null || field.values[i] === null || field.values[i] === undefined) continue;
        let next = i + 1 < times.length ? times[i + 1] : null;
        if (next === null) next = status ? t + typicalStep(times) : Math.max(t, ctx.range.to);
        cells.push({ start: t, end: next, row, value: display(field, field.values[i], ctx.palette), field });
      }
      if (!status && o.mergeValues !== false) {
        for (const c of cells) {
          const last = spans[spans.length - 1];
          if (last && last.row === row && last.end === c.start && displayText(last.value) === displayText(c.value) && last.value.color === c.value.color) {
            last.end = c.end;
          } else {
            spans.push(c);
          }
        }
      } else {
        spans.push(...cells);
      }
    }
  }

  const name = status ? "Status history" : "State timeline";
  if (!anyTime) return { message: `${name} needs a time field` };
  if (!rows.length) return { message: `${name} needs a field besides time` };

  const rowHeight = clamp(toNumber(o.rowHeight) ?? 0.9, 0.1, 1);
  const colWidth = clamp(toNumber(o.colWidth) ?? 0.9, 0.1, 1);
  const lineWidth = toNumber(o.lineWidth) ?? 0;
  const fillOpacity = (toNumber(o.fillOpacity) ?? 70) / 100;
  const align = o.alignValue === "center" || o.alignValue === "right" ? o.alignValue : "left";
  const labels = showValues(o.showValue, spans.length > ctx.width / 6);

  // The legend lists each distinct value once.
  const legend: LegendItem[] = [];
  for (const s of spans) {
    const text = displayText(s.value);
    if (legend.length >= 24 || legend.some((l) => l.name === text)) continue;
    legend.push({ name: text, color: s.value.color, hidden: false, values: {} });
  }

  const style = axisStyle(ctx.palette);
  const mode = tooltipMode(o);
  return {
    option: {
      animation: ctx.animation,
      textStyle: textStyle(ctx.palette),
      grid: grid(),
      xAxis: timeAxis(ctx),
      yAxis: {
        type: "category",
        data: rows,
        inverse: true,
        ...style,
        splitLine: { show: false },
        axisTick: { show: false },
        axisLine: { show: false },
      },
      tooltip:
        mode === "none"
          ? { show: false }
          : {
              ...tooltipBase(ctx.palette),
              trigger: "item",
              formatter: (p: any) => {
                const s = spans[p.dataIndex];
                if (!s) return "";
                return (
                  tooltipHeader(`${timeText(s.start)} (${duration(s.end - s.start)})`) +
                  tooltipRow(s.value.color, rows[s.row], displayText(s.value))
                );
              },
            },
      series: [
        {
          type: "custom",
          animation: false,
          encode: { x: [0, 1], y: 2 },
          data: spans.map((s) => [s.start, s.end, s.row]),
          renderItem: (params: any, api: any) => {
            const s = spans[params.dataIndex];
            if (!s) return null;
            let start = api.coord([api.value(0), api.value(2)]);
            let end = api.coord([api.value(1), api.value(2)]);
            const h = api.size([0, 1])[1] * rowHeight;
            if (status) {
              const w = end[0] - start[0];
              start = [start[0] + (w * (1 - colWidth)) / 2, start[1]];
              end = [end[0] - (w * (1 - colWidth)) / 2, end[1]];
            }
            const rect = echarts.graphic.clipRectByRect(
              { x: start[0], y: start[1] - h / 2, width: Math.max(1, end[0] - start[0]), height: h },
              { x: params.coordSys.x, y: params.coordSys.y, width: params.coordSys.width, height: params.coordSys.height },
            );
            if (!rect) return null;
            const fill = withAlpha(s.value.color, fillOpacity);
            const children: any[] = [
              {
                type: "rect",
                shape: rect,
                style: { fill, stroke: lineWidth > 0 ? s.value.color : undefined, lineWidth },
              },
            ];
            const text = displayText(s.value);
            if (labels && rect.width > text.length * 6 + 6) {
              const x = align === "left" ? rect.x + 4 : align === "right" ? rect.x + rect.width - 4 : rect.x + rect.width / 2;
              children.push({
                type: "text",
                style: {
                  text,
                  x,
                  y: rect.y + rect.height / 2,
                  fill: fillOpacity >= 0.5 ? contrastText(s.value.color, ctx.palette) : ctx.palette.text,
                  align,
                  verticalAlign: "middle",
                  fontSize: 11,
                  fontFamily: ctx.palette.font,
                },
                silent: true,
              });
            }
            return { type: "group", children };
          },
        },
      ],
    },
    legend,
    zoomable: true,
  };
}

// typicalStep is the median gap between times, the width of a last cell.
function typicalStep(times: (number | null)[]): number {
  const gaps: number[] = [];
  for (let i = 1; i < times.length; i++) {
    const a = times[i - 1];
    const b = times[i];
    if (a !== null && b !== null && b > a) gaps.push(b - a);
  }
  if (!gaps.length) return 60000;
  gaps.sort((a, b) => a - b);
  return gaps[Math.floor(gaps.length / 2)];
}

function clamp(n: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, n));
}
