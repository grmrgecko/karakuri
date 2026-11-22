import type { PortSet } from './nodePorts';
import { paramFieldId } from './payloadPorts';

// PortEdge is the part of an edge that names the ports at its ends.
interface PortEdge {
  source: string;
  target: string;
  sourceHandle?: string | null;
  targetHandle?: string | null;
}

// renamedPort finds a port whose ID was edited in place: exactly one changed
// position in an otherwise identical list is a rename, not a removal. It
// returns the old and new IDs, or null when the change is anything else.
function renamedPort(previous: PortSet['inputs'], next: PortSet['inputs']): [string, string] | null {
  if (next.length !== previous.length) return null;
  const changed = next.flatMap((p, i) => (p.id !== previous[i].id ? [i] : []));
  if (changed.length !== 1) return null;
  const from = previous[changed[0]].id;
  const to = next[changed[0]].id;
  if (!to || next.some((p) => p.id === from)) return null;
  return [from, to];
}

// renamePortEdges moves a node's wires from a port whose ID was edited in
// place, such as a browser prompt button's output or a script's input, to the
// port's new ID. Without it the pruning that follows a config edit drops the
// wire at the first keystroke. Parameter ports are named by their field and
// are never renamed. It returns edges unchanged when nothing moved.
export function renamePortEdges<E extends PortEdge>(
  nodeId: string,
  previous: PortSet,
  next: PortSet,
  edges: E[]
): E[] {
  if (!previous.known || !next.known) return edges;
  const output = renamedPort(previous.outputs, next.outputs);
  const input = renamedPort(previous.inputs, next.inputs);
  if (input && (paramFieldId(input[0]) !== null || paramFieldId(input[1]) !== null)) return edges;
  if (!output && !input) return edges;
  let moved = false;
  const out = edges.map((e) => {
    let edge = e;
    if (output && e.source === nodeId && e.sourceHandle === output[0]) edge = { ...edge, sourceHandle: output[1] };
    if (input && e.target === nodeId && e.targetHandle === input[0]) edge = { ...edge, targetHandle: input[1] };
    if (edge !== e) moved = true;
    return edge;
  });
  return moved ? out : edges;
}
