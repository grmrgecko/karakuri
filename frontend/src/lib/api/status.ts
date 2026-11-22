// transientStatus reports whether an HTTP status is the server failing or
// being unavailable rather than answering for the request: a timeout, a rate
// limit, or a 5xx, which a reverse proxy sends while the service restarts.
// Such an answer says nothing about whether a session is still good.
export function transientStatus(status: number | undefined): boolean {
  return status === 408 || status === 429 || (status !== undefined && status >= 500);
}
