import { describe, expect, it } from 'vitest';
import { formatValue, formattedText, unitFromSource } from '../../../../plugins/core/widgets/chart/src/data/units';

// A source reports units the way a sensor spells them, and some spellings are
// also unit IDs. "m" from a sensor is metres, but the unit ID "m" is minutes,
// and the ID was matched first, so a 2.5 m reading was drawn as 2.5 min.
describe('unitFromSource', () => {
  it('reads a source unit of m as metres and min as minutes', () => {
    expect(unitFromSource('m')).toBe('lengthm');
    expect(formattedText(formatValue(2.5, unitFromSource('m')))).toBe('2.50 m');
    expect(unitFromSource('min')).toBe('m');
  });

  it('keeps a unit ID no source spelling claims, and suffixes an unknown unit', () => {
    expect(unitFromSource('celsius')).toBe('celsius');
    expect(unitFromSource('rpm')).toBe('suffix: rpm');
  });
});
