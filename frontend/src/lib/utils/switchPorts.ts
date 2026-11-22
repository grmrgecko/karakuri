import type { PortSpec } from '../stores/nodeCatalog';

// SwitchRule is one routing rule of a switch node. Each rule owns one output
// port, keyed by its stable ID so reordering or deleting rules never
// re-points the edges of the surviving ones.
export interface SwitchRule {
  id?: string;
  operator: string;
  value?: string;
  label?: string;
}

// Operators that compare against a configured value, so the editor knows when
// to show a value input. Mirrors flow.SwitchOperatorNeedsValue.
export function operatorNeedsValue(operator: string): boolean {
  return operator !== 'exists' && operator !== 'otherwise';
}

// newRuleId returns an ID for a freshly added rule. Random rather than
// positional so it stays attached to its edges as rules move around.
export function newRuleId(): string {
  return 'r' + Math.random().toString(36).slice(2, 8);
}

// rulePortId mirrors flow.SwitchRulePortID: a rule's port is its ID, falling
// back to its position for rules written without one.
export function rulePortId(rule: SwitchRule, index: number): string {
  return rule.id || `r${index + 1}`;
}

// ruleLabel mirrors flow.SwitchRuleLabel: the short port label a rule shows
// on the canvas.
export function ruleLabel(rule: SwitchRule): string {
  if (rule.label) return rule.label;
  const value = (rule.value ?? '').trim();
  switch (rule.operator) {
    case 'otherwise':
      return 'otherwise';
    case 'exists':
      return 'exists';
    case 'expression':
      return value === '' ? 'expression' : truncate(value);
    case 'contains':
      return `contains ${truncate(value)}`;
    case 'regex':
      return `~ ${truncate(value)}`;
  }
  const symbols: Record<string, string> = {
    eq: '=',
    neq: '≠',
    lt: '<',
    lte: '≤',
    gt: '>',
    gte: '≥',
  };
  const symbol = symbols[rule.operator];
  return symbol ? `${symbol} ${truncate(value)}`.trim() : rule.operator;
}

function truncate(s: string): string {
  const chars = [...s];
  return chars.length <= 16 ? s : chars.slice(0, 15).join('') + '…';
}

// switchPorts mirrors the backend's flow.SwitchPorts: it derives a switch
// node's outputs from its configured rules. A config saved before per-rule
// ports existed keeps the legacy true/false pair its edges are wired to.
export function switchPorts(config: Record<string, unknown> | undefined): PortSpec[] {
  const cfg = config ?? {};
  const rules = Array.isArray(cfg.rules) ? (cfg.rules as SwitchRule[]) : [];
  // Routing does not change the message, so every branch carries whatever
  // arrived rather than declaring anything of its own.
  if (rules.length === 0) {
    if (cfg.condition || cfg.operator || cfg.mode || cfg.value) {
      return [
        { id: 'true', label: 'True', type: 'any', type_from: 'in' },
        { id: 'false', label: 'False', type: 'any', type_from: 'in' },
      ];
    }
    return [];
  }
  return rules.map((rule, i) => ({
    id: rulePortId(rule, i),
    label: ruleLabel(rule),
    type: 'any',
    type_from: 'in',
  }));
}
