# Data model: Time-series connections and history forwarding

## `history_forwards` (persistent, AutoMigrate)

| Field | Type | Rules |
| --- | --- | --- |
| `id` | UUID | Primary key |
| `name` | text | 1 to 120 bytes |
| `enabled` | bool | Default true |
| `source_type` | text | `control`, `mqtt_device`, or `ha` |
| `source_ids` | JSON array of UUID | Controls or devices; empty means all. Unused for `ha` |
| `owner_id` | UUID, nullable, indexed | Set when the forward belongs to one control or device editor; then `source_ids` holds exactly that ID |
| `connection_id` | UUID, nullable | Home Assistant connection for `ha` |
| `include`, `exclude` | JSON arrays of text | `ha` entity ID globs; `include` needs at least one |
| `attributes` | JSON array of text | `ha` attributes to record besides the state |
| `all_attributes` | bool | `ha`: record every numeric attribute |
| `destinations` | JSON array of UUID | 1 to 10 writable connections |
| `metric` | text | Optional override; a valid metric name |
| `labels` | JSON object | Up to 20 extra labels; names must be valid label names and not reserved (`id`, `name`, `attribute`, `unit`, `entity_id`, `domain`, `friendly_name`, `connection`, `state`) |
| `min_interval_ms` | int | 0 to 86,400,000 |
| `created_at`, `updated_at` | time | |

An editor-owned forward is unique per `owner_id`. Deleting a control or device
deletes its owned forward; other forwards drop the deleted ID on the next
reload.

## Connection settings added by kind

| Kind | Fields |
| --- | --- |
| `prometheus` | `url`, `auth` (`none`, `basic`, `bearer`), `username`, `password`*, `token`*, `headers` (map, values secret), `tls_skip_verify`, `timeout_s`, `scrape_interval_s`, `write_enabled`, `write_url`, `heartbeat_s`, `buffer_mib` |
| `influxdb` | `version` (1, 2, 3), `url`, `database` (v1, v3), `retention_policy` (v1), `username`, `password`* (v1), `org`, `bucket` (v2), `token`* (v2, v3), `tls_skip_verify`, `timeout_s`, `write_enabled`, `heartbeat_s`, `buffer_mib` |
| `postgres` | `host`, `port`, `database`, `username`, `password`*, `sslmode`, `timeout_s`, `write_enabled`, `table`, `heartbeat_s`, `buffer_mib` |

\* secret. Heartbeat defaults to 60 for `prometheus` and 0 (off)
for the others.

## Point (spool and sink)

`{"t": <unix ms>, "m": "<metric>", "l": {"k": "v"}, "v": <number|null>, "s": "<state>|null"}`

## Writer status (memory)

`state` (`ok`, `failing`, `disabled`, `idle`), `last_error`, `last_success`,
`buffered_points`, `buffered_bytes`, `written`, `discarded`, `rejected`,
`skipped`. Buffered counts are read from the spool at start; totals reset on
restart.

## Series key

`destination ID + metric + sorted labels`, used for heartbeats, coalescing,
and per-event deduplication.
