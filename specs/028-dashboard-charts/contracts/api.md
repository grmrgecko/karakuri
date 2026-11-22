# Interfaces: Dashboard charts

| Route | Access | Body | Result |
| --- | --- | --- | --- |
| `POST /api/dashboards/{id}/widgets/{widgetId}/chart` | Dashboard view (session, share, public, render) | `{vars, from, to, width, refs}` | `{results: [{ref_id, result, error, cached}], range: {from, to}, interval_ms}` |
| `POST /api/dashboards/{id}/chart/preview` | Dashboard edit and connections read | `{query, vars, from, to, width}` | Same as one result |
| MCP `get_chart_data` | Dashboard view for the principal | `dashboard`, `widget_id`, `vars`, `from`, `to` | Same as the data route |

`result` is feature 027's `Result`. `error` is `{class, message}`.

## Chart config (placement)

```json
{
  "viz": "timeseries",
  "queries": [{"refId": "A", "connectionId": "…", "language": "influxql",
               "text": "SELECT mean(value) FROM … WHERE $timeFilter GROUP BY time($__interval)",
               "ha": null, "legendFormat": "", "maxDataPoints": 0,
               "minInterval": "", "instant": false, "format": "time_series",
               "hide": false}],
  "timeRange": "${range}", "relativeTime": "", "timeShift": "", "refresh": "30s",
  "fieldConfig": {"defaults": {"unit": "celsius", "decimals": 1,
                  "thresholds": {"mode": "absolute", "steps": [{"value": null, "color": "green"}]},
                  "mappings": [], "color": {"mode": "palette-classic"}},
                  "overrides": [{"matcher": {"id": "byName", "options": "A"},
                                 "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "blue"}}]}]},
  "transformations": [{"id": "renameByRegex", "options": {"regex": "(.*)", "renamePattern": "$1"}}],
  "options": {"legend": {"displayMode": "list", "placement": "bottom", "calcs": []},
              "tooltip": {"mode": "single"}}
}
```
