import { describe, expect, it } from 'vitest';
import type { Field, Frame, Thresholds, ValueMapping } from '../../../../plugins/core/widgets/chart/src/data/types';
import { frameFromSeries } from '../../../../plugins/core/widgets/chart/src/data/frames';
import { applyFieldConfig } from '../../../../plugins/core/widgets/chart/src/data/fieldConfig';
import { mapValue } from '../../../../plugins/core/widgets/chart/src/data/mappings';
import { activeThreshold } from '../../../../plugins/core/widgets/chart/src/data/colors';

function series(name: string, refId: string, unit?: string): Frame {
  return frameFromSeries({ name, unit, fields: [
    { name: 'time', type: 'time', values: [1000] },
    { name: 'value', type: 'number', values: [10] },
  ] }, refId);
}

function valueField(frame: Frame): Field {
  return frame.fields[1];
}

describe('chart field options', () => {
  // Precedence: panel defaults, then every matching override in
  // order with later ones winning. Spec 028 user story 5 sets unit and colour
  // on fields matching outdoor.* only. The unit the database
  // reports wins over the panel default, and overrides win over both.
  it('applies defaults, then matching overrides in order', () => {
    const frames = applyFieldConfig([series('outdoor temp', 'A', '°C'), series('indoor temp', 'B', '°C')], {
      defaults: { unit: 'fahrenheit', decimals: 1 },
      overrides: [
        { matcher: { id: 'byRegexp', options: 'outdoor.*' }, properties: [{ id: 'unit', value: 'kelvin' }, { id: 'color', value: { mode: 'fixed', fixedColor: 'blue' } }] },
        { matcher: { id: 'byFrameRefID', options: 'A' }, properties: [{ id: 'unit', value: 'celsius' }, { id: 'custom.lineWidth', value: 3 }] },
      ],
    });
    const outdoor = valueField(frames[0]).config;
    const indoor = valueField(frames[1]).config;
    expect(outdoor.unit, 'the later override for query A wins over the regex override').toBe('celsius');
    expect(outdoor.color).toEqual({ mode: 'fixed', fixedColor: 'blue' });
    expect(outdoor.custom?.lineWidth).toBe(3);
    expect(outdoor.decimals, 'defaults not overridden still apply').toBe(1);
    expect(indoor.unit, 'an unmatched field keeps the reported °C over the panel default').toBe('celsius');
    expect(indoor.color, 'overrides apply only to fields they match').toBeUndefined();
  });

  // Spec 028 user story 4: a door sensor's 1 and 0 map to Open and Closed;
  // ranges, regexes with groups, and special values cover the other forms.
  it.each([
    { value: 1, want: 'Open' },
    { value: '0', want: 'Closed' },
    { value: 27, want: 'Warm' },
    { value: 'sensor.kitchen', want: 'kitchen' },
    { value: null, want: 'Unknown' },
    { value: 99, want: undefined },
  ])('maps $value to $want', ({ value, want }) => {
    const mappings: ValueMapping[] = [
      { type: 'value', options: { '1': { text: 'Open', color: 'red' }, '0': { text: 'Closed', color: 'green' } } },
      { type: 'range', options: { from: 25, to: 30, result: { text: 'Warm' } } },
      { type: 'regex', options: { pattern: '^sensor\\.(.*)$', result: { text: '$1' } } },
      { type: 'special', options: { match: 'null', result: { text: 'Unknown' } } },
    ];
    expect(mapValue(value, mappings)?.text).toBe(want);
  });

  // Spec 028 user story 4: thresholds at 0 green, 25 orange, 30 red draw 27
  // orange. Percentage mode places steps along min to max instead: 52 of 0
  // to 200 is 26%, past 25; 60 is 30%, at the red step.
  it.each([
    { value: 27, mode: 'absolute', min: 0, max: 100, want: 'orange' },
    { value: -5, mode: 'absolute', min: 0, max: 100, want: 'green' },
    { value: 30, mode: 'absolute', min: 0, max: 100, want: 'red' },
    { value: 52, mode: 'percentage', min: 0, max: 200, want: 'orange' },
    { value: 60, mode: 'percentage', min: 0, max: 200, want: 'red' },
  ])('$value in $mode mode is $want', ({ value, mode, min, max, want }) => {
    const thresholds: Thresholds = { mode: mode as Thresholds['mode'], steps: [
      { value: null, color: 'green' }, { value: 30, color: 'red' }, { value: 25, color: 'orange' },
    ] };
    expect(activeThreshold(value, thresholds, min, max).color).toBe(want);
  });
});
