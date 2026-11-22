# Feature specification: Weather station overrides

**Feature**: `specs/041-weather-station-overrides`
**Created**: 2026-11-11
**Status**: Implemented
**Input**: Move weather widget metric overrides from the plugin's HTTP push
endpoint into a flow node. A local weather station publishing over MQTT
overrides the provider's current conditions for a postal code or coordinates,
and each reading returns to the provider's value after a maximum age.

## User scenarios and testing

### User story 1: Show my station's readings on the weather widget (Priority: P1)

A station on the roof publishes Weather Underground-style JSON to
`cotech-weather-station/state` every few minutes. A flow takes each message
into a Weather Override node for US 35757, and the kitchen weather widget
shows the station's temperature, humidity, pressure, and wind instead of the
provider's model values.

**Why this priority**: Local readings are more accurate than a forecast
model's current conditions, and the station already reports.
**Independent test**: Run the node with a station-shaped payload and read the
forecast API for coordinates inside the radius.

**Acceptance scenarios**:

1. **Given** an override for postal code 35757 in the US with a 10 km
   radius, **When** a forecast is requested for coordinates 6.8 km from the
   resolved place, **Then** its current conditions carry the station's
   readings and `override` lists them with when each was set and expires.
2. **Given** readings in imperial units, **When** they are set, **Then** they
   are stored in metric and served in the units the forecast was asked for.
3. **Given** a forecast outside every override's radius, **When** it is
   requested, **Then** it shows the provider's values and no `override`.
4. **Given** an open widget near an override, **When** the override is set,
   cleared, or a reading expires, **Then** the widget refetches.

### User story 2: Fall back when the station stops reporting (Priority: P1)

The station's battery dies. After the maximum age the widget shows the
provider's values again, with no flow change.

**Why this priority**: A stale station reading is worse than a model value.
**Independent test**: Set readings with a short maximum age and request the
forecast after it passes.

**Acceptance scenarios**:

1. **Given** a reading set with `max_age: 2m`, **When** two minutes pass with
   no new reading, **Then** the forecast shows the provider's value.
2. **Given** humidity set 16 minutes ago and temperature 6 minutes ago with a
   15 minute age, **When** a forecast is served, **Then** only the temperature
   is overridden.
3. **Given** `operation: clear`, **When** the node runs, **Then** every
   reading for the location is dropped at once.

### Edge cases

- A field that is blank or renders as empty, `null`, `unknown`,
  `unavailable`, or `none` keeps the provider's value; a run where every
  field does so fails with "no readings to set".
- A non-numeric reading or one outside the metric's physical range fails the
  node, so the failure branch and run history show it.
- A postal code resolves to the place that lists it, not the code's
  centroid; a code no result lists fails rather than borrowing a nearby
  place's coordinates.
- Overrides are in plugin memory; a restart shows provider values until the
  next report.
- Overlapping radii: the nearest live override wins.

## Requirements

### Functional requirements

- **FR-001**: The weather plugin MUST provide a `weather-override` node with
  an input, an output, and a failure branch, taking a postal code with an
  optional ISO country, or latitude and longitude.
- **FR-002**: The node MUST accept `temp`, `feels_like`, `dew_point`,
  `humidity`, `pressure`, `wind_speed`, `wind_deg`, and `uvi` as numbers or
  `{{ }}` text, read in metric (°C, m/s, hPa) or imperial (°F, mph, inHg)
  units, and store them in metric.
- **FR-003**: Each reading MUST expire `max_age` after it was last set
  (default 15 minutes, at most 24 hours), independently of the others.
- **FR-004**: Forecasts served by the plugin's API, Weather Forecast, and
  Weather Changed MUST apply the nearest live override within its
  `radius_km` (default 10, at most 200), for either provider, on a copy of
  the cached forecast.
- **FR-005**: A served forecast with an override MUST include `override`
  with its source, coordinates, and each replaced metric's `updated_at` and
  `expires_at`.
- **FR-006**: Setting, clearing, or expiring an override MUST publish an
  update on every watched forecast topic within its radius.
- **FR-007**: The node MUST resolve a postal code once per code and country
  and not remember a failed lookup.
- **FR-008**: The station push endpoint and its `api:weather:station-push`
  permission MUST be removed.

## Success criteria

- **SC-001**: A real Cotech station message changes the forecast served for
  a point inside 35757 to the station's values, matching Home Assistant's
  sensors for the same station.
- **SC-002**: A reading reverts to the provider's value within a second of
  its maximum age.
- **SC-003**: `make test TARGET=server` and `make dev` pass and the
  development service stays active.

## Assumptions and scope

- Wind gust, rain, and solar radiation have no field in the forecast's
  current conditions and are out of scope.
- Feels-like is not derived from an overridden temperature; a station's wind
  chill or heat index is set as `feels_like`.
- The widget draws no station badge; the `override` block is data for it and
  for flows.

## References

- [Plugins and extensible catalogs](../008-plugin-system/spec.md)
- [Dashboard layout and publication](../006-dashboard-authoring/spec.md)
- [Flow guide](../../docs/flow-authoring.md#weather-station-overrides)
