<script lang="ts">
  import type { CryptoQuote } from "../types.ts";
  import { formatPrice, formatChange, formatPercent, formatVolume, changeDirection } from "../formatters.ts";

  let {
    quote = null,
  }: {
    quote: CryptoQuote | null;
  } = $props();

  let direction = $derived(quote ? changeDirection(quote.change_pct_24h) : "neutral");

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
        {#if quote.image}
          <img class="coin-icon" src={quote.image} alt="" />
        {/if}
        <div class="header-text">
          <span class="pair">{quote.pair}</span>
          {#if quote.name}
            <span class="name">{quote.name}</span>
          {/if}
        </div>
      </div>

      <div class="price-row">
        <span class="price">{formatPrice(quote.price)}</span>
        <span class="currency">{quote.quote}</span>
      </div>

      <div class="change-row {direction}">
        <span class="trend-arrow">
          {#if quote.change_pct_24h > 0}&#9650;{:else if quote.change_pct_24h < 0}&#9660;{:else}&#9644;{/if}
        </span>
        <span class="change">{formatChange(quote.change_24h)}</span>
        <span class="change-pct">({formatPercent(quote.change_pct_24h)})</span>
        <span class="period">24h</span>
      </div>

      <div class="details">
        <div class="detail">
          <span class="detail-label">High</span>
          <span class="detail-value">{formatPrice(quote.high_24h)}</span>
        </div>
        <div class="detail">
          <span class="detail-label">Low</span>
          <span class="detail-value">{formatPrice(quote.low_24h)}</span>
        </div>
        <div class="detail">
          <span class="detail-label">Vol</span>
          <span class="detail-value">{formatVolume(quote.volume_24h)}</span>
        </div>
        {#if quote.market_cap}
          <div class="detail">
            <span class="detail-label">MCap</span>
            <span class="detail-value">{formatVolume(quote.market_cap)}</span>
          </div>
        {/if}
      </div>

      {#if quote.rank}
        <div class="rank">Rank #{quote.rank}</div>
      {/if}
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
    align-items: center;
    gap: 0.5rem;
  }

  .coin-icon {
    width: 32px;
    height: 32px;
    border-radius: 50%;
  }

  .header-text {
    display: flex;
    flex-direction: column;
    align-items: center;
  }

  .pair {
    font-size: 1.25rem;
    font-weight: 700;
    color: var(--theme-text, #cdd6f4);
    letter-spacing: 0.03em;
  }

  .name {
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

  .trend-arrow {
    font-size: 0.9rem;
  }

  .period {
    font-size: 0.65rem;
    opacity: 0.7;
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

  .rank {
    font-size: 0.65rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.6;
    letter-spacing: 0.05em;
  }
</style>
