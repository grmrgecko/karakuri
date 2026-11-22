# Quickstart: Energy tracking

Prerequisites: `make dev`, `systemctl --user restart karakuri-dev.service`,
the MCP token. Live device checks publish only under the `karakuri-test` base
topic on the lab broker, never under `zigbee2mqtt/`; the lab Home Assistant
is read only.

## 1. Sources

1. Create an MQTT test device under `karakuri-test/plug1` with kind number and
   attribute `energy` in kWh; publish 100.0, then 100.5 a minute later.
2. `list_energy_candidates` lists it; `save_energy_source` adds it as a
   consumer, and a second test device reporting watts as a power source.
3. `get_energy_rollups target=source:<id> period=hour` shows 0.5 kWh in the
   current hour. Publish 0.2 (a reset) and see 0.2 added, state `reset`.
4. Restart the service mid-hour, publish 0.4, and see the hour hold 0.9 kWh.

## 2. Tariff

1. Save a time-of-use tariff with a period covering the current hour at 0.40
   and the rest of the week at 0.15, and a daily charge of 0.50; check the
   refusal for a tariff missing a weekend morning.
2. `get_energy_rollups measures=cost,total_cost period=day` gives
   `0.9 * 0.40` plus 0.50.
3. Edit the rate to 0.30 and see the day's cost change and usage stay.

## 3. Flows

1. `test_node energy-price mode=cheapest hours=3` returns a window in the
   0.15 period.
2. `test_node energy-usage target=house span=today compare_usual=true`.

## 4. Dashboard

1. `create_energy_dashboard`; open it in the browser, check the panels and the
   console.

## Cleanup

Delete the test devices, sources, tariff, and dashboard; clear the
`karakuri-test` topics; report what was created.
