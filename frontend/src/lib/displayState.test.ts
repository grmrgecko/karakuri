import { describe, expect, it, vi } from 'vitest';
import type { Condition, WidgetDisplay } from './stores/dashboards';
import { activeCue, compare, displayConditions, readValue } from './displayState';

// displayState reaches the API client through variable substitution, and the
// client reads localStorage when it is built, which the test runtime lacks.
// vi.mock is hoisted above the imports.
vi.mock('./api/client', () => ({ api: {} }));

// A state condition on a composite control with no attribute named compares
// the kind's primary attribute. Comparing the JSON of the whole object never
// matched a typed value, so such a condition was always false.
describe('readValue', () => {
  const record = { kind: 'location', value: { state: 'home', latitude: 41.88 } };
  const condition = { kind: 'state', source: 'control', controlId: 'x', operator: 'equals', value: 'home' } as Condition;

  it('compares the primary attribute when none is named', () => {
    const actual = readValue(condition, record, { location: 'state' });
    expect(actual).toBe('home');
    expect(compare(actual, 'equals', 'home')).toBe(true);
  });

  it('reads a named attribute as before', () => {
    expect(readValue({ ...condition, attribute: 'latitude' }, record, { location: 'state' })).toBe(41.88);
  });

  it('keeps a scalar value and an unknown kind as they are', () => {
    expect(readValue(condition, { kind: 'select', value: 'away' }, { location: 'state' })).toBe('away');
    expect(readValue(condition, record)).toEqual(record.value);
  });
});

// `above` and `below` are numeric. Number('') and Number([]) are both 0, so a
// template sensor whose state rendered empty, or an attribute holding an empty
// list, satisfied "below 10" and showed the placement.
describe('compare', () => {
  it.each<[unknown, string]>([
    ['', 'an empty state'],
    ['  ', 'a blank state'],
    [[], 'an empty list attribute'],
  ])('does not read %j as zero', (actual) => {
    expect(compare(actual, 'below', '10')).toBe(false);
    expect(compare(actual, 'above', '-1')).toBe(false);
  });

  it('compares numbers and numeric text', () => {
    expect(compare('21.5', 'above', '21')).toBe(true);
    expect(compare(0, 'below', '10')).toBe(true);
    expect(compare('unavailable', 'below', '10')).toBe(false);
  });
});

// A placement draws the first status cue whose conditions pass, judged by the
// same rules as visibility: a source that has not answered passes nothing, so
// a border does not flash on while the page loads.
describe('activeCue', () => {
  const door = { kind: 'state', source: 'control', controlId: 'door', operator: 'equals', value: 'open' } as Condition;
  const late = { kind: 'time', from: '22:00', to: '06:00' } as Condition;
  const display: WidgetDisplay = {
    cues: [
      { color: 'red', animation: 'blink', conditions: [door, late] },
      { color: 'orange', animation: 'breathe', conditions: [door] },
    ],
  };
  const open = { 'control:door': { kind: 'select', value: 'open' } };
  const evening = new Date(2026, 10, 21, 23, 0);
  const noon = new Date(2026, 10, 21, 12, 0);

  it('draws only the first cue when several pass', () => {
    expect(activeCue(display, open, evening)?.color).toBe('red');
  });

  it('falls through to a later cue when an earlier one fails', () => {
    expect(activeCue(display, open, noon)?.color).toBe('orange');
  });

  it('draws nothing before the source answers', () => {
    expect(activeCue(display, {}, evening)).toBeNull();
  });

  it('passes a cue with any of its conditions when match is any', () => {
    const any: WidgetDisplay = { cues: [{ color: 'red', match: 'any', conditions: [door, late] }] };
    expect(activeCue(any, {}, evening)?.color).toBe('red');
  });

  it('always draws a cue with no conditions', () => {
    expect(activeCue({ cues: [{ color: 'blue' }] }, {}, noon)?.color).toBe('blue');
  });

  it('lists the visibility conditions before the cues for following sources', () => {
    const shown = { ...door, controlId: 'lamp' } as Condition;
    expect(displayConditions({ ...display, conditions: [shown] }).map((c) => c.controlId ?? c.kind)).toEqual([
      'lamp',
      'door',
      'time',
      'door',
    ]);
  });
});
