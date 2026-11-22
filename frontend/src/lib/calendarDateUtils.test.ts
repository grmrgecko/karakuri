import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { eventsForDate, relativeDay } from '../../../plugins/calendar/widgets/calendar/src/dateUtils';

// The calendar widget measured a day as 24 hours. A day the clocks change on
// is 23 or 25 hours long, so the day's end landed an hour into the next day
// or an hour short of midnight. New York changes on 8 March and 1 November
// 2026.
describe('calendar days across a clock change', () => {
  beforeEach(() => {
    vi.stubEnv('TZ', 'America/New_York');
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllEnvs();
  });

  // The 23-hour day ended at 01:00 on the 9th, so every all-day event on the
  // 9th was also drawn on the 8th.
  it('leaves the next day\'s all-day event off the day the clocks go forward', () => {
    const next = { title: 'Monday', start: '2026-03-09T00:00:00-04:00', end: '2026-03-10T00:00:00-04:00' };
    expect(eventsForDate([next], new Date(2026, 2, 8))).toEqual([]);
    expect(eventsForDate([next], new Date(2026, 2, 9))).toEqual([next]);
  });

  // The 25-hour day ended at 23:00, so an event in its last hour was on no
  // day of the grid at all.
  it('keeps a late event on the day the clocks go back', () => {
    const late = { title: 'Late', start: '2026-11-01T23:30:00-05:00', end: '2026-11-01T23:45:00-05:00' };
    expect(eventsForDate([late], new Date(2026, 10, 1))).toEqual([late]);
    expect(eventsForDate([late], new Date(2026, 10, 2))).toEqual([]);
  });

  // Midnight to midnight across the change is 23 hours, which is not one
  // whole day, so the agenda named the day instead of saying Tomorrow.
  it('calls the day after a clock change Tomorrow', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-03-08T12:00:00-04:00'));
    expect(relativeDay('2026-03-09T09:00:00-04:00')).toBe('Tomorrow');
    expect(relativeDay('2026-03-08T09:00:00-04:00')).toBe('Today');
    expect(relativeDay('2026-03-10T09:00:00-04:00')).toBe('Tue, Mar 10');
  });
});
