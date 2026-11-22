import { parseAttr } from "../../shared/dashboardLive";

// CountdownEvent is one dated event. date is "YYYY-MM-DD" and time, when set,
// is "HH:MM", both in the viewer's local time.
export interface CountdownEvent {
  label: string;
  date: string;
  time?: string;
  /** yearly repeats the event on the same month and day every year. */
  yearly?: boolean;
}

export interface CountdownConfig {
  events: CountdownEvent[];
  /** layout is "single" for the nearest event drawn large, or "list". */
  layout: "single" | "list";
  /** units is "days", or "auto" for hours and minutes under a day. */
  units: "days" | "auto";
  /** maxItems caps the list layout. */
  maxItems: number;
  /** showPast keeps one-off events whose day is over, as "N days ago". */
  showPast: boolean;
  todayLabel: string;
}

export const DEFAULT_CONFIG: CountdownConfig = {
  events: [],
  layout: "single",
  units: "days",
  maxItems: 5,
  showPast: false,
  todayLabel: "Today",
};

const DATE_RE = /^(\d{4})-(\d{2})-(\d{2})$/;
const TIME_RE = /^(\d{2}):(\d{2})$/;

/** parseConfig reads the host's config attribute, falling back field by field. */
export function parseConfig(attr: string | Record<string, unknown>): CountdownConfig {
  const fields = parseAttr(attr);
  const events: CountdownEvent[] = [];
  if (Array.isArray(fields.events)) {
    for (const raw of fields.events) {
      if (!raw || typeof raw !== "object") continue;
      const e = raw as Record<string, unknown>;
      const event: CountdownEvent = {
        label: typeof e.label === "string" ? e.label : "",
        date: typeof e.date === "string" ? e.date : "",
      };
      if (typeof e.time === "string" && e.time !== "") event.time = e.time;
      if (e.yearly === true) event.yearly = true;
      events.push(event);
    }
  }
  const maxItems = Number(fields.maxItems);
  return {
    events,
    layout: fields.layout === "list" ? "list" : "single",
    units: fields.units === "auto" ? "auto" : "days",
    maxItems: Number.isInteger(maxItems) && maxItems >= 1 ? maxItems : DEFAULT_CONFIG.maxItems,
    showPast: fields.showPast === true,
    todayLabel: typeof fields.todayLabel === "string" && fields.todayLabel !== "" ? fields.todayLabel : DEFAULT_CONFIG.todayLabel,
  };
}

// Occurrence is an event placed on the calendar relative to now.
export interface Occurrence {
  event: CountdownEvent;
  /** at is the occurrence's start: its time, or local midnight without one. */
  at: Date;
  /** days is whole calendar days from today; negative once the day is over. */
  days: number;
  /** value and unit are the countdown as drawn, such as "12" and "days". */
  value: string;
  unit: string;
}

// dayNumber counts local calendar days, so a daylight saving change does not
// turn a 23 or 25 hour day into a rounding error.
function dayNumber(d: Date): number {
  return Math.floor(Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()) / 86_400_000);
}

// onDate builds a local date, holding February 29 to the 28th in years
// without one rather than letting it spill into March.
function onDate(year: number, month: number, day: number, hours: number, minutes: number): Date {
  const last = new Date(year, month + 1, 0).getDate();
  return new Date(year, month, Math.min(day, last), hours, minutes);
}

/**
 * occurrence places an event relative to now, or returns null when its date
 * does not parse. A yearly event stays on its day until the day is over and
 * then moves to next year's.
 */
export function occurrence(event: CountdownEvent, now: Date, cfg: CountdownConfig): Occurrence | null {
  const dm = DATE_RE.exec(event.date);
  if (!dm) return null;
  const year = Number(dm[1]);
  const month = Number(dm[2]) - 1;
  const day = Number(dm[3]);
  if (month < 0 || month > 11 || day < 1 || day > 31) return null;

  let hours = 0;
  let minutes = 0;
  const hasTime = !!event.time && TIME_RE.test(event.time);
  if (hasTime) {
    const tm = TIME_RE.exec(event.time as string)!;
    hours = Math.min(23, Number(tm[1]));
    minutes = Math.min(59, Number(tm[2]));
  }

  const today = dayNumber(now);
  let at = onDate(year, month, day, hours, minutes);
  if (event.yearly) {
    at = onDate(now.getFullYear(), month, day, hours, minutes);
    if (dayNumber(at) < today) at = onDate(now.getFullYear() + 1, month, day, hours, minutes);
  }

  const days = dayNumber(at) - today;
  let value: string;
  let unit: string;
  const remaining = at.getTime() - now.getTime();
  if (cfg.units === "auto" && hasTime && remaining > 0 && remaining < 86_400_000) {
    const totalMinutes = Math.ceil(remaining / 60_000);
    const h = Math.floor(totalMinutes / 60);
    const m = totalMinutes % 60;
    value = h > 0 ? `${h}h ${m}m` : `${m}m`;
    unit = "";
  } else if (days === 0) {
    value = cfg.todayLabel;
    unit = "";
  } else if (days > 0) {
    value = String(days);
    unit = days === 1 ? "day" : "days";
  } else {
    value = String(-days);
    unit = days === -1 ? "day ago" : "days ago";
  }
  return { event, at, days, value, unit };
}

/**
 * upcoming places every event and orders them for display: today and future
 * events soonest first, then, when showPast is set, past events most recent
 * first. Past one-off events are dropped otherwise.
 */
export function upcoming(cfg: CountdownConfig, now: Date): Occurrence[] {
  const future: Occurrence[] = [];
  const past: Occurrence[] = [];
  for (const event of cfg.events) {
    const o = occurrence(event, now, cfg);
    if (!o) continue;
    if (o.days >= 0) future.push(o);
    else if (cfg.showPast) past.push(o);
  }
  future.sort((a, b) => a.at.getTime() - b.at.getTime());
  past.sort((a, b) => b.at.getTime() - a.at.getTime());
  return [...future, ...past];
}
