<script lang="ts">
  import type { CurrentWeather } from '../types.ts';
  import { getIconUrl, getIconDescription } from '../iconMap.ts';

  let {
    current = null,
    units = 'metric',
    iconTheme = 'static',
    locationName = '',
  }: {
    current: CurrentWeather | null;
    units: string;
    iconTheme: string;
    locationName: string;
  } = $props();

  let tempUnit = $derived(units === 'imperial' ? '°F' : '°C');
  let speedUnit = $derived(units === 'imperial' ? 'mph' : 'm/s');

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });

  // Cardinal direction from degrees.
  function degToCardinal(deg: number): string {
    const dirs = ['N', 'NNE', 'NE', 'ENE', 'E', 'ESE', 'SE', 'SSE', 'S', 'SSW', 'SW', 'WSW', 'W', 'WNW', 'NW', 'NNW'];
    return dirs[Math.round(deg / 22.5) % 16];
  }
</script>

<div class="tilegrid-view" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
  <div class="location">{locationName || 'Weather'}</div>

  {#if current}
    <div class="grid">
      <div class="tile">
        <div class="tile-top">
          <img
            class="tile-icon"
            src={getIconUrl(current.icon, iconTheme)}
            alt={getIconDescription(current.icon)}
          />
        </div>
        <span class="tile-value">{Math.round(current.temp)}{tempUnit}</span>
        <span class="tile-label">Temperature</span>
      </div>

      <div class="tile">
        <div class="tile-top">
          <svg class="tile-arrow" viewBox="0 0 24 24" style="transform: rotate({current.wind_deg}deg)">
            <line x1="12" y1="4" x2="12" y2="18" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
            <polyline points="12,4 8,10 12,4 16,10" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </div>
        <span class="tile-value">{Math.round(current.wind_speed * 10) / 10} {speedUnit}</span>
        <span class="tile-label">Wind {degToCardinal(current.wind_deg)}</span>
      </div>

      <div class="tile">
        <span class="tile-big">{current.humidity}%</span>
        <span class="tile-label">Humidity</span>
        <span class="tile-sub">UV {current.uvi}</span>
      </div>

      <div class="tile">
        <span class="tile-big">{current.pressure}</span>
        <span class="tile-label">Pressure hPa</span>
        <span class="tile-sub">{Math.round(current.visibility / 1000)} km vis</span>
      </div>
    </div>
  {/if}
  </div>
</div>

<style>
  .tilegrid-view {
    width: 100%;
    height: 100%;
    overflow: hidden;
    flex: 1;
  }

  .scale-content {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    padding: 0.35rem;
    box-sizing: border-box;
  }

  .location {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--theme-subtext, #a6adc8);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    text-align: center;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    grid-template-rows: 1fr 1fr;
    gap: 0.25rem;
    flex: 1;
    min-height: 0;
  }

  .tile {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.2rem;
    background: color-mix(in srgb, var(--theme-text, #cdd6f4) 6%, transparent);
    border-radius: var(--theme-radius-sm, 4px);
    padding: 0.25rem;
    min-height: 0;
    overflow: hidden;
  }

  .tile-top {
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .tile-icon {
    width: 40px;
    height: 40px;
    object-fit: contain;
  }

  .tile-arrow {
    width: 28px;
    height: 28px;
    color: var(--theme-accent, #89b4fa);
  }

  .tile-value {
    font-size: 1.25rem;
    font-weight: 500;
    color: var(--theme-text, #cdd6f4);
    line-height: 1;
  }

  .tile-big {
    font-size: 1.75rem;
    font-weight: 300;
    color: var(--theme-text, #cdd6f4);
    line-height: 1;
  }

  .tile-label {
    font-size: 0.65rem;
    color: var(--theme-subtext, #a6adc8);
    text-align: center;
  }

  .tile-sub {
    font-size: 0.6rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.7;
  }
</style>
