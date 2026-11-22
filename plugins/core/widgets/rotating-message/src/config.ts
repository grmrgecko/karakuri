import type { EntitySource } from "../../shared/entitySource";
import { parseAttr } from "../../shared/dashboardLive";
import type { Lists } from "./schedule";

// RotatingConfig is a placement's lists and how it rotates through them.
export interface RotatingConfig {
  /** lists maps a bucket, date pattern, cron key, or weather word to its lines. */
  lists: Lists;
  morningStart: number;
  morningEnd: number;
  afternoonStart: number;
  afternoonEnd: number;
  /** specialDayUnique shows only a matching date or cron key's lines. */
  specialDayUnique: boolean;
  /** interval is seconds between lines. */
  interval: number;
  /** fade is the milliseconds one change of line takes. */
  fade: number;
  align: "left" | "center" | "right";
  size: "small" | "medium" | "large" | "fit";
  /**
   * weather is an optional display-only binding whose current value selects
   * a condition key. It is absent rather than null when unset, because the
   * host reads the field as a binding and refuses a null one.
   */
  weather?: EntitySource;
}

export const DEFAULT_LISTS: Lists = {
  anytime: ["Hello there", "Have a good day"],
  morning: ["Good morning", "Rise and shine"],
  afternoon: ["Good afternoon", "Looking good today"],
  evening: ["Good evening", "Time to wind down"],
  "....-01-01": ["Happy new year"],
};

export const DEFAULT_CONFIG: RotatingConfig = {
  lists: DEFAULT_LISTS,
  morningStart: 3,
  morningEnd: 12,
  afternoonStart: 12,
  afternoonEnd: 17,
  specialDayUnique: false,
  interval: 30,
  fade: 1000,
  align: "center",
  size: "fit",
};

const ALIGNS = ["left", "center", "right"];
const SIZES = ["small", "medium", "large", "fit"];

function hour(value: unknown, fallback: number): number {
  const n = Number(value);
  return Number.isInteger(n) && n >= 0 && n <= 24 ? n : fallback;
}

function number(value: unknown, fallback: number, min: number): number {
  const n = Number(value);
  return Number.isFinite(n) && n >= min ? n : fallback;
}

/** cleanLists keeps the keys whose value is a list of strings. */
export function cleanLists(value: unknown): Lists {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  const out: Lists = {};
  for (const [key, lines] of Object.entries(value as Record<string, unknown>)) {
    if (!Array.isArray(lines)) continue;
    out[key] = lines.filter((l) => l !== null && l !== undefined).map((l) => String(l));
  }
  return out;
}

/** parseConfig reads the host's config attribute, falling back field by field. */
export function parseConfig(attr: string | Record<string, unknown>): RotatingConfig {
  const f = parseAttr(attr);
  const cfg: RotatingConfig = {
    lists: f.lists === undefined ? DEFAULT_LISTS : cleanLists(f.lists),
    morningStart: hour(f.morningStart, DEFAULT_CONFIG.morningStart),
    morningEnd: hour(f.morningEnd, DEFAULT_CONFIG.morningEnd),
    afternoonStart: hour(f.afternoonStart, DEFAULT_CONFIG.afternoonStart),
    afternoonEnd: hour(f.afternoonEnd, DEFAULT_CONFIG.afternoonEnd),
    specialDayUnique: f.specialDayUnique === true,
    interval: number(f.interval, DEFAULT_CONFIG.interval, 2),
    fade: number(f.fade, DEFAULT_CONFIG.fade, 0),
    align: ALIGNS.includes(String(f.align)) ? (f.align as RotatingConfig["align"]) : DEFAULT_CONFIG.align,
    size: SIZES.includes(String(f.size)) ? (f.size as RotatingConfig["size"]) : DEFAULT_CONFIG.size,
  };
  if (f.weather && typeof f.weather === "object" && !Array.isArray(f.weather)) {
    cfg.weather = f.weather as EntitySource;
  }
  return cfg;
}

/**
 * pushedLines reads a content command's value as the lines it replaces the
 * lists with: a string is one line, a list of strings is the lines. Anything
 * else, or an empty list, leaves the configured lists in charge.
 */
export function pushedLines(value: unknown): string[] | null {
  if (typeof value === "string") return value.trim() === "" ? null : [value];
  if (typeof value === "number" || typeof value === "boolean") return [String(value)];
  if (Array.isArray(value)) {
    const lines = value
      .filter((v) => typeof v === "string" || typeof v === "number")
      .map((v) => String(v))
      .filter((l) => l.trim() !== "");
    return lines.length > 0 ? lines : null;
  }
  return null;
}
