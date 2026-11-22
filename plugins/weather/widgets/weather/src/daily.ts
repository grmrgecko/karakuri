// Day helpers for the daily forecast entries.

import type { DailyWeather } from './types.ts';

// Return the daily entries after today, at most limit of them. Tomorrow starts
// at the next local midnight, which is 23 or 25 hours after today's on a day
// the clocks change.
export function daysAfterToday(daily: DailyWeather[], now: Date, limit: number = 7): DailyWeather[] {
  const tomorrowStart = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1).getTime() / 1000;
  return daily.filter((d) => d.dt >= tomorrowStart).slice(0, limit);
}

// Report whether a daily entry carries moon data. A provider without any sends
// the phase, moonrise, and moonset as 0. A real new moon is also phase 0 but
// has a rise or set time.
export function hasMoonData(day: DailyWeather | undefined): boolean {
  if (!day) return false;
  return Boolean(day.moon_phase || day.moonrise || day.moonset);
}
