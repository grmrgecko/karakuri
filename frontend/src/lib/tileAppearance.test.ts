import { describe, expect, it } from 'vitest';
import { applyFormat } from '../../../plugins/core/widgets/shared/formats';
import { formatValue } from '../../../plugins/core/widgets/shared/controlKinds';
import { compareState, matchState, stateLabel } from '../../../plugins/core/widgets/shared/appearance';
import type { Resolved } from '../../../plugins/core/widgets/shared/entitySource';

// A binding's format turns a number into the reading a person expects. The
// examples are the spec's, so a change to any of them is a change of contract.
describe('applyFormat', () => {
  it('writes seconds as a clock', () => {
    expect(applyFormat(187, 'duration', undefined, '')).toBe('3:07');
    expect(applyFormat(11262, 'duration', undefined, '')).toBe('3:07:42');
    expect(applyFormat(90061, 'duration', undefined, '')).toBe('1d 1:01:01');
  });

  it('writes sizes and rates in 1024 steps with at most one decimal', () => {
    expect(applyFormat(1536, 'bytes', undefined, '')).toBe('1.5 KB');
    expect(applyFormat(0, 'bytes', undefined, '')).toBe('0 B');
    expect(applyFormat(5 * 1024 ** 3, 'bytes', undefined, '')).toBe('5 GB');
    expect(applyFormat(3670016, 'bytes_per_second', undefined, '')).toBe('3.5 MB/s');
  });

  it('writes percents and numbers with the binding precision and unit', () => {
    expect(applyFormat(12.5, 'percent', undefined, '')).toBe('12.5 %');
    expect(applyFormat('21.4083', 'number', 1, '°C')).toBe('21.4 °C');
  });

  it('leaves a reading that is not a number to the caller', () => {
    expect(applyFormat('on', 'bytes', undefined, '')).toBeNull();
    expect(applyFormat(12, undefined, undefined, '')).toBeNull();
    expect(formatValue('open', { format: 'bytes' })).toBe('open');
  });

  it('reaches every reading through formatValue', () => {
    expect(formatValue(187, { format: 'duration' })).toBe('3:07');
  });
});

// A binding's states are matched against its own reading, first match wins,
// and nothing matches before the source answers or while it is unavailable.
describe('matchState', () => {
  const reading = (value: unknown, extra: Partial<Resolved> = {}): Resolved => ({
    kind: 'text',
    options: {},
    readOnly: true,
    name: 'Sensor',
    value,
    ...extra,
  });
  const states = [
    { operator: 'above' as const, value: '30', label: 'Hot {value}' },
    { operator: 'equals' as const, value: 'on', label: 'On' },
    { label: 'Fallback' },
  ];

  it('applies the first state that matches', () => {
    expect(matchState(states, reading(31.2))?.label).toBe('Hot {value}');
    expect(matchState(states, reading(30))?.label).toBe('Fallback');
  });

  it('reads a switch as on or off and accepts true for on', () => {
    expect(matchState(states, reading(true, { kind: 'boolean' }))?.label).toBe('On');
    expect(compareState('on', 'equals', 'true')).toBe(true);
  });

  it('matches nothing while the source cannot answer', () => {
    expect(matchState(states, reading(31, { unavailable: true }))).toBeNull();
    expect(matchState(states, reading(31), false)).toBeNull();
  });

  it('fails a numeric comparison on a value that is not a number', () => {
    expect(compareState('warm', 'above', '30')).toBe(false);
  });

  it('fills a label with the formatted reading and the name', () => {
    expect(stateLabel('{name}: {value}', reading(42.3, { options: { format: 'percent' } }), 'CPU')).toBe('CPU: 42.3 %');
  });
});
