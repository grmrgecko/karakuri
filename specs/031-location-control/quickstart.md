# Quickstart: Location control kind

Prerequisites: `make dev`, `systemctl --user restart karakuri-dev.service`,
the MCP token from `./karakuri token list admin --name=mcp --raw` in
`build/`, and the browser logged in as `admin` / `password123`. Remove every
test object at the end and report them.

## 1. A location control over MCP

1. `save_control` a control "Test person" of kind `location`, with
   `restrict_zones` on and zones `home`, `not_home`, `work`.
2. `set_control` it to `"home"`. Expect `state: home` and a `since`.
3. `set_control` `{latitude: 41.88, longitude: -87.63, accuracy: 20}`.
   Expect `since` unchanged.
4. `set_control` `"gym"`. Expect a refusal naming the zones.
5. `set_control` `{latitude: 95}` and `{since: "2020-01-01T00:00:00Z"}`.
   Expect both refused.

## 2. Control Changed fires per state change

1. Create a flow: Control Changed on "Test person" with attribute `state` and
   "Ignore writes from flows" off, into a Debug node. Deploy and enable.
2. Over MCP, write `{state: work, latitude: 41.9}`, then
   `{latitude: 41.91}`, then `{state: home}`.
3. Expect two runs: `home` to `work`, then `work` to `home`, each with
   `attribute_value` and `attribute_old`.

## 3. OwnTracks over MQTT

1. Create an MQTT device of kind `location` on topic `owntracks/test/phone`
   with the OwnTracks script from `docs/mqtt-device-state-script.md`.
2. `publish_mqtt` a `transition` enter for `home`, then a `location`
   message, then a `transition` leave.
3. Expect `state` `home` with a `since`, then new coordinates with the same
   `since`, then `not_home` with a new `since`.

## 4. Fusion across a restart

1. Build the guide's fusion flow with the Trigger wait shortened to 2m and two
   boolean source controls.
2. Set one source home. Expect "Test person" `home` at once.
3. Set both away, restart the service within the two minutes, and expect
   `not_home` two minutes after the last away, with the resumed run linked in
   run history.
4. Set both away again and one home inside the wait. Expect no `not_home`.

## 5. Dashboards

1. On a test dashboard add an Entities Card with a `person` entity from the
   lab Home Assistant, the control, and the device. Turn on coordinates for
   one row.
2. Expect zone labels, "for" times, coordinates on the chosen row, and no
   press on the Home Assistant row.
3. Turn on `manual` for the control and pick `work` from the row.
4. Add a placement condition on the control, operator equals, value `home`,
   no attribute. Expect it shown when the control is `home` and hidden at
   `work`.
5. Take screenshots and check the console for errors.
