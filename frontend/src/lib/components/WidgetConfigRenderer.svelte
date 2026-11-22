<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { loadWidget } from '../widgetLoader';

  let { packageName, configTagName, config = '{}', entryPoint = 'index.js', onconfigchange }: {
    packageName: string;
    configTagName: string;
    config?: string;
    entryPoint?: string;
    onconfigchange?: (detail: any) => void;
  } = $props();

  let container: HTMLDivElement;
  let element = $state<HTMLElement | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  function handleConfigChange(event: Event) {
    onconfigchange?.((event as CustomEvent).detail);
  }

  onMount(async () => {
    try {
      await loadWidget(packageName, entryPoint);
      element = document.createElement(configTagName);
      element.setAttribute('config', config);
      element.addEventListener('configchange', handleConfigChange);
      container.appendChild(element);
      loading = false;
    } catch (e) {
      error = `Failed to load config: ${configTagName}`;
      loading = false;
    }
  });

  onDestroy(() => {
    if (element) {
      element.removeEventListener('configchange', handleConfigChange);
      if (container?.contains(element)) container.removeChild(element);
    }
  });

  $effect(() => {
    if (element && config) {
      element.setAttribute('config', config);
    }
  });
</script>

<div bind:this={container} class="config-container">
  {#if loading}
    <div class="config-loading">
      <div class="spinner"></div>
      <span class="loading-text">Loading config...</span>
    </div>
  {/if}
  {#if error}
    <div class="config-error">
      <span>{error}</span>
    </div>
  {/if}
</div>

<style>
  .config-container {
    width: 100%;
  }

  .config-loading, .config-error {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 2rem 0;
    gap: 0.5rem;
    color: var(--theme-text-muted, #a6adc8);
    font-size: 0.875rem;
  }

  .config-error {
    color: var(--theme-error, #f38ba8);
  }

  .loading-text {
    font-size: 0.8rem;
  }

  .spinner {
    width: 20px;
    height: 20px;
    border: 2px solid var(--theme-surface1, #45475a);
    border-top-color: var(--theme-accent, #89b4fa);
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }

  @keyframes spin {
    to { transform: rotate(360deg); }
  }
</style>
