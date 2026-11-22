<script lang="ts">
  import type { CurrentWeather, DailyWeather } from '../types.ts';
  import { hasMoonData } from '../daily.ts';

  let {
    current = null,
    daily = [],
    units = 'metric',
    locationName = '',
  }: {
    current: CurrentWeather | null;
    daily: DailyWeather[];
    units: string;
    locationName: string;
  } = $props();

  let tempUnit = $derived(units === 'imperial' ? '°F' : '°C');

  // Use current sunrise/sunset if available, otherwise fall back to today's daily entry.
  let sunrise = $derived(current?.sunrise || (daily.length > 0 ? daily[0].sunrise : 0));
  let sunset = $derived(current?.sunset || (daily.length > 0 ? daily[0].sunset : 0));

  // Moon data from the first daily entry. The section is hidden when the
  // provider reports none.
  let showMoon = $derived(hasMoonData(daily[0]));
  let moonPhase = $derived(daily.length > 0 ? daily[0].moon_phase : 0);
  let moonrise = $derived(daily.length > 0 ? daily[0].moonrise : 0);
  let moonset = $derived(daily.length > 0 ? daily[0].moonset : 0);

  // Calculate sun position as a fraction of the arc (0 = sunrise, 1 = sunset).
  let sunProgress = $derived((() => {
    if (!sunrise || !sunset) return 0.5;
    const now = current?.dt || Math.floor(Date.now() / 1000);
    const total = sunset - sunrise;
    if (total <= 0) return 0.5;
    const elapsed = now - sunrise;
    return Math.max(0, Math.min(1, elapsed / total));
  })());

  let isDaytime = $derived(sunProgress > 0 && sunProgress < 1);

  // Sun position on the SVG arc. The arc spans from (-90, 50) through (0, 10) to (90, 50).
  let sunX = $derived((() => {
    const angle = Math.PI * (1 - sunProgress);
    return 50 + 40 * Math.cos(angle);
  })());
  let sunY = $derived((() => {
    const angle = Math.PI * (1 - sunProgress);
    return 55 - 40 * Math.sin(angle);
  })());

  // Moon phase name mapping.
  let moonPhaseName = $derived((() => {
    const p = moonPhase;
    if (p === 0 || p === 1) return 'New Moon';
    if (p < 0.25) return 'Waxing Crescent';
    if (p === 0.25) return 'First Quarter';
    if (p < 0.5) return 'Waxing Gibbous';
    if (p === 0.5) return 'Full Moon';
    if (p < 0.75) return 'Waning Gibbous';
    if (p === 0.75) return 'Last Quarter';
    return 'Waning Crescent';
  })());

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });

  // Format unix timestamp to local time string (HH:MM).
  function formatTime(ts: number | undefined): string {
    if (!ts) return '--:--';
    const d = new Date(ts * 1000);
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }
</script>

<div class="astro-view" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
  <div class="header">
    <span class="location">{locationName || 'Astro'}</span>
    {#if current}
      <span class="temp-badge">{Math.round(current.temp)}{tempUnit} &middot; {current.description}</span>
    {/if}
  </div>

  <div class="sun-section">
    <svg class="sun-arc" viewBox="0 0 100 65" preserveAspectRatio="xMidYMid meet">
      <!-- Horizon line -->
      <line x1="5" y1="55" x2="95" y2="55" class="horizon" />
      <!-- Arc path -->
      <path d="M 10 55 Q 50 -10 90 55" class="arc-path" />
      <!-- Sun dot -->
      {#if isDaytime}
        <circle cx={sunX} cy={sunY} r="4" class="sun-dot" />
      {/if}
    </svg>
    <div class="sun-times">
      <div class="time-block">
        <span class="time-label">Sunrise</span>
        <span class="time-value">{formatTime(sunrise)}</span>
      </div>
      <div class="time-block">
        <span class="time-label">Sunset</span>
        <span class="time-value">{formatTime(sunset)}</span>
      </div>
    </div>
  </div>

  {#if showMoon}
  <div class="moon-section">
    <div class="moon-visual">
      <svg class="moon-icon" viewBox="0 0 40 40">
        <!-- Background circle -->
        <circle cx="20" cy="20" r="16" class="moon-bg" />
        <!-- Shadow overlay for phase visualization -->
        {#if moonPhase < 0.5}
          <!-- Waxing: shadow on left side, shrinking -->
          <ellipse cx="20" cy="20" rx={16 * Math.abs(1 - moonPhase * 4)} ry="16" class="moon-shadow" />
        {:else}
          <!-- Waning: shadow on right side, growing -->
          <ellipse cx="20" cy="20" rx={16 * Math.abs(1 - (moonPhase - 0.5) * 4)} ry="16" class="moon-shadow-right" />
        {/if}
      </svg>
      <span class="moon-name">{moonPhaseName}</span>
    </div>
    <div class="moon-times">
      <span class="moon-time">Rise {formatTime(moonrise)}</span>
      <span class="moon-time">Set {formatTime(moonset)}</span>
    </div>
  </div>
  {/if}
  </div>
</div>

<style>
  .astro-view {
    width: 100%;
    height: 100%;
    overflow: hidden;
    flex: 1;
  }

  .scale-content {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
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

  .temp-badge {
    font-size: 0.8rem;
    color: var(--theme-text, #cdd6f4);
    text-transform: capitalize;
    white-space: nowrap;
  }

  .sun-section {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    min-height: 0;
    background: color-mix(in srgb, var(--theme-text, #cdd6f4) 6%, transparent);
    border-radius: var(--theme-radius-sm, 4px);
    padding: 0.35rem;
  }

  .sun-arc {
    flex: 1;
    width: 100%;
    max-height: 50%;
    min-height: 0;
  }

  .horizon {
    stroke: var(--theme-subtext, #a6adc8);
    stroke-width: 0.5;
    stroke-dasharray: 3,2;
    opacity: 0.4;
  }

  .arc-path {
    fill: none;
    stroke: var(--theme-accent, #89b4fa);
    stroke-width: 1.5;
    stroke-dasharray: 4,2;
    opacity: 0.5;
  }

  .sun-dot {
    fill: #f9e2af;
    filter: drop-shadow(0 0 3px #f9e2af);
  }

  .sun-times {
    display: flex;
    justify-content: space-between;
    width: 100%;
    gap: 1rem;
  }

  .time-block {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.1rem;
  }

  .time-label {
    font-size: 0.65rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .time-value {
    font-size: 1rem;
    font-weight: 500;
    color: var(--theme-text, #cdd6f4);
  }

  .moon-section {
    display: flex;
    align-items: center;
    justify-content: space-between;
    background: color-mix(in srgb, var(--theme-text, #cdd6f4) 6%, transparent);
    border-radius: var(--theme-radius-sm, 4px);
    padding: 0.5rem;
    gap: 0.65rem;
  }

  .moon-visual {
    display: flex;
    align-items: center;
    gap: 0.35rem;
  }

  .moon-icon {
    width: 32px;
    height: 32px;
    flex-shrink: 0;
  }

  .moon-bg {
    fill: #cdd6f4;
    opacity: 0.9;
  }

  .moon-shadow {
    fill: var(--theme-widget-bg, #1e1e2e);
    opacity: 0.85;
  }

  .moon-shadow-right {
    fill: var(--theme-widget-bg, #1e1e2e);
    opacity: 0.85;
  }

  .moon-name {
    font-size: 0.8rem;
    color: var(--theme-text, #cdd6f4);
    font-weight: 500;
    white-space: nowrap;
  }

  .moon-times {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 0.1rem;
  }

  .moon-time {
    font-size: 0.65rem;
    color: var(--theme-subtext, #a6adc8);
    white-space: nowrap;
  }
</style>
