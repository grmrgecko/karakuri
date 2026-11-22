# Data model: Durable waits and device state

All tables are new or gain one nullable column through AutoMigrate. No existing
row is rewritten, so existing databases, durable node state, variables,
control values, secrets, and enrolled identities read back unchanged.

## `pending_waits` (new)

One row is one node's held work for one key in one flow.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | integer | Primary key. |
| `flow_id` | uuid | Owning flow. Unique with `node_id` and `key`. |
| `node_id` | text | Graph node ID in the deployed graph. |
| `node_type` | text | Node type when armed. A redeploy that changes it drops the row. |
| `key` | text | Node-defined key: the keyed bucket, or `exec:<execution id>` for a wait owned by one node execution. |
| `token` | text | Random token that identifies the waiter that owns the row. |
| `deadline` | timestamp, nullable | Absolute wall-clock deadline. Null means state only, with no waiter to resume. |
| `run_id` | uuid | Run that armed or last restarted the wait. |
| `state` | text | JSON node state: held message or parts, payload settings, quiet window, retrigger mode. |
| `created_at`, `updated_at` | timestamp | |

Lifecycle:

```text
armed ──extend──▶ armed (same token, new deadline)
  │  └─restart──▶ armed (new token, new deadline, new run_id)
  ├─reset/cancel─▶ deleted
  ├─claim───────▶ deleted, then the node emits
  ├─suspend─────▶ unchanged (shutdown, redeploy)
  └─drop────────▶ deleted, with a canceled run recording why
                  (disable, flow delete, node removed or retyped, missed and drop)
```

The flow delete transaction removes the flow's rows next to its node store,
variables, and runs.

### Node states

| Node | Key | Deadline | State |
| --- | --- | --- | --- |
| Trigger | `armed`, or `armed:key:<key>` | expiry | message |
| Delay fixed/random | `exec:<execution id>` | release time | `kind: hold`, message |
| Delay debounce | `debounce`, or `debounce:key:<key>` | quiet-period end | `kind: debounce`, latest message, `maximum` |
| Delay rate queue | `exec:<execution id>` | slot time | `kind: queue`, message, epoch token, bucket key |
| Wait until | `exec:<execution id>` | target instant | message |
| Condition wait | `exec:<execution id>` | timeout | message, start time |
| Join/Batch/Aggregate, Limit and Sort sequences | `group:stream` or `group:g:<group id>` | timeout, or null | indexed parts, expected count, owner topic and group stack |

Stored messages keep a binary payload in `payload_binary` so it is restored as
bytes. The running server emits the messages it holds in memory; the stored
JSON is used only after a restart.

An execution ID is the run ID and node instance index, so it stays unique when
a fan-out delivers copies of one message ID.

Delay's rate epoch stays in the wait store as a state-only row
(`rate_epoch`, or `rate_epoch:key:<key>`, null deadline) so a reset before a restart still invalidates
queued messages after it. The rate schedule `next_at` stays in `DurableStore`
as it is today.

## `runs` (added column)

| Column | Type | Notes |
| --- | --- | --- |
| `resumed_from_run_id` | uuid, nullable | The arming run of a restored wait. Kept even after that run is pruned by retention. |

Resume runs record `trigger_node_id` as the waiting node. Their emitted
messages carry:

```json
"meta": {
  "resumed": {
    "from_run_id": "…",
    "deadline": "2026-10-30T18:00:00Z",
    "late_ms": 0
  }
}
```

## `mqtt_device_states` (new)

| Column | Type | Notes |
| --- | --- | --- |
| `device_id` | uuid | Primary key; the MQTT device. |
| `control` | text | Control kind the state was decoded for; state stored under another kind is not restored. |
| `state` | text | JSON merged state. |
| `reported_at` | timestamp | Time the state script last merged a report. |
| `updated_at` | timestamp | |

The device relay keeps two new in-memory fields per device: `stale` (true from
restore until the next merge) and `reportedAt`. Deleting the device or changing
its `control` kind deletes the row and clears the in-memory state.

## In memory

- **Resume queue**: one entry per wait awaiting admission, ordered by deadline.
  It exists only while a flow's restored waits are being started and is
  discarded on disarm.
- **Write queue**: the single-writer queue for `pending_waits`. Claims wait
  for their delete to commit; other writes do not wait.
