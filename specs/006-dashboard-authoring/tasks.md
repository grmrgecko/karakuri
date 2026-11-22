# Tasks: Weather widget provider and refresh recovery

- [x] T001 Align weather provider defaults and publish config schema/background documentation (FR-017).
- [x] T002 Scope forecast state to its selection, reject obsolete responses, and match topic coordinates/units (FR-018).
- [x] T003 Verify wire coordinate boundaries, browser failure/recovery and races, and the live photo background.
- [x] T004 Build/test through Makefile targets, restart and check the service, and commit the verified change.
- [x] T005 Honor zero dashboard padding/gaps in settings and layout; verify full-viewport image bounds (FR-019).

Verification on 2026-10-06: `make dev`, server tests, and frontend checks passed;
frontend build/checks passed again after the spacing correction. Coordinate tests pin
Huntsville, southern latitudes, trailing zeroes, halfway values, and repeated rounding.
A browser fixture exercised the real widget with delayed HTTP responses and topic
callbacks. Omitted provider selected Open-Meteo; provider failure cleared the previous
forecast; obsolete responses were ignored; presentation-only edits did not refetch;
metric topic updates fetched imperial HTTP data; same-selection errors remained visible
and Retry recovered. Fixture changes were DOM-only and removed by navigation.

The user's trace showed omitted-provider weather configs, repeated overlapping widget
writes, and no built-in background selection. OpenWeatherMap returned a 401 requiring
One Call subscription access; Open-Meteo succeeded. With user approval, the existing
Weather draft was backed up under `build/var/assistant-eval/`, reduced to one full-grid
Huntsville weather widget using Open-Meteo and the photo background, and given zero
padding/gap/widget padding. No deployment or render schedule was created, and no test
dashboard was added. Because this dashboard is undeployed, its ordinary view uses the
draft. MCP validation returned no issues. The 320x240 draft image had one ready widget
at (0, 0) with 320x240 bounds and no browser errors; overflow diagnostics still reported
internal scaled text bounds. Editor settings displayed zero spacing after reload.
Screenshots were captured after browser interactions, and no new console errors appeared
during the regression checks. The restarted service was active and returned HTTP 200.
