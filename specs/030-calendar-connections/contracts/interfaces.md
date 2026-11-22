# Contract: calendar interfaces

## Host additions

`pkg/integrations`:

```go
type ConnectionKind struct {
	// ...existing fields
	// Probe tests a configuration like Test and also reports what it found,
	// such as the calendars an account holds. Optional; when set, connection
	// tests call it instead of Test.
	Probe func(ctx context.Context, id uuid.UUID, cfg []byte) (detail string, err error)
}
```

`connops.TestResult` gains `Detail string \`json:"detail,omitempty"\``,
returned by `POST /api/connections/{id}/test` and the MCP `test_connection`
tool and shown under the result in the connection settings form.

`pkg/nodeapi`:

```go
// TriggerRecorder is implemented by trigger contexts that can record an
// event the trigger decided not to start a run for.
type TriggerRecorder interface {
	// Skipped records a run that settles at once as canceled, with reason as
	// its error and msg's payload as its trigger payload.
	Skipped(msg *Msg, reason string)
}
```

## Plugin protocol additions

```proto
// HostService
rpc ConnectionAccessToken(ConnectionTokenRequest) returns (ConnectionTokenResponse);
rpc TriggerSkipped(TriggerSkippedRequest) returns (Empty);

message ConnectionTokenRequest { string conn_id = 1; }
message ConnectionTokenResponse { string token = 1; string error = 2; }
message TriggerSkippedRequest { string handle = 1; WireMsg msg = 2; string reason = 3; }

// TestKindConnection's response
message ErrorResponse { string error = 1; string detail = 2; }
```

`ConnectionAccessToken` returns an error unless the connection exists, is
enabled, and its driver provides `AccessToken(ctx)`. The SDK exposes it as
`sdk.Host.ConnectionAccessToken(ctx, id)` and `TriggerSkipped` through the
plugin trigger context's `Skipped`.

## Nodes

`calendar-event` output (topic `calendar:event`):

```json
{
  "edge": "start",
  "event": { "id": "…", "title": "Vacation", "calendar": "Family", "start": "…", "end": "…", "all_day": true },
  "moment": "2026-11-20T00:00:00-06:00",
  "fired_at": "2026-11-20T00:00:03-06:00"
}
```

`meta.late_ms` is set when the moment came while the server was down.
`meta.stale_since` is set when the event list came from a failed refresh's
last good fetch.

`calendar-events` output:

```json
{ "in_progress": [ { "...": "event" } ], "upcoming": [ { "...": "event" } ] }
```

## Plugin HTTP API

- `GET /api/plugins/calendar/connections` (`api:dashboards:write`): running
  calendar connections the widget panel can choose, with `id`, `name`, and
  `kind` only.
- `POST /api/plugins/calendar/{dashboard}/{widget}/set_source` (existing
  route, new body): `[{connection_id, name, color, tag, calendars}]`.
  Connections that are not running are refused. The `/{provider}/config_schema`
  route and the `ics`/`google` widget providers are removed.
- The event route the widget reads is unchanged in shape.

## Skipped and late runs

A dropped occurrence is a canceled run with trigger node the calendar trigger
and error `missed <edge> of "<title>" at <moment> by <lateness>; dropped`.
