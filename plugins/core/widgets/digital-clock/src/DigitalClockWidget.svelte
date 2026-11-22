<svelte:options customElement="digital-clock-widget" />

<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { getTimeComponents, getDayProgress } from "./timeUtils.ts";
  import type { TimeComponents } from "./timeUtils.ts";
  import StandardInline from "./layouts/StandardInline.svelte";
  import StackedVertical from "./layouts/StackedVertical.svelte";
  import BigDate from "./layouts/BigDate.svelte";
  import WorldList from "./layouts/WorldList.svelte";
  import RetroLcd from "./layouts/RetroLcd.svelte";
  import TimeProgress from "./layouts/TimeProgress.svelte";
  import SidebarDate from "./layouts/SidebarDate.svelte";

  interface TimezoneEntry {
    timezone: string;
    label: string;
  }

  interface ClockConfig {
    timezone: string;
    format: string;
    showSeconds: boolean;
    fontStyle: string;
    color: string;
    layout: string;
    worldTimezones: TimezoneEntry[];
  }

  // The config attribute is a JSON string passed from the dashboard host.
  let { config: configAttr = "{}" }: { config?: string | Record<string, unknown> } = $props();

  const DEFAULTS: ClockConfig = {
    timezone: "",
    format: "24h",
    showSeconds: true,
    fontStyle: "monospace",
    color: "",
    layout: "standard-inline",
    worldTimezones: [
      { timezone: "America/New_York", label: "New York" },
      { timezone: "Europe/London", label: "London" },
      { timezone: "Asia/Tokyo", label: "Tokyo" },
    ],
  };

  // Reactively parse the config attribute whenever it changes.
  let cfg: ClockConfig = $derived((() => {
    let parsed: Record<string, unknown> = {};
    if (typeof configAttr === "string") {
      try {
        parsed = JSON.parse(configAttr);
      } catch {
        parsed = {};
      }
    } else if (typeof configAttr === "object" && configAttr !== null) {
      parsed = configAttr as Record<string, unknown>;
    }
    return { ...DEFAULTS, ...parsed } as ClockConfig;
  })());

  // Clock state updated every second.
  let time = $state<TimeComponents>(getTimeComponents("", DEFAULTS.format));
  let progress = $state<number>(0);
  let timer: ReturnType<typeof setInterval> | null = null;

  function tick(): void {
    time = getTimeComponents(cfg.timezone, cfg.format);
    progress = getDayProgress(cfg.timezone);
  }

  onMount(() => {
    tick();
    timer = setInterval(tick, 1000);
  });

  onDestroy(() => {
    if (timer) clearInterval(timer);
  });

  // Re-tick immediately when config changes so the display updates without
  // waiting for the next interval.
  $effect(() => {
    const _tz = cfg.timezone;
    const _fmt = cfg.format;
    tick();
  });

  // Resolve the text color from config. "auto" falls through to the CSS variable.
  let textColor: string | null = $derived(cfg.color || null);
</script>

<div
  class="clock-root"
  style:color={textColor}
>
  {#if cfg.layout === "stacked-vertical"}
    <StackedVertical
      {time}
      showSeconds={cfg.showSeconds}
      fontStyle={cfg.fontStyle}
      period={time.period}
    />
  {:else if cfg.layout === "big-date"}
    <BigDate
      {time}
      showSeconds={cfg.showSeconds}
      fontStyle={cfg.fontStyle}
    />
  {:else if cfg.layout === "world-list"}
    <WorldList
      timezones={cfg.worldTimezones}
      format={cfg.format}
      showSeconds={cfg.showSeconds}
      fontStyle={cfg.fontStyle}
    />
  {:else if cfg.layout === "retro-lcd"}
    <RetroLcd
      {time}
      showSeconds={cfg.showSeconds}
      format={cfg.format}
    />
  {:else if cfg.layout === "time-progress"}
    <TimeProgress
      {time}
      showSeconds={cfg.showSeconds}
      fontStyle={cfg.fontStyle}
      {progress}
    />
  {:else if cfg.layout === "sidebar-date"}
    <SidebarDate
      {time}
      showSeconds={cfg.showSeconds}
      fontStyle={cfg.fontStyle}
    />
  {:else}
    <StandardInline
      {time}
      showSeconds={cfg.showSeconds}
      fontStyle={cfg.fontStyle}
    />
  {/if}
</div>

<style>
  :host {
    display: flex;
    flex-direction: column;
    height: 100%;
    width: 100%;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    color: var(--theme-text, #cdd6f4);
    background: var(--theme-widget-bg, transparent);
    border-radius: var(--theme-radius-md, 8px);
    overflow: hidden;
    box-sizing: border-box;
  }

  .clock-root {
    display: flex;
    flex: 1;
    width: 100%;
    height: 100%;
    min-height: 0;
    min-width: 0;
  }
</style>
