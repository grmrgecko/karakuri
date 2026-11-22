// Mirrors the edge checks in pkg/flow/validate.go, for edges that are already
// drawn. isValidConnection only runs while an author is dragging a connection:
// retyping a script's declared ports, renaming one, or changing a setting that
// moves a port's type afterwards leaves a wired edge the server will reject,
// and until the next save nothing on the canvas says so.
//
// Only the checks the editor can answer live here. Node purity is not in the
// client catalog, so a value edge pulled from a node with side effects is still
// caught at save.

import type { CarriedTypes } from '../stores/carriedTypes';
import type { PortSet } from './nodePorts';
import { compatible, messageCompatible } from './payloadPorts';

// EdgeEndpoints is the part of a canvas edge the checks read.
export interface EdgeEndpoints {
  id: string;
  source: string;
  sourceHandle?: string | null;
  target: string;
  targetHandle?: string | null;
  kind?: string;
}

// invalidEdges returns the IDs of the edges validation would reject. Ports
// holds each node's resolved ports and carried the types resolved across the
// graph, which is what an output port that only carries its input onward
// actually emits.
export function invalidEdges(
  ports: Map<string, PortSet>,
  carried: CarriedTypes,
  edges: EdgeEndpoints[]
): Set<string> {
  const bad = new Set<string>();
  const inputSeen = new Set<string>();

  for (const e of edges) {
    const isValue = e.kind === 'value';

    // Resolve the target port first: the single-source rule needs to know
    // whether it is a multi parameter port, which accepts several edges.
    const targetHandle = e.targetHandle ?? '';
    const srcSet = ports.get(e.source);
    const tgtSet = ports.get(e.target);
    const tgtPort = tgtSet?.known ? tgtSet.inputs.find((p) => p.id === targetHandle) : undefined;

    // Single-source rule: only one edge may feed an input port, unless the
    // port is multi. The first edge to claim it keeps it, so a later one is
    // the problem.
    if (e.target && targetHandle && !tgtPort?.multi) {
      const inputKey = `${e.target}/${targetHandle}`;
      if (inputSeen.has(inputKey)) {
        bad.add(e.id);
        continue;
      }
      inputSeen.add(inputKey);
    }

    // A node with unresolved ports answers for none of them, and neither does
    // a missing one, so those edges are left alone rather than marked broken
    // over an unrelated problem.
    if (!srcSet?.known || !tgtSet?.known) continue;

    const srcPort = srcSet.outputs.find((p) => p.id === (e.sourceHandle ?? ''));
    if (!srcPort || !tgtPort) {
      bad.add(e.id);
      continue;
    }

    // Check type compatibility. A value edge supplies a config field of a
    // declared type, so an undeclared source is a real problem there; a
    // message edge only carries what the run produces.
    const srcType = carried[e.source]?.[srcPort.id] ?? srcPort.type;
    const ok = isValue
      ? compatible(srcType, tgtPort.type)
      : messageCompatible(srcType, tgtPort.type);
    if (!ok) {
      bad.add(e.id);
      continue;
    }

    // A value edge binds a parameter; a message edge drives the flow. The two
    // must agree with the port the edge lands on.
    if (isValue !== (tgtPort.kind === 'param')) {
      bad.add(e.id);
      continue;
    }

    // The source side has kinds too: a param-kind output (a tool's argument
    // port) supplies a value and can never travel the message path.
    if (!isValue && srcPort.kind === 'param') bad.add(e.id);
  }

  return bad;
}
