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

  // Three vertical columns, each representing one time component as a binary integer.
  // Use 6-bit representation for consistency (MSB at top, LSB at bottom).
  let cols = $derived([
    { label: "H", bits: toBits(h, 6) },
    { label: "M", bits: toBits(m, 6) },
    { label: "S", bits: toBits(s, 6) },
  ]);

  let activeFill = $derived(activeColor || "var(--theme-accent, #89b4fa)");
  let inactiveFill = $derived(inactiveColor || "var(--theme-surface0, #313244)");
</script>

<svg viewBox="0 0 160 230" xmlns="http://www.w3.org/2000/svg" class="true-binary-cols">
  {#each cols as col, ci}
    {@const cx = 36 + ci * 46}

    <!-- Column header -->
    <text
      x={cx}
      y="18"
      text-anchor="middle"
      dominant-baseline="central"
      class="label"
    >{col.label}</text>

    <!-- Bits rendered vertically (MSB at top, LSB at bottom) -->
    {#each col.bits as bit, ri}
      {@const cy = 48 + ri * 30}
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
  .true-binary-cols {
    width: 100%;
    height: 100%;
  }

  .label {
    fill: var(--theme-subtext, #a6adc8);
    font-family: var(--theme-font-family, "Inter", sans-serif);
    font-size: 12px;
    font-weight: 600;
  }

  .led {
    transition: fill 0.15s ease, opacity 0.15s ease;
    opacity: 0.4;
  }

  .led.active {
    opacity: 1;
  }
</style>
