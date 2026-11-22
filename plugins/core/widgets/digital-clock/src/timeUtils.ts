// Shared time parsing utilities for the digital clock widget.
// All functions use Intl.DateTimeFormat with timezone options so the widget
// can display accurate time for any IANA timezone without manual offset math.

export interface TimeComponents {
  hours: string;
  minutes: string;
  seconds: string;
  period: string;
  dayName: string;
  monthName: string;
  dayOfMonth: string;
  year: string;
  dateString: string;
}

// Extracts formatted time and date components for a given timezone.
export function getTimeComponents(timezone: string, format: string): TimeComponents {
  const now = new Date();
  const hour12 = format === "12h";

  // Extract individual numeric parts via formatToParts.
  const timeParts = new Intl.DateTimeFormat("en-US", {
    timeZone: timezone || undefined,
    hour: "numeric",
    minute: "2-digit",
    second: "2-digit",
    hour12,
  }).formatToParts(now);

  let hours = "";
  let minutes = "";
  let seconds = "";
  let period = "";

  for (const part of timeParts) {
    if (part.type === "hour") hours = part.value;
    if (part.type === "minute") minutes = part.value;
    if (part.type === "second") seconds = part.value;
    if (part.type === "dayPeriod") period = part.value;
  }

  // Ensure two-digit formatting for 24h hours.
  if (!hour12 && hours.length === 1) {
    hours = "0" + hours;
  }

  const dateParts = new Intl.DateTimeFormat("en-US", {
    timeZone: timezone || undefined,
    weekday: "long",
    year: "numeric",
    month: "long",
    day: "numeric",
  }).formatToParts(now);

  let dayName = "";
  let monthName = "";
  let dayOfMonth = "";
  let year = "";

  for (const part of dateParts) {
    if (part.type === "weekday") dayName = part.value;
    if (part.type === "month") monthName = part.value;
    if (part.type === "day") dayOfMonth = part.value;
    if (part.type === "year") year = part.value;
  }

  const dateString = `${monthName} ${dayOfMonth}, ${year}`;

  return {
    hours,
    minutes,
    seconds,
    period,
    dayName,
    monthName,
    dayOfMonth,
    year,
    dateString,
  };
}

// Returns a value between 0 and 1 representing how much of the day has passed
// in the given timezone. 0 corresponds to midnight, 1 to 23:59:59.
export function getDayProgress(timezone: string): number {
  const now = new Date();

  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: timezone || undefined,
    hour: "numeric",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).formatToParts(now);

  let h = 0;
  let m = 0;
  let s = 0;

  for (const part of parts) {
    if (part.type === "hour") h = parseInt(part.value, 10);
    if (part.type === "minute") m = parseInt(part.value, 10);
    if (part.type === "second") s = parseInt(part.value, 10);
  }

  // Handle the edge case where hour 24 is returned instead of 0.
  if (h === 24) h = 0;

  const totalSeconds = h * 3600 + m * 60 + s;
  const dayTotal = 24 * 3600;

  return totalSeconds / dayTotal;
}
