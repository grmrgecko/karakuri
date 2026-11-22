<script lang="ts">
  import { onDestroy } from "svelte";
  import PickerInput from "./PickerInput.svelte";
  import { choices, hasFeature } from "../controlKinds";

  // A location reads as where someone is and for how long. The picker exists
  // only when the author asked for one, because a zone picked by hand
  // overrides every source until the next one reports.
  let {
    value,
    options,
    readOnly = false,
    busy = false,
    compact = false,
    onpick,
  }: {
    value: unknown;
    options: Record<string, unknown>;
    readOnly?: boolean;
    busy?: boolean;
    compact?: boolean;
    onpick?: (state: string) => void;
  } = $props();

  let fields = $derived(
    value && typeof value === "object" ? (value as Record<string, unknown>) : {},
  );
  let zone = $derived(typeof fields.state === "string" ? fields.state : "");
  let zones = $derived(choices(options, "zones"));
  let label = $derived(zones.find((z) => z.value === zone)?.label || zone);

  // now ticks each minute so "for 5 min" keeps counting on a wall panel that
  // nobody touches.
  let now = $state(Date.now());
  const timer = setInterval(() => (now = Date.now()), 60_000);
  onDestroy(() => clearInterval(timer));

  let since = $derived(typeof fields.since === "string" ? Date.parse(fields.since) : NaN);
  let duration = $derived(Number.isFinite(since) ? elapsed(now - since) : "");

  let coordinates = $derived.by(() => {
    const lat = fields.latitude;
    const lon = fields.longitude;
    if (!hasFeature(options, "show_coordinates")) return "";
    if (typeof lat !== "number" || typeof lon !== "number") return "";
    const acc = typeof fields.accuracy === "number" ? ` ± ${Math.round(fields.accuracy)} m` : "";
    return `${lat.toFixed(4)}, ${lon.toFixed(4)}${acc}`;
  });

  let pickable = $derived(!readOnly && hasFeature(options, "manual") && zones.length > 0);

  /** elapsed renders a span the way a glance reads it: 3 min, 2 h, 4 d. */
  function elapsed(ms: number): string {
    const minutes = Math.max(0, Math.floor(ms / 60_000));
    if (minutes < 1) return "just now";
    if (minutes < 60) return `for ${minutes} min`;
    const hours = Math.floor(minutes / 60);
    if (hours < 48) return `for ${hours} h`;
    return `for ${Math.floor(hours / 24)} d`;
  }
</script>

<span class="location" class:stack={!compact}>
  <span class="line">
    {#if pickable}
      <PickerInput
        value={zone}
        options={zones}
        display="dropdown"
        disabled={busy}
        onpick={(next) => onpick?.(next)}
      />
    {:else if zone}
      <span class="reading">{label}</span>
    {:else}
      <span class="reading dim">Not reported</span>
    {/if}
    {#if duration}<span class="dim">{duration}</span>{/if}
  </span>
  {#if coordinates}
    <span class="dim coords">{coordinates}</span>
  {/if}
</span>

<style>
  .location {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    min-width: 0;
  }

  .location.stack {
    flex-direction: column;
    align-items: flex-end;
    gap: 0.15rem;
  }

  .line {
    display: flex;
    align-items: center;
    gap: 0.4rem;
  }

  .reading {
    font-size: 0.9rem;
    color: var(--theme-subtext0, var(--theme-text-muted, #a6adc8));
    white-space: nowrap;
  }

  .dim {
    font-size: 0.75rem;
    opacity: 0.7;
    white-space: nowrap;
  }

  .coords {
    font-variant-numeric: tabular-nums;
  }
</style>
