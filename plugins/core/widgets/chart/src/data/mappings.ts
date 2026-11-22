// Value mappings: a value, a range, a regex, or a special value maps
// to display text and a colour. The first matching mapping wins.

import type { MappingResult, ValueMapping } from "./types";
import { toNumber } from "./reducers";

/** parseRegex reads a pattern written bare or as /pattern/flags. */
export function parseRegex(pattern: string): RegExp | null {
  try {
    const m = /^\/(.*)\/([gimsuy]*)$/.exec(pattern);
    return m ? new RegExp(m[1], m[2]) : new RegExp(pattern);
  } catch {
    return null;
  }
}

function special(value: unknown, match: string): boolean {
  switch (match) {
    case "null":
      return value === null || value === undefined;
    case "nan":
      return typeof value === "number" && Number.isNaN(value);
    case "null+nan":
      return value === null || value === undefined || (typeof value === "number" && Number.isNaN(value));
    case "true":
      return value === true || value === "true";
    case "false":
      return value === false || value === "false";
    case "empty":
      return value === "";
  }
  return false;
}

/** mapValue returns the result of the first mapping that matches, or null. */
export function mapValue(value: unknown, mappings: ValueMapping[] | undefined): MappingResult | null {
  if (!mappings?.length) return null;
  for (const m of mappings) {
    if (!m || typeof m !== "object" || !m.options) continue;
    switch (m.type) {
      case "value": {
        if (value === null || value === undefined) break;
        const key = typeof value === "number" ? String(value) : String(value);
        const hit = m.options[key];
        if (hit) return hit;
        // A number stored as text, such as "1.0", still matches 1.
        const n = toNumber(value);
        if (n !== null) {
          for (const [k, r] of Object.entries(m.options)) {
            if (k.trim() !== "" && Number(k) === n) return r;
          }
        }
        break;
      }
      case "range": {
        const n = toNumber(value);
        if (n === null) break;
        const from = m.options.from;
        const to = m.options.to;
        if ((from === null || from === undefined || n >= from) && (to === null || to === undefined || n <= to)) {
          return m.options.result;
        }
        break;
      }
      case "regex": {
        if (value === null || value === undefined) break;
        const re = parseRegex(m.options.pattern ?? "");
        if (!re) break;
        const text = String(value);
        const match = re.exec(text);
        if (match) {
          const result = m.options.result ?? {};
          // The display text may use the pattern's groups.
          return result.text ? { ...result, text: text.replace(re, result.text) } : result;
        }
        break;
      }
      case "special":
        if (special(value, m.options.match)) return m.options.result;
        break;
    }
  }
  return null;
}
