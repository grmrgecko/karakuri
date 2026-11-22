<script lang="ts">
  // A slider commits on release rather than on every pixel: an intermediate
  // value would be a command, and dragging a light from 10 to 80 would send
  // seventy of them. The readout follows the handle while dragging so the
  // number under a thumb is the one being chosen.
  let {
    value = 0,
    min = 0,
    max = 100,
    step = 1,
    unit = "",
    disabled = false,
    label = "",
    oncommit,
  }: {
    value?: number;
    min?: number;
    max?: number;
    step?: number;
    unit?: string;
    disabled?: boolean;
    label?: string;
    oncommit?: (next: number) => void | Promise<void>;
  } = $props();

  let dragging = $state<number | null>(null);
  let shown = $derived(dragging ?? value);
</script>

<span class="slider-row">
  {#if label}
    <span class="slider-label">{label}</span>
  {/if}
  <input
    type="range"
    {min}
    {max}
    step={step || 1}
    {disabled}
    value={Number.isFinite(value) ? value : min}
    oninput={(e) => (dragging = Number(e.currentTarget.value))}
    onchange={async (e) => {
      const next = Number(e.currentTarget.value);
      dragging = null;
      await oncommit?.(next);
    }}
  />
  <span class="readout">{Number.isFinite(shown) ? shown : "—"}{unit}</span>
</span>

<style>
  .slider-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    min-width: 0;
  }

  .slider-label {
    font-size: 0.75rem;
    opacity: 0.7;
    white-space: nowrap;
  }

  input[type="range"] {
    width: 6.5rem;
    min-width: 3rem;
    flex: 1 1 auto;
    accent-color: var(--theme-accent, #89b4fa);
  }

  .readout {
    font-size: 0.8rem;
    color: var(--theme-subtext0, var(--theme-text-muted, #a6adc8));
    white-space: nowrap;
    min-width: 2.5rem;
    text-align: right;
  }
</style>
