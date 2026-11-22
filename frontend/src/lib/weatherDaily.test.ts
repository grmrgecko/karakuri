import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { daysAfterToday, hasMoonData } from '../../../plugins/weather/widgets/weather/src/daily';

// The daily strip found tomorrow by adding 86400 seconds to today's midnight.
// On the day the clocks go forward that is 01:00 tomorrow, an hour past the
// local-midnight timestamp Open-Meteo gives tomorrow's entry, so tomorrow was
// dropped and the strip started a day late. New York goes forward on 8 March
// 2026.
describe('weather days after today on a 23-hour day', () => {
  beforeEach(() => {
    vi.stubEnv('TZ', 'America/New_York');
  });
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it('starts at tomorrow and leaves today out', () => {
    // One entry per day at local midnight, Sunday 8 March through Saturday.
    const daily = Array.from({ length: 7 }, (_, i) => ({
      dt: new Date(2026, 2, 8 + i).getTime() / 1000,
      temp_min: 1, temp_max: 10 + i, icon: '01d', pop: 0,
    }));
    const days = daysAfterToday(daily, new Date(2026, 2, 8, 12));
    expect(days.map((d) => new Date(d.dt * 1000).getDate())).toEqual([9, 10, 11, 12, 13, 14]);
  });
});

// Open-Meteo, the default provider, has no moon data, so the server sends
// moon_phase, moonrise, and moonset as 0. The astro layout read phase 0 as a
// new moon and named one every night. OpenWeatherMap reports a real new moon
// as phase 0 too, with rise and set times beside it.
describe('weather moon data', () => {
  const day = { dt: 1795000000, temp_min: 1, temp_max: 10, icon: '01d', pop: 0 };

  it('is absent when the provider sends every moon field as zero', () => {
    expect(hasMoonData({ ...day, moon_phase: 0, moonrise: 0, moonset: 0 })).toBe(false);
  });

  it('is present for a real new moon, which has rise and set times', () => {
    expect(hasMoonData({ ...day, moon_phase: 0, moonrise: 1795021000, moonset: 1795057000 })).toBe(true);
  });
});
