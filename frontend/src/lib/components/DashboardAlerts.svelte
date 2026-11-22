<script lang="ts">
  import { fly, fade } from 'svelte/transition';
  import type { LiveAlert } from '../stores/dashboards';

  // DashboardAlerts draws the alerts a dashboard's runtime state holds:
  // toasts stacked in the corner and the newest full-screen alert over
  // everything. The server expires them; a viewer may dismiss one locally.
  let { alerts }: { alerts: LiveAlert[] } = $props();

  let dismissed = $state<Record<string, boolean>>({});

  let shown = $derived(alerts.filter((a) => !dismissed[a.id]));
  let toasts = $derived(shown.filter((a) => a.style !== 'fullscreen'));
  let fullscreen = $derived([...shown].reverse().find((a) => a.style === 'fullscreen'));

  const icons: Record<string, string> = {
    info: 'la-info-circle',
    success: 'la-check-circle',
    warning: 'la-exclamation-triangle',
    error: 'la-times-circle',
  };

  function dismiss(id: string) {
    dismissed = { ...dismissed, [id]: true };
  }
</script>

{#if fullscreen}
  {#key fullscreen.id}
    <div class="alert-fullscreen level-{fullscreen.level}" role="alert" transition:fade={{ duration: 250 }}>
      <button class="fullscreen-body" onclick={() => dismiss(fullscreen!.id)} aria-label="Dismiss alert">
        <i class="las {icons[fullscreen.level] ?? icons.info} alert-icon"></i>
        {#if fullscreen.title}<div class="fullscreen-title">{fullscreen.title}</div>{/if}
        {#if fullscreen.message}<div class="fullscreen-message">{fullscreen.message}</div>{/if}
      </button>
    </div>
  {/key}
{/if}

{#if toasts.length > 0}
  <div class="toasts" aria-live="polite">
    {#each toasts as alert (alert.id)}
      <button class="toast level-{alert.level}" onclick={() => dismiss(alert.id)} transition:fly={{ x: 40, duration: 250 }}>
        <i class="las {icons[alert.level] ?? icons.info}"></i>
        <span class="toast-text">
          {#if alert.title}<strong>{alert.title}</strong>{/if}
          {#if alert.message}<span>{alert.message}</span>{/if}
        </span>
      </button>
    {/each}
  </div>
{/if}

<style>
  .toasts {
    position: absolute;
    top: 1rem;
    right: 1rem;
    z-index: 70;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    max-width: min(26rem, 80vw);
  }

  .toast {
    display: flex;
    gap: 0.6rem;
    align-items: flex-start;
    text-align: left;
    padding: 0.7rem 0.9rem;
    border-radius: var(--theme-radius-md, 8px);
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    border-left: 4px solid var(--alert-color);
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
    cursor: pointer;
    font-size: 0.95rem;
  }

  .toast i {
    color: var(--alert-color);
    font-size: 1.3rem;
  }

  .toast-text {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    white-space: pre-line;
  }

  .alert-fullscreen {
    position: absolute;
    inset: 0;
    z-index: 80;
    display: flex;
    background: color-mix(in srgb, var(--theme-base, #1e1e2e) 92%, transparent);
  }

  .fullscreen-body {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 1rem;
    padding: 2rem;
    text-align: center;
    background: transparent;
    border: none;
    color: var(--theme-text, #cdd6f4);
    cursor: pointer;
  }

  .alert-icon {
    font-size: 5rem;
    color: var(--alert-color);
  }

  .fullscreen-title {
    font-size: clamp(1.5rem, 5vw, 3.5rem);
    font-weight: 600;
  }

  .fullscreen-message {
    font-size: clamp(1rem, 3vw, 2rem);
    max-width: 50rem;
    white-space: pre-line;
    opacity: 0.85;
  }

  .level-info { --alert-color: var(--theme-info, #89b4fa); }
  .level-success { --alert-color: var(--theme-success, #a6e3a1); }
  .level-warning { --alert-color: var(--theme-warning, #f9e2af); }
  .level-error { --alert-color: var(--theme-error, #f38ba8); }
</style>
