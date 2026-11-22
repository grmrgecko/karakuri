// Dashboard variables as widgets see them through the host bridge. The host
// substitutes ${name} in a widget's config before the widget runs, so widgets
// only need this for settings panels, which edit the stored templates, and for
// widgets that set or send values themselves.

/** VariableOption is one value a variable may take. */
export interface VariableOption {
  value: string;
  label?: string;
}

/** BridgeVariable is a resolved dashboard variable. */
export interface BridgeVariable {
  name: string;
  label?: string;
  description?: string;
  type?: "custom" | "constant" | "query" | "time_range" | "interval";
  options?: VariableOption[];
  default?: string;
  value?: string;
}

/** BridgeVariables is what the host reports about the open dashboard's variables. */
export interface BridgeVariables {
  dashboardId: string;
  variables: BridgeVariable[];
  values: Record<string, string>;
}

const REF = /\$\{([A-Za-z][A-Za-z0-9_]{0,31})(?::(text|raw|regex|sqlstring|from|to))?\}/g;
const EMPTY: BridgeVariables = { dashboardId: "", variables: [], values: {} };

const bridge = () => (window as any).__karakuri;

/** currentVariables returns the open dashboard's variables, none when the host has no dashboard open. */
export function currentVariables(): BridgeVariables {
  return bridge()?.variables?.() ?? EMPTY;
}

/** watchVariables follows the open dashboard's variables, calling back at once. */
export function watchVariables(handler: (state: BridgeVariables) => void): () => void {
  const watch = bridge()?.watchVariables;
  if (!watch) {
    handler(EMPTY);
    return () => {};
  }
  return watch(handler);
}

/** setVariable changes this screen's value of a variable. */
export function setVariable(name: string, value: string): void {
  bridge()?.setVariable?.(name, value);
}

/** hasReference reports whether a stored value contains a variable reference. */
export function hasReference(value: string | undefined | null): boolean {
  return !!value && /\$\{[A-Za-z][A-Za-z0-9_]{0,31}(?::(text|raw|regex|sqlstring|from|to))?\}/.test(value);
}

/** reference spells a variable reference. */
export function reference(name: string): string {
  return "${" + name + "}";
}

/** optionsOf returns a variable's options, a constant's value included. */
export function optionsOf(variable: BridgeVariable): VariableOption[] {
  if (variable.type === "constant" && !variable.options?.length) return [{ value: variable.value ?? "" }];
  return variable.options ?? [];
}

/**
 * resolve substitutes references with the values the screen holds, or each
 * variable's default, so a settings panel can show and look up what a template
 * means right now. An unknown name stays as written.
 */
export function resolve(value: string, state: BridgeVariables = currentVariables()): string {
  if (!hasReference(value)) return value;
  return value.replace(REF, (match, name: string, format: string | undefined) => {
    const variable = state.variables.find((v) => v.name === name);
    if (!variable) return match;
    const options = optionsOf(variable);
    const current = state.values[name];
    // A time range takes any value that parses, so the viewer's value
    // stands even when no preset names it.
    const option =
      options.find((o) => o.value === current) ??
      (variable.type === "time_range" && current ? { value: current } : undefined) ??
      options.find((o) => o.value === variable.default) ??
      options[0];
    if (!option) return match;
    if (format === "text") return option.label || option.value;
    if (format === "from" || format === "to") {
      const comma = option.value.indexOf(",");
      if (comma < 0) return match;
      return format === "from" ? option.value.slice(0, comma) : option.value.slice(comma + 1);
    }
    return option.value;
  });
}
