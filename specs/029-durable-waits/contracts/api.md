# Contract: REST, MCP, and broker shapes

## Pending waits

`GET /api/flows/{id}/waits` (permission `api:runs:read`) lists a flow's pending
waits, ordered by deadline. Waits with no deadline (state-only rows) are not
listed.

```json
{
  "waits": [
    {
      "node_id": "n7",
      "node_type": "trigger",
      "key": "arm:living_room",
      "deadline": "2026-10-30T22:00:00Z",
      "missed_deadline": "deliver",
      "run_id": "…",
      "durable": true
    }
  ]
}
```

`durable` is false for a wait held only in memory because its state could not
be stored.

`DELETE /api/flows/{id}/waits/{node_id}?key=…` (permission `api:runs:cancel`)
cancels one wait. It returns 200 with a message, like the other delete
endpoints, or 404 when no such wait exists. The waiter
finds its claim refused at its deadline and emits nothing. A canceled run with
error `wait cancelled by <user>` records the cancellation.

MCP:

- `inspect_flow_state` accepts `store: "waits"` and returns the same list.
- `cancel_flow_wait` takes `flow`, `node_id`, and `key`, and requires the same
  permission.

## Run records

Run JSON over REST, MCP `get_flow_run`/`list_flow_runs`, and the WebSocket
`run_started`/`run_finished` events gains:

```json
"resumed_from_run_id": "…"
```

The field is omitted for runs that were not resumed. Dropped waits appear as
canceled runs whose `error` is one of:

- `missed deadline 2026-10-30T22:00:00Z by 9h12m; dropped`
- `node n7 removed by redeploy; wait dropped`
- `node n7 changed type by redeploy; wait dropped`
- `flow disabled; wait dropped`

## MQTT device state

`GET /api/mqtt_devices` entries, the MCP `list_mqtt_devices` result, and the
broker message `mqtt_device_state` on `mqttdevice:{id}` gain:

```json
"stale": true,
"reported_at": "2026-10-30T17:41:03Z"
```

`stale` is true from restore until the device's next merged report.
`reported_at` is the time of the last merge, restored or live. `mqtt-device-changed`
does not fire on restore. `mqtt-device-get` adds `meta.stale` and
`meta.reported_at` to its output.
