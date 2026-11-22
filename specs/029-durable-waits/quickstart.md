# Quickstart: Durable waits and device state

Build and run as `AGENTS.md` describes:

```sh
make dev
make test TARGET=server
make test TARGET=frontend
systemctl --user restart karakuri-dev.service
```

Use MCP at `http://localhost:8080/mcp` with the development token. Name test
flows `029 …` and report them afterwards. Each scenario takes a few minutes.

## 1. Trigger across a restart (US1, SC-001)

1. Create a flow: Inject → Trigger (`2m`, second payload `{"done":true}`) →
   Set control `029 mode`. Deploy and enable it.
2. Start it, wait 30 seconds, and run
   `systemctl --user restart karakuri-dev.service`.
3. Expect `GET /api/flows/{id}/waits` to list the Trigger after the restart
   with its original deadline.
4. Expect a run with `resumed_from_run_id` set to finish within five seconds
   of that deadline, and the control to change.
5. Repeat with a reset sent after the restart. Expect no delivery.

## 2. Missed deadlines (US2, SC-002)

1. Add two Delay nodes (`30s`): one with `missed_deadline: deliver`, one with
   `drop` and `missed_grace: 5s`. Start the flow.
2. `systemctl --user stop karakuri-dev.service`, wait 45 seconds, and start it.
3. Expect the deliver node to emit once with `meta.resumed.late_ms` above
   10000. Expect the drop node to leave one canceled run with a
   `missed deadline … dropped` error.

## 3. Redeploy (US3, SC-003)

1. Arm two Trigger nodes (`5m`) in one flow. Delete one in the draft and
   deploy.
2. Expect one wait listed, a canceled run for the deleted node, and one
   delivery at the original deadline.
3. Disable the flow. Expect the remaining wait gone and a `flow disabled` run.

## 4. Keyed volume (SC-004)

Send 100 messages with distinct `topic` keys through five keyed Trigger nodes
(`90s`), restart, and count deliveries per key from run history: exactly one
each.

## 5. Inspect and cancel (US4)

List waits with MCP `inspect_flow_state` (`store: "waits"`), cancel one with
`cancel_flow_wait`, and confirm it emits nothing and leaves a cancellation run.
In the editor, open the flow's pending waits panel, take a settled screenshot,
and check the console.

## 6. MQTT device state (US5, SC-005)

1. Publish a state message for a test device on the development broker.
2. Restart the service. Within ten seconds, expect `list_mqtt_devices` and the
   dashboard row to show the values with `stale: true` and `reported_at`.
3. Expect no `mqtt-device-changed` run from the restore.
4. Publish again. Expect `stale: false`.

## 7. Existing database (SC-006)

Before the first build, record `inspect_flow_state` output for a flow that uses
Dedupe and a control value. After the upgrade and restart, compare: they must
match.
