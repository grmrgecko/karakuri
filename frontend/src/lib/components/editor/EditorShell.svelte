<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte';
  import { navigate } from 'svelte-routing';
  import {
    SvelteFlow,
    Background,
    BackgroundVariant,
    Controls,
    MiniMap,
    Panel,
    SelectionMode,
    useSvelteFlow,
    type Viewport,
    type Connection,
    type IsValidConnection,
  } from '@xyflow/svelte';
  import '@xyflow/svelte/dist/style.css';
  import { api } from '../../api/client';
  import { watchDocument } from '../../stores/documentSync';
  import { auth, preferences } from '../../stores/auth';
  import { nodeCatalog, isTrigger, isLinkNode } from '../../stores/nodeCatalog';
  import { connectionsStore, connectionMatchesField } from '../../stores/connections';
  import { editor } from '../../stores/editor';
  import { runState } from '../../stores/runState';
  import { subflowLibrary } from '../../stores/subflows';
  import { nodePorts } from '../../utils/nodePorts';
  import { renamePortEdges } from '../../utils/portRename';
  import {
    compatible,
    messageCompatible,
    isValuePortType,
    paramFieldId,
    paramPortId,
  } from '../../utils/payloadPorts';
  import {
    setClipboard,
    getClipboard,
    nextPasteOffset,
    type ClipboardFragment,
  } from '../../utils/clipboard';
  import { propagateTypes } from '../../utils/typeFlow';
  import { portTypeColor } from '../../utils/typeColors';
  import { invalidEdges } from '../../utils/edgeValidity';
  import { carriedTypes } from '../../stores/carriedTypes';
  import { testTargets } from '../../stores/testTargets';
  import { confirm } from '../../stores/confirmDialog';
  import { relTime } from '../../utils/time';
  import { uid } from '../../utils/id';
  import { canFlow } from '../../stores/flows';
  import type { FlowDetail, GraphDoc } from '../../stores/flows';
  import Modal from '../Modal.svelte';
  import EditorAssistant from '../EditorAssistant.svelte';
  import FormField from '../FormField.svelte';
  import NodeChat from './NodeChat.svelte';
  import FlowNode from './FlowNode.svelte';
  import FlowEdge from './FlowEdge.svelte';
  import NoteNode from './NoteNode.svelte';
  import GroupNode from './GroupNode.svelte';
  import RerouteNode from './RerouteNode.svelte';
  import Palette from './Palette.svelte';
  import NodePlacer, { type PlacerFilter } from './NodePlacer.svelte';
  import ConfigPanel from './ConfigPanel.svelte';
  import RunPanel from './RunPanel.svelte';
  import type { EditorNode, EditorEdge, FlowNodeData } from './types';

  let { flow }: { flow: FlowDetail } = $props();

  // The group frame is registered as "groupframe" rather than "group": Svelte
  // Flow's stylesheet carries a built-in look for a node type named "group" —
  // padding, a grey fill, and a border of its own — and a renderer under that
  // key inherits it, drawing a second box around the frame.
  const nodeTypes = { flow: FlowNode, note: NoteNode, groupframe: GroupNode, reroute: RerouteNode };
  // Every edge renders through FlowEdge, which adds the midpoint action dot to
  // the standard bezier path.
  const edgeTypes = { default: FlowEdge };
  const { screenToFlowPosition, fitView } = useSvelteFlow();

  // ---- Graph document mapping ----

  // Annotations are sized on the canvas rather than by their contents, so one
  // that has never been resized — or was saved before it could be — opens at a
  // size worth having instead of whatever it happens to measure. These are also
  // the only node types whose size is stored: every other node measures itself
  // from its ports and would be pinned by carrying one.
  const defaultSizes: Record<string, { width: number; height: number }> = {
    note: { width: 240, height: 140 },
    group: { width: 380, height: 260 },
  };

  // A group is a frame drawn behind the graph, and Svelte Flow adds 1000 to a
  // selected node's z-index. Sitting far enough below zero keeps the frame
  // behind the nodes it holds even while it is selected for resizing.
  const groupZIndex = -1001;

  // canvasType picks the component that draws a node. The backend type is what
  // is stored; this is only which of the three renderers it is handed to.
  function canvasType(nodeType: string): string {
    if (nodeType === 'note') return 'note';
    if (nodeType === 'group') return 'groupframe';
    if (nodeType === 'reroute') return 'reroute';
    return 'flow';
  }

  function toEditorNodes(graph: GraphDoc): EditorNode[] {
    return (graph.nodes ?? []).map((n) => {
      const config = { ...(n.config ?? {}) };
      const label = typeof config._label === 'string' ? config._label : '';
      delete config._label;
      const size = defaultSizes[n.type];
      return {
        id: n.id,
        type: canvasType(n.type),
        position: { x: n.position.x, y: n.position.y },
        ...(n.width || size ? { width: n.width || size!.width } : {}),
        ...(n.height || size ? { height: n.height || size!.height } : {}),
        ...(n.type === 'group' ? { zIndex: groupZIndex } : {}),
        data: {
          nodeType: n.type,
          config,
          label,
          params: n.params ? [...n.params] : undefined,
          retry: n.retry ? { ...n.retry } : undefined,
          ...(n.execute_once ? { execute_once: true } : {}),
          subflowId: n.subflow_id,
          subgraphId: n.subgraph_id,
          ...(n.bypass ? { bypass: true } : {}),
          ...(n.collapsed ? { collapsed: true } : {}),
        },
      };
    });
  }

  // edgeClass decides how an edge is drawn. A value edge binds a parameter and
  // carries no message, so its own styling wins over the error-branch styling;
  // an edge validation would reject is marked broken on top of either.
  function edgeClass(
    e: { kind?: string; source: string; sourceHandle?: string | null },
    invalid = false
  ): string | undefined {
    return edgeClassFor(e, outputPort(e.source, e.sourceHandle)?.kind, invalid);
  }

  // edgeClassFor is edgeClass with the source port's kind already resolved, for
  // the reactive pass below: it must not read the carried-types store it feeds.
  function edgeClassFor(
    e: { kind?: string },
    srcKind: string | undefined,
    invalid: boolean
  ): string | undefined {
    const base = e.kind === 'value' ? 'value-edge' : srcKind === 'error' ? 'error-edge' : '';
    const cls = invalid ? `${base} invalid-edge`.trim() : base;
    return cls || undefined;
  }

  function toEditorEdges(graph: GraphDoc): EditorEdge[] {
    return (graph.edges ?? []).map((e) => ({
      id: e.id,
      source: e.source,
      sourceHandle: e.source_handle,
      target: e.target,
      targetHandle: e.target_handle,
      kind: e.kind,
    }));
  }

  // ---- Canvas state and drill-in levels ----
  //
  // The canvas always shows one level of the document: the root graph or a
  // subflow. crumbs records the path down; each crumb stores the parent
  // level's canvas so navigating up restores it. Local subflow edits commit
  // into the subgraphs map when leaving the level or serializing the doc.

  type CanvasState = { nodes: EditorNode[]; edges: EditorEdge[]; viewport: Viewport };
  type Crumb = {
    kind: 'local' | 'shared';
    nodeId: string;
    refId: string;
    name: string;
    saved: CanvasState;
  };

  // The flow prop is intentionally captured once: the canvas arrays are the
  // source of truth after mount, and saves must not reset editor state.
  // svelte-ignore state_referenced_locally
  let subgraphs = $state<Record<string, GraphDoc>>({ ...(flow.graph.subgraphs ?? {}) });
  let crumbs = $state<Crumb[]>([]);

  // The catalog is loaded before this component mounts, so error-branch
  // edges can be classified immediately from port kinds.
  // svelte-ignore state_referenced_locally
  let nodes = $state.raw<EditorNode[]>(toEditorNodes(flow.graph));
  // svelte-ignore state_referenced_locally
  let edges = $state.raw<EditorEdge[]>(
    // svelte-ignore state_referenced_locally
    toEditorEdges(flow.graph).map((e) => ({ ...e, class: edgeClass(e) }))
  );
  // svelte-ignore state_referenced_locally
  let viewport = $state.raw<Viewport>(
    // svelte-ignore state_referenced_locally
    flow.graph.viewport?.zoom ? { ...flow.graph.viewport } : { x: 0, y: 0, zoom: 1 }
  );

  // canEdit is the caller's share on this flow: below edit the whole graph is
  // read only and the toolbar offers nothing that changes or runs it. The
  // server refuses those calls regardless; this only stops offering them.
  let canEdit = $derived(canFlow(flow, 'edit'));
  let canShare = $derived(canFlow(flow, 'admin'));
  let inSharedSubflow = $derived(crumbs.length > 0 && crumbs[crumbs.length - 1].kind === 'shared');
  let readOnly = $derived(!canEdit || inSharedSubflow);
  let paletteMode = $derived<'flow' | 'subflow'>(
    crumbs.length > 0 || flow.is_subflow ? 'subflow' : 'flow'
  );

  // Keep the run event namespace and the document subgraphs mirrored into
  // the editor store for node components.
  $effect(() => {
    editor.setRunPrefix(crumbs.length ? crumbs.map((c) => c.nodeId).join('/') + '/' : '');
  });
  $effect(() => {
    editor.setDocSubgraphs(subgraphs);
  });
  $effect(() => {
    editor.setCanvasReadOnly(readOnly);
  });

  // ---- Undo history ----
  //
  // History is a stack of snapshots of what the author edits: nodes, edges,
  // and the local subgraph definitions. The viewport and the selection are
  // deliberately left out — panning is navigation, not an edit — and so are
  // the transient fields Svelte Flow decorates nodes and edges with
  // (selected, measured, class), which is also what lets a restored state
  // restyle itself through the ordinary reactive passes.
  //
  // Recording is a debounced effect over the tracked state rather than a call
  // in every mutating function: a drag that streams position updates lands as
  // one entry, and no edit path can forget to record. History is scoped to
  // the current drill level; entering or leaving a subflow resets it, so an
  // undo never rewrites a level that is not on screen.

  const historyLimit = 100;
  const historyDebounceMs = 300;
  let undoStack: string[] = [];
  let redoStack: string[] = [];
  let historyCurrent = '';
  let historyTimer: ReturnType<typeof setTimeout> | null = null;

  // historySnapshot captures the durable graph state as a canonical string.
  // Parsing it back yields exactly these fields, so a freshly restored state
  // re-serializes identically and is never recorded as a new edit.
  function historySnapshot(): string {
    return JSON.stringify({
      nodes: nodes.map((n) => ({
        id: n.id,
        type: n.type,
        position: n.position,
        ...(n.width !== undefined ? { width: n.width } : {}),
        ...(n.height !== undefined ? { height: n.height } : {}),
        ...(n.zIndex !== undefined ? { zIndex: n.zIndex } : {}),
        data: n.data,
      })),
      edges: edges.map((e) => ({
        id: e.id,
        source: e.source,
        sourceHandle: e.sourceHandle,
        target: e.target,
        targetHandle: e.targetHandle,
        ...(e.kind ? { kind: e.kind } : {}),
      })),
      subgraphs,
    });
  }

  // recordHistory commits the pending snapshot when it differs from the last
  // recorded state. A real change invalidates the redo path.
  function recordHistory() {
    const snap = historySnapshot();
    if (snap === historyCurrent) return;
    undoStack.push(historyCurrent);
    if (undoStack.length > historyLimit) undoStack.shift();
    redoStack = [];
    historyCurrent = snap;
  }

  // flushHistory commits a pending debounced record immediately, so an edit
  // made moments before an undo is what gets undone.
  function flushHistory() {
    if (!historyTimer) return;
    clearTimeout(historyTimer);
    historyTimer = null;
    recordHistory();
  }

  // resetHistory drops the stacks and takes the current state as the new
  // baseline, for level changes and full reloads.
  function resetHistory() {
    if (historyTimer) {
      clearTimeout(historyTimer);
      historyTimer = null;
    }
    undoStack = [];
    redoStack = [];
    historyCurrent = historySnapshot();
  }

  resetHistory();

  // Record on every tracked change, debounced so a stream of updates from one
  // gesture coalesces into a single entry.
  $effect(() => {
    historySnapshot();
    if (historyTimer) clearTimeout(historyTimer);
    historyTimer = setTimeout(() => {
      historyTimer = null;
      recordHistory();
    }, historyDebounceMs);
  });

  // applySnapshot restores a recorded state. Setting historyCurrent first
  // keeps the record effect from treating the restoration as a fresh edit;
  // the edge classes and carried types rebuild through the reactive passes.
  function applySnapshot(snap: string) {
    const doc = JSON.parse(snap) as {
      nodes: EditorNode[];
      edges: EditorEdge[];
      subgraphs: Record<string, GraphDoc>;
    };
    historyCurrent = snap;
    nodes = doc.nodes;
    edges = doc.edges;
    subgraphs = doc.subgraphs ?? {};
    editor.select(null);
    editor.markDirty();
  }

  function undo() {
    flushHistory();
    const prev = undoStack.pop();
    if (prev === undefined) return;
    redoStack.push(historyCurrent);
    applySnapshot(prev);
  }

  function redo() {
    flushHistory();
    const next = redoStack.pop();
    if (next === undefined) return;
    undoStack.push(historyCurrent);
    applySnapshot(next);
  }

  // editableTarget reports whether a key event belongs to a text field, whose
  // own editing behavior must win over the canvas shortcuts.
  function editableTarget(e: KeyboardEvent): boolean {
    const target = e.target as HTMLElement | null;
    return (
      !!target &&
      (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName))
    );
  }

  // handleHistoryKey binds Ctrl/Cmd+Z and Ctrl/Cmd+Y (or Shift+Z) to the
  // graph history. A shortcut typed into a field belongs to that field's own
  // text history, so anything editable keeps the keystroke.
  function handleHistoryKey(e: KeyboardEvent) {
    if (readOnly) return;
    if (!(e.ctrlKey || e.metaKey) || e.altKey) return;
    const key = e.key.toLowerCase();
    const isUndo = key === 'z' && !e.shiftKey;
    const isRedo = key === 'y' || (key === 'z' && e.shiftKey);
    if (!isUndo && !isRedo) return;
    if (editableTarget(e)) return;
    e.preventDefault();
    if (isUndo) undo();
    else redo();
  }

  // ---- Bypass ----
  //
  // A bypassed node stays wired and configured but nothing runs in it: the
  // engine splices it out when flattening, forwarding each output from its
  // type-matched input. Toggled from the node's context menu or Ctrl+B.

  // nodeBypassable mirrors flow.Bypassable: the flag needs a message input to
  // forward from and an output to forward to.
  function nodeBypassable(node: EditorNode): boolean {
    if (isLinkNode(node.data.nodeType)) return false;
    const meta = $nodeCatalog.byType[node.data.nodeType];
    if (!meta || meta.category === 'annotation') return false;
    const ports = portsOf(node);
    return ports.outputs.length > 0 && ports.inputs.some((p) => p.kind !== 'param' && p.kind !== 'signal');
  }

  // toggleBypass flips the flag on every eligible node in the set. A mixed
  // selection bypasses the whole set, so one press always leaves it uniform.
  function toggleBypass(ids: string[]) {
    if (readOnly) return;
    const eligible = new Set(
      ids.filter((id) => {
        const node = nodes.find((n) => n.id === id);
        return node && nodeBypassable(node);
      })
    );
    if (eligible.size === 0) return;
    const enable = nodes.some((n) => eligible.has(n.id) && !n.data.bypass);
    nodes = nodes.map((n) =>
      eligible.has(n.id)
        ? { ...n, data: { ...n.data, bypass: enable ? true : undefined } }
        : n
    );
    editor.markDirty();
  }

  // handleBypassKey toggles bypass on the selected nodes with Ctrl/Cmd+B.
  function handleBypassKey(e: KeyboardEvent) {
    if (readOnly || e.defaultPrevented) return;
    if (!(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey) return;
    if (e.key.toLowerCase() !== 'b') return;
    if (editableTarget(e)) return;
    const selected = nodes.filter((n) => n.selected).map((n) => n.id);
    if (selected.length === 0) return;
    e.preventDefault();
    toggleBypass(selected);
  }

  // ---- Collapse ----
  //
  // A collapsed node shrinks to its title bar. Presentation
  // only: the handles stay mounted so no edge breaks, and the engine never
  // sees the flag.

  // nodeCollapsible limits collapsing to card-rendered nodes: an annotation
  // or a reroute dot has no body to fold away.
  function nodeCollapsible(node: EditorNode): boolean {
    return node.type === 'flow';
  }

  // toggleCollapse flips the flag on every eligible node in the set. A mixed
  // selection collapses the whole set, so one action leaves it uniform.
  function toggleCollapse(ids: string[]) {
    if (readOnly) return;
    const eligible = new Set(
      ids.filter((id) => {
        const node = nodes.find((n) => n.id === id);
        return node && nodeCollapsible(node);
      })
    );
    if (eligible.size === 0) return;
    const enable = nodes.some((n) => eligible.has(n.id) && !n.data.collapsed);
    nodes = nodes.map((n) =>
      eligible.has(n.id)
        ? { ...n, data: { ...n.data, collapsed: enable ? true : undefined } }
        : n
    );
    editor.markDirty();
  }

  // ---- Clipboard ----
  //
  // Copy captures a node set, the edges wholly between its members, and the
  // local subgraph definitions any of them reference, so the fragment pastes
  // whole into this flow or another one. Node IDs are minted fresh on paste;
  // subgraph definitions keep theirs, so a pasted copy of a local-subflow
  // node shares the definition the way duplicating it by hand would.

  // fragmentFor builds a clipboard fragment from a node set, stripped of the
  // transient canvas fields. Null with a toast when a referenced subgraph
  // definition is not in this document (a copy taken inside a drilled shared
  // subflow can point at definitions only the shared flow holds).
  function fragmentFor(selected: EditorNode[]): ClipboardFragment | null {
    const idSet = new Set(selected.map((n) => n.id));
    const defs: Record<string, GraphDoc> = {};
    const queue = selected.map((n) => n.data.subgraphId).filter(Boolean) as string[];
    while (queue.length > 0) {
      const sgId = queue.pop()!;
      if (defs[sgId]) continue;
      const def = subgraphs[sgId];
      if (!def) {
        showToast('The selection references a local subflow that is not part of this flow.');
        return null;
      }
      defs[sgId] = def;
      for (const gn of def.nodes ?? []) {
        if (gn.subgraph_id) queue.push(gn.subgraph_id);
      }
    }
    return {
      nodes: selected.map((n) => ({
        id: n.id,
        type: n.type,
        position: { ...n.position },
        ...(n.width !== undefined ? { width: n.width } : {}),
        ...(n.height !== undefined ? { height: n.height } : {}),
        ...(n.zIndex !== undefined ? { zIndex: n.zIndex } : {}),
        data: n.data,
      })) as EditorNode[],
      edges: edges
        .filter((e) => idSet.has(e.source) && idSet.has(e.target))
        .map((e) => ({
          id: e.id,
          source: e.source,
          sourceHandle: e.sourceHandle,
          target: e.target,
          targetHandle: e.targetHandle,
          ...(e.kind ? { kind: e.kind } : {}),
        })),
      subgraphs: defs,
    };
  }

  // copyNodes puts a node set on the clipboard. Reports whether anything was
  // actually copied, so the key handler can leave the browser's copy alone.
  function copyNodes(selected: EditorNode[]): boolean {
    if (selected.length === 0) return false;
    const fragment = fragmentFor(selected);
    if (!fragment) return false;
    setClipboard(fragment);
    return true;
  }

  // insertFragment places a fragment with fresh node IDs at the given offset,
  // adopts subgraph definitions the document lacks, and moves the selection
  // to what was placed.
  function insertFragment(fragment: ClipboardFragment, offset: { x: number; y: number }) {
    const idMap = new Map<string, string>();
    const placed = fragment.nodes.map((n) => {
      const id = `${n.data.nodeType}_${uid()}`;
      idMap.set(n.id, id);
      return {
        ...n,
        id,
        position: { x: n.position.x + offset.x, y: n.position.y + offset.y },
        selected: true,
      };
    });
    const missing = Object.entries(fragment.subgraphs).filter(([sgId]) => !subgraphs[sgId]);
    if (missing.length > 0) subgraphs = { ...subgraphs, ...Object.fromEntries(missing) };
    nodes = [...nodes.map((n) => (n.selected ? { ...n, selected: false } : n)), ...placed];
    const placedEdges = fragment.edges.map((e) => {
      const edge: EditorEdge = {
        ...e,
        id: `edge_${uid()}`,
        source: idMap.get(e.source)!,
        target: idMap.get(e.target)!,
      };
      edge.class = edgeClass(edge);
      return edge;
    });
    edges = [...edges, ...placedEdges];
    editor.select(placed.length === 1 ? placed[0].id : null);
    editor.markDirty();
  }

  function pasteClipboard() {
    if (readOnly) return;
    const fragment = getClipboard();
    if (!fragment || fragment.nodes.length === 0) return;
    insertFragment(fragment, nextPasteOffset());
  }

  // pasteClipboardAt places the clipboard fragment with its top-left corner at
  // a canvas point, so a menu paste lands where the click was instead of at a
  // staggered offset from the original.
  function pasteClipboardAt(at: { x: number; y: number }) {
    if (readOnly) return;
    const fragment = getClipboard();
    if (!fragment || fragment.nodes.length === 0) return;
    const minX = Math.min(...fragment.nodes.map((n) => n.position.x));
    const minY = Math.min(...fragment.nodes.map((n) => n.position.y));
    insertFragment(fragment, { x: at.x - minX, y: at.y - minY });
  }

  // duplicateNodes clones a node set in place without touching the clipboard.
  function duplicateNodes(ids: string[]) {
    if (readOnly) return;
    const idSet = new Set(ids);
    const fragment = fragmentFor(nodes.filter((n) => idSet.has(n.id)));
    if (!fragment || fragment.nodes.length === 0) return;
    insertFragment(fragment, { x: 40, y: 40 });
  }

  // handleClipboardKey binds Ctrl/Cmd+C, V, and D to the canvas clipboard. A
  // key aimed at a text field keeps its editing meaning, and copy stays
  // available on a read-only level.
  function handleClipboardKey(e: KeyboardEvent) {
    if (e.defaultPrevented) return;
    if (!(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey) return;
    const key = e.key.toLowerCase();
    if (key !== 'c' && key !== 'v' && key !== 'd') return;
    if (editableTarget(e)) return;
    if (key === 'c') {
      // A highlighted text selection outranks the canvas: copying an error
      // message must stay possible while nodes are selected.
      if (window.getSelection()?.isCollapsed === false) return;
      if (copyNodes(nodes.filter((n) => n.selected))) e.preventDefault();
      return;
    }
    if (readOnly) return;
    if (key === 'd') {
      const sel = nodes.filter((n) => n.selected).map((n) => n.id);
      if (sel.length === 0) return;
      e.preventDefault();
      duplicateNodes(sel);
      return;
    }
    e.preventDefault();
    pasteClipboard();
  }

  // handleSaveKey keeps Ctrl/Cmd+S working: it commits the pending autosave
  // rather than opening the browser's save dialog.
  function handleSaveKey(e: KeyboardEvent) {
    if (!(e.ctrlKey || e.metaKey) || e.altKey || e.key.toLowerCase() !== 's') return;
    e.preventDefault();
    void flushSave();
  }

  // handleEditorKey routes the canvas keyboard shortcuts.
  function handleEditorKey(e: KeyboardEvent) {
    if ((e.target as HTMLElement | null)?.closest('.assistant-dialog')) return;
    handleHistoryKey(e);
    handleBypassKey(e);
    handleClipboardKey(e);
    handleSaveKey(e);
  }

  // loadLevel replaces the canvas with one graph level.
  function loadLevel(g: GraphDoc) {
    nodes = toEditorNodes(g);
    edges = toEditorEdges(g).map((e) => ({ ...e, class: edgeClass(e) }));
    viewport = g.viewport?.zoom ? { ...g.viewport } : { x: 0, y: 0, zoom: 1 };
    resetHistory();
  }

  // serializeLevel converts one canvas state into a graph document level.
  function serializeLevel(ns: EditorNode[], es: EditorEdge[], vp: Viewport): GraphDoc {
    return {
      nodes: ns.map((n) => {
        const config: Record<string, unknown> = {};
        for (const [k, v] of Object.entries(n.data.config)) {
          if (v !== undefined) config[k] = v;
        }
        if (n.data.label) config._label = n.data.label;
        return {
          id: n.id,
          type: n.data.nodeType,
          position: { x: Math.round(n.position.x), y: Math.round(n.position.y) },
          // Only a hand-sized node carries a size; everything else measures
          // itself from its ports and would be pinned by storing one.
          ...(defaultSizes[n.data.nodeType] && n.width ? { width: Math.round(n.width) } : {}),
          ...(defaultSizes[n.data.nodeType] && n.height ? { height: Math.round(n.height) } : {}),
          ...(Object.keys(config).length > 0 ? { config } : {}),
          ...(n.data.params?.length ? { params: [...n.data.params] } : {}),
          ...(n.data.retry ? { retry: n.data.retry } : {}),
          ...(n.data.execute_once ? { execute_once: true } : {}),
          ...(n.data.subflowId ? { subflow_id: n.data.subflowId } : {}),
          ...(n.data.subgraphId ? { subgraph_id: n.data.subgraphId } : {}),
          ...(n.data.bypass ? { bypass: true } : {}),
          ...(n.data.collapsed ? { collapsed: true } : {}),
        };
      }),
      edges: es.map((e) => ({
        id: e.id,
        source: e.source,
        source_handle: e.sourceHandle ?? '',
        target: e.target,
        target_handle: e.targetHandle ?? '',
        ...(e.kind === 'value' ? { kind: 'value' } : {}),
      })),
      viewport: { x: vp.x, y: vp.y, zoom: vp.zoom },
    };
  }

  // canvasStateAt returns the canvas of a drill level: a stashed parent
  // level or the live canvas at the deepest level.
  function canvasStateAt(level: number): CanvasState {
    if (level < crumbs.length) return crumbs[level].saved;
    return { nodes, edges, viewport };
  }

  // serializeGraph converts the whole editor state back into the backend
  // graph document, with every drilled local level in its subgraphs map. It
  // leaves the editor state alone: subgraphs is part of the undo history and
  // the autosave trigger, so writing it here would record each save while
  // drilled as an edit. Leaving a level commits it through goToLevel.
  function serializeGraph(): GraphDoc {
    const subs: Record<string, GraphDoc> = { ...subgraphs };
    for (let i = 0; i < crumbs.length; i++) {
      if (crumbs[i].kind !== 'local') continue;
      const cs = canvasStateAt(i + 1);
      subs[crumbs[i].refId] = serializeLevel(cs.nodes, cs.edges, cs.viewport);
    }
    const rootCS = canvasStateAt(0);
    const root = serializeLevel(rootCS.nodes, rootCS.edges, rootCS.viewport);
    return { ...root, ...(Object.keys(subs).length > 0 ? { subgraphs: subs } : {}) };
  }

  // ---- Drill-in navigation ----

  // frameLevel centers the canvas on the nodes of the level just loaded. A
  // subflow's stored viewport is rarely meaningful on entry, so drilling in
  // always frames its contents rather than dropping the user at its origin.
  async function frameLevel() {
    await tick();
    void fitView({ duration: 200, minZoom: 1, maxZoom: 1 });
  }

  function enterLocal(node: EditorNode) {
    const sgId = node.data.subgraphId;
    if (!sgId) return;
    const g = subgraphs[sgId] ?? { nodes: [], edges: [], viewport: { x: 0, y: 0, zoom: 1 } };
    crumbs = [
      ...crumbs,
      { kind: 'local', nodeId: node.id, refId: sgId, name: node.data.label || 'Subflow', saved: { nodes, edges, viewport } },
    ];
    editor.select(null);
    loadLevel(g);
    void frameLevel();
  }

  async function enterShared(node: EditorNode) {
    const id = node.data.subflowId;
    if (!id) return;
    const entry = await subflowLibrary.getGraph(id);
    if (!entry) {
      showToast('Failed to load the shared subflow');
      return;
    }
    crumbs = [
      ...crumbs,
      { kind: 'shared', nodeId: node.id, refId: id, name: entry.name, saved: { nodes, edges, viewport } },
    ];
    editor.select(null);
    loadLevel(entry.graph);
    void frameLevel();
  }

  // goToLevel navigates up the breadcrumb path, committing local subflow
  // edits into the document on the way.
  function goToLevel(level: number) {
    while (crumbs.length > level) {
      const crumb = crumbs[crumbs.length - 1];
      if (crumb.kind === 'local') {
        subgraphs = { ...subgraphs, [crumb.refId]: serializeLevel(nodes, edges, viewport) };
      }
      nodes = crumb.saved.nodes;
      edges = crumb.saved.edges;
      viewport = crumb.saved.viewport;
      crumbs = crumbs.slice(0, -1);
    }
    editor.select(null);
    // The canvas now shows a different level: its history starts here.
    resetHistory();
  }

  // openSubflowNode drills into the referenced subflow of a node.
  function openSubflowNode(nodeId: string) {
    if (readOnly) return;
    const node = nodes.find((n) => n.id === nodeId);
    if (!node) return;
    if (node.data.subgraphId) enterLocal(node);
    else if (node.data.subflowId) void enterShared(node);
  }

  // Drill requests raised by node components and the config panel.
  $effect(() => {
    const req = $editor.drillRequestNodeId;
    if (!req) return;
    editor.clearDrillRequest();
    openSubflowNode(req);
  });

  // ---- Port resolution ----

  // portsOf resolves a placed node's ports through the shared resolver, so the
  // connection checks below see exactly what the canvas draws and what the
  // server validates. Only the referenced subflow graph is supplied here.
  function portsOf(node: EditorNode | undefined) {
    if (!node) return { inputs: [], outputs: [], known: false };
    const sub = node.data.subgraphId
      ? subgraphs[node.data.subgraphId]
      : node.data.subflowId
        ? $subflowLibrary.graphs[node.data.subflowId]?.graph
        : null;
    return nodePorts($nodeCatalog.byType[node.data.nodeType], node.data, sub);
  }

  // Resolve the outputs that carry their input's payload onward, then restyle
  // the edges that no longer pass validation. A node that only routes or delays
  // a message cannot answer for itself, so types are resolved once over the
  // whole graph and shared with the node components through the store, which is
  // also what the connection checks below read.
  //
  // Retyping a node's ports leaves its existing edges in place, so the same
  // pass marks the ones that have gone bad: the canvas says an edge is broken
  // when it breaks, not at the next save. This effect reads edges as well as
  // writing them, so it only assigns when a class actually changed.
  // linkVirtualEdges pairs each Get with whatever feeds the Set publishing its
  // name, as the virtual sources flow.PropagateTypes builds on the server: a
  // Set has no output port, so the Get's upstream is the Set's own feeder, and
  // the Get's output carries that resolved type without a drawn wire. Type
  // flow only — they are never part of the graph or the validity checks.
  function linkVirtualEdges(ns: EditorNode[], es: EditorEdge[]) {
    const sets = new Map<string, string>();
    for (const n of ns) {
      if (n.data.nodeType !== 'link-set') continue;
      const name = String(n.data.config.name ?? '');
      if (name && !sets.has(name)) sets.set(name, n.id);
    }
    const feeders = new Map<string, EditorEdge>();
    for (const e of es) {
      if (e.kind === 'value') continue;
      feeders.set(`${e.target}/${e.targetHandle ?? ''}`, e);
    }
    const out: { source: string; sourceHandle: string; target: string; targetHandle: string }[] = [];
    for (const n of ns) {
      if (n.data.nodeType !== 'link-get') continue;
      const setId = sets.get(String(n.data.config.name ?? ''));
      const feeder = setId ? feeders.get(`${setId}/in`) : undefined;
      if (feeder) {
        out.push({
          source: feeder.source,
          sourceHandle: feeder.sourceHandle ?? 'out',
          target: n.id,
          targetHandle: 'in',
        });
      }
    }
    return out;
  }

  $effect(() => {
    const sets = new Map(nodes.map((n) => [n.id, portsOf(n)]));
    const carried = propagateTypes(sets, [...edges, ...linkVirtualEdges(nodes, edges)]);
    carriedTypes.set(carried);

    const bad = invalidEdges(sets, carried, edges);
    let changed = false;
    const next = edges.map((e) => {
      const srcPort = sets.get(e.source)?.outputs.find((p) => p.id === (e.sourceHandle ?? ''));
      const cls = edgeClassFor(e, srcPort?.kind, bad.has(e.id));
      // The edge is colored by the payload type it carries: the source port's
      // resolved type, so a delay behind a parser draws the parser's color.
      // Error branches keep their semantic red instead of a type color.
      const type = carried[e.source]?.[e.sourceHandle ?? ''] ?? srcPort?.type;
      const typeColor = srcPort?.kind === 'error' ? undefined : portTypeColor(type);
      if (cls === e.class && typeColor === e.data?.typeColor) return e;
      changed = true;
      return { ...e, class: cls, data: { ...e.data, typeColor } };
    });
    if (changed) edges = next;
  });

  function outputPort(nodeId: string | null, handleId: string | null | undefined) {
    const node = nodes.find((n) => n.id === nodeId);
    const port = portsOf(node).outputs.find((p) => p.id === (handleId ?? ''));
    if (!port || !nodeId) return port;
    // A carried type is the resolved answer for this port; without it a delay
    // in front of a parser would read as undeclared and the canvas would allow
    // an edge the server then rejects.
    const carried = $carriedTypes[nodeId]?.[port.id];
    return carried ? { ...port, type: carried } : port;
  }

  function inputPort(nodeId: string | null, handleId: string | null | undefined) {
    const node = nodes.find((n) => n.id === nodeId);
    return portsOf(node).inputs.find((p) => p.id === (handleId ?? ''));
  }

  // ---- Node/edge editing ----

  // localTimeZone reports the browser's IANA zone, or "" when unavailable.
  function localTimeZone(): string {
    try {
      return Intl.DateTimeFormat().resolvedOptions().timeZone || '';
    } catch {
      return '';
    }
  }

  function addNode(type: string, position: { x: number; y: number }) {
    const meta = $nodeCatalog.byType[type];
    const config: Record<string, unknown> = {};
    for (const f of meta?.config_spec ?? []) {
      // Structured defaults (a switch's starting rules) are copied, so two
      // nodes of the same type never share one object with the catalog.
      if (f.default !== undefined && f.default !== null) {
        config[f.id] = typeof f.default === 'object' ? structuredClone(f.default) : f.default;
      }
      // Time zones default to the one the author is sitting in, which is far
      // more often right than the server's.
      else if (f.type === 'timezone') {
        const zone = localTimeZone();
        if (zone) config[f.id] = zone;
      }
      // Connections default to the first of the required kind, which is the
      // only one in the common single-broker setup.
      else if (f.type === 'connection') {
        const first = $connectionsStore.connections.find(
          (connection) => connectionMatchesField(connection, f),
        );
        if (first) config[f.id] = first.id;
      }
    }
    // Pseudo-port nodes get a unique port ID suggestion.
    if (type === 'subflow-input' || type === 'subflow-output') {
      const base = type === 'subflow-input' ? 'in' : 'out';
      const existing = new Set(
        nodes.filter((n) => n.data.nodeType === type).map((n) => String(n.data.config.port_id ?? ''))
      );
      let pid = base;
      let i = 2;
      while (existing.has(pid)) pid = `${base}${i++}`;
      config.port_id = pid;
    }
    const node: EditorNode = {
      id: `${type}_${uid()}`,
      type: canvasType(type),
      position: { x: Math.round(position.x), y: Math.round(position.y) },
      ...(defaultSizes[type] ?? {}),
      ...(type === 'group' ? { zIndex: groupZIndex } : {}),
      data: { nodeType: type, config, label: '' },
    };
    nodes = [...nodes, node];
    editor.markDirty();
    return node;
  }

  // addSubflowNode places a subflow node referencing a shared library flow.
  function addSubflowNode(subflowId: string, position: { x: number; y: number }) {
    subflowLibrary.ensureGraph(subflowId);
    const node: EditorNode = {
      id: `subflow_${uid()}`,
      type: 'flow',
      position: { x: Math.round(position.x), y: Math.round(position.y) },
      data: { nodeType: 'subflow', config: {}, label: '', subflowId },
    };
    nodes = [...nodes, node];
    editor.markDirty();
  }

  // newLocalSubflow creates a local subgraph seeded with an input and output
  // port and places a subflow node referencing it. The new node is selected but
  // not opened; drilling in is an explicit action on the node itself.
  function newLocalSubflow(position: { x: number; y: number }) {
    const sgId = uid() + uid();
    subgraphs = {
      ...subgraphs,
      [sgId]: {
        nodes: [
          {
            id: `subflow-input_${uid()}`,
            type: 'subflow-input',
            position: { x: 0, y: 0 },
            config: { port_id: 'in', port_type: 'any' },
          },
          {
            id: `subflow-output_${uid()}`,
            type: 'subflow-output',
            position: { x: 560, y: 0 },
            config: { port_id: 'out', port_type: 'any' },
          },
        ],
        edges: [],
        viewport: { x: 0, y: 0, zoom: 1 },
      },
    };
    const node: EditorNode = {
      id: `subflow_${uid()}`,
      type: 'flow',
      position: { x: Math.round(position.x), y: Math.round(position.y) },
      data: { nodeType: 'subflow', config: {}, label: '', subgraphId: sgId },
    };
    nodes = [...nodes, node];
    editor.markDirty();
    editor.select(node.id);
  }

  function updateNodeData(id: string, patch: Partial<FlowNodeData>) {
    nodes = nodes.map((n) => (n.id === id ? { ...n, data: { ...n.data, ...patch } } : n));
    editor.markDirty();
  }

  // toggleNodeParam exposes a config field as a value input socket, or hides it
  // again. Hiding drops any edge that fed it, since the port is going away.
  function toggleNodeParam(nodeId: string, fieldId: string, exposed: boolean) {
    if (readOnly) return;
    nodes = nodes.map((n) => {
      if (n.id !== nodeId) return n;
      const current = n.data.params ?? [];
      const next = exposed
        ? current.includes(fieldId)
          ? current
          : [...current, fieldId]
        : current.filter((f) => f !== fieldId);
      return { ...n, data: { ...n.data, params: next } };
    });
    if (!exposed) {
      const handle = paramPortId(fieldId);
      edges = edges.filter((e) => !(e.target === nodeId && e.targetHandle === handle));
    }
    editor.markDirty();
  }

  function setConfigValue(id: string, fieldId: string, value: unknown) {
    const previous = portsOf(nodes.find((n) => n.id === id));
    nodes = nodes.map((n) => {
      if (n.id !== id) return n;
      const config = { ...n.data.config };
      if (value === undefined) {
        delete config[fieldId];
      } else {
        config[fieldId] = value;
      }
      return { ...n, data: { ...n.data, config } };
    });
    edges = renamePortEdges(id, previous, portsOf(nodes.find((n) => n.id === id)), edges);
    pruneDanglingEdges(id);
    editor.markDirty();
  }

  // pruneDanglingEdges drops edges touching a port the node no longer has.
  // Config drives ports beyond the dynamic-port types now — a show_if-gated
  // port field (the guardrail's model) vanishes with its setting the way a
  // deleted switch rule does — so every config change checks both directions,
  // and a node whose ports cannot change simply keeps all its edges.
  function pruneDanglingEdges(nodeId: string) {
    const node = nodes.find((n) => n.id === nodeId);
    if (!node) return;
    const ports = portsOf(node);
    if (!ports.known) return;
    const outs = new Set(ports.outputs.map((p) => p.id));
    const ins = new Set(ports.inputs.map((p) => p.id));
    const kept = edges.filter(
      (e) =>
        (e.source !== nodeId || outs.has(e.sourceHandle ?? '')) &&
        (e.target !== nodeId || ins.has(e.targetHandle ?? ''))
    );
    if (kept.length !== edges.length) edges = kept;
  }

  // deleteNodes removes a set of nodes with their edges, healing the wires
  // deleted reroutes were carrying.
  function deleteNodes(ids: string[]) {
    const idSet = new Set(ids);
    const removed = nodes.filter((n) => idSet.has(n.id));
    if (removed.length === 0) return;
    const removedEdges = edges.filter((e) => idSet.has(e.source) || idSet.has(e.target));
    nodes = nodes.filter((n) => !idSet.has(n.id));
    edges = [
      ...edges.filter((e) => !idSet.has(e.source) && !idSet.has(e.target)),
      ...healedEdges(removed, removedEdges),
    ];
    editor.select(null);
    editor.markDirty();
    void cleanupOrphanSubgraphs(removed);
  }

  function deleteNode(id: string) {
    deleteNodes([id]);
  }

  // healedEdges reconnects the wires a deleted reroute was carrying: an elbow
  // is routing, not a step, so removing it should straighten the wire rather
  // than cut it. Each edge that left a deleted reroute for a surviving target
  // is traced upstream — through any other reroutes deleted in the same sweep
  // — to the surviving source, and rebuilt as a direct edge with the
  // downstream hop's kind, so a parameter binding stays one.
  function healedEdges(deletedNodes: EditorNode[], deletedEdges: EditorEdge[]): EditorEdge[] {
    const gone = new Set(deletedNodes.map((n) => n.id));
    const goneReroutes = new Set(
      deletedNodes.filter((n) => n.data.nodeType === 'reroute').map((n) => n.id)
    );
    if (goneReroutes.size === 0) return [];

    // Each deleted reroute's single incoming edge, for the upstream walk.
    const inEdge = new Map<string, EditorEdge>();
    for (const e of deletedEdges) {
      if (goneReroutes.has(e.target)) inEdge.set(e.target, e);
    }

    const healed: EditorEdge[] = [];
    for (const e of deletedEdges) {
      if (!goneReroutes.has(e.source) || gone.has(e.target)) continue;
      let cur: EditorEdge | undefined = inEdge.get(e.source);
      for (let depth = 0; cur && goneReroutes.has(cur.source) && depth < 64; depth++) {
        cur = inEdge.get(cur.source);
      }
      // A chain with no surviving source upstream has nothing to reconnect.
      if (!cur || gone.has(cur.source)) continue;
      const edge: EditorEdge = {
        id: `edge_${uid()}`,
        source: cur.source,
        sourceHandle: cur.sourceHandle,
        target: e.target,
        targetHandle: e.targetHandle,
        kind: e.kind,
      };
      edge.class = edgeClass(edge);
      healed.push(edge);
    }
    return healed;
  }

  // cleanupOrphanSubgraphs offers to drop local subgraph definitions once
  // their last referencing node is deleted.
  async function cleanupOrphanSubgraphs(removed: EditorNode[]) {
    const candidates = [...new Set(removed.map((n) => n.data.subgraphId).filter(Boolean))] as string[];
    if (candidates.length === 0) return;

    // Collect every remaining subgraph reference across the whole document.
    const doc = serializeGraph();
    const referenced = new Set<string>();
    const scan = (g: GraphDoc) => {
      for (const n of g.nodes ?? []) {
        if (n.subgraph_id) referenced.add(n.subgraph_id);
      }
    };
    scan(doc);
    for (const sub of Object.values(doc.subgraphs ?? {})) scan(sub);

    for (const sgId of candidates) {
      if (referenced.has(sgId) || !subgraphs[sgId]) continue;
      if (await confirm('This was the last node using the local subflow. Delete its definition too?')) {
        const subs = { ...subgraphs };
        delete subs[sgId];
        subgraphs = subs;
        editor.markDirty();
      }
    }
  }

  // ---- Convert to subflow ----
  //
  // Wraps a node set into a fresh local subflow: the nodes move into a new
  // subgraph, each boundary edge becomes a port declared by a pseudo-port
  // node, and a single subflow node takes the selection's place with the
  // outside wiring reattached to it.

  // convertBlocker names why a node set cannot become a subflow, or returns
  // an empty string when the conversion can go ahead.
  function convertBlocker(selected: EditorNode[], idSet: Set<string>): string {
    for (const n of selected) {
      if (isTrigger($nodeCatalog.byType[n.data.nodeType])) {
        return 'Triggers start runs and cannot move into a subflow.';
      }
      if (n.data.nodeType === 'subflow-input' || n.data.nodeType === 'subflow-output') {
        return 'Subflow port nodes declare the enclosing subflow and cannot move into a new one.';
      }
    }
    // A Set/Get pair is joined by name within one level; splitting it across
    // the boundary would sever the link.
    const names = (inSelection: boolean) =>
      new Set(
        nodes
          .filter((n) => isLinkNode(n.data.nodeType) && idSet.has(n.id) === inSelection)
          .map((n) => String(n.data.config.name ?? ''))
      );
    const outside = names(false);
    for (const name of names(true)) {
      if (outside.has(name)) {
        return `Set/Get "${name}" would be split across the subflow boundary. Select both sides or neither.`;
      }
    }
    // A subflow node is not pure, so it cannot stand in for a pulled source.
    if (edges.some((e) => e.kind === 'value' && idSet.has(e.source) && !idSet.has(e.target))) {
      return 'A node outside the selection pulls a value from inside it, and a subflow cannot supply value edges.';
    }
    return '';
  }

  // convertToSubflow performs the wrap. Inbound message edges become untyped
  // message inputs; inbound value edges become typed parameters, keeping their
  // bindings; outbound edges group by the inside port feeding them, one output
  // port each, fanning back out from the subflow node.
  function convertToSubflow(ids: string[]) {
    if (readOnly) return;
    const idSet = new Set(ids);
    const selected = nodes.filter((n) => idSet.has(n.id));
    if (selected.length === 0) return;
    const blocker = convertBlocker(selected, idSet);
    if (blocker) {
      showToast(blocker);
      return;
    }

    // Selection bounding box: inside positions are rebased against it so the
    // subgraph opens near the origin, and the subflow node lands at its centre.
    let minX = Infinity;
    let minY = Infinity;
    let maxX = -Infinity;
    let maxY = -Infinity;
    for (const n of selected) {
      const box = nodeBox(n);
      minX = Math.min(minX, box.x);
      minY = Math.min(minY, box.y);
      maxX = Math.max(maxX, box.x + box.width);
      maxY = Math.max(maxY, box.y + box.height);
    }

    const internal = edges.filter((e) => idSet.has(e.source) && idSet.has(e.target));
    const inbound = edges.filter((e) => !idSet.has(e.source) && idSet.has(e.target));
    const outbound = edges.filter((e) => idSet.has(e.source) && !idSet.has(e.target));

    const subNodeId = `subflow_${uid()}`;
    const usedPortIds = new Set<string>();
    const claimPortId = (base: string) => {
      let pid = base;
      let i = 2;
      while (usedPortIds.has(pid)) pid = `${base}${i++}`;
      usedPortIds.add(pid);
      return pid;
    };

    const pseudoNodes: EditorNode[] = [];
    const insideEdges: EditorEdge[] = [];
    const parentEdges: EditorEdge[] = [];

    // Each inbound edge declares one input port. Pseudo-node Y follows the
    // inside endpoint, which is also what orders the ports on the node.
    for (const e of inbound) {
      const tgt = nodes.find((n) => n.id === e.target)!;
      const tgtPort = inputPort(e.target, e.targetHandle);
      const isValue = e.kind === 'value' || tgtPort?.kind === 'param';
      // A message input must stay untyped: declaring any value type would turn
      // the port into a parameter, which a message edge cannot feed.
      const portType = isValue
        ? tgtPort && isValuePortType(tgtPort.type)
          ? tgtPort.type
          : 'object'
        : 'any';
      const portId = claimPortId(isValue ? (paramFieldId(e.targetHandle) ?? 'value') : 'in');
      const pseudoId = `subflow-input_${uid()}`;
      pseudoNodes.push({
        id: pseudoId,
        type: 'flow',
        position: { x: -320, y: Math.round(tgt.position.y - minY) },
        data: { nodeType: 'subflow-input', config: { port_id: portId, port_type: portType }, label: '' },
      });
      insideEdges.push({
        id: `edge_${uid()}`,
        source: pseudoId,
        sourceHandle: 'out',
        target: e.target,
        targetHandle: e.targetHandle,
        kind: isValue ? 'value' : undefined,
      });
      parentEdges.push({
        id: `edge_${uid()}`,
        source: e.source,
        sourceHandle: e.sourceHandle,
        target: subNodeId,
        targetHandle: portId,
        kind: isValue ? 'value' : undefined,
      });
    }

    // One output port per inside source port; the port keeps the resolved
    // payload type so the subflow's signature says what actually leaves it.
    const outPortByKey = new Map<string, string>();
    for (const e of outbound) {
      const key = `${e.source}|${e.sourceHandle ?? ''}`;
      let portId = outPortByKey.get(key);
      if (!portId) {
        const src = nodes.find((n) => n.id === e.source)!;
        portId = claimPortId('out');
        outPortByKey.set(key, portId);
        const pseudoId = `subflow-output_${uid()}`;
        pseudoNodes.push({
          id: pseudoId,
          type: 'flow',
          position: { x: Math.round(maxX - minX) + 320, y: Math.round(src.position.y - minY) },
          data: {
            nodeType: 'subflow-output',
            config: {
              port_id: portId,
              port_type: outputPort(e.source, e.sourceHandle)?.type ?? 'any',
            },
            label: '',
          },
        });
        insideEdges.push({
          id: `edge_${uid()}`,
          source: e.source,
          sourceHandle: e.sourceHandle,
          target: pseudoId,
          targetHandle: 'in',
        });
      }
      parentEdges.push({
        id: `edge_${uid()}`,
        source: subNodeId,
        sourceHandle: portId,
        target: e.target,
        targetHandle: e.targetHandle,
      });
    }

    // The moved nodes keep their IDs — the subgraph is its own namespace — so
    // the internal edges carry over untouched.
    const moved = selected.map((n) => ({
      ...n,
      position: { x: Math.round(n.position.x - minX), y: Math.round(n.position.y - minY) },
      selected: false,
    }));
    const sgId = uid() + uid();
    subgraphs = {
      ...subgraphs,
      [sgId]: serializeLevel([...moved, ...pseudoNodes], [...internal, ...insideEdges], {
        x: 0,
        y: 0,
        zoom: 1,
      }),
    };

    const subNode: EditorNode = {
      id: subNodeId,
      type: 'flow',
      position: { x: Math.round((minX + maxX) / 2) - 90, y: Math.round((minY + maxY) / 2) - 30 },
      data: { nodeType: 'subflow', config: {}, label: '', subgraphId: sgId },
    };
    nodes = [...nodes.filter((n) => !idSet.has(n.id)), subNode];
    edges = [
      ...edges.filter((e) => !idSet.has(e.source) && !idSet.has(e.target)),
      ...parentEdges.map((e) => ({ ...e, class: edgeClass(e) })),
    ];
    editor.select(subNode.id);
    editor.markDirty();
  }

  // ---- Connection rules ----

  // targetsParam reports whether an input handle is a parameter port, which
  // is what decides an edge's kind. Exposed config fields carry the param:
  // prefix, but a subflow's value-typed inputs are params under their bare
  // port ID, so the resolved port is the authority.
  function targetsParam(nodeId: string | null, handleId: string | null | undefined): boolean {
    if (paramFieldId(handleId) !== null) return true;
    return inputPort(nodeId, handleId)?.kind === 'param';
  }

  const isValidConnection: IsValidConnection<EditorEdge> = (conn) => {
    const src = outputPort(conn.source, conn.sourceHandle);
    const tgt = inputPort(conn.target, conn.targetHandle);
    if (!src || !tgt) return false;
    // A param-kind output (a tool's argument port) supplies a value that
    // exists only while a call runs; it can only feed a parameter port. The
    // tool's call output is the reverse: it delivers the call message and
    // cannot supply a value.
    const bindsParam = paramFieldId(conn.targetHandle) !== null || tgt.kind === 'param';
    if (src.kind === 'param' && !bindsParam) return false;
    const srcNode = nodes.find((n) => n.id === conn.source);
    if (srcNode?.data.nodeType === 'tool' && conn.sourceHandle === 'call' && bindsParam) return false;
    // A value edge supplies a config field of a declared type, so an
    // undeclared source is a real problem there. A message edge only carries
    // what the run produces, and "any" on it means not knowable until then.
    const ok =
      paramFieldId(conn.targetHandle) !== null || tgt.kind === 'param'
        ? compatible(src.type, tgt.type)
        : messageCompatible(src.type, tgt.type);
    if (!ok) return false;
    // An input port accepts at most one edge, unless it is a multi parameter
    // port — several wires is what those are for.
    if (
      !tgt.multi &&
      edges.some((e) => e.target === conn.target && (e.targetHandle ?? '') === (conn.targetHandle ?? ''))
    ) {
      return false;
    }
    return true;
  };

  function handleBeforeConnect(connection: Connection): EditorEdge {
    editor.markDirty();
    // Landing on a parameter port makes this a value edge; anything else is
    // the ordinary message path.
    const isParam = targetsParam(connection.target, connection.targetHandle);
    const edge: EditorEdge = {
      id: `edge_${uid()}`,
      source: connection.source,
      sourceHandle: connection.sourceHandle,
      target: connection.target,
      targetHandle: connection.targetHandle,
      kind: isParam ? 'value' : undefined,
    };
    edge.class = edgeClass(edge);
    return edge;
  }

  // ---- Drag and drop from palette ----

  function handleDragOver(event: DragEvent) {
    event.preventDefault();
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
  }

  function handleDrop(event: DragEvent) {
    event.preventDefault();
    if (readOnly) return;
    const position = screenToFlowPosition({ x: event.clientX, y: event.clientY });
    const subflowId = event.dataTransfer?.getData('application/karakuri-subflow');
    if (subflowId) {
      addSubflowNode(subflowId, position);
      return;
    }
    if (event.dataTransfer?.getData('application/karakuri-new-subflow')) {
      newLocalSubflow(position);
      return;
    }
    const type = event.dataTransfer?.getData('application/karakuri-node');
    if (!type) return;
    addNode(type, position);
  }

  let remoteConflict = $state(false);
  let disposed = false;
  let refreshGeneration = 0;
  onDestroy(() => { disposed = true; });
  onMount(() => watchDocument('flow', flow.id, async () => {
    if (inFlight) return false;
    const generation = ++refreshGeneration;
    const updated = await api.flows.get<FlowDetail>(flow.id);
    if (disposed || generation !== refreshGeneration) return true;
    if (inFlight) return false;
    if ($editor.dirty) {
      if (updated.draft_revision !== $editor.flow?.draft_revision) remoteConflict = true;
      return true;
    }
    applyRemoteDraft(updated);
    return true;
  }, (error: any) => showToast(error?.message || 'could not refresh the draft')));

  function applyRemoteDraft(updated: FlowDetail) {
    if (updated.draft_revision !== $editor.flow?.draft_revision) {
      const wasDrilled = crumbs.length > 0;
      flushHistory();
      crumbs = [];
      subgraphs = updated.graph.subgraphs ?? {};
      nodes = toEditorNodes(updated.graph);
      edges = toEditorEdges(updated.graph).map((e) => ({ ...e, class: edgeClass(e) }));
      editor.select(null);
      if (wasDrilled) resetHistory();
    }
    editor.acceptDraft(updated);
  }

  async function refreshAssistantDraft() {
    const generation = ++refreshGeneration;
    const updated = await api.flows.get<FlowDetail>(flow.id);
    if (!disposed && generation === refreshGeneration) applyRemoteDraft(updated);
  }

  async function reloadSharedDraft() {
    if (!await confirm('Discard your pending edits and load the shared draft?')) return;
    if (saveTimer) { clearTimeout(saveTimer); saveTimer = null; }
    if (inFlight) await inFlight;
    await refreshAssistantDraft();
    resetHistory();
    remoteConflict = false;
  }

  // ---- Save / deploy / toolbar ----

  let current = $derived($editor.flow ?? flow);
  let selectedNode = $derived(nodes.find((n) => n.id === $editor.selectedNodeId));
  // Validation problems are keyed by "node:<id>"/"edge:<id>". Resolve node
  // keys to the label shown on the canvas so the panel names what the user
  // sees rather than an internal id.
  let problemList = $derived(
    Object.entries($editor.problems ?? {}).map(([location, message]) => {
      const nodeId = location.startsWith('node:') ? location.slice(5) : '';
      const node = nodeId ? nodes.find((n) => n.id === nodeId) : undefined;
      const name = node
        ? node.data.label || $nodeCatalog.byType[node.data.nodeType]?.label || node.data.nodeType
        : location;
      return { location, message, nodeId: node ? nodeId : '', name };
    }),
  );

  // ---- Autosave ----
  //
  // The draft is bookkeeping, not a commitment: only Deploy changes what runs,
  // and draft saves are lenient, so an edit persists on its own once the author
  // pauses. The debounce restarts on every durable change, which is what keeps
  // one gesture to one request, and anything that reads the *stored* draft — a
  // test run, a deploy, leaving the editor — flushes first.

  const autosaveDebounceMs = 1000;
  let saveTimer: ReturnType<typeof setTimeout> | null = null;
  let inFlight: Promise<boolean> | null = null;

  // save persists the canvas, one request at a time. Overlapping PUTs of the
  // same document could land out of order, so a second call waits.
  async function save(): Promise<boolean> {
    // Every waiter wakes when one request lands, so loop: two callers let
    // through at once would be the overlapping PUTs this serializes against.
    while (inFlight) await inFlight;
    if (remoteConflict) return false;
    const attempt = editor.saveGraph(serializeGraph());
    inFlight = attempt;
    try {
      return await attempt;
    } finally {
      inFlight = null;
    }
  }

  // flushSave commits a pending save immediately and reports whether the
  // stored draft now matches the canvas.
  async function flushSave(): Promise<boolean> {
    if (saveTimer) {
      clearTimeout(saveTimer);
      saveTimer = null;
    }
    if (inFlight) await inFlight;
    if (!$editor.dirty && !$editor.saveError) return true;
    if (readOnly) return true;
    return save();
  }

  // Arm the debounce on every durable change while the draft is dirty. A
  // drilled shared subflow is somebody else's document and never saves here.
  $effect(() => {
    historySnapshot();
    // A timer armed while a save was in flight would repeat that save once
    // it lands clean, so a clean or conflicted draft disarms it.
    if (!$editor.dirty || remoteConflict) {
      if (saveTimer) {
        clearTimeout(saveTimer);
        saveTimer = null;
      }
      return;
    }
    if (readOnly) return;
    if (saveTimer) clearTimeout(saveTimer);
    saveTimer = setTimeout(() => {
      saveTimer = null;
      void save();
    }, autosaveDebounceMs);
  });

  // A pending save would die with the component, so commit it on the way out.
  onDestroy(() => {
    if (!saveTimer) return;
    clearTimeout(saveTimer);
    saveTimer = null;
    void save();
  });

  async function deploy() {
    // Deploy freezes what is stored, so a draft that will not save must not be
    // deployed under the author's nose.
    if (!(await flushSave())) return;
    const ok = await editor.deploy();
    // A redeployed subflow presents a fresh interface to other flows.
    if (ok && current.is_subflow) subflowLibrary.invalidate(current.id);
  }

  // leaveEditor flushes before navigating, and only asks when the draft cannot
  // be stored — that is the one case where walking away loses work.
  async function leaveEditor(to: string) {
    if (!(await flushSave()) && !window.confirm('The last change could not be saved. Leave anyway?')) return;
    navigate(to);
  }

  // openSharedFlow navigates to a shared subflow's own editor.
  function openSharedFlow(flowId: string) {
    void leaveEditor(`/flows/${flowId}`);
  }

  // ---- Promote to shared library ----

  let promoteTarget = $state<EditorNode | null>(null);
  let promoteName = $state('');
  let promoting = $state(false);
  let promoteError = $state('');

  function openPromote(node: EditorNode) {
    promoteTarget = node;
    promoteName = node.data.label || '';
    promoteError = '';
  }

  // reinitFrom rebuilds the whole canvas state from a freshly loaded flow.
  function reinitFrom(detail: FlowDetail) {
    crumbs = [];
    subgraphs = { ...(detail.graph.subgraphs ?? {}) };
    loadLevel(detail.graph);
    editor.select(null);
  }

  async function doPromote() {
    const sgId = promoteTarget?.data.subgraphId;
    if (!sgId) return;
    promoting = true;
    promoteError = '';
    try {
      if (!(await flushSave())) return;
      await api.flows.promoteSubgraph(current.id, sgId, promoteName);
      await subflowLibrary.load();
      const detail = await editor.load(current.id);
      if (detail) reinitFrom(detail);
      promoteTarget = null;
    } catch (e: any) {
      promoteError = e?.message || 'Failed to promote subflow';
    } finally {
      promoting = false;
    }
  }

  // importAsLocal converts a shared subflow reference into an editable local
  // copy of its deployed graph.
  async function importAsLocal(node: EditorNode) {
    const subflowId = node.data.subflowId;
    if (!subflowId) return;
    try {
      if (!(await flushSave())) return;
      const res = await api.flows.importSubflow<{ subgraph_id: string }>(current.id, subflowId);
      // The server adds the subgraph but leaves the node a shared reference,
      // so this shell has to rewire it and save. editor.load would remount the
      // editor from the unrewired draft, and that shell's autosave would then
      // overwrite the rewired one. Bumping the generation drops any refresh
      // fetched before the import.
      ++refreshGeneration;
      const detail = await api.flows.get<FlowDetail>(current.id);
      if (disposed) return;
      editor.acceptDraft(detail);
      subgraphs = { ...(detail.graph.subgraphs ?? {}) };
      nodes = nodes.map((n) =>
        n.id === node.id
          ? { ...n, data: { ...n.data, subflowId: undefined, subgraphId: res.subgraph_id } }
          : n
      );
      editor.markDirty();
      await flushSave();
    } catch (e: any) {
      showToast(e?.message || 'Failed to import subflow');
    }
  }

  // ---- Side panel visibility ----
  //
  // Both side panels can be put away, and the choice follows the account
  // rather than the browser, so it is kept in the profile preferences. The
  // panel reads the stored value directly: the write is optimistic, so there
  // is no local copy to fall out of step with it. The palette hides outright
  // and is reopened from the toolbar; the inspector collapses to a rail, which
  // is what brings it back.

  let paletteHidden = $derived($preferences.editor_palette_hidden === true);
  let inspectorHidden = $derived($preferences.editor_inspector_hidden === true);

  // A double-click on a node, or asking for run details, opens the inspector
  // without changing the stored preference: collapsing it again leaves it
  // hidden. peekNodeId records which node borrowed the space, so the peek can
  // end when that node is deselected; a peek opened for a run is not tied to a
  // node and stays until it is closed by hand.
  let inspectorPeek = $state(false);
  let peekNodeId = $state<string | null>(null);
  let inspectorOpen = $derived(!inspectorHidden || inspectorPeek);

  function setPaletteHidden(hidden: boolean) {
    void auth.setPreferences({ editor_palette_hidden: hidden });
  }

  function endPeek() {
    inspectorPeek = false;
    peekNodeId = null;
  }

  function setInspectorHidden(hidden: boolean) {
    endPeek();
    void auth.setPreferences({ editor_inspector_hidden: hidden });
  }

  // hideInspector closes the panel. While peeking it only ends the peek, so a
  // node opened by double-click can be dismissed without hiding the panel for
  // good.
  function hideInspector() {
    if (inspectorPeek) {
      endPeek();
      return;
    }
    setInspectorHidden(true);
  }

  // peekInspector selects a node and opens the panel for it, borrowing the
  // space when the panel is hidden.
  function peekInspector(nodeId: string) {
    editor.select(nodeId);
    if (inspectorHidden) {
      inspectorPeek = true;
      peekNodeId = nodeId;
    }
  }

  // A peek taken for a node lasts as long as that node stays selected:
  // clicking off it on the canvas gives the space back.
  $effect(() => {
    const selected = $editor.selectedNodeId;
    if (inspectorPeek && peekNodeId !== null && selected !== peekNodeId) endPeek();
  });

  // ---- Test run ----

  // Right panel tab; the Run tab auto-activates when a new run starts.
  let panelTab = $state<'config' | 'run'>('config');

  // showRun switches to the Run tab and borrows the space when the panel is
  // hidden: selecting the tab is useless if the panel it lives in is collapsed.
  // The peek is not tied to a node, so a later deselect leaves it open.
  function showRun() {
    panelTab = 'run';
    if (inspectorHidden) inspectorPeek = true;
    peekNodeId = null;
  }

  // Transient toast for test-run errors.
  let toast = $state('');
  let toastTimer: ReturnType<typeof setTimeout> | null = null;

  function showToast(message: string) {
    toast = message;
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(() => (toast = ''), 6000);
  }

  // Ticking clock for the run chip's relative time.
  let now = $state(Date.now());
  $effect(() => {
    const t = setInterval(() => (now = Date.now()), 10000);
    return () => clearInterval(t);
  });

  let runInfo = $derived($runState.run);
  let running = $derived(runInfo?.status === 'running');

  // ---- Test runs ----
  //
  // A run starts at a trigger, so the button that starts a test one lives on
  // the trigger node itself. Which nodes qualify mirrors the backend's rules,
  // and a node component cannot answer that for itself, so it is resolved for
  // the whole canvas here and published for FlowNode to read.
  //
  // Only top-level nodes can be triggers: drilled into a subflow there is
  // nothing to start, so the map is empty and no node shows a button.
  let testTargetMap = $derived.by(() => {
    const out: Record<string, string> = {};
    if (!canEdit || current.is_subflow || crumbs.length > 0) return out;
    let candidates = nodes.filter((n) => isTrigger($nodeCatalog.byType[n.data.nodeType]));
    // A graph with no trigger never fires on its own, but a pure source node
    // (a value, a file read — output-only, nothing to wait for) can still seed
    // a test run, which mirrors the backend's rule. Link nodes are pure
    // sources on paper but are wiring spliced out before a run, so a run
    // cannot start at one.
    if (candidates.length === 0) {
      candidates = nodes.filter((n) => {
        const meta = $nodeCatalog.byType[n.data.nodeType];
        // A faceplate holds settings, not a value worth starting a run from.
        return meta?.pure && (meta.inputs ?? []).length === 0 && !isLinkNode(n.data.nodeType) && n.data.nodeType !== 'faceplate';
      });
    }
    const blocked = running
      ? 'A run is already in progress'
      : $runState.starting
        ? 'Starting a run...'
        : $editor.saving
          ? 'Saving...'
          : '';
    for (const n of candidates) out[n.id] = blocked;
    return out;
  });

  $effect(() => testTargets.set(testTargetMap));

  // Run requests raised by the trigger nodes. A trigger with no stored test
  // parameters cannot be seeded on its own, so the payload prompt is opened
  // rather than refusing the run.
  $effect(() => {
    const req = $editor.testRunRequest;
    if (!req) return;
    editor.clearTestRunRequest();
    if (testTargetMap[req.nodeId] !== '') return;
    const node = nodes.find((n) => n.id === req.nodeId);
    if (req.withValue || (node && needsTestValue(node))) openTestValue(req.nodeId);
    else void testRun(req.nodeId);
  });

  // needsTestValue reports whether a trigger has nothing to be seeded with,
  // mirroring the backend's TEST_PARAMS_MISSING rule.
  function needsTestValue(node: EditorNode): boolean {
    const meta = $nodeCatalog.byType[node.data.nodeType];
    if (meta?.test_without_params) return false;
    if (node.data.nodeType === 'manual') return false;
    return node.data.config?.test_params == null;
  }

  async function testRun(nodeId: string, override?: { payload?: unknown; topic?: string }) {
    // Test runs execute the stored draft; commit pending edits first.
    if (!(await flushSave())) return;
    try {
      await runState.startTestRun(current.id, {
        triggerNodeId: nodeId,
        payload: override?.payload,
        topic: override?.topic,
      });
      showRun();
    } catch (e: any) {
      if (e?.code === 'TEST_PARAMS_MISSING') {
        showToast('The trigger needs a test value: alt-click its run button, or add test_params in its Testing section.');
      } else {
        showToast(e?.message || 'Failed to start test run');
      }
    }
  }

  // ---- Test with a value ----
  //
  // Any trigger can be exercised against a payload chosen at run time, so a
  // webhook flow can be tried with a body and a schedule with whatever the
  // author wants to see, without editing the node first.

  let showTestValue = $state(false);
  let testValueNodeId = $state('');
  let testValueText = $state('{}');
  let testValueTopic = $state('');
  let testValueError = $state('');

  // openTestValue prefills from whatever the run would be seeded with anyway:
  // a value node's payload, or the trigger's stored test_params.
  function openTestValue(nodeId: string) {
    const node = nodes.find((n) => n.id === nodeId);
    let seed: unknown = {};
    if (node) {
      const cfg = node.data.config ?? {};
      if (cfg.test_params !== undefined) seed = cfg.test_params;
      else if (cfg.payload !== undefined) seed = cfg.payload;
    }
    testValueNodeId = nodeId;
    testValueText =
      typeof seed === 'string' ? seed : JSON.stringify(seed ?? {}, null, 2);
    testValueTopic = '';
    testValueError = '';
    showTestValue = true;
  }

  async function runWithTestValue() {
    let payload: unknown;
    try {
      payload = JSON.parse(testValueText);
    } catch {
      testValueError = 'Not valid JSON. Wrap text in quotes to send a string.';
      return;
    }
    showTestValue = false;
    await testRun(testValueNodeId, { payload, topic: testValueTopic || undefined });
  }

  // ---- Run from node ----

  // Right-click context menu offering a partial run from the chosen node,
  // seeded by the last run's stored outputs.
  // ---- Node placer ----
  //
  // Opened by double-clicking empty canvas, or by releasing a connection over
  // it. The released case carries the dragged port so the list only offers
  // nodes that can actually connect, and wires the pick up on placement.

  type PlacerState = {
    // Screen position for the popup.
    x: number;
    y: number;
    // Canvas position the new node lands at.
    flowX: number;
    flowY: number;
    filter: PlacerFilter | null;
    // The connection to complete once a node is picked.
    pending: PendingConnection | null;
    // The edge to splice the picked node into, from the edge menu's Add Node.
    insertEdgeId?: string | null;
  };
  type PendingConnection = {
    nodeId: string;
    handleId: string;
    // fromOutput is true when the drag started at an output, so the new node
    // is wired downstream of it.
    fromOutput: boolean;
    portType: string;
    // value is true when the drag started at a parameter port, so the new node
    // supplies a config field over a value edge.
    value: boolean;
  };

  let placer = $state<PlacerState | null>(null);
  let pendingConnection: PendingConnection | null = null;

  // openPlacer positions the popup so it stays on screen near the pointer. It
  // is fixed-positioned like the context menu, so these are client coordinates.
  function openPlacer(clientX: number, clientY: number, pending: PendingConnection | null) {
    const flow = screenToFlowPosition({ x: clientX, y: clientY });
    const width = 300;
    const height = 380;
    placer = {
      x: Math.max(8, Math.min(clientX, window.innerWidth - width - 8)),
      y: Math.max(8, Math.min(clientY, window.innerHeight - height - 8)),
      flowX: flow.x,
      flowY: flow.y,
      filter: pending
        ? {
            portType: pending.portType,
            need: pending.fromOutput ? 'input' : 'output',
            value: pending.value,
          }
        : null,
      pending,
    };
  }

  // placeFits reports whether a port on a freshly placed node satisfies the
  // port the drag came from. A value edge supplies a config field of a declared
  // type, so an undeclared output is no good there; the message path takes one.
  function placeFits(pending: PendingConnection, type: string): boolean {
    if (pending.fromOutput) return messageCompatible(pending.portType, type);
    return pending.value
      ? compatible(type, pending.portType)
      : messageCompatible(type, pending.portType);
  }

  // tunedOutputType retypes a freshly placed node so its output satisfies the
  // port the drag came from. A node that reads authored text cannot know which
  // type the author meant, and the wire says it: a value node dropped on a
  // Number parameter should land holding a number rather than the object its
  // default emits. Node types without an output type setting are left alone,
  // and so is one whose options all miss.
  function tunedOutputType(node: EditorNode, pending: PendingConnection) {
    const field = $nodeCatalog.byType[node.data.nodeType]?.config_spec?.find(
      (f) => f.id === 'output_format'
    );
    for (const opt of field?.options ?? []) {
      const config = { ...node.data.config, output_format: opt.value };
      const candidate = { ...node, data: { ...node.data, config } };
      const port = portsOf(candidate).outputs.find((p) => placeFits(pending, p.type));
      if (port) return { config, port };
    }
    return null;
  }

  // placeNode creates the picked node and, when the placer was opened from a
  // released connection, joins it to the port the drag came from.
  function placeNode(type: string) {
    const state = placer;
    placer = null;
    if (!state || readOnly) return;
    // Nudge the node up-left so it lands centred on the pointer rather than
    // hanging off it.
    const node = addNode(type, { x: state.flowX - 75, y: state.flowY - 20 });
    if (!node) return;
    if (state.insertEdgeId) {
      insertNodeIntoEdge(node, state.insertEdgeId);
      return;
    }
    if (!state.pending) return;

    const pending = state.pending;
    const ports = portsOf(node);
    const wanted = pending.fromOutput ? ports.inputs : ports.outputs;
    let port = wanted.find((p) => placeFits(pending, p.type));
    // Nothing fit as the node's defaults left it, so try the output types it
    // offers before giving up and leaving the two ends unwired.
    if (!port && !pending.fromOutput) {
      const tuned = tunedOutputType(node, pending);
      if (tuned) {
        updateNodeData(node.id, { config: tuned.config });
        port = tuned.port;
      }
    }
    if (!port) return;

    const edge: EditorEdge = pending.fromOutput
      ? {
          id: `edge_${uid()}`,
          source: pending.nodeId,
          sourceHandle: pending.handleId,
          target: node.id,
          targetHandle: port.id,
        }
      : {
          id: `edge_${uid()}`,
          source: node.id,
          sourceHandle: port.id,
          target: pending.nodeId,
          targetHandle: pending.handleId,
        };
    if (targetsParam(edge.target, edge.targetHandle)) edge.kind = 'value';
    edge.class = edgeClass(edge);
    edges = [...edges, edge];
    editor.markDirty();
  }

  // handleConnectStart records which port a drag came from, so a release over
  // empty canvas knows what the new node has to accept.
  function handleConnectStart(_event: unknown, params: { nodeId?: string | null; handleId?: string | null; handleType?: string | null }) {
    const nodeId = params?.nodeId ?? '';
    const handleId = params?.handleId ?? '';
    if (!nodeId) {
      pendingConnection = null;
      return;
    }
    const fromOutput = params?.handleType === 'source';
    const port = fromOutput ? outputPort(nodeId, handleId) : inputPort(nodeId, handleId);
    if (!port) {
      pendingConnection = null;
      return;
    }
    pendingConnection = {
      nodeId,
      handleId,
      fromOutput,
      portType: port.type,
      value: !fromOutput && targetsParam(nodeId, handleId),
    };
  }

  // handleConnectEnd opens the placer when a connection was dropped on empty
  // canvas. A drop on a handle has already been turned into an edge. The DOM
  // target alone cannot tell those apart: the pane receives the pointer events
  // for the whole drag, so a release over a node's body still reports the
  // pane. The connection state says what the drop actually landed on.
  function handleConnectEnd(
    event: MouseEvent | TouchEvent,
    connectionState: { isValid: boolean | null; toNode: unknown }
  ) {
    const pending = pendingConnection;
    pendingConnection = null;
    if (!pending || readOnly) return;
    // A release on or near a handle was a connection attempt, and a release
    // over a node was aimed at that node; neither asks for a new one.
    if (connectionState.isValid !== null || connectionState.toNode) return;
    const target = (event as MouseEvent)?.target as HTMLElement | null;
    if (!target?.classList?.contains('svelte-flow__pane')) return;
    const point = 'clientX' in event ? event : (event as TouchEvent).changedTouches?.[0];
    if (!point) return;
    openPlacer(point.clientX, point.clientY, pending);
  }

  // handleCanvasDoubleClick opens the placer on empty canvas and the inspector
  // on a node. Double-click to zoom is turned off on the flow itself so these
  // are the only meanings it has. Svelte Flow has no node double-click event,
  // so the node is resolved from the wrapper's data-id.
  function handleCanvasDoubleClick(event: MouseEvent) {
    const target = event.target as HTMLElement | null;

    const wrapper = target?.closest?.('.svelte-flow__node') as HTMLElement | null;
    if (wrapper?.dataset.id) {
      event.preventDefault();
      peekInspector(wrapper.dataset.id);
      return;
    }

    if (readOnly) return;
    if (!target?.classList?.contains('svelte-flow__pane')) return;
    event.preventDefault();
    openPlacer(event.clientX, event.clientY, null);
  }

  // ---- Edge actions ----
  //
  // Every edge carries a midpoint dot (FlowEdge) that opens this menu: insert
  // a node into the wire, add a reroute elbow, convert the wire to a Set/Get
  // pair, or delete it. The routing nodes are wiring rather than steps — the
  // engine splices them into direct edges before a run — so all of these are
  // pure canvas edits.

  let edgeMenu = $derived($editor.edgeMenuRequest);
  let edgeMenuEdge = $derived(edgeMenu ? edges.find((e) => e.id === edgeMenu!.edgeId) : undefined);

  function closeEdgeMenu() {
    editor.clearEdgeMenuRequest();
  }

  function deleteEdgeById(edgeId: string) {
    closeEdgeMenu();
    if (readOnly) return;
    edges = edges.filter((e) => e.id !== edgeId);
    editor.markDirty();
  }

  // rewireThrough replaces an edge with the two hops through a freshly placed
  // routing node. The downstream hop keeps the edge's kind, so a parameter
  // binding stays a parameter binding across the detour.
  function rewireThrough(edge: EditorEdge, nodeId: string) {
    const upstream: EditorEdge = {
      id: `edge_${uid()}`,
      source: edge.source,
      sourceHandle: edge.sourceHandle,
      target: nodeId,
      targetHandle: 'in',
    };
    const downstream: EditorEdge = {
      id: `edge_${uid()}`,
      source: nodeId,
      sourceHandle: 'out',
      target: edge.target,
      targetHandle: edge.targetHandle,
      kind: edge.kind,
    };
    upstream.class = edgeClass(upstream);
    downstream.class = edgeClass(downstream);
    edges = [...edges.filter((e) => e.id !== edge.id), upstream, downstream];
    editor.markDirty();
  }

  // rerouteOrigin is the node position that centres a reroute dot on a screen
  // point; the dot is 22px square.
  function rerouteOrigin(clientX: number, clientY: number) {
    const pos = screenToFlowPosition({ x: clientX, y: clientY });
    return { x: pos.x - 11, y: pos.y - 11 };
  }

  function addRerouteOnEdge(edgeId: string, clientX: number, clientY: number) {
    closeEdgeMenu();
    const edge = edges.find((e) => e.id === edgeId);
    if (!edge || readOnly) return;
    const node = addNode('reroute', rerouteOrigin(clientX, clientY));
    rewireThrough(edge, node.id);
  }

  // Dragging an edge's dot bends the wire in one gesture: the elbow is
  // inserted where the press landed and follows the pointer until it is
  // released. Svelte Flow's own node dragging cannot pick a node up that did
  // not exist when the pointer went down, so the move is driven from here.
  $effect(() => {
    const req = $editor.edgeRerouteRequest;
    if (!req) return;
    editor.clearEdgeRerouteRequest();
    startRerouteDrag(req.edgeId, req.x, req.y);
  });

  function startRerouteDrag(edgeId: string, clientX: number, clientY: number) {
    closeEdgeMenu();
    const edge = edges.find((e) => e.id === edgeId);
    if (!edge || readOnly) return;
    const node = addNode('reroute', rerouteOrigin(clientX, clientY));
    rewireThrough(edge, node.id);

    const move = (event: PointerEvent) => {
      // A press released before the hand-off leaves no button down; the elbow
      // stays where it landed rather than following the pointer around.
      if (event.buttons === 0) {
        end();
        return;
      }
      const position = rerouteOrigin(event.clientX, event.clientY);
      nodes = nodes.map((n) => (n.id === node.id ? { ...n, position } : n));
    };
    const end = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', end);
      window.removeEventListener('pointercancel', end);
      editor.markDirty();
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', end);
    window.addEventListener('pointercancel', end);
  }

  // openInsertPlacer opens the node placer primed to splice the pick into the
  // edge, filtered to nodes that can accept what the wire carries.
  function openInsertPlacer(edgeId: string, clientX: number, clientY: number) {
    closeEdgeMenu();
    const edge = edges.find((e) => e.id === edgeId);
    if (!edge || readOnly) return;
    const src = outputPort(edge.source, edge.sourceHandle);
    openPlacer(clientX, clientY, null);
    if (placer) {
      placer = {
        ...placer,
        filter: src ? { portType: src.type, need: 'input', value: false } : null,
        insertEdgeId: edgeId,
      };
    }
  }

  // insertNodeIntoEdge splices a freshly placed node into an existing edge:
  // the old source feeds the node's first compatible input, and the node's
  // first compatible output feeds the old target. A side nothing fits stays
  // unwired rather than wired wrong.
  function insertNodeIntoEdge(node: EditorNode, edgeId: string) {
    const edge = edges.find((e) => e.id === edgeId);
    if (!edge) return;
    const ports = portsOf(node);
    const src = outputPort(edge.source, edge.sourceHandle);
    const tgt = inputPort(edge.target, edge.targetHandle);
    const inPort = ports.inputs.find(
      (p) => p.kind !== 'param' && (!src || messageCompatible(src.type, p.type))
    );
    const outPort = ports.outputs.find(
      (p) => p.kind !== 'error' && (!tgt || messageCompatible(p.type, tgt.type))
    );
    const created: EditorEdge[] = [];
    if (inPort) {
      created.push({
        id: `edge_${uid()}`,
        source: edge.source,
        sourceHandle: edge.sourceHandle,
        target: node.id,
        targetHandle: inPort.id,
      });
    }
    if (outPort) {
      created.push({
        id: `edge_${uid()}`,
        source: node.id,
        sourceHandle: outPort.id,
        target: edge.target,
        targetHandle: edge.targetHandle,
        kind: edge.kind,
      });
    }
    for (const e of created) e.class = edgeClass(e);
    edges = [...edges.filter((e) => e.id !== edgeId), ...created];
    editor.markDirty();
  }

  // linkNameFor names a converted pair after what travels the wire — the
  // resolved payload type when there is one, else the source node type — and
  // keeps it unique among the level's Set nodes.
  function linkNameFor(edge: EditorEdge): string {
    const src = outputPort(edge.source, edge.sourceHandle);
    const srcNode = nodes.find((n) => n.id === edge.source);
    const raw =
      src && src.type !== 'any' ? src.type : (srcNode?.data.nodeType ?? 'value');
    const base = raw.toUpperCase().replace(/[^A-Z0-9]+/g, '_');
    const used = new Set(
      nodes
        .filter((n) => n.data.nodeType === 'link-set')
        .map((n) => String(n.data.config.name ?? ''))
    );
    if (!used.has(base)) return base;
    let i = 2;
    while (used.has(`${base}_${i}`)) i++;
    return `${base}_${i}`;
  }

  // convertEdgeToLink replaces a drawn wire with a named Set/Get pair: the Set
  // lands beside the source, the Get beside the target, and the name carries
  // the connection between them.
  function convertEdgeToLink(edgeId: string) {
    closeEdgeMenu();
    const edge = edges.find((e) => e.id === edgeId);
    if (!edge || readOnly) return;
    const srcNode = nodes.find((n) => n.id === edge.source);
    const tgtNode = nodes.find((n) => n.id === edge.target);
    if (!srcNode || !tgtNode) return;
    const name = linkNameFor(edge);

    const setNode = addNode('link-set', {
      x: srcNode.position.x + (srcNode.measured?.width ?? srcNode.width ?? 160) + 60,
      y: srcNode.position.y,
    });
    const getNode = addNode('link-get', {
      x: tgtNode.position.x - 220,
      y: tgtNode.position.y,
    });
    updateNodeData(setNode.id, { config: { ...setNode.data.config, name } });
    updateNodeData(getNode.id, { config: { ...getNode.data.config, name } });

    // Wire source → Set; Get → target. The Set→Get hop is the name itself.
    const upstream: EditorEdge = {
      id: `edge_${uid()}`,
      source: edge.source,
      sourceHandle: edge.sourceHandle,
      target: setNode.id,
      targetHandle: 'in',
    };
    const downstream: EditorEdge = {
      id: `edge_${uid()}`,
      source: getNode.id,
      sourceHandle: 'out',
      target: edge.target,
      targetHandle: edge.targetHandle,
      kind: edge.kind,
    };
    upstream.class = edgeClass(upstream);
    downstream.class = edgeClass(downstream);
    edges = [...edges.filter((e) => e.id !== edge.id), upstream, downstream];
    editor.markDirty();
  }

  // ---- Group capture ----
  //
  // A group frame carries the nodes standing on it. Membership is geometric and
  // worked out when the drag starts rather than stored: a node belongs to the
  // group it sits inside, and dragging it out of the frame is all it takes to
  // leave. Nothing in the graph document records the relationship, so a group
  // deleted, resized, or moved away never leaves a stale membership behind.

  // captured maps a group being dragged to the nodes it picked up and the
  // position it was last seen at, which is what the per-frame delta comes from.
  let captured = new Map<string, { ids: string[]; x: number; y: number }>();

  // nodeBox returns a node's canvas rectangle. Measured dimensions are what the
  // node actually occupies; a node that has not been measured yet is skipped
  // rather than treated as a point, which would sweep it into any frame.
  function nodeBox(n: EditorNode) {
    const width = n.measured?.width ?? n.width ?? 0;
    const height = n.measured?.height ?? n.height ?? 0;
    return { x: n.position.x, y: n.position.y, width, height };
  }

  // insideGroup reports whether a node sits wholly within a group's frame.
  function insideGroup(n: EditorNode, frame: ReturnType<typeof nodeBox>) {
    const box = nodeBox(n);
    if (box.width === 0 || box.height === 0) return false;
    return (
      box.x >= frame.x &&
      box.y >= frame.y &&
      box.x + box.width <= frame.x + frame.width &&
      box.y + box.height <= frame.y + frame.height
    );
  }

  function handleNodeDragStart({ nodes: dragged }: { nodes: EditorNode[] }) {
    captured = new Map();
    if (readOnly) return;
    const moving = new Set(dragged.map((n) => n.id));
    for (const group of dragged) {
      if (group.data.nodeType !== 'group') continue;
      const frame = nodeBox(group);
      const ids = nodes
        .filter((n) => !moving.has(n.id) && insideGroup(n, frame))
        .map((n) => n.id);
      captured.set(group.id, { ids, x: group.position.x, y: group.position.y });
    }
  }

  // handleNodeDrag moves what each dragged group picked up by the same step the
  // frame just took. The step is measured against the group's own position
  // rather than the pointer, so it stays correct under snapping and zoom.
  function handleNodeDrag() {
    if (captured.size === 0) return;
    const moves = new Map<string, { dx: number; dy: number }>();
    for (const group of nodes) {
      const state = captured.get(group.id);
      if (!state) continue;
      const dx = group.position.x - state.x;
      const dy = group.position.y - state.y;
      if (dx === 0 && dy === 0) continue;
      state.x = group.position.x;
      state.y = group.position.y;
      for (const id of state.ids) moves.set(id, { dx, dy });
    }
    if (moves.size === 0) return;
    nodes = nodes.map((n) => {
      const move = moves.get(n.id);
      return move
        ? { ...n, position: { x: n.position.x + move.dx, y: n.position.y + move.dy } }
        : n;
    });
  }

  // ctxMenu anchors the canvas context menu on a node (nodeId set) or on the
  // empty pane (nodeId null). A pane anchor also carries the click's canvas
  // point for the placement actions, and a sample of what the history and
  // clipboard can offer, taken at open so the disabled states are truthful.
  let ctxMenu = $state<{
    x: number;
    y: number;
    nodeId: string | null;
    flowX?: number;
    flowY?: number;
    canUndo?: boolean;
    canRedo?: boolean;
    canPaste?: boolean;
  } | null>(null);

  function handleNodeContextMenu({ node, event }: { node: EditorNode; event: MouseEvent }) {
    event.preventDefault();
    const meta = $nodeCatalog.byType[node.data.nodeType];
    // Annotation nodes have nothing to run; link nodes are wiring that no
    // longer exists once the graph is flattened, so a partial run cannot
    // start at one either. Either kind still opens the menu as part of a
    // multi-selection, where the group actions apply.
    const multi = node.selected && nodes.filter((n) => n.selected).length > 1;
    if (!multi && (!meta || meta.category === 'annotation' || isLinkNode(node.data.nodeType))) {
      ctxMenu = null;
      return;
    }
    ctxMenu = { x: event.clientX, y: event.clientY, nodeId: node.id };
  }

  // handleSelectionContextMenu opens the menu for a right-click on the
  // bounding rect drawn around a multi-selection, which swallows the pointer
  // events before any node underneath sees them. Anchoring on any selected
  // node makes the menu resolve to the whole selection.
  function handleSelectionContextMenu({
    nodes: sel,
    event,
  }: {
    nodes: EditorNode[];
    event: MouseEvent;
  }) {
    event.preventDefault();
    if (sel.length === 0) return;
    ctxMenu = { x: event.clientX, y: event.clientY, nodeId: sel[0].id };
  }

  // ctxPanePoint closes the menu and returns the canvas point it was opened
  // at, captured before the state is cleared so a placement action can still
  // use it.
  function ctxPanePoint(): { x: number; y: number } {
    const m = ctxMenu;
    ctxMenu = null;
    return { x: m?.flowX ?? 0, y: m?.flowY ?? 0 };
  }

  // handlePaneContextMenu opens the canvas-level menu on a right-click over
  // empty pane, which otherwise falls through to the browser's own menu. The
  // pending history record is flushed first so the undo state offered is the
  // one a click would actually get.
  function handlePaneContextMenu({ event }: { event: MouseEvent }) {
    event.preventDefault();
    flushHistory();
    const pos = screenToFlowPosition({ x: event.clientX, y: event.clientY });
    ctxMenu = {
      x: event.clientX,
      y: event.clientY,
      nodeId: null,
      flowX: pos.x,
      flowY: pos.y,
      canUndo: undoStack.length > 0,
      canRedo: redoStack.length > 0,
      canPaste: (getClipboard()?.nodes.length ?? 0) > 0,
    };
  }

  let ctxNode = $derived(ctxMenu ? nodes.find((n) => n.id === ctxMenu!.nodeId) : undefined);

  // ctxTargets is the node set a context action applies to: the whole
  // selection when the clicked node is part of one, else the clicked node
  // alone — the way every desktop canvas treats a right-click.
  let ctxTargets = $derived.by(() => {
    if (!ctxMenu) return [] as EditorNode[];
    const sel = nodes.filter((n) => n.selected);
    return sel.some((n) => n.id === ctxMenu!.nodeId) && sel.length > 1
      ? sel
      : nodes.filter((n) => n.id === ctxMenu!.nodeId);
  });
  let ctxMulti = $derived(ctxTargets.length > 1);
  let ctxBypassables = $derived(ctxTargets.filter((n) => nodeBypassable(n)));
  let ctxAllBypassed = $derived(
    ctxBypassables.length > 0 && ctxBypassables.every((n) => n.data.bypass)
  );
  let ctxCollapsibles = $derived(ctxTargets.filter((n) => nodeCollapsible(n)));
  let ctxAllCollapsed = $derived(
    ctxCollapsibles.length > 0 && ctxCollapsibles.every((n) => n.data.collapsed)
  );
  // ctxColorables excludes annotations: a note has its own color setting and a
  // group frame is not a card.
  let ctxColorables = $derived(
    ctxTargets.filter(
      (n) => $nodeCatalog.byType[n.data.nodeType]?.category !== 'annotation'
    )
  );

  // ctxRotated is the turned nodes among the targets: the menu offers a way
  // back to square only when there is something to straighten.
  let ctxRotated = $derived(ctxTargets.filter((n) => !!n.data.config._rotation));

  // nodeTints is the card color palette, matching FlowNode's tint classes.
  const nodeTints: { id: string; css: string }[] = [
    { id: 'red', css: 'var(--theme-error, #f38ba8)' },
    { id: 'peach', css: 'var(--theme-peach, #fab387)' },
    { id: 'yellow', css: 'var(--theme-warning, #f9e2af)' },
    { id: 'green', css: 'var(--theme-success, #a6e3a1)' },
    { id: 'teal', css: 'var(--theme-info, #94e2d5)' },
    { id: 'blue', css: 'var(--theme-accent, #89b4fa)' },
    { id: 'mauve', css: 'var(--theme-mauve, #cba6f7)' },
    { id: 'pink', css: 'var(--theme-pink, #f5c2e7)' },
  ];

  // setNodeColor stores a card color on each node, in config under _color the
  // way _label rides: presentation state validation and the engine ignore.
  // An empty color clears back to the neutral card.
  function setNodeColor(ids: string[], color: string) {
    if (readOnly) return;
    const targets = new Set(ids);
    nodes = nodes.map((n) => {
      if (!targets.has(n.id)) return n;
      const config = { ...n.data.config };
      if (color) config._color = color;
      else delete config._color;
      return { ...n, data: { ...n.data, config } };
    });
    editor.markDirty();
  }

  // setNodeRotation turns each node's card to an angle, stored in config under
  // _rotation the way _color rides: presentation state validation and the
  // engine ignore. Zero clears the key, so an unturned node is stored exactly
  // as it was before nodes could turn at all.
  function setNodeRotation(ids: string[], degrees: number) {
    if (readOnly) return;
    const targets = new Set(ids);
    nodes = nodes.map((n) => {
      if (!targets.has(n.id)) return n;
      const config = { ...n.data.config };
      if (degrees) config._rotation = degrees;
      else delete config._rotation;
      return { ...n, data: { ...n.data, config } };
    });
    editor.markDirty();
  }

  // Rotation requests raised by a node's corner grips, one per step of the
  // drag. The node component owns the pointer geometry; the graph is here.
  $effect(() => {
    const req = $editor.nodeRotateRequest;
    if (!req) return;
    editor.clearNodeRotateRequest();
    setNodeRotation([req.nodeId], req.degrees);
  });

  // countLabel suffixes a group action with how many nodes it touches.
  function countLabel(count: number): string {
    return count === 1 ? ' 1 node' : ` ${count} nodes`;
  }

  async function runFromNode(nodeId: string) {
    ctxMenu = null;
    if (!(await flushSave())) return;
    try {
      await runState.startTestRun(current.id, { startNodeId: nodeId });
      showRun();
    } catch (e: any) {
      if (e?.code === 'MISSING_UPSTREAM_DATA') {
        showToast('Run from here needs a previous run that produced data for this node. Run the whole flow first.');
      } else {
        showToast(e?.message || 'Failed to run from this node');
      }
    }
  }

  async function cancelRun() {
    try {
      await runState.cancelRun();
    } catch (e: any) {
      showToast(e?.message || 'Failed to cancel run');
    }
  }

  // ---- Export ----

  // exportFlow downloads the draft as a portable JSON document with shared
  // subflows embedded and connection values blanked.
  async function exportFlow() {
    if (!(await flushSave())) return;
    try {
      const doc = await api.flows.exportDoc<unknown>(current.id);
      const blob = new Blob([JSON.stringify(doc, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `${current.slug || 'flow'}.karakuri.json`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e: any) {
      showToast(e?.message || 'Failed to export the flow');
    }
  }

  // ---- Version history ----

  interface VersionEntry {
    version: number;
    comment: string;
    created_at: string;
    created_by?: { username?: string } | null;
  }

  let showHistory = $state(false);
  let historyLoading = $state(false);
  let historyVersions = $state<VersionEntry[]>([]);
  let restoring = $state(false);

  async function openHistory() {
    showHistory = true;
    historyLoading = true;
    try {
      historyVersions = (await api.flows.versions<VersionEntry[]>(current.id)) ?? [];
    } catch (e: any) {
      showToast(e?.message || 'Failed to load version history');
      showHistory = false;
    } finally {
      historyLoading = false;
    }
  }

  async function restoreVersion(version: number) {
    if (saveTimer) { clearTimeout(saveTimer); saveTimer = null; }
    if (inFlight) await inFlight;
    if (!(await confirm(`Restore version ${version} into the draft? The current draft will be overwritten.`))) return;
    restoring = true;
    try {
      await api.flows.restoreVersion(current.id, version);
      const detail = await editor.load(current.id);
      if (detail) reinitFrom(detail);
      showHistory = false;
    } catch (e: any) {
      showToast(e?.message || 'Failed to restore the version');
    } finally {
      restoring = false;
    }
  }

  // Auto-switch to the Run tab when a run starts elsewhere (e.g. a live
  // trigger firing while the editor is open). Skips the initial last-run load.
  let lastSeenRunId: string | null | undefined = undefined;
  $effect(() => {
    const r = $runState.run;
    if (lastSeenRunId === undefined) {
      lastSeenRunId = r?.id ?? null;
      return;
    }
    if (r && r.id !== lastSeenRunId) {
      lastSeenRunId = r.id;
      if (r.status === 'running') showRun();
    }
  });

  let chatTarget = $state<{ nodeId: string; subgraphId?: string; name: string }>();
  let chatOpen = $state(false);
  $effect(() => {
    const nodeId = $editor.nodeChatRequest;
    if (!nodeId) return;
    editor.clearNodeChatRequest();
    if (readOnly) return;
    const node = nodes.find(n => n.id === nodeId);
    if (!node || !['ai-agent', 'ai-basic-chain'].includes(node.data.nodeType)) return;
    const subgraphId = crumbs.length ? crumbs[crumbs.length - 1].refId : undefined;
    chatTarget = { nodeId, subgraphId, name: node.data.label || $nodeCatalog.byType[node.data.nodeType]?.label || node.data.nodeType };
    chatOpen = true;
  });

  // Rename modal.
  let showRename = $state(false);
  let renameName = $state('');
  let renameDescription = $state('');

  function openRename() {
    renameName = current.name;
    renameDescription = current.description;
    showRename = true;
  }

  async function saveRename() {
    try {
      await editor.updateMeta({ name: renameName, description: renameDescription });
      showRename = false;
    } catch (e: any) {
      alert(e.message || 'Failed to rename flow');
    }
  }
</script>

<svelte:window onkeydown={handleEditorKey} />

{#if chatTarget}
  {#key `${chatTarget.subgraphId ?? ''}/${chatTarget.nodeId}`}
    <NodeChat flowId={flow.id} nodeId={chatTarget.nodeId} subgraphId={chatTarget.subgraphId}
      name={chatTarget.name} open={chatOpen} onclose={() => { chatOpen = false; }} prepare={flushSave} />
  {/key}
{/if}

<div class="flex flex-col flex-1 min-h-0">
  <!-- Toolbar -->
  <div
    class="flex items-center gap-3 px-4 py-2 border-b"
    style="background-color: var(--theme-mantle); border-color: var(--theme-surface0);"
  >
    <!-- The palette's own toggle: it is the only way back once the panel is
         gone, so it lives in the toolbar rather than inside the panel. -->
    {#if !readOnly}
      <button
        class="btn btn-secondary btn-sm"
        onclick={() => setPaletteHidden(!paletteHidden)}
        title={paletteHidden ? 'Show node palette' : 'Hide node palette'}
        aria-label={paletteHidden ? 'Show node palette' : 'Hide node palette'}
      >
        <i class="las {paletteHidden ? 'la-angle-double-right' : 'la-angle-double-left'}"></i>
      </button>
    {/if}

    <div class="flex items-center gap-2 min-w-0">
      {#if current.is_subflow}
        <span class="badge badge-subflow" title="Shared subflow: deploy publishes its interface">
          <i class="las la-layer-group mr-1"></i>Subflow
        </span>
      {/if}
      <h2 class="font-bold truncate">{current.name}</h2>
      {#if canEdit}
        <button class="opacity-50 hover:opacity-100" onclick={openRename} title="Rename flow" aria-label="Rename flow">
          <i class="las la-pen"></i>
        </button>
      {:else}
        <span class="badge badge-readonly" title="This flow was shared with you to view; changes need edit access">
          <i class="las la-lock mr-1"></i>View only
        </span>
      {/if}
    </div>

    <div class="flex items-center gap-2 text-xs">
      {#if current.deployed}
        <span class="badge badge-deployed"><i class="las la-check-circle mr-1"></i>Deployed</span>
        {#if current.has_draft_changes}
          <span class="badge badge-draft" title="Draft differs from the deployed version">Draft changes</span>
        {/if}
      {:else}
        <span class="badge badge-undeployed">Not deployed</span>
      {/if}
    </div>

    <div class="flex-1"></div>

    <EditorAssistant kind="flow" id={flow.id} name={current.name} disabled={readOnly}
      prepare={flushSave} onchange={refreshAssistantDraft} />

    <!-- Runs start from a trigger node's own button; the toolbar only stops one. -->
    {#if running}
      <button class="btn btn-secondary btn-sm" onclick={cancelRun} title="Cancel the running test">
        <i class="las la-spinner la-spin mr-1"></i><i class="las la-stop mr-1"></i>Cancel
      </button>
    {:else if $runState.starting}
      <span class="text-xs opacity-60"><i class="las la-spinner la-spin mr-1"></i>Starting...</span>
    {/if}

    {#if runInfo}
      <button
        class="run-chip status-{runInfo.status}"
        onclick={showRun}
        title="Show run details"
      >
        {#if runInfo.status === 'running'}
          <i class="las la-spinner la-spin"></i>
        {:else if runInfo.status === 'succeeded'}
          <i class="las la-check-circle"></i>
        {:else if runInfo.status === 'canceled'}
          <i class="las la-ban"></i>
        {:else}
          <i class="las la-times-circle"></i>
        {/if}
        <span class="capitalize">{runInfo.status}</span>
        <span class="opacity-60">{relTime(runInfo.ended_at ?? runInfo.started_at, now)}</span>
        {#if runInfo.had_failures}
          <i
            class="las la-exclamation-triangle"
            style="color: var(--theme-warning, #f9e2af);"
            title="Some nodes failed and were handled by an error branch"
          ></i>
        {/if}
        {#if runInfo.error}
          <span class="run-chip-error" title={runInfo.error}>{runInfo.error}</span>
        {/if}
      </button>
    {/if}

    {#if !current.is_subflow && canEdit}
      <label
        class="flex items-center gap-2 text-sm {current.deployed ? 'cursor-pointer' : 'opacity-40 cursor-not-allowed'}"
        title={current.deployed ? '' : 'Deploy the flow before enabling it'}
      >
        <input
          type="checkbox"
          checked={current.enabled}
          disabled={!current.deployed}
          onchange={(e) => editor.setEnabled(e.currentTarget.checked)}
        />
        <span>Enabled</span>
      </label>
    {/if}

    <button
      class="btn btn-secondary btn-sm"
      onclick={exportFlow}
      title="Export the draft as a portable JSON document"
      aria-label="Export flow"
    >
      <i class="las la-file-export"></i>
    </button>
    <button
      class="btn btn-secondary btn-sm"
      onclick={openHistory}
      title="Version history"
      aria-label="Version history"
    >
      <i class="las la-history"></i>
    </button>
    {#if canShare}
      <button
        class="btn btn-secondary btn-sm"
        onclick={() => navigate(`/flows/${flow.id}/share`)}
        title="Share this flow with people and groups"
        aria-label="Share flow"
      >
        <i class="las la-share-alt"></i>
      </button>
    {/if}
    <!-- The draft saves itself, so saving is not news and the toolbar says
         nothing about it. Only a failure needs the author, and it is a button
         because the answer to it is to try again. -->
    {#if remoteConflict}
      <button class="save-state failed" onclick={reloadSharedDraft} title="The shared draft changed while you were editing. Your pending changes have not overwritten it.">Reload shared draft</button>
    {/if}
    {#if $editor.saveError}
      <button class="save-state failed" onclick={() => void flushSave()} title={$editor.saveError}>
        <i class="las la-exclamation-circle mr-1"></i>Save failed — retry
      </button>
    {/if}
    {#if canEdit}
      <button class="btn btn-primary btn-sm" disabled={$editor.deploying || $editor.saving} onclick={deploy}>
        <i class="las la-rocket mr-1"></i>{$editor.deploying ? 'Deploying...' : 'Deploy'}
      </button>
    {/if}
  </div>

  <!-- Breadcrumbs while drilled into a subflow -->
  {#if crumbs.length > 0}
    <div class="breadcrumb-bar">
      <button class="crumb" onclick={() => goToLevel(0)}>{current.name}</button>
      {#each crumbs as crumb, i (i)}
        <i class="las la-angle-right opacity-40"></i>
        {#if i === crumbs.length - 1}
          <span class="crumb current">{crumb.name}</span>
        {:else}
          <button class="crumb" onclick={() => goToLevel(i + 1)}>{crumb.name}</button>
        {/if}
      {/each}
      {#if inSharedSubflow}
        <span class="badge badge-readonly ml-2" title="Shared subflows are edited in their own flow">
          <i class="las la-lock mr-1"></i>Read-only
        </span>
        <button
          class="btn btn-secondary btn-sm ml-2"
          onclick={() => openSharedFlow(crumbs[crumbs.length - 1].refId)}
        >
          <i class="las la-external-link-alt mr-1"></i>Open subflow
        </button>
      {/if}
    </div>
  {/if}

  <div class="flex flex-1 min-h-0">
    <!-- Palette -->
    <!-- Hiding the palette gives the whole width to the canvas: no rail is
         left behind, since the toolbar button brings it back. -->
    {#if !readOnly && !paletteHidden}
      <aside class="w-56 border-r shrink-0" style="background-color: var(--theme-mantle); border-color: var(--theme-surface0);">
        <Palette
          mode={paletteMode}
          onAdd={addNode}
          onAddSubflow={addSubflowNode}
          onNewLocalSubflow={newLocalSubflow}
        />
      </aside>
    {/if}

    <!-- Canvas -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      class="flex-1 min-w-0 relative"
      ondragover={handleDragOver}
      ondrop={handleDrop}
      ondblclick={handleCanvasDoubleClick}
    >
      <SvelteFlow
        bind:nodes
        bind:edges
        bind:viewport
        fitView
        fitViewOptions={{ minZoom: 1, maxZoom: 1 }}
        {nodeTypes}
        {edgeTypes}
        {isValidConnection}
        colorMode="dark"
        class="karakuri-flow"
        deleteKey={readOnly ? [] : ['Backspace', 'Delete']}
        nodesDraggable={!readOnly}
        nodesConnectable={!readOnly}
        minZoom={0.2}
        connectionRadius={34}
        zoomOnDoubleClick={false}
        selectionKey={readOnly ? null : 'Control'}
        selectionMode={SelectionMode.Partial}
        multiSelectionKey={['Shift', 'Meta', 'Control']}
        onbeforeconnect={handleBeforeConnect}
        onconnectstart={handleConnectStart}
        onconnectend={handleConnectEnd}
        ondelete={({ nodes: deletedNodes, edges: deletedEdges }) => {
          if (readOnly) return;
          editor.markDirty();
          // A deleted reroute straightens its wire instead of cutting it. The
          // library has already removed the nodes and their edges by the time
          // this fires, so the healed replacements are appended here.
          const healed = healedEdges(deletedNodes as EditorNode[], deletedEdges as EditorEdge[]);
          if (healed.length > 0) edges = [...edges, ...healed];
          if (deletedNodes.some((n) => n.id === $editor.selectedNodeId)) editor.select(null);
          void cleanupOrphanSubgraphs(deletedNodes as EditorNode[]);
        }}
        onnodedragstart={handleNodeDragStart}
        onnodedrag={handleNodeDrag}
        onnodedragstop={() => {
          captured = new Map();
          if (!readOnly) editor.markDirty();
        }}
        onnodecontextmenu={handleNodeContextMenu}
        onselectioncontextmenu={handleSelectionContextMenu}
        onpanecontextmenu={handlePaneContextMenu}
        onpaneclick={() => {
          ctxMenu = null;
          placer = null;
          closeEdgeMenu();
        }}
        onselectionchange={({ nodes: sel }) => editor.select(sel.length === 1 ? sel[0].id : null)}
      >
        <Background variant={BackgroundVariant.Dots} />
        <Controls />
        <MiniMap />
        {#if problemList.length > 0}
          <Panel position="bottom-center">
            <div class="problems-panel card p-3">
              <div class="flex items-center justify-between mb-1">
                <h4 class="text-xs font-bold" style="color: var(--theme-error);">
                  <i class="las la-exclamation-triangle mr-1"></i>Validation problems
                </h4>
                <button class="opacity-60 hover:opacity-100 text-sm" onclick={() => editor.clearProblems()} aria-label="Dismiss">
                  <i class="las la-times"></i>
                </button>
              </div>
              <ul class="text-xs flex flex-col gap-1">
                {#each problemList as problem (problem.location)}
                  <li>
                    {#if problem.nodeId}
                      <button class="problem-link" onclick={() => editor.select(problem.nodeId)}>
                        {problem.name}
                      </button>
                    {:else}
                      <span class="font-mono opacity-70">{problem.name}</span>
                    {/if}
                    <span>: {problem.message}</span>
                  </li>
                {/each}
              </ul>
            </div>
          </Panel>
        {/if}
      </SvelteFlow>
    </div>

    <!-- Inspector: Config | Run -->
    {#if !inspectorOpen}
      <div class="panel-rail border-l" style="background-color: var(--theme-mantle); border-color: var(--theme-surface0);">
        <button class="rail-btn" onclick={() => setInspectorHidden(false)} title="Show config and run panel">
          <i class="las la-angle-double-left"></i>
          <span class="rail-label">Config / Run</span>
        </button>
      </div>
    {:else}
    <aside
      class="w-80 border-l shrink-0 flex flex-col min-h-0"
      class:peeking={inspectorPeek}
      style="background-color: var(--theme-mantle); border-color: var(--theme-surface0);"
    >
      <div class="panel-tabs" role="tablist">
        <button
          role="tab"
          class="panel-tab"
          class:active={panelTab === 'config'}
          aria-selected={panelTab === 'config'}
          onclick={() => (panelTab = 'config')}
        >
          Config
        </button>
        <button
          role="tab"
          class="panel-tab"
          class:active={panelTab === 'run'}
          aria-selected={panelTab === 'run'}
          onclick={() => (panelTab = 'run')}
        >
          Run
        </button>
        <button
          class="panel-hide"
          onclick={hideInspector}
          title={inspectorPeek ? 'Close panel' : 'Hide config and run panel'}
          aria-label="Hide config and run panel"
        >
          <i class="las la-angle-double-right"></i>
        </button>
      </div>
      <div class="flex-1 overflow-y-auto min-h-0">
        {#if panelTab === 'config'}
          {#if inSharedSubflow}
            <p class="text-xs opacity-50 p-4">
              This is a read-only view of a shared subflow. Use "Open subflow" in the breadcrumb bar to edit it.
            </p>
          {:else if selectedNode && !canEdit}
            <!-- A view share reads node settings; the disabled fieldset stops
                 every native control, and the handlers ignore the rest. -->
            <fieldset disabled class="view-only-config">
              <ConfigPanel
                node={selectedNode}
                onUpdateData={() => {}}
                onConfigChange={() => {}}
                onToggleParam={() => {}}
                onDelete={() => {}}
                onOpenSubflow={() => openSubflowNode(selectedNode!.id)}
                onPromote={() => {}}
                onImportLocal={() => {}}
              />
            </fieldset>
          {:else if selectedNode}
            <ConfigPanel
              node={selectedNode}
              onUpdateData={(patch) => updateNodeData(selectedNode!.id, patch)}
              onConfigChange={(fieldId, value) => setConfigValue(selectedNode!.id, fieldId, value)}
              onToggleParam={(fieldId, exposed) => toggleNodeParam(selectedNode!.id, fieldId, exposed)}
              onDelete={() => deleteNode(selectedNode!.id)}
              onOpenSubflow={() => openSubflowNode(selectedNode!.id)}
              onPromote={() => openPromote(selectedNode!)}
              onImportLocal={() => importAsLocal(selectedNode!)}
            />
          {:else}
            <p class="text-xs opacity-50 p-4">Select a node on the canvas to configure it.</p>
          {/if}
        {:else}
          <RunPanel {selectedNode} {nodes} />
        {/if}
      </div>
    </aside>
    {/if}
  </div>
</div>

{#if showTestValue}
  <Modal title="Test with a value" onclose={() => (showTestValue = false)}>
    <form
      onsubmit={(e: SubmitEvent) => {
        e.preventDefault();
        void runWithTestValue();
      }}
      class="flex flex-col gap-4"
    >
      <p class="text-xs opacity-60">
        Runs the draft once seeded with this payload instead of whatever the trigger would
        supply. Nothing is saved to the node.
      </p>
      <FormField label="Payload (JSON)" id="tv-payload" error={testValueError}>
        <textarea
          id="tv-payload"
          class="input w-full text-sm font-mono"
          rows="8"
          bind:value={testValueText}
        ></textarea>
      </FormField>
      <FormField label="Topic" id="tv-topic">
        <input id="tv-topic" class="input w-full text-sm" bind:value={testValueTopic} placeholder="optional" />
      </FormField>
      <div class="flex justify-end">
        <button type="submit" class="btn btn-primary">
          <i class="las la-play mr-1"></i>Run
        </button>
      </div>
    </form>
  </Modal>
{/if}

{#if placer}
  <NodePlacer
    x={placer.x}
    y={placer.y}
    mode={paletteMode}
    filter={placer.filter}
    onpick={placeNode}
    onclose={() => (placer = null)}
  />
{/if}

{#if ctxMenu}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div
    class="ctx-backdrop"
    onclick={() => (ctxMenu = null)}
    oncontextmenu={(e) => {
      e.preventDefault();
      ctxMenu = null;
    }}
  ></div>
  <div class="ctx-menu" style="left: {ctxMenu.x}px; top: {ctxMenu.y}px;" role="menu">
    {#if ctxMenu.nodeId === null}
      {#if !readOnly}
        <button
          class="ctx-item"
          role="menuitem"
          onclick={() => {
            const m = ctxMenu!;
            ctxMenu = null;
            openPlacer(m.x, m.y, null);
          }}
        >
          <i class="las la-plus-circle mr-1"></i>Add node...
        </button>
        <button
          class="ctx-item"
          role="menuitem"
          disabled={!ctxMenu.canPaste}
          onclick={() => pasteClipboardAt(ctxPanePoint())}
        >
          <i class="las la-paste mr-1"></i>Paste
        </button>
        <button
          class="ctx-item"
          role="menuitem"
          onclick={() => addNode('note', ctxPanePoint())}
        >
          <i class="las la-sticky-note mr-1"></i>Add note
        </button>
        <button
          class="ctx-item"
          role="menuitem"
          onclick={() => addNode('group', ctxPanePoint())}
        >
          <i class="las la-object-group mr-1"></i>Add group frame
        </button>
        <button
          class="ctx-item"
          role="menuitem"
          onclick={() => newLocalSubflow(ctxPanePoint())}
        >
          <i class="las la-layer-group mr-1"></i>New local subflow
        </button>
        <button
          class="ctx-item"
          role="menuitem"
          disabled={!ctxMenu.canUndo}
          onclick={() => {
            ctxMenu = null;
            undo();
          }}
        >
          <i class="las la-undo mr-1"></i>Undo
        </button>
        <button
          class="ctx-item"
          role="menuitem"
          disabled={!ctxMenu.canRedo}
          onclick={() => {
            ctxMenu = null;
            redo();
          }}
        >
          <i class="las la-redo mr-1"></i>Redo
        </button>
      {/if}
      <button
        class="ctx-item"
        role="menuitem"
        onclick={() => {
          ctxMenu = null;
          void fitView({ padding: 0.15, duration: 200 });
        }}
      >
        <i class="las la-expand mr-1"></i>Fit view
      </button>
    {:else}
    {#if !ctxMulti && ctxNode?.data.nodeType === 'subflow'}
      <button
        class="ctx-item"
        role="menuitem"
        onclick={() => {
          const id = ctxMenu!.nodeId!;
          ctxMenu = null;
          openSubflowNode(id);
        }}
      >
        <i class="las la-layer-group mr-1"></i>{ctxNode.data.subgraphId ? 'Edit subflow' : 'View subflow'}
      </button>
      {#if ctxNode.data.subgraphId}
        <button
          class="ctx-item"
          role="menuitem"
          onclick={() => {
            const n = ctxNode!;
            ctxMenu = null;
            openPromote(n);
          }}
        >
          <i class="las la-share-square mr-1"></i>Promote to shared library...
        </button>
      {/if}
    {/if}
    {#if !ctxMulti}
      <button
        class="ctx-item"
        role="menuitem"
        disabled={running || $runState.starting || !!ctxNode?.data.bypass}
        title={ctxNode?.data.bypass ? 'A bypassed node does not run' : undefined}
        onclick={() => runFromNode(ctxMenu!.nodeId!)}
      >
        <i class="las la-play mr-1"></i>Run from here
      </button>
    {/if}
    <button
      class="ctx-item"
      role="menuitem"
      onclick={() => {
        const targets = ctxTargets;
        ctxMenu = null;
        copyNodes(targets);
      }}
    >
      <i class="las la-copy mr-1"></i>Copy{ctxMulti ? countLabel(ctxTargets.length) : ''}
    </button>
    {#if !readOnly}
      <button
        class="ctx-item"
        role="menuitem"
        onclick={() => {
          const ids = ctxTargets.map((n) => n.id);
          ctxMenu = null;
          duplicateNodes(ids);
        }}
      >
        <i class="las la-clone mr-1"></i>Duplicate{ctxMulti ? countLabel(ctxTargets.length) : ''}
      </button>
      {#if ctxCollapsibles.length > 0}
        <button
          class="ctx-item"
          role="menuitem"
          onclick={() => {
            const ids = ctxCollapsibles.map((n) => n.id);
            ctxMenu = null;
            toggleCollapse(ids);
          }}
        >
          <i class="las la-angle-up mr-1"></i>
          {ctxAllCollapsed ? 'Expand' : 'Collapse'}{ctxMulti ? countLabel(ctxCollapsibles.length) : ''}
        </button>
      {/if}
      {#if ctxBypassables.length > 0}
        <button
          class="ctx-item"
          role="menuitem"
          onclick={() => {
            const ids = ctxBypassables.map((n) => n.id);
            ctxMenu = null;
            toggleBypass(ids);
          }}
        >
          <i class="las la-forward mr-1"></i>
          {ctxAllBypassed ? 'Un-bypass' : 'Bypass'}{ctxMulti ? countLabel(ctxBypassables.length) : ''}
        </button>
      {/if}
      {#if ctxRotated.length > 0}
        <button
          class="ctx-item"
          role="menuitem"
          onclick={() => {
            const ids = ctxRotated.map((n) => n.id);
            ctxMenu = null;
            setNodeRotation(ids, 0);
          }}
        >
          <i class="las la-undo mr-1"></i>Straighten{ctxMulti ? countLabel(ctxRotated.length) : ''}
        </button>
      {/if}
      <button
        class="ctx-item"
        role="menuitem"
        onclick={() => {
          const ids = ctxTargets.map((n) => n.id);
          ctxMenu = null;
          convertToSubflow(ids);
        }}
      >
        <i class="las la-object-group mr-1"></i>Convert to subflow{ctxMulti ? countLabel(ctxTargets.length) : ''}
      </button>
      {#if ctxColorables.length > 0}
        <div class="ctx-swatches" role="group" aria-label="Node color">
          <button
            class="ctx-swatch ctx-swatch-none"
            title="No color"
            aria-label="Clear node color"
            onclick={() => {
              const ids = ctxColorables.map((n) => n.id);
              ctxMenu = null;
              setNodeColor(ids, '');
            }}
          ></button>
          {#each nodeTints as t (t.id)}
            <button
              class="ctx-swatch"
              style="--swatch: {t.css}"
              title="Color {t.id}"
              aria-label="Color node {t.id}"
              onclick={() => {
                const ids = ctxColorables.map((n) => n.id);
                ctxMenu = null;
                setNodeColor(ids, t.id);
              }}
            ></button>
          {/each}
        </div>
      {/if}
      <button
        class="ctx-item"
        role="menuitem"
        onclick={() => {
          const ids = ctxTargets.map((n) => n.id);
          ctxMenu = null;
          deleteNodes(ids);
        }}
      >
        <i class="las la-trash mr-1"></i>Delete{ctxMulti ? countLabel(ctxTargets.length) : ''}
      </button>
    {/if}
    {/if}
  </div>
{/if}

{#if edgeMenu}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div
    class="ctx-backdrop"
    onclick={closeEdgeMenu}
    oncontextmenu={(e) => {
      e.preventDefault();
      closeEdgeMenu();
    }}
  ></div>
  <div class="ctx-menu" style="left: {edgeMenu.x}px; top: {edgeMenu.y}px;" role="menu">
    {#if edgeMenuEdge?.kind !== 'value'}
      <!-- A value edge is pulled, not flowed, so an ordinary node spliced into
           it would never see its input; the routing nodes below still work
           there because the engine erases them. -->
      <button
        class="ctx-item"
        role="menuitem"
        onclick={() => openInsertPlacer(edgeMenu!.edgeId, edgeMenu!.x, edgeMenu!.y)}
      >
        <i class="las la-plus-circle mr-1"></i>Add Node...
      </button>
    {/if}
    <button
      class="ctx-item"
      role="menuitem"
      onclick={() => addRerouteOnEdge(edgeMenu!.edgeId, edgeMenu!.x, edgeMenu!.y)}
    >
      <i class="las la-circle mr-1"></i>Add Reroute
    </button>
    <button class="ctx-item" role="menuitem" onclick={() => deleteEdgeById(edgeMenu!.edgeId)}>
      <i class="las la-trash mr-1"></i>Delete
    </button>
    <button class="ctx-item" role="menuitem" onclick={() => convertEdgeToLink(edgeMenu!.edgeId)}>
      <i class="las la-exchange-alt mr-1"></i>Convert to Set/Get
    </button>
  </div>
{/if}

{#if toast}
  <div class="toast" role="alert">
    <i class="las la-exclamation-circle mr-1"></i>{toast}
  </div>
{/if}

{#if showRename}
  <Modal title="Edit Flow" onclose={() => (showRename = false)}>
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); saveRename(); }} class="flex flex-col gap-4">
      <FormField label="Name" id="rn-name">
        <input id="rn-name" class="input w-full" bind:value={renameName} required />
      </FormField>
      <FormField label="Description" id="rn-desc">
        <textarea id="rn-desc" class="input w-full" rows="2" bind:value={renameDescription}></textarea>
      </FormField>
      <div class="flex justify-end">
        <button type="submit" class="btn btn-primary">Save</button>
      </div>
    </form>
  </Modal>
{/if}

{#if showHistory}
  <Modal title="Version History" onclose={() => (showHistory = false)}>
    {#if historyLoading}
      <p class="text-sm opacity-60">Loading versions...</p>
    {:else if historyVersions.length === 0}
      <p class="text-sm opacity-60">No deployed versions yet. Deploy the flow to create version 1.</p>
    {:else}
      <div class="flex flex-col gap-1 max-h-96 overflow-y-auto">
        {#each historyVersions as v (v.version)}
          <div class="version-row">
            <div class="min-w-0">
              <div class="flex items-center gap-2">
                <span class="font-bold text-sm">v{v.version}</span>
                {#if current.deployed && v.version === historyVersions[0]?.version}
                  <span class="badge-current">deployed</span>
                {/if}
              </div>
              <div class="text-xs opacity-60 truncate">
                {v.comment || 'No comment'}
                · {relTime(v.created_at)}
                {#if v.created_by?.username}
                  · {v.created_by.username}
                {/if}
              </div>
            </div>
            {#if canEdit}
              <button
                class="btn btn-secondary btn-sm shrink-0"
                disabled={restoring}
                onclick={() => restoreVersion(v.version)}
                title="Copy this version's graph into the draft"
              >
                <i class="las la-undo-alt mr-1"></i>Restore
              </button>
            {/if}
          </div>
        {/each}
      </div>
      <p class="text-xs opacity-50 mt-3">
        Restoring copies the version's graph into the editable draft. The deployed version is unchanged until you deploy again.
      </p>
    {/if}
  </Modal>
{/if}

{#if promoteTarget}
  <Modal title="Promote to Shared Library" onclose={() => (promoteTarget = null)}>
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); doPromote(); }} class="flex flex-col gap-4">
      <p class="text-xs opacity-60">
        The local subflow becomes a shared library flow (deployed as version 1) that any flow can use.
        This node will reference the shared copy.
      </p>
      <FormField label="Name" id="pr-name" error={promoteError}>
        <input id="pr-name" class="input w-full" bind:value={promoteName} required />
      </FormField>
      <div class="flex justify-end">
        <button type="submit" class="btn btn-primary" disabled={promoting || !promoteName.trim()}>
          {promoting ? 'Promoting...' : 'Promote'}
        </button>
      </div>
    </form>
  </Modal>
{/if}

<style>
  /* A failed save sits where the Save button used to, so the eye lands on it
     out of habit. Nothing renders here while saving is working. */
  .save-state {
    display: inline-flex;
    align-items: center;
    padding: 0.3rem 0.5rem;
    font-size: 0.75rem;
    border-radius: var(--theme-radius-sm, 4px);
    color: var(--theme-subtext0, #a6adc8);
    white-space: nowrap;
  }

  .save-state.failed {
    color: var(--theme-error, #f38ba8);
    border: 1px solid var(--theme-error, #f38ba8);
    background: transparent;
    cursor: pointer;
  }

  .save-state.failed:hover {
    background-color: var(--theme-surface0, #313244);
  }

  .badge {
    padding: 0.1rem 0.5rem;
    border-radius: 999px;
    border: 1px solid;
    white-space: nowrap;
  }

  .badge-deployed {
    color: var(--theme-success, #a6e3a1);
    border-color: var(--theme-success, #a6e3a1);
  }

  .badge-draft {
    color: var(--theme-warning, #f9e2af);
    border-color: var(--theme-warning, #f9e2af);
  }

  .badge-undeployed {
    color: var(--theme-overlay0, #6c7086);
    border-color: var(--theme-overlay0, #6c7086);
  }

  .badge-subflow {
    font-size: 0.7rem;
    color: var(--theme-mauve, #cba6f7);
    border-color: var(--theme-mauve, #cba6f7);
  }

  .view-only-config {
    border: 0;
    margin: 0;
    padding: 0;
    min-width: 0;
  }

  .view-only-config :global(button:disabled) {
    opacity: 0.45;
    cursor: default;
  }

  .badge-readonly {
    font-size: 0.7rem;
    color: var(--theme-overlay0, #6c7086);
    border-color: var(--theme-overlay0, #6c7086);
  }

  .breadcrumb-bar {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    padding: 0.35rem 1rem;
    border-bottom: 1px solid var(--theme-surface0, #313244);
    background-color: var(--theme-mantle, #181825);
    font-size: 0.8rem;
  }

  .crumb {
    background: none;
    border: none;
    padding: 0.1rem 0.3rem;
    border-radius: var(--theme-radius-sm, 4px);
    color: var(--theme-accent, #89b4fa);
    cursor: pointer;
    font-size: 0.8rem;
  }

  .crumb:hover {
    background-color: var(--theme-surface0, #313244);
  }

  .crumb.current {
    color: var(--theme-text, #cdd6f4);
    font-weight: 600;
    cursor: default;
  }

  .run-chip {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    padding: 0.15rem 0.6rem;
    border-radius: 999px;
    border: 1px solid;
    font-size: 0.72rem;
    max-width: 22rem;
    background: none;
    cursor: pointer;
    white-space: nowrap;
  }

  .run-chip.status-running {
    color: var(--theme-accent, #89b4fa);
    border-color: var(--theme-accent, #89b4fa);
  }

  .run-chip.status-succeeded {
    color: var(--theme-success, #a6e3a1);
    border-color: var(--theme-success, #a6e3a1);
  }

  .run-chip.status-failed {
    color: var(--theme-error, #f38ba8);
    border-color: var(--theme-error, #f38ba8);
  }

  .run-chip.status-canceled {
    color: var(--theme-overlay0, #6c7086);
    border-color: var(--theme-overlay0, #6c7086);
  }

  .run-chip-error {
    color: var(--theme-error, #f38ba8);
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 10rem;
  }

  .panel-tabs {
    display: flex;
    align-items: stretch;
    border-bottom: 1px solid var(--theme-surface0, #313244);
  }

  .panel-hide {
    padding: 0 0.5rem;
    background: none;
    border: none;
    color: var(--theme-text, #cdd6f4);
    opacity: 0.5;
    cursor: pointer;
    line-height: 1;
  }

  .panel-hide:hover {
    opacity: 1;
  }

  /* A panel opened by double-click is on loan: the accent edge says it will
     not stay once dismissed. */
  .peeking {
    box-shadow: inset 3px 0 0 0 var(--theme-accent, #89b4fa);
  }

  /* The inspector collapses to a rail, which is its only way back. The palette
     hides outright instead, because its toggle lives in the toolbar. */
  .panel-rail {
    display: flex;
    flex-shrink: 0;
    width: 1.75rem;
    border-color: var(--theme-surface0, #313244);
  }

  .rail-btn {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.5rem;
    width: 100%;
    padding: 0.5rem 0;
    background: none;
    border: none;
    color: var(--theme-text, #cdd6f4);
    opacity: 0.55;
    cursor: pointer;
  }

  .rail-btn:hover {
    opacity: 1;
    background-color: var(--theme-surface0, #313244);
  }

  .rail-label {
    writing-mode: vertical-rl;
    font-size: 0.7rem;
    font-weight: 600;
    letter-spacing: 0.03em;
    white-space: nowrap;
  }

  .panel-tab {
    flex: 1;
    padding: 0.45rem 0;
    font-size: 0.75rem;
    font-weight: 600;
    text-align: center;
    background: none;
    border: none;
    color: var(--theme-text, #cdd6f4);
    opacity: 0.55;
    cursor: pointer;
    border-bottom: 2px solid transparent;
  }

  .panel-tab:hover {
    opacity: 0.85;
  }

  .panel-tab.active {
    opacity: 1;
    border-bottom-color: var(--theme-accent, #89b4fa);
  }

  .ctx-backdrop {
    position: fixed;
    inset: 0;
    z-index: 55;
  }

  .ctx-menu {
    position: fixed;
    z-index: 56;
    min-width: 10rem;
    padding: 4px;
    border-radius: var(--theme-radius-md, 8px);
    border: 1px solid var(--theme-surface1, #45475a);
    background-color: var(--theme-mantle, #181825);
    box-shadow: 0 6px 16px rgba(0, 0, 0, 0.4);
  }

  /* Card color swatches: one row in the node menu. The first
     swatch clears back to the neutral card. */
  .ctx-swatches {
    display: flex;
    gap: 4px;
    padding: 6px 10px;
  }

  .ctx-swatch {
    width: 16px;
    height: 16px;
    padding: 0;
    border-radius: 4px;
    border: 1px solid var(--theme-surface1, #45475a);
    background-color: var(--swatch);
    cursor: pointer;
  }

  .ctx-swatch:hover {
    border-color: var(--theme-text, #cdd6f4);
  }

  .ctx-swatch-none {
    background:
      linear-gradient(
        to top left,
        transparent 44%,
        var(--theme-error, #f38ba8) 46%,
        var(--theme-error, #f38ba8) 54%,
        transparent 56%
      )
      var(--theme-surface0, #313244);
  }

  .ctx-item {
    display: block;
    width: 100%;
    padding: 6px 10px;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--theme-text, #cdd6f4);
    font-size: 0.8rem;
    text-align: left;
    cursor: pointer;
  }

  .ctx-item:hover:not(:disabled) {
    background-color: var(--theme-surface1, #45475a);
  }

  .ctx-item:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .toast {
    position: fixed;
    bottom: 1.25rem;
    left: 50%;
    transform: translateX(-50%);
    z-index: 60;
    max-width: 28rem;
    padding: 0.6rem 1rem;
    border-radius: var(--theme-radius-md, 8px);
    background-color: var(--theme-surface0, #313244);
    border: 1px solid var(--theme-warning, #f9e2af);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.8rem;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
  }

  .version-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    padding: 0.45rem 0.6rem;
    border-radius: var(--theme-radius-sm, 4px);
    border: 1px solid var(--theme-surface0, #313244);
  }

  .badge-current {
    padding: 0.05rem 0.4rem;
    border-radius: 999px;
    border: 1px solid var(--theme-success, #a6e3a1);
    color: var(--theme-success, #a6e3a1);
    font-size: 0.65rem;
  }

  .problems-panel {
    max-width: 32rem;
    max-height: 10rem;
    overflow-y: auto;
  }

  /* Selecting the offending node from its problem entry. */
  .problem-link {
    font-weight: 600;
    color: var(--theme-accent, #89b4fa);
    text-decoration: underline;
  }

  .problem-link:hover {
    opacity: 0.8;
  }

  /* Theme the Svelte Flow canvas with the app's CSS variables. */
  :global(.svelte-flow.karakuri-flow) {
    --xy-background-color: var(--theme-crust, #11111b);
    --xy-background-pattern-dots-color-default: var(--theme-surface1, #45475a);
    --xy-edge-stroke: var(--theme-overlay0, #6c7086);
    --xy-edge-stroke-selected: var(--theme-accent, #89b4fa);
    --xy-connectionline-stroke: var(--theme-accent, #89b4fa);
    --xy-controls-button-background-color: var(--theme-surface0, #313244);
    --xy-controls-button-background-color-hover: var(--theme-surface1, #45475a);
    --xy-controls-button-color: var(--theme-text, #cdd6f4);
    --xy-controls-button-border-color: var(--theme-surface1, #45475a);
    --xy-minimap-background-color: var(--theme-mantle, #181825);
    --xy-minimap-mask-background-color: rgba(17, 17, 27, 0.6);
    --xy-attribution-background-color: transparent;
  }

  /* A value edge binds a parameter and carries no message, so it is drawn as a
     thin dashed line: the solid lines remain the path a message takes. The
     color is the carried type's, like every other edge; mauve is only the
     fallback for an untyped binding. */
  :global(.svelte-flow.karakuri-flow .svelte-flow__edge.value-edge .svelte-flow__edge-path) {
    stroke: var(--edge-type-color, var(--theme-mauve, #cba6f7));
    stroke-width: 1.5;
    stroke-dasharray: 5 4;
  }

  /* Parameter sockets take their type's color like every socket; mauve is
     only the fallback for an untyped binding, so a node still reads as
     "message in on the left, parameters underneath". */
  :global(.svelte-flow.karakuri-flow .svelte-flow__handle.param-handle)::after {
    background-color: var(--port-type-color, var(--theme-mauve, #cba6f7));
  }

  :global(.svelte-flow.karakuri-flow .svelte-flow__edge.error-edge .svelte-flow__edge-path) {
    stroke: var(--theme-error, #f38ba8);
    stroke-dasharray: 5 3;
  }

  /* An edge validation would now reject — a port retyped, renamed, or dropped
     after it was wired. Drawn thick, solid, and glowing so it reads as broken
     rather than as the error branch, which is also red but thin and dashed.
     Last of the edge rules so it wins over both. */
  :global(.svelte-flow.karakuri-flow .svelte-flow__edge.invalid-edge .svelte-flow__edge-path) {
    stroke: var(--theme-error, #f38ba8);
    stroke-width: 3;
    stroke-dasharray: none;
    filter: drop-shadow(0 0 3px rgba(243, 139, 168, 0.65));
  }

  :global(.svelte-flow.karakuri-flow .svelte-flow__minimap-node) {
    fill: var(--theme-surface1, #45475a);
  }

  /* The drag-select box, and the bounding rect drawn around a multi-selection
     (which is also its group drag handle). */
  :global(.svelte-flow.karakuri-flow .svelte-flow__selection),
  :global(.svelte-flow.karakuri-flow .svelte-flow__nodesselection-rect) {
    background-color: color-mix(in srgb, var(--theme-accent, #89b4fa) 10%, transparent);
    border: 1px dashed var(--theme-accent, #89b4fa);
  }
</style>
