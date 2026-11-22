// Field calculations. Each reduces a column to one value; the
// numeric ones skip nulls, NaN, and non-numbers.

export interface ReducerInfo {
  id: string;
  name: string;
  description: string;
}

export const REDUCERS: ReducerInfo[] = [
  { id: "lastNotNull", name: "Last *", description: "Last non-null value" },
  { id: "last", name: "Last", description: "Last value" },
  { id: "firstNotNull", name: "First *", description: "First non-null value" },
  { id: "first", name: "First", description: "First value" },
  { id: "min", name: "Min", description: "Minimum value" },
  { id: "max", name: "Max", description: "Maximum value" },
  { id: "mean", name: "Mean", description: "Average value" },
  { id: "median", name: "Median", description: "Middle value" },
  { id: "sum", name: "Total", description: "Sum of all values" },
  { id: "count", name: "Count", description: "Number of values" },
  { id: "delta", name: "Delta", description: "Cumulative change, counting a drop as a counter reset" },
  { id: "range", name: "Range", description: "Max minus min" },
  { id: "diff", name: "Difference", description: "Last minus first" },
  { id: "diffperc", name: "Difference percent", description: "Difference as a percentage of the first value" },
  { id: "stdDev", name: "Standard deviation", description: "Population standard deviation" },
  { id: "variance", name: "Variance", description: "Population variance" },
  { id: "changeCount", name: "Change count", description: "Times the value changed" },
  { id: "distinctCount", name: "Distinct count", description: "Number of distinct values" },
  { id: "allIsNull", name: "All null", description: "Whether every value is null" },
  { id: "allIsZero", name: "All zero", description: "Whether every value is zero" },
  { id: "step", name: "Minimal step", description: "Smallest interval between values" },
];

/** reducerName returns a calculation's display name. */
export function reducerName(id: string): string {
  return REDUCERS.find((r) => r.id === id)?.name ?? id;
}

/** toNumber reads a cell as a finite number, or null. */
export function toNumber(v: unknown): number | null {
  if (typeof v === "number") return Number.isFinite(v) ? v : null;
  if (typeof v === "boolean") return v ? 1 : 0;
  if (typeof v === "string" && v.trim() !== "") {
    const n = Number(v);
    return Number.isFinite(n) ? n : null;
  }
  return null;
}

function isNullish(v: unknown): boolean {
  return v === null || v === undefined || (typeof v === "number" && Number.isNaN(v));
}

/** reduce runs one calculation over a column. Unknown calculations return null. */
export function reduce(values: unknown[], id: string): unknown {
  switch (id) {
    case "last":
      return values.length ? values[values.length - 1] : null;
    case "first":
      return values.length ? values[0] : null;
    case "lastNotNull":
      for (let i = values.length - 1; i >= 0; i--) if (!isNullish(values[i])) return values[i];
      return null;
    case "firstNotNull":
      for (const v of values) if (!isNullish(v)) return v;
      return null;
    case "count":
      return values.length;
    case "distinctCount":
      return new Set(values.filter((v) => !isNullish(v))).size;
    case "changeCount": {
      let n = 0;
      let prev: unknown = undefined;
      let seen = false;
      for (const v of values) {
        if (isNullish(v)) continue;
        if (seen && v !== prev) n++;
        prev = v;
        seen = true;
      }
      return n;
    }
    case "allIsNull":
      return values.every(isNullish);
  }

  const nums: number[] = [];
  for (const v of values) {
    const n = toNumber(v);
    if (n !== null) nums.push(n);
  }
  if (id === "allIsZero") return nums.length > 0 && nums.every((n) => n === 0);
  if (nums.length === 0) return id === "sum" ? 0 : null;

  switch (id) {
    case "min":
      return nums.reduce((a, b) => (b < a ? b : a));
    case "max":
      return nums.reduce((a, b) => (b > a ? b : a));
    case "sum":
      return nums.reduce((a, b) => a + b, 0);
    case "mean":
      return nums.reduce((a, b) => a + b, 0) / nums.length;
    case "median": {
      const s = [...nums].sort((a, b) => a - b);
      const mid = Math.floor(s.length / 2);
      return s.length % 2 ? s[mid] : (s[mid - 1] + s[mid]) / 2;
    }
    case "range":
      return Math.max(...nums) - Math.min(...nums);
    case "diff":
      return nums[nums.length - 1] - nums[0];
    case "diffperc":
      return nums[0] === 0 ? null : ((nums[nums.length - 1] - nums[0]) / nums[0]) * 100;
    case "delta": {
      let total = 0;
      for (let i = 1; i < nums.length; i++) {
        const step = nums[i] - nums[i - 1];
        total += step >= 0 ? step : nums[i];
      }
      return total;
    }
    case "variance":
    case "stdDev": {
      const mean = nums.reduce((a, b) => a + b, 0) / nums.length;
      const variance = nums.reduce((a, b) => a + (b - mean) * (b - mean), 0) / nums.length;
      return id === "variance" ? variance : Math.sqrt(variance);
    }
    case "step": {
      let min: number | null = null;
      for (let i = 1; i < nums.length; i++) {
        const step = nums[i] - nums[i - 1];
        if (min === null || step < min) min = step;
      }
      return min;
    }
  }
  return null;
}

/** reduceAll runs several calculations over a column. */
export function reduceAll(values: unknown[], ids: string[]): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const id of ids) out[id] = reduce(values, id);
  return out;
}
