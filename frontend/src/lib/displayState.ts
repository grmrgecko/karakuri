import type {
  Condition,
  DashboardDisplay,
  DashboardPage,
  DashboardWidget,
  LiveState,
  StatusCue,
  WidgetDisplay,
} from './stores/dashboards';
import type { DashboardVariable } from './stores/dashboards';
import { substituteCondition } from './variables';
import type { VariableValues } from './variables';

// Everything a viewer decides from a dashboard's authored display settings
// and its runtime state lives here as plain functions, so the viewer and the
// editor's preview reach the same answer from the same inputs.

/** START_LOCK is the lock a placement authored hidden on start begins with. */
export const START_LOCK = 'start';

/** WAKE_SECONDS is how long input lifts dimming when no idle timeout is set. */
export const WAKE_SECONDS = 30;

/** emptyLiveState is the state of a dashboard nothing has commanded. */
export function emptyLiveState(): LiveState {
  return { seq: 0, locks: {}, alerts: [], content: {} };
}

/**
 * locksFor returns the locks holding a placement hidden. A placement the
 * server has recorded uses that record, even when it is empty; one it has
 * not falls back to the authored start state.
 */
export function locksFor(widget: DashboardWidget, state: LiveState): string[] {
  const recorded = state.locks?.[widget.id];
  if (recorded) return recorded;
  return widget.display?.hidden ? [START_LOCK] : [];
}

/** orderedPages returns pages in display order. */
export function orderedPages(pages: DashboardPage[] | undefined): DashboardPage[] {
  return [...(pages ?? [])].sort((a, b) => a.sort_order - b.sort_order);
}

/**
 * onPage reports whether a placement belongs on the current page. Without
 * pages every placement shows, so a page reference left behind by a deleted
 * page cannot hide anything.
 */
export function onPage(widget: DashboardWidget, currentPage: string, pages: DashboardPage[]): boolean {
  if (pages.length === 0 || !widget.page_id) return true;
  if (!pages.some((p) => p.id === widget.page_id)) return true;
  return widget.page_id === currentPage;
}

/** stepPage returns the page delta steps from the current one, wrapping. */
export function stepPage(pages: DashboardPage[], currentPage: string, delta: number): string {
  if (pages.length === 0) return '';
  const index = Math.max(0, pages.findIndex((p) => p.id === currentPage));
  return pages[(((index + delta) % pages.length) + pages.length) % pages.length].id;
}

/** minutesOf converts an HH:MM time to minutes after midnight. */
export function minutesOf(hhmm: string | undefined): number {
  const [h, m] = (hhmm ?? '').split(':').map(Number);
  if (!Number.isFinite(h) || !Number.isFinite(m)) return NaN;
  return h * 60 + m;
}

/**
 * windowStart reports whether now falls in a daily window and on which
 * calendar day the window began: 0 for today, -1 for a window that started
 * before midnight. A window whose start equals its end covers the day.
 */
export function windowStart(from: string, to: string, now: Date): number | null {
  const start = minutesOf(from);
  const end = minutesOf(to);
  if (!Number.isFinite(start) || !Number.isFinite(end)) return null;
  const minute = now.getHours() * 60 + now.getMinutes();
  if (start === end) return 0;
  if (start < end) return minute >= start && minute < end ? 0 : null;
  if (minute >= start) return 0;
  if (minute < end) return -1;
  return null;
}

/**
 * timeConditionMet checks a time window. Weekdays name the day the window
 * starts on, so a Friday 22:00 to 06:00 window still holds at 02:00 on
 * Saturday.
 */
export function timeConditionMet(c: Condition, now: Date): boolean {
  const offset = windowStart(c.from ?? '', c.to ?? '', now);
  if (offset === null) return false;
  if (!c.days || c.days.length === 0) return true;
  const day = (now.getDay() + offset + 7) % 7;
  return c.days.includes(day);
}

/** sourceKey names the source a state condition reads. */
export function sourceKey(c: Condition): string {
  switch (c.source) {
    case 'control':
      return `control:${c.controlId ?? ''}`;
    case 'ha':
      return `ha:${c.connectionId ?? ''}:${c.entityId ?? ''}`;
    case 'mqtt':
      return `mqtt:${c.deviceId ?? ''}`;
  }
  return '';
}

/** PrimaryAttributes maps a composite control kind to its primary attribute. */
export type PrimaryAttributes = Record<string, string>;

/**
 * readValue pulls the compared value out of a source record: a control's
 * value or one of its attributes, a Home Assistant entity's state or one of
 * its attributes, or an MQTT device's single value or one of its fields.
 */
export function readValue(c: Condition, record: any, primary: PrimaryAttributes = {}): unknown {
  if (record === undefined || record === null) return undefined;
  const attribute = (c.attribute ?? '').trim();
  switch (c.source) {
    case 'control': {
      const value = record.value;
      if (!attribute) {
        // A composite value compared as JSON never equals a typed value, so
        // with no attribute named the kind's primary one is compared: a
        // location's state, a thermostat's mode.
        const key = primary[String(record.kind ?? '')];
        if (key && value && typeof value === 'object') return (value as Record<string, unknown>)[key];
        return value;
      }
      return value && typeof value === 'object' ? (value as Record<string, unknown>)[attribute] : undefined;
    }
    case 'ha':
      return attribute ? record.attributes?.[attribute] : record.state;
    case 'mqtt': {
      const state = record.state && typeof record.state === 'object' ? record.state : {};
      if (attribute) return state[attribute];
      const keys = Object.keys(state);
      return keys.length === 1 ? state[keys[0]] : undefined;
    }
  }
  return undefined;
}

/**
 * compare applies a condition operator. A value that has not arrived never
 * matches, so a placement waiting on its source stays hidden rather than
 * flashing on and off as the page loads.
 */
export function compare(actual: unknown, operator: Condition['operator'], expected: string | undefined): boolean {
  if (actual === undefined || actual === null) return false;
  const text = typeof actual === 'object' ? JSON.stringify(actual) : String(actual);
  const want = (expected ?? '').trim();
  switch (operator) {
    case 'equals':
      return text.trim().toLowerCase() === want.toLowerCase();
    case 'not_equals':
      return text.trim().toLowerCase() !== want.toLowerCase();
    case 'contains':
      return text.toLowerCase().includes(want.toLowerCase());
    case 'above':
    case 'below': {
      // Number() reads blank text and an empty list as 0, and neither is a
      // number an author compared against.
      if (typeof actual === 'object' || (typeof actual === 'string' && actual.trim() === '')) return false;
      const a = Number(actual);
      const b = Number(want);
      if (!Number.isFinite(a) || !Number.isFinite(b) || want === '') return false;
      return operator === 'above' ? a > b : a < b;
    }
  }
  return false;
}

/** VariableInputs are a viewer's variables and the values it currently holds. */
export interface VariableInputs {
  variables: DashboardVariable[];
  values: VariableValues;
}

/**
 * conditionsMet evaluates a placement's conditions against the sources read
 * so far. State conditions are compared after variable substitution, so the
 * source they read is the one the current variable value names.
 */
export function conditionsMet(
  display: WidgetDisplay | null | undefined,
  records: Record<string, unknown>,
  now: Date,
  variables: VariableInputs = { variables: [], values: {} },
  primary: PrimaryAttributes = {}
): boolean {
  return conditionListMet(display?.conditions, display?.match, records, now, variables, primary);
}

/**
 * conditionListMet evaluates one list of conditions combined by match. An
 * empty list passes, which is what a placement or cue with no conditions
 * means.
 */
export function conditionListMet(
  conditions: Condition[] | undefined,
  match: 'all' | 'any' | undefined,
  records: Record<string, unknown>,
  now: Date,
  variables: VariableInputs = { variables: [], values: {} },
  primary: PrimaryAttributes = {}
): boolean {
  const list = conditions ?? [];
  if (list.length === 0) return true;
  const results = list.map((raw) => {
    if (raw.kind === 'time') return timeConditionMet(raw, now);
    if (raw.kind === 'variable') return compare(variables.values[raw.variable ?? ''], raw.operator, raw.value);
    const c = substituteCondition(raw, variables.variables, variables.values);
    return compare(readValue(c, records[sourceKey(c)], primary), c.operator, c.value);
  });
  return match === 'any' ? results.some(Boolean) : results.every(Boolean);
}

/**
 * activeCue returns the first of a placement's status cues whose conditions
 * pass, or null. A cue waiting on a source that has not answered does not
 * pass, so a border does not flash while the page loads.
 */
export function activeCue(
  display: WidgetDisplay | null | undefined,
  records: Record<string, unknown>,
  now: Date,
  variables: VariableInputs = { variables: [], values: {} },
  primary: PrimaryAttributes = {}
): StatusCue | null {
  for (const cue of display?.cues ?? []) {
    if (conditionListMet(cue.conditions, cue.match, records, now, variables, primary)) return cue;
  }
  return null;
}

/** displayConditions returns every condition a placement's display reads: visibility, then each cue's. */
export function displayConditions(display: WidgetDisplay | null | undefined): Condition[] {
  return [...(display?.conditions ?? []), ...(display?.cues ?? []).flatMap((c) => c.conditions ?? [])];
}

/** VisibilityInputs are everything a placement's visibility depends on. */
export interface VisibilityInputs {
  state: LiveState;
  currentPage: string;
  pages: DashboardPage[];
  records: Record<string, unknown>;
  now: Date;
  variables?: VariableInputs;
  primaryAttributes?: PrimaryAttributes;
}

/** isVisible reports whether a placement shows: no locks, conditions met, and on the current page. */
export function isVisible(widget: DashboardWidget, inputs: VisibilityInputs): boolean {
  return (
    locksFor(widget, inputs.state).length === 0 &&
    onPage(widget, inputs.currentPage, inputs.pages) &&
    conditionsMet(widget.display, inputs.records, inputs.now, inputs.variables, inputs.primaryAttributes)
  );
}

/** nightDimLevel returns the scheduled dim level for now, or 0 outside the window. */
export function nightDimLevel(display: DashboardDisplay | null | undefined, now: Date): number {
  const night = display?.night_dim;
  if (!night) return 0;
  return windowStart(night.from, night.to, now) === null ? 0 : night.level;
}

/** DimInputs are what the effective dim level depends on. */
export interface DimInputs {
  display: DashboardDisplay | null | undefined;
  override: number | null | undefined;
  now: Date;
  /** idleSeconds is how long since the last input. */
  idleSeconds: number;
  /** awake is true while recent input has lifted dimming. */
  awake: boolean;
}

/**
 * dimLevel returns how dark the screen is, 0 to 100. Recent input wins over
 * everything; a command's override wins over the night window and idle
 * timeout, which otherwise combine by taking the darker.
 */
export function dimLevel(inputs: DimInputs): number {
  if (inputs.awake) return 0;
  if (inputs.override !== null && inputs.override !== undefined) return inputs.override;
  const idle = inputs.display?.idle_dim;
  const idleLevel = idle && inputs.idleSeconds >= idle.after ? idle.level : 0;
  return Math.max(nightDimLevel(inputs.display, inputs.now), idleLevel);
}

/** wakeSeconds is how long input lifts dimming. */
export function wakeSeconds(display: DashboardDisplay | null | undefined): number {
  return display?.idle_dim?.after || WAKE_SECONDS;
}
