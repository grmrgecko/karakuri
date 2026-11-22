import { describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

// The API client reads localStorage when it is built, which the test runtime
// lacks, so the store runs against a stand-in for GET /dashboards. It pages
// the way apiutil.MakePageMeta does: `next` is the following page number and
// is absent on the last page. vi.mock is hoisted above the imports.
const pages = vi.hoisted(() => ({ rows: [] as { id: string }[], limits: [] as number[] }));
vi.mock('../api/client', () => ({
  api: {
    get: async (path: string) => {
      const query = new URLSearchParams(path.split('?')[1]);
      const page = Number(query.get('page') ?? 1);
      const limit = Number(query.get('limit') ?? 20);
      pages.limits.push(limit);
      const data = pages.rows.slice((page - 1) * limit, page * limit);
      return { data, total: pages.rows.length, limit, ...(page * limit < pages.rows.length ? { next: page + 1 } : {}) };
    },
  },
}));

import { dashboards } from './dashboards';

// The dashboard list page draws one tree with no pager, so the store has to
// follow `next` to the end. It read only the first page of 20, and the 21st
// dashboard never appeared in the list.
describe('dashboards.list', () => {
  it('loads every page of the list', async () => {
    // 205 rows is two full pages of the server's 100-row maximum and a part.
    pages.rows = Array.from({ length: 205 }, (_, i) => ({ id: `d${i}` }));
    await dashboards.list();
    const state = get(dashboards);
    expect(state.dashboards.map((d) => d.id)).toEqual(pages.rows.map((d) => d.id));
    expect(state.total).toBe(205);
    expect(pages.limits.every((limit) => limit <= 100)).toBe(true);
  });
});

// The dashboard command node's picker fetched one page of 100 on its own, so
// the 101st dashboard could not be picked. It reads the same paged walk as
// the list page, without replacing what the list page holds.
describe('dashboards.listAll', () => {
  it('returns every page without touching the store', async () => {
    pages.rows = Array.from({ length: 205 }, (_, i) => ({ id: `d${i}` }));
    await dashboards.list();
    pages.rows = [...pages.rows, { id: 'd205' }];
    const all = await dashboards.listAll();
    expect(all.map((d) => d.id)).toEqual(pages.rows.map((d) => d.id));
    expect(get(dashboards).dashboards, 'the list page keeps its own copy').toHaveLength(205);
  });
});
