import type { Node, Edge } from '@xyflow/svelte';
import type { RetryPolicy } from '../../stores/nodeCatalog';

// FlowNodeData is the data payload carried by every editor canvas node.
// nodeType is the backend node type; the Svelte Flow node type only selects
// the rendering component ("flow" or "note").
export type FlowNodeData = {
  nodeType: string;
  config: Record<string, unknown>;
  // params lists config field IDs exposed as value input sockets.
  params?: string[];
  label: string;
  retry?: RetryPolicy;
  execute_once?: boolean;
  // bypass disables the node without unwiring it: the engine splices it out
  // and forwards each output from its type-matched input.
  bypass?: boolean;
  // collapsed shrinks the node to its title bar on the canvas. The handles
  // stay mounted so the wiring never breaks; only the body is hidden.
  collapsed?: boolean;
  // Subflow references: exactly one is set on subflow nodes.
  subflowId?: string;
  subgraphId?: string;
  [key: string]: unknown;
};

export type EditorNode = Node<FlowNodeData>;
// EditorEdge extends the flow library's edge with the karakuri edge kind: an
// empty kind is the message path, "value" binds a parameter.
export type EditorEdge = Edge & { kind?: string };
