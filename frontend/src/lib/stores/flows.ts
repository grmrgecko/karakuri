import { writable } from 'svelte/store';
import { api } from '../api/client';
import type { RetryPolicy } from './nodeCatalog';

interface GraphNode {
  id: string;
  type: string;
  position: { x: number; y: number };
  config?: Record<string, unknown>;
  // params lists config field IDs exposed as value input ports.
  params?: string[];
  // width and height record a node the author sized by hand; only notes have
  // them, since every other node measures itself from its ports.
  width?: number;
  height?: number;
  retry?: RetryPolicy;
  execute_once?: boolean;
  subflow_id?: string;
  subgraph_id?: string;
  // bypass disables the node without unwiring it; the engine splices it out
  // when flattening, forwarding each output from its type-matched input.
  bypass?: boolean;
  // collapsed shrinks the node to its title bar on the canvas; presentation
  // state only, ignored by validation and the engine.
  collapsed?: boolean;
}

interface GraphEdge {
  id: string;
  source: string;
  source_handle: string;
  target: string;
  target_handle: string;
  // kind is "value" for a parameter binding; absent for the message path.
  kind?: string;
}

interface GraphDoc {
  nodes: GraphNode[];
  edges: GraphEdge[];
  viewport: { x: number; y: number; zoom: number };
  subgraphs?: Record<string, GraphDoc>;
}

interface FlowRecord {
  id: string;
  name: string;
  slug: string;
  description: string;
  is_subflow: boolean;
  folder_id?: number;
  enabled: boolean;
  deployed: boolean;
  has_draft_changes: boolean;
  /** The caller's level on the flow; the server enforces every route regardless. */
  access_level?: FlowAccessLevel;
  created_at: string;
  updated_at: string;
}

type FlowAccessLevel = 'faceplate' | 'view' | 'edit' | 'admin';

// flowAccessRank orders the levels so a page can ask whether the caller holds
// at least one of them.
const flowAccessRank: Record<FlowAccessLevel, number> = { faceplate: 1, view: 2, edit: 3, admin: 4 };

/** canFlow reports whether a flow's caller holds at least level on it. */
function canFlow(flow: { access_level?: FlowAccessLevel } | null | undefined, level: FlowAccessLevel): boolean {
  if (!flow?.access_level) return false;
  return flowAccessRank[flow.access_level] >= flowAccessRank[level];
}

// FlowAccess is one share of a flow with a user or user group.
interface FlowAccess {
  id: number;
  flow_id: string;
  user_id?: string;
  user?: { username: string };
  group_id?: number;
  group?: { name: string };
  permission: FlowAccessLevel;
  created_at: string;
}

// FlowDetail is a flow record with its draft graph, as returned by GET /flows/{id}.
interface FlowDetail extends FlowRecord {
  draft_revision: string;
  graph: GraphDoc;
}

interface FlowList {
  data: FlowRecord[];
  limit?: number;
  total?: number;
  next?: number;
  [key: string]: unknown;
}

// pageLimit is the server's maximum page size, so listAll walks the fewest pages.
const pageLimit = 100;

function createFlowsStore() {
  const flows = writable<FlowRecord[]>([]);

  return {
    flows,

    // listAll returns flows and subflows together, following pagination to the
    // end: the list page renders one tree, so a partial first page would hide
    // entries rather than merely paginate them.
    async listAll() {
      const all: FlowRecord[] = [];
      let page = 1;
      for (;;) {
        const result = await api.flows.list<FlowList>(undefined, pageLimit, page);
        all.push(...(result.data || []));
        if (!result.next) break;
        page = result.next;
      }
      flows.set(all);
      return all;
    },

    async create(name: string, description: string, isSubflow = false, folderId: number | null = null) {
      const flow = await api.flows.create<FlowDetail>({
        name,
        description,
        is_subflow: isSubflow,
        ...(folderId === null ? {} : { folder_id: folderId }),
      });
      await this.listAll();
      return flow;
    },

    async move(id: string, folderId: number | null) {
      await api.flows.move<FlowDetail>(id, folderId);
      await this.listAll();
    },

    async remove(id: string) {
      await api.flows.remove(id);
      await this.listAll();
    },

    async listShares(id: string) {
      return api.get<FlowAccess[]>(`/flows/${id}/shares`);
    },

    async addShare(id: string, username?: string, groupName?: string, permission: FlowAccessLevel = 'view') {
      const body: Record<string, string> = { permission };
      if (username) body.username = username;
      if (groupName) body.group_name = groupName;
      return api.post<FlowAccess>(`/flows/${id}/shares`, body);
    },

    async removeShare(id: string, shareId: number) {
      return api.delete(`/flows/${id}/shares/${shareId}`);
    },

    async setEnabled(id: string, enabled: boolean) {
      if (enabled) {
        await api.flows.enable<FlowDetail>(id);
      } else {
        await api.flows.disable<FlowDetail>(id);
      }
      await this.listAll();
    },
  };
}

export const flowsStore = createFlowsStore();
export { canFlow };
export type { FlowRecord, FlowDetail, GraphDoc, GraphNode, GraphEdge, FlowAccess, FlowAccessLevel };
