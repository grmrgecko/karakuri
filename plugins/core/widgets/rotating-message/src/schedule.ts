// Which lines a rotating message may show at a given moment. Every key of the
// lists object is one of four things: a time-of-day bucket, a date pattern, a
// five-field cron expression, or a weather condition word. The kind is read
// from the key's shape, so an author never declares it.

export interface Hours {
  morningStart: number;
  morningEnd: number;
  afternoonStart: number;
  afternoonEnd: number;
}

export type Lists = Record<string, string[]>;

const BUCKETS = ["morning", "afternoon", "evening", "anytime"];

// DATE_KEY is YYYY-MM-DD where any character may be "." to match any digit.
const DATE_KEY = /^[0-9.]{4}-[0-9.]{2}-[0-9.]{2}$/;

/** bucket names the time-of-day list an hour falls in; evening is every hour the other two leave. */
export function bucket(hour: number, hours: Hours): "morning" | "afternoon" | "evening" {
  if (hour >= hours.morningStart && hour < hours.morningEnd) return "morning";
  if (hour >= hours.afternoonStart && hour < hours.afternoonEnd) return "afternoon";
  return "evening";
}

/** isDateKey reports whether a key is a date pattern. */
export function isDateKey(key: string): boolean {
  return DATE_KEY.test(key);
}

/** dateMatches compares a date pattern against a local date, "." matching any digit. */
export function dateMatches(key: string, now: Date): boolean {
  const date = [
    String(now.getFullYear()).padStart(4, "0"),
    String(now.getMonth() + 1).padStart(2, "0"),
    String(now.getDate()).padStart(2, "0"),
  ].join("-");
  for (let i = 0; i < key.length; i++) {
    if (key[i] !== "." && key[i] !== date[i]) return false;
  }
  return key.length === date.length;
}

// ---- Cron ----

const MONTHS = ["jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"];
const DAYS = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"];

interface CronField {
  values: Set<number>;
  /** any is a field written as "*", which matters for the day fields. */
  any: boolean;
}

interface Cron {
  minute: CronField;
  hour: CronField;
  dom: CronField;
  month: CronField;
  dow: CronField;
}

const cronCache = new Map<string, Cron | null>();

/** isCronKey reports whether a key has the five fields of a cron expression. */
export function isCronKey(key: string): boolean {
  return key.trim().split(/\s+/).length === 5;
}

/**
 * cronMatches reports whether the minute holding now matches a cron key. As in
 * Vixie cron, when both day fields are restricted a day matching either one
 * matches. A key that does not parse never matches.
 */
export function cronMatches(key: string, now: Date): boolean {
  let cron = cronCache.get(key);
  if (cron === undefined) {
    cron = parseCron(key);
    cronCache.set(key, cron);
  }
  if (!cron) return false;
  if (!cron.minute.values.has(now.getMinutes())) return false;
  if (!cron.hour.values.has(now.getHours())) return false;
  if (!cron.month.values.has(now.getMonth() + 1)) return false;
  const domOk = cron.dom.values.has(now.getDate());
  const dowOk = cron.dow.values.has(now.getDay());
  if (cron.dom.any || cron.dow.any) return domOk && dowOk;
  return domOk || dowOk;
}

function parseCron(key: string): Cron | null {
  const parts = key.trim().toLowerCase().split(/\s+/);
  if (parts.length !== 5) return null;
  const minute = parseField(parts[0], 0, 59, null);
  const hour = parseField(parts[1], 0, 23, null);
  const dom = parseField(parts[2], 1, 31, null);
  const month = parseField(parts[3], 1, 12, MONTHS);
  const dow = parseField(parts[4], 0, 7, DAYS);
  if (!minute || !hour || !dom || !month || !dow) return null;
  // 7 is Sunday as well as 0.
  if (dow.values.has(7)) dow.values.add(0);
  return { minute, hour, dom, month, dow };
}

// parseValue reads a number or a name. A name's index is offset by the
// field's minimum, so January is 1 and Sunday is 0.
function parseValue(text: string, min: number, names: string[] | null): number | null {
  if (/^\d+$/.test(text)) return Number(text);
  const i = names ? names.indexOf(text) : -1;
  return i >= 0 ? i + min : null;
}

function parseField(text: string, min: number, max: number, names: string[] | null): CronField | null {
  const values = new Set<number>();
  let any = false;
  for (const part of text.split(",")) {
    if (part === "") return null;
    const [range, stepText, extra] = part.split("/");
    if (extra !== undefined) return null;
    let step = 1;
    if (stepText !== undefined) {
      if (!/^\d+$/.test(stepText) || Number(stepText) < 1) return null;
      step = Number(stepText);
    }
    let lo: number;
    let hi: number;
    if (range === "*") {
      lo = min;
      hi = max;
      if (stepText === undefined) any = true;
    } else if (range.includes("-")) {
      const [a, b, more] = range.split("-");
      if (more !== undefined) return null;
      const av = parseValue(a, min, names);
      const bv = parseValue(b, min, names);
      if (av === null || bv === null) return null;
      lo = av;
      hi = bv;
    } else {
      const v = parseValue(range, min, names);
      if (v === null) return null;
      lo = v;
      // "5/15" starts at 5 and steps to the end of the field.
      hi = stepText === undefined ? v : max;
    }
    if (lo < min || hi > max || lo > hi) return null;
    for (let v = lo; v <= hi; v += step) values.add(v);
  }
  return { values, any };
}

// ---- Selection ----

export interface Pool {
  lines: string[];
  /** special says a date or cron key matched. */
  special: boolean;
}

/**
 * pool gathers the lines that may show now: anytime, the time-of-day bucket,
 * every matching date and cron key, and the key naming the current weather.
 * With specialDayUnique set, a matching date or cron key shows its own lines
 * and nothing else.
 */
export function pool(lists: Lists, now: Date, hours: Hours, weather: string, specialDayUnique: boolean): Pool {
  const special: string[] = [];
  const weatherLines: string[] = [];
  const condition = weather.trim().toLowerCase();

  for (const [key, lines] of Object.entries(lists)) {
    if (!Array.isArray(lines) || BUCKETS.includes(key)) continue;
    if (isDateKey(key)) {
      if (dateMatches(key, now)) special.push(...lines);
    } else if (isCronKey(key)) {
      if (cronMatches(key, now)) special.push(...lines);
    } else if (condition && key.trim().toLowerCase() === condition) {
      weatherLines.push(...lines);
    }
  }

  const clean = (lines: unknown[]): string[] =>
    lines.filter((l): l is string => typeof l === "string" && l.trim() !== "");

  if (specialDayUnique && clean(special).length > 0) {
    return { lines: clean(special), special: true };
  }
  const listOf = (key: string): unknown[] => (Array.isArray(lists[key]) ? lists[key] : []);
  return {
    lines: clean([
      ...listOf("anytime"),
      ...listOf(bucket(now.getHours(), hours)),
      ...special,
      ...weatherLines,
    ]),
    special: special.length > 0,
  };
}

/** pick chooses a random line, a different one from current whenever the pool has another. */
export function pick(lines: string[], current: string): string {
  if (lines.length === 0) return "";
  const others = lines.filter((l) => l !== current);
  const from = others.length > 0 ? others : lines;
  return from[Math.floor(Math.random() * from.length)];
}

/** display turns a line's escaped "\n", left by single-quoted YAML, into the break it meant. */
export function display(line: string): string {
  return line.replace(/\\n/g, "\n");
}
