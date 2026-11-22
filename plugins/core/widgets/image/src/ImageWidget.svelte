<svelte:options customElement="image-widget" />

<script lang="ts">
  import StaticView from "./layouts/StaticView.svelte";
  import SlideshowView from "./layouts/SlideshowView.svelte";

  let { config: configAttr = "{}" } = $props();

  const DEFAULTS: Record<string, any> = {
    layout: "static",
    images: [],
    dashboardWidgetId: "",
    fit: "cover",
    aspectRatio: "",
    refreshInterval: 0,
    slideInterval: 5,
    transition: "fade",
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
      parsed = configAttr as Record<string, any>;
    }
    return { ...DEFAULTS, ...parsed };
  })());

  // Collect all image sources from the images list.
  let allSources = $derived((() => {
    if (!Array.isArray(cfg.images)) return [];
    return cfg.images.map((img: Record<string, any>) => img.url).filter(Boolean);
  })());
</script>

{#if allSources.length === 0}
  <div class="empty-state">
    <svg class="empty-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <rect x="3" y="3" width="18" height="18" rx="2" ry="2" />
      <circle cx="8.5" cy="8.5" r="1.5" />
      <polyline points="21 15 16 10 5 21" />
    </svg>
    <span class="empty-text">No image configured</span>
  </div>
{:else if cfg.layout === "slideshow" && allSources.length > 1}
  <SlideshowView
    sources={allSources}
    fit={cfg.fit}
    aspectRatio={cfg.aspectRatio}
    refreshInterval={cfg.refreshInterval}
    slideInterval={cfg.slideInterval}
    transition={cfg.transition}
  />
{:else}
  <StaticView
    source={allSources[0]}
    fit={cfg.fit}
    aspectRatio={cfg.aspectRatio}
    refreshInterval={cfg.refreshInterval}
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
</style>
