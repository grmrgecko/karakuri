// Scales, ticks, and SVG paths for the graph. Everything is in pixels of the
// plot area, measured by the widget, so the drawing stays sharp at any cell
// size instead of being a fixed viewBox stretched to fit.

export interface Point {
  t: number;
  v: number;
}

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
const TIME_STEPS = [5 * MINUTE, 10 * MINUTE, 15 * MINUTE, 30 * MINUTE, HOUR, 2 * HOUR, 3 * HOUR, 6 * HOUR, 12 * HOUR, DAY, 2 * DAY];

/** niceTicks returns evenly spaced round values covering lo..hi. */
export function niceTicks(lo: number, hi: number, count: number): number[] {
  const span = hi - lo;
  if (!(span > 0) || count < 1) return [lo];
  const raw = span / count;
  const magnitude = Math.pow(10, Math.floor(Math.log10(raw)));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * magnitude).find((s) => s >= raw) ?? 10 * magnitude;
  const ticks: number[] = [];
  for (let v = Math.ceil(lo / step) * step; v <= hi + step * 1e-9; v += step) {
    ticks.push(Number(v.toFixed(10)));
  }
  return ticks;
}

/**
 * valueDomain fits the value axis to the data unless the author fixed an end.
 * A flat line gets a band around it, because a zero-height axis cannot place
 * anything.
 */
export function valueDomain(values: number[], min: number | null, max: number | null): [number, number] {
  let lo = min ?? Infinity;
  let hi = max ?? -Infinity;
  if (min === null || max === null) {
    for (const v of values) {
      if (min === null && v < lo) lo = v;
      if (max === null && v > hi) hi = v;
    }
  }
  if (!Number.isFinite(lo) && !Number.isFinite(hi)) return [0, 1];
  if (!Number.isFinite(lo)) lo = hi - 1;
  if (!Number.isFinite(hi)) hi = lo + 1;
  if (hi === lo) {
    const band = Math.abs(lo) * 0.1 || 1;
    if (min === null) lo -= band;
    if (max === null) hi += band;
  } else {
    const pad = (hi - lo) * 0.05;
    if (min === null) lo -= pad;
    if (max === null) hi += pad;
  }
  if (hi <= lo) hi = lo + 1;
  return [lo, hi];
}

/**
 * timeTicks places time labels on local clock boundaries, as few as fit the
 * width: hours on the hour, days at midnight.
 */
export function timeTicks(start: number, end: number, maxTicks: number): { ticks: number[]; step: number } {
  const span = end - start;
  const step = TIME_STEPS.find((s) => span / s <= Math.max(1, maxTicks)) ?? TIME_STEPS[TIME_STEPS.length - 1];
  // getTimezoneOffset is UTC minus local, so adding it back after rounding
  // aligns the first tick on local wall time rather than on UTC.
  const offset = new Date(start).getTimezoneOffset() * MINUTE;
  const ticks: number[] = [];
  for (let t = Math.ceil((start - offset) / step) * step + offset; t <= end; t += step) ticks.push(t);
  return { ticks, step };
}

/** timeLabel names a tick: the weekday at midnight on a multi-day axis, otherwise the time. */
export function timeLabel(t: number, step: number, span: number): string {
  const date = new Date(t);
  const midnight = date.getHours() === 0 && date.getMinutes() === 0;
  if (step >= DAY || (span > DAY && midnight)) {
    return date.toLocaleDateString(undefined, { weekday: "short" });
  }
  return date.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
}

/**
 * decimate keeps each pixel column's lowest and highest reading, in time
 * order, once there are more readings than columns. A week of a busy sensor
 * is tens of thousands of points, and the line it draws is the same.
 */
export function decimate(points: Point[], x: (t: number) => number): Point[] {
  if (points.length < 2) return points;
  const width = Math.abs(x(points[points.length - 1].t) - x(points[0].t));
  if (points.length <= width * 2) return points;
  const out: Point[] = [];
  let column = NaN;
  let lo: Point | null = null;
  let hi: Point | null = null;
  const flush = () => {
    if (!lo || !hi) return;
    if (lo === hi) out.push(lo);
    else if (lo.t < hi.t) out.push(lo, hi);
    else out.push(hi, lo);
  };
  for (const p of points) {
    const c = Math.floor(x(p.t));
    if (c !== column) {
      flush();
      column = c;
      lo = hi = p;
      continue;
    }
    if (p.v < lo!.v) lo = p;
    if (p.v > hi!.v) hi = p;
  }
  flush();
  // The last reading is the one held to now, so it is kept exactly.
  const last = points[points.length - 1];
  if (out[out.length - 1] !== last) out.push(last);
  return out;
}

/**
 * linePath draws the readings and holds the last one to endX: a state stays
 * what it was until it changes, which is what the right edge shows.
 */
export function linePath(
  points: Point[],
  x: (t: number) => number,
  y: (v: number) => number,
  step: boolean,
  endX: number,
): string {
  if (points.length === 0) return "";
  const parts: string[] = [];
  let prevY = 0;
  points.forEach((p, i) => {
    const px = x(p.t).toFixed(1);
    const py = y(p.v).toFixed(1);
    if (i === 0) parts.push(`M${px} ${py}`);
    else if (step) parts.push(`H${px}V${py}`);
    else parts.push(`L${px} ${py}`);
    prevY = Number(py);
  });
  const lastX = x(points[points.length - 1].t);
  if (endX > lastX) parts.push(step ? `H${endX.toFixed(1)}` : `L${endX.toFixed(1)} ${prevY}`);
  return parts.join("");
}

/** areaPath closes a line path down to the axis floor. */
export function areaPath(line: string, firstX: number, endX: number, floorY: number): string {
  if (!line) return "";
  return `${line}L${endX.toFixed(1)} ${floorY.toFixed(1)}L${firstX.toFixed(1)} ${floorY.toFixed(1)}Z`;
}
