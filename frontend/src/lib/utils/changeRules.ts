import type { PortSpec } from '../stores/nodeCatalog';
import { PortAny, PortBool, PortNumber, PortObject, PortString } from './payloadPorts';

// ChangeRule is one rewrite applied by a change node. Mirrors flow.ChangeRule
// so the editor offers exactly the operations the backend applies.
export interface ChangeRule {
  op: string;
  target: string;
  from?: string;
  value?: unknown;
  search?: string;
  search_type?: string;
  replace?: string;
  replace_all?: boolean;
}

// Value sources a set writes with, and a change replaces with. Mirrors the
// flow.ChangeFrom* constants.
export const changeValueSources: { value: string; label: string }[] = [
  { value: 'string', label: 'string' },
  { value: 'number', label: 'number' },
  { value: 'boolean', label: 'boolean' },
  { value: 'json', label: 'JSON' },
  { value: 'expression', label: 'expression' },
  { value: 'property', label: 'property' },
];

// Search modes a change rule matches with. Mirrors flow.ChangeSearch*.
export const changeSearchTypes: { value: string; label: string }[] = [
  { value: 'string', label: 'text' },
  { value: 'regex', label: 'regex' },
];

// newChangeRule returns the rule a freshly added row starts as: the most
// common case, setting a payload property to a string.
export function newChangeRule(): ChangeRule {
  return { op: 'set', target: '', from: 'string', value: '' };
}

// changeRuleOp mirrors flow.ChangeRuleOp: an empty op means set, which is how
// rules written before the op field existed are read.
export function changeRuleOp(rule: ChangeRule): string {
  return rule.op?.trim() || 'set';
}

// changeRuleFrom mirrors flow.ChangeRuleFrom: the value source, defaulting to
// a plain string.
export function changeRuleFrom(rule: ChangeRule): string {
  return rule.from?.trim() || 'string';
}

// changeRuleSearchType mirrors flow.ChangeRuleSearchType: the search mode,
// defaulting to a substring match.
export function changeRuleSearchType(rule: ChangeRule): string {
  return rule.search_type?.trim() || 'string';
}

// changeValueString renders a rule's value for a text input. Values typed as
// JSON may be objects, which the JSON editor below renders instead.
export function changeValueString(value: unknown): string {
  if (value === undefined || value === null) return '';
  if (typeof value === 'string') return value;
  return JSON.stringify(value);
}

// changeValuePlaceholder hints at what a value source expects.
export function changeValuePlaceholder(from: string): string {
  switch (from) {
    case 'number':
      return '21.5';
    case 'boolean':
      return 'true';
    case 'json':
      return '{"fade": 2}';
    case 'expression':
      return 'payload.temperature * 2';
    case 'property':
      return 'payload.other';
    default:
      return 'value, or {{ payload.x }}';
  }
}

// changeValueIsCode reports whether a value should be shown in a monospace
// input: expressions, JSON, and property paths read better that way.
export function changeValueIsCode(from: string): boolean {
  return from === 'expression' || from === 'json' || from === 'property';
}

// changeTargetLabel names the target field for the operation being edited, so
// a row reads as the sentence it performs.
export function changeTargetLabel(op: string): string {
  switch (op) {
    case 'delete':
      return 'Property to delete';
    case 'change':
      return 'Property to search';
    case 'move':
    case 'copy':
      return 'To property';
    default:
      return 'Property';
  }
}

// changePorts mirrors flow.ChangePorts: a change node's output type is
// whatever its rules leave in the payload. A node whose last word on the
// payload is "set it to a string" says so on the canvas, rather than making
// the next node guess.
export function changePorts(config: Record<string, unknown> | undefined): PortSpec[] {
  const rules = (config?.rules as ChangeRule[] | undefined) ?? [];
  // The payload arrives undeclared and each rule may or may not have a say, so
  // the type is whatever the last rule to touch the payload left behind.
  let payload = PortAny;
  let touched = false;
  for (const rule of rules) {
    const scope = changeTargetScope(rule?.target);
    if (scope === 'off') continue;
    touched = true;
    // Writing a property into the payload makes it an object whatever the
    // value being written is.
    payload = scope === 'in' ? PortObject : changeRulePortType(rule);
  }
  // A node whose rules never touch the payload passes it through, so an
  // undeclared result carries the input's type rather than losing it. Once a
  // rule has replaced the payload, what arrived says nothing about what leaves.
  if (!touched) return [{ id: 'out', label: 'Out', type: payload, type_from: 'in' }];
  return [{ id: 'out', label: 'Out', type: payload }];
}

// changeRulePortType maps a rule that rewrites the payload root to the type it
// leaves behind. Expressions, property reads, and search-and-replace produce a
// value the config cannot name, so they undeclare the payload.
function changeRulePortType(rule: ChangeRule): string {
  if (changeRuleOp(rule) !== 'set') return PortAny;
  switch (changeRuleFrom(rule)) {
    case 'string':
      return PortString;
    case 'number':
      return PortNumber;
    case 'boolean':
      return PortBool;
    case 'json':
      return PortObject;
    default:
      return PortAny;
  }
}

// changeTargetScope classifies a rule's target path: the payload itself, a
// property inside it, or the topic and metadata, which leave it alone.
function changeTargetScope(target: string | undefined): 'root' | 'in' | 'off' {
  const path = target?.trim() ?? '';
  if (path === '' || path === 'payload') return 'root';
  const head = path.split('.')[0];
  return head === 'topic' || head === 'meta' ? 'off' : 'in';
}

// changeRuleOpPatch returns the fields to write when a rule's operation
// changes. Value means different things per operation — a literal for set, a
// source property path for move and copy — so it is cleared rather than
// carried into an operation that would read it differently.
export function changeRuleOpPatch(rule: ChangeRule, op: string): Partial<ChangeRule> {
  const patch: Partial<ChangeRule> = { op };
  const readsPath = (o: string) => o === 'move' || o === 'copy';
  if (readsPath(op) !== readsPath(changeRuleOp(rule))) patch.value = '';
  return patch;
}
