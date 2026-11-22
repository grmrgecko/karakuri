# REST contract: Flow shares

These routes mirror the dashboard share routes. All require authentication.

## List shares

`GET /api/flows/{id}/shares`. The caller needs admin on the flow.

```json
{"data": [
  {"id": 3, "flow_id": "…", "user_id": "…", "user": {"username": "sam"}, "permission": "edit", "created_at": "…"},
  {"id": 4, "flow_id": "…", "group_id": 2, "group": {"name": "Kids"}, "permission": "faceplate", "created_at": "…"}
]}
```

## Add a share

`POST /api/flows/{id}/shares`. The caller needs admin on the flow.

```json
{"username": "sam", "permission": "edit"}
{"group_name": "Kids", "permission": "faceplate"}
```

- Exactly one of `username` and `group_name` is required.
- `permission` is one of `faceplate`, `view`, `edit`, or `admin`, and
  defaults to `view`.
- Any other level returns 400 with a validation error and stores nothing.
- An unknown user or group returns 400.
- A successful add returns 201 with the share and records a `flow` event:
  "Shared {flow} with {grantee} at {level}".

## Remove a share

`DELETE /api/flows/{id}/shares/{shareId}`. The caller needs admin on the
flow. It removes the share and records a `flow` event. A share belonging to
another flow returns 404.

## Flow responses

`GET /api/flows/{id}` and list items gain:

```json
{"access_level": "edit"}
```

The value is the caller's highest level: `faceplate`, `view`, `edit`, or
`admin`. It is a hint for the editor. The server enforces every route
regardless.

## Ownership transfer

`PUT /api/flows/{id}` accepts `"owner_id": "<uuid>"` only from a holder of
`api:flows:manage`; anyone else receives 403. The new owner must exist.

## Errors

| Situation | Status |
|---|---|
| The caller holds no level on the flow | 404 `flow not found` |
| The caller holds a level below what the route needs | 403 `forbidden` |
| The route's global permission is missing | 403, unchanged |
