// Pie chart: one slice per reduced value.

import { fieldDisplays, displayText } from "../data/display";
import {
  escapeHtml,
  swatch,
  textStyle,
  tooltipBase,
  tooltipMode,
  type LegendItem,
  type VizContext,
  type VizResult,
} from "./common";

export function buildPieChart(ctx: VizContext): VizResult {
  const o = ctx.options;
  let values = fieldDisplays(ctx.frames, o.reduceOptions, ctx.palette);
  if (!values.length) return { message: "Pie chart needs a number field" };

  const sort = o.sort === "asc" || o.sort === "none" ? o.sort : "desc";
  if (sort !== "none") {
    values = [...values].sort((a, b) => {
      const x = Number.isFinite(a.display.numeric) ? a.display.numeric : 0;
      const y = Number.isFinite(b.display.numeric) ? b.display.numeric : 0;
      return sort === "asc" ? x - y : y - x;
    });
  }

  const mode = (v: (typeof values)[number], i: number) => {
    const m = v.field.config.color?.mode ?? "palette-classic";
    return m === "palette-classic" ? ctx.palette.series[i % ctx.palette.series.length] : v.display.color;
  };
  const colored = values.map((v, i) => ({ ...v, color: mode(v, i) }));
  const total = colored.reduce((sum, v) => sum + (Number.isFinite(v.display.numeric) && v.display.numeric > 0 ? v.display.numeric : 0), 0);
  const percent = (n: number) => (total > 0 && Number.isFinite(n) ? `${((Math.max(0, n) / total) * 100).toFixed(1)}%` : "-");

  const legendValues: string[] = Array.isArray(o.legend?.values) ? o.legend.values : [];
  const legend: LegendItem[] = colored.map((v) => {
    const vals: Record<string, string> = {};
    if (legendValues.includes("value")) vals.value = displayText(v.display);
    if (legendValues.includes("percent")) vals.percent = percent(v.display.numeric);
    return { name: v.name, color: v.color, hidden: ctx.hidden.has(v.name), values: vals };
  });

  const shown = colored.filter((v) => !ctx.hidden.has(v.name));
  const labels: string[] = Array.isArray(o.displayLabels) ? o.displayLabels : [];
  const donut = o.pieType === "donut";
  const tip = tooltipMode(o);

  return {
    option: {
      animation: ctx.animation,
      textStyle: textStyle(ctx.palette),
      tooltip:
        tip === "none"
          ? { show: false }
          : {
              ...tooltipBase(ctx.palette),
              trigger: "item",
              formatter: (p: any) => {
                const v = shown[p.dataIndex];
                if (!v) return "";
                return `${swatch(v.color)}${escapeHtml(v.name)}<br/><b>${escapeHtml(displayText(v.display))}</b> (${escapeHtml(percent(v.display.numeric))})`;
              },
            },
      series: [
        {
          type: "pie",
          radius: donut ? ["50%", "80%"] : [0, "80%"],
          center: ["50%", "50%"],
          animation: ctx.animation,
          avoidLabelOverlap: true,
          itemStyle: { borderColor: ctx.palette.background, borderWidth: 1 },
          label: {
            show: labels.length > 0,
            color: ctx.palette.text,
            fontSize: 11,
            formatter: (p: any) => {
              const v = shown[p.dataIndex];
              if (!v) return "";
              const parts: string[] = [];
              if (labels.includes("name")) parts.push(v.name);
              if (labels.includes("value")) parts.push(displayText(v.display));
              if (labels.includes("percent")) parts.push(percent(v.display.numeric));
              return parts.join("\n");
            },
          },
          labelLine: { show: labels.length > 0 },
          emphasis: { scale: false },
          data: shown.map((v) => ({
            name: v.name,
            value: Number.isFinite(v.display.numeric) ? Math.max(0, v.display.numeric) : 0,
            itemStyle: { color: v.color },
          })),
        },
      ],
    },
    legend,
  };
}
