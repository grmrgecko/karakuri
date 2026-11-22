import { describe, expect, it } from 'vitest';
import type { Frame } from '../../../../plugins/core/widgets/chart/src/data/types';
import { framesFromResponse, displayName } from '../../../../plugins/core/widgets/chart/src/data/frames';
import { applyTransformations, parseDate } from '../../../../plugins/core/widgets/chart/src/data/transformations';

// twoSeries builds the data route's answer for two queries the way pkg/tsdb
// serializes series: time in Unix milliseconds, then one value column.
function twoSeries(): Frame[] {
  return framesFromResponse({
    results: [
      {
        ref_id: 'A',
        result: {
          series: [{ name: 'indoor', fields: [
            { name: 'time', type: 'time', values: [1000, 2000, 3000] },
            { name: 'value', type: 'number', values: [21, 22, 23] },
          ] }],
        },
      },
      {
        ref_id: 'B',
        result: {
          series: [{ name: 'outdoor', fields: [
            { name: 'time', type: 'time', values: [2000, 3000, 4000] },
            { name: 'value', type: 'number', values: [5, 7, 9] },
          ] }],
        },
      },
    ],
  }).frames;
}

function column(frame: Frame, name: string): unknown[] | undefined {
  return frame.fields.find((f) => displayName(f, frame) === name)?.values;
}

describe('chart transformations', () => {
  // Spec 028 user story 5: a join by time followed by a binary calculation
  // holds the difference wherever both series have a value. Each joined field
  // keeps its series' name, which the calculation refers to.
  it.each([
    { mode: 'outer', times: [1000, 2000, 3000, 4000], diff: [null, 22 - 5, 23 - 7, null] },
    { mode: 'inner', times: [2000, 3000], diff: [22 - 5, 23 - 7] },
  ])('$mode join then indoor - outdoor', ({ mode, times, diff }) => {
    const [out] = applyTransformations(twoSeries(), [
      { id: 'joinByField', options: { mode } },
      { id: 'calculateField', options: { mode: 'binary', binary: { left: 'indoor', operator: '-', right: 'outdoor' }, alias: 'delta' } },
    ]);
    expect(column(out, 'Time'), 'the join field is the sorted union or intersection of times').toEqual(times);
    expect(column(out, 'delta'), 'a missing operand leaves the calculated cell empty').toEqual(diff);
  });

  // Reduce in its default series-to-rows mode makes one row per series with
  // one column per calculation, which tables and bar charts draw directly.
  it('reduces each series to a row of calculations', () => {
    const [out] = applyTransformations(twoSeries(), [{ id: 'reduce', options: { reducers: ['max', 'mean'] } }]);
    expect(column(out, 'Field')).toEqual(['indoor', 'outdoor']);
    expect(column(out, 'Max')).toEqual([23, 9]);
    expect(column(out, 'Mean'), 'mean of 21, 22, 23 and of 5, 7, 9').toEqual([22, 7]);
  });

  // Disabled steps are skipped and steps naming fields the data lacks leave
  // it unchanged, so a stale transformation never blanks a chart.
  it('skips disabled steps and unknown fields', () => {
    const out = applyTransformations(twoSeries(), [
      { id: 'renameByRegex', options: { regex: '.*', renamePattern: 'x' }, disabled: true },
      { id: 'sortBy', options: { sort: [{ field: 'missing', desc: true }] } },
    ]);
    expect(out.map((f) => displayName(f.fields[1], f))).toEqual(['indoor', 'outdoor']);
  });
});

describe('convert field type date formats', () => {
  // Convert field type reads text as time with moment.js tokens
  // (YYYY MM DD HH mm ss SSS, and X for Unix seconds), in local time.
  it.each([
    { text: '30/10/2026 18:05', format: 'DD/MM/YYYY HH:mm', want: new Date(2026, 9, 30, 18, 5).getTime() },
    { text: '2026.01.02 03:04:05.006', format: 'YYYY.MM.DD HH:mm:ss.SSS', want: new Date(2026, 0, 2, 3, 4, 5, 6).getTime() },
    { text: '1793400000', format: 'X', want: 1793400000000 },
    { text: '30-10-2026', format: 'DD/MM/YYYY', want: null },
  ])('reads $text as $format', ({ text, format, want }) => {
    expect(parseDate(text, format)).toBe(want);
  });
});
