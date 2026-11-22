import { parseAttr } from "../../shared/dashboardLive";

/** MAX_FEEDS matches the server, which reads no more than this many. */
export const MAX_FEEDS = 5;

export interface FeedSource {
  url: string;
  title: string;
}

// NewsConfig is a placement's feeds and how their headlines are drawn. The
// server reads the feed URLs from this saved config, never from a request.
export interface NewsConfig {
  feeds: FeedSource[];
  /** interval is seconds between headlines. */
  interval: number;
  /** reload is minutes between fetches of the feeds. */
  reload: number;
  maxItems: number;
  /** maxAgeHours drops older items; 0 keeps every item. */
  maxAgeHours: number;
  showDescription: boolean;
  showSource: boolean;
  showTime: boolean;
  /** layout is one headline at a time, or a stacked window of listSize. */
  layout: "ticker" | "list";
  listSize: number;
}

export const DEFAULT_CONFIG: NewsConfig = {
  feeds: [],
  interval: 10,
  reload: 5,
  maxItems: 20,
  maxAgeHours: 0,
  showDescription: false,
  showSource: true,
  showTime: true,
  layout: "ticker",
  listSize: 5,
};

function number(value: unknown, fallback: number, min: number): number {
  const n = Number(value);
  return Number.isFinite(n) && n >= min ? n : fallback;
}

/** parseConfig reads the host's config attribute, falling back field by field. */
export function parseConfig(attr: string | Record<string, unknown>): NewsConfig {
  const f = parseAttr(attr);
  const feeds = Array.isArray(f.feeds)
    ? f.feeds
        .filter((x) => x && typeof x === "object")
        .slice(0, MAX_FEEDS)
        .map((x: any) => ({ url: String(x.url ?? ""), title: String(x.title ?? "") }))
    : [];
  return {
    feeds,
    interval: number(f.interval, DEFAULT_CONFIG.interval, 2),
    reload: number(f.reload, DEFAULT_CONFIG.reload, 1),
    maxItems: Math.floor(number(f.maxItems, DEFAULT_CONFIG.maxItems, 1)),
    maxAgeHours: number(f.maxAgeHours, DEFAULT_CONFIG.maxAgeHours, 0),
    showDescription: f.showDescription === true,
    showSource: f.showSource !== false,
    showTime: f.showTime !== false,
    layout: f.layout === "list" ? "list" : "ticker",
    listSize: Math.floor(number(f.listSize, DEFAULT_CONFIG.listSize, 1)),
  };
}

export interface NewsItem {
  title: string;
  link?: string;
  description?: string;
  published?: string;
  source: string;
}

export interface FeedError {
  feed: string;
  error: string;
}

/** keep applies the age and count limits to the server's newest-first items. */
export function keep(items: NewsItem[], cfg: NewsConfig, now: number): NewsItem[] {
  const cutoff = cfg.maxAgeHours > 0 ? now - cfg.maxAgeHours * 3600_000 : 0;
  return items
    .filter((item) => item && typeof item.title === "string" && item.title.trim() !== "")
    .filter((item) => {
      if (!cutoff || !item.published) return true;
      const t = Date.parse(item.published);
      return Number.isNaN(t) || t >= cutoff;
    })
    .slice(0, cfg.maxItems);
}

/** ago renders a publication time as a short relative age. */
export function ago(published: string | undefined, now: number): string {
  if (!published) return "";
  const t = Date.parse(published);
  if (Number.isNaN(t)) return "";
  const minutes = Math.floor((now - t) / 60_000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

/** itemKey identifies a headline across fetches. */
export function itemKey(item: NewsItem): string {
  return `${item.source}\u0000${item.link ?? ""}\u0000${item.title}`;
}
