<script lang="ts">
  import type { CurrentWeather, HourlyWeather } from '../types.ts';

  let {
    current = null,
    hourly = [],
    units = 'metric',
    locationName = '',
    loading = false,
  }: {
    current: CurrentWeather | null;
    hourly: HourlyWeather[];
    units: string;
    locationName: string;
    loading: boolean;
  } = $props();

  let speedUnit = $derived(units === 'imperial' ? 'mph' : 'm/s');

  // Wind direction for compass arrow rotation.
  let windDeg = $derived(current?.wind_deg || 0);

  interface WindTrendEntry {
    index: number;
    speed: number;
    label: string;
  }

  // Recent 6-hour wind trend from hourly data.
  let windTrend: WindTrendEntry[] = $derived((() => {
    const slice = hourly.slice(0, 6);
    return slice.map((h, i) => {
      const d = new Date(h.dt * 1000);
      return {
        index: i,
        speed: h.wind_speed,
        label: d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
      };
    });
  })());

  let maxWind = $derived(Math.max(1, ...windTrend.map(w => w.speed)));

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });

  // Cardinal direction label from degrees.
  function degToCardinal(deg: number): string {
    const dirs = ['N', 'NNE', 'NE', 'ENE', 'E', 'ESE', 'SE', 'SSE', 'S', 'SSW', 'SW', 'WSW', 'W', 'WNW', 'NW', 'NNW'];
    return dirs[Math.round(deg / 22.5) % 16];
  }
</script>

<div class="wind-view" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
  <div class="header">
    <span class="location">{locationName || 'Wind'}</span>
    {#if loading}
      <div class="refresh-dot"></div>
    {/if}
  </div>

  {#if current}
    <div class="main-content">
      <div class="compass-area">
        <svg class="compass" viewBox="0 0 100 100">
          <!-- Compass circle -->
          <circle cx="50" cy="50" r="42" class="compass-ring" />
          <!-- Cardinal direction labels -->
          <text x="50" y="12" class="cardinal">N</text>
          <text x="90" y="54" class="cardinal">E</text>
          <text x="50" y="96" class="cardinal">S</text>
          <text x="10" y="54" class="cardinal">W</text>
          <!-- Wind direction arrow -->
          <g transform="rotate({windDeg}, 50, 50)">
            <line x1="50" y1="20" x2="50" y2="55" class="arrow-line" />
            <polygon points="50,16 46,26 54,26" class="arrow-head" />
          </g>
          <!-- Center dot -->
          <circle cx="50" cy="50" r="3" class="center-dot" />
        </svg>
        <div class="wind-speed-label">
          <span class="wind-speed">{Math.round(current.wind_speed * 10) / 10}</span>
          <span class="wind-unit">{speedUnit}</span>
          <span class="wind-dir">{degToCardinal(windDeg)}</span>
        </div>
      </div>

      <div class="stats">
        <div class="stat">
          <span class="stat-label">Humidity</span>
          <span class="stat-value">{current.humidity}%</span>
        </div>
        <div class="stat">
          <span class="stat-label">Pressure</span>
          <span class="stat-value">{current.pressure} hPa</span>
        </div>
        <div class="stat">
          <span class="stat-label">Visibility</span>
          <span class="stat-value">{Math.round(current.visibility / 1000)} km</span>
        </div>
      </div>
    </div>

    <div class="trend">
      <div class="trend-label">Wind trend</div>
      <div class="trend-bars">
        {#each windTrend as w}
          <div class="trend-bar-col">
            <div class="trend-bar" style="height: {Math.max(4, (w.speed / maxWind) * 100)}%"></div>
            <span class="trend-time">{w.label}</span>
          </div>
        {/each}
      </div>
    </div>
  {/if}
  </div>
</div>

<style>
  .wind-view {
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

  .main-content {
    display: flex;
    align-items: center;
    gap: 0.65rem;
    flex: 1;
    min-height: 0;
  }

  .compass-area {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.2rem;
    flex-shrink: 0;
  }

  .compass {
    width: 100px;
    height: 100px;
  }

  .compass-ring {
    fill: color-mix(in srgb, var(--theme-text, #cdd6f4) 6%, transparent);
    stroke: var(--theme-subtext, #a6adc8);
    stroke-width: 0.8;
    opacity: 0.5;
  }

  .cardinal {
    font-size: 8px;
    fill: var(--theme-subtext, #a6adc8);
    text-anchor: middle;
    dominant-baseline: middle;
    font-weight: 600;
  }

  .arrow-line {
    stroke: var(--theme-accent, #89b4fa);
    stroke-width: 2;
    stroke-linecap: round;
  }

  .arrow-head {
    fill: var(--theme-accent, #89b4fa);
  }

  .center-dot {
    fill: var(--theme-text, #cdd6f4);
  }

  .wind-speed-label {
    display: flex;
    align-items: baseline;
    gap: 0.2rem;
  }

  .wind-speed {
    font-size: 1.75rem;
    font-weight: 300;
    color: var(--theme-text, #cdd6f4);
  }

  .wind-unit {
    font-size: 0.8rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .wind-dir {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--theme-accent, #89b4fa);
  }

  .stats {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    flex: 1;
    min-width: 0;
  }

  .stat {
    display: flex;
    justify-content: space-between;
    align-items: center;
    background: color-mix(in srgb, var(--theme-text, #cdd6f4) 6%, transparent);
    border-radius: var(--theme-radius-sm, 4px);
    padding: 0.35rem 0.5rem;
  }

  .stat-label {
    font-size: 0.8rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .stat-value {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--theme-text, #cdd6f4);
  }

  .trend {
    background: color-mix(in srgb, var(--theme-text, #cdd6f4) 6%, transparent);
    border-radius: var(--theme-radius-sm, 4px);
    padding: 0.35rem;
  }

  .trend-label {
    font-size: 0.65rem;
    color: var(--theme-subtext, #a6adc8);
    margin-bottom: 0.2rem;
  }

  .trend-bars {
    display: flex;
    align-items: flex-end;
    gap: 0.2rem;
    height: 40px;
  }

  .trend-bar-col {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    height: 100%;
    justify-content: flex-end;
  }

  .trend-bar {
    width: 100%;
    background: var(--theme-accent, #89b4fa);
    border-radius: 2px 2px 0 0;
    opacity: 0.7;
    min-height: 2px;
  }

  .trend-time {
    font-size: 0.5rem;
    color: var(--theme-subtext, #a6adc8);
    margin-top: 2px;
    white-space: nowrap;
  }
</style>
