# Data model: Stateful flow nodes

- Hysteresis: durable map of key to active boolean, atomic transition updates.
- Rate timer: durable next-admission timestamp; existing unkeyed `next_at` remains.
- Debounce/trigger: transient owned arm, deadline and optional pending message.
  Keyed state must not consume another key's timer. Reset invalidates ownership.
- Dedupe: existing `seen`, `max`, `latest` values with optional admission timestamp records.
  Rejected messages do not move timestamps; expired entries are removed lazily.
- Fresh pull: isolated in-memory values/inflight cache per observation. No ordinary
  run cache writes, persisted values, or repeated producer success events.
- Condition wait subscriptions: transient map of buffered wake channels in one
  flow/node store. Register before observing, signal under the store update lock,
  and remove by channel identity on completion, failure or cancellation. Each
  execution retains its own message, stability interval and absolute deadline.
- HA hold: live per-entity arm plus connection continuity. Never persisted.
- HTTP error: message, retry permission, optional minimum delay and JSON-compatible
  bounded safe details. Error branches preserve the original input envelope.

No database schema changes, secret re-encryption, identity changes or migration
of existing graph documents is required.
