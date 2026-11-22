// Value formats turn a number a source reports into the reading a person
// expects: seconds as a clock, bytes in binary steps. A binding names one, and
// every place its value is drawn as text applies it before the kind's own
// labels and unit.

export type ValueFormat = "duration" | "bytes" | "bytes_per_second" | "percent" | "number";

/** VALUE_FORMATS lists the formats a binding may name, with their panel labels. */
export const VALUE_FORMATS: { value: ValueFormat; label: string }[] = [
  { value: "number", label: "Number" },
  { value: "percent", label: "Percent" },
  { value: "duration", label: "Duration (seconds)" },
  { value: "bytes", label: "Bytes" },
  { value: "bytes_per_second", label: "Bytes per second" },
];

const BYTE_UNITS = ["B", "KB", "MB", "GB", "TB", "PB"];

/** numeric reads a number from a reading, or null for anything else. */
function numeric(value: unknown): number | null {
  if (typeof value === "number") return Number.isFinite(value) ? value : null;
  if (typeof value !== "string" || value.trim() === "") return null;
  const n = Number(value);
  return Number.isFinite(n) ? n : null;
}

/** trimmed writes n with at most digits decimals, dropping trailing zeros. */
function trimmed(n: number, digits: number): string {
  return String(Number(n.toFixed(digits)));
}

/** duration writes seconds as m:ss, h:mm:ss, or Nd h:mm:ss. */
function duration(seconds: number): string {
  const sign = seconds < 0 ? "-" : "";
  let s = Math.floor(Math.abs(seconds));
  const days = Math.floor(s / 86400);
  s -= days * 86400;
  const hours = Math.floor(s / 3600);
  s -= hours * 3600;
  const minutes = Math.floor(s / 60);
  const secs = s - minutes * 60;
  const two = (n: number) => String(n).padStart(2, "0");
  if (days > 0) return `${sign}${days}d ${hours}:${two(minutes)}:${two(secs)}`;
  if (hours > 0) return `${sign}${hours}:${two(minutes)}:${two(secs)}`;
  return `${sign}${minutes}:${two(secs)}`;
}

/** bytes writes a size in 1024 steps with at most one decimal place. */
function bytes(n: number): string {
  let value = Math.abs(n);
  let unit = 0;
  while (value >= 1024 && unit < BYTE_UNITS.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${n < 0 ? "-" : ""}${trimmed(value, unit === 0 ? 0 : 1)} ${BYTE_UNITS[unit]}`;
}

/**
 * applyFormat returns a reading in a format, or null when the format does not
 * apply: no format, an unknown one, or a value that is not a number. The
 * caller then draws the value as it would have without one.
 */
export function applyFormat(
  value: unknown,
  format: unknown,
  precision: number | undefined,
  unit: string,
): string | null {
  if (typeof format !== "string" || format === "") return null;
  const n = numeric(value);
  if (n === null) return null;
  const digits = typeof precision === "number" && Number.isFinite(precision) && precision >= 0 ? precision : undefined;
  switch (format) {
    case "duration":
      return duration(n);
    case "bytes":
      return bytes(n);
    case "bytes_per_second":
      return `${bytes(n)}/s`;
    case "percent":
      return `${digits === undefined ? trimmed(n, 2) : n.toFixed(digits)} %`;
    case "number": {
      const text = digits === undefined ? String(n) : n.toFixed(digits);
      return unit ? `${text} ${unit}` : text;
    }
  }
  return null;
}
