# Research

- Decision: Launch through existing Open URL proxy; keep waiting server-side.
  Rationale: The remote proxy serializes Config and Ports overrides, and long
  human waits must outlive agent execution deadlines. Alternative: a new agent
  RPC and local HTTP server would add transport and authentication work.
- Decision: Resume the same run through one selected output. Rationale: Preserves
  failure and input context. Alternative: independent button-trigger runs lose
  correlation and require deployed trigger/session ownership.
- Decision: Use SignalParker and a request-local completion channel. Rationale:
  Releases workers and respects cancellation, disable and redeploy. Timer and
  signal both return nil, so inspect request state atomically afterward.
- Decision: Store bounded attachments in the graph. Rationale: Drafts, versions,
  exports and deletion remain self-contained like existing note images.
  Alternative: dashboard uploads depend on a widget placement and do not export.
- Decision: Fragment token and bearer-header asset fetches. Rationale: Avoids
  bearer credentials in server paths, query strings, and referrers. Alternative:
  public image URLs would reveal private remediation documents.
- Constraint: Message-path nodes execute once per group. A Retry output must lead
  to a fresh downstream action or an intentional new run. No scheduler changes.
