<svelte:options customElement={null} />

<script lang="ts">
  import { getMonthGrid, getDayHeaders, eventsForDate, getMonthName, type CalendarEvent, type DayCell } from "../dateUtils.ts";

  let { events = [], startOfWeek = 0 }: { events: CalendarEvent[]; startOfWeek: number } = $props();

  let now: Date = new Date();
  let viewYear = $state<number>(now.getFullYear());
  let viewMonth = $state<number>(now.getMonth());

  let weeks = $derived<DayCell[][]>(getMonthGrid(viewYear, viewMonth, startOfWeek));
  let dayHeaders = $derived<string[]>(getDayHeaders(startOfWeek));
  let monthLabel = $derived<string>(`${getMonthName(viewMonth)} ${viewYear}`);

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });

  function prevMonth(): void {
    if (viewMonth === 0) {
      viewMonth = 11;
      viewYear--;
    } else {
      viewMonth--;
    }
  }

  function nextMonth(): void {
    if (viewMonth === 11) {
      viewMonth = 0;
      viewYear++;
    } else {
      viewMonth++;
    }
  }

  function goToday(): void {
    const today = new Date();
    viewYear = today.getFullYear();
    viewMonth = today.getMonth();
  }

  function getEventDots(date: Date): CalendarEvent[] {
    const dayEvents = eventsForDate(events, date);
    return dayEvents.slice(0, 3);
  }

  function extraCount(date: Date): number {
    const dayEvents = eventsForDate(events, date);
    return dayEvents.length > 3 ? dayEvents.length - 3 : 0;
  }
</script>

<div class="monthly-grid" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
    <div class="header">
      <button class="nav-btn" onclick={prevMonth} aria-label="Previous month">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="15 18 9 12 15 6"/></svg>
      </button>
      <button class="month-label" onclick={goToday}>{monthLabel}</button>
      <button class="nav-btn" onclick={nextMonth} aria-label="Next month">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="9 6 15 12 9 18"/></svg>
      </button>
    </div>

    <div class="day-headers">
      {#each dayHeaders as day}
        <div class="day-header">{day}</div>
      {/each}
    </div>

    <div class="weeks">
      {#each weeks as week}
        <div class="week-row">
          {#each week as cell}
            <div class="day-cell" class:out-of-month={!cell.inMonth} class:is-today={cell.isToday}>
              <span class="day-number">{cell.day}</span>
              <div class="dots">
                {#each getEventDots(cell.date) as ev}
                  <span class="dot" style="background:{ev.color || 'var(--theme-accent, #89b4fa)'}"></span>
                {/each}
                {#if extraCount(cell.date) > 0}
                  <span class="extra">+{extraCount(cell.date)}</span>
                {/if}
              </div>
            </div>
          {/each}
        </div>
      {/each}
    </div>
  </div>
</div>

<style>
  .monthly-grid {
    width: 100%;
    height: 100%;
    overflow: hidden;
  }

  .scale-content {
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

  .month-label {
    font-size: 0.9rem;
    font-weight: 600;
    color: var(--theme-text, #cdd6f4);
    background: none;
    border: none;
    cursor: pointer;
    padding: 0.2rem 0.5rem;
    border-radius: 4px;
    font-family: inherit;
  }

  .month-label:hover {
    background: var(--theme-surface-1, #45475a);
  }

  .day-headers {
    display: grid;
    grid-template-columns: repeat(7, 1fr);
    gap: 1px;
    padding: 0 0.25rem;
    flex-shrink: 0;
  }

  .day-header {
    text-align: center;
    font-size: 0.7rem;
    font-weight: 500;
    color: var(--theme-subtext, #a6adc8);
    padding: 0.15rem 0;
  }

  .weeks {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    padding: 0 0.25rem 0.25rem;
    gap: 1px;
  }

  .week-row {
    display: grid;
    grid-template-columns: repeat(7, 1fr);
    gap: 1px;
    flex: 1;
    min-height: 0;
  }

  .day-cell {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: flex-start;
    padding: 0.15rem;
    border-radius: 3px;
    min-height: 0;
    overflow: hidden;
  }

  .day-cell.out-of-month {
    opacity: 0.3;
  }

  .day-cell.is-today .day-number {
    background: var(--theme-accent, #89b4fa);
    color: var(--theme-base, #1e1e2e);
    border-radius: 50%;
    width: 1.4em;
    height: 1.4em;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .day-number {
    font-size: 0.75rem;
    font-weight: 500;
    color: var(--theme-text, #cdd6f4);
    line-height: 1;
  }

  .dots {
    display: flex;
    gap: 2px;
    align-items: center;
    margin-top: 2px;
    flex-wrap: wrap;
    justify-content: center;
  }

  .dot {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    flex-shrink: 0;
  }

  .extra {
    font-size: 0.55rem;
    color: var(--theme-subtext, #a6adc8);
    line-height: 1;
  }
</style>
