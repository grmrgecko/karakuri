import { writable, get } from 'svelte/store';
import { api, type ApiError } from '../api/client';
import type { FlowDetail, GraphDoc } from './flows';

interface EditorState {
  flow: FlowDetail | null;
  loading: boolean;
  saving: boolean;
  deploying: boolean;
  dirty: boolean;
  selectedNodeId: string | null;
  // problems maps validation locations ("node:<id>", "edge:<id>") to messages,
  // as returned in the details of a graph validation error.
  problems: Record<string, string> | null;
  // saveError holds why the last draft save failed. Autosave runs unattended,
  // so a failure is reported in the toolbar rather than through a dialog the
  // author never asked for.
  saveError: string;
  error: string;
  // runPrefix namespaces run event node IDs while drilled into a subflow
  // ("<subflowNodeID>/..."). Empty at the top level.
  runPrefix: string;
  // docSubgraphs mirrors the current document's local subgraphs so node
  // components can derive subflow ports and labels.
  docSubgraphs: Record<string, GraphDoc>;
  // drillRequestNodeId asks the editor shell to open the given subflow node.
  drillRequestNodeId: string | null;
  // testRunRequest asks the editor shell to start a test run at the given
  // trigger node. withValue opens the payload prompt instead of running
  // straight away.
  testRunRequest: { nodeId: string; withValue: boolean } | null;
  nodeChatRequest: string | null;
  // edgeMenuRequest asks the editor shell to open the edge action menu at the
  // given screen position. The shell owns the graph, so the edge component
  // only names the edge; the shell renders and acts on the menu.
  edgeMenuRequest: { edgeId: string; x: number; y: number } | null;
  // nodeRotateRequest asks the editor shell to turn a node's card to the
  // given angle in degrees. The node component owns the pointer geometry of
  // the rotation drag; the graph it writes into is the shell's.
  nodeRotateRequest: { nodeId: string; degrees: number } | null;
  // edgeRerouteRequest asks the editor shell to insert a reroute into the
  // given edge at the given screen position and hand the pointer drag to it,
  // which is what dragging an edge's dot does.
  edgeRerouteRequest: { edgeId: string; x: number; y: number } | null;
  // canvasReadOnly mirrors the shell's read-only state (a drilled shared
  // subflow) for canvas components that offer edits, like the edge menu dot.
  canvasReadOnly: boolean;
}

const initialState: EditorState = {
  flow: null,
  loading: false,
  saving: false,
  deploying: false,
  dirty: false,
  selectedNodeId: null,
  problems: null,
  saveError: '',
  error: '',
  runPrefix: '',
  docSubgraphs: {},
  drillRequestNodeId: null,
  testRunRequest: null,
  nodeChatRequest: null,
  edgeMenuRequest: null,
  nodeRotateRequest: null,
  edgeRerouteRequest: null,
  canvasReadOnly: false,
};

// problemDetails extracts string validation details from an API error, or
// null when the error carries no usable per-location details.
function problemDetails(err: ApiError): Record<string, string> | null {
  if (err.code !== 'VALIDATION_ERROR' || !err.details) return null;
  const out: Record<string, string> = {};
  for (const [key, value] of Object.entries(err.details)) {
    out[key] = String(value);
  }
  return Object.keys(out).length > 0 ? out : null;
}

// editGeneration counts durable edits, so a save can tell whether the canvas
// changed under it while its request was in flight.
let editGeneration = 0;

function createEditorStore() {
  const store = writable<EditorState>({ ...initialState });
  const { subscribe, update } = store;

  // updateFlow applies the outcome of a request about flow id. reset and load
  // re-point the store while a request is in flight, and a result written
  // into another flow's state would aim the next save at the wrong flow.
  function updateFlow(id: string, fn: (s: EditorState) => EditorState) {
    update((s) => (s.flow?.id === id ? fn(s) : s));
  }

  return {
    subscribe,

    // load fetches a flow with its draft graph and resets editor state.
    async load(id: string) {
      store.set({ ...initialState, loading: true });
      try {
        const flow = await api.flows.get<FlowDetail>(id);
        update((s) => ({ ...s, flow, loading: false }));
        return flow;
      } catch (e: any) {
        update((s) => ({ ...s, loading: false, error: e.message || 'Failed to load flow' }));
        return null;
      }
    },

    acceptDraft(flow: FlowDetail) {
      update((s) => ({ ...s, flow, dirty: false, saveError: '', problems: {} }));
    },

    reset() {
      store.set({ ...initialState });
    },

    select(nodeId: string | null) {
      update((s) => (s.selectedNodeId === nodeId ? s : { ...s, selectedNodeId: nodeId }));
    },

    markDirty() {
      editGeneration += 1;
      update((s) => (s.dirty ? s : { ...s, dirty: true }));
    },

    // setRunPrefix records the drill-in namespace for run event mapping.
    setRunPrefix(prefix: string) {
      update((s) => (s.runPrefix === prefix ? s : { ...s, runPrefix: prefix }));
    },

    // setDocSubgraphs mirrors the document's local subgraphs for components.
    setDocSubgraphs(subgraphs: Record<string, GraphDoc>) {
      update((s) => ({ ...s, docSubgraphs: subgraphs }));
    },

    // requestDrill asks the editor shell to drill into a subflow node.
    requestDrill(nodeId: string) {
      update((s) => ({ ...s, drillRequestNodeId: nodeId }));
    },

    clearDrillRequest() {
      update((s) => (s.drillRequestNodeId === null ? s : { ...s, drillRequestNodeId: null }));
    },

    // requestTestRun asks the editor shell to test-run the flow from a trigger
    // node. The shell owns the graph and the save-before-run rule, so the node
    // component only names the trigger.
    requestTestRun(nodeId: string, withValue: boolean) {
      update((s) => ({ ...s, testRunRequest: { nodeId, withValue } }));
    },

    requestNodeChat(nodeId: string) {
      update((s) => ({ ...s, nodeChatRequest: nodeId }));
    },

    clearNodeChatRequest() {
      update((s) => ({ ...s, nodeChatRequest: null }));
    },

    clearTestRunRequest() {
      update((s) => (s.testRunRequest === null ? s : { ...s, testRunRequest: null }));
    },

    // requestEdgeMenu asks the editor shell to open the edge action menu.
    requestEdgeMenu(edgeId: string, x: number, y: number) {
      update((s) => ({ ...s, edgeMenuRequest: { edgeId, x, y } }));
    },

    clearEdgeMenuRequest() {
      update((s) => (s.edgeMenuRequest === null ? s : { ...s, edgeMenuRequest: null }));
    },

    // requestNodeRotate asks the editor shell to turn a node's card. It is
    // sent for every step of the drag, so the wires follow the sockets round.
    requestNodeRotate(nodeId: string, degrees: number) {
      update((s) => ({ ...s, nodeRotateRequest: { nodeId, degrees } }));
    },

    clearNodeRotateRequest() {
      update((s) => (s.nodeRotateRequest === null ? s : { ...s, nodeRotateRequest: null }));
    },

    // requestEdgeReroute asks the editor shell to bend an edge: a reroute is
    // inserted at the given screen position and follows the pointer until it
    // is released.
    requestEdgeReroute(edgeId: string, x: number, y: number) {
      update((s) => ({ ...s, edgeRerouteRequest: { edgeId, x, y } }));
    },

    clearEdgeRerouteRequest() {
      update((s) => (s.edgeRerouteRequest === null ? s : { ...s, edgeRerouteRequest: null }));
    },

    // setCanvasReadOnly mirrors the shell's read-only state for components.
    setCanvasReadOnly(readOnly: boolean) {
      update((s) => (s.canvasReadOnly === readOnly ? s : { ...s, canvasReadOnly: readOnly }));
    },

    // saveGraph persists the draft graph. Returns true on success; on a
    // validation failure the problems map is populated for the editor, and the
    // draft stays dirty so the next attempt carries the same edits.
    async saveGraph(graph: GraphDoc): Promise<boolean> {
      const flow = get(store).flow;
      if (!flow) return false;
      const generation = editGeneration;
      update((s) => ({ ...s, saving: true }));
      try {
        const updated = await api.flows.saveGraph<FlowDetail>(flow.id, graph, flow.draft_revision);
        // An edit made while the request was in flight is not in what was
        // stored. The draft stays dirty for it, or Deploy and leaving the
        // editor would trust a store that never saw the edit.
        updateFlow(flow.id, (s) => ({
          ...s,
          flow: updated,
          saving: false,
          dirty: editGeneration !== generation,
          problems: null,
          saveError: '',
        }));
        return true;
      } catch (e: any) {
        updateFlow(flow.id, (s) => ({
          ...s,
          saving: false,
          problems: problemDetails(e),
          saveError: e?.message || 'Failed to save the draft',
        }));
        return false;
      }
    },

    // deploy freezes the current draft as a deployed version.
    async deploy(): Promise<boolean> {
      const flow = get(store).flow;
      if (!flow) return false;
      update((s) => ({ ...s, deploying: true }));
      try {
        const updated = await api.flows.deploy<FlowDetail>(flow.id);
        updateFlow(flow.id, (s) => ({ ...s, flow: updated, deploying: false, problems: null }));
        return true;
      } catch (e: any) {
        updateFlow(flow.id, (s) => ({ ...s, deploying: false, problems: problemDetails(e) }));
        if (!problemDetails(e)) {
          alert(e.message || 'Failed to deploy flow');
        }
        return false;
      }
    },

    async setEnabled(enabled: boolean) {
      const flow = get(store).flow;
      if (!flow) return;
      try {
        const updated = enabled
          ? await api.flows.enable<FlowDetail>(flow.id)
          : await api.flows.disable<FlowDetail>(flow.id);
        updateFlow(flow.id, (s) => ({ ...s, flow: updated }));
      } catch (e: any) {
        alert(e.message || 'Failed to update flow');
      }
    },

    // updateMeta patches flow name/description.
    async updateMeta(body: { name?: string; description?: string }) {
      const flow = get(store).flow;
      if (!flow) return;
      const updated = await api.flows.update<FlowDetail>(flow.id, body);
      updateFlow(flow.id, (s) => ({ ...s, flow: updated }));
    },

    clearProblems() {
      update((s) => (s.problems ? { ...s, problems: null } : s));
    },
  };
}

export const editor = createEditorStore();
export type { EditorState };
