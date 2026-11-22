<svelte:options customElement="binary-clock-widget" />

<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { getTimeComponents } from "./binaryUtils.ts";
  import BcdGrid from "./layouts/BcdGrid.svelte";
  import TrueBinaryRows from "./layouts/TrueBinaryRows.svelte";
  import TrueBinaryCols from "./layouts/TrueBinaryCols.svelte";
  import DotMatrix from "./layouts/DotMatrix.svelte";
  import BarGraph from "./layouts/BarGraph.svelte";

  let { config: configAttr = "{}" }: { config: string | Record<string, any> } = $props();

  const DEFAULTS = {
    timezone: "",
    format: "24h",
    ledShape: "circle",
    activeColor: "",
    inactiveColor: "",
    layout: "bcd-grid",
  };

  let cfg = $derived((() => {
    let parsed: Record<string, any> = {};
    if (typeof configAttr === "string") {
      try { parsed = JSON.parse(configAttr); } catch { parsed = {}; }
    } else if (typeof configAttr === "object" && configAttr !== null) {
      parsed = configAttr;
    }
    return { ...DEFAULTS, ...parsed };
  })());

  let h = $state(0);
  let m = $state(0);
  let s = $state(0);
  let timer: ReturnType<typeof setInterval> | null = null;

  function tick(): void {
    const time = getTimeComponents(cfg.timezone, cfg.format);
    h = time.h;
    m = time.m;
    s = time.s;
  }

  onMount(() => {
    tick();
    timer = setInterval(tick, 1000);
  });

  onDestroy(() => {
    if (timer) clearInterval(timer);
  });

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="scale-container" bind:this={containerEl}>
  <div class="binary-clock-container" bind:this={contentEl}>
    {#if cfg.layout === "true-binary-rows"}
    <TrueBinaryRows {h} {m} {s} ledShape={cfg.ledShape} activeColor={cfg.activeColor} inactiveColor={cfg.inactiveColor} />
  {:else if cfg.layout === "true-binary-cols"}
    <TrueBinaryCols {h} {m} {s} ledShape={cfg.ledShape} activeColor={cfg.activeColor} inactiveColor={cfg.inactiveColor} />
  {:else if cfg.layout === "dot-matrix"}
    <DotMatrix {h} {m} {s} ledShape={cfg.ledShape} activeColor={cfg.activeColor} inactiveColor={cfg.inactiveColor} />
  {:else if cfg.layout === "bar-graph"}
    <BarGraph {h} {m} {s} ledShape={cfg.ledShape} activeColor={cfg.activeColor} inactiveColor={cfg.inactiveColor} />
  {:else}
    <BcdGrid {h} {m} {s} ledShape={cfg.ledShape} activeColor={cfg.activeColor} inactiveColor={cfg.inactiveColor} />
    {/if}
  </div>
</div>

<style>
  :host {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    height: 100%;
    box-sizing: border-box;
    background: var(--theme-widget-bg, transparent);
    font-family: var(--theme-font-family, "Inter", sans-serif);
    color: var(--theme-text, #cdd6f4);
    overflow: hidden;
  }

  .scale-container {
    width: 100%;
    height: 100%;
    overflow: hidden;
  }

  .binary-clock-container {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    height: 100%;
    padding: 16px;
    box-sizing: border-box;
  }
</style>
