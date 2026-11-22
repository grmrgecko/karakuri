import type { NodeFunction, NodeMeta } from '../../stores/nodeCatalog';

function meta(type: string, label: string, patch: Partial<NodeMeta> = {}): NodeMeta {
  return {
    type,
    label,
    description: `${label} description.`,
    icon: '',
    inputs: [],
    outputs: [],
    config_spec: [],
    retryable: false,
    ...patch,
  };
}

// analysisFixtures mirror the public catalog shapes used by the editor tests.
export function analysisFixtures(): NodeFunction[] {
  return [
    {
      name: 'ha-get-state',
      aliases: ['haGetState'],
      node_type: 'ha-get-state',
      identity_required: false,
      meta: meta('ha-get-state', 'HA Get State', {
        pure: true,
        retryable: true,
        config_spec: [
          { id: 'connection', label: 'Connection', type: 'connection', required: true, help: 'Home Assistant connection.' },
          { id: 'entity_id', label: 'Entity', type: 'entity', required: true, help: 'Single entity to read.' },
        ],
        outputs: [{
          id: 'out',
          label: 'Out',
          type: 'object',
          payload_schema: {
            type: 'object',
            required: ['entity_id', 'state', 'attributes', 'last_changed', 'last_updated'],
            properties: {
              entity_id: { type: 'string' },
              state: { type: 'string' },
              attributes: { type: 'object', additionalProperties: true },
              last_changed: { type: 'string' },
              last_updated: { type: 'string' },
            },
            additionalProperties: false,
          },
        }],
      }),
    },
    {
      name: 'base64-encode',
      aliases: ['base64Encode'],
      node_type: 'base64-encode',
      identity_required: false,
      meta: meta('base64-encode', 'Base64 Encode', {
        pure: true,
        config_spec: [
          { id: 'property', label: 'Property', type: 'string', required: true },
          { id: 'target', label: 'Target', type: 'string', required: true },
          {
            id: 'encoding', label: 'Encoding', type: 'select', required: false, default: 'standard',
            options: [{ value: 'standard', label: 'Standard' }, { value: 'url', label: 'URL safe' }],
          },
          { id: 'padding', label: 'Padding', type: 'boolean', required: false },
          {
            id: 'line_width', label: 'Line width', type: 'number', required: true,
            show_if: { field: 'encoding', equals: 'standard' },
          },
        ],
        inputs: [{ id: 'in', label: 'In', type: 'any' }],
        outputs: [{ id: 'out', label: 'Out', type: 'string' }],
      }),
    },
    {
      name: 'number-node',
      aliases: ['numberNode'],
      node_type: 'number-node',
      identity_required: false,
      meta: meta('number-node', 'Number', {
        config_spec: [{ id: 'count', label: 'Count', type: 'number', required: true }],
        outputs: [{ id: 'out', label: 'Out', type: 'number' }],
      }),
    },
    {
      name: 'broad-node',
      aliases: ['broadNode'],
      node_type: 'broad-node',
      identity_required: false,
      meta: meta('broad-node', 'Broad', {
        outputs: [{ id: 'out', label: 'Out', type: 'object' }],
      }),
    },
    {
      name: 'open-node',
      aliases: ['openNode'],
      node_type: 'open-node',
      identity_required: false,
      meta: meta('open-node', 'Open', {
        outputs: [{
          id: 'out', label: 'Out', type: 'object',
          payload_schema: {
            type: 'object',
            properties: { state: { type: 'string' } },
          },
        }],
      }),
    },
    {
      name: 'stateful-node',
      aliases: ['statefulNode'],
      node_type: 'stateful-node',
      identity_required: true,
      meta: meta('stateful-node', 'Stateful', {
        retryable: true,
        default_retry: { max_attempts: 3, delay_ms: 100, backoff: 2, max_delay_ms: 1000 },
        invocation_state: 'stateful',
        outputs: [{ id: 'out', label: 'Out', type: 'binary' }],
      }),
    },
    {
      name: 'dynamic-node',
      aliases: ['dynamicNode'],
      node_type: 'dynamic-node',
      identity_required: false,
      meta: meta('dynamic-node', 'Dynamic', {
        dynamic_ports: true,
        inputs: [{ id: 'in', label: 'In', type: 'any' }],
        outputs: [{ id: 'out', label: 'Out', type: 'any', type_from: 'in' }],
      }),
    },
  ];
}
