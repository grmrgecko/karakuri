import { describe, expect, it, vi } from 'vitest';
import { formatBytes } from './stores/history';
import { parseDuration, parseTime, resolveRange, shiftRange, validRange } from './timeRange';
import { paramPorts } from './utils/payloadPorts';
import { duration } from './utils/time';

// The history store builds the API client on import, and the client reads
// localStorage, which the test runtime lacks. vi.mock is hoisted.
vi.mock('./api/client', () => ({ api: {} }));

// The run panel shows this for every run. Flooring the minutes before
// rounding the seconds let the remainder round up to a full minute.
describe('duration', () => {
  const span = (ms: number) => duration('2026-01-01T00:00:00.000Z', new Date(Date.UTC(2026, 0, 1) + ms).toISOString());

  it('carries a remainder that rounds to 60 seconds into the minutes', () => {
    expect(span(119_600)).toBe('2m 0s');
    expect(span(59_960)).toBe('1m 0s');
  });

  it('leaves ordinary spans as they were', () => {
    expect(span(250)).toBe('250ms');
    expect(span(1_200)).toBe('1.2s');
    expect(span(192_000)).toBe('3m 12s');
  });
});

// The contract is null for text that is not a duration. A run of digits and
// dots such as "1.2.3" matched the token pattern and came back as NaN, which
// the callers' `?? 0` fallback does not catch.
describe('parseDuration', () => {
  it('returns null for malformed numbers', () => {
    expect(parseDuration('1.2.3s')).toBeNull();
    expect(parseDuration('.s')).toBeNull();
  });

  it('reads compound and fractional durations', () => {
    expect(parseDuration('1h30m')).toBe(5_400_000);
    expect(parseDuration('1.5s')).toBe(1500);
    expect(parseDuration('30')).toBe(30_000);
  });

  // tsdb.ParseDuration reads a number with strconv.ParseFloat, which takes a
  // bare leading or trailing point, so the server saves a minimum interval
  // of ".5m". The viewer read it as no duration and let auto resolve below
  // the minimum the server applies.
  it('reads a number with a bare leading or trailing point as the server does', () => {
    expect(parseDuration('.5m')).toBe(30_000);
    expect(parseDuration('1.m')).toBe(60_000);
    expect(parseDuration('1h.5m')).toBe(3_630_000);
  });
});

// The unit was picked before rounding, so one byte under a mebibyte printed
// as "1024.0 KiB".
describe('formatBytes', () => {
  it('moves to the next unit when rounding reaches 1024', () => {
    expect(formatBytes(1_048_575)).toBe('1.0 MiB');
    expect(formatBytes(1536)).toBe('1.5 KiB');
    expect(formatBytes(512)).toBe('512 B');
  });

  // The backup page lists archives with the same function, and those reach
  // sizes the forwarding buffers never do: 3 * 1024^4 bytes.
  it('names tebibytes', () => {
    expect(formatBytes(3 * 1024 ** 4)).toBe('3.0 TiB');
  });
});

// Mirrors tsdb.ParseRange in pkg/tsdb/timerange.go, which refuses an end it
// cannot place. A Date holds at most 8.64e15 ms, so a longer run of digits or
// a huge offset made an invalid Date. Every comparison with NaN is false, so
// the range passed the order and span checks and counted as valid.
describe('resolveRange', () => {
  const now = new Date('2026-01-01T00:00:00Z');

  it.each([
    ['9999999999999999,now', 'Unix milliseconds past what a Date holds'],
    ['now-999999999999y,now', 'an offset that leaves the Date range'],
  ])('refuses %s', (value) => {
    expect(resolveRange(value, now)).toBeNull();
    expect(validRange(value)).toBe(false);
  });

  it('still resolves relative and absolute ends', () => {
    expect(resolveRange('now-6h,now', now)).toEqual({ from: new Date('2025-12-31T18:00:00Z'), to: now });
    expect(resolveRange('1767225600000,1767229200000', now)?.to.getTime()).toBe(1767229200000);
  });
});

// Mirrors roundUnit in pkg/tsdb/timerange.go, which rounds to the second,
// minute, and hour by subtracting the clock fields below the unit. A Date
// setter rebuilds the time from its local fields, and in the hour a daylight
// saving change repeats that names the first pass, so rounding during the
// second pass moved the range end an hour into the past.
describe('parseTime rounding in a repeated hour', () => {
  // 06:30:20.500Z is 01:30:20.500 EST, the second 01:30 of the night the
  // clocks go back in New York.
  const now = new Date('2026-11-01T06:30:20.500Z');

  it.each([
    ['now/s', '2026-11-01T06:30:20.000Z'],
    ['now/m', '2026-11-01T06:30:00.000Z'],
    ['now/h', '2026-11-01T06:00:00.000Z'],
  ])('rounds %s down within the second pass', (expr, want) => {
    vi.stubEnv('TZ', 'America/New_York');
    try {
      expect(parseTime(expr, now)?.toISOString()).toBe(want);
    } finally {
      vi.unstubAllEnvs();
    }
  });
});

// Mirrors addMonths in pkg/tsdb/timerange.go, which keeps a month or year
// offset inside the target month. Date.setMonth carries a day the target month
// lacks into the month after: on 31 October "now-1M" was 1 October, so
// "now-1M/M" named October and the viewer sized a "last month" range, and the
// auto interval from it, on this month.
describe('parseTime month and year offsets', () => {
  const at = (iso: string, expr: string, isEnd = false) => {
    vi.stubEnv('TZ', 'UTC');
    try {
      return parseTime(expr, new Date(iso), isEnd)?.toISOString();
    } finally {
      vi.unstubAllEnvs();
    }
  };

  it.each([
    ['2026-10-31T12:00:00Z', 'now-1M', false, '2026-09-30T12:00:00.000Z'],
    ['2026-10-31T12:00:00Z', 'now-1M/M', false, '2026-09-01T00:00:00.000Z'],
    ['2026-10-31T12:00:00Z', 'now-1M/M', true, '2026-09-30T23:59:59.999Z'],
    ['2026-10-31T12:00:00Z', 'now+4M', false, '2027-02-28T12:00:00.000Z'],
    ['2028-02-29T12:00:00Z', 'now-1y', false, '2027-02-28T12:00:00.000Z'],
  ])('at %s keeps %s in the target month', (iso, expr, isEnd, want) => {
    expect(at(iso, expr, isEnd)).toBe(want);
  });

  it('keeps a day the target month has', () => {
    expect(at('2026-10-30T12:00:00Z', 'now-1M')).toBe('2026-09-30T12:00:00.000Z');
    expect(at('2027-01-15T12:00:00Z', 'now-2M')).toBe('2026-11-15T12:00:00.000Z');
    expect(at('2028-02-29T12:00:00Z', 'now-4y')).toBe('2024-02-29T12:00:00.000Z');
  });
});

// Mirrors the absolute layouts tsdb.ParseTime accepts in pkg/tsdb/timerange.go.
// Go refuses a day the month does not have, an hour of 24, and a zone offset
// on a time without seconds. Date rolls the first two into the next day and
// reads the third, so the picker accepted a range every query then failed on.
describe('parseTime absolute times', () => {
  it.each([
    ['2026-02-30', 'a day February does not have'],
    ['2026-04-31T00:00', 'a day April does not have'],
    ['2025-02-29', 'a leap day in a common year'],
    ['2026-01-01T24:00:00', 'an hour past 23'],
    ['2026-01-01T10:00+02:00', 'a zone offset without seconds'],
    ['2026-01-01 10:00:00Z', 'a zone after a space separator'],
  ])('refuses %s', (expr) => {
    expect(parseTime(expr)).toBeNull();
    expect(validRange(`${expr},2026-12-31`)).toBe(false);
  });

  it('reads every layout the server does', () => {
    vi.stubEnv('TZ', 'America/New_York');
    try {
      expect(parseTime('2024-02-29')?.toISOString()).toBe('2024-02-29T05:00:00.000Z');
      expect(parseTime('2026-01-01T10:00')?.toISOString()).toBe('2026-01-01T15:00:00.000Z');
      expect(parseTime('2026-01-01 10:00:30')?.toISOString()).toBe('2026-01-01T15:00:30.000Z');
      expect(parseTime('2026-01-01T10:00:00.250Z')?.toISOString()).toBe('2026-01-01T10:00:00.250Z');
      expect(parseTime('2026-01-01T10:00:00+02:00')?.toISOString()).toBe('2026-01-01T08:00:00.000Z');
    } finally {
      vi.unstubAllEnvs();
    }
  });
});

// A rounded end is the last millisecond of its unit, so "Today" spans one
// millisecond less than a day. Stepping by that difference put Earlier on
// 00:00:00.001 yesterday to 00:00:00.000 today, and each further step drifted
// another millisecond; the step is the whole period.
describe('shiftRange', () => {
  const now = new Date('2026-03-10T15:00:00Z');
  const day = 86_400_000;
  const today = Date.UTC(2026, 2, 10);
  const shift = (value: string, direction: -1 | 1) => {
    vi.stubEnv('TZ', 'UTC');
    try {
      return shiftRange(value, direction, now);
    } finally {
      vi.unstubAllEnvs();
    }
  };

  it('steps a rounded range by whole periods', () => {
    expect(shift('now/d,now/d', -1)).toBe(`${today - day},${today - 1}`);
    expect(shift('now/d,now/d', 1)).toBe(`${today + day},${today + 2 * day - 1}`);
  });

  it('steps an unrounded range by its span', () => {
    const hour = 3_600_000;
    expect(shift('now-6h,now', -1)).toBe(`${now.getTime() - 12 * hour},${now.getTime() - 6 * hour}`);
    expect(shift(`${today},${today + hour}`, 1)).toBe(`${today + hour},${today + 2 * hour}`);
  });
});

// Mirrors flow.ParamPorts in pkg/flow/params.go, which skips a field marked
// no_param even when the node lists it. Drawing the socket anyway let the
// canvas accept an edge the server rejects at save.
describe('paramPorts', () => {
  const spec = [
    { id: 'secret', type: 'string', label: 'Secret', no_param: true },
    { id: 'name', type: 'string', label: 'Name' },
  ];

  it('gives no socket to a no_param field', () => {
    const ports = paramPorts(spec as never, ['secret', 'name'], {});
    expect(ports.map((p) => p.id)).toEqual(['param:name']);
  });

  // flow.FieldPortType counts an ha-target field (the call service node's
  // areas, floors, devices, and labels) as a string a wire can supply. The
  // canvas left it out, so a graph exposing one drew no socket and its value
  // edge was marked as landing on a port that does not exist.
  it('gives a string socket to an exposed ha-target field', () => {
    const target = [{ id: 'area_id', type: 'ha-target', label: 'Areas' }];
    const ports = paramPorts(target as never, ['area_id'], {});
    expect(ports).toEqual([{ id: 'param:area_id', label: 'Areas', type: 'string', kind: 'param', optional: true }]);
  });
});
