// Candlestick: open, high, low, and close per time as candles or OHLC bars,
// with an optional volume panel below.

import type { Field, Frame } from "../data/types";
import { resolveColor, seriesColor } from "../data/colors";
import { displayName, findField, timeField } from "../data/frames";
import { formatNumber } from "../data/display";
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

const ROLES = ["open", "high", "low", "close", "volume"] as const;
type Role = (typeof ROLES)[number];

function fieldFor(frame: Frame, role: Role, configured: string | undefined): Field | undefined {
  if (configured) {
    const hit = findField([frame], configured);
    if (hit) return hit.field;
  }
  return frame.fields.find((f) => f.type === "number" && (f.name.toLowerCase() === role || displayName(f, frame).toLowerCase() === role));
}

export function buildCandlestick(ctx: VizContext): VizResult {
  const o = ctx.options;
  const frame = ctx.frames.find((f) => timeField(f));
  if (!frame) return { message: "Candlestick needs a time field" };
  const time = timeField(frame)!;
  const fields: Partial<Record<Role, Field>> = {};
  for (const role of ROLES) fields[role] = fieldFor(frame, role, o.fields?.[role]);

  const mode = o.mode === "candles" || o.mode === "volume" ? o.mode : "candles+volume";
  const needed: Role[] = mode === "volume" ? ["volume"] : ["open", "high", "low", "close"];
  const missing = needed.filter((r) => !fields[r]);
  if (missing.length) return { message: `Candlestick needs ${needed.join(", ")} fields; missing ${missing.join(", ")}` };

  const up = resolveColor(o.colors?.up ?? "green", ctx.palette);
  const down = resolveColor(o.colors?.down ?? "red", ctx.palette);
  const closeClose = o.colorStrategy === "close-close";
  const times = time.values.map(toNumber);
  const n = times.length;
  const col = (r: Role, i: number) => (fields[r] ? toNumber(fields[r]!.values[i]) : null);
  const rising = (i: number): boolean => {
    const close = col("close", i);
    const ref = closeClose ? (i > 0 ? col("close", i - 1) : col("open", i)) : col("open", i);
    return close !== null && ref !== null ? close >= ref : true;
  };

  const showPrice = mode !== "volume";
  const showVolume = mode !== "candles" && !!fields.volume;
  const style = axisStyle(ctx.palette);
  const priceField = fields.close ?? fields.volume!;
  const grids = showPrice && showVolume ? [grid({ bottom: "28%" }), grid({ top: "76%" })] : [grid()];
  const xAxes = grids.map((_, i) => timeAxis(ctx, { gridIndex: i, axisLabel: { ...style.axisLabel, show: i === grids.length - 1, hideOverlap: true } }));
  const yAxes: any[] = [];
  const series: any[] = [];
  const legend: LegendItem[] = [];

  if (showPrice) {
    yAxes.push({
      type: "value",
      gridIndex: 0,
      scale: true,
      ...style,
      axisLabel: { ...style.axisLabel, formatter: (v: number) => formatNumber({ ...priceField, config: { ...priceField.config, decimals: undefined } }, v) },
    });
    const ohlc = Array.from({ length: n }, (_, i) => [times[i], col("open", i), col("close", i), col("low", i), col("high", i)]);
    if (o.candleStyle === "ohlcbars") {
      series.push({
        type: "custom",
        xAxisIndex: 0,
        yAxisIndex: 0,
        animation: false,
        encode: { x: 0, y: [1, 2, 3, 4] },
        data: ohlc,
        renderItem: (params: any, api: any) => {
          const t = api.value(0);
          const [open, close, low, high] = [api.value(1), api.value(2), api.value(3), api.value(4)];
          const w = Math.max(2, api.size([ctx.range.to - ctx.range.from, 0])[0] / Math.max(n, 1) * 0.3);
          const pLow = api.coord([t, low]);
          const pHigh = api.coord([t, high]);
          const pOpen = api.coord([t, open]);
          const pClose = api.coord([t, close]);
          const color = rising(params.dataIndex) ? up : down;
          const line = (x1: number, y1: number, x2: number, y2: number) => ({ type: "line", shape: { x1, y1, x2, y2 }, style: { stroke: color, lineWidth: 1.5 } });
          return {
            type: "group",
            children: [line(pLow[0], pLow[1], pHigh[0], pHigh[1]), line(pOpen[0] - w, pOpen[1], pOpen[0], pOpen[1]), line(pClose[0], pClose[1], pClose[0] + w, pClose[1])],
          };
        },
      });
    } else {
      series.push({
        type: "candlestick",
        xAxisIndex: 0,
        yAxisIndex: 0,
        animation: ctx.animation,
        encode: { x: 0, y: [1, 2, 3, 4] },
        barMaxWidth: 16,
        data: ohlc.map((d, i) => {
          const color = rising(i) ? up : down;
          return closeClose ? { value: d, itemStyle: { color, color0: color, borderColor: color, borderColor0: color } } : d;
        }),
        itemStyle: { color: up, color0: down, borderColor: up, borderColor0: down },
      });
    }
    legend.push(legendItem("Price", up, fields.close, ctx));

    if (o.includeAllFields === true) {
      const used = new Set(Object.values(fields));
      for (const f of frame.fields) {
        if (f.type !== "number" || used.has(f)) continue;
        const name = displayName(f, frame);
        const color = seriesColor(f, ctx.palette);
        legend.push(legendItem(name, color, f, ctx));
        if (ctx.hidden.has(name)) continue;
        series.push({
          type: "line",
          xAxisIndex: 0,
          yAxisIndex: 0,
          showSymbol: false,
          animation: ctx.animation,
          lineStyle: { color, width: 1 },
          itemStyle: { color },
          data: times.map((t, i) => [t, toNumber(f.values[i])]),
        });
      }
    }
  }

  if (showVolume) {
    const g = showPrice ? 1 : 0;
    yAxes.push({ type: "value", gridIndex: g, ...style, splitNumber: 2, axisLabel: { ...style.axisLabel, formatter: (v: number) => formatNumber({ ...fields.volume!, config: { ...fields.volume!.config, decimals: undefined, unit: fields.volume!.config.unit ?? "short" } }, v) } });
    series.push({
      type: "bar",
      xAxisIndex: g,
      yAxisIndex: yAxes.length - 1,
      animation: ctx.animation,
      barMaxWidth: 16,
      data: times.map((t, i) => ({ value: [t, col("volume", i)], itemStyle: { color: rising(i) ? up : down, opacity: 0.6 } })),
    });
  }

  const tip = tooltipMode(o);
  return {
    option: {
      animation: ctx.animation,
      textStyle: textStyle(ctx.palette),
      grid: grids,
      xAxis: xAxes,
      yAxis: yAxes,
      axisPointer: { link: [{ xAxisIndex: "all" }] },
      series,
      tooltip:
        tip === "none"
          ? { show: false }
          : {
              ...tooltipBase(ctx.palette),
              trigger: "axis",
              axisPointer: { type: "line", lineStyle: { color: ctx.palette.textMuted } },
              formatter: (raw: any) => {
                const params: any[] = Array.isArray(raw) ? raw : [raw];
                if (!params.length) return "";
                const i = params[0].dataIndex;
                const color = rising(i) ? up : down;
                const rows = ROLES.filter((r) => fields[r]).map((r) => tooltipRow(color, r[0].toUpperCase() + r.slice(1), formatNumber(fields[r], fields[r]!.values[i])));
                return tooltipHeader(timeText(times[i])) + rows.join("");
              },
            },
    },
    legend,
    zoomable: true,
  };
}
