// Shared by the widgets that read a dashboard's runtime display state:
// rotating-message, flow-text, and news-feed.

// LiveContent is what a flow's content command left in one placement: the
// value, the format it was sent as, and for Markdown the server's rendering.
export interface LiveContent {
  value: unknown;
  format?: string;
  html?: string;
  updated_at?: string;
}

// watchContent follows a placement's pushed content on the dashboard's live
// state and calls apply with it, or with null when there is none. It reads the
// current state once and then the live topic, and drops any state older than
// the last one applied, so a slow first read cannot overwrite a newer push.
// The returned function stops it; a read that lands after that is ignored.
export function watchContent(
  dashboardId: string,
  widgetId: string,
  apply: (content: LiveContent | null) => void,
): () => void {
  const bridge = (window as any).__karakuri;
  if (!bridge || !dashboardId || !widgetId) return () => {};

  let closed = false;
  let lastSeq = -1;

  const take = (state: any): void => {
    if (closed || !state || typeof state !== "object") return;
    const seq = Number(state.seq ?? 0);
    if (seq < lastSeq) return;
    lastSeq = seq;
    const content = state.content?.[widgetId];
    if (!content || typeof content !== "object" || content.value === null || content.value === undefined) {
      apply(null);
      return;
    }
    apply(content as LiveContent);
  };

  // The host's watcher refetches after the socket reconnects, so content
  // pushed while it was down still arrives. An older host without it gets
  // the topic and one read.
  if (typeof bridge.watchDashboardLive === "function") {
    const stop = bridge.watchDashboardLive(dashboardId, (state: any) => take(state));
    return () => {
      closed = true;
      stop?.();
    };
  }

  const off = bridge.subscribeTopic(`dashboard:${dashboardId}:live`, (data: any) => take(data?.state));

  // The client unwraps the response envelope; the fallback reads one that was
  // not.
  bridge.api
    .get(`/dashboards/${encodeURIComponent(dashboardId)}/live`)
    .then((res: any) => take(res && typeof res === "object" && "seq" in res ? res : res?.data))
    .catch(() => {});

  return () => {
    closed = true;
    off?.();
  };
}

// placementIds reads the IDs the host injects into a widget's config.
export function placementIds(attr: string | Record<string, unknown>): { dashboardId: string; widgetId: string } {
  let parsed: Record<string, unknown> = {};
  if (typeof attr === "string") {
    try {
      parsed = JSON.parse(attr);
    } catch {
      parsed = {};
    }
  } else if (attr && typeof attr === "object") {
    parsed = attr;
  }
  return {
    dashboardId: String(parsed.dashboardId ?? ""),
    widgetId: String(parsed.dashboardWidgetId ?? ""),
    references: Array.isArray(parsed.dashboardReferences) ? parsed.dashboardReferences.map(String) : [],
  };
}

// parseAttr reads the host's config attribute without the IDs it injects,
// which are the host's bookkeeping and never part of what a panel saves.
export function parseAttr(attr: string | Record<string, unknown>): Record<string, unknown> {
  let parsed: Record<string, unknown> = {};
  if (typeof attr === "string") {
    try {
      parsed = JSON.parse(attr);
    } catch {
      parsed = {};
    }
  } else if (attr && typeof attr === "object") {
    parsed = attr;
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
  const { dashboardId, dashboardWidgetId, dashboardReferences, ...fields } = parsed;
  void dashboardId;
  void dashboardWidgetId;
  void dashboardReferences;
  return fields;
}
