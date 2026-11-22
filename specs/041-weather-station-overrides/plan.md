# Design plan: Weather station overrides

**Feature**: `specs/041-weather-station-overrides` | **Date**: 2026-11-11 | **Spec**: [spec.md](spec.md)

## Summary

The weather plugin keeps an in-memory override store keyed by rounded
coordinates. The `weather-override` node resolves its location, converts its
readings to metric, and merges them into the store with per-reading expiry.
Every forecast path looks up the nearest live override within its radius and
applies it to a copy of the forecast before unit conversion.

## Lessons from the removed push endpoint

`POST /station/push_metrics/{lat}:{lon}` shaped the design by its faults:

- It wrote station values into the cached forecast pointer, so a reading
  stayed in the cache until the next provider poll replaced it and expiry did
  not revert anything. Overrides now apply to a copy on each read.
- Every field was required and a missing one read as zero. Readings are now
  optional and merge per metric.
- One 60 second TTL covered all readings. Each reading now has its own
  configurable maximum age.
- It published only on the OpenWeatherMap topic for the exact coordinates.
  Updates now go to every watched topic, either provider, within the radius.
- Matching needed the widget's exact four-decimal coordinates. A radius now
  lets a postal code or a station's own coordinates cover nearby widgets.

## Components

- `plugins/weather/overrides.go`: metric catalog with ranges and imperial
  conversion, `overrideStore` (set, clear, nearest lookup, expiry timer that
  calls back with expired locations), `applyOverride`, `notifyOverride`.
- `plugins/weather/nodes.go`: `WeatherOverrideNode`, reading parsing, postal
  resolution cache; Weather Forecast and Weather Changed apply overrides.
- `plugins/weather/openmeteo.go`: `PostalSearch` and `matchPostal`, using the
  `postcodes` list of Open-Meteo geocoding results.
- `plugins/weather/scheduler.go`: `Watched` lists polled locations for
  update fan-out.
- `plugins/weather/handler.go`, `main.go`: the push route and permission are
  gone; the forecast route applies overrides.

## Verification

- Unit: `matchPostal` against a captured Open-Meteo answer for 35757;
  store lookup for per-reading expiry, radius, and nearest match.
- Live: the node over MCP `test_node`, forecast reads through the API before
  and after expiry, and a deployed flow fed by the real Cotech station on the
  lab broker, compared with Home Assistant's sensors.
