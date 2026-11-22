// Mirrors pkg/flow/ports.go. Port resolution is the one place that answers
// "what ports does this placed node have?", and the canvas has to give the same
// answer the server does: a socket the editor draws but validation rejects, or
// one it never draws while the engine happily pulls it, is a bug the author
// cannot see. Every editor component asks nodePorts rather than assembling the
// derivation itself.

import type { NodeMeta, PortSpec } from '../stores/nodeCatalog';
import type { FlowNodeData } from '../components/editor/types';
import { scriptPorts, toolArgPorts, classifyPorts, paramPorts, resolvePortTypes } from './payloadPorts';
import { changePorts } from './changeRules';
import { switchPorts } from './switchPorts';
import { subflowPorts } from './subflowPorts';
import type { GraphDoc } from '../stores/flows';

export interface PortSet {
  inputs: PortSpec[];
  outputs: PortSpec[];
  // known is false when the node's ports could not be determined — a subflow
  // node whose reference does not resolve. Mirrors flow.PortSet.Known: a caller
  // checking edges must not call a port missing when the answer was never
  // available.
  known: boolean;
}

// nodePorts resolves one placed node's ports. Ordinary types use their static
// metadata; the dynamic-port types derive theirs from config. Parameter ports
// are appended last in every case, so a value socket always sits below the
// node's own inputs.
//
// A subflow node's ports come from the graph it references, which the caller
// looks up and passes as sub — the editor holds those in its own stores. A
// missing sub yields no ports, matching the server's unresolved case.
export function nodePorts(
  meta: NodeMeta | undefined,
  data: FlowNodeData,
  sub?: GraphDoc | null
): PortSet {
  let inputs = meta?.inputs ?? [];
  let outputs = meta?.outputs ?? [];

  switch (data.nodeType) {
    case 'webhook':
      if (data.config.response_mode !== 'sync') inputs = inputs.filter((port) => port.kind !== 'return');
      break;
    case 'switch':
      outputs = switchPorts(data.config);
      break;
    case 'browser-prompt': {
      const buttons = (Array.isArray(data.config.buttons) ? data.config.buttons : [
        { id: 'retry', label: 'Retry' }, { id: 'bypass', label: 'Bypass' }, { id: 'stop', label: 'Stop' },
      ]) as { id: string; label: string }[];
      outputs = [...buttons.map((button) => ({ id: button.id, label: button.label, type: 'any', type_from: 'in' })),
        { id: 'error', label: 'Error', type: 'any', type_from: 'in', kind: 'error' }];
      break;
    }
    case 'faceplate':
      outputs = faceplatePorts(data.config);
      break;
    case 'change':
      outputs = changePorts(data.config);
      break;
    case 'script': {
      const declared = scriptPorts(data.config);
      inputs = declared.inputs;
      outputs = declared.outputs;
      break;
    }
    case 'ai-classify': {
      // Category ports sit above the static outputs (other, error), so the
      // routes come before the fallbacks — mirroring flow.NodePorts.
      const cats = classifyPorts(data.config);
      if (cats.length) outputs = [...cats, ...outputs];
      break;
    }
    case 'tool': {
      // Argument ports are appended to the static outputs (out, call), which
      // belong to the node type — mirroring flow.NodePorts.
      const args = toolArgPorts(data.config);
      if (args.length) outputs = [...outputs, ...args];
      break;
    }
    case 'subflow': {
      if (!sub) return { inputs: [], outputs: [], known: false };
      const derived = subflowPorts(sub);
      inputs = derived.inputs;
      outputs = derived.outputs;
      break;
    }
  }

  // Ports whose payload depends on the node's settings resolve their type
  // here, so a file read as binary and one read as text advertise what they
  // each actually carry.
  inputs = resolvePortTypes(inputs, data.config);
  outputs = resolvePortTypes(outputs, data.config);

  const params = paramPorts(meta?.config_spec, data.params, data.config);
  return { inputs: params.length ? [...inputs, ...params] : inputs, outputs, known: true };
}

// controlPortTypes maps a faceplate control type to the port type its output
// carries, mirroring flow.ControlPortType.
const controlPortTypes: Record<string, string> = {
  number: 'number',
  boolean: 'bool',
  string: 'string',
  entity: 'object',
  array: 'object',
  object: 'object',
};

// faceplatePorts derives one value output per control, mirroring
// flow.FaceplatePorts: controls without an ID, or repeating one, are skipped.
export function faceplatePorts(config: Record<string, unknown>): PortSpec[] {
  const controls = Array.isArray(config.controls) ? (config.controls as { id?: string; label?: string; type?: string }[]) : [];
  const seen = new Set<string>();
  const ports: PortSpec[] = [];
  for (const c of controls) {
    if (!c?.id || seen.has(c.id)) continue;
    seen.add(c.id);
    ports.push({ id: c.id, label: c.label || c.id, type: controlPortTypes[c.type ?? ''] ?? 'any' });
  }
  return ports;
}
