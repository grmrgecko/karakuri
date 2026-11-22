<script lang="ts">
  import type { StockQuote } from "../types.ts";
  import { formatPrice, formatPercent, formatVolume, formatMarketCap, changeColor } from "../formatters.ts";

  let {
    quotes = [],
    colorLogic = "red-green",
  }: {
    quotes: StockQuote[];
    colorLogic: string;
  } = $props();

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.fitText(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="list-table-view" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
    <div class="list-header">
      <span class="col-symbol">Symbol</span>
      <span class="col-price">Price</span>
      <span class="col-change">Change</span>
      <span class="col-volume">Volume</span>
      <span class="col-mcap">Market Cap</span>
    </div>
    <div class="list-body">
      {#each quotes as quote}
        <div class="list-row">
          <span class="col-symbol">
            <span class="stock-symbol">{quote.symbol}</span>
            {#if quote.name}
              <span class="stock-name">{quote.name}</span>
            {/if}
          </span>
          <span class="col-price">{formatPrice(quote.price, quote.currency)}</span>
          <span class="col-change {changeColor(quote.change, colorLogic)}">
            {formatPercent(quote.change_percent)}
          </span>
          <span class="col-volume">{formatVolume(quote.volume)}</span>
          <span class="col-mcap">{quote.market_cap ? formatMarketCap(quote.market_cap) : "-"}</span>
        </div>
      {/each}
    </div>
  </div>
</div>

<style>
  .list-table-view {
    flex: 1;
    width: 100%;
    height: 100%;
    overflow: hidden;
  }

  .scale-content {
    display: flex;
    flex-direction: column;
    padding: 0.5em;
    box-sizing: border-box;
  }

  .list-header {
    display: flex;
    align-items: center;
    gap: 0.6em;
    padding: 0.4em 0.6em;
    font-size: 0.65em;
    font-weight: 600;
    color: var(--theme-subtext, #a6adc8);
    border-bottom: 1px solid var(--theme-border, #45475a);
    flex-shrink: 0;
  }

  .list-body {
    display: flex;
    flex-direction: column;
  }

  .list-row {
    display: flex;
    align-items: center;
    gap: 0.6em;
    padding: 0.35em 0.6em;
    font-size: 0.7em;
    border-bottom: 1px solid var(--theme-border, #45475a);
    transition: background 0.15s;
  }

  .list-row:hover {
    background: var(--theme-surface-1, #45475a);
  }

  .col-symbol {
    flex: 2;
    display: flex;
    align-items: center;
    gap: 0.4em;
    min-width: 0;
    overflow: hidden;
  }

  .stock-symbol {
    font-weight: 600;
    color: var(--theme-text, #cdd6f4);
  }

  .stock-name {
    color: var(--theme-subtext, #a6adc8);
    font-size: 0.85em;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .col-price {
    flex: 1.5;
    text-align: right;
    font-weight: 500;
    color: var(--theme-text, #cdd6f4);
  }

  .col-change {
    flex: 1;
    text-align: right;
    font-weight: 500;
  }

  .col-change.positive {
    color: var(--theme-success, #a6e3a1);
  }

  .col-change.negative {
    color: var(--theme-error, #f38ba8);
  }

  .col-change.neutral {
    color: var(--theme-subtext, #a6adc8);
  }

  .col-volume {
    flex: 1;
    text-align: right;
    color: var(--theme-subtext, #a6adc8);
  }

  .col-mcap {
    flex: 1.5;
    text-align: right;
    color: var(--theme-subtext, #a6adc8);
  }
</style>
