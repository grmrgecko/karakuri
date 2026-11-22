<svelte:options customElement="calendar-widget" />

<script lang="ts">
  import type { CalendarEvent } from "./dateUtils.ts";
  import MonthlyGrid from "./layouts/MonthlyGrid.svelte";
  import Weekly from "./layouts/Weekly.svelte";
  import Agenda from "./layouts/Agenda.svelte";
  import DayFocus from "./layouts/DayFocus.svelte";
  import Timeline from "./layouts/Timeline.svelte";

  // The config attribute is a JSON string passed from the dashboard host.
  let { config: configAttr = "{}" } = $props();

  const DEFAULTS = {
    layout: "monthly",
    startOfWeek: 0,
    showTags: "",
    dashboardId: "",
    dashboardWidgetId: "",
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
      parsed = configAttr as Record<string, unknown>;
    }
    return { ...DEFAULTS, ...parsed };
  })());

  // Calendar event data state.
  let events = $state<CalendarEvent[]>([]);
  // showTags narrows the events to sources carrying one of the listed tags,
  // so one widget can show one person's calendars with showTags "${person}".
  // The host has already substituted the reference.
  let tags = $derived(
    String(cfg.showTags ?? "").split(",").map((t) => t.trim()).filter((t) => t && !t.includes("${")),
  );
  let shown = $derived(tags.length === 0 ? events : events.filter((e) => !!e.tag && tags.includes(e.tag)));
  let loading = $state<boolean>(true);
  let error = $state<string | null>(null);
  let wsUnsub: (() => void) | null = null;
  let staleTimer: ReturnType<typeof setTimeout> | null = null;

  // Build the MQ topic for this widget.
  function buildTopic(dashboardId: string, widgetId: string): string {
    return `calendar:update:${dashboardId}:${widgetId}`;
  }

  // Fetch calendar data via HTTP.
  async function fetchData(): Promise<void> {
    const { dashboardId, dashboardWidgetId } = cfg;
    if (!dashboardId || !dashboardWidgetId) {
      loading = false;
      return;
    }

    loading = true;
    error = null;
    try {
      const path = `/plugins/calendar/${dashboardId}/${dashboardWidgetId}/data`;
      const data = await (window as any).__karakuri?.api?.get(path);
      events = data?.events || [];
      loading = false;
    } catch (err: unknown) {
      error = (err as Error).message || "Failed to fetch calendar data";
      loading = false;
    }
  }

  // Subscribe to WebSocket topic for real-time updates.
  function subscribeWs(): void {
    unsubscribeWs();
    const { dashboardId, dashboardWidgetId } = cfg;
    if (!dashboardId || !dashboardWidgetId) return;

    const topic = buildTopic(dashboardId as string, dashboardWidgetId as string);
    if ((window as any).__karakuri?.subscribeTopic) {
      wsUnsub = (window as any).__karakuri.subscribeTopic(topic, (data: any) => {
        events = data?.events || [];
        loading = false;
        error = null;
        resetStaleTimer();
      });
    }
  }

  function unsubscribeWs(): void {
    if (wsUnsub) {
      wsUnsub();
      wsUnsub = null;
    }
  }

  // Re-fetch via HTTP if no WebSocket update within 20 minutes.
  function resetStaleTimer(): void {
    if (staleTimer) clearTimeout(staleTimer);
    staleTimer = setTimeout(() => {
      fetchData();
    }, 20 * 60 * 1000);
  }

  function clearStaleTimer(): void {
    if (staleTimer) {
      clearTimeout(staleTimer);
      staleTimer = null;
    }
  }

  function retry(): void {
    fetchData();
    subscribeWs();
  }

  // Fetch and subscribe on mount and whenever config changes.
  $effect(() => {
    const key = `${cfg.dashboardId}:${cfg.dashboardWidgetId}:${cfg.layout}`;
    fetchData();
    subscribeWs();
    resetStaleTimer();

    return () => {
      unsubscribeWs();
      clearStaleTimer();
    };
  });
</script>

<!-- Loading State -->
{#if loading && events.length === 0}
  <div class="loading-container">
    <svg class="spinner" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
      <circle cx="12" cy="12" r="10" stroke-opacity="0.25" />
      <path d="M12 2a10 10 0 0 1 10 10" stroke-linecap="round" />
    </svg>
    <span class="loading-text">Loading calendar...</span>
  </div>

<!-- Error State -->
{:else if error && events.length === 0}
  <div class="error-container">
    <svg class="error-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <circle cx="12" cy="12" r="10" />
      <line x1="12" y1="8" x2="12" y2="12" />
      <line x1="12" y1="16" x2="12.01" y2="16" />
    </svg>
    <span class="error-text">{error}</span>
    <button class="retry-button" onclick={retry}>Retry</button>
  </div>

<!-- Calendar Display -->
{:else}
  <div class="calendar-container">
    {#if cfg.layout === "weekly"}
      <Weekly events={shown} startOfWeek={cfg.startOfWeek} />
    {:else if cfg.layout === "agenda"}
      <Agenda events={shown} />
    {:else if cfg.layout === "dayfocus"}
      <DayFocus events={shown} />
    {:else if cfg.layout === "timeline"}
      <Timeline events={shown} />
    {:else}
      <MonthlyGrid events={shown} startOfWeek={cfg.startOfWeek} />
    {/if}
  </div>
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

  .loading-container {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    flex: 1;
    gap: 0.75rem;
  }

  .spinner {
    width: 32px;
    height: 32px;
    color: var(--theme-accent, #89b4fa);
    animation: spin 1s linear infinite;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  .loading-text {
    font-size: 0.85rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .error-container {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    flex: 1;
    gap: 0.75rem;
    text-align: center;
    padding: 1rem;
  }

  .error-icon {
    width: 32px;
    height: 32px;
    color: var(--theme-error, #f38ba8);
  }

  .error-text {
    font-size: 0.85rem;
    color: var(--theme-error, #f38ba8);
    max-width: 200px;
    word-wrap: break-word;
  }

  .retry-button {
    padding: 0.4rem 1rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: var(--theme-radius-sm, 4px);
    background: var(--theme-surface-0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.8rem;
    cursor: pointer;
    transition: background 0.2s, border-color 0.2s;
    font-family: inherit;
  }

  .retry-button:hover {
    background: var(--theme-surface-1, #45475a);
    border-color: var(--theme-accent, #89b4fa);
  }

  .retry-button:active {
    transform: scale(0.97);
  }

  .calendar-container {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
  }
</style>
