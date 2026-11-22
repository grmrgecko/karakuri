// The dashboard currently on screen. Widgets hold no permissions of their own
// and know nothing about authorization: they ask the host for the data their
// config names, and the host says which dashboard the request came from. The
// server resolves that claim — the caller still has to be able to view the
// dashboard, and the capability still has to be one that dashboard's deployed
// layout exposes — so this is context, never a credential.
//
// It lives apart from the widget bridge because both the API client and the
// WebSocket store read it, and neither should have to know what a widget is.

let current: string | null = null;

// setDashboardContext names the dashboard being displayed, or clears it on the
// way out. A page that shows no dashboard carries no claim, so its requests are
// judged on the caller's own permissions alone.
export function setDashboardContext(id: string | null): void {
  current = id;
}

export function dashboardContext(): string | null {
  return current;
}

// DASHBOARD_HEADER matches middleware.DashboardHeader on the server.
export const DASHBOARD_HEADER = 'X-Karakuri-Dashboard';
