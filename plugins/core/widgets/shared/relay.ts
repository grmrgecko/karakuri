// Images and streams come through the host's authenticated fetch rather than
// an <img> URL: an image request cannot carry the session token or the
// dashboard header, and the server will not hand a camera to a caller that
// cannot say which dashboard it is viewing.

const bridge = () => (window as any).__karakuri;

/** relayFetch requests an /api path through the host, which adds the credentials. */
export function relayFetch(path: string, init?: RequestInit): Promise<Response> {
  const fetcher = bridge()?.fetch;
  if (typeof fetcher !== "function") {
    return Promise.reject(new Error("this Karakuri version cannot relay images; update and reload"));
  }
  return fetcher(path, init);
}

/** responseError reads the reason from a failed response's JSON error body. */
export async function responseError(res: Response): Promise<string> {
  try {
    const body = await res.json();
    const message = body?.message || body?.error;
    if (message) return String(message);
  } catch {
    // Not JSON: the status line is all there is.
  }
  return res.statusText || `HTTP ${res.status}`;
}

/** isAbort reports whether an error is the widget cancelling its own request. */
export function isAbort(e: unknown): boolean {
  return (e as any)?.name === "AbortError";
}

/** errorMessage reads an Error, an API client error object, or anything else. */
export function errorMessage(e: unknown, fallback: string): string {
  const message = (e as any)?.message;
  return message ? String(message) : fallback;
}
