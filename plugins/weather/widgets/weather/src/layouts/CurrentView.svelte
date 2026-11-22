<script lang="ts">
  import type { CurrentWeather } from '../types.ts';
  import { getIconUrl, getIconDescription } from '../iconMap.ts';

  let {
    current = null,
    locationName = '',
    units = 'metric',
    iconTheme = 'static',
    loading = false,
  }: {
    current: CurrentWeather | null;
    locationName: string;
    units: string;
    iconTheme: string;
    loading: boolean;
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
</script>

<div class="current-view" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
  <div class="header">
    <span class="location">{locationName || 'Weather'}</span>
    {#if loading}
      <div class="refresh-dot"></div>
    {/if}
  </div>

  {#if current}
    <div class="main-row">
      <img
        class="icon-large"
        src={getIconUrl(current.icon, iconTheme)}
        alt={getIconDescription(current.icon)}
      />
      <div class="temp-block">
        <span class="temp">{Math.round(current.temp)}</span>
        <span class="unit">{tempUnit}</span>
      </div>
    </div>

    <div class="condition">{current.description}</div>

    <div class="details">
      <div class="detail">
        <span class="detail-label">Feels like</span>
        <span class="detail-value">{Math.round(current.feels_like)}{tempUnit}</span>
      </div>
      <div class="detail">
        <span class="detail-label">Humidity</span>
        <span class="detail-value">{current.humidity}%</span>
      </div>
      <div class="detail">
        <span class="detail-label">Wind</span>
        <span class="detail-value">{Math.round(current.wind_speed * 10) / 10} {speedUnit}</span>
      </div>
    </div>
  {/if}
  </div>
</div>

<style>
  .current-view {
    width: 100%;
    height: 100%;
    overflow: hidden;
    flex: 1;
  }

  .scale-content {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.75rem;
    box-sizing: border-box;
  }

  .header {
    display: flex;
    align-items: center;
    justify-content: space-between;
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

  .icon-large {
    width: 72px;
    height: 72px;
    flex-shrink: 0;
    object-fit: contain;
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
    margin-top: 0.5rem;
  }

  .detail {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    font-size: 1rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .detail-label {
    opacity: 0.7;
  }

  .detail-value {
    color: var(--theme-text, #cdd6f4);
    font-weight: 500;
  }
</style>
