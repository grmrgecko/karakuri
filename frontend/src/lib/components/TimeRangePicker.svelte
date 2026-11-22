<script lang="ts">
  import type { VariableOption } from '../stores/dashboards';
  import { rangeText, shiftRange, splitRange, validRange, zoomOut } from '../timeRange';

  // TimeRangePicker is the viewer's control for a time range variable:
  // presets, a custom range for both ends, a step back and
  // forward by the current span, and a zoom out.
  let {
    value,
    presets,
    label = '',
    onpick,
  }: {
    value: string;
    presets: VariableOption[];
    label?: string;
    onpick: (value: string) => void;
  } = $props();

  let open = $state(false);
  let from = $state('');
  let to = $state('');
  let invalid = $derived(!validRange(`${from},${to}`));

  function toggle() {
    open = !open;
    if (open) {
      const parts = splitRange(value) ?? ['now-6h', 'now'];
      from = /^[0-9]+$/.test(parts[0]) ? new Date(Number(parts[0])).toISOString() : parts[0];
      to = /^[0-9]+$/.test(parts[1]) ? new Date(Number(parts[1])).toISOString() : parts[1];
    }
  }

  function pick(v: string | null) {
    if (!v) return;
    onpick(v);
    open = false;
  }
</script>

<span class="time-range-picker">
  <button type="button" class="btn btn-secondary btn-sm" title="Earlier" aria-label="Earlier" onclick={() => pick(shiftRange(value, -1))}>
    <i class="las la-angle-left"></i>
  </button>
  <button type="button" class="btn btn-secondary btn-sm range-button" aria-label={label || 'Time range'} aria-expanded={open} onclick={toggle}>
    <i class="las la-clock mr-1"></i>{rangeText(value, presets)}
  </button>
  <button type="button" class="btn btn-secondary btn-sm" title="Later" aria-label="Later" onclick={() => pick(shiftRange(value, 1))}>
    <i class="las la-angle-right"></i>
  </button>
  <button type="button" class="btn btn-secondary btn-sm" title="Zoom out" aria-label="Zoom out" onclick={() => pick(zoomOut(value))}>
    <i class="las la-search-minus"></i>
  </button>
  {#if open}
    <div class="range-popover" role="dialog" aria-label="Choose a time range">
      <form class="custom" onsubmit={(e) => { e.preventDefault(); if (!invalid) pick(`${from.trim()},${to.trim()}`); }}>
        <label>From <input class="input font-mono" bind:value={from} placeholder="now-6h" /></label>
        <label>To <input class="input font-mono" bind:value={to} placeholder="now" /></label>
        <button type="submit" class="btn btn-primary btn-sm" disabled={invalid}>Apply</button>
        {#if invalid}<span class="error">Use now-6h, now/d, a date and time, or Unix milliseconds, with From before To.</span>{/if}
      </form>
      <ul class="presets">
        {#each presets as p (p.value)}
          <li><button type="button" class:active={p.value === value} onclick={() => pick(p.value)}>{p.label || p.value}</button></li>
        {/each}
      </ul>
    </div>
  {/if}
</span>

<style>
  .time-range-picker {
    position: relative;
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
  }
  .range-button {
    white-space: nowrap;
  }
  .range-popover {
    position: absolute;
    top: calc(100% + 0.25rem);
    left: 0;
    z-index: 50;
    display: flex;
    gap: 1rem;
    padding: 0.75rem;
    min-width: 30rem;
    background: var(--theme-mantle);
    border: 1px solid var(--theme-border);
    border-radius: var(--theme-radius-md, 0.5rem);
    box-shadow: 0 8px 24px rgb(0 0 0 / 0.3);
  }
  .custom {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    flex: 1;
  }
  .custom label {
    display: flex;
    flex-direction: column;
    font-size: 0.75rem;
    gap: 0.25rem;
  }
  .error {
    font-size: 0.75rem;
    color: var(--theme-error);
  }
  .presets {
    list-style: none;
    margin: 0;
    padding: 0;
    max-height: 18rem;
    overflow-y: auto;
    min-width: 10rem;
  }
  .presets button {
    width: 100%;
    text-align: left;
    padding: 0.25rem 0.5rem;
    border-radius: var(--theme-radius-sm, 0.25rem);
    font-size: 0.85rem;
  }
  .presets button:hover,
  .presets button.active {
    background: var(--theme-surface0);
  }
</style>
