import { writable } from 'svelte/store';
import { api } from '../api/client';

interface Widget {
  id: number;
  name: string;
  plugin_name: string;
  package_name: string;
  tag_name: string;
  config_tag_name: string;
  version: string;
  description: string;
  author: string;
  tags: string;
}

interface PaginatedResponse<T> {
  data: T[];
  total: number;
  next?: number;
  previous?: number;
  limit: number;
}

function createWidgetStore() {
  const { subscribe, update } = writable<{
    widgets: Widget[];
    loading: boolean;
    total: number;
  }>({
    widgets: [],
    loading: false,
    total: 0,
  });

  return {
    subscribe,

    // The API returns only the widgets whose plugin is loaded, so this is
    // exactly what can be placed on a dashboard right now.
    async list() {
      update((s) => ({ ...s, loading: true }));
      try {
        // The picker lists every widget with no pager, so the list is
        // followed to its last page.
        const all: Widget[] = [];
        let total = 0;
        let page = 1;
        for (;;) {
          const data = await api.get<PaginatedResponse<Widget>>(`/widgets?page=${page}&limit=100`);
          all.push(...(data.data || []));
          total = data.total;
          if (!data.next) break;
          page = data.next;
        }
        update((s) => ({
          ...s,
          widgets: all,
          total,
          loading: false,
        }));
      } catch {
        update((s) => ({ ...s, loading: false }));
      }
    },
  };
}

export const widgetRegistry = createWidgetStore();
export type { Widget };
