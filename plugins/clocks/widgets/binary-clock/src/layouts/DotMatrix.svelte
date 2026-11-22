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

  // Same BCD grid as BcdGrid, but ultra-compact with no labels.
  let columns = $derived((() => {
    const [ht, ho] = toBcdDigits(h);
    const [mt, mo] = toBcdDigits(m);
    const [st, so] = toBcdDigits(s);
    return [
      toBits(ht, 4),
      toBits(ho, 4),
      toBits(mt, 4),
      toBits(mo, 4),
      toBits(st, 4),
      toBits(so, 4),
    ];
  })());

  let activeFill = $derived(activeColor || "var(--theme-accent, #89b4fa)");
  let inactiveFill = $derived(inactiveColor || "var(--theme-surface0, #313244)");
</script>

<svg viewBox="0 0 170 110" xmlns="http://www.w3.org/2000/svg" class="dot-matrix">
  {#each columns as col, ci}
    <!-- Small gap between H/M and M/S digit pairs -->
    {@const gapOffset = ci >= 4 ? 12 : ci >= 2 ? 6 : 0}
    {@const cx = 18 + ci * 22 + gapOffset}
    {#each col as bit, ri}
      {@const cy = 18 + ri * 24}
      {@const isActive = bit === 1}
      {#if ledShape === "square"}
        {#if isActive}
          <rect
            x={cx - 8}
            y={cy - 8}
            width="16"
            height="16"
            rx="2"
            fill={activeFill}
            class="dot active"
          />
        {:else}
          <rect
            x={cx - 5}
            y={cy - 5}
            width="10"
            height="10"
            rx="1.5"
            fill="transparent"
            stroke={inactiveFill}
            stroke-width="1.2"
            class="dot"
          />
        {/if}
      {:else}
        {#if isActive}
          <circle
            cx={cx}
            cy={cy}
            r="8"
            fill={activeFill}
            class="dot active"
          />
        {:else}
          <circle
            cx={cx}
            cy={cy}
            r="5"
            fill="transparent"
            stroke={inactiveFill}
            stroke-width="1.2"
            class="dot"
          />
        {/if}
      {/if}
    {/each}
  {/each}
</svg>

<style>
  .dot-matrix {
    width: 100%;
    height: 100%;
  }

  .dot {
    transition: fill 0.15s ease, r 0.15s ease, opacity 0.15s ease;
    opacity: 0.35;
  }

  .dot.active {
    opacity: 1;
  }
</style>
