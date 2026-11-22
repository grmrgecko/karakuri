# Quickstart: reconnect catch-up, control handoff, and MQTT delivery

Prerequisites: `make dev`, `systemctl --user restart karakuri-dev.service`,
the MCP token. The lab Home Assistant is read only: no service calls.

## Reconnect catch-up

1. A test flow with `ha-state` on a few lab entities that change on their own
   (a sun or weather sensor, a clock-driven sensor), into a debug node; deploy
   and enable.
2. Disable the Home Assistant connection for a couple of minutes, read the
   states through Home Assistant's own API, enable the connection.
3. Expect one run per watched entity whose state differs from before the
   outage, each with `caught_up: true`, and none for the rest.
4. With a dashboard open on those entities, expect the changed values without a
   reload.

## Control handoff

1. A test number control; flow A (manual trigger) sets it; flow B, Control
   Changed on it with the default option, writes a debug message. Run A; B runs
   once.
2. Two test controls X and Y; flow C on X sets Y, flow D on Y sets X. Set X
   from MCP; expect the chain to stop at depth 8 with one skipped start whose
   reason names the chain.

## MQTT delivery

Test devices use the `karakuri-test` base topic on the lab broker, never
`zigbee2mqtt/`.

1. Create `Delivery test` on `karakuri-test/deliverytest/state` with command QoS
   1 and retain on; command it and capture the publish: QoS 1, retained.
2. A payload script returning `retain: false` for one field publishes that one
   without retain.
3. Publish a retained state, then save the device (resubscribing): the state
   script sees `retained` true. Publish a live state: `retained` false.
4. Set the state script limit to 5000 and give the script a 400 ms busy loop: it
   decodes; at the default it fails with a timeout.
5. Clear the retained topics with empty retained publishes.

## Cleanup

Delete the test flows, controls, and dashboard; report what was created.
