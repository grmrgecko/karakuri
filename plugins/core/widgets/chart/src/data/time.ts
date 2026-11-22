// Time ranges and durations in the grammar pkg/tsdb parses: now-6h, now/d,
// Unix milliseconds, RFC 3339, and durations such as 30s or 1h30m. The viewer
// needs them to draw the axis before the server answers, to zoom, and to pace
// its refresh.

import type { TimeRange } from "./types";

const DURATION_UNITS: Record<string, number> = {
  ms: 1,
  s: 1000,
  m: 60000,
  h: 3600000,
  d: 86400000,
  w: 604800000,
  y: 31536000000,
};

/** parseDuration reads a duration in milliseconds; a bare number is seconds. Null if it does not parse. */
export function parseDuration(text: string | undefined | null): number | null {
  const s = (text ?? "").trim();
  if (!s) return null;
  if (/^\d+$/.test(s)) return Number(s) * 1000;
  let total = 0;
  const re = /(\d+(?:\.\d+)?)(ms|s|m|h|d|w|y)/y;
  let at = 0;
  while (at < s.length) {
    re.lastIndex = at;
    const m = re.exec(s);
    if (!m) return null;
    total += Number(m[1]) * DURATION_UNITS[m[2]];
    at = re.lastIndex;
  }
  return total;
}

/** formatDuration writes milliseconds in the largest whole unit, as pkg/tsdb does. */
export function formatDuration(ms: number): string {
  if (ms <= 0) return "0s";
  for (const [suffix, size] of [["w", 604800000], ["d", 86400000], ["h", 3600000], ["m", 60000], ["s", 1000]] as const) {
    if (ms % size === 0) return `${ms / size}${suffix}`;
  }
  return `${Math.round(ms)}ms`;
}

function addUnit(t: Date, n: number, unit: string): Date {
  const d = new Date(t.getTime());
  switch (unit) {
    case "ms":
      d.setTime(d.getTime() + n);
      break;
    case "s":
      d.setTime(d.getTime() + n * 1000);
      break;
    case "m":
      d.setTime(d.getTime() + n * 60000);
      break;
    case "h":
      d.setTime(d.getTime() + n * 3600000);
      break;
    case "d":
      d.setDate(d.getDate() + n);
      break;
    case "w":
      d.setDate(d.getDate() + 7 * n);
      break;
    case "M":
      return addMonths(t, n);
    case "y":
      return addMonths(t, 12 * n);
  }
  return d;
}

// addMonths moves a time by calendar months. A day the target month lacks
// becomes its last day, as addMonths in pkg/tsdb/timerange.go has it:
// setMonth would carry 31 September into October, and "now-1M/M" on the 31st
// would then name the current month.
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

function roundUnit(t: Date, unit: string, isEnd: boolean): Date {
  const d = new Date(t.getTime());
  switch (unit) {
    // Seconds, minutes, and hours subtract the clock fields below them. A
    // Date setter rebuilds the time from its local fields, which names the
    // first pass of the hour a daylight saving change repeats.
    case "ms":
    case "s":
      d.setTime(t.getTime() - t.getMilliseconds());
      break;
    case "m":
      d.setTime(t.getTime() - t.getSeconds() * 1000 - t.getMilliseconds());
      break;
    case "h":
      d.setTime(t.getTime() - t.getMinutes() * 60000 - t.getSeconds() * 1000 - t.getMilliseconds());
      break;
    case "d":
      d.setHours(0, 0, 0, 0);
      break;
    case "w":
      d.setHours(0, 0, 0, 0);
      d.setDate(d.getDate() - ((d.getDay() + 6) % 7));
      break;
    case "M":
      d.setHours(0, 0, 0, 0);
      d.setDate(1);
      break;
    case "y":
      d.setHours(0, 0, 0, 0);
      d.setMonth(0, 1);
      break;
  }
  if (!isEnd) return d;
  return new Date(addUnit(d, 1, unit === "ms" ? "s" : unit).getTime() - 1);
}

/** parseTime resolves one range end to Unix milliseconds, or null. */
export function parseTime(expr: string, now: number, isEnd: boolean): number | null {
  const s = expr.trim();
  if (!s) return null;
  if (s.startsWith("now")) {
    let t = new Date(now);
    const re = /([+-])(\d+)(ms|[smhdwMy])|\/(ms|[smhdwMy])/y;
    let at = 3;
    while (at < s.length) {
      re.lastIndex = at;
      const m = re.exec(s);
      if (!m) return null;
      if (m[4]) t = roundUnit(t, m[4], isEnd);
      else t = addUnit(t, (m[1] === "-" ? -1 : 1) * Number(m[2]), m[3]);
      at = re.lastIndex;
    }
    return t.getTime();
  }
  if (/^\d+$/.test(s)) return Number(s);
  return parseAbsolute(s);
}

const ABSOLUTE = /^(\d{4})-(\d{2})-(\d{2})(?:([T ])(\d{2}):(\d{2})(?::(\d{2})(\.\d+)?)?)?(Z|[+-]\d{2}:\d{2})?$/;

// parseAbsolute reads a date, a local date and time, or an RFC 3339 time, the
// layouts tsdb.ParseTime accepts. The fields are checked here because Date
// rolls a day the month does not have, or an hour of 24, into the next day,
// where the server refuses both, and the time is rebuilt with its clock
// fields because Date.parse reads a bare date as UTC where the server reads
// it as local.
function parseAbsolute(s: string): number | null {
  const m = ABSOLUTE.exec(s);
  if (!m) return null;
  const [, year, month, day, separator, hour = "00", minute = "00", second, fraction = "", zone = ""] = m;
  // Only RFC 3339 carries a zone, and it needs the T and the seconds.
  if (zone && (separator !== "T" || second === undefined)) return null;
  const y = Number(year);
  const mo = Number(month);
  const leap = (y % 4 === 0 && y % 100 !== 0) || y % 400 === 0;
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  if (mo < 1 || mo > 12 || Number(day) < 1 || Number(day) > days[mo - 1]) return null;
  if (Number(hour) > 23 || Number(minute) > 59 || Number(second ?? "0") > 59) return null;
  const t = Date.parse(`${year}-${month}-${day}T${hour}:${minute}:${second ?? "00"}${fraction}${zone}`);
  return Number.isNaN(t) ? null : t;
}

/** splitRange splits a "from,to" range value. */
export function splitRange(value: string | undefined | null): { from: string; to: string } | null {
  const v = (value ?? "").trim();
  const i = v.indexOf(",");
  if (i < 0) return null;
  const from = v.slice(0, i).trim();
  const to = v.slice(i + 1).trim();
  return from && to ? { from, to } : null;
}

/** parseRange resolves a "from,to" value, or null if it does not parse. */
export function parseRange(value: string | undefined | null, now = Date.now()): TimeRange | null {
  const parts = splitRange(value);
  if (!parts) return null;
  const from = parseTime(parts.from, now, false);
  const to = parseTime(parts.to, now, true);
  return from !== null && to !== null && from < to ? { from, to } : null;
}

/**
 * chartRange resolves a chart's range the way the server does: a local zoom,
 * then the configured range, then the last 6 hours, with the relative time
 * replacing the span and the time shift moving it back.
 */
export function chartRange(
  opts: { zoom?: TimeRange | null; timeRange?: string; relativeTime?: string; timeShift?: string },
  now = Date.now(),
): TimeRange {
  let range = opts.zoom ?? parseRange(opts.timeRange, now) ?? { from: now - 6 * 3600000, to: now };
  if (!opts.zoom) {
    const relative = relativeSpan(opts.relativeTime);
    if (relative) range = { from: range.to - relative, to: range.to };
  }
  const shift = relativeSpan(opts.timeShift);
  if (shift) range = { from: range.from - shift, to: range.to - shift };
  return range;
}

// relativeSpan reads "1h" or "now-1h" as a span in milliseconds.
function relativeSpan(text: string | undefined): number | null {
  const s = (text ?? "").trim().replace(/^now-/, "");
  const ms = parseDuration(s);
  return ms && ms > 0 ? ms : null;
}

/** zoomOut doubles a range's span around its centre, never past now. */
export function zoomOut(range: TimeRange, now = Date.now()): TimeRange {
  const span = range.to - range.from;
  const centre = range.from + span / 2;
  let to = Math.round(centre + span);
  let from = Math.round(centre - span);
  if (to > now) {
    from -= to - now;
    to = now;
  }
  return { from, to };
}
