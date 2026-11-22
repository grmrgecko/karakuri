import { describe, expect, it, vi } from 'vitest';
import { parseRange, parseTime } from '../../../../plugins/core/widgets/chart/src/data/time';

// The chart widget resolves its range with its own copy of the grammar in
// pkg/tsdb/timerange.go, to draw the axis and size the query before the
// server answers. These cases are the ones where Date arithmetic and Go's
// disagree, so the widget's axis covered a different span than its data.

// iso resolves one range end in a zone and prints it, or null when refused.
const iso = (zone: string, expr: string, now: string, isEnd = false): string | null => {
  vi.stubEnv('TZ', zone);
  try {
    const t = parseTime(expr, Date.parse(now), isEnd);
    return t === null ? null : new Date(t).toISOString();
  } finally {
    vi.unstubAllEnvs();
  }
};

// Mirrors roundUnit in pkg/tsdb/timerange.go. 06:30Z on 1 November 2026 is
// 01:30 EST, the second pass of the hour New York repeats. Date setters
// rebuild the time from local fields and name the first pass, so "now/h,now"
// spanned ninety minutes where the server queried thirty.
describe('chart parseTime rounding in a repeated hour', () => {
  it.each([
    ['now/s', '2026-11-01T06:30:20.000Z'],
    ['now/m', '2026-11-01T06:30:00.000Z'],
    ['now/h', '2026-11-01T06:00:00.000Z'],
  ])('rounds %s down within the second pass', (expr, want) => {
    expect(iso('America/New_York', expr, '2026-11-01T06:30:20.500Z')).toBe(want);
  });
});

// Mirrors addMonths in pkg/tsdb/timerange.go, which keeps a month or year
// offset inside the target month. Date.setMonth carries a day the target
// month lacks into the month after: on 31 October "now-1M" was 1 October, so
// a "last month" chart drew this month's axis.
describe('chart parseTime month and year offsets', () => {
  it.each([
    ['2026-10-31T12:00:00Z', 'now-1M', false, '2026-09-30T12:00:00.000Z'],
    ['2026-10-31T12:00:00Z', 'now-1M/M', false, '2026-09-01T00:00:00.000Z'],
    ['2026-10-31T12:00:00Z', 'now-1M/M', true, '2026-09-30T23:59:59.999Z'],
    ['2028-02-29T12:00:00Z', 'now-1y', false, '2027-02-28T12:00:00.000Z'],
    ['2027-01-15T12:00:00Z', 'now-2M', false, '2026-11-15T12:00:00.000Z'],
  ])('at %s keeps %s in the target month', (now, expr, isEnd, want) => {
    expect(iso('UTC', expr, now, isEnd)).toBe(want);
  });
});

// Mirrors the absolute layouts tsdb.ParseTime accepts. Go reads a bare date
// as local midnight and refuses a day the month does not have or an hour of
// 24. Date.parse reads a bare date as UTC midnight and rolls the other two
// into the next day, so the axis sat hours away from the queried data.
describe('chart parseTime absolute times', () => {
  it('reads every layout the server does', () => {
    const now = '2026-06-01T00:00:00Z';
    expect(iso('America/New_York', '2024-02-29', now)).toBe('2024-02-29T05:00:00.000Z');
    expect(iso('America/New_York', '2026-01-01T10:00', now)).toBe('2026-01-01T15:00:00.000Z');
    expect(iso('America/New_York', '2026-01-01 10:00:30', now)).toBe('2026-01-01T15:00:30.000Z');
    expect(iso('America/New_York', '2026-01-01T10:00:00.250Z', now)).toBe('2026-01-01T10:00:00.250Z');
    expect(iso('America/New_York', '2026-01-01T10:00:00+02:00', now)).toBe('2026-01-01T08:00:00.000Z');
  });

  it.each([
    ['2026-02-30', 'a day February does not have'],
    ['2025-02-29', 'a leap day in a common year'],
    ['2026-01-01T24:00:00', 'an hour past 23'],
    ['2026-01-01T10:00+02:00', 'a zone offset without seconds'],
  ])('refuses %s', (expr) => {
    expect(iso('UTC', expr, '2026-06-01T00:00:00Z')).toBeNull();
    expect(parseRange(`${expr},2026-12-31`)).toBeNull();
  });
});
