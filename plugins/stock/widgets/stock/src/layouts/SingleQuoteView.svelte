<script lang="ts">
  import type { StockQuote } from "../types.ts";
  import { formatPrice, formatChange, formatPercent, formatVolume, changeColor } from "../formatters.ts";

  let {
    quote = null,
    colorLogic = "red-green",
  }: {
    quote: StockQuote | null;
    colorLogic: string;
  } = $props();

  let direction = $derived(quote ? changeColor(quote.change, colorLogic) : "neutral");

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="size-container" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
    {#if quote}
      <div class="header">
        <span class="symbol">{quote.symbol}</span>
        <span class="exchange">{quote.exchange}</span>
      </div>

      <div class="price-row">
        <span class="price">{formatPrice(quote.price, quote.currency)}</span>
        <span class="currency">{quote.currency}</span>
      </div>

      <div class="change-row {direction}">
        <span class="change">{formatChange(quote.change)}</span>
        <span class="change-pct">({formatPercent(quote.change_percent)})</span>
      </div>

      {#if quote.name}
        <div class="name">{quote.name}</div>
      {/if}

      <div class="details">
        <div class="detail">
          <span class="detail-label">Open</span>
          <span class="detail-value">{formatPrice(quote.open)}</span>
        </div>
        <div class="detail">
          <span class="detail-label">High</span>
          <span class="detail-value">{formatPrice(quote.high)}</span>
        </div>
        <div class="detail">
          <span class="detail-label">Low</span>
          <span class="detail-value">{formatPrice(quote.low)}</span>
        </div>
        <div class="detail">
          <span class="detail-label">Vol</span>
          <span class="detail-value">{formatVolume(quote.volume)}</span>
        </div>
      </div>

      <div class="market-state">{quote.market_state}</div>
    {/if}
  </div>
</div>

<style>
  .size-container {
    width: 100%;
    height: 100%;
    overflow: hidden;
    flex: 1;
    min-height: 0;
  }

  .scale-content {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: space-evenly;
    width: 100%;
    height: 100%;
  }

  .header {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
  }

  .symbol {
    font-size: 1.25rem;
    font-weight: 700;
    color: var(--theme-text, #cdd6f4);
    letter-spacing: 0.05em;
  }

  .exchange {
    font-size: 0.75rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .price-row {
    display: flex;
    align-items: flex-start;
    line-height: 1;
  }

  .price {
    font-size: 4.5rem;
    font-weight: 300;
    letter-spacing: -0.02em;
    color: var(--theme-text, #cdd6f4);
  }

  .currency {
    font-size: 1rem;
    color: var(--theme-subtext, #a6adc8);
    margin-top: 0.3rem;
    margin-left: 0.25rem;
  }

  .change-row {
    display: flex;
    align-items: center;
    gap: 0.3rem;
    font-size: 1.25rem;
    font-weight: 500;
  }

  .change-row.positive {
    color: var(--theme-success, #a6e3a1);
  }

  .change-row.negative {
    color: var(--theme-error, #f38ba8);
  }

  .change-row.neutral {
    color: var(--theme-subtext, #a6adc8);
  }

  .name {
    font-size: 0.9rem;
    color: var(--theme-subtext, #a6adc8);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 90%;
  }

  .details {
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: 0.75rem;
    margin-top: 0.5rem;
  }

  .detail {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    font-size: 0.9rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .detail-label {
    opacity: 0.7;
  }

  .detail-value {
    color: var(--theme-text, #cdd6f4);
    font-weight: 500;
  }

  .market-state {
    font-size: 0.65rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.6;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
</style>
