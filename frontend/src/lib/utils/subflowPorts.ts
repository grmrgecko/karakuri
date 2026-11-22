import type { GraphDoc } from '../stores/flows';
import type { PortSpec } from '../stores/nodeCatalog';
import { isValuePortType } from './payloadPorts';

// SubflowPortSets is the derived interface of a subflow graph.
export interface SubflowPortSets {
  inputs: PortSpec[];
  outputs: PortSpec[];
}

// subflowPorts mirrors the backend's flow.SubflowPorts: it derives the ports
// a subflow node exposes from a subflow graph's pseudo-port nodes, ordered by
// canvas Y position then node ID for a stable layout.
export function subflowPorts(graph: GraphDoc | undefined | null): SubflowPortSets {
  if (!graph) return { inputs: [], outputs: [] };

  const collect = (nodeType: string): PortSpec[] =>
    (graph.nodes ?? [])
      .filter((n) => n.type === nodeType)
      .map((n) => {
        const cfg = (n.config ?? {}) as Record<string, unknown>;
        const id = typeof cfg.port_id === 'string' ? cfg.port_id : '';
        const label = typeof cfg.label === 'string' && cfg.label ? cfg.label : id;
        const type = typeof cfg.port_type === 'string' && cfg.port_type ? cfg.port_type : 'any';
        return { id, label, type, y: n.position?.y ?? 0, nid: n.id };
      })
      .filter((p) => p.id !== '')
      .sort((a, b) => a.y - b.y || (a.nid < b.nid ? -1 : a.nid > b.nid ? 1 : 0))
      .map(({ id, label, type }) => {
        // A value-typed input is a parameter on the subflow node: bound by a
        // value edge, pulled when the subflow runs, never gating readiness.
        // Outputs keep their declared type but are never parameters.
        if (nodeType === 'subflow-input' && isValuePortType(type)) {
          return { id, label, type, kind: 'param', optional: true };
        }
        return { id, label, type };
      });

  return { inputs: collect('subflow-input'), outputs: collect('subflow-output') };
}
