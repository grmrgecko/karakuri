# Quickstart: Event log

Against the development server. Test objects are prefixed `Event test`, and
test devices use the `karakuri-test` base topic on the lab broker, never
`zigbee2mqtt/`.

## 1. Who changed it

1. Create a number control `Event test level` and a flow that sets it from a
   manual trigger.
2. Set it from a dashboard as admin, through the REST API with the MCP token,
   and by running the flow.
3. `GET /api/events?subject=control:<id>`: three entries naming admin and the
   dashboard, the `mcp` API key, and the flow with its run (SC-001).
4. Command a `karakuri-test` device from the device page; its entry names
   admin.

## 2. Security

1. Fail a sign-in as `admin` with a wrong password, then sign in.
2. Create and delete an API key, and create a user `eventtest` with only the
   User role.
3. As admin, filter to security: each appears with the address, and no entry
   contains the wrong password or the key's value (SC-002, SC-006).
4. As `eventtest`, the page, the API, and MCP show no security entries, and
   `category=security` is refused.

## 3. Failures and folding

1. Create an MQTT connection `Event test broken` to a closed port; it retries.
2. After a minute the log has a handful of entries for it with counts, not one
   per retry (SC-003).
3. Make a flow fail at a node; its entry links the run.

## 4. Flows and dashboards

1. A flow with Event Logged on category `connection` and a debug node fires
   once per folded entry from scenario 3.
2. A flow with Log Event writes "Event test holiday on"; it appears as
   `custom`.
3. A dashboard with an activity widget bound to `Event test level` shows the
   entries and a new one live; its public view refuses any other subject.
4. Browser check of the Activity page and the widget with screenshots and a
   console check.

## 5. Size

Insert a million synthetic rows into a copy of the database, list the newest
100 filtered by category and by subject, and time it (SC-004). A device under
`karakuri-test` publishing a reading every second for five minutes adds no
entries (SC-005).

## Cleanup

Delete the test control, flows, connection, device, user, API key, and
dashboard, clear the `karakuri-test` topics, and report what was created and
removed. Log entries about them remain, as they would for real objects.
