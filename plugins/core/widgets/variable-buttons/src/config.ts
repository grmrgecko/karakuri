import { parseAttr } from "../../shared/dashboardLive";

/** ButtonsConfig is what a Variable buttons placement stores. */
export interface ButtonsConfig {
  /** source marks the config as a variable binding the host reads for scope. */
  source: "variable";
  variable: string;
  /** scope is screen to switch only the viewer, or dashboard for every screen. */
  scope: "screen" | "dashboard";
  /** displayOnly mirrors scope: a screen-only widget grants nothing. */
  displayOnly: boolean;
  layout: "row" | "column" | "grid";
  size: "small" | "medium" | "large" | "fit";
  showLabel: boolean;
}

/** parseConfig reads the host's config attribute with defaults filled in. */
export function parseConfig(attr: string | Record<string, unknown>): ButtonsConfig {
  const f = parseAttr(attr);
  const scope = f.scope === "dashboard" ? "dashboard" : "screen";
  return {
    source: "variable",
    variable: typeof f.variable === "string" ? f.variable : "",
    scope,
    displayOnly: scope !== "dashboard",
    layout: f.layout === "column" || f.layout === "grid" ? f.layout : "row",
    size: f.size === "small" || f.size === "large" || f.size === "fit" ? f.size : "medium",
    showLabel: f.showLabel === true,
  };
}
