<svelte:options customElement="crypto-config" />

<script lang="ts">
  import type { CryptoConfig, SearchResult, ProviderEntry, PortfolioHolding } from "./types.ts";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  function autoFocus(node: HTMLElement) {
    node.focus();
  }

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

  let cfg = $state<CryptoConfig>({ ...DEFAULTS });
  let searchQuery = $state<string>("");
  let searchResults = $state<SearchResult[]>([]);
  let searching = $state<boolean>(false);
  let searchTimeout = $state<ReturnType<typeof setTimeout> | null>(null);
  let host = $state<HTMLElement | null>(null);
  let providers = $state<ProviderEntry[]>([]);
  let newHoldingPair = $state<string>("");
  let newHoldingQty = $state<string>("");
  let holdingSearchResults = $state<SearchResult[]>([]);
  let holdingSearching = $state<boolean>(false);
  let holdingSearchTimeout = $state<ReturnType<typeof setTimeout> | null>(null);
  let editingHoldingPair = $state<string | null>(null);
  let editingHoldingQty = $state<string>("");

  // Fetch available crypto providers on mount.
  $effect(() => {
    (async () => {
      try {
        const resp = await (window as any).__karakuri?.api?.get("/providers/crypto");
        if (Array.isArray(resp)) {
          providers = resp;
        }
      } catch {
        providers = [{ name: "coingecko", endpoint: "/plugins/crypto/coingecko" }];
      }
    })();
  });

  $effect(() => {
    try {
      const parsed = typeof configAttr === "string" ? JSON.parse(configAttr) : configAttr;
      const merged = { ...DEFAULTS, ...parsed };
      if (!Array.isArray(merged.holdings)) {
        merged.holdings = [];
      }
      if (!Array.isArray(merged.pairs)) {
        merged.pairs = [];
      }
      cfg = merged as CryptoConfig;
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
          `/plugins/crypto/${cfg.provider}/search?q=${encodeURIComponent(searchQuery)}`
        );
        searchResults = Array.isArray(results) ? results : [];
      } catch {
        searchResults = [];
      } finally {
        searching = false;
      }
    }, 300);
  }

  function selectPair(result: SearchResult): void {
    cfg.pair = result.pair.toUpperCase();
    searchFocused = false;
    searchQuery = "";
    searchResults = [];
    dispatch();
  }

  function onHoldingSearchInput(e: Event): void {
    newHoldingPair = (e.target as HTMLInputElement).value;
    if (holdingSearchTimeout) clearTimeout(holdingSearchTimeout);

    if (newHoldingPair.length < 2) {
      holdingSearchResults = [];
      return;
    }

    holdingSearchTimeout = setTimeout(async () => {
      holdingSearching = true;
      try {
        const results = await (window as any).__karakuri?.api?.get(
          `/plugins/crypto/${cfg.provider}/search?q=${encodeURIComponent(newHoldingPair)}`
        );
        holdingSearchResults = Array.isArray(results) ? results : [];
      } catch {
        holdingSearchResults = [];
      } finally {
        holdingSearching = false;
      }
    }, 300);
  }

  function selectHoldingPair(result: SearchResult): void {
    newHoldingPair = result.pair.toUpperCase();
    holdingSearchResults = [];
  }

  function onHoldingSearchBlur(): void {
    setTimeout(() => {
      holdingSearchResults = [];
    }, 200);
  }

  function addHolding(): void {
    const pair = newHoldingPair.toUpperCase().trim();
    const qty = parseFloat(newHoldingQty);
    if (!pair || isNaN(qty) || qty <= 0) return;

    // Update existing holding or add new.
    const existing = cfg.holdings.findIndex((h) => h.pair === pair);
    if (existing >= 0) {
      cfg.holdings[existing].quantity = qty;
      cfg.holdings = [...cfg.holdings];
    } else {
      cfg.holdings = [...cfg.holdings, { pair, quantity: qty }];
    }
    newHoldingPair = "";
    newHoldingQty = "";
    holdingSearchResults = [];
    dispatch();
  }

  function removeHolding(pair: string): void {
    cfg.holdings = cfg.holdings.filter((h) => h.pair !== pair);
    dispatch();
  }

  function startEditQty(pair: string, currentQty: number): void {
    editingHoldingPair = pair;
    editingHoldingQty = String(currentQty);
  }

  function commitEditQty(): void {
    if (!editingHoldingPair) return;
    const qty = parseFloat(editingHoldingQty);
    if (isNaN(qty) || qty <= 0) {
      cancelEditQty();
      return;
    }
    const idx = cfg.holdings.findIndex((h) => h.pair === editingHoldingPair);
    if (idx >= 0) {
      cfg.holdings[idx].quantity = qty;
      cfg.holdings = [...cfg.holdings];
      dispatch();
    }
    editingHoldingPair = null;
    editingHoldingQty = "";
  }

  function cancelEditQty(): void {
    editingHoldingPair = null;
    editingHoldingQty = "";
  }

  function addManualPair(result: SearchResult): void {
    const pair = result.pair.toUpperCase();
    if (!cfg.pairs) cfg.pairs = [];
    if (!cfg.pairs.includes(pair)) {
      cfg.pairs = [...cfg.pairs, pair];
      dispatch();
    }
    searchQuery = "";
    searchResults = [];
  }

  function removeManualPair(pair: string): void {
    cfg.pairs = (cfg.pairs || []).filter((p) => p !== pair);
    dispatch();
  }

  let showPairSearch = $derived(cfg.layout === "price-card" || cfg.layout === "history-chart");
  let isMultiLayout = $derived(cfg.layout === "list-table" || cfg.layout === "ticker-tape");
  let currentSourceMode = $derived(isMultiLayout ? (cfg.sourceMode || "trending") : "");
  let showTrendingOptions = $derived(isMultiLayout && currentSourceMode === "trending");
  let showManualPairs = $derived(isMultiLayout && currentSourceMode === "manual");
  let showPortfolio = $derived(cfg.layout === "portfolio");
  let showTimeline = $derived(cfg.layout === "history-chart");
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
      <option value="price-card">Price Card</option>
      <option value="ticker-tape">Ticker Tape</option>
      <option value="list-table">List Table</option>
      <option value="portfolio">Portfolio</option>
      <option value="history-chart">History Chart</option>
    </select>
  </div>

  <!-- Pair search (price-card and history-chart only) -->
  {#if showPairSearch}
    <div class="field">
      <label class="label" for="pair-search-input">Trading Pair</label>
      <div class="search-container">
        <input
          id="pair-search-input"
          type="text"
          class="input"
          placeholder={searchFocused ? "Search for a coin..." : ""}
          value={searchFocused ? searchQuery : cfg.pair}
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
            <button class="search-result" onclick={() => selectPair(result)}>
              {#if result.image}
                <img class="result-icon" src={result.image} alt="" />
              {/if}
              <span class="result-symbol">{result.pair}</span>
              <span class="result-name">{result.name}</span>
            </button>
          {/each}
        </div>
      {/if}
    </div>
  {/if}

  <!-- Source mode (list-table and ticker-tape only) -->
  {#if isMultiLayout}
    <div class="field">
      <label class="label" for="source-mode-select">Source</label>
      <select
        id="source-mode-select"
        class="input"
        value={currentSourceMode}
        onchange={(e: Event) => { cfg.sourceMode = (e.target as HTMLSelectElement).value; dispatch(); }}
      >
        <option value="trending">Trending Crypto</option>
        <option value="manual">Manual List</option>
      </select>
    </div>
  {/if}

  <!-- Trending options -->
  {#if showTrendingOptions}
    <div class="field">
      <label class="label" for="top-count-select">Number of Coins</label>
      <select
        id="top-count-select"
        class="input"
        value={String(cfg.topCount)}
        onchange={(e: Event) => { cfg.topCount = parseInt((e.target as HTMLSelectElement).value); dispatch(); }}
      >
        <option value="5">Top 5</option>
        <option value="10">Top 10</option>
        <option value="20">Top 20</option>
      </select>
    </div>

    <div class="field">
      <label class="label" for="quote-currency-select">Quote Currency</label>
      <select
        id="quote-currency-select"
        class="input"
        value={cfg.quoteCurrency}
        onchange={(e: Event) => { cfg.quoteCurrency = (e.target as HTMLSelectElement).value; dispatch(); }}
      >
        <option value="USD">USD</option>
        <option value="EUR">EUR</option>
        <option value="GBP">GBP</option>
        <option value="USDT">USDT</option>
      </select>
    </div>
  {/if}

  <!-- Manual pairs (list-table and ticker-tape manual mode) -->
  {#if showManualPairs}
    <div class="field">
      <label class="label" for="manual-pair-search">Add Pair</label>
      <div class="search-container">
        <input
          id="manual-pair-search"
          type="text"
          class="input"
          placeholder="Search for a coin..."
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
            <button class="search-result" onclick={() => addManualPair(result)}>
              {#if result.image}
                <img class="result-icon" src={result.image} alt="" />
              {/if}
              <span class="result-symbol">{result.pair}</span>
              <span class="result-name">{result.name}</span>
            </button>
          {/each}
        </div>
      {/if}
    </div>

    <div class="field">
      <span class="label">Pairs</span>
      <div class="chip-list">
        {#each cfg.pairs || [] as pair}
          <span class="chip">
            {pair}
            <button class="chip-remove" onclick={() => removeManualPair(pair)} aria-label="Remove {pair}">&times;</button>
          </span>
        {/each}
        {#if !cfg.pairs || cfg.pairs.length === 0}
          <span class="chip-empty">No pairs added</span>
        {/if}
      </div>
    </div>
  {/if}

  <!-- Portfolio holdings (portfolio only) -->
  {#if showPortfolio}
    <div class="field">
      <span class="label">Holdings</span>
      <div class="holdings-list">
        {#each cfg.holdings as holding}
          <div class="holding-row">
            <span class="holding-pair">{holding.pair}</span>
            {#if editingHoldingPair === holding.pair}
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
              <button class="holding-qty" onclick={() => startEditQty(holding.pair, holding.quantity)}>{holding.quantity}</button>
            {/if}
            <button class="holding-remove" onclick={() => removeHolding(holding.pair)} aria-label="Remove {holding.pair}">&times;</button>
          </div>
        {/each}
        {#if cfg.holdings.length === 0}
          <span class="holdings-empty">No holdings added</span>
        {/if}
      </div>
      <div class="add-holding-form">
        <div class="add-holding-pair">
          <div class="search-container">
            <input
              type="text"
              class="input holding-input"
              placeholder="Search coin..."
              value={newHoldingPair}
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
                <button class="search-result" onclick={() => selectHoldingPair(result)}>
                  {#if result.image}
                    <img class="result-icon" src={result.image} alt="" />
                  {/if}
                  <span class="result-symbol">{result.pair}</span>
                  <span class="result-name">{result.name}</span>
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

  <!-- Timeline (sparkline/history-chart only) -->
  {#if showTimeline}
    <div class="field">
      <label class="label" for="timeline-select">Timeline</label>
      <select
        id="timeline-select"
        class="input"
        value={cfg.timeline || "1D"}
        onchange={(e: Event) => { cfg.timeline = (e.target as HTMLSelectElement).value; dispatch(); }}
      >
        <option value="1D">1 Day</option>
        <option value="1W">1 Week</option>
        <option value="1M">1 Month</option>
        <option value="3M">3 Months</option>
        <option value="1Y">1 Year</option>
      </select>
    </div>
  {/if}

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

  .result-icon {
    width: 20px;
    height: 20px;
    border-radius: 50%;
  }

  .result-symbol {
    font-weight: 600;
    min-width: 5rem;
  }

  .result-name {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
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

  .holding-pair {
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

  .add-holding-pair {
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
