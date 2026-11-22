<script lang="ts">
  import type { CryptoQuote } from "../types.ts";
  import { formatPrice, formatPercent, changeDirection } from "../formatters.ts";

  let {
    coins = [],
  }: {
    coins: CryptoQuote[];
  } = $props();

  let containerEl = $state<HTMLElement | null>(null);
  let contentEl = $state<HTMLElement | null>(null);

  // Scale text to fit the container height.
  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.fitText(containerEl, contentEl, { heightOnly: true });
    return () => cleanup?.();
  });
</script>

<div class="ticker-tape-view" bind:this={containerEl}>
  <div class="tape-track" bind:this={contentEl}>
    <!-- Duplicate the content for seamless looping. -->
    {#each [0, 1] as _copy}
      <div class="tape-content">
        {#each coins as coin}
          <span class="tape-item">
            {#if coin.image}
              <img class="tape-icon" src={coin.image} alt="" />
            {/if}
            <span class="tape-symbol">{coin.base}</span>
            <span class="tape-price">{formatPrice(coin.price)}</span>
            <span class="tape-change {changeDirection(coin.change_pct_24h)}">
              {formatPercent(coin.change_pct_24h)}
            </span>
          </span>
        {/each}
      </div>
    {/each}
  </div>
</div>

<style>
  .ticker-tape-view {
    width: 100%;
    height: 100%;
    overflow: hidden;
    display: flex;
    align-items: center;
  }

  .tape-track {
    display: flex;
    width: max-content;
    animation: scroll 20s linear infinite;
  }

  .tape-content {
    display: flex;
    align-items: center;
    gap: 1em;
    padding: 0 0.5em;
    flex-shrink: 0;
  }

  .tape-item {
    display: inline-flex;
    align-items: center;
    gap: 0.3em;
    white-space: nowrap;
  }

  .tape-icon {
    width: 1em;
    height: 1em;
    border-radius: 50%;
  }

  .tape-symbol {
    font-weight: 700;
    color: var(--theme-text, #cdd6f4);
    letter-spacing: 0.03em;
  }

  .tape-price {
    color: var(--theme-text, #cdd6f4);
  }

  .tape-change {
    font-size: 0.9em;
    font-weight: 500;
  }

  .tape-change.positive {
    color: var(--theme-success, #a6e3a1);
  }

  .tape-change.negative {
    color: var(--theme-error, #f38ba8);
  }

  .tape-change.neutral {
    color: var(--theme-subtext, #a6adc8);
  }

  @keyframes scroll {
    0% {
      transform: translateX(0);
    }
    100% {
      transform: translateX(-50%);
    }
  }

  .tape-track:hover {
    animation-play-state: paused;
  }
</style>
