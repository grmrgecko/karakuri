import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { weekDates } from '../../../plugins/calendar/widgets/calendar/src/dateUtils';

// The weekly layout stepped from day to day in 24-hour jumps. A week holding
// a clock change has a 23 or 25 hour day, so a jump landed on the wrong date.
// New York changes on 8 March and 1 November 2026.
describe('calendar week across a clock change', () => {
  beforeEach(() => {
    vi.stubEnv('TZ', 'America/New_York');
  });
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  const dayNumbers = (dates: Date[]) => dates.map((d) => `${d.getMonth() + 1}/${d.getDate()}`);

  // Stepping back three 24-hour days from Wednesday the 11th crossed the
  // 23-hour Sunday and landed at 23:00 on Saturday the 7th.
  it('starts the week of Wednesday 11 March on Sunday 8 March', () => {
    const week = weekDates(new Date(2026, 2, 11, 12), 0);
    expect(dayNumbers(week)).toEqual(['3/8', '3/9', '3/10', '3/11', '3/12', '3/13', '3/14']);
  });

  // Sunday 1 November is 25 hours long, so 24 hours after its midnight was
  // still the 1st and the week ended on the 6th.
  it('gives seven different dates from Sunday 1 November', () => {
    const week = weekDates(new Date(2026, 10, 1, 12), 0);
    expect(dayNumbers(week)).toEqual(['11/1', '11/2', '11/3', '11/4', '11/5', '11/6', '11/7']);
  });

  it('moves a whole week back across the clock change with a Monday start', () => {
    const week = weekDates(new Date(2026, 2, 11, 12), 1, -1);
    expect(dayNumbers(week)).toEqual(['3/2', '3/3', '3/4', '3/5', '3/6', '3/7', '3/8']);
    expect(week.every((d) => d.getHours() === 0)).toBe(true);
  });
});
