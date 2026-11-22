<svelte:options customElement={null} />

<script lang="ts">
  import { eventsForDate, formatTime, relativeDay, type CalendarEvent } from "../dateUtils.ts";

  let { events = [] }: { events: CalendarEvent[] } = $props();

  interface DayGroup {
    label: string;
    events: CalendarEvent[];
  }

  // Group events by day for the next 7 days.
  let grouped = $derived<DayGroup[]>((() => {
    const now = new Date();
    const groups: DayGroup[] = [];
    for (let i = 0; i < 7; i++) {
      const date = new Date(now.getFullYear(), now.getMonth(), now.getDate() + i);
      const dayEvents = eventsForDate(events, date);

      if (dayEvents.length > 0) {
        groups.push({
          label: relativeDay(date.toISOString()),
          events: dayEvents,
        });
      }
    }
    return groups;
  })());
</script>

<div class="agenda">
  {#if grouped.length === 0}
    <div class="empty">
      <span class="empty-text">No upcoming events</span>
    </div>
  {:else}
    {#each grouped as group}
      <div class="day-group">
        <div class="day-label">{group.label}</div>
        {#each group.events as ev}
          <div class="event-row">
            <div class="color-bar" style="background:{ev.color || 'var(--theme-accent, #89b4fa)'}"></div>
            <div class="event-info">
              <span class="event-time">
                {#if ev.all_day}
                  All Day
                {:else}
                  {formatTime(ev.start)}{#if ev.end} - {formatTime(ev.end)}{/if}
                {/if}
              </span>
              <span class="event-title">{ev.title}</span>
              {#if ev.location}
                <span class="event-location">{ev.location}</span>
              {/if}
            </div>
          </div>
        {/each}
      </div>
    {/each}
  {/if}
</div>

<style>
  .agenda {
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow-y: auto;
    padding: 0.5rem;
    gap: 0.6rem;
  }

  .empty {
    display: flex;
    align-items: center;
    justify-content: center;
    flex: 1;
  }

  .empty-text {
    font-size: 0.85rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .day-group {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }

  .day-label {
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--theme-accent, #89b4fa);
    text-transform: uppercase;
    letter-spacing: 0.03em;
    padding-bottom: 0.15rem;
    border-bottom: 1px solid var(--theme-border, rgba(255,255,255,0.08));
  }

  .event-row {
    display: flex;
    gap: 0.5rem;
    align-items: stretch;
    padding: 0.3rem 0;
  }

  .color-bar {
    width: 3px;
    border-radius: 2px;
    flex-shrink: 0;
  }

  .event-info {
    display: flex;
    flex-direction: column;
    gap: 0.1rem;
    min-width: 0;
  }

  .event-time {
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .event-title {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--theme-text, #cdd6f4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .event-location {
    font-size: 0.65rem;
    color: var(--theme-subtext, #a6adc8);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
</style>
