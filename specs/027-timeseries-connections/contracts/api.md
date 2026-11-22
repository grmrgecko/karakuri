# Interfaces: Time-series connections and history forwarding

## REST

| Route | Permission | Body / query | Result |
| --- | --- | --- | --- |
| `POST /api/connections/{id}/timeseries/query` | connections read | `{query: Query}` | `Result` |
| `GET /api/connections/{id}/timeseries/metadata` | connections read | `kind` (`metrics`, `labels`, `label_values`, `databases`, `measurements`, `tag_keys`, `tag_values`, `field_keys`, `schemas`, `tables`, `columns`, `entities`, `statistic_ids`), `name`, `match`, `measurement`, `table` | `{values: [string]}` |
| `GET /api/history/forwards` | connections read | `source=control:<id>` or `mqtt_device:<id>` optional | `[Forward]` with `status` per destination |
| `POST /api/history/forwards` | connections write | `Forward` | `Forward` |
| `PUT /api/history/forwards/{id}` | connections write | `Forward` | `Forward` |
| `DELETE /api/history/forwards/{id}` | connections write | | |
| `GET /api/history/status` | connections read | | `[{connection_id, name, kind, status}]` |

Connection items for writable kinds gain `writer` with the writer status.

`Query`:

```json
{
  "ref_id": "A",
  "language": "promql | influxql | flux | sql | ha_history | ha_statistics",
  "text": "rate(node_cpu_seconds_total[$__rate_interval])",
  "ha": {"entities": ["sensor.x"], "attribute": "", "statistic_ids": [], "period": "hour", "types": ["mean"]},
  "from": "now-6h", "to": "now",
  "max_data_points": 1000, "min_interval": "15s",
  "format": "time_series | table", "legend_format": "{{instance}}",
  "instant": false
}
```

`Result`:

```json
{
  "series": [{"name": "A", "labels": {"instance": "a"}, "unit": "°C",
              "fields": [{"name": "time", "type": "time", "values": [1730000000000]},
                         {"name": "value", "type": "number", "values": [21.5]}]}],
  "truncated": false, "executed_query": "...", "interval_ms": 15000
}
```

Errors: 404 for an unknown connection; 422 `rejected` or `unsupported` with
the database message; 502 `unavailable`.

## MCP

| Tool | Permission | Input |
| --- | --- | --- |
| `query_timeseries` | connections read | `connection_id`, `query` |
| `list_timeseries_metadata` | connections read | `connection_id`, metadata fields |
| `list_history_forwards` | connections read | optional `source` |
| `save_history_forward` | connections write | `forward` (with `id` to update) |
| `delete_history_forward` | connections write | `id` |
| `get_history_status` | connections read | |

## Nodes

- `timeseries-query`: config `connection`, `language`, `query`, `entities`,
  `attribute`, `statistic_ids`, `period`, `statistic_types`, `from`, `to`,
  `max_data_points`, `min_interval`, `instant`, `reduce`. Output `out`:
  `{series, value, truncated, interval_ms}`.
- `timeseries-write`: config `connection`, `metric`, `labels`, `value`,
  `timestamp`, `wait`. A payload with `points: [{metric, labels, value,
  state, time}]` writes those instead. Output `out` passes the message
  through with `written` set to the point count.
