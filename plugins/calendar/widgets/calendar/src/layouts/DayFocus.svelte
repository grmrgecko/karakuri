<svelte:options customElement={null} />

<script lang="ts">
  import { getNextEvent, formatTime, getMonthName, getDayName, type CalendarEvent } from "../dateUtils.ts";

  let { events = [] }: { events: CalendarEvent[] } = $props();

  let now: Date = new Date();
  let dayNumber = $derived<number>(now.getDate());
  let dayName = $derived<string>(getDayName(now.getDay()));
  let monthYear = $derived<string>(`${getMonthName(now.getMonth())} ${now.getFullYear()}`);

  let nextEvent = $derived<CalendarEvent | null>(getNextEvent(events));

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="day-focus" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
    <div class="date-display">
      <span class="day-number">{dayNumber}</span>
      <span class="day-name">{dayName}</span>
      <span class="month-year">{monthYear}</span>
    </div>

    <div class="next-event-card">
      {#if nextEvent}
        <div class="event-label">Next Event</div>
        <div class="event-detail">
          <span class="event-time">
            {#if nextEvent.all_day}
              All Day
            {:else}
              {formatTime(nextEvent.start)}
            {/if}
          </span>
          <span class="event-title">{nextEvent.title}</span>
          {#if nextEvent.location}
            <span class="event-location">{nextEvent.location}</span>
          {/if}
        </div>
      {:else}
        <div class="no-events">No upcoming events</div>
      {/if}
    </div>
  </div>
</div>

<style>
  .day-focus {
    width: 100%;
    height: 100%;
    overflow: hidden;
  }

  .scale-content {
    display: flex;
    flex-direction: column;
    height: 100%;
    padding: 0.75rem;
    gap: 0.75rem;
  }

  .date-display {
    display: flex;
    flex-direction: column;
    align-items: center;
    flex: 1;
    justify-content: center;
  }

  .day-number {
    font-size: 6.25rem;
    font-weight: 700;
    line-height: 1;
    color: var(--theme-text, #cdd6f4);
  }

  .day-name {
    font-size: 1.6rem;
    font-weight: 600;
    color: var(--theme-accent, #89b4fa);
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  .month-year {
    font-size: 0.8rem;
    color: var(--theme-subtext, #a6adc8);
    margin-top: 0.15rem;
  }

  .next-event-card {
    background: var(--theme-surface-0, rgba(255,255,255,0.04));
    border: 1px solid var(--theme-border, rgba(255,255,255,0.08));
    border-radius: 6px;
    padding: 0.5rem 0.6rem;
    flex-shrink: 0;
  }

  .event-label {
    font-size: 0.65rem;
    font-weight: 500;
    color: var(--theme-subtext, #a6adc8);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    margin-bottom: 0.25rem;
  }

  .event-detail {
    display: flex;
    flex-direction: column;
    gap: 0.1rem;
  }

  .event-time {
    font-size: 0.75rem;
    color: var(--theme-accent, #89b4fa);
    font-weight: 500;
  }

  .event-title {
    font-size: 0.85rem;
    font-weight: 500;
    color: var(--theme-text, #cdd6f4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .event-location {
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .no-events {
    font-size: 0.8rem;
    color: var(--theme-subtext, #a6adc8);
    text-align: center;
    padding: 0.25rem 0;
  }
</style>
