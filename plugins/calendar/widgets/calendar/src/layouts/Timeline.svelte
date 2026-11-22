<svelte:options customElement={null} />

<script lang="ts">
  import { eventsForDate, timeToPercent, type CalendarEvent } from "../dateUtils.ts";

  let { events = [] }: { events: CalendarEvent[] } = $props();

  interface HourLabel {
    label: string;
    pct: number;
  }

  // Filter to today's events.
  let todayEvents = $derived<CalendarEvent[]>(eventsForDate(events, new Date()));

  let timedEvents = $derived<CalendarEvent[]>(todayEvents.filter((ev: CalendarEvent) => !ev.all_day));
  let allDayEvents = $derived<CalendarEvent[]>(todayEvents.filter((ev: CalendarEvent) => ev.all_day));

  // Current time marker position.
  let nowPercent = $derived<number>((() => {
    const now = new Date();
    const hours = now.getHours() + now.getMinutes() / 60;
    return (hours / 24) * 100;
  })());

  // Generate hour labels.
  let hourLabels = $derived<HourLabel[]>((() => {
    const labels: HourLabel[] = [];
    for (let h = 0; h < 24; h += 3) {
      const label = h === 0 ? "12a" : h < 12 ? `${h}a` : h === 12 ? "12p" : `${h - 12}p`;
      labels.push({ label, pct: (h / 24) * 100 });
    }
    return labels;
  })());

  function eventStyle(ev: CalendarEvent): string {
    const startPct = Math.max(0, timeToPercent(ev.start));
    let endPct = ev.end ? timeToPercent(ev.end) : startPct + 2;
    endPct = Math.min(100, endPct);
    const width = Math.max(1, endPct - startPct);
    const bg = ev.color || "var(--theme-accent, #89b4fa)";
    return `left:${startPct}%;width:${width}%;background:${bg}`;
  }
</script>

<div class="timeline">
  {#if allDayEvents.length > 0}
    <div class="allday-row">
      {#each allDayEvents as ev}
        <span class="allday-chip" style="background:{ev.color || 'var(--theme-accent, #89b4fa)'}">
          {ev.title}
        </span>
      {/each}
    </div>
  {/if}

  <div class="bar-container">
    <!-- Hour gridlines and labels -->
    {#each hourLabels as h}
      <div class="hour-mark" style="left:{h.pct}%">
        <span class="hour-label">{h.label}</span>
      </div>
    {/each}

    <!-- Timed event blocks -->
    {#each timedEvents as ev}
      <div class="event-block" style={eventStyle(ev)} title={ev.title}></div>
    {/each}

    <!-- Current time marker -->
    <div class="now-marker" style="left:{nowPercent}%"></div>
  </div>

  {#if todayEvents.length === 0}
    <div class="empty">No events today</div>
  {/if}
</div>

<style>
  .timeline {
    display: flex;
    flex-direction: column;
    height: 100%;
    padding: 0.5rem;
    gap: 0.4rem;
    justify-content: center;
  }

  .allday-row {
    display: flex;
    gap: 0.25rem;
    flex-wrap: wrap;
    flex-shrink: 0;
  }

  .allday-chip {
    font-size: 0.6rem;
    color: var(--theme-base, #1e1e2e);
    padding: 0.1rem 0.4rem;
    border-radius: 3px;
    font-weight: 500;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 120px;
  }

  .bar-container {
    position: relative;
    height: 32px;
    background: var(--theme-surface-0, rgba(255,255,255,0.04));
    border-radius: 4px;
    overflow: hidden;
    flex-shrink: 0;
  }

  .hour-mark {
    position: absolute;
    top: 0;
    bottom: 0;
    width: 0;
    border-left: 1px solid var(--theme-border, rgba(255,255,255,0.06));
  }

  .hour-label {
    position: absolute;
    top: 100%;
    left: 0;
    transform: translateX(-50%);
    font-size: 0.55rem;
    color: var(--theme-subtext, #a6adc8);
    margin-top: 2px;
    white-space: nowrap;
  }

  .event-block {
    position: absolute;
    top: 4px;
    height: calc(100% - 8px);
    border-radius: 3px;
    opacity: 0.85;
    min-width: 2px;
  }

  .now-marker {
    position: absolute;
    top: 0;
    bottom: 0;
    width: 2px;
    background: var(--theme-accent, #89b4fa);
    z-index: 1;
  }

  .empty {
    font-size: 0.8rem;
    color: var(--theme-subtext, #a6adc8);
    text-align: center;
    padding: 0.5rem 0;
  }
</style>
