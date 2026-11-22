import { describe, expect, it } from 'vitest';
import { resolveStart } from './homeStart';

// resolveStart encodes 045 FR-002: who lands where at `/`. A preference that
// names a dashboard the caller cannot see must fall back rather than open it,
// and only a user who cannot read flows is sent to their dashboards.
describe('resolveStart', () => {
  const cases: {
    name: string;
    pref: unknown;
    flows: boolean;
    ids: string[];
    want: ReturnType<typeof resolveStart>;
  }[] = [
    { name: 'a builder with no preference gets the overview', pref: undefined, flows: true, ids: ['a', 'b'], want: { kind: 'overview' } },
    { name: 'a chosen, visible dashboard wins over the overview', pref: 'b', flows: true, ids: ['a', 'b'], want: { kind: 'dashboard', id: 'b' } },
    { name: 'a chosen dashboard no longer visible falls back to the default', pref: 'gone', flows: true, ids: ['a'], want: { kind: 'overview' } },
    { name: 'a dashboard-only user with one dashboard lands on it', pref: undefined, flows: false, ids: ['a'], want: { kind: 'dashboard', id: 'a' } },
    { name: 'a dashboard-only user with several gets the list', pref: 'gone', flows: false, ids: ['a', 'b'], want: { kind: 'list' } },
    { name: 'an explicit overview choice holds without flow access', pref: 'overview', flows: false, ids: ['a'], want: { kind: 'overview' } },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(resolveStart(c.pref, c.flows, c.ids)).toEqual(c.want);
    });
  }
});
