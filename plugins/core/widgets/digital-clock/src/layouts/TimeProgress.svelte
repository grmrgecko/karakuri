<script lang="ts">
  import type { TimeComponents } from "../timeUtils.ts";

  let {
    time,
    showSeconds = true,
    fontStyle = "monospace",
    progress = 0,
  }: {
    time: TimeComponents;
    showSeconds?: boolean;
    fontStyle?: string;
    progress?: number;
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

  let percent: number = $derived(Math.round(progress * 100));

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="time-progress" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
    <div class="time-area">
      <span class="time" style:font-family={fontFamily}>
        {display}
      </span>
      {#if time.period}
        <span class="period" style:font-family={fontFamily}>{time.period}</span>
      {/if}
    </div>
    <div class="progress-area">
      <div class="progress-track">
        <div
          class="progress-fill"
          style:width="{percent}%"
        ></div>
      </div>
      <span class="progress-label">{percent}%</span>
    </div>
  </div>
</div>

<style>
  .time-progress {
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
    gap: 1rem;
    padding: 0.75rem;
    box-sizing: border-box;
  }

  .time-area {
    display: flex;
    align-items: center;
    gap: 0.3em;
    flex-shrink: 0;
  }

  .time {
    font-size: 6rem;
    font-weight: 300;
    letter-spacing: 0.02em;
    line-height: 1;
    color: inherit;
    white-space: nowrap;
  }

  .period {
    font-size: 2rem;
    font-weight: 400;
    color: var(--theme-subtext, #a6adc8);
    line-height: 1;
    align-self: flex-end;
    margin-bottom: 0.1em;
  }

  .progress-area {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    width: 80%;
    max-width: 400px;
    flex-shrink: 0;
  }

  .progress-track {
    flex: 1;
    height: 8px;
    background: var(--theme-surface0, #313244);
    border-radius: 999px;
    overflow: hidden;
  }

  .progress-fill {
    height: 100%;
    background: var(--theme-accent, #89b4fa);
    border-radius: 999px;
    transition: width 0.5s ease;
  }

  .progress-label {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--theme-subtext, #a6adc8);
    white-space: nowrap;
    min-width: 2.5em;
    text-align: right;
  }
</style>
