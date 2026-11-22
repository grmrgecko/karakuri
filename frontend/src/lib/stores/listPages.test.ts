import { describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

// The API client reads localStorage when it is built, which the test runtime
// lacks, so the stores run against a stand-in that pages the way
// apiutil.MakePageMeta does: `next` is the following page number and is
// absent on the last page, and a page holds at most 100 rows. vi.mock is
// hoisted above the imports.
const server = vi.hoisted(() => {
  const rows = Array.from({ length: 205 }, (_, i) => ({ id: `r${i}` }));
  const page = (limit: number, page: number) => {
    const size = limit < 1 || limit > 100 ? 20 : limit;
    const data = rows.slice((page - 1) * size, page * size);
    return { data, total: rows.length, limit: size, ...(page * size < rows.length ? { next: page + 1 } : {}) };
  };
  return { rows, page };
});
vi.mock('../api/client', () => ({
  api: {
    get: async (path: string) => {
      const query = new URLSearchParams(path.split('?')[1]);
      return server.page(Number(query.get('limit') ?? 20), Number(query.get('page') ?? 1));
    },
    users: { list: async (limit = 100, page = 1) => server.page(limit, page) },
    groups: { list: async (limit = 100, page = 1) => server.page(limit, page) },
    flows: { list: async (_isSubflow?: boolean, limit = 100, page = 1) => server.page(limit, page) },
  },
}));

import { usersStore } from './users';
import { groupsStore } from './groups';
import { subflowLibrary } from './subflows';
import { widgetRegistry } from './widgets';

// Each of these lists is drawn whole, with no pager: the users and groups
// tables in settings, the subflow palette, and the widget picker. The stores
// read only the first page of 100, so the 101st user, group, shared subflow,
// or widget never appeared and could not be picked.
describe('paged lists drawn without a pager', () => {
  const ids = server.rows.map((r) => r.id);
  const cases: { name: string; load: () => Promise<string[]> }[] = [
    { name: 'users', load: async () => ((await usersStore.list()) as unknown as { id: string }[]).map((r) => r.id) },
    { name: 'groups', load: async () => ((await groupsStore.list()) as unknown as { id: string }[]).map((r) => r.id) },
    {
      name: 'shared subflows',
      load: async () => {
        await subflowLibrary.load();
        return get(subflowLibrary).records.map((r) => r.id);
      },
    },
    {
      name: 'widgets',
      load: async () => {
        await widgetRegistry.list();
        return (get(widgetRegistry).widgets as unknown as { id: string }[]).map((r) => r.id);
      },
    },
  ];
  for (const c of cases) {
    it(`loads every page of ${c.name}`, async () => {
      expect(await c.load()).toEqual(ids);
    });
  }
});
