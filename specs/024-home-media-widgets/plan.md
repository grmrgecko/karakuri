# Design plan: Home media and list widgets

**Feature**: `specs/024-home-media-widgets` | **Date**: 2026-10-24 | **Spec**: [spec.md](spec.md)

## Summary

Home Assistant already supplies every source these widgets need. The server
adds read routes for the shapes `get_states` cannot carry (history, to-do items,
images, and streams), each scoped by the same dashboard binding rule as entity
states. Writes reuse the service call route. Widgets are core packages declaring
their bindings, so placing one is what grants its entity.

## Technical context

- Components: `pkg/integrations/homeassistant` (WebSocket commands and an
  authenticated HTTP fetch), `pkg/api` (routes), `frontend/src/lib/widgetBridge.ts`
  (raw fetch), `pkg/dashlive` and the viewer (spoken alerts),
  `plugins/core/widgets` (six widgets, Flow Text table), `plugins/weather`
  (air quality).
- Storage: none.
- Limits: history 1 to 168 hours; image bodies up to 10 MiB; a stream ends when
  the viewer disconnects or Home Assistant closes it.

## Constitution check

- Failures: relay failures return 502 with the reason; widgets show it and retry.
  Air quality failures leave the forecast intact.
- Permissions: every new read route checks connection read or the dashboard's
  read capability for the exact entity, and the entity's domain. The token is
  added server-side and never returned. Writes use the existing control policy.
- State: no schema, key, or identity changes.
- Checks: `make dev`, `make test` for server and frontend, live checks against the
  development Home Assistant connection where it has the entity types, and
  browser screenshots and console checks.

## Design and contracts

Driver additions:

- `History(ctx, entityID, start, end)` sends `history/history_during_period`
  with `minimal_response` and `no_attributes` and returns `{t, v}` points.
- `TodoItems(ctx, entityID)` sends `todo/item/list`.
- `Fetch(ctx, path)` performs an authenticated GET of a Home Assistant `/api/`
  path without following redirects.

Routes (optionally authenticated, dashboard scope as in `haVisibleEntities`):

| Route | Result |
| --- | --- |
| `GET /connections/{id}/ha/history/{entity}?hours=24` | `{entity_id, unit, points: [{t, v}]}` with `t` in Unix milliseconds |
| `GET /connections/{id}/ha/todo/{entity}` | `{items: [{uid, summary, status, due, description}]}` |
| `GET /connections/{id}/ha/camera/{entity}/snapshot` | Image bytes |
| `GET /connections/{id}/ha/camera/{entity}/stream` | Home Assistant's `multipart/x-mixed-replace` MJPEG stream |
| `GET /connections/{id}/ha/media/{entity}/art` | The image at the entity's relative `entity_picture`; 404 when absent or external |

`window.__karakuri.fetch(path, init)` returns a `Response` for an `/api` path,
with the session token and dashboard header the API client already sends.

`dashlive.Alert` gains `speak`; the Dashboard Command node, MCP, REST, and the
dashboard notification service (`speak` setting) accept it. The viewer speaks an
alert whose ID it first sees in a live message, never one present when it loaded.

Widgets: `camera` (snapshot or live, fit, name overlay; binding `.`),
`media-player` (binding `.`), `todo-list` (binding `.`), `history-graph`
(bindings `series`), `countdown`, `wifi-qr`. Flow Text draws an array of objects
as a table. Weather fetches Open-Meteo's air-quality API beside the forecast and
adds an `air_quality` block and layout.

## Verification

- Go test through the real router: a dashboard binding one entity lets an
  anonymous viewer read its history and to-do routes and refuses another entity,
  and camera routes refuse a non-camera entity.
- Go test in `plugins/weather`: real Open-Meteo air-quality responses saved for
  Berlin and Huntsville decode into the air-quality block, keeping zero pollen
  readings and dropping null ones.
- Live: the development Home Assistant's doorbell camera (snapshot and stream),
  shopping list (add, tick, then remove the test item), two sensors' history, an
  idle media player (drawn; playback not pressed, since it would play on a real
  device), an MCP alert with `speak` recorded in the page, browser screenshots of
  each widget and the editor panels, and the console.

## Risks and open decisions

- MJPEG through the server holds one Home Assistant stream per open viewer.
- Some media players report art only as external URLs, which the browser loads
  directly from their host.
