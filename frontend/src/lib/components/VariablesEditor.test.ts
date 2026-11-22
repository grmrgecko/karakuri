import { describe, expect, it, vi } from 'vitest';

// The editor reaches the API client, which reads localStorage when it is
// built; the test runtime lacks it. vi.mock is hoisted above the imports.
vi.mock('../api/client', () => ({ api: {} }));

import { draftOf, parseOptions, variableOf } from './VariablesEditor.svelte';
import type { DashboardVariable } from '../stores/dashboards';

// Saving the dashboard settings dialog writes every variable back from its
// draft, edited or not. Options are drafted as "value | label" lines and were
// read back by cutting at the first bar, so a value that holds a bar, such as
// a regex alternation, came back as value "web" with label "api | Front ends".
describe('variable option drafts', () => {
  it('returns stored options unchanged, including a value that holds a bar', () => {
    const stored: DashboardVariable = {
      name: 'job',
      options: [
        { value: 'web|api', label: 'Front ends' },
        { value: 'db|cache' },
        { value: 'db' },
        { value: 'east', label: 'East' },
      ],
    };
    expect(variableOf(draftOf(stored)).options, 'a save with no edits must not change the options').toEqual(stored.options);
  });

  it('reads typed lines as before', () => {
    expect(parseOptions('a|b\na | b\nc\nd |'), 'the first bar still separates a typed value from its label').toEqual([
      { value: 'a', label: 'b' },
      { value: 'a', label: 'b' },
      { value: 'c' },
      { value: 'd' },
    ]);
  });
});
