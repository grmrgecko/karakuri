import { writable } from 'svelte/store';
import { api } from '../api/client';

interface DashboardWidget {
  id: string;
  dashboard_id: string;
  /** Rises with each config replacement; sent back to refuse overwriting a newer one. */
  config_revision?: number;
  widget_id: number;
  /** Owning section; absent for the sectionless region above the first section. */
  section_id?: string | null;
  x: number;
  y: number;
  width: number;
  height: number;
  config: string;
  widget?: Widget;
  /** Page the placement is shown on; absent shows it on every page. */
  page_id?: string | null;
  /** Named screen region in the region layout. */
  region?: string;
  display?: WidgetDisplay | null;
}

/** Condition is one rule a placement's visibility depends on. */
interface Condition {
  kind: 'state' | 'time' | 'variable';
  /** Variable name a variable condition compares. */
  variable?: string;
  source?: 'control' | 'ha' | 'mqtt';
  controlId?: string;
  connectionId?: string;
  entityId?: string;
  deviceId?: string;
  /** Attribute or device field to compare; empty compares the state. */
  attribute?: string;
  operator?: 'equals' | 'not_equals' | 'above' | 'below' | 'contains';
  value?: string;
  from?: string;
  to?: string;
  /** Weekdays, 0 for Sunday; empty means every day. */
  days?: number[];
}

/** WidgetDisplay holds a placement's name, visibility, and animations. */
interface WidgetDisplay {
  name?: string;
  hidden?: boolean;
  match?: 'all' | 'any';
  conditions?: Condition[];
  animate_in?: string;
  animate_out?: string;
  duration?: number;
  style?: TileStyle;
  cues?: StatusCue[];
}

/** TileStyle overrides a placement tile's theme look; empty fields keep the theme. */
interface TileStyle {
  /** A CSS color, or transparent to drop the tile face, border, and shadow. */
  background?: string;
  border_color?: string;
  border_width?: number;
  /** Corner radius in pixels; unset keeps the theme's, 0 is square. */
  radius?: number;
  text_color?: string;
  accent_color?: string;
}

/** CueAnimation is the motion a status cue's border takes. */
type CueAnimation = 'static' | 'pulse' | 'blink' | 'breathe' | 'comet' | 'ants';

/** StatusCue is a border drawn while its conditions pass. */
interface StatusCue {
  match?: 'all' | 'any';
  conditions?: Condition[];
  color?: string;
  animation?: CueAnimation;
}

/** DashboardDisplay holds the always-on screen settings. */
interface DashboardDisplay {
  page_interval?: number;
  night_dim?: { from: string; to: string; level: number } | null;
  idle_dim?: { after: number; level: number } | null;
  pixel_shift?: number;
  variables?: DashboardVariable[];
}

/** VariableOption is one value a dashboard variable may take. */
interface VariableOption {
  value: string;
  label?: string;
}

/** VariableQuery is where a query variable's options come from. */
interface VariableQuery {
  source:
    | 'ha_entities'
    | 'ha_select'
    | 'prometheus_label_values'
    | 'prometheus_metrics'
    | 'prometheus_query'
    | 'influx_query'
    | 'sql_query';
  connection_id: string;
  /** Prometheus label whose values become options. */
  label?: string;
  /** Prometheus series selector narrowing lookups. */
  match?: string;
  /** Query text for prometheus_query, influx_query, and sql_query; may reference earlier variables. */
  text?: string;
  /** influx_query language: influxql, flux, or sql. */
  language?: string;
  /** Time range variable whose value the query's time macros use. */
  range_variable?: string;
  /** Entity ID pattern for ha_entities, with * for any run of characters. */
  pattern?: string;
  /** input_select or select entity for ha_select. */
  entity_id?: string;
  regex?: string;
  sort?: '' | 'asc' | 'desc';
  /** For ha_select, the dashboard-wide value is the entity's state. */
  follow?: boolean;
}

/** DashboardVariable is a named choice placements refer to as ${name}. */
interface DashboardVariable {
  name: string;
  label?: string;
  description?: string;
  type?: 'custom' | 'constant' | 'query' | 'time_range' | 'interval';
  display?: '' | 'above' | 'label_hidden' | 'menu' | 'hidden';
  /** A custom variable's values, or any variable's once resolved. */
  options?: VariableOption[];
  default?: string;
  /** A constant's value. */
  value?: string;
  query?: VariableQuery | null;
  /** Interval: offer auto, sized from range_variable. */
  auto?: boolean;
  /** Interval: how many steps auto divides the range into. */
  steps?: number;
  /** Interval: the smallest value auto resolves to. */
  min_interval?: string;
  /** Interval: the time range variable auto is sized from. */
  range_variable?: string;
}

/** ResolvedVariables is what GET /dashboards/{id}/variables returns. */
interface ResolvedVariables {
  variables: DashboardVariable[];
  followed: Record<string, string>;
  follow_map: Record<string, Record<string, string>>;
}

/** DashboardPage is one page placements can be shown on. */
interface DashboardPage {
  id: string;
  dashboard_id: string;
  title: string;
  sort_order: number;
}

/** LiveContent is a value a flow pushed into one placement. */
interface LiveContent {
  value: unknown;
  format?: 'text' | 'markdown' | 'json';
  html?: string;
  updated_at: string;
}

/** LiveAlert is one message shown over a dashboard. */
interface LiveAlert {
  id: string;
  title?: string;
  message?: string;
  level: 'info' | 'success' | 'warning' | 'error';
  style: 'toast' | 'fullscreen';
  /** speak asks viewers to read the alert aloud when it arrives. */
  speak?: boolean;
  created_at: string;
  expires_at?: string;
}

/** LiveState is a dashboard's runtime display state, held by the server. */
interface LiveState {
  seq: number;
  locks: Record<string, string[]>;
  page?: string;
  alerts: LiveAlert[];
  dim?: number | null;
  content: Record<string, LiveContent>;
  /** Dashboard-wide variable values; a variable absent here is at its default. */
  variables?: Record<string, string>;
}

interface Widget {
  id: number;
  name: string;
  package_name: string;
  tag_name: string;
  config_tag_name: string;
  version: string;
  description: string;
  default_provider?: string;
  entry_point?: string;
}

interface Dashboard {
  id: string;
  name: string;
  /** Rises with each display replacement. */
  display_revision?: number;
  slug: string;
  description: string;
  owner_id: string;
  is_public: boolean;
  public_control: boolean;
  /** Whether this viewer may operate what the dashboard shows. */
  can_operate?: boolean;
  group_id: number | null;
  sort_order: number;
  group?: {
    id: number;
    name: string;
    description: string;
    owner_id: string;
    owner?: { id: string; username: string; email: string };
  };
  theme_id: number | null;
  /** fit scales the grid to the viewport; scroll fixes the row height and overflows. */
  layout_mode: 'fit' | 'scroll' | 'regions';
  grid_rows: number;
  grid_cols: number;
  /** Pixel height of one grid row; scroll layout only. */
  row_height: number;
  padding: number;
  gap: number;
  widget_padding: number;
  /** Version viewers are served; 0 when the dashboard has never been deployed. */
  deployed_version: number;
  /** Whether the draft differs from that version. Only filled in on single-dashboard reads. */
  has_draft_changes?: boolean;
  widgets?: DashboardWidget[];
  sections?: DashboardSection[];
  pages?: DashboardPage[];
  display?: DashboardDisplay | null;
}

/** DashboardSection is one collapsible band of a scrolling dashboard. */
interface DashboardSection {
  id: string;
  dashboard_id: string;
  title: string;
  sort_order: number;
  /** The authored default; a viewer's own folds are not stored. */
  collapsed: boolean;
}

/** DashboardVersion is one frozen layout, listed in the editor's history. */
interface DashboardVersion {
  id: number;
  dashboard_id: string;
  version: number;
  comment: string;
  widget_count: number;
  created_at: string;
  created_by?: { id: string; username: string };
}

interface DashboardAccess {
  id: number;
  dashboard_id: string;
  user_id?: string;
  user?: { id: string; username: string };
  group_id?: number;
  group?: { id: number; name: string };
  permission: string;
}

interface PaginatedResponse<T> {
  data: T[];
  total: number;
  next?: number;
  previous?: number;
  limit: number;
}

// pageLimit is the server's maximum page size, so list walks the fewest pages.
const pageLimit = 100;

function createDashboardStore() {
  const { subscribe, set, update } = writable<{
    dashboards: Dashboard[];
    current: Dashboard | null;
    loading: boolean;
    total: number;
  }>({
    dashboards: [],
    current: null,
    loading: false,
    total: 0,
  });

  // fetchAll reads every dashboard the caller can see, following pagination
  // to the end, with the total the last page reported.
  async function fetchAll(): Promise<{ all: Dashboard[]; total: number }> {
    const all: Dashboard[] = [];
    let total = 0;
    let page = 1;
    for (;;) {
      const data = await api.get<PaginatedResponse<Dashboard>>(
        `/dashboards?page=${page}&limit=${pageLimit}`
      );
      all.push(...(data.data || []));
      total = data.total;
      if (!data.next) return { all, total };
      page = data.next;
    }
  }

  return {
    subscribe,

    // list loads every dashboard the caller can see into the store. The list
    // page draws one tree with no pager, so a first page alone would hide the
    // rest.
    async list() {
      update((s) => ({ ...s, loading: true }));
      try {
        const { all, total } = await fetchAll();
        update((s) => ({
          ...s,
          dashboards: all,
          total,
          loading: false,
        }));
      } catch {
        update((s) => ({ ...s, loading: false }));
      }
    },

    // listAll returns every dashboard the caller can see without changing the
    // store, for pickers that hold their own list.
    async listAll(): Promise<Dashboard[]> {
      return (await fetchAll()).all;
    },

    // get reads the deployed layout by default, which is what a viewer sees;
    // the editor asks for the draft it is about to change.
    async get(id: string, view: 'deployed' | 'draft' = 'deployed') {
      update((s) => ({ ...s, loading: true }));
      try {
        const dashboard = await api.get<Dashboard>(`/dashboards/${id}?view=${view}`);
        update((s) => ({ ...s, current: dashboard, loading: false }));
        return dashboard;
      } catch {
        update((s) => ({ ...s, loading: false }));
        return null;
      }
    },

    async create(data: Partial<Dashboard>) {
      const dashboard = await api.post<Dashboard>('/dashboards', data);
      update((s) => ({
        ...s,
        dashboards: [...s.dashboards, dashboard],
      }));
      return dashboard;
    },

    async update(id: string, data: Partial<Dashboard> & { expected_display_revision?: number }) {
      const dashboard = await api.put<Dashboard>(`/dashboards/${id}`, data);
      update((s) => ({
        ...s,
        dashboards: s.dashboards.map((d) => (d.id === id ? dashboard : d)),
        current: s.current?.id === id ? dashboard : s.current,
      }));
      return dashboard;
    },

    async remove(id: string) {
      await api.delete(`/dashboards/${id}`);
      update((s) => ({
        ...s,
        dashboards: s.dashboards.filter((d) => d.id !== id),
        current: s.current?.id === id ? null : s.current,
      }));
    },

    async addWidget(dashboardId: string, widget: Partial<DashboardWidget>) {
      return api.post<DashboardWidget>(`/dashboards/${dashboardId}/widgets`, widget);
    },

    async updateWidget(dashboardId: string, widgetId: string, data: Partial<DashboardWidget> & { expected_config_revision?: number }) {
      return api.put<DashboardWidget>(`/dashboards/${dashboardId}/widgets/${widgetId}`, data);
    },

    async removeWidget(dashboardId: string, widgetId: string) {
      return api.delete(`/dashboards/${dashboardId}/widgets/${widgetId}`);
    },

    // Sections follow the same draft/deployed split as widget placements.
    async listSections(dashboardId: string, view: 'deployed' | 'draft' = 'deployed') {
      return api.get<DashboardSection[]>(`/dashboards/${dashboardId}/sections?view=${view}`);
    },

    async addSection(dashboardId: string, title: string) {
      return api.post<DashboardSection>(`/dashboards/${dashboardId}/sections`, { title });
    },

    async updateSection(dashboardId: string, sectionId: string, data: Partial<DashboardSection>) {
      return api.put<DashboardSection>(`/dashboards/${dashboardId}/sections/${sectionId}`, data);
    },

    async removeSection(dashboardId: string, sectionId: string) {
      return api.delete(`/dashboards/${dashboardId}/sections/${sectionId}`);
    },

    async reorderSections(dashboardId: string, items: { id: string; sort_order: number }[]) {
      return api.put(`/dashboards/${dashboardId}/sections/reorder`, { items });
    },

    // Pages filter which placements show; deleting one moves its placements
    // to every page rather than deleting them.
    async addPage(dashboardId: string, title: string) {
      return api.post<DashboardPage>(`/dashboards/${dashboardId}/pages`, { title });
    },

    async updatePage(dashboardId: string, pageId: string, data: Partial<DashboardPage>) {
      return api.put<DashboardPage>(`/dashboards/${dashboardId}/pages/${pageId}`, data);
    },

    async removePage(dashboardId: string, pageId: string) {
      return api.delete(`/dashboards/${dashboardId}/pages/${pageId}`);
    },

    async reorderPages(dashboardId: string, pages: { id: string; sort_order: number }[]) {
      return api.put(`/dashboards/${dashboardId}/pages/reorder`, { pages });
    },

    // live reads the runtime display state commands change.
    async live(dashboardId: string) {
      return api.get<LiveState>(`/dashboards/${dashboardId}/live`);
    },

    async command(dashboardId: string, command: Record<string, unknown>) {
      return api.post<{ seq: number; alert_id?: string }>(`/dashboards/${dashboardId}/commands`, command);
    },

    // deploy freezes the draft as a new version and points viewers at it.
    async deploy(id: string, comment = '') {
      const dashboard = await api.post<Dashboard>(`/dashboards/${id}/deploy`, { comment });
      update((s) => ({
        ...s,
        dashboards: s.dashboards.map((d) => (d.id === dashboard.id ? dashboard : d)),
        current: s.current?.id === dashboard.id ? dashboard : s.current,
      }));
      return dashboard;
    },

    async versions(id: string) {
      return api.get<DashboardVersion[]>(`/dashboards/${id}/versions`);
    },

    // restoreVersion rewrites the draft; viewers keep the deployed version
    // until it is deployed again.
    async restoreVersion(id: string, version: number) {
      return api.post<Dashboard>(`/dashboards/${id}/versions/${version}/restore`, {});
    },

    // exportDoc returns the draft layout as a portable document; widgets are
    // named by package so the file resolves on another instance.
    async exportDoc(id: string) {
      return api.get<unknown>(`/dashboards/${id}/export`);
    },

    async importDoc(document: unknown) {
      const dashboard = await api.post<Dashboard>('/dashboards/import', { document });
      update((s) => ({ ...s, dashboards: [...s.dashboards, dashboard] }));
      return dashboard;
    },

    async listShares(id: string) {
      return api.get<DashboardAccess[]>(`/dashboards/${id}/shares`);
    },

    async addShare(id: string, username?: string, groupName?: string, permission = 'view') {
      const body: any = { permission };
      if (username) body.username = username;
      if (groupName) body.group_name = groupName;
      return api.post<DashboardAccess>(`/dashboards/${id}/shares`, body);
    },

    async removeShare(id: string, shareId: number) {
      return api.delete(`/dashboards/${id}/shares/${shareId}`);
    },

    async reorder(items: { id: string; group_id: number | null; sort_order: number }[]) {
      await api.put('/dashboards/reorder', { items });
    },

    setCurrent(dashboard: Dashboard | null) {
      update((s) => ({ ...s, current: dashboard }));
    },
  };
}

export const dashboards = createDashboardStore();
export type {
  Dashboard,
  DashboardWidget,
  DashboardSection,
  DashboardAccess,
  DashboardVersion,
  DashboardPage,
  DashboardDisplay,
  DashboardVariable,
  VariableOption,
  VariableQuery,
  ResolvedVariables,
  WidgetDisplay,
  TileStyle,
  StatusCue,
  CueAnimation,
  Condition,
  LiveState,
  LiveAlert,
  LiveContent,
  Widget,
};
