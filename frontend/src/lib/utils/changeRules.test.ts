import { describe, expect, it } from 'vitest';
import { changePorts } from './changeRules';
import { propagateTypes } from './typeFlow';

// A change node behind an object source must type its output the way
// flow.ChangePorts and flow.PropagateTypes do on the server. A rule that
// rewrites the payload from an expression used to inherit the source's object
// type on the canvas, so the editor drew a number input as incompatible even
// though the server accepts the edge.
describe('changePorts', () => {
  it.each([
    ['no rules pass the payload through', [], 'object'],
    ['a topic write leaves the payload alone', [{ op: 'set', target: 'topic', from: 'string', value: 'kitchen' }], 'object'],
    ['an expression undeclares the payload', [{ op: 'set', target: 'payload', from: 'expression', value: 'payload.temp' }], 'any'],
    ['deleting the payload undeclares it', [{ op: 'delete', target: 'payload' }], 'any'],
    ['a string set declares a string', [{ op: 'set', target: 'payload', from: 'string', value: 'on' }], 'string'],
  ])('types the output behind an object source when %s', (_name, rules, want) => {
    const ports = new Map([
      ['src', { inputs: [], outputs: [{ id: 'out', label: 'Out', type: 'object' }] }],
      ['chg', { inputs: [{ id: 'in', label: 'In', type: 'any' }], outputs: changePorts({ rules }) }],
    ]);
    const carried = propagateTypes(ports, [{ source: 'src', sourceHandle: 'out', target: 'chg', targetHandle: 'in' }]);
    const declared = changePorts({ rules })[0].type;
    expect(carried['chg']?.['out'] ?? declared).toBe(want);
  });
});
