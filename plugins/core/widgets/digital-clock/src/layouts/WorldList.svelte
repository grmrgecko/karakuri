<script lang="ts">
  import { getTimeComponents } from "../timeUtils.ts";
  import type { TimeComponents } from "../timeUtils.ts";

  interface TimezoneEntry {
    timezone: string;
    label: string;
  }

  interface WorldListEntry {
    label: string;
    time: string;
    period: string;
  }

  let {
    timezones = [],
    format = "24h",
    showSeconds = true,
    fontStyle = "monospace",
  }: {
    timezones?: TimezoneEntry[];
    format?: string;
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

  // Trigger reactivity by reading the props, then build time strings.
  // The parent re-renders every second which passes new prop snapshots.
  let entries: WorldListEntry[] = $derived(
    timezones.map((tz: TimezoneEntry) => {
      const t: TimeComponents = getTimeComponents(tz.timezone, format);
      const timeStr = showSeconds
        ? `${t.hours}:${t.minutes}:${t.seconds}`
        : `${t.hours}:${t.minutes}`;
      return {
        label: tz.label || tz.timezone,
        time: timeStr,
        period: t.period,
      };
    })
  );

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="world-list" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
    {#each entries as entry}
      <div class="tz-row">
        <span class="tz-label">{entry.label}</span>
        <span class="tz-dots"></span>
        <span class="tz-time" style:font-family={fontFamily}>
          {entry.time}
          {#if entry.period}
            <span class="tz-period">{entry.period}</span>
          {/if}
        </span>
      </div>
    {/each}
  </div>
</div>

<style>
  .world-list {
    flex: 1;
    width: 100%;
    height: 100%;
    overflow: hidden;
  }

  .scale-content {
    display: flex;
    flex-direction: column;
    justify-content: center;
    gap: 0.6rem;
    padding: 0.75rem;
    box-sizing: border-box;
  }

  .tz-row {
    display: flex;
    align-items: baseline;
    gap: 0.4em;
    min-width: 0;
  }

  .tz-label {
    font-size: 1.1rem;
    font-weight: 500;
    color: var(--theme-subtext, #a6adc8);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    flex-shrink: 0;
  }

  .tz-dots {
    flex: 1;
    min-width: 0.5em;
    border-bottom: 1px dotted var(--theme-border, #45475a);
    margin-bottom: 0.25em;
  }

  .tz-time {
    font-size: 1.3rem;
    font-weight: 300;
    color: inherit;
    white-space: nowrap;
    flex-shrink: 0;
    line-height: 1;
  }

  .tz-period {
    font-size: 0.7em;
    font-weight: 400;
    color: var(--theme-subtext, #a6adc8);
    margin-left: 0.2em;
  }
</style>
