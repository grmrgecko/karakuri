<svelte:options customElement="crypto-widget" />

<script lang="ts">
  import { untrack } from "svelte";
  import type { CryptoQuote, PricePoint, CryptoConfig, PortfolioHolding } from "./types.ts";
  import PriceCardView from "./layouts/PriceCardView.svelte";
  import ListTableView from "./layouts/ListTableView.svelte";
  import PortfolioView from "./layouts/PortfolioView.svelte";
  import HistoryChartView from "./layouts/HistoryChartView.svelte";
  import TickerTapeView from "./layouts/TickerTapeView.svelte";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  const DEFAULTS: CryptoConfig = {
    pair: "BTC/USD",
    layout: "price-card",
    provider: "coingecko",
    topCount: 10,
    quoteCurrency: "USD",
    holdings: [],
    sourceMode: "trending",
    pairs: [],
    timeline: "1D",
  };

  // Reactively parse the config attribute whenever it changes.
  let cfg: CryptoConfig = $derived((() => {
    let parsed: Record<string, unknown> = {};
    if (typeof configAttr === "string") {
      try {
        parsed = JSON.parse(configAttr);
      } catch {
        parsed = {};
      }
    } else if (typeof configAttr === "object" && configAttr !== null) {
      parsed = configAttr;
    }
    const merged = { ...DEFAULTS, ...parsed } as CryptoConfig;
    if (!Array.isArray(merged.holdings)) {
      merged.holdings = [];
    }
    if (!Array.isArray(merged.pairs)) {
      merged.pairs = [];
    }
    return merged;
  })());

  // Crypto data state.
  let currentQuote = $state<CryptoQuote | null>(null);
  let trendingCoins = $state<CryptoQuote[]>([]);
  let manualCoins = $state<CryptoQuote[]>([]);
  let portfolioQuotes = $state<Record<string, CryptoQuote>>({});
  let historyPoints = $state<PricePoint[]>([]);
  let loading = $state<boolean>(true);
  let error = $state<string | null>(null);
  let wsUnsubs: (() => void)[] = [];
  let staleTimer: ReturnType<typeof setTimeout> | null = null;

  // Whether this layout supports source mode toggling.
  let isMultiLayout = $derived(cfg.layout === "list-table" || cfg.layout === "ticker-tape");
  let sourceMode = $derived(isMultiLayout ? (cfg.sourceMode || "trending") : "");

  // Resolved list of coins for multi-layouts based on source mode.
  let listCoins = $derived(sourceMode === "manual" ? manualCoins : trendingCoins);

  // Fetch single-pair quote via HTTP.
  async function fetchQuote(): Promise<void> {
    if (isMultiLayout || cfg.layout === "portfolio") return;
    loading = true;
    error = null;
    try {
      const path = `/plugins/crypto/${cfg.provider}/${cfg.pair}`;
      const data = await (window as any).__karakuri?.api?.get(path);
      if (data) currentQuote = data;
      loading = false;
    } catch (err: unknown) {
      error = err instanceof Error ? err.message : "Failed to fetch crypto data";
      loading = false;
    }
  }

  // Fetch trending coins for list-table and ticker-tape layouts.
  async function fetchTrending(): Promise<void> {
    if (!isMultiLayout || sourceMode !== "trending") return;
    loading = true;
    error = null;
    try {
      const path = `/plugins/crypto/${cfg.provider}/trending?count=${cfg.topCount}&quote=${cfg.quoteCurrency}`;
      const data = await (window as any).__karakuri?.api?.get(path);
      if (Array.isArray(data)) trendingCoins = data;
      loading = false;
    } catch (err: unknown) {
      error = err instanceof Error ? err.message : "Failed to fetch trending coins";
      loading = false;
    }
  }

  // Fetch individual quotes for manual mode pairs.
  async function fetchManualPairQuotes(): Promise<void> {
    if (!isMultiLayout || sourceMode !== "manual") return;
    const pairs = cfg.pairs || [];
    if (pairs.length === 0) return;
    loading = true;
    error = null;
    try {
      const results: CryptoQuote[] = [];
      for (const pair of pairs) {
        const path = `/plugins/crypto/${cfg.provider}/${pair}`;
        const data = await (window as any).__karakuri?.api?.get(path);
        if (data) results.push(data);
      }
      manualCoins = results;
      loading = false;
    } catch (err: unknown) {
      error = err instanceof Error ? err.message : "Failed to fetch coin data";
      loading = false;
    }
  }

  // Fetch quotes for all portfolio holdings.
  async function fetchPortfolioQuotes(): Promise<void> {
    if (cfg.layout !== "portfolio" || cfg.holdings.length === 0) return;
    loading = true;
    error = null;
    try {
      const results: Record<string, CryptoQuote> = {};
      for (const h of cfg.holdings) {
        const path = `/plugins/crypto/${cfg.provider}/${h.pair}`;
        const data = await (window as any).__karakuri?.api?.get(path);
        if (data) results[h.pair.toUpperCase()] = data;
      }
      portfolioQuotes = results;
      loading = false;
    } catch (err: unknown) {
      error = err instanceof Error ? err.message : "Failed to fetch portfolio data";
      loading = false;
    }
  }

  // Fetch price history for history-chart and sparkline layouts.
  async function fetchHistory(): Promise<void> {
    if (cfg.layout !== "history-chart") return;
    try {
      const path = `/plugins/crypto/${cfg.provider}/${cfg.pair}/history?timeline=${cfg.timeline || "1D"}`;
      const data = await (window as any).__karakuri?.api?.get(path);
      if (Array.isArray(data)) historyPoints = data;
    } catch {
      // History fetch is non-critical; price card still works.
    }
  }

  // Subscribe to WebSocket topics for real-time updates.
  function subscribeWs(): void {
    unsubscribeWs();

    if (cfg.layout === "portfolio") {
      // Subscribe to each holding.
      for (const h of cfg.holdings) {
        subscribePair(h.pair, (data: CryptoQuote) => {
          portfolioQuotes = { ...portfolioQuotes, [data.pair]: data };
          loading = false;
          error = null;
          resetStaleTimer();
        });
      }
    } else if (isMultiLayout && sourceMode === "manual") {
      // Subscribe to each manual pair for real-time updates.
      for (const pair of cfg.pairs || []) {
        subscribePair(pair, (data: CryptoQuote) => {
          manualCoins = manualCoins.map((c) => (c.pair === data.pair ? data : c));
          loading = false;
          error = null;
          resetStaleTimer();
        });
      }
    } else if (isMultiLayout) {
      // Trending mode: subscribe to a trigger pair to re-fetch the list periodically.
      const triggerPair = trendingCoins.length > 0 ? trendingCoins[0].pair : "BTC/USD";
      subscribePair(triggerPair, () => {
        fetchTrending();
        resetStaleTimer();
      });
    } else {
      // Single pair subscription.
      subscribePair(cfg.pair, (data: CryptoQuote) => {
        currentQuote = data;
        loading = false;
        error = null;
        resetStaleTimer();
        if (cfg.layout === "history-chart") {
          fetchHistory();
        }
      });
    }
  }

  function subscribePair(pair: string, handler: (data: CryptoQuote) => void): void {
    const topic = `market:crypto:${cfg.provider}:${pair.toUpperCase()}`;
    if ((window as any).__karakuri?.subscribeTopic) {
      const unsub = (window as any).__karakuri.subscribeTopic(topic, handler);
      wsUnsubs.push(unsub);
    }
  }

  function unsubscribeWs(): void {
    for (const unsub of wsUnsubs) {
      unsub();
    }
    wsUnsubs = [];
  }

  // If no WebSocket update within 2 minutes, re-fetch via HTTP.
  function resetStaleTimer(): void {
    if (staleTimer) clearTimeout(staleTimer);
    staleTimer = setTimeout(() => {
      fetchAll();
    }, 2 * 60 * 1000);
  }

  function clearStaleTimer(): void {
    if (staleTimer) {
      clearTimeout(staleTimer);
      staleTimer = null;
    }
  }

  function fetchAll(): void {
    fetchQuote();
    fetchTrending();
    fetchManualPairQuotes();
    fetchPortfolioQuotes();
    fetchHistory();
  }

  function retry(): void {
    fetchAll();
    subscribeWs();
  }

  // Determine if there's data to show based on layout.
  let hasData: boolean = $derived((() => {
    switch (cfg.layout) {
      case "list-table":
      case "ticker-tape":
        return listCoins.length > 0;
      case "portfolio":
        return Object.keys(portfolioQuotes).length > 0;
      default:
        return currentQuote !== null;
    }
  })());

  // Fetch and subscribe on mount and whenever config changes.
  $effect(() => {
    const _key = `${cfg.provider}:${cfg.pair}:${cfg.layout}:${cfg.topCount}:${cfg.quoteCurrency}:${cfg.sourceMode}:${cfg.timeline}:${JSON.stringify(cfg.holdings)}:${JSON.stringify(cfg.pairs)}`;
    untrack(() => {
      fetchAll();
      subscribeWs();
      resetStaleTimer();
    });

    return () => {
      unsubscribeWs();
      clearStaleTimer();
    };
  });
</script>

<!-- Loading State -->
{#if loading && !hasData}
  <div class="loading-container">
    <svg class="spinner" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
      <circle cx="12" cy="12" r="10" stroke-opacity="0.25" />
      <path d="M12 2a10 10 0 0 1 10 10" stroke-linecap="round" />
    </svg>
    <span class="loading-text">Loading crypto data...</span>
  </div>

<!-- Error State -->
{:else if error && !hasData}
  <div class="error-container">
    <svg class="error-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <circle cx="12" cy="12" r="10" />
      <line x1="12" y1="8" x2="12" y2="12" />
      <line x1="12" y1="16" x2="12.01" y2="16" />
    </svg>
    <span class="error-text">{error}</span>
    <button class="retry-button" onclick={retry}>Retry</button>
  </div>

<!-- Crypto Display -->
{:else if hasData}
  <div class="crypto-container">
    {#if cfg.layout === "list-table"}
      <ListTableView coins={listCoins} />
    {:else if cfg.layout === "ticker-tape"}
      <TickerTapeView coins={listCoins} />
    {:else if cfg.layout === "portfolio"}
      <PortfolioView holdings={cfg.holdings} quotes={portfolioQuotes} />
    {:else if cfg.layout === "history-chart"}
      <HistoryChartView quote={currentQuote} points={historyPoints} timeline={cfg.timeline || "1D"} />
    {:else}
      <PriceCardView quote={currentQuote} />
    {/if}
  </div>
{/if}

<style>
  :host {
    display: flex;
    flex-direction: column;
    height: 100%;
    width: 100%;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    color: var(--theme-text, #cdd6f4);
    background: var(--theme-widget-bg, transparent);
    border-radius: var(--theme-radius-md, 8px);
    overflow: hidden;
    box-sizing: border-box;
  }

  .loading-container {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    flex: 1;
    gap: 0.75rem;
  }

  .spinner {
    width: 32px;
    height: 32px;
    color: var(--theme-accent, #89b4fa);
    animation: spin 1s linear infinite;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  .loading-text {
    font-size: 0.85rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .error-container {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    flex: 1;
    gap: 0.75rem;
    text-align: center;
    padding: 1rem;
  }

  .error-icon {
    width: 32px;
    height: 32px;
    color: var(--theme-error, #f38ba8);
  }

  .error-text {
    font-size: 0.85rem;
    color: var(--theme-error, #f38ba8);
    max-width: 200px;
    word-wrap: break-word;
  }

  .retry-button {
    padding: 0.4rem 1rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: var(--theme-radius-sm, 4px);
    background: var(--theme-surface-0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.8rem;
    cursor: pointer;
    transition: background 0.2s, border-color 0.2s;
    font-family: inherit;
  }

  .retry-button:hover {
    background: var(--theme-surface-1, #45475a);
    border-color: var(--theme-accent, #89b4fa);
  }

  .retry-button:active {
    transform: scale(0.97);
  }

  .crypto-container {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
  }
</style>
