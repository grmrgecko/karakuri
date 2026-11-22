<script lang="ts">
  import type { AirQuality, Pollen } from '../types.ts';

  let {
    airQuality = null,
    scale = 'us',
    locationName = '',
  }: {
    airQuality: AirQuality | null;
    scale: string;
    locationName: string;
  } = $props();

  interface Band {
    max: number;
    label: string;
    color: string;
    ink: string;
  }

  // US_BANDS are the EPA AQI categories with their official colors.
  const US_BANDS: Band[] = [
    { max: 50, label: 'Good', color: '#00e400', ink: '#000' },
    { max: 100, label: 'Moderate', color: '#ffff00', ink: '#000' },
    { max: 150, label: 'Unhealthy for sensitive groups', color: '#ff7e00', ink: '#000' },
    { max: 200, label: 'Unhealthy', color: '#ff0000', ink: '#fff' },
    { max: 300, label: 'Very unhealthy', color: '#8f3f97', ink: '#fff' },
    { max: Infinity, label: 'Hazardous', color: '#7e0023', ink: '#fff' },
  ];

  // EU_BANDS are the European Environment Agency index levels as Open-Meteo
  // scales them, with the agency's colors.
  const EU_BANDS: Band[] = [
    { max: 20, label: 'Good', color: '#50f0e6', ink: '#000' },
    { max: 40, label: 'Fair', color: '#50ccaa', ink: '#000' },
    { max: 60, label: 'Moderate', color: '#f0e641', ink: '#000' },
    { max: 80, label: 'Poor', color: '#ff5050', ink: '#000' },
    { max: 100, label: 'Very poor', color: '#960032', ink: '#fff' },
    { max: Infinity, label: 'Extremely poor', color: '#7d2181', ink: '#fff' },
  ];

  const POLLEN_NAMES: [keyof Pollen, string][] = [
    ['alder', 'Alder'],
    ['birch', 'Birch'],
    ['grass', 'Grass'],
    ['mugwort', 'Mugwort'],
    ['olive', 'Olive'],
    ['ragweed', 'Ragweed'],
  ];

  function bandFor(bands: Band[], value: number): Band {
    return bands.find((b) => value <= b.max) ?? bands[bands.length - 1];
  }

  // The configured index leads; the other stands in when the source has no
  // reading for it, so the layout still shows a number.
  let index = $derived.by(() => {
    if (!airQuality) return null;
    const us = { name: 'US AQI', key: 'us_aqi' as const, bands: US_BANDS, value: airQuality.us_aqi };
    const eu = { name: 'European AQI', key: 'european_aqi' as const, bands: EU_BANDS, value: airQuality.european_aqi };
    const [first, second] = scale === 'european' ? [eu, us] : [us, eu];
    if (typeof first.value === 'number') return { ...first, value: first.value };
    if (typeof second.value === 'number') return { ...second, value: second.value };
    return null;
  });

  let band = $derived(index ? bandFor(index.bands, index.value) : null);

  let pollutants = $derived.by(() => {
    if (!airQuality) return [];
    const rows: { label: string; value: number; unit: string }[] = [];
    const add = (label: string, value: number | undefined, unit = 'µg/m³') => {
      if (typeof value === 'number') rows.push({ label, value, unit });
    };
    add('PM2.5', airQuality.pm2_5);
    add('PM10', airQuality.pm10);
    add('O₃', airQuality.ozone);
    add('NO₂', airQuality.nitrogen_dioxide);
    add('SO₂', airQuality.sulphur_dioxide);
    add('CO', airQuality.carbon_monoxide);
    add('UV', airQuality.uv_index, '');
    return rows;
  });

  let pollen = $derived.by(() => {
    const p = airQuality?.pollen;
    if (!p) return [];
    return POLLEN_NAMES.filter(([key]) => typeof p[key] === 'number').map(([key, label]) => ({
      label,
      value: p[key] as number,
    }));
  });

  // hours is the next day of the leading index, for the strip under the number.
  let hours = $derived.by(() => {
    if (!airQuality?.hourly || !index) return [];
    const { key, bands } = index;
    const values = airQuality.hourly.map((h) => h[key]).filter((v): v is number => typeof v === 'number');
    const top = Math.max(1, ...values);
    return airQuality.hourly.map((h) => {
      const v = h[key];
      if (typeof v !== 'number') return { dt: h.dt, height: 0, color: 'transparent', value: null };
      return { dt: h.dt, height: Math.max(4, (v / top) * 100), color: bandFor(bands, v).color, value: v };
    });
  });

  function formatValue(value: number): string {
    return value >= 100 ? String(Math.round(value)) : String(Math.round(value * 10) / 10);
  }

  function hourLabel(dt: number): string {
    return new Date(dt * 1000).toLocaleTimeString([], { hour: 'numeric' });
  }

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;
  let boxW = $state(0);
  let boxH = $state(0);
  // A wide, short cell lays the sections side by side; scaled as one column
  // they would shrink to a sliver in the middle of it.
  let wide = $derived(boxH > 0 && boxW / boxH >= 2);

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="aq-view" bind:this={containerEl} bind:clientWidth={boxW} bind:clientHeight={boxH}>
  <div class="scale-content" class:wide bind:this={contentEl}>
    {#if index && band}
      <div class="summary">
      <div class="location">{locationName || 'Air quality'}</div>
      <div class="headline">
        <span class="aqi" style:color={band.color}>{Math.round(index.value)}</span>
        <div class="headline-text">
          <span class="category" style:background={band.color} style:color={band.ink}>{band.label}</span>
          <span class="scale-name">{index.name}</span>
        </div>
      </div>

      {#if hours.length > 0}
        <div class="strip" aria-label="{index.name} over the next 24 hours">
          {#each hours as h}
            <div class="bar" style:height="{h.height}%" style:background={h.color}
                 title={h.value === null ? hourLabel(h.dt) : `${hourLabel(h.dt)}: ${Math.round(h.value)}`}></div>
          {/each}
        </div>
      {/if}
      </div>

      {#if pollutants.length > 0}
        <div class="grid">
          {#each pollutants as p}
            <div class="cell">
              <span class="cell-value">{formatValue(p.value)}</span>
              <span class="cell-label">{p.label}{#if p.unit}<span class="unit">{' ' + p.unit}</span>{/if}</span>
            </div>
          {/each}
        </div>
      {/if}

      {#if pollen.length > 0}
        <div class="pollen">
          <span class="pollen-title">Pollen <span class="unit">grains/m³</span></span>
          <div class="pollen-row">
            {#each pollen as p}
              <div class="cell">
                <span class="cell-value">{formatValue(p.value)}</span>
                <span class="cell-label">{p.label}</span>
              </div>
            {/each}
          </div>
        </div>
      {/if}
    {:else}
      <div class="location">{locationName || 'Air quality'}</div>
      <div class="empty">No air quality data for this location</div>
    {/if}
  </div>
</div>

<style>
  .aq-view {
    width: 100%;
    height: 100%;
    overflow: hidden;
    flex: 1;
  }

  .scale-content {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    padding: 0.5rem;
    box-sizing: border-box;
  }

  .scale-content.wide {
    flex-direction: row;
    align-items: center;
    gap: 1.25rem;
  }

  .summary {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    min-width: 11rem;
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

  .headline {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.75rem;
  }

  .aqi {
    font-size: 3.5rem;
    font-weight: 600;
    line-height: 1;
    font-variant-numeric: tabular-nums;
    text-shadow: 0 1px 3px rgba(0, 0, 0, 0.5);
  }

  .headline-text {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 0.25rem;
  }

  .category {
    font-size: 0.95rem;
    font-weight: 600;
    padding: 0.15rem 0.5rem;
    border-radius: var(--theme-radius-sm, 4px);
    text-shadow: none;
    max-width: 12rem;
  }

  .scale-name {
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .strip {
    display: flex;
    align-items: flex-end;
    gap: 2px;
    height: 1.5rem;
  }

  .bar {
    flex: 1;
    border-radius: 1px;
    opacity: 0.85;
  }

  .grid {
    display: grid;
    /* Fixed counts: autoScale measures at max-content, where auto-fit
       collapses to a single column. */
    grid-template-columns: repeat(4, minmax(4.5rem, 1fr));
    gap: 0.25rem;
  }

  .pollen {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }

  .pollen-title {
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  .pollen-row {
    display: grid;
    grid-template-columns: repeat(3, minmax(4rem, 1fr));
    gap: 0.25rem;
  }

  .cell {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.1rem;
    padding: 0.3rem 0.25rem;
    background: color-mix(in srgb, var(--theme-text, #cdd6f4) 6%, transparent);
    border-radius: var(--theme-radius-sm, 4px);
  }

  .cell-value {
    font-size: 1.1rem;
    font-weight: 500;
    line-height: 1.1;
    font-variant-numeric: tabular-nums;
  }

  .cell-label {
    font-size: 0.65rem;
    color: var(--theme-subtext, #a6adc8);
    text-align: center;
  }

  .unit {
    opacity: 0.7;
    text-transform: none;
    letter-spacing: 0;
  }

  .empty {
    padding: 1rem;
    text-align: center;
    font-size: 0.85rem;
    color: var(--theme-subtext, #a6adc8);
  }
</style>
