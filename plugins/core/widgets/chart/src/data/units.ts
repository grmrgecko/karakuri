// Common units. A unit formats a number into a prefix, text, and suffix,
// scaling SI units by 1000 and IEC units by 1024. Unknown units are drawn as
// a suffix.

export interface FormattedValue {
  prefix?: string;
  text: string;
  suffix?: string;
}

/** UnitOption is one entry of the unit picker. */
export interface UnitOption {
  id: string;
  name: string;
  group: string;
}

type Formatter = (value: number, decimals?: number | null) => FormattedValue;

const SI_PREFIXES = ["f", "p", "n", "µ", "m", "", "k", "M", "G", "T", "P", "E", "Z", "Y"];
const SI_BASE = 5;
const IEC_PREFIXES = ["", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "Zi", "Yi"];

/** autoDecimals picks decimals from a value's magnitude. */
export function autoDecimals(value: number): number {
  if (!Number.isFinite(value) || value === 0 || value % 1 === 0) return 0;
  const abs = Math.abs(value);
  const log10 = Math.floor(Math.log(abs) / Math.LN10);
  let dec = -log10 + 1;
  const magn = Math.pow(10, -dec);
  if (abs / magn > 2.25) dec++;
  return Math.max(0, Math.min(dec, 10));
}

/** toFixed writes a number with fixed or automatic decimals. */
export function toFixed(value: number, decimals?: number | null): string {
  if (!Number.isFinite(value)) return String(value);
  const d = decimals === null || decimals === undefined ? autoDecimals(value) : Math.max(0, Math.min(20, decimals));
  const text = value.toFixed(d);
  return text === "-0" || /^-0\.0*$/.test(text) ? text.slice(1) : text;
}

function fixed(suffix: string, prefix = ""): Formatter {
  return (v, d) => ({ prefix: prefix || undefined, text: toFixed(v, d), suffix: suffix || undefined });
}

// scaled divides a value by powers of factor and picks the matching suffix.
// offset is the suffix index of an unscaled value.
function scaled(factor: number, suffixes: string[], offset = 0, prefix = ""): Formatter {
  return (v, d) => {
    let index = 0;
    if (v !== 0 && Number.isFinite(v)) index = Math.floor(Math.log(Math.abs(v)) / Math.log(factor));
    index = Math.max(-offset, Math.min(suffixes.length - offset - 1, index));
    const n = v / Math.pow(factor, index);
    return { prefix: prefix || undefined, text: toFixed(n, d), suffix: suffixes[index + offset] || undefined };
  };
}

// si scales by 1000 through the SI prefixes. offset is the SI power of the
// input, so kwatt has offset 1 and reads kilowatts.
function si(unit: string, offset = 0): Formatter {
  const suffixes = SI_PREFIXES.map((p) => " " + p + unit);
  return scaled(1000, suffixes, SI_BASE + offset);
}

function iec(unit: string): Formatter {
  return scaled(1024, IEC_PREFIXES.map((p) => " " + p + unit));
}

const SHORT = ["", " K", " Mil", " Bil", " Tri", " Quadr", " Quint", " Sext", " Sept"];
const CURRENCY = ["", "K", "M", "B", "T"];

// TIME_UNITS are seconds per unit, largest last.
const TIME_UNITS: [number, string][] = [
  [0.001, "ms"],
  [1, "s"],
  [60, "min"],
  [3600, "hour"],
  [86400, "day"],
  [604800, "week"],
  [31536000, "year"],
];

// time formats a duration given in a unit of `seconds` seconds in the largest
// unit it fills.
function time(seconds: number): Formatter {
  return (v, d) => {
    const s = v * seconds;
    const abs = Math.abs(s);
    let unit = TIME_UNITS[0];
    for (const u of TIME_UNITS) {
      if (abs >= u[0]) unit = u;
    }
    if (abs === 0) unit = TIME_UNITS.find((u) => u[0] === seconds) ?? TIME_UNITS[1];
    // These units do not split into weeks.
    if (unit[1] === "week") unit = TIME_UNITS[4];
    return { text: toFixed(s / unit[0], d), suffix: " " + unit[1] };
  };
}

const DURATION_PARTS: [number, string][] = [
  [31536000000, "year"],
  [2592000000, "month"],
  [604800000, "week"],
  [86400000, "day"],
  [3600000, "hour"],
  [60000, "minute"],
  [1000, "second"],
  [1, "millisecond"],
];

/** duration writes milliseconds as words, keeping `parts` units (default 2). */
export function duration(ms: number, parts?: number | null): string {
  if (!Number.isFinite(ms)) return String(ms);
  const negative = ms < 0;
  let rest = Math.abs(Math.round(ms));
  if (rest === 0) return "0 milliseconds";
  const keep = parts && parts > 0 ? parts : 2;
  const out: string[] = [];
  // Units count from the largest one present, so 1 hour and 5 seconds kept
  // to two units reads 1 hour: the empty minutes take the second place.
  let used = 0;
  for (const [size, name] of DURATION_PARTS) {
    const n = Math.floor(rest / size);
    rest -= n * size;
    if (n === 0 && used === 0) continue;
    used++;
    if (n > 0) out.push(`${n} ${name}${n === 1 ? "" : "s"}`);
    if (used >= keep) break;
  }
  return (negative ? "-" : "") + out.join(", ");
}

function pad(n: number, width = 2): string {
  return String(n).padStart(width, "0");
}

/** isoDateTime writes a time as YYYY-MM-DD HH:mm:ss in local time. */
export function isoDateTime(ms: number): string {
  const d = new Date(ms);
  if (Number.isNaN(d.getTime())) return String(ms);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

/** fromNow writes a time as an age or a time to come. */
export function fromNow(ms: number, now = Date.now()): string {
  const diff = now - ms;
  const text = duration(Math.abs(diff), 1);
  return diff >= 0 ? `${text} ago` : `in ${text}`;
}

const UNITS: Record<string, { name: string; group: string; format: Formatter }> = {
  none: { name: "None", group: "Misc", format: fixed("") },
  short: { name: "Short", group: "Misc", format: scaled(1000, SHORT) },
  percent: { name: "Percent (0-100)", group: "Misc", format: fixed("%") },
  percentunit: { name: "Percent (0.0-1.0)", group: "Misc", format: (v, d) => ({ text: toFixed(v * 100, d), suffix: "%" }) },
  humidity: { name: "Humidity (%H)", group: "Temperature and weather", format: fixed("%H") },
  celsius: { name: "Celsius (°C)", group: "Temperature and weather", format: fixed("°C") },
  fahrenheit: { name: "Fahrenheit (°F)", group: "Temperature and weather", format: fixed("°F") },
  kelvin: { name: "Kelvin (K)", group: "Temperature and weather", format: fixed(" K") },
  pressurehpa: { name: "Hectopascals (hPa)", group: "Temperature and weather", format: fixed(" hPa") },
  lux: { name: "Lux (lx)", group: "Temperature and weather", format: fixed(" lx") },
  ppm: { name: "Parts per million (ppm)", group: "Concentration", format: fixed(" ppm") },
  conmgm3: { name: "Milligrams per cubic metre (mg/m³)", group: "Concentration", format: fixed(" mg/m³") },
  watt: { name: "Watt (W)", group: "Energy", format: si("W") },
  kwatt: { name: "Kilowatt (kW)", group: "Energy", format: si("W", 1) },
  watth: { name: "Watt-hour (Wh)", group: "Energy", format: si("Wh") },
  kwatth: { name: "Kilowatt-hour (kWh)", group: "Energy", format: si("Wh", 1) },
  volt: { name: "Volt (V)", group: "Energy", format: si("V") },
  amp: { name: "Ampere (A)", group: "Energy", format: si("A") },
  hertz: { name: "Hertz (Hz)", group: "Energy", format: si("Hz") },
  bytes: { name: "Bytes (IEC)", group: "Data", format: iec("B") },
  decbytes: { name: "Bytes (SI)", group: "Data", format: si("B") },
  bits: { name: "Bits (IEC)", group: "Data", format: iec("b") },
  bps: { name: "Bits per second", group: "Data rate", format: si("b/s") },
  Bps: { name: "Bytes per second", group: "Data rate", format: si("B/s") },
  ms: { name: "Milliseconds (ms)", group: "Time", format: time(0.001) },
  s: { name: "Seconds (s)", group: "Time", format: time(1) },
  m: { name: "Minutes (m)", group: "Time", format: time(60) },
  h: { name: "Hours (h)", group: "Time", format: time(3600) },
  d: { name: "Days (d)", group: "Time", format: time(86400) },
  dtdurationms: { name: "Duration (ms)", group: "Time", format: (v, d) => ({ text: duration(v, d) }) },
  dtdurations: { name: "Duration (s)", group: "Time", format: (v, d) => ({ text: duration(v * 1000, d) }) },
  dateTimeAsIso: { name: "Datetime ISO", group: "Date and time", format: (v) => ({ text: isoDateTime(v) }) },
  dateTimeFromNow: { name: "From now", group: "Date and time", format: (v) => ({ text: fromNow(v) }) },
  currencyUSD: { name: "Dollars ($)", group: "Currency", format: scaled(1000, CURRENCY, 0, "$") },
  currencyEUR: { name: "Euro (€)", group: "Currency", format: scaled(1000, CURRENCY, 0, "€") },
  currencyGBP: { name: "Pounds (£)", group: "Currency", format: scaled(1000, CURRENCY, 0, "£") },
  lengthm: { name: "Metre (m)", group: "Length and speed", format: si("m") },
  lengthkm: { name: "Kilometre (km)", group: "Length and speed", format: fixed(" km") },
  velocityms: { name: "Metres per second (m/s)", group: "Length and speed", format: fixed(" m/s") },
  velocitykmh: { name: "Kilometres per hour (km/h)", group: "Length and speed", format: fixed(" km/h") },
  litre: { name: "Litre (L)", group: "Volume and flow", format: si("L") },
  m3: { name: "Cubic metre (m³)", group: "Volume and flow", format: fixed(" m³") },
  flowlpm: { name: "Litres per minute (L/min)", group: "Volume and flow", format: fixed(" L/min") },
};

/** UNIT_OPTIONS lists the units in picker order. */
export const UNIT_OPTIONS: UnitOption[] = Object.entries(UNITS).map(([id, u]) => ({ id, name: u.name, group: u.group }));

/** formatValue formats a number in a unit. A null or missing value has empty text. */
export function formatValue(value: number | null | undefined, unit?: string, decimals?: number | null): FormattedValue {
  if (value === null || value === undefined) return { text: "" };
  if (Number.isNaN(value)) return { text: "NaN" };
  const id = unit || "none";
  if (id.startsWith("suffix:")) return fixed(id.slice(7))(value, decimals);
  if (id.startsWith("prefix:")) return { prefix: id.slice(7), text: toFixed(value, decimals) };
  const known = UNITS[id];
  if (known) return known.format(value, decimals);
  return fixed(" " + id)(value, decimals);
}

/** formattedText joins a formatted value's parts. */
export function formattedText(v: FormattedValue): string {
  return `${v.prefix ?? ""}${v.text}${v.suffix ?? ""}`;
}

// SOURCE_UNITS maps the unit strings sources report to unit IDs.
const SOURCE_UNITS: Record<string, string> = {
  "°C": "celsius",
  "°F": "fahrenheit",
  K: "kelvin",
  "%": "percent",
  W: "watt",
  kW: "kwatt",
  Wh: "watth",
  kWh: "kwatth",
  V: "volt",
  A: "amp",
  Hz: "hertz",
  lx: "lux",
  hPa: "pressurehpa",
  ppm: "ppm",
  "µg/m³": "suffix: µg/m³",
  "mg/m³": "conmgm3",
  B: "bytes",
  "B/s": "Bps",
  "bit/s": "bps",
  ms: "ms",
  s: "s",
  min: "m",
  h: "h",
  d: "d",
  m: "lengthm",
  km: "lengthkm",
  "m/s": "velocityms",
  "km/h": "velocitykmh",
  L: "litre",
  "m³": "m3",
  "L/min": "flowlpm",
};

/**
 * unitFromSource turns a unit a database reports, such as °C, into a unit ID.
 * The source table is read first because a reported unit can spell a different
 * unit's ID: a sensor's "m" is metres, and the ID "m" is minutes.
 */
export function unitFromSource(unit: string | undefined): string | undefined {
  if (!unit) return undefined;
  const mapped = SOURCE_UNITS[unit];
  if (mapped) return mapped;
  if (UNITS[unit]) return unit;
  return `suffix: ${unit}`;
}
