<svelte:options customElement="video-widget" />

<script lang="ts">
  import { detectEmbed } from "./embed.ts";
  import type { EmbedResult } from "./embed.ts";
  import PlayerView from "./layouts/PlayerView.svelte";
  import AmbientView from "./layouts/AmbientView.svelte";

  let { config: configAttr = "{}" }: { config: string | Record<string, any> } = $props();

  const DEFAULTS: Record<string, any> = {
    layout: "player",
    source: "",
    dashboardWidgetId: "",
    sizing: "contain",
    autoplay: false,
    loop: false,
    mute: false,
  };

  let cfg = $derived((() => {
    let parsed: Record<string, any> = {};
    if (typeof configAttr === "string") {
      try {
        parsed = JSON.parse(configAttr);
      } catch {
        parsed = {};
      }
    } else if (typeof configAttr === "object" && configAttr !== null) {
      parsed = configAttr;
    }
    return { ...DEFAULTS, ...parsed };
  })());

  let videoSource: string = $derived(cfg.source || "");
  let embed: EmbedResult | null = $derived(detectEmbed(videoSource));

  // Parse unified sizing value into aspectRatio and fit for layout views.
  const FIT_VALUES = new Set(["contain", "cover", "fill"]);
  let aspectRatio: string = $derived(FIT_VALUES.has(cfg.sizing) ? "" : (cfg.sizing || ""));
  let fit: string = $derived(FIT_VALUES.has(cfg.sizing) ? cfg.sizing : "fill");

  // Embed sizing via ResizeObserver. YouTube/Vimeo always render at 16:9 internally,
  // so we size/position the iframe to achieve contain, cover, fill, or aspect crop.
  const NATIVE_RATIO = 16 / 9;
  let embedOuterEl = $state<HTMLElement | null>(null);
  let embedAreaW = $state<string>("100%");
  let embedAreaH = $state<string>("100%");
  let embedIframeW = $state<string>("100%");
  let embedIframeH = $state<string>("100%");
  let embedIframeLeft = $state<string>("0");
  let embedIframeTop = $state<string>("0");
  let embedIframeTransform = $state<string>("");

  function parseRatio(r: string): number {
    if (!r) return 0;
    const parts = r.split("/");
    const val = parseFloat(parts[0]) / parseFloat(parts[1] || "1");
    return isFinite(val) ? val : 0;
  }

  $effect(() => {
    if (!embedOuterEl) return;
    const sizing = cfg.sizing || "contain";

    const observer = new ResizeObserver((entries) => {
      const { width: cw, height: ch } = entries[0].contentRect;
      if (cw <= 0 || ch <= 0) return;

      const isContain = sizing === "contain";
      const isCover = sizing === "cover";
      const isFill = sizing === "fill";
      const targetRatio = FIT_VALUES.has(sizing) ? 0 : parseRatio(sizing);

      // Visible area dimensions.
      let aw: number, ah: number;
      if (isCover || isFill) {
        aw = cw; ah = ch;
      } else if (isContain) {
        // Letterbox 16:9 inside widget.
        if (cw / ch > NATIVE_RATIO) { ah = ch; aw = ch * NATIVE_RATIO; }
        else { aw = cw; ah = cw / NATIVE_RATIO; }
      } else {
        // Specific aspect ratio fitted inside widget.
        if (cw / ch > targetRatio) { ah = ch; aw = ch * targetRatio; }
        else { aw = cw; ah = cw / targetRatio; }
      }

      // Iframe dimensions: 16:9 contained within the visible area, then stretched to fill.
      let iw: number, ih: number;
      if (isContain) {
        iw = aw; ih = ah;
      } else if (isCover) {
        // 16:9 that covers the visible area (crop excess).
        ih = Math.max(ah, aw * 9 / 16);
        iw = ih * NATIVE_RATIO;
      } else {
        // Contain 16:9 inside the visible area first.
        if (aw / ah > NATIVE_RATIO) { ih = ah; iw = ah * NATIVE_RATIO; }
        else { iw = aw; ih = aw / NATIVE_RATIO; }
      }

      // Fill and aspect ratio modes stretch the contained iframe to fill the area.
      if (isFill || targetRatio) {
        embedIframeTransform = `scale(${aw / iw}, ${ah / ih})`;
      } else {
        embedIframeTransform = "";
      }

      embedAreaW = `${aw}px`;
      embedAreaH = `${ah}px`;
      embedIframeW = `${iw}px`;
      embedIframeH = `${ih}px`;
      embedIframeLeft = `${(aw - iw) / 2}px`;
      embedIframeTop = `${(ah - ih) / 2}px`;
    });
    observer.observe(embedOuterEl);

    return () => observer.disconnect();
  });
</script>

{#if !videoSource}
  <div class="empty-state">
    <svg class="empty-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <polygon points="23 7 16 12 23 17 23 7" />
      <rect x="1" y="5" width="15" height="14" rx="2" ry="2" />
    </svg>
    <span class="empty-text">No video configured</span>
  </div>
{:else if embed}
  <!-- Embedded platform player (YouTube, Vimeo, etc.) -->
  <div class="embed-outer" bind:this={embedOuterEl}>
    <div class="embed-area" style:width={embedAreaW} style:height={embedAreaH}>
      <iframe
        class="embed-iframe"
        src="{embed.embedUrl}?autoplay={cfg.autoplay ? 1 : 0}&loop={cfg.loop ? 1 : 0}&mute={cfg.mute ? 1 : 0}"
        title="{embed.platform} player"
        allow="autoplay; fullscreen; picture-in-picture"
        allowfullscreen
        frameborder="0"
        style:width={embedIframeW}
        style:height={embedIframeH}
        style:left={embedIframeLeft}
        style:top={embedIframeTop}
        style:transform={embedIframeTransform || undefined}
      ></iframe>
    </div>
  </div>
{:else if cfg.layout === "ambient"}
  <AmbientView
    source={videoSource}
    {aspectRatio}
    {fit}
    mute={cfg.mute}
  />
{:else}
  <PlayerView
    source={videoSource}
    {aspectRatio}
    {fit}
    autoplay={cfg.autoplay}
    loop={cfg.loop}
    mute={cfg.mute}
  />
{/if}

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

  .empty-state {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    flex: 1;
    gap: 0.75rem;
    opacity: 0.5;
  }

  .empty-icon {
    width: 32px;
    height: 32px;
    color: var(--theme-subtext, #a6adc8);
  }

  .empty-text {
    font-size: 0.8rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .embed-outer {
    width: 100%;
    height: 100%;
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    background: #000;
    overflow: hidden;
  }

  .embed-area {
    position: relative;
    overflow: hidden;
  }

  .embed-iframe {
    position: absolute;
    border: none;
    border-radius: var(--theme-radius-md, 8px);
  }
</style>
