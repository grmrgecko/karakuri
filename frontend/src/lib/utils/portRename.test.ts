import { describe, expect, it } from 'vitest';
import { renamePortEdges } from './portRename';
import type { PortSet } from './nodePorts';

function ports(inputs: string[], outputs: string[]): PortSet {
  return { inputs: inputs.map((id) => ({ id }) as any), outputs: outputs.map((id) => ({ id }) as any), known: true };
}

// A port ID typed into a node's config changes on every keystroke, and the
// pruning that follows each config edit drops wires on IDs the node no longer
// has. Outputs were moved to the new ID, but inputs were not, so the first
// keystroke in a script input's ID deleted the wire into it.
describe('renamePortEdges', () => {
  const edges = [
    { id: 'in', source: 'a', target: 's', sourceHandle: 'out', targetHandle: 'in' },
    { id: 'out', source: 's', target: 'b', sourceHandle: 'done', targetHandle: 'in' },
  ];

  it('moves the wire into a renamed input', () => {
    const got = renamePortEdges('s', ports(['in'], ['done']), ports(['inx'], ['done']), edges);
    expect(got.map((e) => [e.sourceHandle, e.targetHandle])).toEqual([
      ['out', 'inx'],
      ['done', 'in'],
    ]);
  });

  it('moves the wire out of a renamed output', () => {
    const got = renamePortEdges('s', ports(['in'], ['done']), ports(['in'], ['ok']), edges);
    expect(got.map((e) => [e.sourceHandle, e.targetHandle])).toEqual([
      ['out', 'in'],
      ['ok', 'in'],
    ]);
  });

  it('leaves parameter ports alone', () => {
    const wired = [{ source: 'a', target: 's', sourceHandle: 'out', targetHandle: 'param:delay' }];
    const got = renamePortEdges('s', ports(['param:delay'], []), ports(['param:timeout'], []), wired);
    expect(got).toBe(wired);
  });
});
