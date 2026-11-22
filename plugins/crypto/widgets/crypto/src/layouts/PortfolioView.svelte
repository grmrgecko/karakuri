<script lang="ts">
  import type { CryptoQuote, PortfolioHolding } from "../types.ts";
  import { formatPrice, formatPercent, changeDirection } from "../formatters.ts";

  let {
    holdings = [],
    quotes = {},
  }: {
    holdings: PortfolioHolding[];
    quotes: Record<string, CryptoQuote>;
  } = $props();

  // Compute portfolio rows with current values.
  let rows = $derived(
    holdings.map((h) => {
      const quote = quotes[h.pair.toUpperCase()];
      const price = quote?.price ?? 0;
      const value = price * h.quantity;
      const changePct = quote?.change_pct_24h ?? 0;
      return {
        pair: h.pair,
        quantity: h.quantity,
        price,
        value,
        changePct,
        name: quote?.name ?? h.pair,
        image: quote?.image,
      };
    })
  );

  let totalValue = $derived(rows.reduce((sum, r) => sum + r.value, 0));

  // Weighted average 24h change.
  let totalChangePct = $derived((() => {
    if (totalValue === 0) return 0;
    return rows.reduce((sum, r) => sum + (r.value / totalValue) * r.changePct, 0);
  })());

  let totalDirection = $derived(changeDirection(totalChangePct));

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="portfolio-view" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
    <div class="portfolio-header">
      <div class="total-label">Portfolio Value</div>
      <div class="total-value">${formatPrice(totalValue)}</div>
      <div class="total-change {totalDirection}">
        {#if totalChangePct > 0}&#9650;{:else if totalChangePct < 0}&#9660;{:else}&#9644;{/if}
        {formatPercent(totalChangePct)} (24h)
      </div>
    </div>

    <div class="holdings-list">
      {#each rows as row}
        <div class="holding-row">
          <div class="holding-info">
            {#if row.image}
              <img class="coin-icon" src={row.image} alt="" />
            {/if}
            <div class="holding-text">
              <span class="holding-pair">{row.pair}</span>
              <span class="holding-qty">{row.quantity} units</span>
            </div>
          </div>
          <div class="holding-values">
            <span class="holding-value">${formatPrice(row.value)}</span>
            <span class="holding-change {changeDirection(row.changePct)}">
              {formatPercent(row.changePct)}
            </span>
          </div>
        </div>
      {/each}
      {#if rows.length === 0}
        <div class="empty-state">No holdings configured. Edit the widget to add coins.</div>
      {/if}
    </div>
  </div>
</div>

<style>
  .portfolio-view {
    flex: 1;
    width: 100%;
    height: 100%;
    overflow: hidden;
  }

  .scale-content {
    display: flex;
    flex-direction: column;
    padding: 0.5rem;
    box-sizing: border-box;
  }

  .portfolio-header {
    display: flex;
    flex-direction: column;
    align-items: center;
    padding: 0.5rem 0.6rem;
    border-bottom: 1px solid var(--theme-border, #45475a);
    flex-shrink: 0;
  }

  .total-label {
    font-size: 0.65rem;
    color: var(--theme-subtext, #a6adc8);
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  .total-value {
    font-size: 2.5rem;
    font-weight: 300;
    color: var(--theme-text, #cdd6f4);
    letter-spacing: -0.02em;
  }

  .total-change {
    font-size: 1rem;
    font-weight: 500;
  }

  .total-change.positive {
    color: var(--theme-success, #a6e3a1);
  }

  .total-change.negative {
    color: var(--theme-error, #f38ba8);
  }

  .total-change.neutral {
    color: var(--theme-subtext, #a6adc8);
  }

  .holdings-list {
    display: flex;
    flex-direction: column;
  }

  .holding-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.4rem 0.6rem;
    border-bottom: 1px solid var(--theme-border, #45475a);
    transition: background 0.15s;
  }

  .holding-row:hover {
    background: var(--theme-surface-1, #45475a);
  }

  .holding-info {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    min-width: 0;
  }

  .coin-icon {
    width: 18px;
    height: 18px;
    border-radius: 50%;
    flex-shrink: 0;
  }

  .holding-text {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  .holding-pair {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--theme-text, #cdd6f4);
  }

  .holding-qty {
    font-size: 0.65rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .holding-values {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
  }

  .holding-value {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--theme-text, #cdd6f4);
  }

  .holding-change {
    font-size: 0.65rem;
    font-weight: 500;
  }

  .holding-change.positive {
    color: var(--theme-success, #a6e3a1);
  }

  .holding-change.negative {
    color: var(--theme-error, #f38ba8);
  }

  .holding-change.neutral {
    color: var(--theme-subtext, #a6adc8);
  }

  .empty-state {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1rem;
    font-size: 0.85rem;
    color: var(--theme-subtext, #a6adc8);
    font-style: italic;
  }
</style>
