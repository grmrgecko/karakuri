<svelte:options customElement="weather-widget" />

<script lang="ts">
  import type { ForecastData, WeatherConfig, CurrentWeather, HourlyWeather, DailyWeather, MinutelyWeather, WeatherAlert, AirQuality } from "./types.ts";
  import { getIconUrl, getIconDescription } from "./iconMap.ts";
  import { getGradient, getBackgroundImageUrl } from "./backgroundMap.ts";
  import CurrentView from "./layouts/CurrentView.svelte";
  import HourlyView from "./layouts/HourlyView.svelte";
  import DailyView from "./layouts/DailyView.svelte";
  import FullReport from "./layouts/FullReport.svelte";
  import PrecipitationView from "./layouts/PrecipitationView.svelte";
  import AstroView from "./layouts/AstroView.svelte";
  import WindMarineView from "./layouts/WindMarineView.svelte";
  import TextOnlyView from "./layouts/TextOnlyView.svelte";
  import TileGridView from "./layouts/TileGridView.svelte";
  import AlertBannerView from "./layouts/AlertBannerView.svelte";
  import ThermometerView from "./layouts/ThermometerView.svelte";
  import AirQualityView from "./layouts/AirQualityView.svelte";

  // The config attribute is a JSON string passed from the dashboard host.
  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  const DEFAULTS: WeatherConfig = {
    latitude: 40.7128,
    longitude: -74.006,
    locationName: "New York",
    units: "metric",
    language: "en",
    iconTheme: "static",
    layout: "current",
    provider: "openmeteo",
    backgroundStyle: "none",
    aqiScale: "us",
  };

  // Reactively parse the config attribute whenever it changes.
  let cfg: WeatherConfig = $derived((() => {
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
    return { ...DEFAULTS, ...parsed } as WeatherConfig;
  })());

  // Weather data state.
  let weatherData = $state<ForecastData | null>(null);
  let loading = $state<boolean>(true);
  let error = $state<string | null>(null);
  let retry = () => {};

  function formatCoord(value: number): string {
    return (Math.round(value * 10000) / 10000).toFixed(4);
  }

  let forecastPath = $derived(`/plugins/weather/${cfg.provider}/forecast/${cfg.latitude}:${cfg.longitude}?units=${cfg.units}&lang=${encodeURIComponent(cfg.language)}`);
  let forecastTopic = $derived(`weather:forecast:${cfg.provider}:${formatCoord(cfg.latitude)}:${formatCoord(cfg.longitude)}`);

  // Derived data slices from the forecast response.
  let current: CurrentWeather | null = $derived(weatherData?.current || null);
  let hourly: HourlyWeather[] = $derived(weatherData?.hourly || []);
  let daily: DailyWeather[] = $derived(weatherData?.daily || []);
  let minutely: MinutelyWeather[] = $derived(weatherData?.minutely || []);
  let alerts: WeatherAlert[] = $derived(weatherData?.alerts || []);
  let airQuality: AirQuality | null = $derived(weatherData?.air_quality || null);

  // Background style derived from config and current weather condition.
  let hasBackground: boolean = $derived(cfg.backgroundStyle !== "none" && !!current?.icon);
  let backgroundCSS: string = $derived((() => {
    if (!current?.icon) return "";
    if (cfg.backgroundStyle === "gradient") return getGradient(current.icon);
    if (cfg.backgroundStyle === "image") return `url(${getBackgroundImageUrl(current.icon)})`;
    return "";
  })());

  $effect(() => {
    const path = forecastPath;
    const topic = forecastTopic;
    let active = true;
    let request = 0;
    let timer: ReturnType<typeof setTimeout>;
    weatherData = null;
    error = null;

    async function fetchForecast(): Promise<void> {
      const currentRequest = ++request;
      clearTimeout(timer);
      loading = true;
      error = null;
      try {
        const data = await (window as any).__karakuri.api.get(path);
        if (!active || currentRequest !== request) return;
        weatherData = data;
      } catch (err: unknown) {
        if (!active || currentRequest !== request) return;
        error = err instanceof Error ? err.message : "could not fetch weather data";
      } finally {
        if (active && currentRequest === request) {
          loading = false;
          timer = setTimeout(fetchForecast, 20 * 60 * 1000);
        }
      }
    }

    retry = () => { void fetchForecast(); };
    void fetchForecast();
    // Topics carry metric data. Read the cached HTTP response in the selected units.
    const unsubscribe = (window as any).__karakuri?.subscribeTopic?.(topic, () => {
      if (active) void fetchForecast();
    });
    return () => {
      active = false;
      clearTimeout(timer);
      unsubscribe?.();
    };
  });
</script>

<!-- Loading State -->
{#if loading && !weatherData}
  <div class="loading-container">
    <svg class="spinner" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
      <circle cx="12" cy="12" r="10" stroke-opacity="0.25" />
      <path d="M12 2a10 10 0 0 1 10 10" stroke-linecap="round" />
    </svg>
    <span class="loading-text">Loading weather...</span>
  </div>

<!-- Error State -->
{:else if error && !weatherData}
  <div class="error-container">
    <svg class="error-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <circle cx="12" cy="12" r="10" />
      <line x1="12" y1="8" x2="12" y2="12" />
      <line x1="12" y1="16" x2="12.01" y2="16" />
    </svg>
    <span class="error-text">{error}</span>
    <button class="retry-button" onclick={() => retry()}>Retry</button>
  </div>

<!-- Weather Display -->
{:else if weatherData}
  <div
    class="weather-bg"
    class:has-bg={hasBackground}
    style:background={cfg.backgroundStyle === "gradient" ? backgroundCSS : undefined}
    style:background-image={cfg.backgroundStyle === "image" ? backgroundCSS : undefined}
  >
    {#if hasBackground}
      <div class="weather-overlay"></div>
    {/if}
    <div class="weather-container">
    {#if error}
      <div class="refresh-error" role="status">
        <span>{error}</span>
        <button class="retry-button" onclick={() => retry()}>Retry</button>
      </div>
    {/if}
    {#if cfg.layout === "hourly"}
      <HourlyView
        {current}
        {hourly}
        units={cfg.units}
        iconTheme={cfg.iconTheme}
        locationName={cfg.locationName}
        {loading}
      />
    {:else if cfg.layout === "daily"}
      <DailyView
        {current}
        {daily}
        units={cfg.units}
        iconTheme={cfg.iconTheme}
        locationName={cfg.locationName}
        {loading}
      />
    {:else if cfg.layout === "full"}
      <FullReport
        {current}
        {hourly}
        {daily}
        {alerts}
        units={cfg.units}
        iconTheme={cfg.iconTheme}
        locationName={cfg.locationName}
        {loading}
      />
    {:else if cfg.layout === "precipitation"}
      <PrecipitationView
        {minutely}
        {hourly}
        {current}
        units={cfg.units}
        iconTheme={cfg.iconTheme}
        locationName={cfg.locationName}
        {loading}
      />
    {:else if cfg.layout === "astro"}
      <AstroView
        {current}
        {daily}
        units={cfg.units}
        locationName={cfg.locationName}
      />
    {:else if cfg.layout === "wind"}
      <WindMarineView
        {current}
        {hourly}
        units={cfg.units}
        locationName={cfg.locationName}
        {loading}
      />
    {:else if cfg.layout === "textonly"}
      <TextOnlyView
        {current}
        units={cfg.units}
        locationName={cfg.locationName}
      />
    {:else if cfg.layout === "tilegrid"}
      <TileGridView
        {current}
        units={cfg.units}
        iconTheme={cfg.iconTheme}
        locationName={cfg.locationName}
      />
    {:else if cfg.layout === "alertbanner"}
      <AlertBannerView
        {alerts}
        {current}
        units={cfg.units}
        iconTheme={cfg.iconTheme}
        locationName={cfg.locationName}
        {loading}
      />
    {:else if cfg.layout === "air-quality"}
      <AirQualityView
        {airQuality}
        scale={cfg.aqiScale}
        locationName={cfg.locationName}
      />
    {:else if cfg.layout === "thermometer"}
      <ThermometerView
        {current}
        {daily}
        units={cfg.units}
        locationName={cfg.locationName}
        {loading}
      />
    {:else}
      <CurrentView
        {current}
        units={cfg.units}
        iconTheme={cfg.iconTheme}
        locationName={cfg.locationName}
        {loading}
      />
    {/if}
    </div>
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

  .refresh-error { padding: .5rem; font-size: .8rem; display: flex; align-items: center; gap: .5rem; }

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

  .weather-bg {
    display: flex;
    flex-direction: column;
    flex: 1;
    position: relative;
    min-height: 0;
  }

  .weather-bg.has-bg {
    background-size: cover;
    background-position: center;
    --theme-text: #ffffff;
    --theme-subtext: rgba(255, 255, 255, 0.85);
    text-shadow: 0 1px 4px rgba(0, 0, 0, 0.7);
  }

  .weather-overlay {
    position: absolute;
    inset: 0;
    background: rgba(0, 0, 0, 0.4);
    pointer-events: none;
    z-index: 0;
  }

  .weather-container {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    position: relative;
    z-index: 1;
  }
</style>
