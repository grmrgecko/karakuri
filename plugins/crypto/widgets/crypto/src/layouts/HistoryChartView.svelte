<script lang="ts">
  import type { CryptoQuote, PricePoint } from "../types.ts";
  import { formatPrice, formatChange, formatPercent, changeDirection } from "../formatters.ts";

  let {
    quote = null,
    points = [],
    timeline = "1D",
  }: {
    quote: CryptoQuote | null;
    points: PricePoint[];
    timeline: string;
  } = $props();

  let canvas = $state<HTMLCanvasElement | null>(null);
  let wrapperEl = $state<HTMLElement | null>(null);
  let canvasW = $state<number>(0);
  let canvasH = $state<number>(0);
  let direction = $derived(quote ? changeDirection(quote.change_pct_24h) : "neutral");

  let lineColor = $derived(
    direction === "positive" ? "#a6e3a1" : direction === "negative" ? "#f38ba8" : "#a6adc8"
  );

  // Track container size so the canvas redraws at the correct resolution.
  $effect(() => {
    if (!wrapperEl) return;
    const ro = new ResizeObserver((entries) => {
      const entry = entries[0];
      if (!entry) return;
      canvasW = entry.contentRect.width;
      canvasH = entry.contentRect.height;
    });
    ro.observe(wrapperEl);
    return () => ro.disconnect();
  });

  // Draw line chart whenever canvas, data, or size changes.
  $effect(() => {
    if (!canvas || canvasW <= 0 || canvasH <= 0) return;

    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const dpr = window.devicePixelRatio || 1;
    const w = canvasW;
    const h = canvasH;

    canvas.width = w * dpr;
    canvas.height = h * dpr;
    ctx.scale(dpr, dpr);
    ctx.clearRect(0, 0, w, h);

    // Draw header info.
    const paddingTop = 40;
    const paddingBottom = 24;
    const paddingLeft = 8;
    const paddingRight = 8;
    const chartH = h - paddingTop - paddingBottom;
    const chartW = w - paddingLeft - paddingRight;

    if (quote) {
      ctx.textAlign = "left";
      ctx.textBaseline = "top";

      // Coin icon area + pair.
      ctx.fillStyle = "#cdd6f4";
      ctx.font = `bold ${Math.min(14, w * 0.04)}px sans-serif`;
      ctx.fillText(quote.pair, paddingLeft, 4);

      const pairWidth = ctx.measureText(quote.pair).width;
      ctx.font = `${Math.min(13, w * 0.035)}px sans-serif`;
      const priceStr = ` ${formatPrice(quote.price)}`;
      ctx.fillText(priceStr, paddingLeft + pairWidth, 4);

      const priceWidth = ctx.measureText(priceStr).width;
      const changeStr = ` ${formatChange(quote.change_24h)} (${formatPercent(quote.change_pct_24h)})`;
      ctx.fillStyle = lineColor;
      ctx.fillText(changeStr, paddingLeft + pairWidth + priceWidth, 4);

      // Name on second line.
      if (quote.name) {
        ctx.fillStyle = "#a6adc8";
        ctx.font = `${Math.min(11, w * 0.03)}px sans-serif`;
        ctx.fillText(quote.name, paddingLeft, 22);
      }
    }

    // If we don't have enough points, draw an empty state.
    if (points.length < 2) {
      ctx.fillStyle = "#a6adc8";
      ctx.font = `${Math.min(12, w * 0.035)}px sans-serif`;
      ctx.textAlign = "center";
      ctx.textBaseline = "middle";
      ctx.fillText("Loading chart data...", w / 2, paddingTop + chartH / 2);
      return;
    }

    const prices = points.map((pt) => pt.p);
    const minP = Math.min(...prices);
    const maxP = Math.max(...prices);
    const rangeP = maxP - minP || 1;
    const padding = 2;

    function priceToY(price: number): number {
      return paddingTop + chartH - padding - ((price - minP) / rangeP) * (chartH - padding * 2);
    }

    // Draw gradient fill.
    const gradient = ctx.createLinearGradient(0, paddingTop, 0, paddingTop + chartH);
    gradient.addColorStop(0, lineColor + "40");
    gradient.addColorStop(1, lineColor + "05");

    ctx.beginPath();
    ctx.moveTo(paddingLeft, paddingTop + chartH);
    for (let i = 0; i < prices.length; i++) {
      const x = paddingLeft + (i / (prices.length - 1)) * chartW;
      const y = priceToY(prices[i]);
      ctx.lineTo(x, y);
    }
    ctx.lineTo(paddingLeft + chartW, paddingTop + chartH);
    ctx.closePath();
    ctx.fillStyle = gradient;
    ctx.fill();

    // Draw line.
    ctx.beginPath();
    for (let i = 0; i < prices.length; i++) {
      const x = paddingLeft + (i / (prices.length - 1)) * chartW;
      const y = priceToY(prices[i]);
      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    }
    ctx.strokeStyle = lineColor;
    ctx.lineWidth = 1.5;
    ctx.stroke();

    // Draw X-axis time labels.
    ctx.fillStyle = "#a6adc8";
    ctx.font = `${Math.min(10, w * 0.028)}px sans-serif`;
    ctx.textAlign = "center";
    ctx.textBaseline = "top";
    const labelCount = Math.min(5, points.length);
    const labelStep = Math.max(1, Math.floor(points.length / labelCount));
    for (let i = 0; i < points.length; i += labelStep) {
      const x = paddingLeft + (i / (points.length - 1)) * chartW;
      const date = new Date(points[i].t * 1000);
      const label = timeline === "1D"
        ? date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
        : date.toLocaleDateString([], { month: "short", day: "numeric" });
      ctx.fillText(label, x, h - paddingBottom + 4);
    }

    // Draw Y-axis price labels.
    ctx.textAlign = "right";
    ctx.textBaseline = "middle";
    const ySteps = 3;
    for (let i = 0; i <= ySteps; i++) {
      const price = minP + (rangeP / ySteps) * i;
      const y = priceToY(price);
      ctx.fillStyle = "#a6adc8";
      ctx.fillText(formatPrice(price), w - paddingRight, y);

      // Grid line.
      ctx.strokeStyle = "#45475a40";
      ctx.lineWidth = 0.5;
      ctx.beginPath();
      ctx.moveTo(paddingLeft, y);
      ctx.lineTo(w - paddingRight, y);
      ctx.stroke();
    }
  });
</script>

<div class="history-chart-view">
  <div class="canvas-wrapper" bind:this={wrapperEl}>
    <canvas bind:this={canvas}></canvas>
  </div>
</div>

<style>
  .history-chart-view {
    display: flex;
    flex: 1;
    min-height: 0;
    overflow: hidden;
  }

  .canvas-wrapper {
    flex: 1;
    min-height: 0;
    min-width: 0;
    position: relative;
  }

  canvas {
    display: block;
    width: 100%;
    height: 100%;
  }
</style>
