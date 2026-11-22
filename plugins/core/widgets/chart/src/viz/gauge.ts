// Gauge: one arc per reduced value, filled to the value in its colour, with
// an outer band of threshold colours and optional threshold labels.

import { fieldMinMax, resolveColor, sortedSteps, thresholdValue, withAlpha } from "../data/colors";
import { displayText, fieldDisplays, formatNumber } from "../data/display";
import type { VizContext, VizResult } from "./common";
import { textStyle } from "./common";

const START = 210;
const END = -30;

export function buildGauge(ctx: VizContext): VizResult {
  const o = ctx.options;
  const values = fieldDisplays(ctx.frames, o.reduceOptions, ctx.palette);
  if (!values.length) return { message: "Gauge needs a number field" };

  const n = values.length;
  const horizontal = o.orientation === "horizontal" || (o.orientation !== "vertical" && ctx.width >= ctx.height);
  const cols = horizontal ? n : 1;
  const rows = horizontal ? 1 : n;
  const cellW = ctx.width / cols;
  const cellH = ctx.height / rows;
  const showMarkers = o.showThresholdMarkers !== false;
  const fullRadius = Math.max(10, Math.min(cellW, cellH * 1.15) / 2 - 8);
  // Threshold labels sit outside the ring, so the gauge shrinks to make room
  // for them; a gauge too small to keep a readable ring
  // after that draws no labels.
  const labelFont = Math.max(9, Math.min(fullRadius * 0.38, 48) * 0.28);
  const labelRoom = labelFont * 4.4;
  const showLabels = o.showThresholdLabels === true && fullRadius - labelRoom >= 30;
  const radius = showLabels ? fullRadius - labelRoom : fullRadius;
  const width = Math.max(4, radius * 0.18);
  const series: any[] = [];

  values.forEach((v, i) => {
    const cx = (horizontal ? (i + 0.5) * cellW : ctx.width / 2);
    const cy = (horizontal ? ctx.height / 2 : (i + 0.5) * cellH) + radius * 0.12;
    const center = [cx, cy];
    const { min, max } = fieldMinMax(v.field, true);
    const value = Number.isFinite(v.display.numeric) ? Math.max(min, Math.min(max, v.display.numeric)) : min;
    const r = showMarkers ? radius - width * 0.6 : radius;
    const fontSize = Math.max(10, Math.min(radius * 0.38, 48));

    series.push({
      type: "gauge",
      center,
      radius: r,
      min,
      max,
      startAngle: START,
      endAngle: END,
      animation: ctx.animation,
      progress: { show: true, width, roundCap: false, itemStyle: { color: v.display.color } },
      axisLine: { lineStyle: { width, color: [[1, withAlpha(ctx.palette.border, 0.6)]] } },
      pointer: { show: false },
      axisTick: { show: false },
      splitLine: { show: false },
      axisLabel: { show: false },
      anchor: { show: false },
      title: {
        show: n > 1 || o.showName === true,
        offsetCenter: [0, "62%"],
        color: ctx.palette.textMuted,
        fontSize: Math.max(10, fontSize * 0.4),
      },
      detail: {
        valueAnimation: false,
        offsetCenter: [0, "0%"],
        fontSize,
        fontWeight: 500,
        color: v.display.color,
        formatter: () => displayText(v.display),
      },
      data: [{ value, name: v.name }],
    });

    const steps = sortedSteps(v.field.config.thresholds);
    if (showMarkers) {
      const stops: [number, string][] = [];
      steps.forEach((s, k) => {
        const next = steps[k + 1];
        const hi = next ? thresholdValue(next, v.field.config.thresholds, min, max) : max;
        const frac = Math.max(0, Math.min(1, ((hi ?? max) - min) / (max - min)));
        if (stops.length && frac <= stops[stops.length - 1][0]) {
          stops[stops.length - 1][1] = resolveColor(s.color, ctx.palette);
          return;
        }
        stops.push([frac, resolveColor(s.color, ctx.palette)]);
      });
      if (!stops.length || stops[stops.length - 1][0] < 1) stops.push([1, stops[stops.length - 1]?.[1] ?? ctx.palette.border]);
      series.push({
        type: "gauge",
        center,
        radius,
        min,
        max,
        startAngle: START,
        endAngle: END,
        silent: true,
        animation: false,
        axisLine: { lineStyle: { width: Math.max(2, width * 0.3), color: stops } },
        pointer: { show: false },
        progress: { show: false },
        axisTick: { show: false },
        splitLine: { show: false },
        axisLabel: { show: false },
        title: { show: false },
        detail: { show: false },
        data: [],
      });
    }

    if (showLabels) {
      // Labels closer together than their own height along the arc would
      // print over each other; the later one is left out.
      const minGap = ((labelFont * 1.4) / (radius + labelRoom / 2)) * (180 / Math.PI);
      let lastAngle = Infinity;
      for (const s of steps) {
        const t = thresholdValue(s, v.field.config.thresholds, min, max);
        if (t === null || t < min || t > max) continue;
        // A gauge whose arc has no length places one label at the threshold.
        const angle = START - ((t - min) / (max - min)) * (START - END);
        if (Math.abs(lastAngle - angle) < minGap) continue;
        lastAngle = angle;
        // A centred label spreads sideways, so labels toward the sides of
        // the ring sit further out to clear it.
        const side = Math.abs(Math.cos((angle * Math.PI) / 180));
        series.push({
          type: "gauge",
          center,
          radius: radius + 4,
          min: 0,
          max: 1,
          splitNumber: 1,
          startAngle: angle,
          endAngle: angle,
          silent: true,
          animation: false,
          axisLine: { show: false },
          pointer: { show: false },
          progress: { show: false },
          axisTick: { show: false },
          splitLine: { show: false },
          title: { show: false },
          detail: { show: false },
          axisLabel: {
            show: true,
            distance: -labelFont * (1.8 + 1.6 * side),
            color: ctx.palette.textMuted,
            fontSize: labelFont,
            formatter: (x: number) => (x === 0 ? formatNumber(v.field, t) : ""),
          },
          data: [],
        });
      }
    }
  });

  return { option: { animation: ctx.animation, textStyle: textStyle(ctx.palette), series } };
}
