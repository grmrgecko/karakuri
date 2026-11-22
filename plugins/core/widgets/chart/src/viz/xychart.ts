// XY chart: number fields plotted against a number x field, as points,
// lines, or both, with optional size and colour from other fields.

import type { Field, Frame } from "../data/types";
import { seriesColor, valueColor } from "../data/colors";
import { displayName, findField } from "../data/frames";
import { formatNumber } from "../data/display";
import { hiddenFromViz } from "../data/fieldConfig";
import { toNumber } from "../data/reducers";
import {
  axisStyle,
  grid,
  legendItem,
  textStyle,
  tooltipBase,
  tooltipMode,
  tooltipRow,
  type LegendItem,
  type VizContext,
  type VizResult,
} from "./common";

interface XYSeries {
  name: string;
  frame: Frame;
  x: Field;
  y: Field;
  size?: Field;
  color?: Field;
}

function named(frame: Frame, name: string | undefined): Field | undefined {
  if (!name) return undefined;
  const hit = findField([frame], name);
  return hit && hit.field.type === "number" ? hit.field : undefined;
}

function seriesOf(ctx: VizContext): XYSeries[] {
  const o = ctx.options;
  const out: XYSeries[] = [];
  if (o.seriesMapping === "manual" && Array.isArray(o.series)) {
    for (const s of o.series) {
      for (const frame of ctx.frames) {
        const x = named(frame, s?.x);
        const y = named(frame, s?.y);
        if (!x || !y) continue;
        out.push({ name: s.name || displayName(y, frame), frame, x, y, size: named(frame, s.size), color: named(frame, s.color) });
        break;
      }
    }
    return out;
  }
  for (const frame of ctx.frames) {
    const numbers = frame.fields.filter((f) => f.type === "number" && !hiddenFromViz(f));
    const x = named(frame, o.xField) ?? numbers[0];
    if (!x) continue;
    const size = named(frame, o.sizeField);
    const color = named(frame, o.colorField);
    for (const y of numbers) {
      if (y === x || y === size || y === color) continue;
      out.push({ name: displayName(y, frame), frame, x, y, size, color });
    }
  }
  return out;
}

export function buildXYChart(ctx: VizContext): VizResult {
  const o = ctx.options;
  const list = seriesOf(ctx);
  if (!list.length) return { message: "XY chart needs two number fields: one for x and one for y" };

  const show = o.show === "lines" || o.show === "points+lines" ? o.show : "points";
  const fixed = toNumber(o.pointSize?.fixed) ?? 5;
  const minSize = toNumber(o.pointSize?.min) ?? 3;
  const maxSize = toNumber(o.pointSize?.max) ?? 20;
  const legend: LegendItem[] = [];
  const drawn: XYSeries[] = [];
  const series: any[] = [];

  for (const s of list) {
    const color = seriesColor(s.y, ctx.palette);
    legend.push(legendItem(s.name, color, s.y, ctx));
    if (ctx.hidden.has(s.name)) continue;
    const sizes = s.size ? s.size.values.map(toNumber) : null;
    const sMin = sizes ? Math.min(...sizes.filter((v): v is number => v !== null)) : 0;
    const sMax = sizes ? Math.max(...sizes.filter((v): v is number => v !== null)) : 0;
    const data = s.x.values.map((xv, i) => {
      const x = toNumber(xv);
      const y = toNumber(s.y.values[i]);
      let size = fixed;
      if (sizes && sizes[i] !== null && sMax > sMin) size = minSize + ((sizes[i]! - sMin) / (sMax - sMin)) * (maxSize - minSize);
      const item: any = { value: [x, y], symbolSize: size };
      if (s.color) item.itemStyle = { color: valueColor(s.color, toNumber(s.color.values[i]), ctx.palette) };
      return item;
    });
    drawn.push(s);
    series.push({
      type: show === "points" ? "scatter" : "line",
      name: s.name,
      animation: ctx.animation,
      showSymbol: show !== "lines",
      symbolSize: fixed,
      lineStyle: { color, width: toNumber(o.lineWidth) ?? 1 },
      itemStyle: { color },
      emphasis: { disabled: true },
      data,
    });
  }

  const style = axisStyle(ctx.palette);
  const xField = list[0].x;
  const yField = list[0].y;
  const plain = (f: Field) => ({ ...f, config: { ...f.config, decimals: undefined } });
  const mode = tooltipMode(o);
  return {
    option: {
      animation: ctx.animation,
      textStyle: textStyle(ctx.palette),
      grid: grid(),
      xAxis: {
        type: "value",
        scale: true,
        name: o.xAxisLabel || displayName(xField, list[0].frame),
        nameLocation: "middle",
        nameGap: 24,
        ...style,
        splitLine: { show: false },
        axisLabel: { ...style.axisLabel, hideOverlap: true, formatter: (v: number) => formatNumber(plain(xField), v) },
      },
      yAxis: {
        type: "value",
        scale: true,
        ...style,
        axisLabel: { ...style.axisLabel, formatter: (v: number) => formatNumber(plain(yField), v) },
      },
      series,
      tooltip:
        mode === "none"
          ? { show: false }
          : {
              ...tooltipBase(ctx.palette),
              trigger: "item",
              formatter: (p: any) => {
                const s = drawn[p.seriesIndex];
                if (!s) return "";
                const i = p.dataIndex;
                const color = seriesColor(s.y, ctx.palette);
                const rows = [
                  tooltipRow(color, displayName(s.x, s.frame), formatNumber(s.x, s.x.values[i])),
                  tooltipRow(color, s.name, formatNumber(s.y, s.y.values[i])),
                ];
                if (s.size) rows.push(tooltipRow(color, displayName(s.size, s.frame), formatNumber(s.size, s.size.values[i])));
                if (s.color) rows.push(tooltipRow(color, displayName(s.color, s.frame), formatNumber(s.color, s.color.values[i])));
                return rows.join("");
              },
            },
    },
    legend,
  };
}
