import { describe, expect, it } from 'vitest';
import { hasReference, resolve } from '../../../plugins/core/widgets/shared/variables';
import type { BridgeVariables } from '../../../plugins/core/widgets/shared/variables';

// The host substitutes every format of a reference: ${name}, and ${name:text},
// :raw, :regex, :sqlstring, :from, and :to. The settings panels' guard knew
// only the first two, so resolve returned "${conn:raw}" as written and a
// picker went looking for a connection with that ID.
describe('widget variable references', () => {
  const state: BridgeVariables = {
    dashboardId: 'd',
    variables: [
      { name: 'conn', type: 'custom', options: [{ value: 'abc', label: 'Living room' }] },
      { name: 'range', type: 'time_range', options: [{ value: 'now-6h,now' }] },
    ],
    values: { conn: 'abc', range: 'now-1h,now' },
  };

  it.each([
    ['${conn}', 'abc'],
    ['${conn:text}', 'Living room'],
    ['${conn:raw}', 'abc'],
    ['${range:from}', 'now-1h'],
    ['${range:to}', 'now'],
  ])('resolves %s', (template, want) => {
    expect(hasReference(template)).toBe(true);
    expect(resolve(template, state)).toBe(want);
  });

  it('leaves text that is not a reference alone', () => {
    expect(hasReference('${conn:upper}')).toBe(false);
    expect(resolve('abc', state)).toBe('abc');
  });
});
