<svelte:options customElement="weather-config" />

<script lang="ts">
  import type { WeatherConfig, GeoResult, ProviderEntry } from "./types.ts";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  const DEFAULTS: WeatherConfig = {
    latitude: 0,
    longitude: 0,
    locationName: "",
    units: "metric",
    language: "en",
    iconTheme: "static",
    layout: "current",
    provider: "openmeteo",
    backgroundStyle: "none",
    aqiScale: "us",
  };

  const FALLBACK_LOCATION: Partial<WeatherConfig> = {
    latitude: 40.7128,
    longitude: -74.006,
    locationName: "New York, New York",
  };

  let cfg = $state<WeatherConfig>({ ...DEFAULTS });
  let searchResults = $state<GeoResult[]>([]);
  let searching = $state<boolean>(false);
  let searchTimeout = $state<ReturnType<typeof setTimeout> | null>(null);
  let host = $state<HTMLElement | null>(null);
  let providers = $state<ProviderEntry[]>([]);
  let searchFocused = $state<boolean>(false);
  let searchQuery = $state<string>("");
  let detecting = $state<boolean>(false);
  let autoDetectDone = false;

  // Fetch available weather providers on mount.
  $effect(() => {
    (async () => {
      try {
        const resp = await (window as any).__karakuri?.api?.get("/providers/weather");
        if (Array.isArray(resp)) {
          providers = resp;
        }
      } catch {
        // Fallback: keep current provider selectable.
        providers = [{ name: "openmeteo", endpoint: "/plugins/weather/openmeteo" }];
      }
    })();
  });

  $effect(() => {
    try {
      const parsed = typeof configAttr === "string" ? JSON.parse(configAttr) : configAttr;
      cfg = { ...DEFAULTS, ...parsed };
    } catch {
      cfg = { ...DEFAULTS };
    }
  });

  // Auto-detect location once when the widget has no saved location.
  $effect(() => {
    if (!autoDetectDone && !cfg.locationName) {
      autoDetectDone = true;
      detectLocation(true);
    }
  });

  async function detectLocation(autoFallback = false): Promise<void> {
    detecting = true;
    try {
      const pos = await new Promise<GeolocationPosition>((resolve, reject) => {
        navigator.geolocation.getCurrentPosition(resolve, reject, {
          enableHighAccuracy: false,
          timeout: 10000,
        });
      });
      const { latitude, longitude } = pos.coords;
      const results = await (window as any).__karakuri?.api?.get(
        `/plugins/weather/${cfg.provider}/geo/reverse/${latitude}:${longitude}`
      );
      if (Array.isArray(results) && results.length > 0) {
        selectLocation(results[0]);
      } else {
        cfg.latitude = latitude;
        cfg.longitude = longitude;
        cfg.locationName = `${latitude.toFixed(4)}, ${longitude.toFixed(4)}`;
        dispatch();
      }
    } catch {
      // Geolocation denied or failed — fall back to New York on first load.
      if (autoFallback && !cfg.locationName) {
        cfg = { ...cfg, ...FALLBACK_LOCATION };
        dispatch();
      }
    } finally {
      detecting = false;
    }
  }

  function dispatch(): void {
    const event = new CustomEvent("configchange", {
      detail: { ...cfg },
      bubbles: true,
      composed: true,
    });
    host?.dispatchEvent(event);
  }

  function setHost(node: HTMLElement): void {
    host = node.getRootNode()?.host || node;
  }

  function onLocationFocus(): void {
    searchFocused = true;
    searchQuery = "";
  }

  function onLocationBlur(): void {
    // Delay to allow click on search results to fire first.
    setTimeout(() => {
      searchFocused = false;
      searchQuery = "";
      searchResults = [];
    }, 200);
  }

  function onLocationInput(e: Event): void {
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
          `/plugins/weather/${cfg.provider}/geo?q=${encodeURIComponent(searchQuery)}`
        );
        searchResults = Array.isArray(results) ? results : [];
      } catch {
        searchResults = [];
      } finally {
        searching = false;
      }
    }, 300);
  }

  function selectLocation(result: GeoResult): void {
    cfg.latitude = result.lat;
    cfg.longitude = result.lon;
    cfg.locationName = result.state
      ? `${result.name}, ${result.state}`
      : `${result.name}, ${result.country}`;
    searchFocused = false;
    searchQuery = "";
    searchResults = [];
    dispatch();
  }

  function onLatitudeInput(e: Event): void {
    const val = parseFloat((e.target as HTMLInputElement).value);
    if (!isNaN(val)) {
      cfg.latitude = val;
      dispatch();
    }
  }

  function onLongitudeInput(e: Event): void {
    const val = parseFloat((e.target as HTMLInputElement).value);
    if (!isNaN(val)) {
      cfg.longitude = val;
      dispatch();
    }
  }
</script>

<div class="config-form" use:setHost>
  <!-- Location search and display -->
  <div class="field">
    <div class="label-row">
      <label class="label" for="location-input">Location</label>
      <button class="detect-btn" onclick={() => detectLocation()} disabled={detecting}>
        {detecting ? "Detecting..." : "Detect"}
      </button>
    </div>
    <div class="search-container">
      <input
        id="location-input"
        type="text"
        class="input"
        placeholder={searchFocused ? "Type a city name..." : ""}
        value={searchFocused ? searchQuery : cfg.locationName}
        oninput={onLocationInput}
        onfocus={onLocationFocus}
        onblur={onLocationBlur}
      />
      {#if searching}
        <div class="search-spinner"></div>
      {/if}
    </div>
    {#if searchResults.length > 0}
      <div class="search-results">
        {#each searchResults as result}
          <button class="search-result" onclick={() => selectLocation(result)}>
            <span class="result-name">{result.name}</span>
            <span class="result-detail">
              {#if result.state}{result.state}, {/if}{result.country}
            </span>
          </button>
        {/each}
      </div>
    {/if}
  </div>

  <div class="coord-row">
    <div class="field">
      <label class="label" for="latitude-input">Latitude</label>
      <input
        id="latitude-input"
        type="number"
        step="any"
        class="input"
        value={cfg.latitude}
        oninput={onLatitudeInput}
      />
    </div>
    <div class="field">
      <label class="label" for="longitude-input">Longitude</label>
      <input
        id="longitude-input"
        type="number"
        step="any"
        class="input"
        value={cfg.longitude}
        oninput={onLongitudeInput}
      />
    </div>
  </div>

  <!-- Units -->
  <div class="field">
    <label class="label" for="units-select">Units</label>
    <select
      id="units-select"
      class="input"
      value={cfg.units}
      onchange={(e: Event) => { cfg.units = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="metric">Metric (°C, m/s)</option>
      <option value="imperial">Imperial (°F, mph)</option>
    </select>
  </div>

  <!-- Language -->
  <div class="field">
    <label class="label" for="language-input">Language</label>
    <input
      id="language-input"
      type="text"
      class="input"
      value={cfg.language}
      oninput={(e: Event) => { cfg.language = (e.target as HTMLInputElement).value; dispatch(); }}
    />
  </div>

  <!-- Icon theme -->
  <div class="field">
    <label class="label" for="icon-theme-select">Icon Theme</label>
    <select
      id="icon-theme-select"
      class="input"
      value={cfg.iconTheme}
      onchange={(e: Event) => { cfg.iconTheme = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="static">Static</option>
      <option value="animated">Animated</option>
    </select>
  </div>

  <!-- Background -->
  <div class="field">
    <label class="label" for="bg-style-select">Background</label>
    <select
      id="bg-style-select"
      class="input"
      value={cfg.backgroundStyle}
      onchange={(e: Event) => { cfg.backgroundStyle = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="none">None</option>
      <option value="gradient">Gradient</option>
      <option value="image">Photo</option>
    </select>
  </div>

  <!-- Layout -->
  <div class="field">
    <label class="label" for="layout-select">Layout</label>
    <select
      id="layout-select"
      class="input"
      value={cfg.layout}
      onchange={(e: Event) => { cfg.layout = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="current">Current Conditions</option>
      <option value="hourly">Hourly Forecast</option>
      <option value="daily">Daily Forecast</option>
      <option value="full">Full Report</option>
      <option value="precipitation">Precipitation Focus</option>
      <option value="astro">Astro View</option>
      <option value="wind">Wind & Marine</option>
      <option value="textonly">Text-Only (Minimal)</option>
      <option value="tilegrid">Tile Grid</option>
      <option value="alertbanner">Alert Banner</option>
      <option value="thermometer">Thermometer</option>
      <option value="air-quality">Air quality</option>
    </select>
  </div>

  {#if cfg.layout === "air-quality"}
    <div class="field">
      <label class="label" for="aqi-scale-select">Air quality index</label>
      <select
        id="aqi-scale-select"
        class="input"
        value={cfg.aqiScale}
        onchange={(e: Event) => { cfg.aqiScale = (e.target as HTMLSelectElement).value as WeatherConfig["aqiScale"]; dispatch(); }}
      >
        <option value="us">US EPA</option>
        <option value="european">European</option>
      </select>
      <span class="hint">Air quality comes from Open-Meteo. OpenWeatherMap has none, and pollen is only published for Europe.</span>
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

  .hint {
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.8;
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

  .label-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .detect-btn {
    padding: 0.15rem 0.5rem;
    font-size: 0.7rem;
    font-family: inherit;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-subtext, #a6adc8);
    cursor: pointer;
  }

  .detect-btn:hover:not(:disabled) {
    background: var(--theme-surface1, #45475a);
    color: var(--theme-text, #cdd6f4);
  }

  .detect-btn:disabled {
    opacity: 0.5;
    cursor: default;
  }

  .coord-row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.5rem;
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
    flex-direction: column;
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

  .result-name {
    font-weight: 500;
  }

  .result-detail {
    font-size: 0.75rem;
    color: var(--theme-subtext, #a6adc8);
  }
</style>
