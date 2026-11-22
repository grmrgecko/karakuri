import type { FieldSpec, NodeFunction, NodeMeta, PortSpec } from '../../stores/nodeCatalog';
import { fieldBindable } from '../../utils/payloadPorts';

export type CatalogScriptLanguage = 'python' | 'bash';

export interface CatalogDocumentation {
  display: string;
  documentation: string;
  tags: string[];
}

export interface CatalogCompletion {
  label: string;
  insert: string;
  type: string;
  detail?: string;
  filterText?: string;
  documentation: CatalogDocumentation;
  cursor?: number;
}

export interface CatalogCompletionResult {
  from: number;
  to: number;
  options: CatalogCompletion[];
}

export interface CatalogQuickInfo extends CatalogDocumentation {
  from: number;
  to: number;
}

export interface CatalogSignatureParameter {
  name: string;
  display: string;
  documentation: string;
  optional: boolean;
}

export interface CatalogSignatureHelp {
  from: number;
  to: number;
  activeArgument: number;
  parameters: CatalogSignatureParameter[];
  prefix: string;
  suffix: string;
  separator: string;
  documentation: string;
}

export interface CatalogDiagnostic {
  from: number;
  to: number;
  severity: 'error';
  code: string;
  message: string;
  nodeType: string;
}

export interface CatalogSchema {
  type: string;
  description?: string;
  properties: Map<string, CatalogSchema>;
  required: Set<string>;
  closed: boolean;
  items?: CatalogSchema;
}

export interface CatalogField {
  spec: FieldSpec;
  schema?: CatalogSchema;
}

export interface CatalogPort {
  spec: PortSpec;
  payload: CatalogSchema;
}

export interface CatalogNode {
  fn: NodeFunction;
  config: CatalogField[];
  params: CatalogField[];
  inputs: CatalogPort[];
  outputs: CatalogPort[];
}

export interface ScriptCatalogProjection {
  functions: NodeFunction[];
  nodes: CatalogNode[];
  byType: Map<string, CatalogNode>;
  byAlias: Map<string, CatalogNode>;
}

export interface CatalogMember {
  label: string;
  type: string;
  documentation: CatalogDocumentation;
  schema?: CatalogSchema;
  field?: CatalogField;
  port?: CatalogPort;
}

function broadSchema(): CatalogSchema {
  return {
    type: 'any',
    properties: new Map(),
    required: new Set(),
    closed: false,
  };
}

function schemaTypeLabel(input: unknown): string {
  if (!input || typeof input !== 'object' || Array.isArray(input)) return 'any';
  const schema = input as Record<string, unknown>;
  if (schema.const !== undefined) return JSON.stringify(schema.const) ?? 'any';
  if (Array.isArray(schema.enum) && schema.enum.length > 0) {
    return schema.enum.map((value) => JSON.stringify(value) ?? 'any').join(' | ');
  }
  if (Array.isArray(schema.type)) return schema.type.join(' | ');
  if (schema.type === 'array') {
    const items = schemaTypeLabel(schema.items);
    return `array<${items}>`;
  }
  if (schema.type === 'object' || schema.properties !== undefined || schema.additionalProperties !== undefined) return 'object';
  if (schema.type === 'binary') return 'string';
  if (schema.type === 'integer') return 'number';
  return typeof schema.type === 'string' ? schema.type : 'any';
}

// projectSchema retains object openness so broad payloads never gain invented members.
export function projectSchema(input: unknown): CatalogSchema {
  if (!input || typeof input !== 'object' || Array.isArray(input)) return broadSchema();
  const schema = input as Record<string, unknown>;
  if (schema.$ref !== undefined) return broadSchema();
  const properties = new Map<string, CatalogSchema>();
  if (schema.properties && typeof schema.properties === 'object' && !Array.isArray(schema.properties)) {
    for (const [name, value] of Object.entries(schema.properties)) {
      const child = projectSchema(value);
      if (value && typeof value === 'object' && !Array.isArray(value)) {
        const description = (value as Record<string, unknown>).description;
        if (typeof description === 'string') child.description = description;
      }
      properties.set(name, child);
    }
  }
  const required = new Set(
    Array.isArray(schema.required)
      ? schema.required.filter((name): name is string => typeof name === 'string')
      : [],
  );
  const result: CatalogSchema = {
    type: schemaTypeLabel(schema),
    properties,
    required,
    closed: schema.additionalProperties === false,
  };
  if (schema.type === 'array' && schema.items && typeof schema.items === 'object' && !Array.isArray(schema.items)) {
    result.items = projectSchema(schema.items);
  }
  if (typeof schema.description === 'string') result.description = schema.description;
  return result;
}

function fieldSchema(field: FieldSpec): CatalogSchema | undefined {
  return field.value_schema ? projectSchema(field.value_schema) : undefined;
}

// parameterField reports whether a call may supply the field through
// options.params, as scriptsdk.ValidateCall decides it.
function parameterField(field: FieldSpec): boolean {
  if (field.port) return true;
  if (field.no_param) return false;
  return fieldBindable(field.type);
}

function portPayload(port: PortSpec, dynamic: boolean): CatalogSchema {
  if (port.payload_schema) return projectSchema(port.payload_schema);
  if (dynamic || port.type_rule || port.type_from) return broadSchema();
  const type = port.type === 'binary' ? 'string'
    : ['integer', 'float'].includes(port.type) ? 'number'
      : port.type === 'bool' ? 'boolean'
        : port.type || 'any';
  return { ...broadSchema(), type };
}

function projectNode(fn: NodeFunction): CatalogNode {
  const meta = fn.meta;
  const inputs = (meta.inputs ?? [])
    .filter((port) => !port.kind || port.kind === 'signal')
    .map((spec) => ({ spec, payload: portPayload(spec, false) }));
  const outputs = (meta.outputs ?? [])
    .filter((port) => port.kind !== 'error' && port.kind !== 'param')
    .map((spec) => ({ spec, payload: portPayload(spec, meta.dynamic_ports === true) }));
  return {
    fn,
    config: (meta.config_spec ?? []).filter((field) => !field.port)
      .map((spec) => ({ spec, schema: fieldSchema(spec) })),
    params: (meta.config_spec ?? []).filter(parameterField)
      .map((spec) => ({ spec, schema: fieldSchema(spec) })),
    inputs,
    outputs,
  };
}

// projectScriptCatalog is the single metadata projection shared by Python and Bash.
export function projectScriptCatalog(functions: NodeFunction[]): ScriptCatalogProjection {
  const nodes = functions.map(projectNode);
  const byType = new Map<string, CatalogNode>();
  const byAlias = new Map<string, CatalogNode>();
  for (const node of nodes) {
    byType.set(node.fn.node_type, node);
    byType.set(node.fn.name, node);
    for (const alias of node.fn.aliases ?? []) {
      if (!byAlias.has(alias)) byAlias.set(alias, node);
    }
  }
  return { functions, nodes, byType, byAlias };
}

export function fieldType(field: CatalogField): string {
  if (field.schema) return field.schema.type;
  const spec = field.spec;
  if (spec.type === 'select' && spec.options?.length) {
    return spec.options.map((option) => JSON.stringify(option.value)).join(' | ');
  }
  if (['string', 'connection', 'entity', 'ha-target', 'expression', 'duration', 'cron', 'text', 'timezone', 'script', 'markdown'].includes(spec.type)) return 'string';
  if (spec.type === 'boolean') return 'boolean';
  if (spec.type === 'number') return 'number';
  return 'any';
}

function clean(value: unknown): string {
  return String(value ?? '').replace(/\s+/g, ' ').trim();
}

function fieldLines(field: CatalogField): string[] {
  const spec = field.spec;
  const lines = [clean(spec.label) || spec.id, clean(spec.help)];
  if (spec.required) lines.push(spec.show_if ? 'Required when shown.' : 'Required.');
  if (Object.prototype.hasOwnProperty.call(spec, 'default')) {
    lines.push(`Default: ${JSON.stringify(spec.default) ?? String(spec.default)}.`);
  }
  if (spec.options?.length) {
    lines.push(`Values: ${spec.options.map((option) => `${option.value} (${option.label})`).join(', ')}.`);
  }
  return lines.filter(Boolean);
}

export function fieldDocumentation(field: CatalogField): CatalogDocumentation {
  const spec = field.spec;
  return {
    display: `${spec.id}: ${fieldType(field)}${field.schema?.closed ? ' (closed object)' : ''}`,
    documentation: fieldLines(field).join('\n'),
    tags: [`Stored config ID: ${spec.id}`],
  };
}

function portDocumentation(port: CatalogPort, direction: 'input' | 'output'): CatalogDocumentation {
  const spec = port.spec;
  const lines = [clean(spec.label) || spec.id];
  if (direction === 'input') {
    lines.push(spec.optional || spec.kind === 'signal' ? 'Optional input message.' : 'Required input message.');
  } else {
    lines.push('Always an array, including when the call emitted no messages.');
  }
  lines.push(`Payload: ${port.payload.type}.`);
  if (port.payload.description) lines.push(port.payload.description);
  if (direction === 'output' && port.payload.type === 'object' && !port.payload.closed) {
    lines.push('Payload properties are open; only documented properties are offered.');
  }
  return {
    display: `${spec.id}: ${direction === 'input' ? 'message' : 'message[]'}<${port.payload.type}>`,
    documentation: lines.filter(Boolean).join('\n'),
    tags: [`Stored ${direction} port ID: ${spec.id}`],
  };
}

export function inputDocumentation(port: CatalogPort): CatalogDocumentation {
  return portDocumentation(port, 'input');
}

export function outputDocumentation(port: CatalogPort): CatalogDocumentation {
  return portDocumentation(port, 'output');
}

export function functionDocumentation(node: CatalogNode): CatalogDocumentation {
  const { fn } = node;
  const meta = fn.meta;
  const lines = [clean(meta.label), clean(meta.description), `Node type: ${fn.node_type}.`];
  lines.push(meta.pure ? 'Pure value evaluation.' : 'May perform side effects.');
  lines.push(meta.retryable ? 'Supports per-call retry.' : 'Per-call retry is unavailable.');
  if (fn.identity_required) lines.push('options.id is required and must be stable across runs.');
  if (meta.default_retry) lines.push(`Default retry: ${JSON.stringify(meta.default_retry)}.`);
  for (const output of node.outputs) {
    const doc = outputDocumentation(output);
    lines.push(`Output ${output.spec.id}: ${doc.documentation}`);
  }
  if (meta.dynamic_ports) lines.push('Output ports are dynamic and use the broad message fallback.');
  return {
    display: `${fn.name}: ${meta.label || fn.node_type}`,
    documentation: lines.filter(Boolean).join('\n'),
    tags: [
      `Exact node type: ${fn.node_type}`,
      ...(fn.aliases?.length ? [`Aliases: ${fn.aliases.join(', ')}`] : []),
    ],
  };
}

export function schemaDocumentation(name: string, schema: CatalogSchema): CatalogDocumentation {
  const lines = [`Type: ${schema.type}.`];
  if (schema.description) lines.push(schema.description);
  if (schema.type === 'object') {
    lines.push(schema.closed
      ? 'This object is closed; only documented properties are valid.'
      : 'This object is open; undocumented dynamic properties remain valid.');
  }
  return {
    display: `${name}: ${schema.type}`,
    documentation: lines.join('\n'),
    tags: [],
  };
}

export function schemaMember(name: string, schema: CatalogSchema): CatalogMember {
  return {
    label: name,
    type: schema.type,
    documentation: schemaDocumentation(name, schema),
    schema,
  };
}

export function configMembers(node: CatalogNode): CatalogMember[] {
  return node.config.map((field) => ({
    label: field.spec.id,
    type: fieldType(field),
    documentation: fieldDocumentation(field),
    schema: field.schema,
    field,
  }));
}

export function paramMembers(node: CatalogNode): CatalogMember[] {
  return node.params.map((field) => ({
    label: field.spec.id,
    type: fieldType(field),
    documentation: fieldDocumentation(field),
    schema: field.schema,
    field,
  }));
}

export function inputMembers(node: CatalogNode): CatalogMember[] {
  if (node.fn.meta.dynamic_ports) return [];
  return node.inputs.map((port) => ({
    label: port.spec.id,
    type: 'message',
    documentation: inputDocumentation(port),
    port,
  }));
}

export function outputMembers(node: CatalogNode): CatalogMember[] {
  if (node.fn.meta.dynamic_ports) return [];
  return node.outputs.map((port) => ({
    label: port.spec.id,
    type: 'message[]',
    documentation: outputDocumentation(port),
    port,
  }));
}

export function messageMembers(port: CatalogPort): CatalogMember[] {
  const payload = port.payload;
  const message = (name: string, type: string, description: string, schema?: CatalogSchema): CatalogMember => ({
    label: name,
    type,
    documentation: {
      display: `${name}: ${type}`,
      documentation: description,
      tags: [],
    },
    schema,
  });
  return [
    message('payload', payload.type, 'The output message payload.', payload),
    message('topic', 'string', 'The output message topic.'),
    message('meta', 'object', 'Message metadata.'),
    message('id', 'string', 'The message ID, when present.'),
    message('group', 'array', 'Message grouping frames, when present.'),
    message('continuation', 'boolean', 'Whether this message continues a sequence.'),
  ];
}

export function optionMembers(node: CatalogNode): CatalogMember[] {
  const member = (label: string, type: string, text: string): CatalogMember => ({
    label,
    type,
    documentation: { display: `${label}: ${type}`, documentation: text, tags: [] },
  });
  const members = [
    member('id', 'string', node.fn.identity_required
      ? 'Required stable invocation identity. Keep the same ID across runs.'
      : 'Optional stable invocation identity.'),
  ];
  if (node.params.length) {
    members.push(member('params', 'object', 'Config values supplied as value parameters, keyed by stored field ID.'));
  }
  if (node.fn.meta.retryable) {
    const lines = ['Per-call retry policy.'];
    if (node.fn.meta.default_retry) lines.push(`Default retry: ${JSON.stringify(node.fn.meta.default_retry)}.`);
    members.push(member('retry', 'object', lines.join('\n')));
  }
  return members;
}

export function retryMembers(): CatalogMember[] {
  const descriptions: Record<string, string> = {
    max_attempts: 'Maximum attempts, including the first execution.',
    delay_ms: 'Initial delay between attempts in milliseconds.',
    backoff: 'Multiplier applied to the delay after each attempt.',
    max_delay_ms: 'Maximum delay between attempts in milliseconds.',
    jitter: 'Optional random delay fraction.',
  };
  return Object.entries(descriptions).map(([label, documentation]) => ({
    label,
    type: 'number',
    documentation: {
      display: `${label}: number`,
      documentation,
      tags: [],
    },
  }));
}

export function membersForPath(node: CatalogNode, section: 'config' | 'inputs' | 'options', path: string[]): CatalogMember[] {
  if (section === 'config') {
    if (path.length === 0) return configMembers(node);
    const field = node.config.find((candidate) => candidate.spec.id === path[0]);
    if (!field?.schema) return [];
    return schemaMembersAt(field.schema, path.slice(1));
  }
  if (section === 'inputs') {
    if (path.length === 0) return inputMembers(node);
    if (path.length === 1) {
      const port = node.inputs.find((candidate) => candidate.spec.id === path[0]);
      return port ? messageMembers(port) : [];
    }
    return [];
  }
  if (path.length === 0) return optionMembers(node);
  if (path[0] === 'params' && path.length === 1) return paramMembers(node);
  if (path[0] === 'retry' && path.length === 1 && node.fn.meta.retryable) return retryMembers();
  return [];
}

export function schemaAtPath(schema: CatalogSchema, path: (string | number)[]): CatalogSchema | undefined {
  let current = schema;
  for (const segment of path) {
    if (typeof segment === 'number') {
      if (!current.items) return undefined;
      current = current.items;
      continue;
    }
    const next = current.properties.get(segment);
    if (!next) return undefined;
    current = next;
  }
  return current;
}

export function schemaMembersAt(schema: CatalogSchema, path: (string | number)[]): CatalogMember[] {
  const current = schemaAtPath(schema, path);
  return current ? [...current.properties.entries()].map(([name, value]) => schemaMember(name, value)) : [];
}

export function memberAtPath(members: CatalogMember[], name: string): CatalogMember | undefined {
  return members.find((member) => member.label === name);
}

export function inputsRequired(meta: NodeMeta): boolean {
  if (meta.dynamic_ports) return false;
  return (meta.inputs ?? []).some((port) => !port.optional && !port.kind);
}
