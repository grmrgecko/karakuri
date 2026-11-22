<script lang="ts">
  import type { MinutelyWeather, HourlyWeather, CurrentWeather } from '../types.ts';
  import { getIconUrl, getIconDescription } from '../iconMap.ts';

  let {
    minutely = [],
    hourly = [],
    current = null,
    units = 'metric',
    iconTheme = 'static',
    locationName = '',
    loading = false,
  }: {
    minutely: MinutelyWeather[];
    hourly: HourlyWeather[];
    current: CurrentWeather | null;
    units: string;
    iconTheme: string;
    locationName: string;
    loading: boolean;
  } = $props();

  let tempUnit = $derived(units === 'imperial' ? '°F' : '°C');
  let precipUnit = $derived(units === 'imperial' ? 'in/h' : 'mm/h');

  // Use minutely data if available, otherwise fall back to hourly pop.
  let useMinutely = $derived(minutely.length > 0);

  interface BarData {
    index: number;
    value: number;
    label: string;
  }

  // Build bar chart data from minutely precipitation.
  let minutelyBars: BarData[] = $derived((() => {
    if (!useMinutely) return [];
    const slice = minutely.slice(0, 60);
    return slice.map((m, i) => ({
      index: i,
      value: m.precipitation,
      label: i % 15 === 0 ? `${i}m` : '',
    }));
  })());

  // Fallback: hourly pop bars for the next 12 hours.
  let hourlyBars: BarData[] = $derived((() => {
    if (useMinutely) return [];
    const slice = hourly.slice(0, 12);
    return slice.map((h, i) => {
      const d = new Date(h.dt * 1000);
      const hr = d.getHours();
      const label = i % 2 === 0 ? `${hr}:00` : '';
      return { index: i, value: h.pop * 100, label };
    });
  })());

  let bars = $derived(useMinutely ? minutelyBars : hourlyBars);
  let maxValue = $derived(Math.max(0.1, ...bars.map(b => b.value)));

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="precip-view" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
  <div class="header">
    <div class="header-left">
      {#if current}
        <img
          class="icon-sm"
          src={getIconUrl(current.icon, iconTheme)}
          alt={getIconDescription(current.icon)}
        />
        <span class="temp-sm">{Math.round(current.temp)}{tempUnit}</span>
      {/if}
    </div>
    <div class="header-right">
      <span class="location">{locationName || 'Precipitation'}</span>
      {#if loading}
        <div class="refresh-dot"></div>
      {/if}
    </div>
  </div>

  <div class="chart-area">
    {#if bars.length > 0}
      <div class="chart-label">{useMinutely ? `Minutely (${precipUnit})` : 'Chance of Rain (%)'}</div>
      <svg class="chart" viewBox="0 0 {bars.length * 10} 100" preserveAspectRatio="none">
        {#each bars as bar}
          {@const height = Math.max(1, (bar.value / maxValue) * 80)}
          <rect
            x={bar.index * 10 + 1}
            y={90 - height}
            width="8"
            height={height}
            rx="1"
            class="bar"
          />
        {/each}
      </svg>
      <div class="x-labels">
        {#each bars as bar}
          {#if bar.label}
            <span class="x-label" style="left: {(bar.index / bars.length) * 100}%">{bar.label}</span>
          {/if}
        {/each}
      </div>
    {:else}
      <div class="no-data">No precipitation data available</div>
    {/if}
  </div>
  </div>
</div>

<style>
  .precip-view {
    width: 100%;
    height: 100%;
    overflow: hidden;
    flex: 1;
  }

  .scale-content {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    padding: 0.65rem;
    box-sizing: border-box;
  }

  .header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
  }

  .header-left {
    display: flex;
    align-items: center;
    gap: 0.35rem;
  }

  .header-right {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .icon-sm {
    width: 32px;
    height: 32px;
    object-fit: contain;
  }

  .temp-sm {
    font-size: 1rem;
    font-weight: 500;
    color: var(--theme-text, #cdd6f4);
  }

  .location {
    font-size: 0.8rem;
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

  .chart-area {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
    position: relative;
    background: color-mix(in srgb, var(--theme-text, #cdd6f4) 6%, transparent);
    border-radius: var(--theme-radius-sm, 4px);
    padding: 0.35rem;
  }

  .chart-label {
    font-size: 0.75rem;
    color: var(--theme-subtext, #a6adc8);
    margin-bottom: 0.2rem;
  }

  .chart {
    flex: 1;
    width: 100%;
    min-height: 0;
  }

  .bar {
    fill: var(--theme-accent, #89b4fa);
    opacity: 0.8;
  }

  .x-labels {
    position: relative;
    height: 0.95rem;
    margin-top: 0.1rem;
  }

  .x-label {
    position: absolute;
    font-size: 0.65rem;
    color: var(--theme-subtext, #a6adc8);
    transform: translateX(-50%);
    white-space: nowrap;
  }

  .no-data {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 1rem;
    color: var(--theme-subtext, #a6adc8);
  }
</style>
