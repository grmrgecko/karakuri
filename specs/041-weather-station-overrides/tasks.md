# Tasks: Weather station overrides

**Feature**: `specs/041-weather-station-overrides`
**Input**: [spec.md](spec.md), [plan.md](plan.md)

- [X] T001 Override store with per-reading expiry, radius lookup, and expiry callback in `plugins/weather/overrides.go`
- [X] T002 `weather-override` node, reading parsing, and postal cache in `plugins/weather/nodes.go`
- [X] T003 `PostalSearch` and `matchPostal` in `plugins/weather/openmeteo.go`, with the captured fixture `testdata/openmeteo-geocoding-postal-35757.json`
- [X] T004 Apply overrides on a copy in the forecast route, Weather Forecast, and Weather Changed; add `override` to `ForecastData`
- [X] T005 Publish updates to watched topics within the radius through `Scheduler.Watched`
- [X] T006 Remove the station push route, `StationMetrics`, and the `api:weather:station-push` permission
- [X] T007 Tests in `plugins/weather/overrides_test.go`
- [X] T008 Document the node in `docs/flow-authoring.md` and the widget behaviour in `docs/dashboard-authoring.md`
- [X] T009 Live: node test, expiry revert, and the Cotech station flow for 35757
