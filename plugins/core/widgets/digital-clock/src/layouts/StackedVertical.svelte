<script lang="ts">
  import type { TimeComponents } from "../timeUtils.ts";

  let {
    time,
    showSeconds = true,
    fontStyle = "monospace",
    period = "",
  }: {
    time: TimeComponents;
    showSeconds?: boolean;
    fontStyle?: string;
    period?: string;
  } = $props();

  let fontFamily: string = $derived(
    fontStyle === "sans"
      ? 'var(--theme-font-family, "Inter", sans-serif)'
      : fontStyle === "segmented"
        ? '"Courier New", "Lucida Console", monospace'
        : '"SF Mono", "Fira Code", "Cascadia Code", "Consolas", monospace'
  );

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="stacked-vertical" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
    <div class="row">
      <span class="value" style:font-family={fontFamily}>{time.hours}</span>
      <span class="unit">hrs</span>
    </div>
    <div class="row">
      <span class="value" style:font-family={fontFamily}>{time.minutes}</span>
      <span class="unit">min</span>
    </div>
    {#if showSeconds}
      <div class="row">
        <span class="value" style:font-family={fontFamily}>{time.seconds}</span>
        <span class="unit">sec</span>
      </div>
    {/if}
    {#if period}
      <span class="period" style:font-family={fontFamily}>{period}</span>
    {/if}
  </div>
</div>

<style>
  .stacked-vertical {
    flex: 1;
    width: 100%;
    height: 100%;
    overflow: hidden;
  }

  .scale-content {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.15em;
  }

  .row {
    display: flex;
    align-items: baseline;
    gap: 0.3em;
  }

  .value {
    font-size: 6rem;
    font-weight: 200;
    line-height: 1;
    letter-spacing: 0.02em;
    color: inherit;
  }

  .unit {
    font-size: 1.2rem;
    font-weight: 500;
    color: var(--theme-subtext, #a6adc8);
    text-transform: uppercase;
    letter-spacing: 0.08em;
    line-height: 1;
  }

  .period {
    font-size: 1.5rem;
    font-weight: 400;
    color: var(--theme-accent, #89b4fa);
    line-height: 1;
    margin-top: 0.1em;
  }
</style>
