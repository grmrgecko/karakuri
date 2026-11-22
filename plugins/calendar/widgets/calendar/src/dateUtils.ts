// Date formatting and grid calculation helpers for the calendar widget.

export interface CalendarEvent {
  start: string;
  end?: string;
  title?: string;
  color?: string;
  /** tag is the tag of the source the event came from. */
  tag?: string;
  [key: string]: unknown;
}

export interface DayCell {
  date: Date;
  day: number;
  inMonth: boolean;
  isToday: boolean;
}

const DAY_NAMES: string[] = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const MONTH_NAMES: string[] = [
  "January", "February", "March", "April", "May", "June",
  "July", "August", "September", "October", "November", "December",
];
const SHORT_MONTH_NAMES: string[] = [
  "Jan", "Feb", "Mar", "Apr", "May", "Jun",
  "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
];

// Format an ISO8601 string to "3:00 PM".
export function formatTime(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  let h = d.getHours();
  const m = d.getMinutes();
  const ampm = h >= 12 ? "PM" : "AM";
  h = h % 12 || 12;
  return `${h}:${String(m).padStart(2, "0")} ${ampm}`;
}

// Format an ISO8601 string to "Feb 12".
export function formatShortDate(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  return `${SHORT_MONTH_NAMES[d.getMonth()]} ${d.getDate()}`;
}

// Return a relative day label: "Today", "Tomorrow", or "Wed, Feb 12".
export function relativeDay(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const target = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  // Midnight to midnight is 23 or 25 hours across a clock change, so the
  // difference is rounded to whole days.
  const diff = Math.round((target.getTime() - today.getTime()) / 86400000);

  if (diff === 0) return "Today";
  if (diff === 1) return "Tomorrow";
  return `${DAY_NAMES[d.getDay()]}, ${SHORT_MONTH_NAMES[d.getMonth()]} ${d.getDate()}`;
}

// Return the seven local midnights of the week holding now, shifted by
// weekOffset weeks. Days are stepped by calendar date because a day the clocks
// change on is 23 or 25 hours long.
export function weekDates(now: Date, startOfWeek: number = 0, weekOffset: number = 0): Date[] {
  let offset = now.getDay() - startOfWeek;
  if (offset < 0) offset += 7;
  const first = now.getDate() - offset + weekOffset * 7;
  const dates: Date[] = [];
  for (let i = 0; i < 7; i++) {
    dates.push(new Date(now.getFullYear(), now.getMonth(), first + i));
  }
  return dates;
}

// Build a month grid for a given year/month/startOfWeek.
// Returns an array of week arrays, each containing {date, day, inMonth, isToday}.
export function getMonthGrid(year: number, month: number, startOfWeek: number = 0): DayCell[][] {
  const firstDay = new Date(year, month, 1);
  const lastDay = new Date(year, month + 1, 0);
  const today = new Date();
  const todayStr = `${today.getFullYear()}-${today.getMonth()}-${today.getDate()}`;

  // Day of week offset for the first day.
  let startOffset = firstDay.getDay() - startOfWeek;
  if (startOffset < 0) startOffset += 7;

  const weeks: DayCell[][] = [];
  let week: DayCell[] = [];

  // Fill leading days from previous month.
  const prevLast = new Date(year, month, 0);
  for (let i = startOffset - 1; i >= 0; i--) {
    const day = prevLast.getDate() - i;
    const date = new Date(year, month - 1, day);
    week.push({
      date,
      day,
      inMonth: false,
      isToday: false,
    });
  }

  // Fill current month days.
  for (let day = 1; day <= lastDay.getDate(); day++) {
    const date = new Date(year, month, day);
    const dateStr = `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`;
    week.push({
      date,
      day,
      inMonth: true,
      isToday: dateStr === todayStr,
    });
    if (week.length === 7) {
      weeks.push(week);
      week = [];
    }
  }

  // Fill trailing days from next month.
  if (week.length > 0) {
    let nextDay = 1;
    while (week.length < 7) {
      week.push({
        date: new Date(year, month + 1, nextDay),
        day: nextDay,
        inMonth: false,
        isToday: false,
      });
      nextDay++;
    }
    weeks.push(week);
  }

  return weeks;
}

// Filter events that fall on a specific date.
export function eventsForDate(events: CalendarEvent[], date: Date): CalendarEvent[] {
  if (!events || !date) return [];
  const dayStart = new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
  // The next local midnight, since a day the clocks change on is not 24
  // hours long.
  const dayEnd = new Date(date.getFullYear(), date.getMonth(), date.getDate() + 1).getTime();

  return events.filter((ev) => {
    const start = new Date(ev.start).getTime();
    const end = ev.end ? new Date(ev.end).getTime() : start;
    // Event overlaps with this day if it starts before day end and ends after day start.
    return start < dayEnd && end > dayStart;
  });
}

// Get the next upcoming event from now.
export function getNextEvent(events: CalendarEvent[]): CalendarEvent | null {
  if (!events || events.length === 0) return null;
  const now = Date.now();
  let next: CalendarEvent | null = null;
  for (const ev of events) {
    const start = new Date(ev.start).getTime();
    if (start >= now) {
      if (!next || start < new Date(next.start).getTime()) {
        next = ev;
      }
    }
  }
  return next;
}

// Convert an ISO time string to a percentage position within a 24-hour day.
export function timeToPercent(iso: string): number {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return 0;
  const hours = d.getHours() + d.getMinutes() / 60;
  return (hours / 24) * 100;
}

// Get the ordered day name headers for a given start-of-week.
export function getDayHeaders(startOfWeek: number = 0): string[] {
  const headers: string[] = [];
  for (let i = 0; i < 7; i++) {
    headers.push(DAY_NAMES[(startOfWeek + i) % 7]);
  }
  return headers;
}

// Get the full month name.
export function getMonthName(month: number): string {
  return MONTH_NAMES[month];
}

// Get the full day name.
export function getDayName(dayIndex: number): string {
  return DAY_NAMES[dayIndex];
}
