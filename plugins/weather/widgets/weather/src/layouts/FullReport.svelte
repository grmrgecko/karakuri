<script lang="ts">
  import type { CurrentWeather, HourlyWeather, DailyWeather, WeatherAlert } from '../types.ts';
  import { getIconUrl, getIconDescription } from '../iconMap.ts';
  import { daysAfterToday } from '../daily.ts';

  let {
    current = null,
    hourly = [],
    daily = [],
    alerts = [],
    units = 'metric',
    iconTheme = 'static',
    locationName = '',
    loading = false,
  }: {
    current: CurrentWeather | null;
    hourly: HourlyWeather[];
    daily: DailyWeather[];
    alerts: WeatherAlert[];
    units: string;
    iconTheme: string;
    locationName: string;
    loading: boolean;
  } = $props();

  let tempUnit = $derived(units === 'imperial' ? '°F' : '°C');
  let speedUnit = $derived(units === 'imperial' ? 'mph' : 'm/s');

  function formatHour(dt: number): string {
    const date = new Date(dt * 1000);
    return date.toLocaleTimeString('en-US', { hour: 'numeric', hour12: true });
  }

  function getDayName(dt: number): string {
    const date = new Date(dt * 1000);
    return date.toLocaleDateString('en-US', { weekday: 'short' });
  }

  let displayHours = $derived(hourly.slice(1, 13));

  // Filter out today and prior days for the daily strip.
  let futureDays = $derived(daysAfterToday(daily, new Date()));

  let currentContainerEl: HTMLElement;
  let currentContentEl: HTMLElement;

  // AutoScale the current weather section to fill remaining space.
  $effect(() => {
    if (!currentContainerEl || !currentContentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(currentContainerEl, currentContentEl);
    return () => cleanup?.();
  });

  // Shared width-scaling logic for forecast strips.
  function widthScale(container: HTMLElement, content: HTMLElement): () => void {
    let rafId = 0;

    function update() {
      const containerW = container.clientWidth;
      if (containerW <= 0) return;

      content.style.transform = 'none';
      content.style.width = 'max-content';
      content.style.height = 'max-content';

      const contentW = content.offsetWidth;
      const contentH = content.offsetHeight;
      if (contentW <= 0 || contentH <= 0) return;

      const scale = containerW / contentW;

      content.style.transformOrigin = '0 0';
      content.style.transform = `scale(${scale})`;
      container.style.height = (contentH * scale) + 'px';
    }

    function scheduleUpdate() {
      if (rafId) cancelAnimationFrame(rafId);
      rafId = requestAnimationFrame(() => { rafId = 0; update(); });
    }

    const ro = new ResizeObserver(scheduleUpdate);
    ro.observe(container);
    const mo = new MutationObserver(scheduleUpdate);
    mo.observe(content, { childList: true, subtree: true, characterData: true });
    scheduleUpdate();

    return () => {
      if (rafId) cancelAnimationFrame(rafId);
      ro.disconnect();
      mo.disconnect();
    };
  }

  let hourlyContainerEl = $state<HTMLElement | undefined>(undefined);
  let hourlyContentEl = $state<HTMLElement | undefined>(undefined);

  $effect(() => {
    if (!hourlyContainerEl || !hourlyContentEl) return;
    const cleanup = widthScale(hourlyContainerEl, hourlyContentEl);
    return () => cleanup();
  });

  let dailyContainerEl = $state<HTMLElement | undefined>(undefined);
  let dailyContentEl = $state<HTMLElement | undefined>(undefined);

  $effect(() => {
    if (!dailyContainerEl || !dailyContentEl) return;
    const cleanup = widthScale(dailyContainerEl, dailyContentEl);
    return () => cleanup();
  });
</script>

<div class="full-report">
  <div class="current-section" bind:this={currentContainerEl}>
    <div class="scale-content" bind:this={currentContentEl}>
    {#if current}
      <span class="location">{locationName || 'Weather'}</span>
      {#if loading}
        <div class="refresh-dot"></div>
      {/if}

      <div class="main-row">
        <div class="temp-block">
          <span class="temp">{Math.round(current.temp)}</span>
          <span class="unit">{tempUnit}</span>
        </div>
        <img
          class="icon-large"
          src={getIconUrl(current.icon, iconTheme)}
          alt={getIconDescription(current.icon)}
        />
        <span class="condition">{current.description}</span>
      </div>

      <div class="details">
        <span class="detail">&#x2B4E; {Math.round(current.wind_speed * 10) / 10} {speedUnit}</span>
        <span class="detail">&#x25C9; {current.humidity}%</span>
        <span class="detail">&#x29B5; {current.pressure} hPa</span>
        <span class="detail">Feels {Math.round(current.feels_like)}{tempUnit}</span>
        <span class="detail">UV {Math.round(current.uvi * 10) / 10}</span>
      </div>

      {#if alerts.length > 0}
        <div class="alerts">
          {#each alerts as alert}
            <span class="alert-tag">{alert.event}</span>
          {/each}
        </div>
      {/if}
    {:else}
      <span class="location">{locationName || 'Full Report'}</span>
    {/if}
    </div>
  </div>

  {#if displayHours.length > 0}
    <div class="forecast-container" bind:this={hourlyContainerEl}>
      <div class="forecast-strip" bind:this={hourlyContentEl}>
        {#each displayHours as hour}
          <div class="hour-card">
            <span class="card-label">{formatHour(hour.dt)}</span>
            <img
              class="card-icon"
              src={getIconUrl(hour.icon, iconTheme)}
              alt={getIconDescription(hour.icon)}
            />
            <span class="card-temp">{Math.round(hour.temp)}{tempUnit}</span>
            {#if hour.pop > 0}
              <span class="card-pop">{Math.round(hour.pop * 100)}%</span>
            {/if}
          </div>
        {/each}
      </div>
    </div>
  {/if}

  {#if futureDays.length > 0}
    <div class="forecast-container" bind:this={dailyContainerEl}>
      <div class="forecast-strip" bind:this={dailyContentEl}>
        {#each futureDays as day}
          <div class="day-card">
            <span class="card-label">{getDayName(day.dt)}</span>
            <img
              class="card-icon"
              src={getIconUrl(day.icon, iconTheme)}
              alt={getIconDescription(day.icon)}
            />
            <span class="card-temp">{Math.round(day.temp_max)}/{Math.round(day.temp_min)}{tempUnit}</span>
            {#if day.pop > 0}
              <span class="card-pop">{Math.round(day.pop * 100)}%</span>
            {/if}
          </div>
        {/each}
      </div>
    </div>
  {/if}
</div>

<style>
  .full-report {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
  }

  .current-section {
    flex: 1;
    min-height: 0;
    overflow: hidden;
  }

  .scale-content {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.5rem;
  }

  .location {
    font-size: 1rem;
    font-weight: 600;
    color: var(--theme-subtext, #a6adc8);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .refresh-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--theme-accent, #89b4fa);
    animation: pulse 1s ease-in-out infinite;
    flex-shrink: 0;
  }

  @keyframes pulse {
    0%, 100% { opacity: 0.4; }
    50% { opacity: 1; }
  }

  .main-row {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }

  .temp-block {
    display: flex;
    align-items: flex-start;
    line-height: 1;
  }

  .temp {
    font-size: 4.5rem;
    font-weight: 300;
    letter-spacing: -0.02em;
    color: var(--theme-text, #cdd6f4);
  }

  .unit {
    font-size: 1.25rem;
    font-weight: 400;
    color: var(--theme-subtext, #a6adc8);
    margin-top: 0.4rem;
    margin-left: 0.15rem;
  }

  .icon-large {
    width: 72px;
    height: 72px;
    flex-shrink: 0;
    object-fit: contain;
  }

  .condition {
    font-size: 1.25rem;
    color: var(--theme-subtext, #a6adc8);
    text-transform: capitalize;
  }

  .details {
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: 0.75rem;
    font-size: 1rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .detail {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    white-space: nowrap;
  }

  .alerts {
    display: flex;
    flex-wrap: wrap;
    gap: 0.35rem;
    justify-content: center;
  }

  .alert-tag {
    font-size: 0.7rem;
    font-weight: 600;
    color: var(--theme-warning, #fab387);
    padding: 0.15rem 0.5rem;
    border-radius: 4px;
    border: 1px solid var(--theme-warning, #fab387);
    opacity: 0.9;
    white-space: nowrap;
  }

  .forecast-container {
    flex-shrink: 0;
    overflow: hidden;
  }

  .forecast-strip {
    display: flex;
    align-items: stretch;
    gap: 0.35rem;
  }

  .hour-card,
  .day-card {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.35rem;
    padding: 0.5rem 0.35rem;
    border-radius: 6px;
    background: color-mix(in srgb, var(--theme-text, #cdd6f4) 6%, transparent);
    flex: 1;
    flex-shrink: 0;
  }

  .day-card {
    padding: 0.5rem 0.65rem;
  }

  .card-label {
    font-size: 0.65rem;
    font-weight: 500;
    color: var(--theme-subtext, #a6adc8);
    text-transform: uppercase;
    white-space: nowrap;
  }

  .card-icon {
    width: 32px;
    height: 32px;
    object-fit: contain;
  }

  .card-temp {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--theme-text, #cdd6f4);
    white-space: nowrap;
  }

  .card-pop {
    font-size: 0.6rem;
    color: var(--theme-accent, #89b4fa);
    opacity: 0.8;
  }
</style>
