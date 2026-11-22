<script lang="ts">
  import type { StockQuote, PortfolioHolding } from "../types.ts";
  import { formatPrice, formatPercent, changeColor } from "../formatters.ts";

  let {
    holdings = [],
    quotes = {},
    colorLogic = "red-green",
  }: {
    holdings: PortfolioHolding[];
    quotes: Record<string, StockQuote>;
    colorLogic: string;
  } = $props();

  // Compute portfolio rows with current values.
  let rows = $derived(
    holdings.map((h) => {
      const quote = quotes[h.symbol.toUpperCase()];
      const price = quote?.price ?? 0;
      const value = price * h.quantity;
      const changePct = quote?.change_percent ?? 0;
      return {
        symbol: h.symbol,
        quantity: h.quantity,
        price,
        value,
        changePct,
        name: quote?.name ?? h.symbol,
      };
    })
  );

  let totalValue = $derived(rows.reduce((sum, r) => sum + r.value, 0));

  // Weighted average change percent.
  let totalChangePct = $derived((() => {
    if (totalValue === 0) return 0;
    return rows.reduce((sum, r) => sum + (r.value / totalValue) * r.changePct, 0);
  })());

  let totalColorClass = $derived(changeColor(totalChangePct, colorLogic));

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
      <div class="total-change {totalColorClass}">
        {#if totalChangePct > 0}&#9650;{:else if totalChangePct < 0}&#9660;{:else}&#9644;{/if}
        {formatPercent(totalChangePct)}
      </div>
    </div>

    <div class="holdings-list">
      {#each rows as row}
        <div class="holding-row">
          <div class="holding-info">
            <div class="holding-text">
              <span class="holding-symbol">{row.symbol}</span>
              <span class="holding-qty">{row.quantity} shares</span>
            </div>
          </div>
          <div class="holding-values">
            <span class="holding-value">${formatPrice(row.value)}</span>
            <span class="holding-change {changeColor(row.changePct, colorLogic)}">
              {formatPercent(row.changePct)}
            </span>
          </div>
        </div>
      {/each}
      {#if rows.length === 0}
        <div class="empty-state">No holdings configured. Edit the widget to add stocks.</div>
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

  .holding-text {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  .holding-symbol {
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
