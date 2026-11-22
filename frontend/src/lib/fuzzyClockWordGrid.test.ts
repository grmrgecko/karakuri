import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { getWordCloudHighlights } from '../../../plugins/clocks/widgets/fuzzy-clock/src/fuzzyTime';

// The word clock rounds to the nearest five minutes, so 11:58 lights
// "TWELVE O'CLOCK". AM/PM came from the unrounded hour, so two minutes before
// noon the grid read "IT IS AM TWELVE O'CLOCK", which is midnight.
describe('word clock AM/PM across a rounded hour', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  const cases = [
    { name: '11:58 rounds up to twelve noon', hour: 11, minute: 58, word: 'TWELVE', lit: 'PM', dark: 'AM' },
    { name: '23:58 rounds up to twelve midnight', hour: 23, minute: 58, word: 'TWELVE', lit: 'AM', dark: 'PM' },
    { name: '11:02 rounds down and stays in the morning', hour: 11, minute: 2, word: 'ELEVEN', lit: 'AM', dark: 'PM' },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      vi.setSystemTime(new Date(Date.UTC(2026, 0, 15, tc.hour, tc.minute, 0)));
      const lit = getWordCloudHighlights('UTC');
      expect(lit.has(tc.word) && lit.has('OCLOCK')).toBe(true);
      expect(lit.has(tc.lit)).toBe(true);
      expect(lit.has(tc.dark)).toBe(false);
    });
  }
});
