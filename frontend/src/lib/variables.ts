import { api } from './api/client';
import { DEFAULT_PRESETS, DEFAULT_RANGE, autoInterval, parseDuration, resolveRange, splitRange, validRange } from './timeRange';
import type {
  Condition,
  DashboardVariable,
  DashboardWidget,
  LiveState,
  ResolvedVariables,
  VariableOption,
} from './stores/dashboards';

// Dashboard variables are named values placement configuration refers to as
// ${name} (the option value) or ${name:text} (its label); ${name:from} and
// ${name:to} take a time range's ends, and the query formats raw, regex, and
// sqlstring insert the plain value outside query text. The server expands
// the same references over every option to decide what the layout grants, in
// pkg/dashscope/variables.go; the two must agree on what a reference is and on
// when a whole-string reference becomes a number or a boolean.

const REF = /\$\{([A-Za-z][A-Za-z0-9_]{0,31})(?::(text|raw|regex|sqlstring|from|to))?\}/g;
const WHOLE_REF = /^\$\{([A-Za-z][A-Za-z0-9_]{0,31})\}$/;
const NUMBER = /^-?(0|[1-9][0-9]*)(\.[0-9]+)?$/;

/** VariableValues maps variable names to the option value each currently takes. */
export type VariableValues = Record<string, string>;

/** VAR_PREFIX is the address parameter prefix a variable value travels under. */
export const VAR_PREFIX = 'var-';

/** PIN_PARAM names the address parameter listing variables that ignore commands. */
export const PIN_PARAM = 'pin';

/** optionsOf returns a variable's options, a constant's one value included. */
export function optionsOf(variable: DashboardVariable): VariableOption[] {
  if (variable.type === 'constant' && !variable.options?.length) return [{ value: variable.value ?? '' }];
  if (variable.type === 'time_range' && !variable.options?.length) return DEFAULT_PRESETS;
  if (variable.type === 'interval' && variable.auto && !variable.options?.some((o) => o.value === 'auto')) {
    return [{ value: 'auto', label: 'Auto' }, ...(variable.options ?? [])];
  }
  return variable.options ?? [];
}

/** optionOf returns a variable's option for a value, by value and then by label ignoring case. */
export function optionOf(variable: DashboardVariable, ref: string | null | undefined): VariableOption | undefined {
  if (ref === null || ref === undefined || ref === '') return undefined;
  const options = optionsOf(variable);
  const found =
    options.find((o) => o.value === ref) ??
    options.find((o) => !!o.label && o.label.toLowerCase() === ref.toLowerCase());
  // A time range bounds when data is read, not which data, so any range
  // that parses is a value, as the server accepts it.
  if (!found && variable.type === 'time_range' && validRange(ref)) return { value: ref };
  return found;
}

/**
 * defaultValue returns the value a variable starts on. A default names an
 * option by value only, as DashboardVariable.DefaultOption has it on the
 * server: matching labels here would start viewers on another option than
 * renders and a command returning the variable to its default.
 */
export function defaultValue(variable: DashboardVariable): string {
  if (variable.type === 'time_range') return optionOf(variable, variable.default)?.value ?? DEFAULT_RANGE;
  const options = optionsOf(variable);
  return (options.find((o) => o.value === variable.default) ?? options[0])?.value ?? '';
}

/** optionText returns what a picker shows for an option. */
export function optionText(option: VariableOption): string {
  return option.label || option.value;
}

/** follows reports whether a variable's dashboard-wide value is a select entity's state. */
export function follows(variable: DashboardVariable): boolean {
  return variable.type === 'query' && variable.query?.source === 'ha_select' && !!variable.query.follow;
}

/** displayOf returns where a variable's picker is drawn; a constant has none. */
export function displayOf(variable: DashboardVariable): 'above' | 'label_hidden' | 'menu' | 'hidden' {
  if (variable.type === 'constant') return 'hidden';
  return variable.display || 'above';
}

/** pinnedNames returns the variables an address pins against commands; `*` pins all. */
export function pinnedNames(search: URLSearchParams): Set<string> {
  const names = new Set<string>();
  for (const value of search.getAll(PIN_PARAM)) {
    for (const name of value.split(',')) if (name.trim()) names.add(name.trim());
  }
  return names;
}

/** isPinned reports whether a pin set covers a variable. */
export function isPinned(pins: Set<string>, name: string): boolean {
  return pins.has('*') || pins.has(name);
}

/**
 * sharedValues returns the dashboard-wide value of every variable: a followed
 * entity's mapped state, otherwise what a command set, otherwise the default.
 */
export function sharedValues(
  variables: DashboardVariable[],
  live: LiveState | null,
  followed: Record<string, string>
): VariableValues {
  const out: VariableValues = {};
  for (const v of variables) {
    const shared = follows(v) ? followed[v.name] : live?.variables?.[v.name];
    out[v.name] = optionOf(v, shared)?.value ?? defaultValue(v);
  }
  return out;
}

/**
 * initialValues seeds a viewer's variables: the address first, then the
 * dashboard-wide value, then the default. A value that is not an option is
 * ignored at every step, so a stale link or a value left by an older
 * deployment falls through to the next. A constant takes its value.
 */
export function initialValues(
  variables: DashboardVariable[],
  live: LiveState | null,
  search: URLSearchParams,
  followed: Record<string, string> = {}
): VariableValues {
  const shared = sharedValues(variables, live, followed);
  const out: VariableValues = {};
  for (const v of variables) {
    const fromAddress = v.type === 'constant' ? undefined : optionOf(v, search.get(VAR_PREFIX + v.name))?.value;
    out[v.name] = fromAddress ?? shared[v.name];
  }
  return out;
}

/** substitute replaces variable references in one string; an unknown name stays as written. */
export function substitute(text: string, variables: DashboardVariable[], values: VariableValues): string {
  if (!text.includes('${')) return text;
  return text.replace(REF, (match, name: string, format: string | undefined) => {
    const option = currentOption(variables, values, name);
    if (!option) return match;
    if (format === 'text') return optionText(option);
    if (format === 'from' || format === 'to') {
      const parts = splitRange(option.value);
      return parts ? parts[format === 'from' ? 0 : 1] : match;
    }
    return option.value;
  });
}

/** currentOption returns the option a variable currently takes, falling back to its default. */
function currentOption(variables: DashboardVariable[], values: VariableValues, name: string): VariableOption | undefined {
  const variable = variables.find((v) => v.name === name);
  if (!variable) return undefined;
  const option = optionOf(variable, values[name]) ?? optionOf(variable, defaultValue(variable));
  if (option && variable.type === 'interval' && option.value === 'auto') {
    return { value: effectiveInterval(variable, variables, values), label: option.label };
  }
  return option;
}

/** effectiveInterval resolves an interval variable's auto value from its range variable's current span. */
export function effectiveInterval(variable: DashboardVariable, variables: DashboardVariable[], values: VariableValues): string {
  const rangeVar = variables.find((v) => v.name === variable.range_variable);
  const range = resolveRange(rangeVar ? values[rangeVar.name] ?? defaultValue(rangeVar) : DEFAULT_RANGE);
  const span = range ? range.to.getTime() - range.from.getTime() : 6 * 3600 * 1000;
  return autoInterval(span, variable.steps ?? 30, parseDuration(variable.min_interval ?? '') ?? 0);
}

/**
 * typedValue substitutes a string that is exactly one reference whose value is
 * a number or a boolean as that type, so "hours": "${range}" reaches a widget
 * as a number.
 */
function typedValue(text: string, variables: DashboardVariable[], values: VariableValues): { value: unknown } | null {
  const m = WHOLE_REF.exec(text);
  if (!m) return null;
  const option = currentOption(variables, values, m[1]);
  if (!option) return null;
  if (option.value === 'true') return { value: true };
  if (option.value === 'false') return { value: false };
  if (NUMBER.test(option.value)) return { value: Number(option.value) };
  return null;
}

/** substituteValue rewrites every string inside decoded JSON, leaving keys and other types alone. */
function substituteValue(value: unknown, variables: DashboardVariable[], values: VariableValues): unknown {
  if (typeof value === 'string') {
    const typed = typedValue(value, variables, values);
    return typed ? typed.value : substitute(value, variables, values);
  }
  if (Array.isArray(value)) return value.map((v) => substituteValue(v, variables, values));
  if (value && typeof value === 'object') {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value)) out[k] = substituteValue(v, variables, values);
    return out;
  }
  return value;
}

/** substituteConfig applies variable values to a decoded placement config. */
export function substituteConfig<T>(config: T, variables: DashboardVariable[], values: VariableValues): T {
  if (variables.length === 0) return config;
  return substituteValue(config, variables, values) as T;
}

/** substituteCondition applies variable values to a state condition's source and comparison fields. */
export function substituteCondition(c: Condition, variables: DashboardVariable[], values: VariableValues): Condition {
  if (variables.length === 0 || c.kind !== 'state') return c;
  const s = (v: string | undefined) => (v === undefined ? v : substitute(v, variables, values));
  return {
    ...c,
    controlId: s(c.controlId),
    connectionId: s(c.connectionId),
    entityId: s(c.entityId),
    deviceId: s(c.deviceId),
    attribute: s(c.attribute),
    value: s(c.value),
  };
}

/** writeAddress records a viewer's pick in the address without reloading, so the link opens on it. */
export function writeAddress(name: string, value: string) {
  const url = new URL(window.location.href);
  url.searchParams.set(VAR_PREFIX + name, value);
  history.replaceState(history.state, '', url);
}

/**
 * loadVariables fetches a dashboard's variables with every query resolved by
 * the server, which decides scope from the same list. On failure it falls back
 * to the stored definitions, whose queries then have no options.
 */
export async function loadVariables(
  dashboardId: string,
  draft: boolean,
  fallback: DashboardVariable[],
  values?: VariableValues
): Promise<ResolvedVariables> {
  try {
    const params = new URLSearchParams();
    if (draft) params.set('view', 'draft');
    if (values && Object.keys(values).length > 0) params.set('vars', JSON.stringify(values));
    const query = params.toString();
    const res = await api.get<ResolvedVariables>(`/dashboards/${dashboardId}/variables${query ? '?' + query : ''}`);
    if (res?.variables) return { variables: res.variables, followed: res.followed ?? {}, follow_map: res.follow_map ?? {} };
  } catch {
    // The stored definitions still template custom variables and constants.
  }
  return { variables: fallback, followed: {}, follow_map: {} };
}

/**
 * chainedDependencies returns, for each variable a time-series query refers
 * to, the names of the query variables that must re-resolve when it changes,
 * in definition order. A query that refers to one of those depends on the
 * change as well, since its options follow the value the first falls back to.
 * An interval on auto is a link in the chain: its duration is sized from its
 * range variable, so a query that reads the interval follows the range.
 */
export function chainedDependencies(variables: DashboardVariable[]): Map<string, string[]> {
  const direct = new Map<string, string[]>();
  for (const v of variables) {
    if (v.type === 'interval' && v.auto && v.range_variable) {
      direct.set(v.range_variable, [...(direct.get(v.range_variable) ?? []), v.name]);
    }
    const q = v.query;
    if (v.type !== 'query' || !q || q.source.startsWith('ha_')) continue;
    const names = new Set<string>();
    for (const text of [q.text, q.match, q.label]) {
      for (const m of (text ?? '').matchAll(REF)) names.add(m[1]);
    }
    if (q.range_variable) names.add(q.range_variable);
    for (const n of names) direct.set(n, [...(direct.get(n) ?? []), v.name]);
  }
  const out = new Map<string, string[]>();
  for (const name of direct.keys()) {
    const reached = new Set<string>();
    const pending = [name];
    while (pending.length > 0) {
      for (const dependent of direct.get(pending.pop()!) ?? []) {
        if (reached.has(dependent)) continue;
        reached.add(dependent);
        pending.push(dependent);
      }
    }
    const queries = variables.filter((v) => v.type === 'query' && reached.has(v.name)).map((v) => v.name);
    if (queries.length > 0) out.set(name, queries);
  }
  return out;
}

/** referencesOf returns the variable names a placement's config and conditions use. */
export function referencesOf(widget: DashboardWidget): Set<string> {
  const names = new Set<string>();
  const scan = (text: string | undefined) => {
    if (!text) return;
    for (const m of text.matchAll(REF)) names.add(m[1]);
  };
  scan(widget.config);
  for (const c of widget.display?.conditions ?? []) {
    if (c.kind === 'variable' && c.variable) names.add(c.variable);
    if (c.kind === 'state') [c.controlId, c.connectionId, c.entityId, c.deviceId, c.attribute, c.value].forEach(scan);
  }
  return names;
}
