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

  let timeDisplay: string = $derived(
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

<div class="big-date" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
    <div class="top-row">
      <span class="time-small" style:font-family={fontFamily}>
        {timeDisplay}
        {#if time.period}
          <span class="period">{time.period}</span>
        {/if}
      </span>
    </div>
    <div class="center-content">
      <span class="day-name">{time.dayName}</span>
      <span class="full-date">{time.dateString}</span>
    </div>
  </div>
</div>

<style>
  .big-date {
    flex: 1;
    width: 100%;
    height: 100%;
    overflow: hidden;
  }

  .scale-content {
    display: flex;
    flex-direction: column;
    padding: 1rem;
    box-sizing: border-box;
  }

  .top-row {
    display: flex;
    justify-content: flex-end;
    flex-shrink: 0;
  }

  .time-small {
    font-size: 1.4rem;
    font-weight: 400;
    color: var(--theme-subtext, #a6adc8);
    line-height: 1;
    white-space: nowrap;
  }

  .period {
    font-size: 0.8em;
    margin-left: 0.2em;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.8;
  }

  .center-content {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    flex: 1;
    gap: 0.15em;
    min-height: 0;
  }

  .day-name {
    font-size: 5rem;
    font-weight: 700;
    line-height: 1.1;
    color: inherit;
    text-align: center;
  }

  .full-date {
    font-size: 1.6rem;
    font-weight: 400;
    color: var(--theme-subtext, #a6adc8);
    line-height: 1.2;
    text-align: center;
    white-space: nowrap;
  }
</style>
