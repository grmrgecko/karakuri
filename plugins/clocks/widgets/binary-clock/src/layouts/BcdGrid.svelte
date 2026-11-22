<script lang="ts">
  import { toBcdDigits, toBits } from "../binaryUtils.ts";

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

  // Compute the six BCD columns (H-tens, H-ones, M-tens, M-ones, S-tens, S-ones).
  let columns = $derived((() => {
    const [ht, ho] = toBcdDigits(h);
    const [mt, mo] = toBcdDigits(m);
    const [st, so] = toBcdDigits(s);
    return [
      { label: "H", bits: toBits(ht, 4) },
      { label: "H", bits: toBits(ho, 4) },
      { label: "M", bits: toBits(mt, 4) },
      { label: "M", bits: toBits(mo, 4) },
      { label: "S", bits: toBits(st, 4) },
      { label: "S", bits: toBits(so, 4) },
    ];
  })());

  const rowLabels = ["8", "4", "2", "1"];

  let activeFill = $derived(activeColor || "var(--theme-accent, #89b4fa)");
  let inactiveFill = $derived(inactiveColor || "var(--theme-surface0, #313244)");
</script>

<svg viewBox="0 0 260 180" xmlns="http://www.w3.org/2000/svg" class="bcd-grid">
  <!-- Row labels on the left -->
  {#each rowLabels as label, ri}
    <text
      x="12"
      y={56 + ri * 34}
      text-anchor="middle"
      dominant-baseline="central"
      class="label"
    >{label}</text>
  {/each}

  <!-- Column labels at top -->
  {#each columns as col, ci}
    <!-- Add separator gaps between H/M and M/S pairs -->
    {@const gapOffset = ci >= 4 ? 16 : ci >= 2 ? 8 : 0}
    {@const cx = 48 + ci * 30 + gapOffset}
    <text
      x={cx}
      y="18"
      text-anchor="middle"
      dominant-baseline="central"
      class="label"
    >{col.label}</text>
  {/each}

  <!-- LED grid -->
  {#each columns as col, ci}
    {@const gapOffset = ci >= 4 ? 16 : ci >= 2 ? 8 : 0}
    {@const cx = 48 + ci * 30 + gapOffset}
    {#each col.bits as bit, ri}
      {@const cy = 56 + ri * 34}
      {@const isActive = bit === 1}
      {#if ledShape === "square"}
        <rect
          x={cx - 10}
          y={cy - 10}
          width="20"
          height="20"
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
          r="10"
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
  .bcd-grid {
    width: 100%;
    height: 100%;
  }

  .label {
    fill: var(--theme-subtext, #a6adc8);
    font-family: var(--theme-font-family, "Inter", sans-serif);
    font-size: 11px;
    font-weight: 600;
  }

  .led {
    transition: fill 0.15s ease, transform 0.15s ease, opacity 0.15s ease;
    opacity: 0.4;
  }

  .led.active {
    opacity: 1;
  }
</style>
