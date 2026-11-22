// Time range values are "from,to", each end `now` with offsets and an
// optional rounding (now-6h, now-1d/d), Unix milliseconds, or an RFC 3339
// time. This is the viewer's copy of the grammar in pkg/tsdb/timerange.go;
// the server parses again before it runs a query, so the two must agree.

/** MAX_SPAN_MS is the widest range accepted, ten years. */
const MAX_SPAN_MS = 10 * 366 * 24 * 3600 * 1000;

/** DEFAULT_RANGE is a time range variable's value when it names none. */
export const DEFAULT_RANGE = 'now-6h,now';

/** DEFAULT_PRESETS are the quick ranges, matching models.DefaultTimePresets. */
export const DEFAULT_PRESETS = [
  { value: 'now-5m,now', label: 'Last 5 minutes' },
  { value: 'now-15m,now', label: 'Last 15 minutes' },
  { value: 'now-30m,now', label: 'Last 30 minutes' },
  { value: 'now-1h,now', label: 'Last 1 hour' },
  { value: 'now-3h,now', label: 'Last 3 hours' },
  { value: 'now-6h,now', label: 'Last 6 hours' },
  { value: 'now-12h,now', label: 'Last 12 hours' },
  { value: 'now-24h,now', label: 'Last 24 hours' },
  { value: 'now-2d,now', label: 'Last 2 days' },
  { value: 'now-7d,now', label: 'Last 7 days' },
  { value: 'now-30d,now', label: 'Last 30 days' },
  { value: 'now-90d,now', label: 'Last 90 days' },
  { value: 'now/d,now/d', label: 'Today' },
  { value: 'now-1d/d,now-1d/d', label: 'Yesterday' },
  { value: 'now/w,now/w', label: 'This week' },
  { value: 'now/M,now/M', label: 'This month' },
];

type Unit = 'ms' | 's' | 'm' | 'h' | 'd' | 'w' | 'M' | 'y';

function unitAt(s: string): [Unit | '', number] {
  if (s.startsWith('ms')) return ['ms', 2];
  const c = s[0];
  if (c && 'smhdwMy'.includes(c)) return [c as Unit, 1];
  return ['', 0];
}

function addUnit(t: Date, n: number, unit: Unit): Date {
  const d = new Date(t.getTime());
  switch (unit) {
    case 'ms': d.setTime(d.getTime() + n); break;
    case 's': d.setTime(d.getTime() + n * 1000); break;
    case 'm': d.setTime(d.getTime() + n * 60000); break;
    case 'h': d.setTime(d.getTime() + n * 3600000); break;
    case 'd': d.setDate(d.getDate() + n); break;
    case 'w': d.setDate(d.getDate() + 7 * n); break;
    case 'M': return addMonths(t, n);
    case 'y': return addMonths(t, 12 * n);
  }
  return d;
}

/**
 * addMonths moves a time by calendar months. A day the target month lacks
 * becomes its last day, as addMonths in pkg/tsdb/timerange.go has it:
 * setMonth would carry 31 September into October, and "now-1M/M" on the 31st
 * would then name the current month.
 */
function addMonths(t: Date, n: number): Date {
  const d = new Date(t.getTime());
  const day = d.getDate();
  // The first exists in every month, so the month is moved from there.
  d.setDate(1);
  d.setMonth(d.getMonth() + n);
  // Day 0 of the month after the target is the target's last day.
  const last = new Date(d.getFullYear(), d.getMonth() + 1, 0).getDate();
  d.setDate(Math.min(day, last));
  return d;
}

function roundUnit(t: Date, unit: Unit, isEnd: boolean): Date {
  const d = new Date(t.getTime());
  switch (unit) {
    // Seconds, minutes, and hours subtract the clock fields below them. A
    // Date setter rebuilds the time from its local fields, which names the
    // first pass of the hour a daylight saving change repeats.
    case 'ms':
    case 's': d.setTime(t.getTime() - t.getMilliseconds()); break;
    case 'm': d.setTime(t.getTime() - t.getSeconds() * 1000 - t.getMilliseconds()); break;
    case 'h': d.setTime(t.getTime() - t.getMinutes() * 60000 - t.getSeconds() * 1000 - t.getMilliseconds()); break;
    case 'd': d.setHours(0, 0, 0, 0); break;
    case 'w': d.setHours(0, 0, 0, 0); d.setDate(d.getDate() - ((d.getDay() + 6) % 7)); break;
    case 'M': d.setHours(0, 0, 0, 0); d.setDate(1); break;
    case 'y': d.setHours(0, 0, 0, 0); d.setMonth(0, 1); break;
  }
  if (!isEnd) return d;
  return new Date(addUnit(d, 1, unit).getTime() - 1);
}

/**
 * valid returns null for a time outside what a Date holds, 8.64e15 ms either
 * side of the epoch. Such a Date compares false with everything, so it would
 * pass the order and span checks in resolveRange.
 */
function valid(t: Date): Date | null {
  return Number.isNaN(t.getTime()) ? null : t;
}

/** parseTime resolves one range end, or returns null when it does not parse. */
export function parseTime(expr: string, now: Date = new Date(), isEnd = false): Date | null {
  const s = expr.trim();
  if (!s) return null;
  if (s.startsWith('now')) {
    let rest = s.slice(3);
    let t = now;
    while (rest) {
      const op = rest[0];
      rest = rest.slice(1);
      if (op === '+' || op === '-') {
        const m = /^[0-9]+/.exec(rest);
        if (!m) return null;
        const [unit, size] = unitAt(rest.slice(m[0].length));
        if (!unit) return null;
        t = addUnit(t, (op === '-' ? -1 : 1) * Number(m[0]), unit);
        rest = rest.slice(m[0].length + size);
      } else if (op === '/') {
        const [unit, size] = unitAt(rest);
        if (!unit) return null;
        t = roundUnit(t, unit, isEnd);
        rest = rest.slice(size);
      } else {
        return null;
      }
    }
    return valid(t);
  }
  if (/^[0-9]+$/.test(s)) return valid(new Date(Number(s)));
  return parseAbsolute(s);
}

const ABSOLUTE = /^(\d{4})-(\d{2})-(\d{2})(?:([T ])(\d{2}):(\d{2})(?::(\d{2})(\.\d+)?)?)?(Z|[+-]\d{2}:\d{2})?$/;

/**
 * parseAbsolute reads a date, a local date and time, or an RFC 3339 time, the
 * layouts tsdb.ParseTime accepts. The fields are checked here because Date
 * rolls a day the month does not have, or an hour of 24, into the next day,
 * where the server refuses both.
 */
function parseAbsolute(s: string): Date | null {
  const m = ABSOLUTE.exec(s);
  if (!m) return null;
  const [, year, month, day, separator, hour = '00', minute = '00', second, fraction = '', zone = ''] = m;
  // Only RFC 3339 carries a zone, and it needs the T and the seconds.
  if (zone && (separator !== 'T' || second === undefined)) return null;
  const y = Number(year);
  const mo = Number(month);
  const leap = (y % 4 === 0 && y % 100 !== 0) || y % 400 === 0;
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  if (mo < 1 || mo > 12 || Number(day) < 1 || Number(day) > days[mo - 1]) return null;
  if (Number(hour) > 23 || Number(minute) > 59 || Number(second ?? '0') > 59) return null;
  return valid(new Date(`${year}-${month}-${day}T${hour}:${minute}:${second ?? '00'}${fraction}${zone}`));
}

/** splitRange splits a "from,to" value. */
export function splitRange(value: string | null | undefined): [string, string] | null {
  if (!value) return null;
  const i = value.indexOf(',');
  if (i < 0) return null;
  const from = value.slice(0, i).trim();
  const to = value.slice(i + 1).trim();
  return from && to ? [from, to] : null;
}

/** resolveRange returns a value's ends as times, or null when it does not parse or is out of bounds. */
export function resolveRange(value: string | null | undefined, now: Date = new Date()): { from: Date; to: Date } | null {
  const parts = splitRange(value);
  if (!parts) return null;
  const from = parseTime(parts[0], now, false);
  const to = parseTime(parts[1], now, true);
  if (!from || !to || from.getTime() >= to.getTime()) return null;
  if (to.getTime() - from.getTime() > MAX_SPAN_MS) return null;
  return { from, to };
}

/** validRange reports whether a time range value parses. */
export function validRange(value: string | null | undefined): boolean {
  return resolveRange(value) !== null;
}

/**
 * shiftRange moves a range by its own span, back (-1) or forward (1), as
 * absolute times. An end rounded up to its unit is that unit's last
 * millisecond, so the period it closes is one millisecond longer than the
 * difference between the ends.
 */
export function shiftRange(value: string, direction: -1 | 1, now: Date = new Date()): string | null {
  const r = resolveRange(value, now);
  const parts = splitRange(value);
  if (!r || !parts) return null;
  const unrounded = parseTime(parts[1], now, false);
  const roundedUp = unrounded !== null && unrounded.getTime() !== r.to.getTime();
  const span = r.to.getTime() - r.from.getTime() + (roundedUp ? 1 : 0);
  return `${r.from.getTime() + direction * span},${r.to.getTime() + direction * span}`;
}

/** zoomOut doubles a range's span around its centre, never past now. */
export function zoomOut(value: string): string | null {
  const r = resolveRange(value);
  if (!r) return null;
  const span = r.to.getTime() - r.from.getTime();
  const to = Math.min(r.to.getTime() + span / 2, Date.now());
  return `${Math.round(to - 2 * span)},${Math.round(to)}`;
}

/** rangeText describes a value for a picker: a preset's label or the two ends. */
export function rangeText(value: string, presets: { value: string; label?: string }[] = DEFAULT_PRESETS): string {
  const preset = presets.find((p) => p.value === value);
  if (preset) return preset.label || preset.value;
  const parts = splitRange(value);
  if (!parts) return value;
  const fmt = (s: string) => (/^[0-9]+$/.test(s) ? new Date(Number(s)).toLocaleString() : s);
  return `${fmt(parts[0])} to ${fmt(parts[1])}`;
}

const AUTO_STEPS_MS = [1, 5, 10, 30, 60, 300, 600, 1800, 3600, 10800, 21600, 43200, 86400, 604800, 2592000].map((s) => s * 1000);

/** parseDuration reads 30s, 5m, 1h30m, 1d; a bare number is seconds. */
export function parseDuration(s: string): number | null {
  const t = s.trim();
  if (!t) return null;
  if (/^[0-9]+$/.test(t)) return Number(t) * 1000;
  const sizes: Record<string, number> = { ms: 1, s: 1000, m: 60000, h: 3600000, d: 86400000, w: 604800000, y: 31536000000 };
  let total = 0;
  // A bare leading or trailing point is a number to the server's parser.
  const re = /(\d+\.?\d*|\.\d+)(ms|s|m|h|d|w|y)/y;
  let pos = 0;
  while (pos < t.length) {
    re.lastIndex = pos;
    const m = re.exec(t);
    if (!m) return null;
    total += Number(m[1]) * sizes[m[2]];
    pos = re.lastIndex;
  }
  return total;
}

/** formatDuration writes a duration in its largest whole unit. */
export function formatDuration(ms: number): string {
  for (const [suffix, size] of [['w', 604800000], ['d', 86400000], ['h', 3600000], ['m', 60000], ['s', 1000]] as const) {
    if (ms % size === 0 && ms >= size) return `${ms / size}${suffix}`;
  }
  return `${ms}ms`;
}

/** autoInterval is an interval variable's auto value: the nearest step at or above span/steps, never below min. */
export function autoInterval(spanMs: number, steps = 30, minMs = 0): string {
  const want = Math.max(spanMs / (steps > 0 ? steps : 30), minMs);
  const step = AUTO_STEPS_MS.find((s) => s >= want) ?? AUTO_STEPS_MS[AUTO_STEPS_MS.length - 1];
  return formatDuration(step);
}
