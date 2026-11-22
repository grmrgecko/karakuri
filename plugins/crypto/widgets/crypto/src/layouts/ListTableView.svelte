<script lang="ts">
  import type { CryptoQuote } from "../types.ts";
  import { formatPrice, formatPercent, formatMarketCap, changeDirection } from "../formatters.ts";

  let {
    coins = [],
  }: {
    coins: CryptoQuote[];
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
      <span class="col-rank">#</span>
      <span class="col-name">Name</span>
      <span class="col-price">Price</span>
      <span class="col-change">24h</span>
      <span class="col-mcap">Market Cap</span>
    </div>
    <div class="list-body">
      {#each coins as coin, i}
        <div class="list-row">
          <span class="col-rank">{coin.rank || i + 1}</span>
          <span class="col-name">
            {#if coin.image}
              <img class="coin-icon" src={coin.image} alt="" />
            {/if}
            <span class="coin-symbol">{coin.base}</span>
            {#if coin.name}
              <span class="coin-label">{coin.name}</span>
            {/if}
          </span>
          <span class="col-price">{formatPrice(coin.price)}</span>
          <span class="col-change {changeDirection(coin.change_pct_24h)}">
            {formatPercent(coin.change_pct_24h)}
          </span>
          <span class="col-mcap">{coin.market_cap ? formatMarketCap(coin.market_cap) : "-"}</span>
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

  .col-rank {
    width: 2em;
    text-align: center;
    color: var(--theme-subtext, #a6adc8);
    flex-shrink: 0;
  }

  .col-name {
    flex: 2;
    display: flex;
    align-items: center;
    gap: 0.4em;
    min-width: 0;
    overflow: hidden;
  }

  .coin-icon {
    width: 1.3em;
    height: 1.3em;
    border-radius: 50%;
    flex-shrink: 0;
  }

  .coin-symbol {
    font-weight: 600;
    color: var(--theme-text, #cdd6f4);
  }

  .coin-label {
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

  .col-mcap {
    flex: 1.5;
    text-align: right;
    color: var(--theme-subtext, #a6adc8);
  }
</style>
