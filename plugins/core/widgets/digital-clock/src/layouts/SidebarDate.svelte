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

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="sidebar-date" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
    <div class="time-side">
      <div class="time-row">
        <span class="time-main" style:font-family={fontFamily}>
          {time.hours}:{time.minutes}
        </span>
        {#if time.period}
          <span class="time-period" style:font-family={fontFamily}>
            {time.period}
          </span>
        {/if}
      </div>
      {#if showSeconds}
        <span class="time-seconds" style:font-family={fontFamily}>
          :{time.seconds}
        </span>
      {/if}
    </div>
    <div class="divider"></div>
    <div class="date-side">
      <span class="date-day">{time.dayName}</span>
      <span class="date-month">{time.monthName} {time.dayOfMonth}</span>
      <span class="date-year">{time.year}</span>
    </div>
  </div>
</div>

<style>
  .sidebar-date {
    flex: 1;
    width: 100%;
    height: 100%;
    overflow: hidden;
  }

  .scale-content {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 1.5rem;
    padding: 0.75rem;
    box-sizing: border-box;
  }

  .time-side {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
  }

  .time-row {
    display: flex;
    align-items: baseline;
    gap: 0.3rem;
  }

  .time-main {
    font-size: 5rem;
    font-weight: 200;
    line-height: 1;
    letter-spacing: 0.02em;
    color: inherit;
    white-space: nowrap;
  }

  .time-seconds {
    font-size: 2rem;
    font-weight: 300;
    color: var(--theme-subtext, #a6adc8);
    line-height: 1;
    margin-top: 0.1em;
  }

  .time-period {
    font-size: 1.8rem;
    font-weight: 400;
    color: var(--theme-accent, #89b4fa);
    line-height: 1;
  }

  .divider {
    width: 1px;
    align-self: stretch;
    margin: 0.75rem 0;
    background: var(--theme-border, #45475a);
    flex-shrink: 0;
  }

  .date-side {
    display: flex;
    flex-direction: column;
    justify-content: center;
    gap: 0.1em;
    min-width: 0;
  }

  .date-day {
    font-size: 1.6rem;
    font-weight: 600;
    color: inherit;
    line-height: 1.2;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .date-month {
    font-size: 1.1rem;
    font-weight: 400;
    color: var(--theme-subtext, #a6adc8);
    line-height: 1.2;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .date-year {
    font-size: 0.95rem;
    font-weight: 400;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.7;
    line-height: 1.2;
  }
</style>
