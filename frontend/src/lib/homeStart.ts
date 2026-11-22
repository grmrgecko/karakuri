// The start page a signed-in user lands on at `/`. The preference is checked
// against the dashboards the server says the caller can see, so a stale or
// foreign ID only falls back and never opens or names anything.

export type StartPage =
  | { kind: 'overview' }
  | { kind: 'dashboard'; id: string }
  | { kind: 'list' };

/** OVERVIEW is the start_page preference value that picks the overview. */
export const OVERVIEW = 'overview';

/**
 * resolveStart picks the start page: the chosen dashboard when the caller can
 * view it; the overview when chosen or when the caller can read flows; the
 * only dashboard of someone who cannot read flows; otherwise the dashboard
 * list.
 */
export function resolveStart(
  startPage: unknown,
  canReadFlows: boolean,
  dashboardIds: string[],
): StartPage {
  if (typeof startPage === 'string' && startPage !== OVERVIEW && dashboardIds.includes(startPage)) {
    return { kind: 'dashboard', id: startPage };
  }
  if (startPage === OVERVIEW || canReadFlows) return { kind: 'overview' };
  if (dashboardIds.length === 1) return { kind: 'dashboard', id: dashboardIds[0] };
  return { kind: 'list' };
}

/** pinnedIds reads the pinned_dashboards preference as a list of IDs. */
export function pinnedIds(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((v): v is string => typeof v === 'string') : [];
}
