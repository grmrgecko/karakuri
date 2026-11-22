// The ECharts build the chart uses: only the chart types and components the
// visualizations draw, rendered as SVG so text stays crisp in renders.
import * as echarts from "echarts/core";
import { BarChart, CandlestickChart, CustomChart, GaugeChart, LineChart, PieChart, ScatterChart } from "echarts/charts";
import {
  BrushComponent,
  GridComponent,
  MarkAreaComponent,
  MarkLineComponent,
  ToolboxComponent,
  TooltipComponent,
  VisualMapContinuousComponent,
  VisualMapPiecewiseComponent,
} from "echarts/components";
import { SVGRenderer } from "echarts/renderers";

echarts.use([
  BarChart,
  CandlestickChart,
  CustomChart,
  GaugeChart,
  LineChart,
  PieChart,
  ScatterChart,
  BrushComponent,
  GridComponent,
  MarkAreaComponent,
  MarkLineComponent,
  ToolboxComponent,
  TooltipComponent,
  VisualMapContinuousComponent,
  VisualMapPiecewiseComponent,
  SVGRenderer,
]);

export { echarts };
