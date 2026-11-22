import { parseAttr } from "../../shared/dashboardLive";

// FlowTextConfig is how a placement draws what a flow pushes into it.
export interface FlowTextConfig {
  /** title is an optional heading above the content. */
  title: string;
  /** placeholder is shown until a flow pushes content, and after it is cleared. */
  placeholder: string;
  align: "left" | "center" | "right";
  /** size is a fixed text scale, or "fit" to fill the placement. */
  size: "small" | "medium" | "large" | "fit";
  /**
   * columns picks and orders a table's columns when content is an array of
   * objects. Empty shows every key in the order it first appears.
   */
  columns: string[];
  /** columnLabels renames table headers by key. */
  columnLabels: Record<string, string>;
  /** maxRows caps a table's rows; 0 shows them all. */
  maxRows: number;
}

export const DEFAULT_CONFIG: FlowTextConfig = {
  title: "",
  placeholder: "Waiting for content",
  align: "left",
  size: "medium",
  columns: [],
  columnLabels: {},
  maxRows: 0,
};

const ALIGNS = ["left", "center", "right"];
const SIZES = ["small", "medium", "large", "fit"];

/** parseConfig reads the host's config attribute, falling back field by field. */
export function parseConfig(attr: string | Record<string, unknown>): FlowTextConfig {
  const fields = parseAttr(attr);
  return {
    title: typeof fields.title === "string" ? fields.title : DEFAULT_CONFIG.title,
    placeholder: typeof fields.placeholder === "string" ? fields.placeholder : DEFAULT_CONFIG.placeholder,
    align: ALIGNS.includes(String(fields.align)) ? (fields.align as FlowTextConfig["align"]) : DEFAULT_CONFIG.align,
    size: SIZES.includes(String(fields.size)) ? (fields.size as FlowTextConfig["size"]) : DEFAULT_CONFIG.size,
    columns: Array.isArray(fields.columns)
      ? fields.columns.filter((c): c is string => typeof c === "string" && c !== "")
      : [],
    columnLabels: stringMap(fields.columnLabels),
    maxRows: Number.isInteger(fields.maxRows) && (fields.maxRows as number) >= 0 ? (fields.maxRows as number) : 0,
  };
}

function stringMap(v: unknown): Record<string, string> {
  const out: Record<string, string> = {};
  if (!v || typeof v !== "object" || Array.isArray(v)) return out;
  for (const [k, label] of Object.entries(v)) {
    if (typeof label === "string") out[k] = label;
  }
  return out;
}

function isPlainObject(v: unknown): v is Record<string, unknown> {
  if (!v || typeof v !== "object" || Array.isArray(v)) return false;
  const proto = Object.getPrototypeOf(v);
  return proto === Object.prototype || proto === null;
}

// Table is content drawn as rows of cells, already formatted as text.
export interface Table {
  headers: string[];
  rows: string[][];
  /** hidden counts the rows maxRows left out. */
  hidden: number;
}

function cellText(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (typeof v === "string") return v;
  if (typeof v === "number") return String(v);
  if (typeof v === "boolean") return v ? "yes" : "no";
  try {
    return JSON.stringify(v);
  } catch {
    return String(v);
  }
}

/**
 * toTable reads a non-empty array of plain objects as a table, or returns
 * null for any other value so it keeps drawing as before.
 */
export function toTable(value: unknown, cfg: FlowTextConfig): Table | null {
  if (!Array.isArray(value) || value.length === 0 || !value.every(isPlainObject)) return null;
  let keys = cfg.columns;
  if (keys.length === 0) {
    const seen = new Set<string>();
    for (const row of value) {
      for (const k of Object.keys(row)) seen.add(k);
    }
    keys = [...seen];
  }
  const limit = cfg.maxRows > 0 ? Math.min(cfg.maxRows, value.length) : value.length;
  return {
    headers: keys.map((k) => cfg.columnLabels[k] || k),
    rows: value.slice(0, limit).map((row) => keys.map((k) => cellText(row[k]))),
    hidden: value.length - limit,
  };
}

/**
 * sameOriginHTML drops every image that would load from another origin. The
 * server has already escaped raw markup, so Markdown image syntax is the only
 * way a remote request gets in, and a pushed summary must not be able to make
 * the display call out to an address the author never chose. The template's
 * document is inert, so parsing here loads nothing.
 */
export function sameOriginHTML(html: string): string {
  const template = document.createElement("template");
  template.innerHTML = html;
  for (const img of Array.from(template.content.querySelectorAll("img"))) {
    img.removeAttribute("srcset");
    let origin = "";
    try {
      origin = new URL(img.getAttribute("src") ?? "", window.location.href).origin;
    } catch {
      origin = "";
    }
    if (origin !== window.location.origin) img.remove();
  }
  return template.innerHTML;
}
