# Node contracts

- `hysteresis`: message `in`, optional `reset`; `active` and `inactive` preserve
  the message. Configure numeric property, lower/upper, direction and optional key.
- `condition-wait`: message `in`; optional `check` signal and value parameter.
  `matched`, `timeout` and `error` preserve the original payload. Condition evaluates the fresh value
  as payload. Configure condition, poll interval, stable duration and timeout.
  `check` requests a fresh read for every active wait on this flow/node; its
  payload is ignored. It starts no wait and emits nothing. Polling continues,
  false observations restart the hold, and signals never extend the deadline.
  Check-only execution skips value pulls and execute-once claims. Repeated
  pending checks can coalesce. External triggers obey normal run admission.
- `delay`: retain `in`/`out`; add reset and dropped/superseded outputs, key and
  debounce maximum wait. Rate state remains durable; waiting messages are transient.
- `trigger`: retain ports; add key for independent arms and selected reset.
- `dedupe`: retain `in`/`out`; add reset, duplicate and expiry. Expiry begins on
  admission. Legacy history acquires a timestamp on first expiry-aware read.
- `ha-state`: retain output; add held-for. Compare each entity independently and
  cancel on changed-away state or lost connection continuity.
- `ha-call-service`: add area/device/label ID lists, explicit confirmation entity,
  optional attribute, expected value, stability duration, interval and timeout.
  Add timeout output. Only initial action failures use the action retry policy.
- `http-request`: add retry mode/status controls and bounded response error details.
  Generic retry policy adds optional jitter (0–1), with 1–1000 attempts and finite, nonnegative delays. Safe defaults do not repeat mutating
  requests unless opted in. Retry-After is a minimum subject to the configured cap.

All settings derive from catalog metadata; unknown fields remain rejected.
Runtime validation rejects invalid ranges before performing external actions.
