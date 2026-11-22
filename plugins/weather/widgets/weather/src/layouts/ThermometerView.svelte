<script lang="ts">
  import type { CurrentWeather, DailyWeather } from '../types.ts';

  let {
    current = null,
    daily = [],
    units = 'metric',
    locationName = '',
    loading = false,
  }: {
    current: CurrentWeather | null;
    daily: DailyWeather[];
    units: string;
    locationName: string;
    loading: boolean;
  } = $props();

  let tempUnit = $derived(units === 'imperial' ? '°F' : '°C');

  // Today's high and low from the first daily entry.
  let high = $derived(daily.length > 0 ? daily[0].temp_max : (current?.temp || 0) + 5);
  let low = $derived(daily.length > 0 ? daily[0].temp_min : (current?.temp || 0) - 5);

  // Mercury fill percentage relative to the day's range.
  let fillPct = $derived((() => {
    if (!current) return 50;
    const range = high - low;
    if (range <= 0) return 50;
    return Math.max(5, Math.min(95, ((current.temp - low) / range) * 100));
  })());

  // Mercury height in SVG units for the thermometer tube.
  let mercuryHeight = $derived((fillPct / 100) * 70);

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="thermo-view" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
  <div class="header">
    <span class="location">{locationName || 'Temperature'}</span>
    {#if loading}
      <div class="refresh-dot"></div>
    {/if}
  </div>

  {#if current}
    <div class="main-content">
      <div class="thermo-column">
        <div class="scale-label high-label">{Math.round(high)}{tempUnit}</div>
        <svg class="thermometer" viewBox="0 0 30 120" preserveAspectRatio="xMidYMid meet">
          <!-- Thermometer outline -->
          <rect x="10" y="10" width="10" height="80" rx="5" class="thermo-tube" />
          <circle cx="15" cy="100" r="12" class="thermo-bulb" />
          <!-- Mercury fill mapped to the tube range -->
          <rect
            x="12"
            y={80 - mercuryHeight}
            width="6"
            height={mercuryHeight + 10}
            rx="3"
            class="mercury"
          />
          <circle cx="15" cy="100" r="9" class="mercury-bulb" />
        </svg>
        <div class="scale-label low-label">{Math.round(low)}{tempUnit}</div>
      </div>

      <div class="info-column">
        <div class="current-temp">{Math.round(current.temp)}<span class="current-unit">{tempUnit}</span></div>
        <div class="condition">{current.description}</div>
        <div class="feels-like">
          <span class="fl-label">Feels like</span>
          <span class="fl-value">{Math.round(current.feels_like)}{tempUnit}</span>
        </div>
        <div class="range-bar">
          <span class="range-low">L: {Math.round(low)}{tempUnit}</span>
          <div class="range-track">
            <div class="range-fill" style="left: 0%; width: {fillPct}%"></div>
          </div>
          <span class="range-high">H: {Math.round(high)}{tempUnit}</span>
        </div>
      </div>
    </div>
  {/if}
  </div>
</div>

<style>
  .thermo-view {
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
    gap: 1rem;
    flex: 1;
    min-height: 0;
  }

  .thermo-column {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.2rem;
    flex-shrink: 0;
  }

  .thermometer {
    height: 160px;
    width: 40px;
  }

  .thermo-tube {
    fill: none;
    stroke: var(--theme-subtext, #a6adc8);
    stroke-width: 1.5;
    opacity: 0.4;
  }

  .thermo-bulb {
    fill: none;
    stroke: var(--theme-subtext, #a6adc8);
    stroke-width: 1.5;
    opacity: 0.4;
  }

  .mercury {
    fill: #f38ba8;
  }

  .mercury-bulb {
    fill: #f38ba8;
  }

  .scale-label {
    font-size: 0.65rem;
    color: var(--theme-subtext, #a6adc8);
    font-weight: 500;
  }

  .info-column {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    flex: 1;
    min-width: 0;
  }

  .current-temp {
    font-size: 4rem;
    font-weight: 300;
    color: var(--theme-text, #cdd6f4);
    line-height: 1;
  }

  .current-unit {
    font-size: 1.25rem;
    color: var(--theme-subtext, #a6adc8);
    font-weight: 400;
  }

  .condition {
    font-size: 1rem;
    color: var(--theme-subtext, #a6adc8);
    text-transform: capitalize;
  }

  .feels-like {
    display: flex;
    align-items: center;
    gap: 0.35rem;
  }

  .fl-label {
    font-size: 0.8rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.7;
  }

  .fl-value {
    font-size: 0.9rem;
    font-weight: 500;
    color: var(--theme-text, #cdd6f4);
  }

  .range-bar {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    margin-top: 0.2rem;
  }

  .range-low,
  .range-high {
    font-size: 0.65rem;
    color: var(--theme-subtext, #a6adc8);
    white-space: nowrap;
  }

  .range-track {
    flex: 1;
    height: 4px;
    background: color-mix(in srgb, var(--theme-text, #cdd6f4) 10%, transparent);
    border-radius: 2px;
    position: relative;
    overflow: hidden;
  }

  .range-fill {
    position: absolute;
    top: 0;
    bottom: 0;
    background: linear-gradient(90deg, #89b4fa, #f38ba8);
    border-radius: 2px;
  }
</style>
