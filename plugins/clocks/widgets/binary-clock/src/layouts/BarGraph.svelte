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

  // Six BCD columns rendered as stacking blocks from bottom to top.
  // Active bits cause blocks to stack, giving a bar/equalizer effect.
  let columns = $derived((() => {
    const [ht, ho] = toBcdDigits(h);
    const [mt, mo] = toBcdDigits(m);
    const [st, so] = toBcdDigits(s);
    return [
      { label: "H", bits: toBits(ht, 4) },
      { label: "", bits: toBits(ho, 4) },
      { label: "M", bits: toBits(mt, 4) },
      { label: "", bits: toBits(mo, 4) },
      { label: "S", bits: toBits(st, 4) },
      { label: "", bits: toBits(so, 4) },
    ];
  })());

  let activeFill = $derived(activeColor || "var(--theme-accent, #89b4fa)");
  let inactiveFill = $derived(inactiveColor || "var(--theme-surface0, #313244)");
</script>

<svg viewBox="0 0 260 180" xmlns="http://www.w3.org/2000/svg" class="bar-graph">
  {#each columns as col, ci}
    {@const gapOffset = ci >= 4 ? 16 : ci >= 2 ? 8 : 0}
    {@const cx = 48 + ci * 30 + gapOffset}

    <!-- Blocks stacked from bottom to top. Row 0 = bit 3 (8), Row 3 = bit 0 (1).
         In bar graph style, we draw from the bottom. Bit 0 (row 3) is the bottom block. -->
    {#each col.bits as bit, ri}
      {@const blockY = 28 + ri * 30}
      {@const isActive = bit === 1}
      {#if ledShape === "square" || true}
        <rect
          x={cx - 12}
          y={blockY}
          width="24"
          height="24"
          rx="3"
          fill={isActive ? activeFill : "transparent"}
          stroke={isActive ? activeFill : inactiveFill}
          stroke-width="1.2"
          class="block"
          class:active={isActive}
        />
      {/if}
    {/each}

    <!-- Column label at bottom -->
    <text
      x={cx}
      y="170"
      text-anchor="middle"
      dominant-baseline="central"
      class="label"
    >{col.label || "\u00A0"}</text>
  {/each}
</svg>

<style>
  .bar-graph {
    width: 100%;
    height: 100%;
  }

  .label {
    fill: var(--theme-subtext, #a6adc8);
    font-family: var(--theme-font-family, "Inter", sans-serif);
    font-size: 11px;
    font-weight: 600;
  }

  .block {
    transition: fill 0.15s ease, opacity 0.15s ease;
    opacity: 0.3;
  }

  .block.active {
    opacity: 1;
  }
</style>
