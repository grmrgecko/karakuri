<svelte:options customElement="rotating-message-widget" />

<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { parseConfig, pushedLines } from "./config";
  import { placementIds, watchContent } from "../../shared/dashboardLive";
  import { display, pick, pool } from "./schedule";
  import { watchWeather } from "./weather";

  let { config: configAttr = "{}" }: { config?: string | Record<string, unknown> } = $props();

  let cfg = $derived(parseConfig(configAttr));
  // The IDs are strings so an edit to any other field does not resubscribe.
  let dashboardId = $derived(placementIds(configAttr).dashboardId);
  let widgetId = $derived(placementIds(configAttr).widgetId);

  // pushed is the pool a content command set; null leaves the lists in charge.
  let pushed = $state<string[] | null>(null);
  let weather = $state("");
  let text = $state("");
  let visible = $state(true);

  let box = $state<HTMLDivElement | null>(null);
  let line = $state<HTMLDivElement | null>(null);
  let fadeTimer: ReturnType<typeof setTimeout> | null = null;

  function currentLines(): string[] {
    if (pushed) return pushed;
    return pool(cfg.lists, new Date(), cfg, weather, cfg.specialDayUnique).lines;
  }

  // show swaps the line, fading out over half the fade time and in over the
  // other half. The first line is drawn at once, so a render does not wait
  // on a transition.
  function show(next: string, animate: boolean): void {
    if (fadeTimer) clearTimeout(fadeTimer);
    fadeTimer = null;
    if (next === text) {
      visible = true;
      return;
    }
    if (!animate || cfg.fade <= 0 || text === "") {
      text = next;
      visible = true;
      return;
    }
    visible = false;
    fadeTimer = setTimeout(() => {
      fadeTimer = null;
      text = next;
      visible = true;
    }, cfg.fade / 2);
  }

  function rotate(animate: boolean): void {
    show(pick(currentLines(), text), animate);
  }

  // Lists, hours, and the special day setting change what may show, so an
  // edit picks again at once rather than at the next interval.
  let listsKey = $derived(
    JSON.stringify([
      cfg.lists,
      cfg.morningStart,
      cfg.morningEnd,
      cfg.afternoonStart,
      cfg.afternoonEnd,
      cfg.specialDayUnique,
    ]),
  );
  $effect(() => {
    void listsKey;
    untrack(() => rotate(false));
  });

  $effect(() => {
    const ms = cfg.interval * 1000;
    const timer = setInterval(() => untrack(() => rotate(true)), ms);
    return () => clearInterval(timer);
  });

  $effect(() => {
    const dash = dashboardId;
    const widget = widgetId;
    let lastKey = "null";
    return watchContent(dash, widget, (content) => {
      const next = content ? pushedLines(content.value) : null;
      const key = JSON.stringify(next);
      if (key === lastKey) return;
      lastKey = key;
      pushed = next;
      untrack(() => rotate(true));
    });
  });

  let weatherKey = $derived(JSON.stringify(cfg.weather ?? null));
  $effect(() => {
    void weatherKey;
    const src = untrack(() => cfg.weather);
    weather = "";
    return watchWeather(src, (value) => {
      weather = value;
    });
  });

  $effect(() => {
    if (cfg.size !== "fit" || !box || !line) return;
    const el = line;
    const off = (window as any).__karakuri?.fitText(box, el);
    return () => {
      off?.();
      el.style.fontSize = "";
    };
  });

  onDestroy(() => {
    if (fadeTimer) clearTimeout(fadeTimer);
  });
</script>

<div class="box align-{cfg.align} size-{cfg.size}" bind:this={box}>
  <div
    class="line"
    class:hidden={!visible}
    style:transition-duration="{cfg.fade / 2}ms"
    bind:this={line}
  >{display(text)}</div>
</div>

<style>
  :host {
    display: block;
    width: 100%;
    height: 100%;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    color: var(--theme-text, #cdd6f4);
    box-sizing: border-box;
  }

  .box {
    display: flex;
    align-items: center;
    width: 100%;
    height: 100%;
    padding: 0.25rem 0.5rem;
    box-sizing: border-box;
    overflow: hidden;
  }

  .align-left { justify-content: flex-start; text-align: left; }
  .align-center { justify-content: center; text-align: center; }
  .align-right { justify-content: flex-end; text-align: right; }

  /* Shrink-wrapped so fit measures the text rather than the box: a word wider
     than the box makes the line wider than it, and the text shrinks. */
  .line {
    display: inline-block;
    white-space: pre-line;
    line-height: 1.25;
    opacity: 1;
    transition-property: opacity;
    transition-timing-function: ease-in-out;
  }

  .line.hidden {
    opacity: 0;
  }

  .size-small .line { font-size: 1rem; }
  .size-medium .line { font-size: 1.6rem; }
  .size-large .line { font-size: 2.6rem; }
</style>
