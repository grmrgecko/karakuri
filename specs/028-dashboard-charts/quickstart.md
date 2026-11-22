# Quickstart: Dashboard charts

1. Bring up the containers (`make tsdb-up`), build with `make dev`, restart
   `karakuri-dev.service`, and create the feature 027 connections.
2. Seed data with `make tsdb-seed`. It writes a sine wave, a door state
   series, and an OHLC series to every container.
3. Create a test dashboard "Chart check" with a `range` time range variable and
   one chart per visualization over the seeded data, plus one Home Assistant
   statistics chart. Deploy it and make it public.
4. In the browser:
   - Open it and screenshot each chart.
   - Pick "Last 7 days".
   - Drag a zoom.
   - Step back.
   - Open a `var-range=now-1h,now` link.
   - Check the console after each step.
5. Use `curl` with no session to post to the chart route with query text, an
   unknown placement, and a non-option value, and expect refusals.
6. `render_dashboard` with `var-range=now-7d,now`; inspect the image.
7. Report the dashboard and connections left in the development database.
