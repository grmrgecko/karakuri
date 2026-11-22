<script lang="ts">
  import type { WeatherAlert, CurrentWeather } from '../types.ts';
  import { getIconUrl, getIconDescription } from '../iconMap.ts';

  let {
    alerts = [],
    current = null,
    units = 'metric',
    iconTheme = 'static',
    locationName = '',
    loading = false,
  }: {
    alerts: WeatherAlert[];
    current: CurrentWeather | null;
    units: string;
    iconTheme: string;
    locationName: string;
    loading: boolean;
  } = $props();

  let tempUnit = $derived(units === 'imperial' ? '°F' : '°C');
  let speedUnit = $derived(units === 'imperial' ? 'mph' : 'm/s');
  let hasAlerts = $derived(alerts.length > 0);

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  // AutoScale the current weather content.
  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });

  let overlayContainerEl = $state<HTMLElement | undefined>(undefined);
  let overlayContentEl = $state<HTMLElement | undefined>(undefined);

  // AutoScale the alert overlay content.
  $effect(() => {
    if (!overlayContainerEl || !overlayContentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(overlayContainerEl, overlayContentEl);
    return () => cleanup?.();
  });

  function formatTime(dt: number): string {
    const date = new Date(dt * 1000);
    return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', hour12: true });
  }
</script>

<div class="alert-view">
  <!-- Current conditions base layer -->
  <div class="current-section" bind:this={containerEl}>
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

        <!-- Alert status line -->
        <div class="alert-status" class:alert-active={hasAlerts}>
          {#if hasAlerts}
            <svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
              <line x1="12" y1="9" x2="12" y2="13" />
              <line x1="12" y1="17" x2="12.01" y2="17" />
            </svg>
            <span>{alerts.length} Active Alert{alerts.length > 1 ? 's' : ''}</span>
          {:else}
            <svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14" />
              <polyline points="22 4 12 14.01 9 11.01" />
            </svg>
            <span>No Active Alerts</span>
          {/if}
        </div>
      {/if}
    </div>
  </div>

  <!-- Alert overlay when active -->
  {#if hasAlerts}
    <div class="alert-overlay" bind:this={overlayContainerEl}>
      <div class="overlay-content" bind:this={overlayContentEl}>
        <div class="overlay-header">
          <svg class="overlay-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
            <line x1="12" y1="9" x2="12" y2="13" />
            <line x1="12" y1="17" x2="12.01" y2="17" />
          </svg>
          <span class="overlay-title">{alerts.length} Active Alert{alerts.length > 1 ? 's' : ''}</span>
        </div>

        {#each alerts as alert}
          <div class="overlay-alert">
            <span class="overlay-event">{alert.event}</span>
            <span class="overlay-time">{formatTime(alert.start)} &ndash; {formatTime(alert.end)}</span>
            <span class="overlay-desc">{alert.description}</span>
          </div>
        {/each}

        {#if current}
          <div class="overlay-conditions">
            <img
              class="overlay-weather-icon"
              src={getIconUrl(current.icon, iconTheme)}
              alt={getIconDescription(current.icon)}
            />
            <span class="overlay-temp">{Math.round(current.temp)}{tempUnit}</span>
            <span class="overlay-location">{locationName}</span>
          </div>
        {/if}
      </div>
    </div>
  {/if}
</div>

<style>
  .alert-view {
    width: 100%;
    height: 100%;
    position: relative;
    overflow: hidden;
    flex: 1;
  }

  /* Current conditions base layer */
  .current-section {
    width: 100%;
    height: 100%;
    overflow: hidden;
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

  /* Alert status line */
  .alert-status {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    font-size: 0.85rem;
    font-weight: 600;
    color: #a6e3a1;
    margin-top: 0.25rem;
  }

  .alert-status.alert-active {
    color: #f38ba8;
  }

  .status-icon {
    width: 16px;
    height: 16px;
    flex-shrink: 0;
  }

  /* Alert overlay */
  .alert-overlay {
    position: absolute;
    inset: 0;
    background: rgba(30, 30, 46, 0.95);
    border: 1px solid rgba(243, 139, 168, 0.3);
    border-radius: var(--theme-radius-md, 8px);
    overflow: hidden;
  }

  .overlay-content {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.75rem;
    box-sizing: border-box;
  }

  .overlay-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .overlay-icon {
    width: 28px;
    height: 28px;
    color: #f38ba8;
    flex-shrink: 0;
  }

  .overlay-title {
    font-size: 1.25rem;
    font-weight: 700;
    color: #f38ba8;
    white-space: nowrap;
  }

  .overlay-alert {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    padding: 0.5rem 0.75rem;
    border-radius: 6px;
    background: rgba(243, 139, 168, 0.1);
    border-left: 3px solid #f38ba8;
    max-width: 320px;
    width: 100%;
  }

  .overlay-event {
    font-size: 1rem;
    font-weight: 600;
    color: var(--theme-text, #cdd6f4);
  }

  .overlay-time {
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.8;
  }

  .overlay-desc {
    font-size: 0.75rem;
    color: var(--theme-subtext, #a6adc8);
    line-height: 1.4;
    display: -webkit-box;
    -webkit-line-clamp: 3;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  .overlay-conditions {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    margin-top: 0.25rem;
    opacity: 0.7;
  }

  .overlay-weather-icon {
    width: 24px;
    height: 24px;
    object-fit: contain;
  }

  .overlay-temp {
    font-size: 0.9rem;
    font-weight: 500;
    color: var(--theme-text, #cdd6f4);
  }

  .overlay-location {
    font-size: 0.8rem;
    color: var(--theme-subtext, #a6adc8);
  }
</style>
