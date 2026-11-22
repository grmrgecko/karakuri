<script lang="ts">
  import { toBits } from "../binaryUtils.ts";

  let {
    h = 0,
    m = 0,
    s = 0,
    ledShape = "circle",
    activeColor = "",
    inactiveColor = "",
  }: {
    h: number;
    m: number;
    s: number;
    ledShape: string;
    activeColor: string;
    inactiveColor: string;
  } = $props();

  // Each row shows a full binary integer. Use a consistent 6-bit grid width,
  // padding hours to match the column count of minutes and seconds.
  let rows = $derived([
    { label: "H", bits: toBits(h, 6) },
    { label: "M", bits: toBits(m, 6) },
    { label: "S", bits: toBits(s, 6) },
  ]);

  // Bit position labels shown across the top (MSB to LSB).
  const bitLabels = ["32", "16", "8", "4", "2", "1"];

  let activeFill = $derived(activeColor || "var(--theme-accent, #89b4fa)");
  let inactiveFill = $derived(inactiveColor || "var(--theme-surface0, #313244)");
</script>

<svg viewBox="0 0 260 150" xmlns="http://www.w3.org/2000/svg" class="true-binary-rows">
  <!-- Bit position labels across the top -->
  {#each bitLabels as label, ci}
    <text
      x={60 + ci * 32}
      y="18"
      text-anchor="middle"
      dominant-baseline="central"
      class="label bit-label"
    >{label}</text>
  {/each}

  <!-- Row labels and LEDs -->
  {#each rows as row, ri}
    {@const cy = 52 + ri * 38}
    <text
      x="18"
      y={cy}
      text-anchor="middle"
      dominant-baseline="central"
      class="label row-label"
    >{row.label}</text>

    {#each row.bits as bit, ci}
      {@const cx = 60 + ci * 32}
      {@const isActive = bit === 1}
      {#if ledShape === "square"}
        <rect
          x={cx - 11}
          y={cy - 11}
          width="22"
          height="22"
          rx="3"
          fill={isActive ? activeFill : "transparent"}
          stroke={isActive ? activeFill : inactiveFill}
          stroke-width="1.5"
          class="led"
          class:active={isActive}
        />
      {:else}
        <circle
          cx={cx}
          cy={cy}
          r="11"
          fill={isActive ? activeFill : "transparent"}
          stroke={isActive ? activeFill : inactiveFill}
          stroke-width="1.5"
          class="led"
          class:active={isActive}
        />
      {/if}
    {/each}
  {/each}
</svg>

<style>
  .true-binary-rows {
    width: 100%;
    height: 100%;
  }

  .label {
    fill: var(--theme-subtext, #a6adc8);
    font-family: var(--theme-font-family, "Inter", sans-serif);
    font-size: 11px;
    font-weight: 600;
  }

  .bit-label {
    font-size: 9px;
    opacity: 0.7;
  }

  .led {
    transition: fill 0.15s ease, opacity 0.15s ease;
    opacity: 0.4;
  }

  .led.active {
    opacity: 1;
  }
</style>
