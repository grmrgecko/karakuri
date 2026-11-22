<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { loadWidget } from '../widgetLoader';

  let { packageName, tagName, config = '{}', entryPoint = 'index.js' }: {
    packageName: string;
    tagName: string;
    config?: string;
    entryPoint?: string;
  } = $props();

  let container: HTMLDivElement;
  let element = $state<HTMLElement | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  onMount(async () => {
    try {
      await loadWidget(packageName, entryPoint);
      element = document.createElement(tagName);
      element.setAttribute('config', config);
      element.style.width = '100%';
      element.style.height = '100%';
      container.appendChild(element);
      loading = false;
    } catch (e) {
      error = `Failed to load widget: ${packageName}`;
      loading = false;
    }
  });

  onDestroy(() => {
    if (element && container?.contains(element)) {
      container.removeChild(element);
    }
  });

  $effect(() => {
    if (element && config) {
      element.setAttribute('config', config);
    }
  });
</script>

<div
  bind:this={container}
  class="widget-container"
  data-widget-state={error ? 'error' : loading ? 'loading' : 'ready'}
>
  {#if loading}
    <div class="widget-loading">
      <div class="spinner"></div>
    </div>
  {/if}
  {#if error}
    <div class="widget-error">
      <span>{error}</span>
    </div>
  {/if}
</div>

<style>
  .widget-container {
    width: 100%;
    height: 100%;
    position: relative;
    overflow: hidden;
  }

  .widget-loading, .widget-error {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    height: 100%;
    color: var(--theme-text-muted, #a6adc8);
    font-size: 0.875rem;
  }

  .widget-error {
    color: var(--theme-error, #f38ba8);
  }

  .spinner {
    width: 24px;
    height: 24px;
    border: 3px solid var(--theme-surface1, #45475a);
    border-top-color: var(--theme-accent, #89b4fa);
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }

  @keyframes spin {
    to { transform: rotate(360deg); }
  }
</style>
