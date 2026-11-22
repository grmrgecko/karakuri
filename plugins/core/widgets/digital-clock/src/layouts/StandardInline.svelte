<script lang="ts">
  import type { TimeComponents } from "../timeUtils.ts";

  let {
    time,
    showSeconds = true,
    fontStyle = "monospace",
  }: {
    time: TimeComponents;
    showSeconds?: boolean;
    fontStyle?: string;
  } = $props();

  let fontFamily: string = $derived(
    fontStyle === "sans"
      ? 'var(--theme-font-family, "Inter", sans-serif)'
      : fontStyle === "segmented"
        ? '"Courier New", "Lucida Console", monospace'
        : '"SF Mono", "Fira Code", "Cascadia Code", "Consolas", monospace'
  );

  let display: string = $derived(
    showSeconds
      ? `${time.hours}:${time.minutes}:${time.seconds}`
      : `${time.hours}:${time.minutes}`
  );

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="standard-inline" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
    <span class="time" style:font-family={fontFamily}>
      {display}
    </span>
    {#if time.period}
      <span class="period" style:font-family={fontFamily}>{time.period}</span>
    {/if}
  </div>
</div>

<style>
  .standard-inline {
    flex: 1;
    width: 100%;
    height: 100%;
    overflow: hidden;
  }

  .scale-content {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.3em;
  }

  .time {
    font-size: 10rem;
    font-weight: 300;
    letter-spacing: 0.02em;
    line-height: 1;
    white-space: nowrap;
    color: inherit;
  }

  .period {
    font-size: 3rem;
    font-weight: 400;
    color: var(--theme-subtext, #a6adc8);
    line-height: 1;
    align-self: flex-end;
    margin-bottom: 0.1em;
  }
</style>
