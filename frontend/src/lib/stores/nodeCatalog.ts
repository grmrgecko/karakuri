import { writable, get } from 'svelte/store';
import { api } from '../api/client';
import type { PortTypeRule, PortTypeDef } from '../utils/payloadPorts';
import { hydratePortTypes } from '../utils/payloadPorts';

interface PortSpec {
  id: string;
  label: string;
  type: string;
  kind?: string;
  // optional marks an input that runs the node on its own rather than waiting
  // for the other inputs, such as a reset port.
  optional?: boolean;
  // type_rule derives the port's type from the placed node's config, for the
  // ports whose payload depends on how the node is set up. type is what an
  // unconfigured node advertises, and the rule refines it.
  type_rule?: PortTypeRule;
  // type_from names an input port on the same node whose payload this output
  // carries onward, for the nodes that only route or delay a message. It is
  // resolved across the whole graph; see utils/typeFlow.ts.
  type_from?: string;
  // multi marks a parameter port that accepts several value edges; the pulled
  // values reach the config field as an array in edge order.
  multi?: boolean;
  // payload_schema documents the payload inside each message. Its presence is
  // what lets script analysis offer closed payload properties.
  payload_schema?: Record<string, unknown>;
}

interface Option {
  value: string;
  label: string;
}

interface ShowIf {
  field: string;
  equals: unknown;
}

interface FieldSpec {
  id: string;
  label: string;
  type: string;
  // control overrides the editor the field renders with, referencing a
  // registered control; unknown controls fall back to their base type.
  control?: string;
  required: boolean;
  default?: unknown;
  options?: Option[];
  show_if?: ShowIf;
  help?: string;
  placeholder?: string;
  attachment_field?: string;
  // hide_label omits a repeated field heading while retaining its schema label.
  hide_label?: boolean;
  connection_kind?: string;
  // connection_match and connection_exclude filter on public connection config.
  connection_match?: Record<string, string>;
  connection_exclude?: Record<string, string>;
  // language_field names the config field holding a script field's language,
  // so the editor can pick the matching syntax highlighting.
  language_field?: string;
  // examples maps a language to starter source prefilled into an empty field.
  examples?: Record<string, string>;
  // geo marks a number field as one half of a coordinate pair, "latitude" or
  // "longitude", so the editor can fill both from the browser's location.
  geo?: string;
  // port makes the field a permanent value input port of the named port type
  // instead of an editable setting: the config panel renders nothing for it,
  // and its value only ever arrives over value edges.
  port?: string;
  // multi lets several value edges feed a port field at once. Only meaningful
  // with port.
  multi?: boolean;
  // no_param keeps an editable field from being exposed as a value socket.
  no_param?: boolean;
  // value_schema describes structured values that the field type cannot.
  value_schema?: Record<string, unknown>;
}

interface RetryPolicy {
  jitter?: number;
  max_attempts: number;
  delay_ms: number;
  backoff: number;
  max_delay_ms: number;
}

interface NodeMeta {
  type: string;
  label: string;
  // category is the behavioral kind (trigger, annotation, or port), absent for
  // ordinary nodes. Palette placement comes from folder.
  category?: string;
  // folder is the palette folder the node is listed under: a free-form display
  // string, so plugins can bring their own domains.
  folder?: string;
  // group is the palette group the folder nests under, naming where the node
  // runs: an agent's name for its nodes, empty for the server's own.
  group?: string;
  description: string;
  icon: string;
  inputs?: PortSpec[];
  outputs?: PortSpec[];
  config_spec?: FieldSpec[];
  default_retry?: RetryPolicy;
  connection_kind?: string;
  retryable: boolean;
  test_without_params?: boolean;
  // pure marks a node that can be evaluated on demand to produce a value, which
  // is what a value edge pulls. An impure one cannot supply a parameter.
  pure?: boolean;
  dynamic_ports?: boolean;
  // invocation_state records whether a script call needs an explicit durable
  // identity. Missing means unknown for contributed nodes.
  invocation_state?: string;
  // trigger marks a node that starts runs from outside the trigger category,
  // such as the file watcher under storage.
  trigger?: boolean;
}

// NodeFunction is one callable entry in the script SDK catalog. Name is the
// exact registered node type; aliases are optional convenience properties.
export interface NodeFunction {
  name: string;
  aliases?: string[];
  node_type: string;
  identity_required: boolean;
  meta: NodeMeta;
}

// NodeFunctionCatalog is the versioned response consumed by script analysis.
export interface NodeFunctionCatalog {
  schema_version: 1;
  revision: string;
  functions: NodeFunction[];
}

// isTrigger reports whether a node type starts runs on its own, mirroring
// NodeMeta.IsTrigger on the backend.
export function isTrigger(meta: NodeMeta | undefined): boolean {
  return !!meta && (meta.category === 'trigger' || meta.trigger === true);
}

// isLinkNode reports whether a node type is canvas wiring the engine splices
// into direct edges before a run, mirroring flow.IsLinkNode on the backend.
// Link nodes read as pure sources but never execute, so anything that starts
// a run at a node must skip them.
export function isLinkNode(nodeType: string): boolean {
  return nodeType === 'reroute' || nodeType === 'link-set' || nodeType === 'link-get';
}

interface Category {
  id: string;
  // group is the palette group the folder sits under: "Server" for everything
  // that runs in the server process, an agent's name for its nodes.
  group: string;
  label: string;
  types: NodeMeta[];
}

// ---- Field controls ----
//
// Mirrors flow.FieldControlDef: a named config-field editor with a fallback
// base type. The catalog hydrates the registry on load; controlBase answers
// what to render when a control is not implemented here.

export interface FieldControlDef {
  name: string;
  base: string;
  description?: string;
}

let controlsByName = new Map<string, FieldControlDef>();

// hydrateFieldControls replaces the control registry with the server's list.
export function hydrateFieldControls(defs: FieldControlDef[]): void {
  controlsByName = new Map(defs.map((d) => [d.name, d]));
}

// controlBase returns the fallback base type for a registered control, or
// undefined for one the server never registered.
export function controlBase(name: string): string | undefined {
  return controlsByName.get(name)?.base;
}

interface CatalogState {
  loaded: boolean;
  byType: Record<string, NodeMeta>;
  categories: Category[];
}

// Palette folder display order for the known folders, applied per group.
// Folders a group has that are not listed — a plugin's — append
// alphabetically after these. The agent folders (Exec, Desktop, GUI, System)
// sit around Storage in the order agent.go concatenates its node groups; no
// server folder uses those names, so they cost the server group nothing.
const folderOrder: string[] = [
  'Values',
  'Logic',
  'Time',
  'Sequence',
  'Parsers',
  'Encoders',
  'Exec',
  'Storage',
  'Desktop',
  'GUI',
  'System',
  'HTTP',
  'Network',
  'MQTT',
  'Devices',
  'Home Assistant',
  'ESPHome',
  'openHAB',
  'NVR',
  'Energy',
  'AI',
  'Utility',
  'Subflow Ports',
  'Annotations',
];

// folderOf returns the palette folder a node type is listed under. A meta with
// no folder — an older backend, or a plugin that skipped it — lands in Other
// rather than disappearing.
function folderOf(m: NodeMeta): string {
  return m.folder || 'Other';
}

// serverGroup labels the palette group holding everything that runs in the
// server process — the built-ins and plugin nodes — the counterpart to each
// agent's group, which is named by its host.
export const serverGroup = 'Server';

// groupOf returns the palette group a node type's folder nests under. Only
// agent nodes carry one; everything else is the server's.
function groupOf(m: NodeMeta): string {
  return m.group || serverGroup;
}

function createNodeCatalogStore() {
  const store = writable<CatalogState>({ loaded: false, byType: {}, categories: [] });
  let loadPromise: Promise<void> | null = null;

  return {
    subscribe: store.subscribe,

    // load fetches the node type catalog once; concurrent calls share one request.
    // Port types and field controls ride along: both hydrate the registries
    // everything type-related derives from, so a plugin's contributions render
    // without a frontend change.
    async load() {
      if (loadPromise) return loadPromise;
      loadPromise = (async () => {
        const [metas, portTypes, controls] = await Promise.all([
          api.nodeTypes.list<NodeMeta[]>().then((m) => m || []),
          api.nodeTypes.portTypes<PortTypeDef[]>().catch(() => null),
          api.nodeTypes.controls<FieldControlDef[]>().catch(() => null),
        ]);
        if (portTypes) hydratePortTypes(portTypes);
        if (controls) hydrateFieldControls(controls);

        const byType: Record<string, NodeMeta> = {};
        for (const m of metas) {
          byType[m.type] = m;
        }

        // Categories are folders nested under groups: the server's own group
        // first, then each agent's alphabetically. Within a group the known
        // folders keep their display order and the rest — a plugin's, an
        // agent's — append alphabetically.
        const categories: Category[] = [];
        const known = new Set(folderOrder);
        const groups = [
          serverGroup,
          ...[...new Set(metas.map(groupOf))].filter((g) => g !== serverGroup).sort(),
        ];
        for (const group of groups) {
          const inGroup = metas.filter((m) => groupOf(m) === group);
          const folders = [
            ...folderOrder.filter((f) => inGroup.some((m) => folderOf(m) === f)),
            ...[...new Set(inGroup.map(folderOf))].filter((f) => !known.has(f)).sort(),
          ];
          for (const folder of folders) {
            categories.push({
              id: `${group}/${folder}`,
              group,
              label: folder,
              types: inGroup.filter((m) => folderOf(m) === folder),
            });
          }
        }

        store.set({ loaded: true, byType, categories });
      })().catch((err) => {
        loadPromise = null;
        throw err;
      });
      return loadPromise;
    },

    // metaFor returns the metadata for a node type, or undefined when unknown.
    metaFor(type: string): NodeMeta | undefined {
      return get(store).byType[type];
    },
  };
}

export const nodeCatalog = createNodeCatalogStore();
export type { NodeMeta, PortSpec, FieldSpec, Option, ShowIf, RetryPolicy, Category };
