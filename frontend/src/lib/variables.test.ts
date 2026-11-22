import { describe, expect, it, vi } from 'vitest';
import type { DashboardVariable } from './stores/dashboards';
import { chainedDependencies, defaultValue, initialValues } from './variables';

// variables reaches the API client to load a resolution, and the client reads
// localStorage when it is built, which the test runtime lacks. vi.mock is
// hoisted above the imports.
vi.mock('./api/client', () => ({ api: {} }));

// A viewer's pick re-resolves the query variables that refer to it, and the
// viewer then drops any of their values that are no longer offered. With
// site, host, and disk chained, a new site changes the hosts and so the disks,
// but only the direct dependent was returned: disk kept a value that belonged
// to the old host.
describe('chainedDependencies', () => {
  const query = (name: string, text: string, extra: Partial<DashboardVariable['query'] & object> = {}): DashboardVariable => ({
    name,
    type: 'query',
    query: { source: 'prometheus_query', connection_id: 'c', text, ...extra },
  });
  const variables: DashboardVariable[] = [
    { name: 'site', type: 'custom', options: [{ value: 'east' }, { value: 'west' }] },
    { name: 'range', type: 'time_range' },
    query('host', 'up{site="${site}"}'),
    query('disk', 'node_disk{host="${host}"}', { range_variable: 'range' }),
    query('other', 'up'),
  ];

  it('follows a chain to every query that depends on the pick', () => {
    const deps = chainedDependencies(variables);
    expect(deps.get('site')).toEqual(['host', 'disk']);
    expect(deps.get('host')).toEqual(['disk']);
  });

  it('keeps unrelated variables out', () => {
    const deps = chainedDependencies(variables);
    expect(deps.get('range')).toEqual(['disk']);
    expect(deps.has('disk')).toBe(false);
    expect(deps.has('other')).toBe(false);
  });

  // The server sizes an auto interval from its range variable and hands that
  // duration to later queries, so rate(x[${step}]) asks something else once
  // the range changes. Only queries were followed, and busy kept the options
  // of the old step.
  it('follows a range through an auto interval to the query that reads it', () => {
    const deps = chainedDependencies([
      { name: 'range', type: 'time_range' },
      { name: 'step', type: 'interval', auto: true, range_variable: 'range', options: [{ value: '1m' }] },
      { name: 'fixed', type: 'interval', range_variable: 'range', options: [{ value: '5m' }] },
      query('busy', 'topk(5, rate(http_requests_total[${step}]))'),
      query('idle', 'rate(http_requests_total[${fixed}])'),
    ]);
    expect(deps.get('range'), 'a new range resizes step, which busy reads').toEqual(['busy']);
    expect(deps.get('step'), 'an interval is never re-resolved itself').toEqual(['busy']);
    expect(deps.get('fixed'), 'an interval that is not on auto does not follow the range').toEqual(['idle']);
  });
});

// The server starts a variable on the option whose value is its default and
// otherwise on the first option (DashboardVariable.DefaultOption in
// pkg/models/dashboard_display.go); renders, validation, and a command that
// returns a variable to its default all use that. A query's default is free
// text, and the viewer also matched it against option labels, so a default of
// "Sam" opened viewers on Sam while renders of the same dashboard drew the
// first person.
describe('defaultValue', () => {
  const people: DashboardVariable = {
    name: 'person',
    type: 'query',
    query: { source: 'ha_entities', connection_id: 'c', pattern: 'person.*' },
    options: [
      { value: 'person.james', label: 'James' },
      { value: 'person.sam', label: 'Sam' },
    ],
  };

  it('starts on the first option when the default is only a label', () => {
    expect(defaultValue({ ...people, default: 'Sam' })).toBe('person.james');
    expect(defaultValue({ ...people, default: 'person.sam' }), 'a default that is a value is kept').toBe('person.sam');
  });

  it('still takes a label from the address', () => {
    const search = new URLSearchParams('var-person=sam');
    expect(initialValues([people], null, search).person).toBe('person.sam');
  });
});
