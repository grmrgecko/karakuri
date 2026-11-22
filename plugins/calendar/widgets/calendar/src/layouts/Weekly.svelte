<svelte:options customElement={null} />

<script lang="ts">
  import { formatTime, eventsForDate, getDayHeaders, weekDates, type CalendarEvent } from "../dateUtils.ts";

  let { events = [], startOfWeek = 0 }: { events: CalendarEvent[]; startOfWeek: number } = $props();

  const SHORT_MONTHS: string[] = ["Jan","Feb","Mar","Apr","May","Jun","Jul","Aug","Sep","Oct","Nov","Dec"];

  interface WeekDay {
    date: Date;
    dayNum: number;
    dayName: string;
    monthLabel: string;
    isToday: boolean;
  }

  // Week navigation state.
  let weekOffset = $state<number>(0);

  // The 7 days of the displayed week, starting on startOfWeek.
  let displayDays = $derived<WeekDay[]>((() => {
    const now = new Date();
    const todayStr = `${now.getFullYear()}-${now.getMonth()}-${now.getDate()}`;
    const headers = getDayHeaders(startOfWeek);
    return weekDates(now, startOfWeek, weekOffset).map((date: Date, i: number) => ({
      date,
      dayNum: date.getDate(),
      dayName: headers[i],
      monthLabel: SHORT_MONTHS[date.getMonth()],
      isToday: `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}` === todayStr,
    }));
  })());

  // Week range label like "Feb 9 - Feb 15, 2026".
  let weekLabel = $derived<string>((() => {
    const first = displayDays[0];
    const last = displayDays[6];
    const f = first.date;
    const l = last.date;
    if (f.getFullYear() === l.getFullYear() && f.getMonth() === l.getMonth()) {
      return `${first.monthLabel} ${first.dayNum} - ${last.dayNum}, ${f.getFullYear()}`;
    }
    if (f.getFullYear() === l.getFullYear()) {
      return `${first.monthLabel} ${first.dayNum} - ${last.monthLabel} ${last.dayNum}, ${f.getFullYear()}`;
    }
    return `${first.monthLabel} ${first.dayNum}, ${f.getFullYear()} - ${last.monthLabel} ${last.dayNum}, ${l.getFullYear()}`;
  })());

  function prevWeek(): void { weekOffset--; }
  function nextWeek(): void { weekOffset++; }
  function goThisWeek(): void { weekOffset = 0; }

  function getDayEvents(date: Date): CalendarEvent[] {
    return eventsForDate(events, date);
  }

  function getAllDay(dayEvents: CalendarEvent[]): CalendarEvent[] {
    return dayEvents.filter((ev: CalendarEvent) => ev.all_day);
  }

  function getTimed(dayEvents: CalendarEvent[]): CalendarEvent[] {
    return dayEvents.filter((ev: CalendarEvent) => !ev.all_day);
  }
</script>

<div class="weekly">
  <div class="header">
    <button class="nav-btn" onclick={prevWeek} aria-label="Previous week">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="15 18 9 12 15 6"/></svg>
    </button>
    <button class="week-label" onclick={goThisWeek}>{weekLabel}</button>
    <button class="nav-btn" onclick={nextWeek} aria-label="Next week">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="9 6 15 12 9 18"/></svg>
    </button>
  </div>

  <div class="day-columns">
    {#each displayDays as day}
      {@const dayEvents = getDayEvents(day.date)}
      {@const allDay = getAllDay(dayEvents)}
      {@const timed = getTimed(dayEvents)}
      <div class="day-column" class:is-today={day.isToday}>
        <div class="day-header">
          <span class="day-name">{day.dayName}</span>
          <span class="day-num" class:today-num={day.isToday}>{day.dayNum}</span>
        </div>
        <div class="day-events">
          {#each allDay as ev}
            <div class="event-chip allday" style="background:{ev.color || 'var(--theme-accent, #89b4fa)'}">
              <span class="chip-title">{ev.title}</span>
            </div>
          {/each}
          {#each timed as ev}
            <div class="event-chip timed">
              <div class="chip-color" style="background:{ev.color || 'var(--theme-accent, #89b4fa)'}"></div>
              <div class="chip-content">
                <span class="chip-time">{formatTime(ev.start)}</span>
                <span class="chip-title">{ev.title}</span>
              </div>
            </div>
          {/each}
          {#if dayEvents.length === 0}
            <div class="no-events"></div>
          {/if}
        </div>
      </div>
    {/each}
  </div>
</div>

<style>
  .weekly {
    display: flex;
    flex-direction: column;
    height: 100%;
  }

  .header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.4rem 0.5rem;
    flex-shrink: 0;
  }

  .nav-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--theme-subtext, #a6adc8);
    cursor: pointer;
    padding: 0;
  }

  .nav-btn:hover {
    background: var(--theme-surface-1, #45475a);
    color: var(--theme-text, #cdd6f4);
  }

  .nav-btn svg {
    width: 16px;
    height: 16px;
  }

  .week-label {
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--theme-text, #cdd6f4);
    background: none;
    border: none;
    cursor: pointer;
    padding: 0.2rem 0.5rem;
    border-radius: 4px;
    font-family: inherit;
  }

  .week-label:hover {
    background: var(--theme-surface-1, #45475a);
  }

  .day-columns {
    display: grid;
    grid-template-columns: repeat(7, 1fr);
    flex: 1;
    min-height: 0;
    gap: 1px;
    padding: 0 0.25rem 0.25rem;
  }

  .day-column {
    display: flex;
    flex-direction: column;
    min-height: 0;
    overflow: hidden;
    border-radius: 3px;
  }

  .day-column.is-today {
    background: var(--theme-surface-0, rgba(255,255,255,0.03));
  }

  .day-header {
    display: flex;
    flex-direction: column;
    align-items: center;
    padding: 0.2rem 0;
    flex-shrink: 0;
    gap: 0.1rem;
  }

  .day-name {
    font-size: 0.6rem;
    font-weight: 500;
    color: var(--theme-subtext, #a6adc8);
    text-transform: uppercase;
    letter-spacing: 0.03em;
  }

  .day-num {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--theme-text, #cdd6f4);
    line-height: 1;
  }

  .today-num {
    background: var(--theme-accent, #89b4fa);
    color: var(--theme-base, #1e1e2e);
    border-radius: 50%;
    width: 1.5em;
    height: 1.5em;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .day-events {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 0.15rem;
    flex: 1;
    min-height: 0;
    overflow-y: auto;
  }

  .event-chip {
    border-radius: 3px;
    font-size: 0.65rem;
    min-height: 0;
    flex-shrink: 0;
  }

  .event-chip.allday {
    padding: 0.15rem 0.25rem;
    color: var(--theme-base, #1e1e2e);
    font-weight: 500;
  }

  .event-chip.timed {
    display: flex;
    gap: 0.25rem;
    align-items: stretch;
    padding: 0.1rem 0.2rem;
  }

  .chip-color {
    width: 2px;
    border-radius: 1px;
    flex-shrink: 0;
  }

  .chip-content {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  .chip-time {
    font-size: 0.55rem;
    color: var(--theme-subtext, #a6adc8);
    line-height: 1.2;
  }

  .chip-title {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    line-height: 1.3;
  }

  .no-events {
    flex: 1;
  }
</style>
