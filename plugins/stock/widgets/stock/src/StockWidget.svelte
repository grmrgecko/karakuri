<svelte:options customElement="stock-widget" />

<script lang="ts">
  import { untrack } from "svelte";
  import type { StockQuote, OHLCData, StockConfig } from "./types.ts";
  import SingleQuoteView from "./layouts/SingleQuoteView.svelte";
  import TickerTapeView from "./layouts/TickerTapeView.svelte";
  import CandlestickView from "./layouts/CandlestickView.svelte";
  import HistoryChartView from "./layouts/HistoryChartView.svelte";
  import ListTableView from "./layouts/ListTableView.svelte";
  import PortfolioView from "./layouts/PortfolioView.svelte";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  const DEFAULTS: StockConfig = {
    symbols: ["AAPL"],
    layout: "single-quote",
    interval: "1D",
    colorLogic: "red-green",
    provider: "yahoo",
    sourceMode: "trending",
    topCount: 10,
    holdings: [],
  };

  // Reactively parse the config attribute whenever it changes.
  let cfg: StockConfig = $derived((() => {
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
    const merged = { ...DEFAULTS, ...parsed } as StockConfig;
    // Ensure symbols is always an array.
    if (!Array.isArray(merged.symbols)) {
      merged.symbols = typeof merged.symbols === "string" ? [merged.symbols] : ["AAPL"];
    }
    if (!Array.isArray(merged.holdings)) {
      merged.holdings = [];
    }
    return merged;
  })());

  // Stock data state.
  let quotes = $state<Record<string, StockQuote>>({});
  let trendingQuotes = $state<StockQuote[]>([]);
  let ohlcData = $state<Record<string, OHLCData>>({});
  let portfolioQuotes = $state<Record<string, StockQuote>>({});
  let loading = $state<boolean>(true);
  let error = $state<string | null>(null);
  let wsUnsubs: (() => void)[] = [];
  let staleTimer: ReturnType<typeof setTimeout> | null = null;

  // Whether this layout supports source mode toggling.
  let isMultiLayout = $derived(cfg.layout === "list-table" || cfg.layout === "ticker-tape");
  let sourceMode = $derived(isMultiLayout ? (cfg.sourceMode || "manual") : "");

  // Fetch quote data for all symbols via HTTP (manual mode or non-multi layouts).
  async function fetchQuotes(): Promise<void> {
    if (isMultiLayout && sourceMode === "trending") return;
    loading = true;
    error = null;
    try {
      const results: Record<string, StockQuote> = {};
      for (const symbol of cfg.symbols) {
        const path = `/plugins/stock/${cfg.provider}/${symbol}`;
        const data = await (window as any).__karakuri?.api?.get(path);
        if (data) results[symbol.toUpperCase()] = data;
      }
      quotes = results;
      loading = false;
    } catch (err: unknown) {
      error = err instanceof Error ? err.message : "Failed to fetch stock data";
      loading = false;
    }
  }

  // Fetch trending stocks for list-table and ticker-tape layouts.
  async function fetchTrending(): Promise<void> {
    if (!isMultiLayout || sourceMode !== "trending") return;
    loading = true;
    error = null;
    try {
      const count = cfg.topCount || 10;
      const path = `/plugins/stock/${cfg.provider}/trending?count=${count}`;
      const data = await (window as any).__karakuri?.api?.get(path);
      if (Array.isArray(data)) trendingQuotes = data;
      loading = false;
    } catch (err: unknown) {
      error = err instanceof Error ? err.message : "Failed to fetch trending stocks";
      loading = false;
    }
  }

  // Fetch OHLC data for sparkline and candlestick layouts.
  async function fetchOHLC(): Promise<void> {
    if (cfg.layout !== "history-chart" && cfg.layout !== "candlestick") return;
    try {
      const results: Record<string, OHLCData> = {};
      const symbol = cfg.symbols[0];
      if (symbol) {
        const path = `/plugins/stock/${cfg.provider}/${symbol}/ohlc?interval=${cfg.interval}`;
        const data = await (window as any).__karakuri?.api?.get(path);
        if (data) results[symbol.toUpperCase()] = data;
      }
      ohlcData = results;
    } catch {
      // OHLC fetch failures are non-critical; quote display still works.
    }
  }

  // Fetch quotes for portfolio holdings.
  async function fetchPortfolioQuotes(): Promise<void> {
    if (cfg.layout !== "portfolio" || !cfg.holdings || cfg.holdings.length === 0) return;
    loading = true;
    error = null;
    try {
      const results: Record<string, StockQuote> = {};
      for (const holding of cfg.holdings) {
        const path = `/plugins/stock/${cfg.provider}/${holding.symbol}`;
        const data = await (window as any).__karakuri?.api?.get(path);
        if (data) results[holding.symbol.toUpperCase()] = data;
      }
      portfolioQuotes = results;
      loading = false;
    } catch (err: unknown) {
      error = err instanceof Error ? err.message : "Failed to fetch portfolio data";
      loading = false;
    }
  }

  // Subscribe to WebSocket topics for real-time updates.
  function subscribeWs(): void {
    unsubscribeWs();
    if (isMultiLayout && sourceMode === "trending") {
      // For trending, subscribe to a trigger symbol to re-fetch periodically.
      const triggerSymbol = trendingQuotes.length > 0 ? trendingQuotes[0].symbol : "AAPL";
      const topic = `market:stock:${cfg.provider}:${triggerSymbol}`;
      if ((window as any).__karakuri?.subscribeTopic) {
        const unsub = (window as any).__karakuri.subscribeTopic(topic, () => {
          fetchTrending();
          resetStaleTimer();
        });
        wsUnsubs.push(unsub);
      }
      return;
    }
    // Portfolio mode: subscribe to each holding's symbol.
    if (cfg.layout === "portfolio" && cfg.holdings && cfg.holdings.length > 0) {
      for (const holding of cfg.holdings) {
        const topic = `market:stock:${cfg.provider}:${holding.symbol.toUpperCase()}`;
        if ((window as any).__karakuri?.subscribeTopic) {
          const unsub = (window as any).__karakuri.subscribeTopic(topic, (data: StockQuote) => {
            portfolioQuotes = { ...portfolioQuotes, [data.symbol]: data };
            loading = false;
            error = null;
            resetStaleTimer();
          });
          wsUnsubs.push(unsub);
        }
      }
      return;
    }
    for (const symbol of cfg.symbols) {
      const topic = `market:stock:${cfg.provider}:${symbol.toUpperCase()}`;
      if ((window as any).__karakuri?.subscribeTopic) {
        const unsub = (window as any).__karakuri.subscribeTopic(topic, (data: StockQuote) => {
          quotes = { ...quotes, [data.symbol]: data };
          loading = false;
          error = null;
          resetStaleTimer();
        });
        wsUnsubs.push(unsub);
      }
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
      fetchQuotes();
    }, 2 * 60 * 1000);
  }

  function clearStaleTimer(): void {
    if (staleTimer) {
      clearTimeout(staleTimer);
      staleTimer = null;
    }
  }

  function retry(): void {
    fetchQuotes();
    fetchTrending();
    fetchOHLC();
    fetchPortfolioQuotes();
    subscribeWs();
  }

  // Derived: list of quotes for rendering, incorporating trending mode.
  let quoteList: StockQuote[] = $derived((() => {
    if (isMultiLayout && sourceMode === "trending") {
      return trendingQuotes;
    }
    return cfg.symbols
      .map((s) => quotes[s.toUpperCase()])
      .filter((q): q is StockQuote => q != null);
  })());

  let primaryOHLC: OHLCData | null = $derived(
    cfg.symbols[0] ? ohlcData[cfg.symbols[0].toUpperCase()] ?? null : null
  );

  let primaryQuote: StockQuote | null = $derived(
    cfg.symbols[0] ? quotes[cfg.symbols[0].toUpperCase()] ?? null : null
  );

  // Whether we have data to render for the current layout.
  let hasData = $derived((() => {
    if (cfg.layout === "portfolio") {
      return Object.keys(portfolioQuotes).length > 0;
    }
    return quoteList.length > 0;
  })());

  // Fetch and subscribe on mount and whenever config changes.
  $effect(() => {
    const _key = `${cfg.provider}:${cfg.symbols.join(",")}:${cfg.layout}:${cfg.interval}:${cfg.sourceMode}:${cfg.topCount}:${JSON.stringify(cfg.holdings)}`;
    untrack(() => {
      fetchQuotes();
      fetchTrending();
      fetchOHLC();
      fetchPortfolioQuotes();
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
    <span class="loading-text">Loading stock data...</span>
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

<!-- Stock Display -->
{:else if hasData}
  <div class="stock-container">
    {#if cfg.layout === "portfolio"}
      <PortfolioView
        holdings={cfg.holdings || []}
        quotes={portfolioQuotes}
        colorLogic={cfg.colorLogic}
      />
    {:else if cfg.layout === "ticker-tape"}
      <TickerTapeView
        quotes={quoteList}
        colorLogic={cfg.colorLogic}
      />
    {:else if cfg.layout === "list-table"}
      <ListTableView
        quotes={quoteList}
        colorLogic={cfg.colorLogic}
      />
    {:else if cfg.layout === "history-chart"}
      <HistoryChartView
        quote={primaryQuote}
        ohlc={primaryOHLC}
        colorLogic={cfg.colorLogic}
      />
    {:else if cfg.layout === "candlestick"}
      <CandlestickView
        quote={primaryQuote}
        ohlc={primaryOHLC}
        colorLogic={cfg.colorLogic}
      />
    {:else}
      <SingleQuoteView
        quote={primaryQuote}
        colorLogic={cfg.colorLogic}
      />
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

  .stock-container {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
  }
</style>
