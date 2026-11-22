<script lang="ts">
  import type { StockQuote, OHLCData } from "../types.ts";
  import { formatPrice, formatChange, formatPercent, changeColor } from "../formatters.ts";

  let {
    quote = null,
    ohlc = null,
    colorLogic = "red-green",
  }: {
    quote: StockQuote | null;
    ohlc: OHLCData | null;
    colorLogic: string;
  } = $props();

  let canvas = $state<HTMLCanvasElement | null>(null);
  let direction = $derived(quote ? changeColor(quote.change, colorLogic) : "neutral");

  let upColor = $derived(colorLogic === "green-red" ? "#f38ba8" : "#a6e3a1");
  let downColor = $derived(colorLogic === "green-red" ? "#a6e3a1" : "#f38ba8");

  // Track container size so the canvas redraws on resize.
  let canvasWidth = $state<number>(0);
  let canvasHeight = $state<number>(0);
  let wrapperEl = $state<HTMLElement | null>(null);

  $effect(() => {
    if (!wrapperEl) return;
    const ro = new ResizeObserver((entries) => {
      const entry = entries[0];
      if (!entry) return;
      canvasWidth = entry.contentRect.width;
      canvasHeight = entry.contentRect.height;
    });
    ro.observe(wrapperEl);
    return () => ro.disconnect();
  });

  // Draw candlestick chart whenever canvas, data, or container size changes.
  $effect(() => {
    if (!canvas || !ohlc || ohlc.candles.length < 2) return;
    if (canvasWidth <= 0 || canvasHeight <= 0) return;

    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const dpr = window.devicePixelRatio || 1;
    const w = canvasWidth;
    const h = canvasHeight;

    canvas.width = w * dpr;
    canvas.height = h * dpr;
    ctx.scale(dpr, dpr);
    ctx.clearRect(0, 0, w, h);

    const candles = ohlc.candles;
    const paddingLeft = 50;
    const paddingRight = 8;
    const paddingTop = 30;
    const paddingBottom = 24;
    const chartW = w - paddingLeft - paddingRight;
    const chartH = h - paddingTop - paddingBottom;

    // Calculate price range.
    let priceMin = Infinity;
    let priceMax = -Infinity;
    let volMax = 0;
    for (const c of candles) {
      if (c.l < priceMin) priceMin = c.l;
      if (c.h > priceMax) priceMax = c.h;
      if (c.v > volMax) volMax = c.v;
    }
    const priceRange = priceMax - priceMin || 1;

    // Volume area takes bottom 20% of chart height.
    const volH = chartH * 0.2;
    const candleH = chartH - volH;

    function priceToY(price: number): number {
      return paddingTop + candleH - ((price - priceMin) / priceRange) * candleH;
    }

    const candleWidth = Math.max(1, (chartW / candles.length) * 0.7);
    const gap = chartW / candles.length;

    // Draw Y-axis price labels.
    ctx.fillStyle = "#a6adc8";
    ctx.font = `${Math.min(10, w * 0.03)}px sans-serif`;
    ctx.textAlign = "right";
    ctx.textBaseline = "middle";
    const steps = 4;
    for (let i = 0; i <= steps; i++) {
      const price = priceMin + (priceRange / steps) * i;
      const y = priceToY(price);
      ctx.fillText(formatPrice(price), paddingLeft - 4, y);

      // Grid line.
      ctx.strokeStyle = "#45475a40";
      ctx.lineWidth = 0.5;
      ctx.beginPath();
      ctx.moveTo(paddingLeft, y);
      ctx.lineTo(w - paddingRight, y);
      ctx.stroke();
    }

    // Draw X-axis date labels.
    ctx.textAlign = "center";
    ctx.textBaseline = "top";
    const labelCount = Math.min(5, candles.length);
    const labelStep = Math.floor(candles.length / labelCount);
    for (let i = 0; i < candles.length; i += labelStep) {
      const x = paddingLeft + i * gap + gap / 2;
      const date = new Date(candles[i].t * 1000);
      let label: string;
      if (ohlc.interval === "1D") {
        label = date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
      } else {
        label = date.toLocaleDateString([], { month: "short", day: "numeric" });
      }
      ctx.fillStyle = "#a6adc8";
      ctx.fillText(label, x, h - paddingBottom + 4);
    }

    // Draw candles and volume bars.
    for (let i = 0; i < candles.length; i++) {
      const c = candles[i];
      const x = paddingLeft + i * gap + gap / 2;
      const isUp = c.c >= c.o;
      const color = isUp ? upColor : downColor;

      // Volume bar.
      if (volMax > 0) {
        const barH = (c.v / volMax) * volH;
        ctx.fillStyle = color + "40";
        ctx.fillRect(
          x - candleWidth / 2,
          paddingTop + candleH + (volH - barH),
          candleWidth,
          barH
        );
      }

      // Wick (high-low line).
      ctx.strokeStyle = color;
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(x, priceToY(c.h));
      ctx.lineTo(x, priceToY(c.l));
      ctx.stroke();

      // Candle body.
      const bodyTop = priceToY(Math.max(c.o, c.c));
      const bodyBottom = priceToY(Math.min(c.o, c.c));
      const bodyHeight = Math.max(1, bodyBottom - bodyTop);

      ctx.fillStyle = color;
      ctx.fillRect(x - candleWidth / 2, bodyTop, candleWidth, bodyHeight);
    }

    // Header info.
    if (quote) {
      ctx.textAlign = "left";
      ctx.textBaseline = "top";
      ctx.fillStyle = "#cdd6f4";
      ctx.font = `bold ${Math.min(13, w * 0.035)}px sans-serif`;
      ctx.fillText(quote.symbol, paddingLeft, 4);

      const symbolWidth = ctx.measureText(quote.symbol).width;
      ctx.font = `${Math.min(12, w * 0.032)}px sans-serif`;
      ctx.fillText(` ${formatPrice(quote.price)}`, paddingLeft + symbolWidth, 4);

      const priceStr = ` ${formatPrice(quote.price)}`;
      const priceWidth = ctx.measureText(priceStr).width;
      const changeStr = ` ${formatChange(quote.change)} (${formatPercent(quote.change_percent)})`;
      ctx.fillStyle = quote.change >= 0 ? upColor : downColor;
      ctx.fillText(changeStr, paddingLeft + symbolWidth + priceWidth, 4);
    }
  });
</script>

<div class="candlestick-view">
  <div class="canvas-wrapper" bind:this={wrapperEl}>
    <canvas bind:this={canvas}></canvas>
  </div>
</div>

<style>
  .candlestick-view {
    width: 100%;
    height: 100%;
    overflow: hidden;
    display: flex;
    min-height: 0;
  }

  .canvas-wrapper {
    flex: 1;
    min-height: 0;
    min-width: 0;
  }

  canvas {
    display: block;
    width: 100%;
    height: 100%;
  }
</style>
