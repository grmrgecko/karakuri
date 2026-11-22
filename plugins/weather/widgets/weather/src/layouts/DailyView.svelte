<script lang="ts">
  import type { CurrentWeather, DailyWeather } from '../types.ts';
  import { getIconUrl, getIconDescription } from '../iconMap.ts';
  import { daysAfterToday } from '../daily.ts';

  let {
    current = null,
    daily = [],
    units = 'metric',
    iconTheme = 'static',
    locationName = '',
    loading = false,
  }: {
    current: CurrentWeather | null;
    daily: DailyWeather[];
    units: string;
    iconTheme: string;
    locationName: string;
    loading: boolean;
  } = $props();

  let tempUnit = $derived(units === 'imperial' ? '°F' : '°C');
  let speedUnit = $derived(units === 'imperial' ? 'mph' : 'm/s');

  function getDayName(dt: number): string {
    const date = new Date(dt * 1000);
    return date.toLocaleDateString('en-US', { weekday: 'short' });
  }

  // Filter out today and prior days, keep up to 7 future days.
  let futureDays = $derived(daysAfterToday(daily, new Date()));

  let currentContainerEl: HTMLElement;
  let currentContentEl: HTMLElement;

  // AutoScale the current weather section to fill remaining space.
  $effect(() => {
    if (!currentContainerEl || !currentContentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(currentContainerEl, currentContentEl);
    return () => cleanup?.();
  });

  let stripContainerEl: HTMLElement;
  let stripContentEl: HTMLElement;

  // Scale the forecast strip uniformly to match container width.
  $effect(() => {
    if (!stripContainerEl || !stripContentEl) return;

    let rafId = 0;

    function update() {
      const containerW = stripContainerEl.clientWidth;
      if (containerW <= 0) return;

      stripContentEl.style.transform = 'none';
      stripContentEl.style.width = 'max-content';
      stripContentEl.style.height = 'max-content';

      const contentW = stripContentEl.offsetWidth;
      const contentH = stripContentEl.offsetHeight;
      if (contentW <= 0 || contentH <= 0) return;

      const scale = containerW / contentW;

      stripContentEl.style.transformOrigin = '0 0';
      stripContentEl.style.transform = `scale(${scale})`;
      stripContainerEl.style.height = (contentH * scale) + 'px';
    }

    function scheduleUpdate() {
      if (rafId) cancelAnimationFrame(rafId);
      rafId = requestAnimationFrame(() => { rafId = 0; update(); });
    }

    const ro = new ResizeObserver(scheduleUpdate);
    ro.observe(stripContainerEl);
    const mo = new MutationObserver(scheduleUpdate);
    mo.observe(stripContentEl, { childList: true, subtree: true, characterData: true });
    scheduleUpdate();

    return () => {
      if (rafId) cancelAnimationFrame(rafId);
      ro.disconnect();
      mo.disconnect();
    };
  });
</script>

<div class="daily-view">
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
      </div>
    {:else}
      <span class="location">{locationName || 'Daily Forecast'}</span>
    {/if}
    </div>
  </div>

  <div class="forecast-container" bind:this={stripContainerEl}>
    <div class="forecast-strip" bind:this={stripContentEl}>
      {#each futureDays as day}
        <div class="day-card">
          <span class="day-name">{getDayName(day.dt)}</span>
          <img
            class="day-icon"
            src={getIconUrl(day.icon, iconTheme)}
            alt={getIconDescription(day.icon)}
          />
          <span class="day-temps">{Math.round(day.temp_max)}/{Math.round(day.temp_min)}{tempUnit}</span>
          {#if day.pop > 0}
            <span class="day-pop">{Math.round(day.pop * 100)}%</span>
          {/if}
        </div>
      {/each}
    </div>
  </div>
</div>

<style>
  .daily-view {
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
    gap: 1rem;
    font-size: 1rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .detail {
    display: flex;
    align-items: center;
    gap: 0.25rem;
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

  .day-card {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.35rem;
    padding: 0.5rem 0.65rem;
    border-radius: 6px;
    background: color-mix(in srgb, var(--theme-text, #cdd6f4) 6%, transparent);
    flex: 1;
    flex-shrink: 0;
  }

  .day-name {
    font-size: 0.65rem;
    font-weight: 600;
    color: var(--theme-subtext, #a6adc8);
    text-transform: uppercase;
    letter-spacing: 0.03em;
    white-space: nowrap;
  }

  .day-icon {
    width: 32px;
    height: 32px;
    object-fit: contain;
  }

  .day-temps {
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--theme-text, #cdd6f4);
    white-space: nowrap;
  }

  .day-pop {
    font-size: 0.6rem;
    color: var(--theme-accent, #89b4fa);
    opacity: 0.8;
  }
</style>
