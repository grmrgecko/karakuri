// Mirrors the backend's pkg/flow/payload.go and flow.Compatible so the canvas
// derives and checks port types exactly the way validation does. A rule that
// only lived on one side would let an author draw an edge the server then
// rejects, or block one it would have accepted.

import type { FieldSpec, PortSpec } from '../stores/nodeCatalog';

// Payload types, the vocabulary behind the "output type" setting.
export const PayloadString = 'string';
export const PayloadInteger = 'integer';
export const PayloadFloat = 'float';
export const PayloadBoolean = 'boolean';
export const PayloadJSON = 'json';
export const PayloadYAML = 'yaml';

// Port types describing a port's payload type. Every port carries a message
// whatever its type — the topic and metadata ride along regardless — so there
// is no "message" type; PortAny is an undeclared payload.
export const PortBool = 'bool';
export const PortNumber = 'number';
export const PortInteger = 'integer';
export const PortFloat = 'float';
export const PortString = 'string';
export const PortObject = 'object';
// PortBinary is bytes. Whatever carries them, the separate name is what lets a
// port say it holds encoded bytes rather than text meant to be read.
export const PortBinary = 'binary';
export const PortAny = 'any';

// ---- Registered port types ----
//
// Types are data served by the backend registry (flow.PortTypes), so a
// plugin's type gets labels, compatibility, colors, and value-parameter
// eligibility without a frontend change. The built-in list below is the
// compiled-in fallback matching the backend's own init registration; the
// node catalog replaces it with the served list on load.

export interface PortTypeDef {
  name: string;
  label: string;
  accepts?: string;
  widens_to?: string[];
  value?: boolean;
  // script marks a type an author may declare on a script node's ports; a
  // capability type like an AI tool set stays out of the picker.
  script?: boolean;
}

const builtinPortTypes: PortTypeDef[] = [
  { name: PortAny, label: 'Any', script: true },
  { name: PortString, label: 'String', value: true, script: true },
  { name: PortNumber, label: 'Number', value: true, script: true },
  { name: PortInteger, label: 'Integer', widens_to: [PortNumber, PortFloat], value: true, script: true },
  { name: PortFloat, label: 'Float', widens_to: [PortNumber], value: true, script: true },
  { name: PortBool, label: 'Boolean', value: true, script: true },
  { name: PortObject, label: 'Object', value: true, script: true },
  { name: PortBinary, label: 'Binary', widens_to: [PortString], value: true, script: true },
];

let registeredTypes: PortTypeDef[] = builtinPortTypes;
let typesByName = new Map(builtinPortTypes.map((d) => [d.name, d]));

// hydratePortTypes replaces the compiled-in type list with the server's, so
// everything derived from it — labels, compatibility, colors — follows the
// registry. Called by the node catalog on load.
export function hydratePortTypes(defs: PortTypeDef[]): void {
  if (!defs?.length) return;
  registeredTypes = defs;
  typesByName = new Map(defs.map((d) => [d.name, d]));
}

// portTypeList returns the registered types in registration order, which the
// rotating-hue colors key off.
export function portTypeList(): PortTypeDef[] {
  return registeredTypes;
}

// payloadPortType maps a payload type to the port type a node emitting it
// advertises. JSON and YAML differ only in how the text is written, so both
// produce an object; an unset type falls back to the untyped message port.
export function payloadPortType(payloadType: unknown): string {
  switch (payloadType) {
    case PayloadString:
      return PortString;
    case PayloadInteger:
      return PortInteger;
    case PayloadFloat:
      return PortFloat;
    case PayloadBoolean:
      return PortBool;
    case PayloadJSON:
    case PayloadYAML:
      return PortObject;
    default:
      return PortAny;
  }
}

// The manual node's port type is no longer derived here: its output carries a
// type_rule keyed on output_format, resolved by resolvePortTypes like every
// other config-typed port, so nodePorts needs no special case for it.

// compatible mirrors flow.Compatible: whether a value of type src may be
// assigned to a port of type tgt. The relation is directional, because
// assignability is. This is the rule for value edges, where a node supplies a
// config field of a declared type; message edges use messageCompatible.
export function compatible(src: string, tgt: string): boolean {
  // Any type connects to an "any" input, which is what keeps typing a node from
  // breaking the graphs already wired to it. The reverse does not hold: an
  // undeclared value is what a declared type exists to reject.
  if (src === tgt || tgt === PortAny) return true;

  // The widening pairs (integer to float and number, binary to string) are
  // data on the registered types, so a plugin type states its own
  // assignability.
  return typesByName.get(src)?.widens_to?.includes(tgt) ?? false;
}

// messageCompatible mirrors flow.MessageCompatible: compatible, plus an
// undeclared source satisfying a typed target. On the message path "any" means
// the payload is not knowable until the run — a webhook body, an MQTT payload,
// a script's return value — and refusing to wire that into a typed input would
// forbid the graphs those nodes exist for. A declared mismatch is still caught.
export function messageCompatible(src: string, tgt: string): boolean {
  return src === PortAny || compatible(src, tgt);
}

// portTypeLabel names a payload type for a badge or tooltip. A registered
// type uses its label; a type the registry has not seen shows its own name
// rather than pretending to be undeclared, and an empty type reads as Any.
export function portTypeLabel(type: string): string {
  const def = typesByName.get(type);
  if (def) return def.label;
  if (!type) return 'Any';
  return type.charAt(0).toUpperCase() + type.slice(1);
}

// portTypeAccepts names the other types a port of this type also takes, for a
// hint beside a declared type. Derived from the registered widenings: the
// types that widen into this one are what satisfies it beyond itself.
export function portTypeAccepts(type: string): string | null {
  const sources = registeredTypes
    .filter((d) => d.name !== type && d.widens_to?.includes(type))
    .map((d) => `a${/^[aeiou]/i.test(d.label) ? 'n' : ''} ${d.label.toLowerCase()}`);
  if (sources.length === 0) return null;
  if (sources.length === 1) return sources[0];
  return sources.slice(0, -1).join(', ') + ' or ' + sources[sources.length - 1];
}

// ---- Config-driven port types ----
//
// Mirrors pkg/flow/porttypes.go. A node's payload type usually depends on how
// it is configured — a JSON node decodes text into an object, a file read as
// binary carries bytes — and the rule that says so travels with the port, so
// the canvas and the server derive it from one description instead of two
// copies of a per-node switch.

export interface PortTypeCase {
  match?: Record<string, string[]>;
  type: string;
  // Overrides the rule's payload_path while this case applies; empty defers
  // to the rule.
  payload_path?: string[];
}

export interface PortTypeRule {
  cases?: PortTypeCase[];
  default?: string;
  payload_path?: string[];
}

// Where a dot path sits relative to the payload, which is all a port type can
// describe.
const PATH_PAYLOAD = 0;
const PATH_IN_PAYLOAD = 1;
const PATH_OFF_PAYLOAD = 2;

// resolvePortType derives a port's payload type from a node's config. A port
// with no rule keeps the type it declared.
export function resolvePortType(port: PortSpec, config: Record<string, unknown> | undefined): string {
  return resolvePortRule(port, config).type;
}

// resolvePortRule derives the type and reports whether a matched case asserted
// "any" outright. Mirrors the backend distinction: a rule falling through to
// its default defers to propagation, but a case that names "any" states the
// payload is config-defined and unknowable, which drops the port's type_from.
// An off-payload path never asserts — the payload then passes through
// untouched, which is exactly what type_from exists to say.
function resolvePortRule(
  port: PortSpec,
  config: Record<string, unknown> | undefined
): { type: string; assertedAny: boolean } {
  const rule = port.type_rule;
  if (!rule) return { type: port.type, assertedAny: false };

  // Pick the type from the first case whose config matches. The matched case
  // may scope the payload path to itself; no match leaves the rule's own.
  let resolved = rule.default || port.type;
  let asserted = false;
  let pathFields = rule.payload_path;
  for (const c of rule.cases ?? []) {
    if (matchesConfig(c.match, config)) {
      resolved = c.type || port.type;
      asserted = c.type === PortAny;
      if (c.payload_path?.length) pathFields = c.payload_path;
      break;
    }
  }

  // Narrow it to what the port actually carries, which is the payload.
  switch (payloadPathScope(pathFields, config)) {
    case PATH_IN_PAYLOAD:
      return { type: PortObject, assertedAny: false };
    case PATH_OFF_PAYLOAD:
      return { type: PortAny, assertedAny: false };
  }
  return { type: resolved, assertedAny: asserted && resolved === PortAny };
}

// matchesConfig reports whether every entry of a case's match set is satisfied.
// An empty match set matches anything, which is how a rule writes a catch-all;
// "*" is satisfied by any non-empty value.
function matchesConfig(
  match: Record<string, string[]> | undefined,
  config: Record<string, unknown> | undefined
): boolean {
  for (const [field, allowed] of Object.entries(match ?? {})) {
    const value = configString(config?.[field]);
    if (!allowed.some((want) => want === value || (want === '*' && value !== ''))) return false;
  }
  return true;
}

// payloadPathScope resolves the configured dot path and classifies it. Fields
// are consulted in order and the first with a value wins, so a parser whose
// target is empty falls back to the property it converted.
function payloadPathScope(
  fields: string[] | undefined,
  config: Record<string, unknown> | undefined
): number {
  if (!fields?.length) return PATH_PAYLOAD;
  let path = '';
  for (const field of fields) {
    const value = configString(config?.[field]).trim();
    if (value) {
      path = value;
      break;
    }
  }
  // The nodes that read or write a property all default to the payload, so an
  // unset path is the payload rather than an unknown.
  if (path === '' || path === 'payload') return PATH_PAYLOAD;
  // Roots other than the payload leave it alone; everything else, including a
  // bare path, is a property inside it.
  const root = path.split('.')[0];
  if (root === 'topic' || root === 'meta') return PATH_OFF_PAYLOAD;
  return PATH_IN_PAYLOAD;
}

// configString renders a config value the way a rule compares it. Select fields
// are already strings; booleans and numbers are written the way they read in
// the editor so a rule can name them literally.
function configString(v: unknown): string {
  if (typeof v === 'string') return v;
  if (typeof v === 'boolean' || typeof v === 'number') return String(v);
  return '';
}

// resolvePortTypes applies each port's type rule against the node's config.
// A case asserting "any" also drops the port's type_from, so type propagation
// cannot dress a config-defined payload in the upstream's type.
export function resolvePortTypes(
  ports: PortSpec[],
  config: Record<string, unknown> | undefined
): PortSpec[] {
  if (!ports.some((p) => p.type_rule)) return ports;
  return ports.map((p) => {
    const { type, assertedAny } = resolvePortRule(p, config);
    const clearFrom = assertedAny && !!p.type_from;
    if (type === p.type && !clearFrom) return p;
    const resolved = { ...p, type };
    if (clearFrom) delete resolved.type_from;
    return resolved;
  });
}

// ---- Parameter ports ----
//
// Mirrors pkg/flow/params.go. A config field can be exposed as a value input
// port so another node supplies it; the field keeps its literal as the fallback.

const PARAM_PREFIX = 'param:';

export function paramPortId(fieldId: string): string {
  return PARAM_PREFIX + fieldId;
}

export function paramFieldId(portId: string | null | undefined): string | null {
  if (!portId || !portId.startsWith(PARAM_PREFIX)) return null;
  return portId.slice(PARAM_PREFIX.length);
}

// fieldPortType maps a config field's editor type to the port type that can
// supply it. Fields with no entry cannot be driven by a wire.
export function fieldPortType(fieldType: string): string | null {
  switch (fieldType) {
    case 'string':
    case 'text':
    case 'markdown':
    case 'expression':
    case 'duration':
    case 'cron':
    case 'timezone':
    case 'entity':
    case 'ha-target':
    case 'select':
    case 'script':
      return PortString;
    case 'number':
      return PortNumber;
    case 'boolean':
      return PortBool;
    case 'json':
      return PortObject;
    default:
      return null;
  }
}

export function fieldBindable(fieldType: string): boolean {
  return fieldPortType(fieldType) !== null;
}

// paramPorts mirrors flow.ParamPorts: every port field of the node type first
// (those are permanent sockets), then the config fields the node opted in.
export function paramPorts(
  spec: FieldSpec[] | undefined,
  params: string[] | undefined,
  config: Record<string, unknown> | undefined
): PortSpec[] {
  if (!spec?.length) return [];
  const ports: PortSpec[] = [];
  for (const field of spec) {
    // A port field with a show_if produces its socket only while the
    // condition holds, mirroring pkg/flow's ParamPorts — the guardrail's
    // model input exists only while a model-scored check is picked.
    if (!field.port || !fieldShown(spec, field, config)) continue;
    ports.push({
      id: paramPortId(field.id),
      label: field.label || field.id,
      type: field.port,
      kind: 'param',
      multi: field.multi,
      optional: true,
    });
  }
  const byId = new Map(spec.map((f) => [f.id, f]));
  for (const fieldId of params ?? []) {
    const field = byId.get(fieldId);
    if (!field || field.port || field.no_param) continue;
    const type = fieldPortType(field.type);
    if (!type) continue;
    ports.push({
      id: paramPortId(fieldId),
      label: field.label || fieldId,
      type,
      kind: 'param',
      optional: true,
    });
  }
  return ports;
}

// fieldShown mirrors pkg/flow's FieldShown: a field's show_if is evaluated
// against the node's config with loose comparison, falling back to the
// controlling field's declared default when unset. A list expectation matches
// any of its values, "*" matches any non-empty value (the PortTypeRule
// convention), and a comma-separated controlling value (the tokens control)
// matches when any one token does. Both the config panel and port derivation
// call this, so a field and its socket appear and vanish together.
export function fieldShown(
  spec: FieldSpec[] | undefined,
  field: FieldSpec,
  values: Record<string, unknown> | undefined
): boolean {
  if (!field.show_if) return true;
  let actual = values?.[field.show_if.field];
  if (actual === undefined) {
    actual = spec?.find((f) => f.id === field.show_if!.field)?.default;
  }
  return showIfMatches(actual, field.show_if.equals);
}

function showIfMatches(actual: unknown, expected: unknown): boolean {
  if (Array.isArray(expected)) {
    return expected.some((e) => showIfMatches(actual, e));
  }
  if (expected === '*') {
    return actual !== undefined && actual !== null && String(actual).trim() !== '';
  }
  if (typeof actual === 'string' && actual.includes(',')) {
    return actual.split(',').some((token) => looseEquals(token.trim(), expected));
  }
  return looseEquals(actual, expected);
}

// looseEquals compares with string coercion so numeric values match their
// string forms, the same tolerance the backend's looseEquals gives.
function looseEquals(a: unknown, b: unknown): boolean {
  return a === b || String(a) === String(b);
}

// ---- Script ports ----
//
// Mirrors pkg/flow/scriptports.go: a script declares its own signature, and an
// undeclared one keeps the shape it always had.

export interface ScriptPort {
  id: string;
  label?: string;
  type?: string;
}

export const SCRIPT_ERROR_PORT = 'err';

// scriptPortTypes lists the types a script port declaration can pick from:
// the registered types that opted in, mirroring flow.ScriptPortTypes so the
// picker never offers a type validation rejects.
export function scriptPortTypes(): string[] {
  return registeredTypes.filter((d) => d.script).map((d) => d.name);
}

function scriptPortSpecs(declared: ScriptPort[] | undefined, defaultType: string): PortSpec[] {
  if (!declared?.length) return [];
  const seen = new Set<string>();
  const specs: PortSpec[] = [];
  for (const p of declared) {
    if (!p.id || seen.has(p.id)) continue;
    seen.add(p.id);
    specs.push({ id: p.id, label: p.label || p.id, type: p.type || defaultType });
  }
  return specs;
}

export function scriptPorts(config: Record<string, unknown> | undefined): {
  inputs: PortSpec[];
  outputs: PortSpec[];
} {
  const declaredIn = config?.inputs as ScriptPort[] | undefined;
  const declaredOut = config?.outputs as ScriptPort[] | undefined;

  let inputs = scriptPortSpecs(declaredIn, PortAny);
  if (inputs.length === 0) inputs = [{ id: 'in', label: 'In', type: PortAny }];

  let outputs = scriptPortSpecs(declaredOut, PortAny);
  if (outputs.length === 0) outputs = [{ id: 'out', label: 'Out', type: PortAny }];
  // Like every error port, the appended branch carries what the node was
  // handed, so it reports the first input's type.
  if (!outputs.some((p) => p.id === SCRIPT_ERROR_PORT)) {
    outputs = [
      ...outputs,
      { id: SCRIPT_ERROR_PORT, label: 'Error', type: PortAny, kind: 'error', type_from: inputs[0].id },
    ];
  }
  return { inputs, outputs };
}

// isValuePortType mirrors flow.IsValuePortType: which declared port types carry
// a value rather than the message itself. A subflow input declared with one
// becomes a parameter on the enclosing subflow node. Answered from the type
// registry, so a plugin type opts in through its definition.
export function isValuePortType(type: string): boolean {
  return typesByName.get(type)?.value === true;
}

// ---- Tool node argument ports (mirrors pkg/flow/toolports.go) ----

export interface ToolArg {
  id: string;
  label?: string;
  type?: string;
  description?: string;
  required?: boolean;
}

export const TOOL_CALL_PORT = 'call';
export const TOOL_RETURN_PORT = 'return';

const TOOL_ARG_PREFIX = 'arg:';

export function toolArgPortId(argId: string): string {
  return TOOL_ARG_PREFIX + argId;
}

export function toolArgId(portId: string | null | undefined): string | null {
  if (!portId || !portId.startsWith(TOOL_ARG_PREFIX)) return null;
  return portId.slice(TOOL_ARG_PREFIX.length);
}

// toolArgTypes is the closed set a tool argument may declare, mirroring
// flow.ValidToolArgType: an argument must have a JSON Schema shape the model
// can fill in, which rules out any and binary.
export function toolArgTypes(): string[] {
  return [PortString, PortNumber, PortInteger, PortFloat, PortBool, PortObject];
}

// lowerRunes lowercases the way Go's strings.ToLower does: each code point by
// its simple mapping. toLowerCase on the whole string applies the full
// mapping, which depends on context (a capital sigma ending a word) and may
// write two code points for one (a dotted capital I), so the port IDs built
// from it would not be the server's.
function lowerRunes(text: string): string {
  return Array.from(text, (rune) => Array.from(rune.toLowerCase())[0]).join('');
}

// splitTokens splits a tokens-control value into trimmed entries, empties
// dropped and case-insensitive duplicates collapsed onto the first spelling.
// Mirrors flow.SplitTokens.
function splitTokens(field: string | undefined): string[] {
  if (!field) return [];
  const out: string[] = [];
  const seen = new Set<string>();
  for (const part of field.split(',')) {
    const token = part.trim();
    if (token === '' || seen.has(lowerRunes(token))) continue;
    seen.add(lowerRunes(token));
    out.push(token);
  }
  return out;
}

// classifyPorts derives the classifier's category outputs from its config:
// one port per category in the order written, each carrying whatever arrived.
// Mirrors flow.ClassifyPorts; the node's static outputs (other, error) are
// appended after these by nodePorts.
export function classifyPorts(config: Record<string, unknown> | undefined): PortSpec[] {
  const categories = splitTokens(config?.categories as string | undefined);
  return categories.map((cat) => ({
    id: `cat:${lowerRunes(cat)}`,
    label: cat,
    type: PortAny,
    type_from: 'in',
  }));
}

// toolArgPorts derives a tool node's argument output ports from its config:
// one typed value port per declared argument, param-kind because they supply
// values rather than messages. Mirrors flow.ToolArgPorts.
export function toolArgPorts(config: Record<string, unknown> | undefined): PortSpec[] {
  const declared = config?.tool_args as ToolArg[] | undefined;
  if (!declared?.length) return [];
  const seen = new Set<string>();
  const specs: PortSpec[] = [];
  for (const a of declared) {
    if (!a.id || seen.has(a.id)) continue;
    seen.add(a.id);
    specs.push({ id: toolArgPortId(a.id), label: a.label || a.id, type: a.type || PortString, kind: 'param' });
  }
  return specs;
}
