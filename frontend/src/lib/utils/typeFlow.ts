// Mirrors pkg/flow/typeflow.go. A node's own metadata can only say so much: a
// delay, a switch, or a filter hands on exactly what it was given, so the
// honest answer for its output is not "any" but "whatever is upstream".
// Resolving that needs the whole graph, which nodePorts deliberately does not
// have — it answers for one placed node — so it happens here, over all of them.
//
// The single-source rule makes this unambiguous: an input port accepts at most
// one edge, so a port that carries its input onward has one type to carry.

import type { PortSpec } from '../stores/nodeCatalog';
import { PortAny } from './payloadPorts';
import type { CarriedTypes } from '../stores/carriedTypes';

// maxTypeFlowDepth bounds how far a type is chased through pass-through nodes.
// A graph may contain a cycle, and a chain this long is past the point where a
// resolved type tells an author anything useful.
const MAX_DEPTH = 64;

export interface PortSet {
  inputs: PortSpec[];
  outputs: PortSpec[];
}

interface TypeFlowEdge {
  source: string;
  sourceHandle?: string | null;
  target: string;
  targetHandle?: string | null;
  // kind sits on the edge itself, the way EditorEdge carries it.
  kind?: string;
}

function portKey(nodeId: string, portId: string): string {
  return `${nodeId}/${portId}`;
}

// propagateTypes resolves the outputs that carry their input's payload onward.
// Ports maps node ID to that node's own resolved ports; the result holds only
// the ports whose type came from upstream, which is what the canvas overlays on
// what nodePorts already answered.
export function propagateTypes(
  ports: Map<string, PortSet>,
  edges: TypeFlowEdge[]
): CarriedTypes {
  if (ports.size === 0) return {};

  // Index each input port's single source. Value edges are skipped: they carry
  // a config value on demand, not the message this is tracing.
  const sources = new Map<string, { node: string; port: string }>();
  for (const e of edges) {
    if (e.kind === 'value') continue;
    sources.set(portKey(e.target, e.targetHandle ?? ''), {
      node: e.source,
      port: e.sourceHandle ?? '',
    });
  }

  const resolved = new Map<string, string>();

  // outputType returns what an output port carries, following type_from
  // upstream. A port that declares its own type keeps it; only an undeclared
  // one asks what it was handed.
  function outputType(nodeId: string, p: PortSpec, depth: number): string {
    if (!p.type_from || p.type !== PortAny || depth >= MAX_DEPTH) return p.type;
    const key = portKey(nodeId, p.id);
    const cached = resolved.get(key);
    if (cached !== undefined) return cached;
    // Mark the port before recursing so a cycle resolves to "any" rather than
    // running until the depth cap.
    resolved.set(key, PortAny);

    const src = sources.get(portKey(nodeId, p.type_from));
    if (!src) return PortAny;
    const upstream = ports.get(src.node);
    if (!upstream) return PortAny;
    const spec = upstream.outputs.find((o) => o.id === src.port);
    if (!spec) return PortAny;

    const carried = outputType(src.node, spec, depth + 1);
    resolved.set(key, carried);
    return carried;
  }

  const out: CarriedTypes = {};
  for (const [nodeId, set] of ports) {
    for (const p of set.outputs) {
      const carried = outputType(nodeId, p, 0);
      if (carried === p.type) continue;
      (out[nodeId] ??= {})[p.id] = carried;
    }
  }
  return out;
}
