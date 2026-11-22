<svelte:options customElement="fuzzy-clock-widget" />

<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { getFuzzyTime, getWordCloudHighlights, getApproximateTime } from "./fuzzyTime.ts";
  import type { FuzzyTimeResult, ApproximateTimeResult } from "./fuzzyTime.ts";
  import SingleSentence from "./layouts/SingleSentence.svelte";
  import WordCloud from "./layouts/WordCloud.svelte";
  import TypographicStack from "./layouts/TypographicStack.svelte";
  import BoldHour from "./layouts/BoldHour.svelte";
  import Approximate from "./layouts/Approximate.svelte";

  interface ClockConfig {
    timezone: string;
    fuzziness: number;
    language: string;
    typography: string;
    layout: string;
  }

  // The config attribute is a JSON string passed from the dashboard host.
  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  const DEFAULTS: ClockConfig = {
    timezone: "",
    fuzziness: 3,
    language: "en",
    typography: "serif",
    layout: "single-sentence",
  };

  // Reactively parse the config attribute whenever it changes.
  let cfg = $derived((() => {
    let parsed: Record<string, unknown> = {};
    if (typeof configAttr === "string") {
      try {
        parsed = JSON.parse(configAttr);
      } catch {
        parsed = {};
      }
    } else if (typeof configAttr === "object" && configAttr !== null) {
      parsed = configAttr;
    }
    return { ...DEFAULTS, ...parsed } as ClockConfig;
  })());

  // Clock state updated on a 15-second interval. Fuzzy time does not need
  // per-second precision since the smallest granularity is one minute.
  let fuzzyData = $state<FuzzyTimeResult>({} as FuzzyTimeResult);
  let cloudHighlights = $state<Set<string>>(new Set());
  let approxData = $state<ApproximateTimeResult>({} as ApproximateTimeResult);
  let timer: ReturnType<typeof setInterval> | null = null;

  function tick(): void {
    fuzzyData = getFuzzyTime(cfg.timezone, cfg.fuzziness);
    cloudHighlights = getWordCloudHighlights(cfg.timezone);
    approxData = getApproximateTime(cfg.timezone);
  }

  onMount(() => {
    tick();
    timer = setInterval(tick, 15000);
  });

  onDestroy(() => {
    if (timer) clearInterval(timer);
  });

  // Re-tick immediately when config changes so the display updates without
  // waiting for the next interval.
  $effect(() => {
    const _tz = cfg.timezone;
    const _fz = cfg.fuzziness;
    tick();
  });
</script>

<div class="clock-root">
  {#if cfg.layout === "word-cloud"}
    <WordCloud
      fuzzyData={cloudHighlights}
      typography={cfg.typography}
      fuzziness={cfg.fuzziness}
    />
  {:else if cfg.layout === "typographic-stack"}
    <TypographicStack
      {fuzzyData}
      typography={cfg.typography}
      fuzziness={cfg.fuzziness}
    />
  {:else if cfg.layout === "bold-hour"}
    <BoldHour
      {fuzzyData}
      typography={cfg.typography}
      fuzziness={cfg.fuzziness}
    />
  {:else if cfg.layout === "approximate"}
    <Approximate
      fuzzyData={approxData}
      typography={cfg.typography}
      fuzziness={cfg.fuzziness}
    />
  {:else}
    <SingleSentence
      {fuzzyData}
      typography={cfg.typography}
      fuzziness={cfg.fuzziness}
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
