<script lang="ts">
  import type { CurrentWeather } from '../types.ts';

  let {
    current = null,
    units = 'metric',
    locationName = '',
  }: {
    current: CurrentWeather | null;
    units: string;
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

  // Background color based on weather code.
  let bgColor = $derived((() => {
    if (!current?.weather_code) return 'rgba(100, 110, 130, 0.3)';
    const code = parseInt(current.weather_code, 10);
    if (code >= 200 && code < 300) return 'rgba(120, 80, 180, 0.35)';
    if (code >= 300 && code < 400) return 'rgba(70, 120, 190, 0.3)';
    if (code >= 500 && code < 600) return 'rgba(50, 100, 180, 0.35)';
    if (code >= 600 && code < 700) return 'rgba(140, 160, 190, 0.3)';
    if (code >= 700 && code < 800) return 'rgba(120, 120, 130, 0.3)';
    if (code === 800) return 'rgba(200, 150, 50, 0.3)';
    if (code > 800) return 'rgba(100, 110, 130, 0.3)';
    return 'rgba(100, 110, 130, 0.3)';
  })());
</script>

<div class="textonly-view" style="background: {bgColor}" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
  {#if current}
    <div class="location">{locationName}</div>

    <div class="main-text">
      <span class="condition">{current.description},</span>
      <span class="temp">{Math.round(current.temp)}{tempUnit}</span>
    </div>

    <div class="secondary">
      <span class="detail">Feels like {Math.round(current.feels_like)}{tempUnit}</span>
      <span class="sep">&middot;</span>
      <span class="detail">Humidity {current.humidity}%</span>
      <span class="sep">&middot;</span>
      <span class="detail">Wind {Math.round(current.wind_speed * 10) / 10} {speedUnit}</span>
    </div>
  {/if}
  </div>
</div>

<style>
  .textonly-view {
    width: 100%;
    height: 100%;
    overflow: hidden;
    flex: 1;
    border-radius: var(--theme-radius-md, 8px);
  }

  .scale-content {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.5rem;
    padding: 0.65rem;
    box-sizing: border-box;
    text-align: center;
  }

  .location {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--theme-subtext, #a6adc8);
    text-transform: uppercase;
    letter-spacing: 0.08em;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .main-text {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    justify-content: center;
    gap: 0.35rem;
  }

  .condition {
    font-size: 2rem;
    font-weight: 700;
    color: var(--theme-text, #cdd6f4);
    text-transform: capitalize;
  }

  .temp {
    font-size: 2rem;
    font-weight: 300;
    color: var(--theme-text, #cdd6f4);
  }

  .secondary {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: center;
    gap: 0.35rem;
  }

  .detail {
    font-size: 0.9rem;
    color: var(--theme-subtext, #a6adc8);
    white-space: nowrap;
  }

  .sep {
    font-size: 0.9rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.5;
  }
</style>
