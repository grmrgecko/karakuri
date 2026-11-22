import { describe, expect, it } from 'vitest';
import { formatValue, formattedText } from '../../../../plugins/core/widgets/chart/src/data/units';

describe('chart units', () => {
  // SI units scale by 1000 and IEC units by 1024, and decimals come from
  // the scaled value's magnitude.
  it.each([
    { unit: 'watt', value: 1530, decimals: undefined, want: '1.53 kW', why: '1530 / 1000 = 1.53, auto decimals keep two' },
    { unit: 'kwatt', value: 0.5, decimals: undefined, want: '500 W', why: 'kwatt reads kilowatts, so 0.5 kW drops one prefix' },
    { unit: 'kwatth', value: 2500, decimals: 1, want: '2.5 MWh', why: '2500 kWh / 1000 = 2.5 MWh' },
    { unit: 'bytes', value: 1536, decimals: undefined, want: '1.50 KiB', why: '1536 / 1024 = 1.5, IEC prefix' },
    { unit: 'decbytes', value: 1536, decimals: 1, want: '1.5 kB', why: 'SI bytes divide by 1000' },
    { unit: 'short', value: 2_500_000, decimals: undefined, want: '2.50 Mil', why: 'short uses K, Mil, Bil' },
    { unit: 'percentunit', value: 0.256, decimals: 1, want: '25.6%', why: 'percentunit multiplies by 100' },
    { unit: 'celsius', value: 21.53, decimals: undefined, want: '21.5°C', why: '21.53 has one auto decimal' },
    { unit: 's', value: 90, decimals: 1, want: '1.5 min', why: '90 s = 1.5 minutes' },
    { unit: 'ms', value: 250, decimals: undefined, want: '250 ms', why: 'below a second stays in ms' },
    { unit: 'dtdurations', value: 3725, decimals: 2, want: '1 hour, 2 minutes', why: '3725 s = 1 h 2 min 5 s, kept to two units' },
    { unit: 'currencyEUR', value: 1200, decimals: 1, want: '€1.2K', why: 'currency prefixes the symbol and scales by K' },
    { unit: 'suffix: rpm', value: 3, decimals: 0, want: '3 rpm', why: 'custom suffix' },
    { unit: 'prefix:~', value: 3, decimals: 0, want: '~3', why: 'custom prefix' },
    { unit: 'widgets', value: 3, decimals: 0, want: '3 widgets', why: 'an unknown unit is a suffix' },
  ])('$unit formats $value as $want', ({ unit, value, decimals, want, why }) => {
    expect(formattedText(formatValue(value, unit, decimals)), why).toBe(want);
  });
});
