import type { FieldSpec, NodeFunction, NodeMeta, PortSpec } from '../../stores/nodeCatalog';
import { fieldBindable } from '../../utils/payloadPorts';

const sandboxDeclarations = `
interface ScriptGroupFrame {
  id: string;
  index: number;
  count: number;
}

interface ScriptMessage<Payload = any> {
  id?: string;
  payload: Payload;
  topic?: string;
  meta?: { [key: string]: any };
  group?: ScriptGroupFrame[];
  continuation?: boolean;
}

interface ScriptNodeRetry {
  max_attempts: number;
  delay_ms: number;
  backoff: number;
  max_delay_ms: number;
  jitter?: number;
}

interface ScriptDynamicOptions {
  id?: string;
  retry?: ScriptNodeRetry;
  params?: { [field: string]: any };
}

interface ScriptHTTPOptions {
  method?: string;
  url?: string;
  headers?: { [name: string]: string };
  body?: string;
}

interface ScriptHTTPResponse {
  status: number;
  ok: boolean;
  headers: { [name: string]: string };
  body: string;
  error: string;
  json(): any;
}

declare const msg: ScriptMessage;
declare const payload: any;
declare const topic: string;
declare const meta: { [key: string]: any };
declare const input: { [port: string]: any };
declare const vars: { [key: string]: any };
declare const flow: { [key: string]: any };
declare const global: { [key: string]: any };
declare function get(name?: string, scope?: "msg" | "run" | "flow" | "global"): any;
declare function set(name: string, value: any, scope?: "msg" | "run" | "flow" | "global"): void;
declare function http(url?: string | ScriptHTTPOptions, options?: ScriptHTTPOptions): ScriptHTTPResponse;
declare const console: {
  log(...values: any[]): void;
  info(...values: any[]): void;
  warn(...values: any[]): void;
  error(...values: any[]): void;
  debug(...values: any[]): void;
};
`;

interface NodeDeclarationNames {
  config: string;
  inputs: string;
  options: string;
  outputs: string;
  params: string;
}

function commentText(value: unknown): string {
  return String(value ?? '')
    .replace(/\*\//g, '* /')
    .replace(/[\r\n\u2028\u2029]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
}

function doc(lines: unknown[], indent = ''): string {
  const content = lines
    .flatMap((line) => Array.isArray(line) ? line : [line])
    .map(commentText)
    .filter(Boolean);
  if (content.length === 0) return '';
  return `${indent}/**\n${content.map((line) => `${indent} * ${line}`).join('\n')}\n${indent} */\n`;
}

function literal(value: unknown): string | null {
  if (value === null) return 'null';
  switch (typeof value) {
    case 'string':
    case 'number':
    case 'boolean': {
      const encoded = JSON.stringify(value);
      return encoded === undefined ? null : encoded
        .replace(/\u2028/g, '\\u2028')
        .replace(/\u2029/g, '\\u2029');
    }
    default:
      return null;
  }
}

function schemaObject(schema: Record<string, unknown>): boolean {
  return schema.type === 'object' || schema.properties !== undefined || schema.additionalProperties !== undefined;
}

// jsonSchemaType converts the JSON Schema subset carried by node metadata.
// Unknown constructs stay broad instead of inventing restrictions the runtime
// does not have.
export function jsonSchemaType(input: unknown): string {
  if (input === true) return 'any';
  if (input === false) return 'never';
  if (!input || typeof input !== 'object' || Array.isArray(input)) return 'any';
  const schema = input as Record<string, unknown>;
  if (schema.$ref !== undefined) return 'any';

  if (schema.const !== undefined) return literal(schema.const) ?? 'any';
  if (Array.isArray(schema.enum)) {
    const values = schema.enum.map(literal).filter((value): value is string => value !== null);
    if (values.length === schema.enum.length && values.length > 0) return [...new Set(values)].join(' | ');
  }
  if (Array.isArray(schema.anyOf)) {
    return schema.anyOf.length > 0 ? schema.anyOf.map(jsonSchemaType).map((type) => `(${type})`).join(' | ') : 'never';
  }
  if (Array.isArray(schema.oneOf)) {
    return schema.oneOf.length > 0 ? schema.oneOf.map(jsonSchemaType).map((type) => `(${type})`).join(' | ') : 'never';
  }
  if (Array.isArray(schema.allOf)) {
    return schema.allOf.length > 0 ? schema.allOf.map(jsonSchemaType).map((type) => `(${type})`).join(' & ') : 'any';
  }

  if (Array.isArray(schema.type)) {
    const types = schema.type.map((type) => jsonSchemaType({ ...schema, type }));
    return [...new Set(types)].map((type) => `(${type})`).join(' | ');
  }

  if (schema.type === 'array') {
    if (Array.isArray(schema.items)) return `[${schema.items.map(jsonSchemaType).join(', ')}]`;
    return `Array<${jsonSchemaType(schema.items ?? true)}>`;
  }

  if (schemaObject(schema)) {
    const properties = schema.properties && typeof schema.properties === 'object' && !Array.isArray(schema.properties)
      ? schema.properties as Record<string, unknown>
      : {};
    const required = new Set(Array.isArray(schema.required) ? schema.required.filter((key): key is string => typeof key === 'string') : []);
    const entries = Object.entries(properties);
    const additional = schema.additionalProperties;

    // With no declared properties, Record preserves a typed additionalProperties
    // schema without weakening it to accommodate declared property types.
    if (entries.length === 0 && additional && typeof additional === 'object' && !Array.isArray(additional)) {
      return `{ [key: string]: ${jsonSchemaType(additional)} }`;
    }

    const lines: string[] = [];
    for (const [key, value] of entries) {
      const description = value && typeof value === 'object' && !Array.isArray(value)
        ? (value as Record<string, unknown>).description
        : undefined;
      lines.push(doc([description], '  ').trimEnd());
      lines.push(`  ${JSON.stringify(key)}${required.has(key) ? '' : '?'}: ${jsonSchemaType(value)};`);
    }
    if (additional !== false) {
      // JSON Schema objects are open unless explicitly closed. An any index also
      // avoids TypeScript's requirement that known properties match the index.
      lines.push('  [key: string]: any;');
    } else if (entries.length === 0) {
      lines.push('  [key: string]: never;');
    }
    return `{\n${lines.filter(Boolean).join('\n')}\n}`;
  }

  switch (schema.type) {
    case 'string': return 'string';
    case 'number':
    case 'integer': return 'number';
    case 'boolean': return 'boolean';
    case 'null': return 'null';
    case 'binary': return 'string';
    default: return 'any';
  }
}

function fieldType(field: FieldSpec): string {
  if (field.value_schema) return jsonSchemaType(field.value_schema);
  switch (field.type) {
    case 'string':
    case 'connection':
    case 'entity':
    case 'ha-target':
    case 'expression':
    case 'duration':
    case 'cron':
    case 'text':
    case 'timezone':
    case 'script':
    case 'markdown':
      return 'string';
    case 'number':
      return 'number';
    case 'boolean':
      return 'boolean';
    case 'select': {
      const values = (field.options ?? []).map((option) => literal(option.value)).filter((value): value is string => value !== null);
      return values.length > 0 ? [...new Set(values)].join(' | ') : 'string';
    }
    default:
      return 'any';
  }
}

// parameterField reports whether a call may supply the field through
// options.params, as scriptsdk.ValidateCall decides it.
function parameterField(field: FieldSpec): boolean {
  if (field.port) return true;
  if (field.no_param) return false;
  return fieldBindable(field.type);
}

function portPayloadType(port: PortSpec, dynamic: boolean): string {
  if (port.payload_schema) return jsonSchemaType(port.payload_schema);
  if (dynamic || port.type_rule || port.type_from) return 'any';
  switch (port.type) {
    case 'string':
    case 'binary':
      return 'string';
    case 'number':
    case 'integer':
    case 'float':
      return 'number';
    case 'bool':
    case 'boolean':
      return 'boolean';
    default:
      return 'any';
  }
}

function fieldDocumentation(field: FieldSpec): string[] {
  const lines: string[] = [];
  if (field.label) lines.push(field.label);
  if (field.help) lines.push(field.help);
  if (field.required) lines.push(field.show_if ? 'Required when shown.' : 'Required.');
  if (Object.prototype.hasOwnProperty.call(field, 'default')) {
    lines.push(`Default: ${JSON.stringify(field.default) ?? String(field.default)}.`);
  }
  return lines;
}

function functionDocumentation(fn: NodeFunction): string[] {
  const meta = fn.meta;
  const lines = [meta.label, meta.description, `Node type: ${fn.node_type}.`];
  lines.push(meta.pure ? 'Pure value evaluation.' : 'May perform side effects.');
  lines.push(meta.retryable ? 'Supports per-call retry.' : 'Per-call retry is unavailable.');
  if (fn.identity_required) lines.push('options.id is required and must be stable across runs.');
  if (meta.default_retry) lines.push(`Default retry: ${JSON.stringify(meta.default_retry)}.`);
  lines.push('Throws NodeCallError when invocation fails.');
  lines.push('@param config Node configuration using stored field IDs.');
  lines.push('@param inputs Complete input messages keyed by port ID.');
  lines.push('@param options Invocation identity, retry, and value parameters.');
  lines.push('@returns Every ordinary output port, with an array even when no messages were emitted.');
  return lines;
}

function configInterface(fn: NodeFunction, name: string): string {
  const fields = (fn.meta.config_spec ?? []).filter((field) => !field.port);
  const lines = fields.map((field) =>
    `${doc(fieldDocumentation(field), '  ')}  ${JSON.stringify(field.id)}${field.required && !field.show_if ? '' : '?'}: ${fieldType(field)};`,
  );
  if (fields.length === 0) lines.push('  [key: string]: never;');
  return `interface ${name} {\n${lines.join('\n')}\n}`;
}

function paramsInterface(fn: NodeFunction, name: string): string {
  const fields = (fn.meta.config_spec ?? []).filter(parameterField);
  const lines = fields.map((field) => {
    const type = field.multi ? `Array<${fieldType(field)}>` : fieldType(field);
    const optional = field.port && field.required && !field.show_if ? '' : '?';
    return `${doc(fieldDocumentation(field), '  ')}  ${JSON.stringify(field.id)}${optional}: ${type};`;
  });
  if (fields.length === 0) lines.push('  [key: string]: never;');
  return `interface ${name} {\n${lines.join('\n')}\n}`;
}

function inputsInterface(meta: NodeMeta, name: string): string {
  if (meta.dynamic_ports) return `interface ${name} {\n  [port: string]: ScriptMessage;\n}`;
  const inputs = (meta.inputs ?? []).filter((port) => !port.kind || port.kind === 'signal');
  const lines = inputs.map((port) =>
    `${doc([port.label, `Input payload: ${portPayloadType(port, false)}.`], '  ')}  ${JSON.stringify(port.id)}${port.optional || port.kind === 'signal' ? '?' : ''}: ScriptMessage<${portPayloadType(port, false)}>;`,
  );
  if (inputs.length === 0) lines.push('  [port: string]: never;');
  return `interface ${name} {\n${lines.join('\n')}\n}`;
}

function optionsInterface(fn: NodeFunction, name: string, params: string): string {
  const lines = [`  id${fn.identity_required ? '' : '?'}: string;`];
  if (fn.meta.retryable) lines.push('  retry?: ScriptNodeRetry;');
  lines.push(`  params?: ${params};`);
  return `interface ${name} {\n${lines.join('\n')}\n}`;
}

function outputsInterface(meta: NodeMeta, name: string): string {
  if (meta.dynamic_ports) return `interface ${name} {\n  [port: string]: ScriptMessage[];\n}`;
  const outputs = (meta.outputs ?? []).filter((port) => port.kind !== 'error' && port.kind !== 'param');
  const lines = outputs.map((port) =>
    `${doc([port.label, 'Always an array, including when the call emitted no messages.'], '  ')}  ${JSON.stringify(port.id)}: ScriptMessage<${portPayloadType(port, false)}>[];`,
  );
  if (outputs.length === 0) lines.push('  [port: string]: never;');
  return `interface ${name} {\n${lines.join('\n')}\n}`;
}

function inputsRequired(meta: NodeMeta): boolean {
  if (meta.dynamic_ports) return false;
  return (meta.inputs ?? []).some((port) => !port.optional && !port.kind);
}

function mappingInterface(name: string, functions: NodeFunction[], names: NodeDeclarationNames[], key: keyof NodeDeclarationNames): string {
  const lines = functions.map((fn, index) => `  ${JSON.stringify(fn.node_type)}: ${names[index][key]};`);
  return `interface ${name} {\n${lines.join('\n')}\n}`;
}

// generateScriptSdkDeclarations derives the complete ambient script contract
// from the served function catalog. No node names or shapes are maintained here.
export function generateScriptSdkDeclarations(functions: NodeFunction[]): string {
  const names = functions.map((_, index): NodeDeclarationNames => ({
    config: `ScriptNode${index}Config`,
    inputs: `ScriptNode${index}Inputs`,
    options: `ScriptNode${index}Options`,
    outputs: `ScriptNode${index}Outputs`,
    params: `ScriptNode${index}Params`,
  }));
  const parts = [sandboxDeclarations.trim()];

  functions.forEach((fn, index) => {
    const name = names[index];
    parts.push(
      configInterface(fn, name.config),
      inputsInterface(fn.meta, name.inputs),
      paramsInterface(fn, name.params),
      optionsInterface(fn, name.options, name.params),
      outputsInterface(fn.meta, name.outputs),
    );
  });

  parts.push(
    mappingInterface('ScriptNodeConfigByType', functions, names, 'config'),
    mappingInterface('ScriptNodeInputsByType', functions, names, 'inputs'),
    mappingInterface('ScriptNodeOptionsByType', functions, names, 'options'),
    mappingInterface('ScriptNodeOutputsByType', functions, names, 'outputs'),
  );

  const methods: string[] = [];
  const aliases = new Set<string>();
  functions.forEach((fn, index) => {
    const name = names[index];
    const signature = `(config: ${name.config}, inputs${inputsRequired(fn.meta) ? '' : '?'}: ${name.inputs}, options?: ${name.options}): ${name.outputs};`;
    methods.push(`${doc(functionDocumentation(fn))}${JSON.stringify(fn.name)}${signature}`);
    for (const alias of fn.aliases ?? []) {
      if (alias === fn.name || alias === 'call' || aliases.has(alias)) continue;
      aliases.add(alias);
      methods.push(`${doc(functionDocumentation(fn))}${JSON.stringify(alias)}${signature}`);
    }
  });

  methods.push(`${doc(['Calls a node by its exact registered type.', 'Literal known types retain their closed config, input, option, and output contracts. Dynamic types use the broad fallback.'])}${JSON.stringify('call')}<const Type extends string>(\n  nodeType: Type,\n  config: Type extends keyof ScriptNodeConfigByType ? ScriptNodeConfigByType[Type] : { [field: string]: unknown },\n  inputs?: Type extends keyof ScriptNodeInputsByType ? ScriptNodeInputsByType[Type] : { [port: string]: ScriptMessage },\n  options?: Type extends keyof ScriptNodeOptionsByType ? ScriptNodeOptionsByType[Type] : ScriptDynamicOptions,\n): Type extends keyof ScriptNodeOutputsByType ? ScriptNodeOutputsByType[Type] : { [port: string]: ScriptMessage[] };`);
  parts.push(`interface ScriptNodes {\n${methods.map((method) => method.split('\n').map((line) => `  ${line}`).join('\n')).join('\n')}\n}`);
  parts.push('declare const nodes: ScriptNodes;');
  return `${parts.join('\n\n')}\n`;
}
