<svelte:options customElement="stock-config" />

<script lang="ts">
  import type { StockConfig, SearchResult, ProviderEntry, PortfolioHolding } from "./types.ts";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  function autoFocus(node: HTMLElement) {
    node.focus();
  }

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

  let cfg = $state<StockConfig>({ ...DEFAULTS });
  let searchQuery = $state<string>("");
  let searchResults = $state<SearchResult[]>([]);
  let searching = $state<boolean>(false);
  let searchTimeout = $state<ReturnType<typeof setTimeout> | null>(null);
  let host = $state<HTMLElement | null>(null);
  let providers = $state<ProviderEntry[]>([]);
  let newHoldingSymbol = $state<string>("");
  let newHoldingQty = $state<string>("");
  let holdingSearchResults = $state<SearchResult[]>([]);
  let holdingSearching = $state<boolean>(false);
  let holdingSearchTimeout = $state<ReturnType<typeof setTimeout> | null>(null);
  let editingHoldingSymbol = $state<string | null>(null);
  let editingHoldingQty = $state<string>("");

  // Fetch available stock providers on mount.
  $effect(() => {
    (async () => {
      try {
        const resp = await (window as any).__karakuri?.api?.get("/providers/stock");
        if (Array.isArray(resp)) {
          providers = resp;
        }
      } catch {
        providers = [{ name: "yahoo", endpoint: "/plugins/stock/yahoo" }];
      }
    })();
  });

  $effect(() => {
    try {
      const parsed = typeof configAttr === "string" ? JSON.parse(configAttr) : configAttr;
      const merged = { ...DEFAULTS, ...parsed };
      if (!Array.isArray(merged.symbols)) {
        merged.symbols = typeof merged.symbols === "string" ? [merged.symbols] : ["AAPL"];
      }
      if (!Array.isArray(merged.holdings)) {
        merged.holdings = [];
      }
      cfg = merged as StockConfig;
    } catch {
      cfg = { ...DEFAULTS };
    }
  });

  function dispatch(): void {
    const event = new CustomEvent("configchange", {
      detail: { ...cfg },
      bubbles: true,
      composed: true,
    });
    host?.dispatchEvent(event);
  }

  function setHost(node: HTMLElement): void {
    host = (node.getRootNode() as ShadowRoot)?.host || node;
  }

  function onSearchInput(e: Event): void {
    searchQuery = (e.target as HTMLInputElement).value;
    if (searchTimeout) clearTimeout(searchTimeout);

    if (searchQuery.length < 2) {
      searchResults = [];
      return;
    }

    searchTimeout = setTimeout(async () => {
      searching = true;
      try {
        const results = await (window as any).__karakuri?.api?.get(
          `/plugins/stock/${cfg.provider}/search?q=${encodeURIComponent(searchQuery)}`
        );
        searchResults = Array.isArray(results) ? results : [];
      } catch {
        searchResults = [];
      } finally {
        searching = false;
      }
    }, 300);
  }

  function addSymbol(result: SearchResult): void {
    const symbol = result.symbol.toUpperCase();
    if (!cfg.symbols.includes(symbol)) {
      cfg.symbols = [...cfg.symbols, symbol];
      dispatch();
    }
    searchQuery = "";
    searchResults = [];
  }

  function removeSymbol(symbol: string): void {
    cfg.symbols = cfg.symbols.filter((s) => s !== symbol);
    dispatch();
  }

  function onHoldingSearchInput(e: Event): void {
    newHoldingSymbol = (e.target as HTMLInputElement).value;
    if (holdingSearchTimeout) clearTimeout(holdingSearchTimeout);

    if (newHoldingSymbol.length < 2) {
      holdingSearchResults = [];
      return;
    }

    holdingSearchTimeout = setTimeout(async () => {
      holdingSearching = true;
      try {
        const results = await (window as any).__karakuri?.api?.get(
          `/plugins/stock/${cfg.provider}/search?q=${encodeURIComponent(newHoldingSymbol)}`
        );
        holdingSearchResults = Array.isArray(results) ? results : [];
      } catch {
        holdingSearchResults = [];
      } finally {
        holdingSearching = false;
      }
    }, 300);
  }

  function selectHoldingSymbol(result: SearchResult): void {
    newHoldingSymbol = result.symbol.toUpperCase();
    holdingSearchResults = [];
  }

  function onHoldingSearchBlur(): void {
    setTimeout(() => {
      holdingSearchResults = [];
    }, 200);
  }

  function addHolding(): void {
    const symbol = newHoldingSymbol.toUpperCase().trim();
    const qty = parseFloat(newHoldingQty);
    if (!symbol || isNaN(qty) || qty <= 0) return;

    if (!cfg.holdings) cfg.holdings = [];
    const existing = cfg.holdings.findIndex((h) => h.symbol === symbol);
    if (existing >= 0) {
      cfg.holdings[existing].quantity = qty;
      cfg.holdings = [...cfg.holdings];
    } else {
      cfg.holdings = [...cfg.holdings, { symbol, quantity: qty }];
    }
    newHoldingSymbol = "";
    newHoldingQty = "";
    holdingSearchResults = [];
    dispatch();
  }

  function removeHolding(symbol: string): void {
    if (!cfg.holdings) return;
    cfg.holdings = cfg.holdings.filter((h) => h.symbol !== symbol);
    dispatch();
  }

  function startEditQty(symbol: string, currentQty: number): void {
    editingHoldingSymbol = symbol;
    editingHoldingQty = String(currentQty);
  }

  function commitEditQty(): void {
    if (!editingHoldingSymbol || !cfg.holdings) return;
    const qty = parseFloat(editingHoldingQty);
    if (isNaN(qty) || qty <= 0) {
      cancelEditQty();
      return;
    }
    const idx = cfg.holdings.findIndex((h) => h.symbol === editingHoldingSymbol);
    if (idx >= 0) {
      cfg.holdings[idx].quantity = qty;
      cfg.holdings = [...cfg.holdings];
      dispatch();
    }
    editingHoldingSymbol = null;
    editingHoldingQty = "";
  }

  function cancelEditQty(): void {
    editingHoldingSymbol = null;
    editingHoldingQty = "";
  }

  // Layouts that only support a single symbol.
  let singleSymbolMode = $derived(
    cfg.layout === "single-quote" || cfg.layout === "history-chart" || cfg.layout === "candlestick"
  );

  // Whether the current layout supports source mode toggling.
  let isMultiLayout = $derived(cfg.layout === "list-table" || cfg.layout === "ticker-tape");
  let currentSourceMode = $derived(isMultiLayout ? (cfg.sourceMode || "manual") : "");
  let showTrendingCount = $derived(isMultiLayout && currentSourceMode === "trending");
  let showManualSymbols = $derived(isMultiLayout && currentSourceMode === "manual");
  let showPortfolio = $derived(cfg.layout === "portfolio");

  // Trim symbols to one when switching to a single-symbol layout.
  $effect(() => {
    if (singleSymbolMode && cfg.symbols.length > 1) {
      cfg.symbols = [cfg.symbols[0]];
      dispatch();
    }
  });

  let searchFocused = $state<boolean>(false);

  function onSearchFocus(): void {
    searchFocused = true;
    searchQuery = "";
  }

  function onSearchBlur(): void {
    // Delay to allow click on search results to fire first.
    setTimeout(() => {
      searchFocused = false;
      searchQuery = "";
      searchResults = [];
    }, 200);
  }

  function selectSymbol(result: SearchResult): void {
    const symbol = result.symbol.toUpperCase();
    cfg.symbols = [symbol];
    searchFocused = false;
    searchQuery = "";
    searchResults = [];
    dispatch();
  }

  let showInterval = $derived(cfg.layout === "history-chart" || cfg.layout === "candlestick");
</script>

<div class="config-form" use:setHost>
  <!-- Layout -->
  <div class="field">
    <label class="label" for="layout-select">Layout</label>
    <select
      id="layout-select"
      class="input"
      value={cfg.layout}
      onchange={(e: Event) => { cfg.layout = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="single-quote">Single Quote</option>
      <option value="ticker-tape">Ticker Tape</option>
      <option value="list-table">List Table</option>
      <option value="candlestick">Candlestick Chart</option>
      <option value="history-chart">History Chart</option>
      <option value="portfolio">Portfolio</option>
    </select>
  </div>

  {#if singleSymbolMode}
    <!-- Single symbol search -->
    <div class="field">
      <label class="label" for="search-symbol-input">Symbol</label>
      <div class="search-container">
        <input
          id="search-symbol-input"
          type="text"
          class="input"
          placeholder={searchFocused ? "Search for a ticker..." : ""}
          value={searchFocused ? searchQuery : (cfg.symbols[0] || "")}
          oninput={onSearchInput}
          onfocus={onSearchFocus}
          onblur={onSearchBlur}
        />
        {#if searching}
          <div class="search-spinner"></div>
        {/if}
      </div>
      {#if searchResults.length > 0}
        <div class="search-results">
          {#each searchResults as result}
            <button class="search-result" onclick={() => selectSymbol(result)}>
              <span class="result-symbol">{result.symbol}</span>
              <span class="result-name">{result.name}</span>
              <span class="result-detail">{result.exchange}</span>
            </button>
          {/each}
        </div>
      {/if}
    </div>
  {/if}

  <!-- Source Mode (list-table/ticker-tape only) -->
  {#if isMultiLayout}
    <div class="field">
      <label class="label" for="source-mode-select">Source</label>
      <select
        id="source-mode-select"
        class="input"
        value={cfg.sourceMode || "manual"}
        onchange={(e: Event) => { cfg.sourceMode = (e.target as HTMLSelectElement).value; dispatch(); }}
      >
        <option value="trending">Trending Stocks</option>
        <option value="manual">Manual List</option>
      </select>
    </div>
  {/if}

  <!-- Number of Stocks (trending mode only) -->
  {#if showTrendingCount}
    <div class="field">
      <label class="label" for="top-count-select">Number of Stocks</label>
      <select
        id="top-count-select"
        class="input"
        value={String(cfg.topCount || 10)}
        onchange={(e: Event) => { cfg.topCount = parseInt((e.target as HTMLSelectElement).value, 10); dispatch(); }}
      >
        <option value="5">5</option>
        <option value="10">10</option>
        <option value="20">20</option>
      </select>
    </div>
  {/if}

  <!-- Multi-symbol search (manual mode for list-table/ticker-tape) -->
  {#if showManualSymbols}
    <div class="field">
      <label class="label" for="search-symbol-input">Add Symbol</label>
      <div class="search-container">
        <input
          id="search-symbol-input"
          type="text"
          class="input"
          placeholder="Search for a ticker..."
          value={searchQuery}
          oninput={onSearchInput}
        />
        {#if searching}
          <div class="search-spinner"></div>
        {/if}
      </div>
      {#if searchResults.length > 0}
        <div class="search-results">
          {#each searchResults as result}
            <button class="search-result" onclick={() => addSymbol(result)}>
              <span class="result-symbol">{result.symbol}</span>
              <span class="result-name">{result.name}</span>
              <span class="result-detail">{result.exchange}</span>
            </button>
          {/each}
        </div>
      {/if}
    </div>

    <!-- Symbol chips -->
    <div class="field">
      <span class="label">Symbols</span>
      <div class="chip-list">
        {#each cfg.symbols as symbol}
          <span class="chip">
            {symbol}
            <button class="chip-remove" onclick={() => removeSymbol(symbol)} aria-label="Remove {symbol}">&times;</button>
          </span>
        {/each}
        {#if cfg.symbols.length === 0}
          <span class="chip-empty">No symbols added</span>
        {/if}
      </div>
    </div>
  {/if}

  <!-- Portfolio holdings (portfolio only) -->
  {#if showPortfolio}
    <div class="field">
      <span class="label">Holdings</span>
      <div class="holdings-list">
        {#each cfg.holdings || [] as holding}
          <div class="holding-row">
            <span class="holding-symbol">{holding.symbol}</span>
            {#if editingHoldingSymbol === holding.symbol}
              <input
                type="number"
                class="qty-edit-input"
                value={editingHoldingQty}
                oninput={(e: Event) => { editingHoldingQty = (e.target as HTMLInputElement).value; }}
                onblur={commitEditQty}
                onkeydown={(e: KeyboardEvent) => { if (e.key === "Enter") commitEditQty(); if (e.key === "Escape") cancelEditQty(); }}
                use:autoFocus
              />
            {:else}
              <button class="holding-qty" onclick={() => startEditQty(holding.symbol, holding.quantity)}>{holding.quantity}</button>
            {/if}
            <button class="holding-remove" onclick={() => removeHolding(holding.symbol)} aria-label="Remove {holding.symbol}">&times;</button>
          </div>
        {/each}
        {#if !cfg.holdings || cfg.holdings.length === 0}
          <span class="holdings-empty">No holdings added</span>
        {/if}
      </div>
      <div class="add-holding-form">
        <div class="add-holding-symbol">
          <div class="search-container">
            <input
              type="text"
              class="input holding-input"
              placeholder="Search ticker..."
              value={newHoldingSymbol}
              oninput={onHoldingSearchInput}
              onblur={onHoldingSearchBlur}
            />
            {#if holdingSearching}
              <div class="search-spinner"></div>
            {/if}
          </div>
          {#if holdingSearchResults.length > 0}
            <div class="search-results">
              {#each holdingSearchResults as result}
                <button class="search-result" onclick={() => selectHoldingSymbol(result)}>
                  <span class="result-symbol">{result.symbol}</span>
                  <span class="result-name">{result.name}</span>
                  <span class="result-detail">{result.exchange}</span>
                </button>
              {/each}
            </div>
          {/if}
        </div>
        <div class="add-holding-row">
          <input
            type="number"
            class="input holding-input"
            placeholder="Qty"
            value={newHoldingQty}
            oninput={(e: Event) => { newHoldingQty = (e.target as HTMLInputElement).value; }}
          />
          <button class="add-button" onclick={addHolding}>Add</button>
        </div>
      </div>
    </div>
  {/if}

  <!-- Timeline (sparkline/candlestick only) -->
  {#if showInterval}
    <div class="field">
      <label class="label" for="interval-select">Timeline</label>
      <select
        id="interval-select"
        class="input"
        value={cfg.interval}
        onchange={(e: Event) => { cfg.interval = (e.target as HTMLSelectElement).value; dispatch(); }}
      >
        <option value="1D">1 Day</option>
        <option value="1W">1 Week</option>
        <option value="1M">1 Month</option>
        <option value="3M">3 Months</option>
        <option value="1Y">1 Year</option>
      </select>
    </div>
  {/if}

  <!-- Color Logic -->
  <div class="field">
    <label class="label" for="color-logic-select">Color Logic</label>
    <select
      id="color-logic-select"
      class="input"
      value={cfg.colorLogic}
      onchange={(e: Event) => { cfg.colorLogic = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="red-green">Red/Green (US Standard)</option>
      <option value="green-red">Green/Red (Alternate)</option>
    </select>
  </div>

  <!-- Provider -->
  <div class="field">
    <label class="label" for="provider-select">Provider</label>
    <select
      id="provider-select"
      class="input"
      value={cfg.provider}
      onchange={(e: Event) => { cfg.provider = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      {#each providers as p}
        <option value={p.name}>{p.display_name || p.name}</option>
      {/each}
      {#if providers.length === 0}
        <option value={cfg.provider}>{cfg.provider}</option>
      {/if}
    </select>
  </div>
</div>

<style>
  :host {
    display: block;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    color: var(--theme-text, #cdd6f4);
  }

  .config-form {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }

  .label {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--theme-subtext, #a6adc8);
  }

  .input {
    padding: 0.4rem 0.6rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.85rem;
    font-family: inherit;
    outline: none;
    width: 100%;
    box-sizing: border-box;
  }

  .input:focus {
    border-color: var(--theme-accent, #89b4fa);
  }

  .search-container {
    position: relative;
  }

  .search-spinner {
    position: absolute;
    right: 8px;
    top: 50%;
    transform: translateY(-50%);
    width: 14px;
    height: 14px;
    border: 2px solid var(--theme-surface1, #45475a);
    border-top-color: var(--theme-accent, #89b4fa);
    border-radius: 50%;
    animation: spin 0.6s linear infinite;
  }

  @keyframes spin {
    to { transform: translateY(-50%) rotate(360deg); }
  }

  .search-results {
    display: flex;
    flex-direction: column;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    max-height: 150px;
    overflow-y: auto;
  }

  .search-result {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.4rem 0.6rem;
    border: none;
    background: none;
    color: var(--theme-text, #cdd6f4);
    text-align: left;
    cursor: pointer;
    font-family: inherit;
    font-size: 0.85rem;
  }

  .search-result:hover {
    background: var(--theme-surface1, #45475a);
  }

  .search-result + .search-result {
    border-top: 1px solid var(--theme-border, #45475a);
  }

  .result-symbol {
    font-weight: 600;
    min-width: 4rem;
  }

  .result-name {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .result-detail {
    font-size: 0.75rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .chip-list {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
  }

  .chip {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
    padding: 0.2rem 0.5rem;
    background: var(--theme-surface0, #313244);
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    font-size: 0.8rem;
    font-weight: 600;
  }

  .chip-remove {
    background: none;
    border: none;
    color: var(--theme-subtext, #a6adc8);
    cursor: pointer;
    font-size: 1rem;
    line-height: 1;
    padding: 0 0.15rem;
    font-family: inherit;
  }

  .chip-remove:hover {
    color: var(--theme-error, #f38ba8);
  }

  .chip-empty {
    font-size: 0.8rem;
    color: var(--theme-subtext, #a6adc8);
    font-style: italic;
  }

  .holdings-list {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }

  .holding-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.3rem 0.5rem;
    background: var(--theme-surface0, #313244);
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    font-size: 0.85rem;
  }

  .holding-symbol {
    font-weight: 600;
    flex: 1;
  }

  .holding-qty {
    background: none;
    border: 1px solid transparent;
    border-radius: 3px;
    color: var(--theme-subtext, #a6adc8);
    font-size: 0.85rem;
    font-family: inherit;
    padding: 0.1rem 0.3rem;
    cursor: pointer;
  }

  .holding-qty:hover {
    border-color: var(--theme-border, #45475a);
    background: var(--theme-surface-1, #45475a);
  }

  .qty-edit-input {
    width: 4rem;
    padding: 0.1rem 0.3rem;
    border: 1px solid var(--theme-accent, #89b4fa);
    border-radius: 3px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.85rem;
    font-family: inherit;
    outline: none;
    box-sizing: border-box;
  }

  .holding-remove {
    background: none;
    border: none;
    color: var(--theme-subtext, #a6adc8);
    cursor: pointer;
    font-size: 1rem;
    line-height: 1;
    padding: 0 0.15rem;
    font-family: inherit;
  }

  .holding-remove:hover {
    color: var(--theme-error, #f38ba8);
  }

  .holdings-empty {
    font-size: 0.8rem;
    color: var(--theme-subtext, #a6adc8);
    font-style: italic;
  }

  .add-holding-form {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    margin-top: 0.25rem;
  }

  .add-holding-symbol {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }

  .add-holding-row {
    display: flex;
    gap: 0.3rem;
  }

  .holding-input {
    flex: 1;
    min-width: 0;
  }

  .add-button {
    padding: 0.4rem 0.75rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.8rem;
    cursor: pointer;
    font-family: inherit;
    white-space: nowrap;
  }

  .add-button:hover {
    background: var(--theme-surface1, #45475a);
    border-color: var(--theme-accent, #89b4fa);
  }
</style>
